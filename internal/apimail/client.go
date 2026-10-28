// Package apimail implements bounded read-only Gmail and Microsoft Graph access.
package apimail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"github.com/enjoydarts/cycrio/internal/mailsync"
)

const gmailBase = "https://gmail.googleapis.com/gmail/v1/users/me/"
const graphBase = "https://graph.microsoft.com/v1.0/me/"
const maxCursor = 128 << 10

type Client struct {
	provider, token string
	http            *http.Client
}

var _ mailaccounts.Checker = (*Client)(nil)
var _ mailsync.Scanner = (*Client)(nil)
var _ mailcontent.Reader = (*Client)(nil)

// cursor is private worker state. It can include mailbox IDs and must only be
// persisted encrypted, independently for historical and incremental discovery.
type cursor struct {
	Folder     string   `json:"f,omitempty"`
	Mode       string   `json:"m,omitempty"`
	Next       string   `json:"n,omitempty"`
	History    string   `json:"h,omitempty"`
	Checkpoint string   `json:"c,omitempty"`
	Fallback   bool     `json:"b,omitempty"`
	Pending    []string `json:"p,omitempty"`
	Visited    []string `json:"v,omitempty"`
}

// NewClient binds an access token to its fixed provider API. The supplied HTTP
// client is copied, with redirects disabled and a bounded timeout enforced.
func NewClient(provider, accessToken string, httpClient *http.Client) *Client {
	var h http.Client
	if httpClient != nil {
		h = *httpClient
	}
	h.Timeout = 30 * time.Second
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{provider: provider, token: accessToken, http: &h}
}
func failure(code string) error { return mailaccounts.AdapterError{Code: code} }
func validID(s string) bool {
	return s != "" && len(s) <= 2048 && !strings.ContainsAny(s, "/\\") && !strings.ContainsFunc(s, unicode.IsControl)
}
func (c *Client) isGmail() bool {
	return c.provider == "gmail" || c.provider == "google" || c.provider == "google_workspace" || c.provider == "gmail_workspace"
}
func (c *Client) valid() bool {
	return c.token != "" && (c.isGmail() || c.provider == "outlook" || c.provider == "microsoft")
}
func (c *Client) get(ctx context.Context, address string, max int) ([]byte, int, error) {
	if !c.valid() {
		return nil, 0, failure("invalid_response")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, 0, failure("invalid_response")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if !c.isGmail() {
		req.Header.Set("Prefer", `IdType="ImmutableId"`)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, failure("connection_failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		code := "connection_failed"
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			code = "authentication_failed"
		}
		if resp.StatusCode == 404 {
			code = "source_missing"
		}
		return nil, resp.StatusCode, failure(code)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(max)+1))
	if len(raw) > max {
		return nil, 200, failure("input_too_large")
	}
	if err != nil {
		return nil, 200, failure("connection_failed")
	}
	return raw, 200, nil
}
func (c *Client) getJSON(ctx context.Context, address string, out any) (int, error) {
	b, status, err := c.get(ctx, address, mailcontent.MaxWireBytes)
	if err != nil {
		return status, err
	}
	if json.Unmarshal(b, out) != nil {
		return status, failure("invalid_response")
	}
	return status, nil
}
func graphLink(link, path string) bool {
	u, e := url.Parse(link)
	return e == nil && len(link) <= 16384 && u.Scheme == "https" && u.Host == "graph.microsoft.com" && u.User == nil && u.Fragment == "" && u.Path == path
}
func pack(s cursor) (string, error) {
	b, e := json.Marshal(s)
	if e != nil || len(b) > maxCursor {
		return "", failure("invalid_response")
	}
	return string(b), nil
}

// advance bounds continuation chains and rejects cycles across worker calls.
func advance(s *cursor, next string) error {
	if next == "" {
		s.Next = ""
		s.Visited = nil
		return nil
	}
	if len(next) > 16384 || len(s.Visited) >= 1000 {
		return failure("invalid_response")
	}
	h := sha256.Sum256([]byte(next))
	key := hex.EncodeToString(h[:])
	for _, prior := range s.Visited {
		if prior == key {
			return failure("invalid_response")
		}
	}
	s.Visited = append(s.Visited, key)
	s.Next = next
	return nil
}
func (c *Client) Check(ctx context.Context, _ string, _ []byte) ([]mailaccounts.RemoteFolder, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out := []mailaccounts.RemoteFolder{}
	seen := map[string]bool{}
	if c.isGmail() {
		var v struct {
			Labels []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"labels"`
		}
		if _, e := c.getJSON(ctx, gmailBase+"labels", &v); e != nil {
			return nil, e
		}
		for _, f := range v.Labels {
			if !validID(f.ID) || seen[f.ID] || len(out) >= 1000 {
				return nil, failure("invalid_response")
			}
			seen[f.ID] = true
			out = append(out, mailaccounts.RemoteFolder{Name: f.ID, DisplayName: f.Name, Selectable: true})
		}
		return out, nil
	}
	paths := []string{"/v1.0/me/mailFolders"}
	links := map[string]bool{}
	for len(paths) > 0 {
		path := paths[0]
		paths = paths[1:]
		next := "https://graph.microsoft.com" + path + "?$top=100&includeHiddenFolders=false"
		for page := 0; next != "" && page < 20; page++ {
			if !graphLink(next, path) || links[next] {
				return nil, failure("invalid_response")
			}
			links[next] = true
			var v struct {
				Value []struct {
					ID          string `json:"id"`
					DisplayName string `json:"displayName"`
					ChildCount  int    `json:"childFolderCount"`
				} `json:"value"`
				Next string `json:"@odata.nextLink"`
			}
			if _, e := c.getJSON(ctx, next, &v); e != nil {
				return nil, e
			}
			for _, f := range v.Value {
				if !validID(f.ID) || seen[f.ID] || len(out) >= 1000 {
					return nil, failure("invalid_response")
				}
				seen[f.ID] = true
				out = append(out, mailaccounts.RemoteFolder{Name: f.ID, DisplayName: f.DisplayName, Selectable: true})
				if f.ChildCount > 0 {
					paths = append(paths, "/v1.0/me/mailFolders/"+url.PathEscape(f.ID)+"/childFolders")
				}
			}
			next = v.Next
		}
		if next != "" {
			return nil, failure("invalid_response")
		}
	}
	return out, nil
}

// Discover advances one provider page without downloading bodies or changing
// mailbox state. The legacy login/password arguments are intentionally unused.
func (c *Client) Discover(ctx context.Context, _ string, _ []byte, r mailsync.PageRequest) (mailsync.Page, error) {
	p := mailsync.Page{Initialized: true, RemoteIDs: []string{}}
	mode := r.Mode
	if mode == "" {
		mode = "manual"
	}
	if !validID(r.Folder) || r.Limit < 1 || r.Limit > 100 || len(r.Cursor) > maxCursor || (mode != "manual" && mode != "historical" && mode != "incremental" && mode != "bootstrap") {
		return p, failure("invalid_response")
	}
	if mode == "manual" || mode == "historical" {
		a, e := time.Parse(time.DateOnly, r.Since)
		b, f := time.Parse(time.DateOnly, r.Before)
		if e != nil || f != nil || !b.After(a) || b.Sub(a) > 366*24*time.Hour {
			return p, failure("invalid_response")
		}
	}
	var s cursor
	if r.Cursor != "" && json.Unmarshal([]byte(r.Cursor), &s) != nil {
		return p, failure("invalid_response")
	}
	if s.Folder != "" && s.Folder != r.Folder {
		return p, failure("invalid_response")
	}
	if s.Mode != mode {
		s.Next = ""
		s.Pending = nil
		s.Fallback = false
		s.Checkpoint = ""
		s.Visited = nil
	}
	s.Folder = r.Folder
	s.Mode = mode
	if c.isGmail() {
		return c.gmailDiscover(ctx, r, mode, s)
	}
	return c.graphDiscover(ctx, r, mode, s)
}
