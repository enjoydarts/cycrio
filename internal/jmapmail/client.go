// Package jmapmail implements bounded, read-only FranklyMail JMAP access.
package jmapmail

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"github.com/enjoydarts/cycrio/internal/maildiagnostics"
	"github.com/enjoydarts/cycrio/internal/mailsync"
)

const SessionURL = "https://jmap.franklymail.com/jmap/session"
const core = "urn:ietf:params:jmap:core"
const mail = "urn:ietf:params:jmap:mail"
const maxCursor = 128 << 10

type Client struct {
	login, password string
	http            *http.Client
}

var _ mailaccounts.Checker = (*Client)(nil)
var _ mailsync.Scanner = (*Client)(nil)
var _ mailcontent.Reader = (*Client)(nil)
var _ mailcontent.MessageReader = (*Client)(nil)

func NewClient(login string, password []byte, h *http.Client) *Client {
	var client http.Client
	if h != nil {
		client = *h
	}
	client.Timeout = 30 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{login: login, password: string(password), http: &client}
}
func failure(code string) error { return mailaccounts.AdapterError{Code: code} }
func valid(s string) bool {
	return s != "" && len(s) <= 2048 && !strings.ContainsFunc(s, unicode.IsControl)
}
func safeURL(s string) bool {
	u, e := url.Parse(s)
	return e == nil && len(s) <= 4096 && u.Scheme == "https" && (u.Host == "jmap.franklymail.com" || u.Host == "mx1.franklymail.com") && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.Path != ""
}
func (c *Client) diagnostic(code string, status int, headers http.Header, kind string, causes ...error) error {
	var body []byte
	// Only a fixed protocol type is passed to the generic diagnostic formatter.
	if kind != "" {
		body, _ = json.Marshal(map[string]any{"error": map[string]string{"type": kind}})
	}
	basic := base64.StdEncoding.EncodeToString([]byte(c.login + ":" + c.password))
	var cause error
	if len(causes) > 0 && causes[0] != nil {
		cause = safeTransportCause(causes[0])
	}
	return maildiagnostics.Failure(code, "franklymail", "jmap", status, headers, body, cause, c.login, c.password, basic, "Basic "+basic)
}

// Only fixed technical classifications and Go type names survive. Error()
// messages may contain URLs, credentials, or arbitrary transport echoes.
func safeTransportCause(err error) error {
	reason := "transport failed"
	var network net.Error
	var dns *net.DNSError
	var certificate *tls.CertificateVerificationError
	switch {
	case errors.Is(err, context.Canceled):
		reason = "request cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		reason = "deadline exceeded"
	case errors.As(err, &network) && network.Timeout():
		reason = "transport timeout"
	case errors.Is(err, syscall.ECONNREFUSED):
		reason = "connection refused"
	case errors.Is(err, syscall.ECONNRESET):
		reason = "connection reset"
	case errors.As(err, &dns):
		reason = "DNS lookup failed"
	case errors.As(err, &certificate):
		reason = "TLS certificate verification failed"
	case errors.Is(err, io.ErrUnexpectedEOF):
		reason = "unexpected end of response"
	case errors.Is(err, io.EOF):
		reason = "end of response"
	}
	types := []string{}
	for current, depth := err, 0; current != nil && depth < 16; current, depth = errors.Unwrap(current), depth+1 {
		types = append(types, fmt.Sprintf("%T", current))
	}
	return fmt.Errorf("%s (%s)", reason, strings.Join(types, " -> "))
}
func (c *Client) request(ctx context.Context, address string, payload any, out any) error {
	if !safeURL(address) || !valid(c.login) || c.password == "" {
		return failure("invalid_response")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var body io.Reader
	method := http.MethodGet
	if payload != nil {
		b, e := json.Marshal(payload)
		if e != nil {
			return failure("invalid_response")
		}
		body = bytes.NewReader(b)
		method = http.MethodPost
	}
	req, e := http.NewRequestWithContext(ctx, method, address, body)
	if e != nil {
		return failure("invalid_response")
	}
	req.SetBasicAuth(c.login, c.password)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, e := c.http.Do(req)
	if e != nil {
		return c.diagnostic("connection_failed", 0, nil, "", e)
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
		return c.diagnostic(code, resp.StatusCode, resp.Header, "")
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, mailcontent.MaxWireBytes+1))
	if len(raw) > mailcontent.MaxWireBytes {
		return c.diagnostic("input_too_large", 200, resp.Header, "")
	}
	if e != nil {
		return c.diagnostic("connection_failed", 200, resp.Header, "", e)
	}
	if !utf8.Valid(raw) || json.Unmarshal(raw, out) != nil {
		return c.diagnostic("invalid_response", 200, resp.Header, "")
	}
	return nil
}

type session struct {
	APIURL       string                     `json:"apiUrl"`
	Username     string                     `json:"username"`
	Capabilities map[string]json.RawMessage `json:"capabilities"`
	Primary      map[string]string          `json:"primaryAccounts"`
	Accounts     map[string]struct {
		Personal     bool                       `json:"isPersonal"`
		Capabilities map[string]json.RawMessage `json:"accountCapabilities"`
	} `json:"accounts"`
}

func (c *Client) session(ctx context.Context) (session, string, error) {
	var s session
	if e := c.request(ctx, SessionURL, nil, &s); e != nil {
		return s, "", e
	}
	if len(s.Accounts) == 0 {
		return s, "", c.diagnostic("authentication_failed", 200, nil, "")
	}
	id := s.Primary[mail]
	a, ok := s.Accounts[id]
	// The authenticated official session is authoritative. RFC 8620 allows
	// username to be empty or canonicalized; cursor ownership is bound to the
	// supplied login hash and primary account ID, not this display metadata.
	if !safeURL(s.APIURL) || !valid(id) || !ok || !a.Personal || s.Capabilities[core] == nil || s.Capabilities[mail] == nil || a.Capabilities[mail] == nil {
		return s, "", failure("invalid_response")
	}
	return s, id, nil
}

type methodError string

func (e methodError) Error() string { return "JMAP method failed" }
func (c *Client) call(ctx context.Context, s session, account, name string, args map[string]any, out any) error {
	switch name {
	case "Mailbox/get", "Email/get", "Email/query", "Email/changes":
	default:
		return failure("invalid_response")
	}
	args["accountId"] = account
	payload := map[string]any{"using": []string{core, mail}, "methodCalls": []any{[]any{name, args, "0"}}}
	var response struct {
		Responses [][]json.RawMessage `json:"methodResponses"`
	}
	if e := c.request(ctx, s.APIURL, payload, &response); e != nil {
		return e
	}
	if len(response.Responses) != 1 || len(response.Responses[0]) != 3 {
		return failure("invalid_response")
	}
	r := response.Responses[0]
	var got, tag string
	if json.Unmarshal(r[0], &got) != nil || json.Unmarshal(r[2], &tag) != nil || tag != "0" {
		return failure("invalid_response")
	}
	if got == "error" {
		var v struct {
			Type string `json:"type"`
		}
		json.Unmarshal(r[1], &v)
		switch v.Type {
		case "cannotCalculateChanges":
			return methodError(v.Type)
		case "accountNotFound", "notFound":
			return c.diagnostic("source_missing", 200, nil, v.Type)
		case "forbidden":
			return c.diagnostic("authentication_failed", 200, nil, v.Type)
		default:
			return c.diagnostic("invalid_response", 200, nil, "method_error")
		}
	}
	var bound struct {
		Account string `json:"accountId"`
	}
	if got != name || json.Unmarshal(r[1], &bound) != nil || bound.Account != account || json.Unmarshal(r[1], out) != nil {
		return failure("invalid_response")
	}
	return nil
}
func (c *Client) Check(ctx context.Context, _ string, _ []byte) ([]mailaccounts.RemoteFolder, error) {
	s, id, e := c.session(ctx)
	if e != nil {
		return nil, e
	}
	var v struct {
		List []struct {
			ID, Name string
			Rights   struct {
				Read bool `json:"mayReadItems"`
			} `json:"myRights"`
		} `json:"list"`
	}
	if e = c.call(ctx, s, id, "Mailbox/get", map[string]any{"properties": []string{"id", "name", "myRights"}}, &v); e != nil {
		return nil, e
	}
	if len(v.List) > 1000 {
		return nil, failure("folder_limit")
	}
	out := []mailaccounts.RemoteFolder{}
	seen := map[string]bool{}
	for _, f := range v.List {
		if !valid(f.ID) || !valid(f.Name) || seen[f.ID] {
			return nil, failure("invalid_response")
		}
		seen[f.ID] = true
		out = append(out, mailaccounts.RemoteFolder{Name: f.ID, DisplayName: f.Name, Selectable: f.Rights.Read})
	}
	return out, nil
}
