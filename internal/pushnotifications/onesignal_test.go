package pushnotifications

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func validMessage() Message {
	return Message{AppID: testAppID, SubscriptionIDs: []string{testSubscriptionID}, IdempotencyKey: "ad427041-a738-4caf-a8f7-192aca0cba98", Kind: "pending_review", URL: "https://app.example.test/notifications"}
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestOneSignalRequestUsesFixedEndpointAndGenericContent(t *testing.T) {
	for _, kind := range []string{"registration", "pending_review", "reminder", "import_error"} {
		t.Run(kind, func(t *testing.T) {
			m := validMessage()
			m.Kind = kind
			if kind == "registration" {
				m.ChallengeID, m.ChallengeSecret = testOtherID, "registration-proof"
			}
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != "https://api.onesignal.com/notifications?c=push" || r.Method != http.MethodPost {
					t.Fatal("provider endpoint changed by application origin")
				}
				if r.Header.Get("Authorization") != "Key "+testKey || r.Header.Get("Content-Type") != "application/json" {
					t.Fatal("incorrect API authentication or content type")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || len(body) > 16384 {
					t.Fatal("request body unbounded")
				}
				var payload struct {
					AppID    string            `json:"app_id"`
					IDs      []string          `json:"include_subscription_ids"`
					Key      string            `json:"idempotency_key"`
					Contents map[string]string `json:"contents"`
					Data     map[string]string `json:"data"`
				}
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatal(err)
				}
				if payload.AppID != testAppID || len(payload.IDs) != 1 || payload.IDs[0] != testSubscriptionID || payload.Key != m.IdempotencyKey {
					t.Fatal("incorrect targeting or idempotency key")
				}
				if _, err := uuid.Parse(payload.Key); err != nil {
					t.Fatal("idempotency key not UUID")
				}
				if payload.Contents["en"] == "" || payload.Contents["ja"] == "" {
					t.Fatal("fixed content absent")
				}
				if kind == "registration" {
					if payload.Data["cycrio_challenge_id"] != testOtherID || payload.Data["cycrio_challenge_secret"] != "registration-proof" {
						t.Fatal("registration proof absent")
					}
				} else if strings.Contains(string(body), "challenge") {
					t.Fatal("challenge leaked into ordinary notification")
				}
				return response(200, `{"id":"`+testOtherID+`"}`), nil
			})}
			got, err := NewOneSignalSender(client, "https://app.example.test").Send(context.Background(), []byte(testKey), m)
			if err != nil || got.State != "accepted" || got.ProviderID != testOtherID {
				t.Fatalf("send failed: %#v %v", got, err)
			}
		})
	}
}

func TestOneSignalRejectsUnsafeMessagesBeforeSending(t *testing.T) {
	cases := map[string]func(*Message){
		"invalid app":         func(m *Message) { m.AppID = "invalid" },
		"invalid idempotency": func(m *Message) { m.IdempotencyKey = "private-api-key" },
		"noncanonical uuid":   func(m *Message) { m.IdempotencyKey = strings.ReplaceAll(m.IdempotencyKey, "-", "") },
		"nil idempotency":     func(m *Message) { m.IdempotencyKey = uuid.Nil.String() },
		"no recipients":       func(m *Message) { m.SubscriptionIDs = nil },
		"many recipients": func(m *Message) {
			m.SubscriptionIDs = make([]string, 101)
			for i := range m.SubscriptionIDs {
				m.SubscriptionIDs[i] = uuid.NewString()
			}
		},
		"invalid subscription":           func(m *Message) { m.SubscriptionIDs = []string{"bad-id"} },
		"foreign url":                    func(m *Message) { m.URL = "https://evil.example/" },
		"credential url":                 func(m *Message) { m.URL = "https://private-api-key@app.example.test/" },
		"scheme relative":                func(m *Message) { m.URL = "//evil.example/path" },
		"relative url":                   func(m *Message) { m.URL = "/notifications" },
		"http":                           func(m *Message) { m.URL = "http://app.example.test/" },
		"url too long":                   func(m *Message) { m.URL += strings.Repeat("x", 2049) },
		"unknown kind":                   func(m *Message) { m.Kind = "private-api-key" },
		"challenge on ordinary":          func(m *Message) { m.ChallengeSecret = "private-api-key" },
		"registration without challenge": func(m *Message) { m.Kind = "registration" },
		"large registration": func(m *Message) {
			m.Kind = "registration"
			m.ChallengeID = testOtherID
			m.ChallengeSecret = strings.Repeat("x", 16385)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := validMessage()
			mutate(&m)
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unsafe message sent"); return nil, nil })}
			got, err := NewOneSignalSender(client, "https://app.example.test").Send(context.Background(), []byte(testKey), m)
			if err == nil || got.State != "failed" || strings.Contains(err.Error(), testKey) {
				t.Fatalf("unsafe validation result: %#v %v", got, err)
			}
		})
	}
}

func TestOneSignalRejectsInvalidOriginAndCredentials(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("invalid configuration sent"); return nil, nil })}
	for _, origin := range []string{"", "https://private-api-key@app.example.test", "https://app.example.test/path", "https://app.example.test?key=secret", "http://evil.example", "https://app.example.test/#fragment"} {
		got, err := NewOneSignalSender(client, origin).Send(context.Background(), []byte(testKey), validMessage())
		if err == nil || got.State != "failed" || strings.Contains(err.Error(), testKey) {
			t.Fatal("invalid origin accepted or leaked")
		}
	}
	for _, key := range []string{"", testKey + "\r\n", "private api key", strings.Repeat("k", 4097)} {
		got, err := NewOneSignalSender(client, "https://app.example.test").Send(context.Background(), []byte(key), validMessage())
		if err == nil || got.State != "failed" || !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), testKey) {
			t.Fatal("invalid raw API key accepted or leaked")
		}
	}
}

func TestOneSignalResponseStatesAndConfirmedInvalidSubscriptions(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		body, state string
		invalid     bool
	}{
		{"accepted", 200, `{"id":"` + testOtherID + `"}`, "accepted", false},
		{"partial", 200, `{"id":"` + testOtherID + `","errors":{"invalid_player_ids":["` + testSubscriptionID + `","` + testOtherID + `","` + testSubscriptionID + `"]}}`, "accepted", true},
		{"no subscriber", 200, `{"id":"","errors":["private-api-key: unsubscribed ` + testSubscriptionID + `"]}`, "failed", false},
		{"null notification id", 200, `{"id":null,"errors":["not subscribed"]}`, "failed", false},
		{"confirmed rejection", 400, `{"errors":{"invalid_player_ids":["` + testSubscriptionID + `"]}}`, "failed", true},
		{"unauthorized", 403, `{"errors":["private-api-key"]}`, "failed", false},
		{"rate limited", 429, `{"errors":["rate limited"]}`, "failed", false},
		{"server error", 503, `{"errors":["private-api-key"]}`, "unknown", false},
		{"malformed success", 200, `private-api-key`, "unknown", false},
		{"empty success", 200, `{}`, "unknown", false},
		{"arbitrary provider id", 200, `{"id":"private-api-key"}`, "unknown", false},
		{"oversize", 200, strings.Repeat("x", 65537), "unknown", false},
		{"trailing", 200, `{"id":"` + testOtherID + `"} {}`, "unknown", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return response(tc.status, tc.body), nil })}
			got, err := NewOneSignalSender(client, "https://app.example.test").Send(context.Background(), []byte(testKey), validMessage())
			if got.State != tc.state {
				t.Fatalf("state=%s want %s", got.State, tc.state)
			}
			if tc.state == "accepted" && err != nil {
				t.Fatal(err)
			}
			if err != nil && (strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), tc.body)) {
				t.Fatal("provider body leaked")
			}
			if tc.invalid {
				if len(got.InvalidSubscriptionIDs) != 1 || got.InvalidSubscriptionIDs[0] != testSubscriptionID {
					t.Fatal("confirmed evidence missing or unrequested IDs returned")
				}
			} else if len(got.InvalidSubscriptionIDs) != 0 {
				t.Fatal("guessed invalid subscription")
			}
		})
	}
}

func TestOneSignalNetworkErrorsAreUnknownAndSanitized(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("private-api-key request body") })}
	got, err := NewOneSignalSender(client, "https://app.example.test").Send(context.Background(), []byte(testKey), validMessage())
	if got.State != "unknown" || err == nil || strings.Contains(err.Error(), testKey) {
		t.Fatal("transport error exposed or definite failure inferred")
	}
}

func TestOneSignalNeverFollowsRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("redirect followed with notification credentials")
	}))
	defer target.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }, Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		r := response(307, "")
		r.Header.Set("Location", target.URL)
		return r, nil
	})}
	got, err := NewOneSignalSender(client, "https://app.example.test").Send(context.Background(), []byte(testKey), validMessage())
	if err == nil || got.State != "unknown" {
		t.Fatal("redirect not handled as ambiguous response")
	}
}

func TestOneSignalBoundaries(t *testing.T) {
	m := validMessage()
	m.SubscriptionIDs = make([]string, 100)
	for i := range m.SubscriptionIDs {
		m.SubscriptionIDs[i] = uuid.NewString()
	}
	called := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called++
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) > 16384 {
			t.Fatal("request byte limit exceeded")
		}
		reply := `{"id":"` + testOtherID + `"}`
		return response(200, reply+strings.Repeat(" ", 65536-len(reply))), nil
	})}
	sender := NewOneSignalSender(client, "https://app.example.test")
	got, err := sender.Send(context.Background(), []byte(strings.Repeat("k", 4096)), m)
	if err != nil || got.State != "accepted" || called != 1 {
		t.Fatalf("valid boundary rejected: %#v %v", got, err)
	}
	m.Kind = "registration"
	m.ChallengeID = testOtherID
	m.ChallengeSecret = strings.Repeat("<", 256)
	m.URL = "https://app.example.test/" + strings.Repeat("<", 2024)
	got, err = sender.Send(context.Background(), []byte(testKey), m)
	if !errors.Is(err, ErrInvalid) || got.State != "failed" || called != 1 {
		t.Fatal("expanded JSON body exceeded limit or was sent")
	}
}
