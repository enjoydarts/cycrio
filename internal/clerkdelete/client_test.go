package clerkdelete

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (fn roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

var target = Target{Issuer: "https://synthetic.clerk.accounts.dev", Subject: "user_synthetic", InstanceID: "ins_synthetic"}

func TestClientVerifiesInstanceBeforeDeletingOnlyRequestedUser(t *testing.T) {
	var requests []string
	client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.String())
		if r.Header.Get("Authorization") != "Bearer sk_test_synthetic" {
			t.Fatal("missing private authorization")
		}
		if until, ok := r.Context().Deadline(); !ok || time.Until(until) > 15*time.Second {
			t.Fatal("request has no bounded timeout")
		}
		if r.Method == "GET" {
			if r.URL.Path == "/v1/domains" {
				return response(200, `{"data":[{"frontend_api_url":"https://synthetic.clerk.accounts.dev"}],"total_count":1}`), nil
			}
			return response(200, `{"id":"ins_synthetic","object":"instance"}`), nil
		}
		return response(200, `{"object":"user","id":"user_synthetic","deleted":true}`), nil
	})})
	got := client.Delete(context.Background(), target, []byte("sk_test_synthetic"))
	if !got.Deleted || got.Code != "" || len(requests) != 3 || requests[0] != "GET https://api.clerk.com/v1/instance" || requests[1] != "GET https://api.clerk.com/v1/domains" || requests[2] != "DELETE https://api.clerk.com/v1/users/user_synthetic" {
		t.Fatal(got, requests)
	}
}

func TestClientFailureNeverCompletesDeletionOrLeaksProviderData(t *testing.T) {
	for _, tt := range []struct {
		name         string
		getStatus    int
		getBody      string
		deleteStatus int
		deleteBody   string
		code         string
		wantDelete   bool
	}{
		{"wrong instance", 200, `{"id":"ins_other"}`, 200, `{}`, "instance_mismatch", false},
		{"invalid instance", 200, `{"id":123}`, 200, `{}`, "invalid_response", false},
		{"missing instance", 404, `private response`, 200, `{}`, "provider_unavailable", false},
		{"unauthorized", 401, `private response`, 200, `{}`, "authentication_failed", false},
		{"delete forbidden", 200, `{"id":"ins_synthetic"}`, 403, `private response`, "authentication_failed", true},
		{"delete fails", 200, `{"id":"ins_synthetic"}`, 500, `private response`, "provider_unavailable", true},
		{"invalid ack", 200, `{"id":"ins_synthetic"}`, 200, `{"object":"user","id":"user_other","deleted":true}`, "invalid_response", true},
		{"not deleted", 200, `{"id":"ins_synthetic"}`, 200, `{"object":"user","id":"user_synthetic","deleted":false}`, "invalid_response", true},
		{"wrong object", 200, `{"id":"ins_synthetic"}`, 200, `{"object":"organization","id":"user_synthetic","deleted":true}`, "invalid_response", true},
		{"unexpected success", 200, `{"id":"ins_synthetic"}`, 204, "", "provider_unavailable", true},
		{"oversized body", 200, strings.Repeat(" ", 1<<20) + `{}`, 200, `{}`, "invalid_response", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			deletes := 0
			client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				if r.Method == "GET" {
					if r.URL.Path == "/v1/domains" {
						return response(200, `{"data":[{"frontend_api_url":"https://synthetic.clerk.accounts.dev"}],"total_count":1}`), nil
					}
					return response(tt.getStatus, tt.getBody), nil
				}
				deletes++
				return response(tt.deleteStatus, tt.deleteBody), nil
			})})
			got := client.Delete(context.Background(), target, []byte("sk_test_synthetic"))
			if got.Deleted || got.Code != tt.code || (deletes == 1) != tt.wantDelete {
				t.Fatal(got, deletes)
			}
		})
	}
}

func TestClientAlreadyAbsentIsSuccessOnlyAfterValidInstance(t *testing.T) {
	client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			if r.URL.Path == "/v1/domains" {
				return response(200, `{"data":[{"frontend_api_url":"https://synthetic.clerk.accounts.dev"}],"total_count":1}`), nil
			}
			return response(200, `{"id":"ins_synthetic"}`), nil
		}
		return response(404, `private`), nil
	})})
	if got := client.Delete(context.Background(), target, []byte("sk_test_synthetic")); !got.Deleted || got.Code != "" {
		t.Fatal(got)
	}
}

func TestClientRequiresIssuerBindingEvenWhenWrongInstanceAndKeyAgree(t *testing.T) {
	deletes := 0
	wrong := target
	wrong.InstanceID = "ins_other"
	client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v1/instance" {
			return response(200, `{"id":"ins_other"}`), nil
		}
		if r.URL.Path == "/v1/domains" {
			return response(200, `{"data":[{"frontend_api_url":"https://other.clerk.accounts.dev"}],"total_count":1}`), nil
		}
		deletes++
		return response(404, `private`), nil
	})})
	got := client.Delete(context.Background(), wrong, []byte("sk_test_other"))
	if got.Deleted || got.Code != "instance_mismatch" || deletes != 0 {
		t.Fatal("wrong Clerk instance falsely completed withdrawal", got, deletes)
	}
}

func TestClientDomainsMustBeValidAndContainActualFrontendOrigin(t *testing.T) {
	for _, tt := range []struct {
		name       string
		status     int
		body, code string
	}{
		{"missing data", 200, `{}`, "invalid_response"},
		{"invalid data", 200, `{"data":123}`, "invalid_response"},
		{"invalid domain", 200, `{"data":[{"frontend_api_url":123}],"total_count":1}`, "invalid_response"},
		{"null domains", 200, `{"data":null,"total_count":0}`, "invalid_response"},
		{"unauthorized", 401, `private`, "authentication_failed"},
		{"throttled", 429, `private`, "rate_limited"},
		{"missing endpoint", 404, `private`, "provider_unavailable"},
		{"provider fails", 500, `private`, "provider_unavailable"},
		{"redirect", 302, `private`, "provider_unavailable"},
		{"empty domain list", 200, `{"data":[],"total_count":0}`, "instance_mismatch"},
		{"proxy is not issuer binding", 200, `{"data":[{"frontend_api_url":"https://other.example","proxy_url":"https://synthetic.clerk.accounts.dev"}],"total_count":1}`, "instance_mismatch"},
		{"userinfo spoof", 200, `{"data":[{"frontend_api_url":"https://synthetic.clerk.accounts.dev@other.example"}],"total_count":1}`, "invalid_response"},
		{"path spoof", 200, `{"data":[{"frontend_api_url":"https://synthetic.clerk.accounts.dev/other"}],"total_count":1}`, "invalid_response"},
		{"encoded path spoof", 200, `{"data":[{"frontend_api_url":"https://synthetic.clerk.accounts.dev/%2f"}],"total_count":1}`, "invalid_response"},
		{"query spoof", 200, `{"data":[{"frontend_api_url":"https://synthetic.clerk.accounts.dev?"}],"total_count":1}`, "invalid_response"},
		{"fragment spoof", 200, `{"data":[{"frontend_api_url":"https://synthetic.clerk.accounts.dev#private"}],"total_count":1}`, "invalid_response"},
		{"http spoof", 200, `{"data":[{"frontend_api_url":"http://synthetic.clerk.accounts.dev"}],"total_count":1}`, "invalid_response"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			deletes := 0
			client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/v1/instance" {
					return response(200, `{"id":"ins_synthetic"}`), nil
				}
				if r.URL.Path == "/v1/domains" {
					rsp := response(tt.status, tt.body)
					rsp.Header.Set("Retry-After", "120")
					return rsp, nil
				}
				deletes++
				return response(404, `private`), nil
			})})
			got := client.Delete(context.Background(), target, []byte("sk_test_synthetic"))
			if got.Deleted || got.Code != tt.code || deletes != 0 {
				t.Fatal(got, deletes)
			}
			if tt.status == 429 && got.RetryAfter != 2*time.Minute {
				t.Fatal("domain retry-after ignored", got)
			}
		})
	}
}

func TestClientAcceptsSatelliteFrontendOriginButNeverRequestsIt(t *testing.T) {
	client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.clerk.com" {
			t.Fatal("accessed provider-supplied domain", r.URL.Host)
		}
		switch r.URL.Path {
		case "/v1/instance":
			return response(200, `{"id":"ins_synthetic"}`), nil
		case "/v1/domains":
			return response(200, `{"data":[{"frontend_api_url":"https://primary.example","is_satellite":false},{"frontend_api_url":"https://SYNTHETIC.clerk.accounts.dev:443/","is_satellite":true}],"total_count":2}`), nil
		default:
			return response(404, `private`), nil
		}
	})})
	if got := client.Delete(context.Background(), target, []byte("sk_test_synthetic")); !got.Deleted {
		t.Fatal(got)
	}
}

func TestClientRateLimitRetryAfterAndUnsafeInputs(t *testing.T) {
	for _, value := range []string{"120", time.Now().Add(2 * time.Minute).UTC().Format(http.TimeFormat), "999999999999999999999", "-1", "invalid"} {
		client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			rsp := response(429, `private`)
			rsp.Header.Set("Retry-After", value)
			return rsp, nil
		})})
		got := client.Delete(context.Background(), target, []byte("sk_test_synthetic"))
		if got.Deleted || got.Code != "rate_limited" || got.RetryAfter < 0 || got.RetryAfter > 24*time.Hour {
			t.Fatal(got)
		}
		if value == "120" && got.RetryAfter != 2*time.Minute {
			t.Fatal("retry-after ignored", got)
		}
	}
	for _, invalid := range []Target{{}, {Issuer: target.Issuer, InstanceID: target.InstanceID, Subject: "../user_other"}, {Issuer: target.Issuer, InstanceID: "other", Subject: target.Subject}} {
		client := NewClient(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			t.Fatal("external request for invalid target")
			return nil, nil
		})})
		if got := client.Delete(context.Background(), invalid, []byte("sk_test_synthetic")); got.Deleted || got.Code != "configuration_unavailable" {
			t.Fatal(got)
		}
	}
	client := NewClient(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		t.Fatal("external request with missing or malformed key")
		return nil, nil
	})})
	for _, key := range [][]byte{nil, []byte("private\r\nkey")} {
		if got := client.Delete(context.Background(), target, key); got.Deleted || got.Code != "configuration_unavailable" {
			t.Fatal(got)
		}
	}
}

func TestClientForbidsRedirectsAndSanitizesTransportFailures(t *testing.T) {
	calls := 0
	client := NewClient(&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { t.Fatal("caller redirect hook used"); return nil }, Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		rsp := response(302, `private`)
		rsp.Header.Set("Location", "https://other.example/secret")
		return rsp, nil
	})})
	if got := client.Delete(context.Background(), target, []byte("sk_test_synthetic")); got.Deleted || got.Code != "provider_unavailable" || calls != 1 {
		t.Fatal(got, calls)
	}
	for _, err := range []error{errors.New("private endpoint/key/subject"), context.DeadlineExceeded, context.Canceled} {
		client = NewClient(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return nil, err })})
		got := client.Delete(context.Background(), target, []byte("sk_test_synthetic"))
		want := "provider_unavailable"
		if errors.Is(err, context.DeadlineExceeded) {
			want = "timeout"
		}
		if errors.Is(err, context.Canceled) {
			want = "request_cancelled"
		}
		if got.Deleted || got.Code != want {
			t.Fatal(got)
		}
	}
}
