package jmapmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"github.com/enjoydarts/cycrio/internal/mailsync"
)

func TestTransportDiagnosticsKeepSafeFailureReasons(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cause  error
		reason string
	}{
		{"deadline", context.DeadlineExceeded, "deadline exceeded"},
		{"cancelled", context.Canceled, "request cancelled"},
		{"refused", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, "connection refused"},
		{"dns", &net.DNSError{Err: "PRIVATE BODY user@example.test app-secret", Name: "private.example.test"}, "DNS lookup failed"},
		{"unexpected", errors.New("PRIVATE BODY user@example.test app-secret"), "transport failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := NewClient("user@example.test", []byte("app-secret"), &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) { return nil, tc.cause })})
			_, e := c.Check(context.Background(), "", nil)
			if e == nil || errors.Unwrap(e) == nil {
				t.Fatal("missing diagnostic")
			}
			detail := errors.Unwrap(e).Error()
			if !strings.Contains(detail, tc.reason) || !strings.Contains(detail, "*url.Error") {
				t.Fatalf("safe transport context lost: %s", detail)
			}
			for _, secret := range []string{"PRIVATE BODY", "user@example.test", "app-secret", "private.example.test", base64.StdEncoding.EncodeToString([]byte("user@example.test:app-secret"))} {
				if strings.Contains(detail, secret) {
					t.Fatal("transport diagnostic disclosed private value", detail)
				}
			}
		})
	}
}

func TestHTTPFailuresNeverRetainProviderBodyOrTransportEcho(t *testing.T) {
	for _, status := range []int{301, 401, 403, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := NewClient("user@example.test", []byte("app-secret"), &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Header: http.Header{"X-Request-Id": []string{"request-123"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"description":"private-email-body","type":"app-secret"}}`))}, nil
			})})
			_, e := c.Check(context.Background(), "", nil)
			var a mailaccounts.AdapterError
			if !errors.As(e, &a) {
				t.Fatal(e)
			}
			if status == 401 || status == 403 {
				if a.Code != "authentication_failed" {
					t.Fatal(a.Code)
				}
			}
			detail := errors.Unwrap(e).Error()
			if !strings.Contains(detail, "request-123") || strings.Contains(detail, "private-email-body") || strings.Contains(detail, "app-secret") {
				t.Fatal("unsafe diagnostic", detail)
			}
		})
	}
	secret := base64.StdEncoding.EncodeToString([]byte("user@example.test:app-secret"))
	c := NewClient("user@example.test", []byte("app-secret"), &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) { return nil, errors.New("private-mail Basic " + secret) })})
	_, e := c.Check(context.Background(), "", nil)
	if strings.Contains(errors.Unwrap(e).Error(), secret) || strings.Contains(errors.Unwrap(e).Error(), "private-mail") {
		t.Fatal("transport error leaked")
	}
}

func TestRejectOversizeAndMalformedResponses(t *testing.T) {
	for _, body := range []string{strings.Repeat("x", mailcontent.MaxWireBytes+1), "{", string([]byte{'"', 0xff, '"'})} {
		c := NewClient("u", []byte("p"), &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})})
		if _, e := c.Check(context.Background(), "", nil); e == nil {
			t.Fatal("accepted invalid response")
		}
	}
}

func TestForeignAndMalformedCursorsRejected(t *testing.T) {
	c := fixture(t, func(string, map[string]any) any { t.Fatal("unexpected API call"); return nil })
	for _, cur := range []string{"{", "{}", `{"Version":1,"Folder":"other"}`, `{"Version":1,"Folder":"inbox","Login":"foreign"}`} {
		if _, e := c.Discover(context.Background(), "", nil, mailsync.PageRequest{Mode: "incremental", Folder: "inbox", Limit: 100, Cursor: cur}); e == nil {
			t.Fatal("accepted foreign cursor")
		}
	}
	st := cursor{Version: 1, Account: "foreign", Login: hash(c.login), Folder: "inbox", Mode: "incremental"}
	cur, _ := pack(st)
	if _, e := c.Discover(context.Background(), "", nil, mailsync.PageRequest{Mode: "incremental", Folder: "inbox", Limit: 100, Cursor: cur}); e == nil {
		t.Fatal("accepted foreign account")
	}
}

func TestReadRejectsMovedMessageAndInvalidBodyValues(t *testing.T) {
	for _, tc := range []struct {
		name               string
		box                string
		value              any
		truncated, problem bool
		want               string
	}{
		{name: "moved", box: "other", value: "body", want: "source_missing"},
		{name: "null", box: "inbox", want: "invalid_response"},
		{name: "truncated", box: "inbox", value: "body", truncated: true, want: "input_too_large"},
		{name: "encoding", box: "inbox", value: "body", problem: true, want: "invalid_response"},
		{name: "exact", box: "inbox", value: "four"},
		{name: "oversize", box: "inbox", value: "fives", want: "input_too_large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixture(t, func(string, map[string]any) any {
				return map[string]any{"accountId": "account", "list": []any{map[string]any{"id": "m", "mailboxIds": map[string]bool{tc.box: true}, "bodyStructure": map[string]any{"partId": "p", "type": "text/plain"}, "textBody": []any{map[string]string{"partId": "p"}}, "bodyValues": map[string]any{"p": map[string]any{"value": tc.value, "isTruncated": tc.truncated, "isEncodingProblem": tc.problem}}}}}
			})
			got, e := c.ReadMessage(context.Background(), "", nil, mailcontent.Source{RemoteID: "m", Folder: "inbox", MaxTextBytes: 4})
			if tc.want == "" {
				if e != nil || string(got.Text) != "four" {
					t.Fatal(got, e)
				}
				return
			}
			var a mailaccounts.AdapterError
			if !errors.As(e, &a) || a.Code != tc.want || got.Text != nil {
				t.Fatal("unexpected error", e, a.Code)
			}
		})
	}
}

func TestIncrementalFiltersUpdatedAndDestroyed(t *testing.T) {
	c := fixture(t, func(name string, a map[string]any) any {
		if name == "Email/changes" {
			if a["sinceState"] != "old" {
				t.Fatal("wrong state")
			}
			return map[string]any{"accountId": "account", "oldState": "old", "newState": "new", "hasMoreChanges": false, "created": []string{"created"}, "updated": []string{"moved"}, "destroyed": []string{"gone"}}
		}
		if name == "Email/get" {
			return map[string]any{"accountId": "account", "list": []any{map[string]any{"id": "created", "mailboxIds": map[string]bool{"inbox": true}}, map[string]any{"id": "moved", "mailboxIds": map[string]bool{"other": true}}}}
		}
		t.Fatal(name)
		return nil
	})
	st := cursor{Version: 1, Account: "account", Login: hash(c.login), Folder: "inbox", Mode: "bootstrap", State: "old"}
	cur, _ := pack(st)
	p, e := c.Discover(context.Background(), "", nil, mailsync.PageRequest{Mode: "incremental", Folder: "inbox", Limit: 100, Cursor: cur})
	if e != nil || !p.Done || len(p.RemoteIDs) != 1 || p.RemoteIDs[0] != "created" {
		t.Fatal(p, e)
	}
	var got cursor
	json.Unmarshal([]byte(p.Cursor), &got)
	if got.State != "new" {
		t.Fatal("lost state")
	}
}

func TestOfficialMx1APIAndAnonymousSession(t *testing.T) {
	for _, anonymous := range []bool{false, true} {
		calls := 0
		c := NewClient("user@example.test", []byte("app-secret"), &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			var body string
			if r.Method == "GET" {
				body = `{"username":"user@example.test","apiUrl":"https://mx1.franklymail.com/jmap/","capabilities":{"urn:ietf:params:jmap:core":{},"urn:ietf:params:jmap:mail":{}},"primaryAccounts":{"urn:ietf:params:jmap:mail":"account"},"accounts":{"account":{"isPersonal":true,"accountCapabilities":{"urn:ietf:params:jmap:mail":{}}}}}`
				if anonymous {
					body = `{"apiUrl":"https://mx1.franklymail.com/jmap/","accounts":{}}`
				}
			} else {
				if r.URL.Host != "mx1.franklymail.com" {
					t.Fatal("wrong API host")
				}
				body = `{"methodResponses":[["Mailbox/get",{"accountId":"account","list":[]},"0"]]}`
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})})
		_, e := c.Check(context.Background(), "", nil)
		if anonymous {
			var a mailaccounts.AdapterError
			if !errors.As(e, &a) || a.Code != "authentication_failed" || calls != 1 {
				t.Fatal("anonymous session", e, calls)
			}
		} else if e != nil || calls != 2 {
			t.Fatal("official API", e, calls)
		}
	}
}

func TestExpiredChangesFallbackKeepsPrescanBaseline(t *testing.T) {
	calls := 0
	c := fixture(t, func(name string, a map[string]any) any {
		calls++
		switch name {
		case "Email/changes":
			return methodError("cannotCalculateChanges")
		case "Email/query":
			return map[string]any{"accountId": "account", "queryState": "q", "position": 0, "ids": []string{"candidate"}}
		case "Email/get":
			if len(a["ids"].([]any)) == 0 {
				return map[string]any{"accountId": "account", "state": "before-scan", "list": []any{}}
			}
			return map[string]any{"accountId": "account", "state": "after-scan", "list": []any{map[string]any{"id": "candidate", "mailboxIds": map[string]bool{"inbox": true}}}}
		}
		t.Fatal(name)
		return nil
	})
	st := cursor{Version: 1, Account: "account", Login: hash(c.login), Folder: "inbox", Mode: "incremental", State: "expired"}
	cur, _ := pack(st)
	p, e := c.Discover(context.Background(), "", nil, mailsync.PageRequest{Mode: "incremental", Folder: "inbox", Limit: 100, Cursor: cur})
	var got cursor
	json.Unmarshal([]byte(p.Cursor), &got)
	if e != nil || !p.Done || got.State != "before-scan" || calls != 4 {
		t.Fatal("fallback lost baseline", p, e, calls)
	}
}

func TestMissingQueryIDsCannotAdvanceCursor(t *testing.T) {
	c := fixture(t, func(name string, a map[string]any) any {
		if name != "Email/query" {
			t.Fatal(name)
		}
		return map[string]any{"accountId": "account", "queryState": "q", "position": 0}
	})
	_, e := c.Discover(context.Background(), "", nil, mailsync.PageRequest{Mode: "historical", Folder: "inbox", Limit: 100, Since: "2026-01-01", Before: "2026-02-01"})
	if e == nil {
		t.Fatal("missing IDs accepted as empty result")
	}
}

func TestRepeatedQueryPageRejected(t *testing.T) {
	c := fixture(t, func(name string, a map[string]any) any {
		if name == "Email/query" {
			return map[string]any{"accountId": "account", "queryState": "q", "position": a["position"], "ids": []string{"duplicate"}}
		}
		return map[string]any{"accountId": "account", "list": []any{map[string]any{"id": "duplicate", "mailboxIds": map[string]bool{"inbox": true}}}}
	})
	r := mailsync.PageRequest{Mode: "historical", Folder: "inbox", Limit: 1, Since: "2026-01-01", Before: "2026-02-01"}
	p, e := c.Discover(context.Background(), "", nil, r)
	if e != nil || p.Done {
		t.Fatal(p, e)
	}
	r.Cursor = p.Cursor
	if _, e = c.Discover(context.Background(), "", nil, r); e == nil {
		t.Fatal("repeated page accepted")
	}
}

func TestMutationMethodsAreRejected(t *testing.T) {
	c := fixture(t, func(string, map[string]any) any { t.Fatal("mutation reached network"); return nil })
	e := c.call(context.Background(), session{APIURL: "https://mx1.franklymail.com/jmap/"}, "account", "Email/set", map[string]any{}, &struct{}{})
	if e == nil {
		t.Fatal("mutation accepted")
	}
}

func TestChangesPaginationAndFallbackDeliveriesDuringScan(t *testing.T) {
	queries := 0
	changes := 0
	c := fixture(t, func(name string, a map[string]any) any {
		switch name {
		case "Email/changes":
			changes++
			if changes == 1 {
				return methodError("cannotCalculateChanges")
			}
			if a["sinceState"] != "baseline" {
				t.Fatal("delivery checkpoint skipped", a)
			}
			return map[string]any{"accountId": "account", "oldState": "baseline", "newState": "arrived-during-scan", "hasMoreChanges": false, "created": []string{"new"}, "updated": []string{}, "destroyed": []string{}}
		case "Email/query":
			queries++
			ids := []string{}
			if queries == 1 {
				ids = []string{"old"}
			}
			return map[string]any{"accountId": "account", "queryState": "stable", "position": a["position"], "ids": ids}
		case "Email/get":
			ids := a["ids"].([]any)
			if len(ids) == 0 {
				return map[string]any{"accountId": "account", "state": "baseline", "list": []any{}}
			}
			return map[string]any{"accountId": "account", "state": "arrived-during-scan", "list": []any{map[string]any{"id": ids[0], "mailboxIds": map[string]bool{"inbox": true}}}}
		}
		t.Fatal(name)
		return nil
	})
	st := cursor{Version: 1, Account: "account", Login: hash(c.login), Folder: "inbox", Mode: "incremental", State: "expired"}
	cur, _ := pack(st)
	r := mailsync.PageRequest{Mode: "incremental", Folder: "inbox", Limit: 1, Cursor: cur}
	p, e := c.Discover(context.Background(), "", nil, r)
	if e != nil || p.Done || len(p.RemoteIDs) != 1 {
		t.Fatal(p, e)
	}
	var pending cursor
	json.Unmarshal([]byte(p.Cursor), &pending)
	if pending.State != "expired" || pending.Checkpoint != "baseline" {
		t.Fatal("baseline promoted too early")
	}
	r.Cursor = p.Cursor
	p, e = c.Discover(context.Background(), "", nil, r)
	if e != nil || !p.Done {
		t.Fatal(p, e)
	}
	r.Cursor = p.Cursor
	p, e = c.Discover(context.Background(), "", nil, r)
	if e != nil || !p.Done || len(p.RemoteIDs) != 1 || p.RemoteIDs[0] != "new" {
		t.Fatal("concurrent delivery lost", p, e)
	}
}

func TestMethodResponseMustStayInSessionAccount(t *testing.T) {
	c := fixture(t, func(name string, a map[string]any) any {
		return map[string]any{"accountId": "foreign", "list": []any{}}
	})
	if _, e := c.Check(context.Background(), "", nil); e == nil {
		t.Fatal("foreign response accepted")
	}
}

func TestServerClampedQueryLimitKeepsFallbackBaselineUntilEmptyPage(t *testing.T) {
	calls := 0
	c := fixture(t, func(name string, a map[string]any) any {
		switch name {
		case "Email/query":
			calls++
			ids := []string{}
			if calls == 1 {
				ids = []string{"first"}
			}
			return map[string]any{"accountId": "account", "queryState": "q", "position": a["position"], "limit": 1, "ids": ids}
		case "Email/get":
			return map[string]any{"accountId": "account", "list": []any{map[string]any{"id": "first", "mailboxIds": map[string]bool{"inbox": true}}}}
		}
		t.Fatal(name)
		return nil
	})
	st := cursor{Version: 1, Account: "account", Login: hash(c.login), Folder: "inbox", Mode: "incremental", State: "expired", Checkpoint: "baseline", Fallback: true}
	cur, _ := pack(st)
	r := mailsync.PageRequest{Mode: "incremental", Folder: "inbox", Limit: 100, Cursor: cur}
	p, e := c.Discover(context.Background(), "", nil, r)
	if e != nil || p.Done || len(p.RemoteIDs) != 1 {
		t.Fatal("server limit completed early", p, e)
	}
	var pending cursor
	json.Unmarshal([]byte(p.Cursor), &pending)
	if pending.State != "expired" || pending.Checkpoint != "baseline" {
		t.Fatal("baseline prematurely advanced")
	}
	r.Cursor = p.Cursor
	p, e = c.Discover(context.Background(), "", nil, r)
	if e != nil || !p.Done {
		t.Fatal("empty final page", p, e)
	}
	var done cursor
	json.Unmarshal([]byte(p.Cursor), &done)
	if done.State != "baseline" {
		t.Fatal("baseline lost")
	}
}

func TestReadRejectsMultipleInlinePreferredBodies(t *testing.T) {
	c := fixture(t, func(name string, a map[string]any) any {
		parts := []any{map[string]any{"partId": "first", "type": "text/plain"}, map[string]any{"partId": "second", "type": "text/plain"}}
		return map[string]any{"accountId": "account", "list": []any{map[string]any{"id": "m", "mailboxIds": map[string]bool{"inbox": true}, "bodyStructure": map[string]any{"type": "multipart/mixed", "subParts": parts}, "textBody": parts, "bodyValues": map[string]any{"first": map[string]any{"value": "first"}, "second": map[string]any{"value": "second"}}}}}
	})
	m, e := c.ReadMessage(context.Background(), "", nil, mailcontent.Source{RemoteID: "m", Folder: "inbox", MaxTextBytes: 100})
	var a mailaccounts.AdapterError
	if !errors.As(e, &a) || a.Code != "unsupported_content" || m.Text != nil {
		t.Fatal("partial multipart body accepted", m, e)
	}
}

type interruptedResponse struct{}

func (interruptedResponse) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (interruptedResponse) Close() error             { return nil }
func TestInterruptedResponseRetainsSafeTransportReason(t *testing.T) {
	c := NewClient("user@example.test", []byte("app-secret"), &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: interruptedResponse{}}, nil
	})})
	_, e := c.Check(context.Background(), "", nil)
	if e == nil || errors.Unwrap(e) == nil || !strings.Contains(errors.Unwrap(e).Error(), "unexpected end of response") {
		t.Fatal("read failure lost", e)
	}
}

func TestSessionUsernameMayBeCanonicalAliasOrEmpty(t *testing.T) {
	for _, username := range []string{"USER@example.test", "canonical@example.test", ""} {
		t.Run(username, func(t *testing.T) {
			c := NewClient("user@example.test", []byte("app-secret"), &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				var payload any
				if r.Method == http.MethodGet {
					payload = map[string]any{"username": username, "apiUrl": "https://mx1.franklymail.com/jmap/", "capabilities": map[string]any{core: map[string]any{}, mail: map[string]any{}}, "primaryAccounts": map[string]string{mail: "account"}, "accounts": map[string]any{"account": map[string]any{"isPersonal": true, "accountCapabilities": map[string]any{mail: map[string]any{}}}}}
				} else {
					payload = map[string]any{"methodResponses": []any{[]any{"Mailbox/get", map[string]any{"accountId": "account", "list": []any{}}, "0"}}}
				}
				body, _ := json.Marshal(payload)
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})})
			if _, e := c.Check(context.Background(), "", nil); e != nil {
				t.Fatal("RFC session username rejected", e)
			}
		})
	}
}
