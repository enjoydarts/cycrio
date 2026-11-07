package healthchecks

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestHTTPFailuresRetainOnlySafeStageAndCategory(t *testing.T) {
	for _, stage := range []string{"api_probe", "healthchecks_post"} {
		for _, tc := range []struct {
			name, category string
			err            error
			status         int
			body           string
		}{
			{"timeout", "timeout", &url.Error{Op: "Post", URL: testURL, Err: context.DeadlineExceeded}, 0, ""},
			{"cancelled", "cancelled", context.Canceled, 0, ""},
			{"network", "network", errors.New("credential secret private-token"), 0, ""},
			{"status", "http_status", nil, 503, "credential secret private-token"},
			{"invalid", "invalid_response", nil, 200, "private-token"},
		} {
			t.Run(stage+"/"+tc.name, func(t *testing.T) {
				client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if tc.err != nil {
						return nil, tc.err
					}
					return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{}, Request: r}, nil
				})}
				var err error
				if stage == "api_probe" {
					err = NewAPIProbe(client)(context.Background())
				} else {
					err = NewClient(client).Ping(context.Background(), testURL)
				}
				assertSafeFailure(t, err, stage, tc.category)
				if tc.status == 503 && !strings.Contains(errors.Unwrap(err).Error(), "status=503") {
					t.Fatalf("HTTP status missing: %v", errors.Unwrap(err))
				}
			})
		}
	}
}

const testURL = "https://hc-ping.com/11111111-1111-4111-8111-111111111111"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPingURLIsRestrictedToOfficialUUIDEndpoint(t *testing.T) {
	if !ValidPingURL(testURL) {
		t.Fatal("valid URL rejected")
	}
	for _, bad := range []string{"http" + strings.TrimPrefix(testURL, "https"), testURL + "/fail", testURL + "?create=1", testURL + "#secret", strings.Replace(testURL, "hc-ping.com", "hc-ping.com.example.com", 1), strings.Replace(testURL, "hc-ping.com", "hc-ping.com:443", 1), strings.Replace(testURL, "hc-ping.com", "user@hc-ping.com", 1), "https://127.0.0.1/test", "https://hc-ping.com/not-a-uuid", testURL + "/", strings.Replace(testURL, "/111", "/%3111", 1)} {
		if ValidPingURL(bad) {
			t.Error("unsafe URL accepted", bad)
		}
	}
}

func TestPingSendsEmptyPostAndRejectsFalseSuccessWithoutRedirect(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		ok     bool
	}{
		{200, "OK", true}, {200, "OK\n", true}, {200, "OK (not found)", false}, {200, "OK (rate limited)", false}, {302, "OK", false}, {500, "OK", false}, {200, strings.Repeat("x", 2048), false},
	} {
		calls := 0
		c := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Method != "POST" || r.URL.String() != testURL || r.Header.Get("Authorization") != "" || r.ContentLength != 0 {
				t.Error("unexpected request", r.Method, r.Header, r.ContentLength)
			}
			if r.Body != nil {
				body, _ := io.ReadAll(r.Body)
				if len(body) != 0 {
					t.Error("body must be empty")
				}
			}
			h := http.Header{}
			h.Set("Location", "https://other.example/secret")
			return &http.Response{StatusCode: tc.status, Header: h, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
		})})
		err := c.Ping(context.Background(), testURL)
		if (err == nil) != tc.ok || calls != 1 {
			t.Fatal(tc, err, calls)
		}
		if err != nil && strings.Contains(err.Error(), "11111111") {
			t.Fatal("URL leaked in error", err)
		}
	}
}

func TestAPIProbeRequiresOKAndRejectsRedirect(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		ok     bool
	}{{200, "ok\n", true}, {200, "not ready", false}, {503, "not ready", false}, {302, "ok", false}} {
		calls := 0
		check := NewAPIProbe(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.String() != "http://api:8080/readyz" || r.Method != "GET" {
				t.Error(r.URL, r.Method)
			}
			return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": []string{"http://other.example/"}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
		})})
		if err := check(context.Background()); (err == nil) != tc.ok || calls != 1 {
			t.Fatal(tc, err, calls)
		}
	}
}
