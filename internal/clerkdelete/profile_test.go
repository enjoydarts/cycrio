package clerkdelete

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

const profileBody = `{"object":"user","id":"user_synthetic","updated_at":1000,"banned":false,"deprovisioned":false,"primary_email_address_id":"idn_primary","email_addresses":[{"id":"idn_other","email_address":"other@example.com","verification":{"status":"verified"}},{"id":"idn_primary","email_address":"self@example.com","verification":{"status":"verified"}}]}`

func profileClient(t *testing.T, status int, body string, requests *[]string) *Client {
	t.Helper()
	return NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		*requests = append(*requests, r.Method+" "+r.URL.String())
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer sk_test_synthetic" {
			t.Fatal("unsafe profile request")
		}
		if until, ok := r.Context().Deadline(); !ok || time.Until(until) > 10*time.Second {
			t.Fatal("profile read has no bounded deadline")
		}
		switch r.URL.Path {
		case "/v1/instance":
			return response(200, `{"id":"ins_synthetic"}`), nil
		case "/v1/domains":
			return response(200, `{"data":[{"frontend_api_url":"https://synthetic.clerk.accounts.dev"}],"total_count":1}`), nil
		case "/v1/users/user_synthetic":
			return response(status, body), nil
		default:
			t.Fatal("unexpected external request", r.Method, r.URL.Path)
			return nil, errors.New("unsafe request")
		}
	})})
}

func TestReadProfileUsesOnlyVerifiedPrimaryAndNeverSerializesIdentity(t *testing.T) {
	var requests []string
	got, err := profileClient(t, 200, profileBody, &requests).ReadProfile(context.Background(), target, []byte("sk_test_synthetic"))
	if err != nil || got.Subject != target.Subject || got.Email != "self@example.com" || got.UpdatedAt != 1000 || got.Blocked || got.Deleted {
		t.Fatal(got, err)
	}
	if len(requests) != 3 || requests[2] != "GET https://api.clerk.com/v1/users/user_synthetic" {
		t.Fatal(requests)
	}
	raw, err := json.Marshal(got)
	if err != nil || string(raw) != "{}" {
		t.Fatal("profile can expose private identity", string(raw), err)
	}
}

func TestReadProfileClearsUnverifiedAbsentOrBlockedPrimary(t *testing.T) {
	for _, tt := range []struct {
		name, body       string
		blocked, deleted bool
	}{
		{"unverified", strings.Replace(profileBody, `"id":"idn_primary","email_address":"self@example.com","verification":{"status":"verified"}`, `"id":"idn_primary","email_address":"self@example.com","verification":{"status":"unverified"}`, 1), false, false},
		{"no primary", strings.Replace(profileBody, `"primary_email_address_id":"idn_primary"`, `"primary_email_address_id":null`, 1), false, false},
		{"unknown primary", strings.Replace(profileBody, `"primary_email_address_id":"idn_primary"`, `"primary_email_address_id":"idn_missing"`, 1), false, false},
		{"banned", strings.Replace(profileBody, `"banned":false`, `"banned":true`, 1), true, false},
		{"deprovisioned", strings.Replace(profileBody, `"deprovisioned":false`, `"deprovisioned":true`, 1), true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var requests []string
			got, err := profileClient(t, 200, tt.body, &requests).ReadProfile(context.Background(), target, []byte("sk_test_synthetic"))
			if err != nil || got.Email != "" || got.Blocked != tt.blocked || got.Deleted != tt.deleted || got.Subject != target.Subject {
				t.Fatal(got, err)
			}
		})
	}
	var requests []string
	got, err := profileClient(t, 404, `private response`, &requests).ReadProfile(context.Background(), target, []byte("sk_test_synthetic"))
	if err != nil || !got.Deleted || got.Email != "" || got.Subject != target.Subject {
		t.Fatal(got, err)
	}
}

func TestReadProfileRejectsUntrustedOrMalformedDataWithoutLeakingIt(t *testing.T) {
	for _, body := range []string{
		strings.Replace(profileBody, `"id":"user_synthetic"`, `"id":"user_other"`, 1),
		strings.Replace(profileBody, `"object":"user"`, `"object":"organization"`, 1),
		strings.Replace(profileBody, `"updated_at":1000`, `"updated_at":0`, 1),
		strings.Replace(profileBody, `"updated_at":1000`, `"updated_at":null`, 1),
		strings.Replace(profileBody, `"updated_at":1000`, `"updated_at":99999999999999`, 1),
		strings.Replace(profileBody, `"banned":false,`, ``, 1),
		strings.Replace(profileBody, `"self@example.com"`, `"Display Name <self@example.com>"`, 1),
		strings.Replace(profileBody, `"self@example.com"`, `"self@example.com\r\nBcc:other@example.com"`, 1),
		strings.Replace(profileBody, `"id":"idn_other"`, `"id":"idn_primary"`, 1),
		strings.Replace(profileBody, `"email_addresses":[`, `"email_addresses":null,"unused":[`, 1),
		`{"id":"user_synthetic","updated_at":1000}`,
		strings.Repeat(" ", 1<<20) + profileBody,
	} {
		t.Run(body[:min(len(body), 35)], func(t *testing.T) {
			var requests []string
			got, err := profileClient(t, 200, body, &requests).ReadProfile(context.Background(), target, []byte("sk_test_synthetic"))
			if !errors.Is(err, ErrUnavailable) || got.Email != "" {
				t.Fatal("malformed profile accepted", got, err)
			}
			if strings.Contains(err.Error(), "@") || strings.Contains(err.Error(), "sk_test") {
				t.Fatal("private error", err)
			}
		})
	}
	for _, status := range []int{401, 403, 429, 500, 302} {
		var requests []string
		_, err := profileClient(t, status, `self@example.com sk_test_private`, &requests).ReadProfile(context.Background(), target, []byte("sk_test_synthetic"))
		if !errors.Is(err, ErrUnavailable) {
			t.Fatal(status, err)
		}
	}
}

func TestReadProfileRequiresInstanceAndIssuerBeforeFetchingUser(t *testing.T) {
	for _, bad := range []bool{false, true} {
		calls := 0
		client := NewClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.Path == "/v1/instance" {
				if bad {
					return response(200, `{"id":"ins_other"}`), nil
				}
				return response(200, `{"id":"ins_synthetic"}`), nil
			}
			if r.URL.Path == "/v1/domains" {
				return response(200, `{"data":[{"frontend_api_url":"https://other.clerk.accounts.dev"}],"total_count":1}`), nil
			}
			t.Fatal("fetched profile from wrong instance")
			return nil, errors.New("unsafe")
		})})
		if _, err := client.ReadProfile(context.Background(), target, []byte("sk_test_synthetic")); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		if bad && calls != 1 || !bad && calls != 2 {
			t.Fatal(calls)
		}
	}
}

func TestReadProfileTransportAndUnsafeTargetNeverLeakProviderError(t *testing.T) {
	client := NewClient(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("private sk_test_secret user_synthetic self@example.com")
	})})
	for _, ctx := range []context.Context{context.Background(), func() context.Context { c, cancel := context.WithCancel(context.Background()); cancel(); return c }()} {
		if _, err := client.ReadProfile(ctx, target, []byte("sk_test_synthetic")); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "@") || strings.Contains(err.Error(), "sk_test") {
			t.Fatal(err)
		}
	}
	called := false
	client = NewClient(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { called = true; return nil, errors.New("unsafe") })})
	unsafe := target
	unsafe.Subject = "user_synthetic/other"
	if _, err := client.ReadProfile(context.Background(), unsafe, []byte("sk_test_synthetic")); !errors.Is(err, ErrUnavailable) || called {
		t.Fatal("unsafe target fetched", called, err)
	}
}

func TestReadProfileTimeoutAndRedirectNeverFetchUserElsewhere(t *testing.T) {
	for _, failure := range []error{context.DeadlineExceeded, context.Canceled} {
		client := NewClient(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return nil, failure })})
		if _, err := client.ReadProfile(context.Background(), target, []byte("sk_test_synthetic")); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}
	calls := 0
	client := NewClient(&http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		rsp := response(http.StatusFound, `private`)
		rsp.Header.Set("Location", "https://other.example/users/user_synthetic")
		return rsp, nil
	})})
	if _, err := client.ReadProfile(context.Background(), target, []byte("sk_test_synthetic")); !errors.Is(err, ErrUnavailable) || calls != 1 {
		t.Fatal("redirect followed", calls, err)
	}
}
