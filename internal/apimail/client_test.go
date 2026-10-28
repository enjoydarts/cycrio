package apimail

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type rewriteTransport struct {
	target string
	base   http.RoundTripper
}

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = strings.TrimPrefix(r.target, "http://")
	return r.base.RoundTrip(clone)
}
func fixture(t *testing.T, provider string, handler http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return NewClient(provider, "secret", &http.Client{Transport: rewriteTransport{s.URL, http.DefaultTransport}})
}
func errorCode(err error) string {
	var a mailaccounts.AdapterError
	if errors.As(err, &a) {
		return a.Code
	}
	return ""
}

func TestGmailBootstrapHistoricalAndHistory(t *testing.T) {
	c := fixture(t, "gmail", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" || r.Method != "GET" {
			t.Error("read only authenticated request expected")
		}
		switch r.URL.Path {
		case "/gmail/v1/users/me/profile":
			fmt.Fprint(w, `{"historyId":"90"}`)
		case "/gmail/v1/users/me/messages":
			if r.URL.Query().Get("labelIds") != "INBOX" || !strings.Contains(r.URL.Query().Get("q"), "after:") {
				t.Error("missing label/date restriction")
			}
			fmt.Fprint(w, `{"messages":[{"id":"a"},{"id":"a"}],"nextPageToken":"next"}`)
		case "/gmail/v1/users/me/history":
			if r.URL.Query().Get("startHistoryId") != "90" {
				t.Error("checkpoint missing")
			}
			fmt.Fprint(w, `{"historyId":"99","history":[{"messagesAdded":[{"message":{"id":"b"}}]}]}`)
		default:
			t.Error(r.URL.Path)
		}
	})
	p, e := c.Discover(context.Background(), "", nil, mailsync.PageRequest{Mode: "bootstrap", Folder: "INBOX", Limit: 100})
	if e != nil || !p.Done || p.Cursor == "" {
		t.Fatalf("bootstrap: %+v %v", p, e)
	}
	h, e := c.Discover(context.Background(), "", nil, mailsync.PageRequest{Mode: "historical", Folder: "INBOX", Since: "2026-01-01", Before: "2026-02-01", Limit: 100, Cursor: p.Cursor})
	if e != nil || h.Done || len(h.RemoteIDs) != 1 {
		t.Fatalf("historical: %+v %v", h, e)
	}
	p, e = c.Discover(context.Background(), "", nil, mailsync.PageRequest{Mode: "incremental", Folder: "INBOX", Limit: 100, Cursor: p.Cursor})
	if e != nil || !p.Done || len(p.RemoteIDs) != 1 || p.RemoteIDs[0] != "b" {
		t.Fatalf("history: %+v %v", p, e)
	}
}

func TestGraphDeltaRejectsForeignNextLink(t *testing.T) {
	c := fixture(t, "outlook", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Prefer") != `IdType="ImmutableId"` {
			t.Error("immutable ID missing")
		}
		fmt.Fprint(w, `{"value":[],"@odata.nextLink":"https://evil.example/v1.0/me/mailFolders/inbox/messages/delta?x=1"}`)
	})
	_, e := c.Discover(context.Background(), "", nil, mailsync.PageRequest{Mode: "bootstrap", Folder: "inbox", Limit: 100})
	if errorCode(e) != "invalid_response" {
		t.Fatalf("unsafe next link accepted: %v", e)
	}
}
func TestReadTextExcludesAttachments(t *testing.T) {
	raw := "MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nbody\r\n--x\r\nContent-Type: text/plain\r\nContent-Disposition: attachment; filename=secret.txt\r\n\r\nattachment secret\r\n--x--\r\n"
	for _, provider := range []string{"gmail", "outlook"} {
		t.Run(provider, func(t *testing.T) {
			c := fixture(t, provider, func(w http.ResponseWriter, r *http.Request) {
				if provider == "gmail" {
					fmt.Fprintf(w, `{"labelIds":["inbox"],"raw":%q}`, base64.RawURLEncoding.EncodeToString([]byte(raw)))
				} else {
					if strings.HasSuffix(r.URL.Path, "/$value") {
						fmt.Fprint(w, raw)
					} else {
						fmt.Fprint(w, `{"parentFolderId":"inbox"}`)
					}
				}
			})
			got, e := c.ReadText(context.Background(), "", nil, mailcontent.Source{RemoteID: "a", Folder: "inbox", MaxTextBytes: 100})
			if e != nil || strings.TrimSpace(string(got)) != "body" {
				t.Fatalf("body=%q error=%v", got, e)
			}
		})
	}
}
func TestHTTPFailuresAreAnonymous(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			c := fixture(t, "gmail", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				fmt.Fprint(w, "secret server detail")
			})
			_, e := c.ReadText(context.Background(), "", nil, mailcontent.Source{RemoteID: "a", Folder: "inbox", MaxTextBytes: 100})
			want := "connection_failed"
			if status == 401 || status == 403 {
				want = "authentication_failed"
			}
			if status == 404 {
				want = "source_missing"
			}
			if errorCode(e) != want || strings.Contains(fmt.Sprint(e), "secret") {
				t.Fatalf("got %v %q", e, errorCode(e))
			}
		})
	}
}

func TestGmailExpiredHistoryRescansAndPreservesNewCheckpoint(t *testing.T) {
	calls := 0
	c := fixture(t, "gmail", func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/gmail/v1/users/me/history":
			if r.URL.Query().Get("startHistoryId") == "1" {
				w.WriteHeader(404)
			} else {
				fmt.Fprint(w, `{"historyId":"22"}`)
			}
		case "/gmail/v1/users/me/profile":
			fmt.Fprint(w, `{"historyId":"20"}`)
		case "/gmail/v1/users/me/messages":
			if r.URL.Query().Get("pageToken") == "" {
				fmt.Fprint(w, `{"messages":[{"id":"a"}],"nextPageToken":"page2"}`)
			} else {
				fmt.Fprint(w, `{"messages":[{"id":"b"}]}`)
			}
		}
	})
	s, _ := pack(cursor{Folder: "INBOX", Mode: "incremental", History: "1"})
	r := mailsync.PageRequest{Mode: "incremental", Folder: "INBOX", Limit: 100, Cursor: s}
	p, e := c.Discover(context.Background(), "", nil, r)
	if e != nil || p.Done || len(p.RemoteIDs) != 1 {
		t.Fatalf("fallback %+v %v", p, e)
	}
	r.Cursor = p.Cursor
	p, e = c.Discover(context.Background(), "", nil, r)
	if e != nil || !p.Done || p.RemoteIDs[0] != "b" {
		t.Fatalf("second %+v %v", p, e)
	}
	r.Cursor = p.Cursor
	p, e = c.Discover(context.Background(), "", nil, r)
	if e != nil || !p.Done || calls != 5 {
		t.Fatalf("resume %+v %v calls=%d", p, e, calls)
	}
}
func TestGraphBootstrapAdvancesEmptyPagesAndThenReadsChanges(t *testing.T) {
	c := fixture(t, "outlook", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("$skiptoken") {
		case "next":
			fmt.Fprint(w, `{"value":[{"id":"old"}],"@odata.deltaLink":"https://graph.microsoft.com/v1.0/me/mailFolders/inbox/messages/delta?$deltatoken=start"}`)
		default:
			if r.URL.Query().Get("$deltatoken") == "start" {
				fmt.Fprint(w, `{"value":[{"id":"new"},{"id":"deleted","@removed":{"reason":"deleted"}}],"@odata.deltaLink":"https://graph.microsoft.com/v1.0/me/mailFolders/inbox/messages/delta?$deltatoken=end"}`)
			} else {
				fmt.Fprint(w, `{"value":[],"@odata.nextLink":"https://graph.microsoft.com/v1.0/me/mailFolders/inbox/messages/delta?$skiptoken=next"}`)
			}
		}
	})
	r := mailsync.PageRequest{Mode: "bootstrap", Folder: "inbox", Limit: 100}
	p, e := c.Discover(context.Background(), "", nil, r)
	if e != nil || p.Done || p.Cursor == "" {
		t.Fatalf("page1 %+v %v", p, e)
	}
	r.Cursor = p.Cursor
	p, e = c.Discover(context.Background(), "", nil, r)
	if e != nil || !p.Done || len(p.RemoteIDs) != 0 {
		t.Fatalf("page2 %+v %v", p, e)
	}
	r.Cursor = p.Cursor
	r.Mode = "incremental"
	p, e = c.Discover(context.Background(), "", nil, r)
	if e != nil || len(p.RemoteIDs) != 1 || p.RemoteIDs[0] != "new" {
		t.Fatalf("changes %+v %v", p, e)
	}
}
func TestPaginationCycleIsRejected(t *testing.T) {
	c := fixture(t, "gmail", func(w http.ResponseWriter, r *http.Request) {
		next := "a"
		if r.URL.Query().Get("pageToken") == "a" {
			next = "b"
		}
		fmt.Fprintf(w, `{"nextPageToken":%q}`, next)
	})
	r := mailsync.PageRequest{Mode: "manual", Folder: "INBOX", Limit: 100, Since: "2026-01-01", Before: "2026-02-01"}
	for i := 0; i < 3; i++ {
		p, e := c.Discover(context.Background(), "", nil, r)
		if i == 2 {
			if errorCode(e) != "invalid_response" {
				t.Fatalf("pagination cycle accepted: %+v %v", p, e)
			}
			return
		}
		if e != nil {
			t.Fatal(e)
		}
		r.Cursor = p.Cursor
	}
}
func TestRedirectDoesNotSendBearerToOtherHost(t *testing.T) {
	leaked := false
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer evil.Close()
	c := fixture(t, "gmail", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, evil.URL, 302) })
	_, e := c.Check(context.Background(), "", nil)
	if e == nil || leaked {
		t.Fatalf("redirect followed: leak=%v err=%v", leaked, e)
	}
}
func TestGraphCheckIncludesNestedFolders(t *testing.T) {
	c := fixture(t, "outlook", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/me/mailFolders":
			fmt.Fprint(w, `{"value":[{"id":"root","childFolderCount":1}]}`)
		case "/v1.0/me/mailFolders/root/childFolders":
			fmt.Fprint(w, `{"value":[{"id":"child"}]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	folders, e := c.Check(context.Background(), "", nil)
	if e != nil || len(folders) != 2 || folders[1].Name != "child" {
		t.Fatalf("folders %+v %v", folders, e)
	}
}

func TestContentLimitsAndAlternativeSelection(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want, code string
		full                  bool
	}{
		{"alternative", "Content-Type: multipart/alternative; boundary=x\r\n\r\n--x\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<b>html</b>\r\n--x\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nplain\r\n--x--\r\n", "plain", "", false},
		{"embedded", "Content-Type: message/rfc822\r\n\r\nContent-Type: text/plain\r\n\r\nsecret", "", "unsupported_content", false},
		{"full", "Content-Type: text/plain; charset=utf-8\r\n\r\n" + strings.Repeat("x", 200), strings.Repeat("x", 200), "", true},
		{"legacy-limit", "Content-Type: text/plain; charset=utf-8\r\n\r\n" + strings.Repeat("x", 200), "", "input_too_large", false},
		{"wire-limit", strings.Repeat("x", mailcontent.MaxWireBytes+1), "", "input_too_large", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixture(t, "outlook", func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/$value") {
					fmt.Fprint(w, tc.raw)
				} else {
					fmt.Fprint(w, `{"parentFolderId":"inbox"}`)
				}
			})
			got, e := c.ReadText(context.Background(), "", nil, mailcontent.Source{RemoteID: "a", Folder: "inbox", MaxTextBytes: 100, FullEmail: tc.full})
			if errorCode(e) != tc.code || strings.TrimSpace(string(got)) != tc.want {
				t.Fatalf("got length=%d err=%v code=%s", len(got), e, errorCode(e))
			}
		})
	}
}

func TestGraphExpiredDeltaRescansWithoutDiscardingMessages(t *testing.T) {
	c := fixture(t, "outlook", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("$deltatoken") == "expired" {
			w.WriteHeader(410)
			return
		}
		fmt.Fprint(w, `{"value":[{"id":"recover"}],"@odata.deltaLink":"https://graph.microsoft.com/v1.0/me/mailFolders/inbox/messages/delta?$deltatoken=new"}`)
	})
	s, _ := pack(cursor{Folder: "inbox", Mode: "incremental", History: "https://graph.microsoft.com/v1.0/me/mailFolders/inbox/messages/delta?$deltatoken=expired"})
	p, e := c.Discover(context.Background(), "", nil, mailsync.PageRequest{Folder: "inbox", Mode: "incremental", Limit: 100, Cursor: s})
	if e != nil || !p.Done || len(p.RemoteIDs) != 1 || p.RemoteIDs[0] != "recover" {
		t.Fatalf("recovery %+v %v", p, e)
	}
}

func TestGraphLinksRejectOtherResourcesAndCredentials(t *testing.T) {
	for _, link := range []string{"https://graph.microsoft.com/v1.0/me/messages", "https://graph.microsoft.com:443/v1.0/me/mailFolders/inbox/messages/delta", "http://graph.microsoft.com/v1.0/me/mailFolders/inbox/messages/delta", "https://secret@graph.microsoft.com/v1.0/me/mailFolders/inbox/messages/delta", "https://graph.microsoft.com/v1.0/me/mailFolders/other/messages/delta"} {
		t.Run(link, func(t *testing.T) {
			calls := 0
			c := fixture(t, "outlook", func(w http.ResponseWriter, r *http.Request) {
				calls++
				fmt.Fprintf(w, `{"value":[],"@odata.nextLink":%q}`, link)
			})
			_, e := c.Discover(context.Background(), "", nil, mailsync.PageRequest{Mode: "bootstrap", Folder: "inbox", Limit: 100})
			if errorCode(e) != "invalid_response" || calls != 1 {
				t.Fatalf("unsafe link accepted calls=%d error=%v", calls, e)
			}
		})
	}
}

func TestGmailSelectedTrashIsNotSilentlyExcluded(t *testing.T) {
	c := fixture(t, "gmail", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("includeSpamTrash") != "true" {
			t.Error("selected spam/trash folder would be silently excluded")
		}
		fmt.Fprint(w, `{"messages":[{"id":"trash-message"}]}`)
	})
	p, e := c.Discover(context.Background(), "", nil, mailsync.PageRequest{Mode: "manual", Folder: "TRASH", Since: "2026-01-01", Before: "2026-02-01", Limit: 100})
	if e != nil || len(p.RemoteIDs) != 1 {
		t.Fatalf("trash %+v %v", p, e)
	}
}
