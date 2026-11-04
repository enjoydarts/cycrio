package clerkdelete

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const clerkBaseURL = "https://api.clerk.com/v1"
const maxResponseBytes = 1 << 20

var subjectPattern = regexp.MustCompile(`^user_[A-Za-z0-9]{1,128}$`)
var instancePattern = regexp.MustCompile(`^ins_[A-Za-z0-9_-]{1,128}$`)

type Client struct{ http *http.Client }

func NewClient(client *http.Client) *Client {
	var copied http.Client
	if client != nil {
		copied = *client
	}
	if copied.Timeout <= 0 || copied.Timeout > 15*time.Second {
		copied.Timeout = 15 * time.Second
	}
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{http: &copied}
}

func validTarget(target Target, secret []byte) bool {
	_, validIssuer := frontendOrigin(target.Issuer)
	if !validIssuer || !subjectPattern.MatchString(target.Subject) || !instancePattern.MatchString(target.InstanceID) || len(secret) < 8 || len(secret) > 4096 {
		return false
	}
	for _, b := range secret {
		if b < 33 || b > 126 {
			return false
		}
	}
	return true
}

// A session token's issuer is its Clerk Frontend API URL. Only the URL's
// HTTPS origin is compared; application names, proxy URLs and allowed origins
// are not evidence that the backend key belongs to that issuer.
func frontendOrigin(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawPath != "" || u.Path != "" && u.Path != "/" || u.ForceQuery || u.RawQuery != "" || strings.ContainsAny(raw, "?#") {
		return "", false
	}
	host := strings.ToLower(u.Host)
	if u.Port() == "443" {
		host = strings.ToLower(u.Hostname())
	}
	return "https://" + host, true
}

func (c *Client) verifyIssuer(ctx context.Context, issuer string, secret []byte) Result {
	rsp, err := c.request(ctx, http.MethodGet, "/domains", secret)
	if err != nil {
		return transportResult(err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return statusResult(rsp)
	}
	var domains struct {
		Data []struct {
			FrontendAPIURL string `json:"frontend_api_url"`
		} `json:"data"`
		TotalCount *int `json:"total_count"`
	}
	if !decodeResponse(rsp, &domains) || domains.Data == nil || domains.TotalCount == nil || *domains.TotalCount != len(domains.Data) {
		return Result{Code: "invalid_response"}
	}
	expected, valid := frontendOrigin(issuer)
	if !valid {
		return Result{Code: "configuration_unavailable"}
	}
	matched := false
	for _, domain := range domains.Data {
		origin, valid := frontendOrigin(domain.FrontendAPIURL)
		if !valid {
			return Result{Code: "invalid_response"}
		}
		if origin == expected {
			matched = true
		}
	}
	if !matched {
		return Result{Code: "instance_mismatch"}
	}
	return Result{}
}

func retryAfter(value string) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		if seconds >= int64((24*time.Hour)/time.Second) {
			return 24 * time.Hour
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		delay := time.Until(when)
		if delay < 0 {
			return 0
		}
		if delay > 24*time.Hour {
			return 24 * time.Hour
		}
		return delay
	}
	return 0
}

func transportResult(err error) Result {
	if errors.Is(err, context.Canceled) {
		return Result{Code: "request_cancelled"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Result{Code: "timeout"}
	}
	return Result{Code: "provider_unavailable"}
}

func statusResult(rsp *http.Response) Result {
	switch rsp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return Result{Code: "authentication_failed"}
	case http.StatusTooManyRequests:
		return Result{Code: "rate_limited", RetryAfter: retryAfter(rsp.Header.Get("Retry-After"))}
	default:
		return Result{Code: "provider_unavailable"}
	}
}

func (c *Client) request(ctx context.Context, method, path string, secret []byte) (*http.Response, error) {
	r, err := http.NewRequestWithContext(ctx, method, clerkBaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+string(secret))
	r.Header.Set("Accept", "application/json")
	return c.http.Do(r)
}

func decodeResponse(rsp *http.Response, into any) bool {
	raw, err := io.ReadAll(io.LimitReader(rsp.Body, maxResponseBytes+1))
	return err == nil && len(raw) <= maxResponseBytes && json.Unmarshal(raw, into) == nil
}

func (c *Client) Delete(ctx context.Context, target Target, secret []byte) Result {
	if !validTarget(target, secret) {
		return Result{Code: "configuration_unavailable"}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rsp, err := c.request(ctx, http.MethodGet, "/instance", secret)
	if err != nil {
		return transportResult(err)
	}
	if rsp.StatusCode != http.StatusOK {
		result := statusResult(rsp)
		_ = rsp.Body.Close()
		return result
	}
	var instance struct {
		ID string `json:"id"`
	}
	valid := decodeResponse(rsp, &instance)
	_ = rsp.Body.Close()
	if !valid || instance.ID == "" {
		return Result{Code: "invalid_response"}
	}
	if instance.ID != target.InstanceID {
		return Result{Code: "instance_mismatch"}
	}
	if binding := c.verifyIssuer(ctx, target.Issuer, secret); binding.Code != "" {
		return binding
	}
	rsp, err = c.request(ctx, http.MethodDelete, "/users/"+url.PathEscape(target.Subject), secret)
	if err != nil {
		return transportResult(err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode == http.StatusNotFound {
		return Result{Deleted: true}
	}
	if rsp.StatusCode != http.StatusOK {
		return statusResult(rsp)
	}
	var deleted struct {
		Object  string `json:"object"`
		ID      string `json:"id"`
		Deleted bool   `json:"deleted"`
	}
	if !decodeResponse(rsp, &deleted) || deleted.Object != "user" || deleted.ID != target.Subject || !deleted.Deleted {
		return Result{Code: "invalid_response"}
	}
	return Result{Deleted: true}
}

// Keep arbitrary strings out of persisted retry diagnostics.
func safeResult(result Result) Result {
	if result.Deleted {
		return Result{Deleted: true}
	}
	switch result.Code {
	case "configuration_unavailable", "instance_mismatch", "authentication_failed", "rate_limited", "provider_unavailable", "invalid_response", "timeout", "request_cancelled":
	default:
		result.Code = "provider_unavailable"
	}
	if result.RetryAfter < 0 {
		result.RetryAfter = 0
	}
	if result.RetryAfter > 24*time.Hour {
		result.RetryAfter = 24 * time.Hour
	}
	return result
}
