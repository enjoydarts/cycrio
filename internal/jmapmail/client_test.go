package jmapmail

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"github.com/enjoydarts/cycrio/internal/mailsync"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixture(t *testing.T, method func(string, map[string]any) any) *Client {
	t.Helper()
	return NewClient("user@example.test", []byte("app-secret"), &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "jmap.franklymail.com" {
			t.Fatal("unsafe origin")
		}
		u, p, ok := r.BasicAuth()
		if !ok || u != "user@example.test" || p != "app-secret" {
			t.Fatal("wrong bound credentials")
		}
		var out any
		if r.Method == "GET" {
			out = map[string]any{"username": "user@example.test", "apiUrl": "https://jmap.franklymail.com/jmap/api", "capabilities": map[string]any{core: map[string]any{}, mail: map[string]any{}}, "primaryAccounts": map[string]string{mail: "account"}, "accounts": map[string]any{"account": map[string]any{"isPersonal": true, "accountCapabilities": map[string]any{mail: map[string]any{}}}}}
		} else {
			var v struct {
				MethodCalls [][]json.RawMessage `json:"methodCalls"`
			}
			if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
				t.Fatal(err)
			}
			var name string
			var args map[string]any
			json.Unmarshal(v.MethodCalls[0][0], &name)
			json.Unmarshal(v.MethodCalls[0][1], &args)
			if args["accountId"] != "account" {
				t.Fatal("missing account")
			}
			result := method(name, args)
			if e, ok := result.(methodError); ok {
				name = "error"
				result = map[string]string{"type": string(e), "description": "private-mail app-secret"}
			}
			out = map[string]any{"methodResponses": []any{[]any{name, result, "0"}}}
		}
		b, _ := json.Marshal(out)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})})
}

func TestQueryChangeRestartsWithoutPromotingFallbackBaseline(t *testing.T) {
	queries := 0
	c := fixture(t, func(name string, a map[string]any) any {
		switch name {
		case "Email/query":
			queries++
			position := int(a["position"].(float64))
			if queries == 1 {
				return map[string]any{"accountId": "account", "queryState": "changed", "position": position, "ids": []string{"later"}}
			}
			if position != 0 {
				t.Fatal("must restart from start")
			}
			return map[string]any{"accountId": "account", "queryState": "stable", "position": 0, "ids": []string{"all"}}
		case "Email/get":
			return map[string]any{"accountId": "account", "state": "newest", "list": []any{map[string]any{"id": "all", "mailboxIds": map[string]bool{"inbox": true}}}}
		}
		t.Fatal(name)
		return nil
	})
	st := cursor{Version: 1, Account: "account", Login: hash(c.login), Folder: "inbox", Mode: "incremental", State: "expired", Checkpoint: "original-baseline", Fallback: true, Position: 100, Query: "old"}
	cur, _ := pack(st)
	p, e := c.Discover(context.Background(), "", nil, mailsync.PageRequest{Mode: "incremental", Folder: "inbox", Limit: 100, Cursor: cur})
	if e != nil || p.Done || len(p.RemoteIDs) != 0 || queries != 1 {
		t.Fatal("restart must stay within one candidate page", p, e, queries)
	}
	p, e = c.Discover(context.Background(), "", nil, mailsync.PageRequest{Mode: "incremental", Folder: "inbox", Limit: 100, Cursor: p.Cursor})
	if e != nil || !p.Done || fmt.Sprint(p.RemoteIDs) != "[all]" || queries != 2 {
		t.Fatalf("restart: %+v %v", p, e)
	}
	var got cursor
	json.Unmarshal([]byte(p.Cursor), &got)
	if got.State != "original-baseline" {
		t.Fatal("advanced baseline during restart")
	}
}

func TestReadMessageReceiptAndAttachmentExclusion(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       string
		bad        bool
	}{
		{"plain", `{"partId":"p","type":"text/plain"}`, "hello", false},
		{"attachment", `{"partId":"p","type":"text/plain","disposition":"attachment"}`, "", true},
		{"forwarded", `{"type":"message/rfc822","subParts":[{"partId":"p","type":"text/plain"}]}`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixture(t, func(name string, a map[string]any) any {
				if name != "Email/get" {
					t.Fatal(name)
				}
				var part any
				json.Unmarshal([]byte(tc.body), &part)
				return map[string]any{"accountId": "account", "list": []any{map[string]any{"id": "message", "mailboxIds": map[string]bool{"inbox": true}, "receivedAt": "2026-01-01T09:00:00+09:00", "bodyStructure": part, "textBody": []any{map[string]any{"partId": "p", "type": "text/plain"}}, "bodyValues": map[string]any{"p": map[string]any{"value": "hello"}}}}}
			})
			got, e := c.ReadMessage(context.Background(), "", nil, mailcontent.Source{Folder: "inbox", RemoteID: "message", MaxTextBytes: 100})
			if (e != nil) != tc.bad || string(got.Text) != tc.want || got.ReceivedAt == nil || got.ReceivedAt.Format("2006-01-02T15:04:05Z07:00") != "2026-01-01T00:00:00Z" {
				t.Fatalf("read: %+v %v", got, e)
			}
		})
	}
}

func TestSafeOrigin(t *testing.T) {
	for _, u := range []string{"http://jmap.franklymail.com/api", "https://evil.test/api", "https://user@jmap.franklymail.com/api", "https://jmap.franklymail.com/api?token=secret", "https://jmap.franklymail.com/api#x", "https://jmap.franklymail.com:443/api"} {
		if safeURL(u) {
			t.Fatal("accepted", u)
		}
	}
}
func TestBootstrapStoresStateWithoutImport(t *testing.T) {
	c := fixture(t, func(name string, a map[string]any) any {
		if name != "Email/get" {
			t.Fatalf("bootstrap called %s", name)
		}
		return map[string]any{"accountId": "account", "state": "baseline", "list": []any{}}
	})
	p, e := c.Discover(context.Background(), "ignored", nil, mailsync.PageRequest{Mode: "bootstrap", Folder: "inbox", Limit: 100})
	if e != nil || !p.Done || !p.Initialized || len(p.RemoteIDs) != 0 || !strings.Contains(p.Cursor, "baseline") {
		t.Fatalf("bootstrap: %+v %v", p, e)
	}
}
func TestHistoricalJSTAndMembership(t *testing.T) {
	c := fixture(t, func(name string, a map[string]any) any {
		switch name {
		case "Email/query":
			f := a["filter"].(map[string]any)
			if f["after"] != "2025-12-31T15:00:00Z" || f["before"] != "2026-01-31T15:00:00Z" {
				t.Fatalf("dates: %v", f)
			}
			return map[string]any{"accountId": "account", "queryState": "q", "position": 0, "ids": []string{"inside", "outside"}}
		case "Email/get":
			return map[string]any{"accountId": "account", "state": "s", "list": []any{map[string]any{"id": "inside", "mailboxIds": map[string]bool{"inbox": true}}, map[string]any{"id": "outside", "mailboxIds": map[string]bool{"other": true}}}}
		}
		t.Fatal(name)
		return nil
	})
	p, e := c.Discover(context.Background(), "", nil, mailsync.PageRequest{Mode: "historical", Folder: "inbox", Limit: 100, Since: "2026-01-01", Before: "2026-02-01"})
	if e != nil || !p.Done || fmt.Sprint(p.RemoteIDs) != "[inside]" {
		t.Fatalf("page: %+v %v", p, e)
	}
}
