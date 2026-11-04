package notificationruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/notifications"
)

const deliveryID = "11111111-1111-4111-8111-111111111111"
const providerID = "22222222-2222-4222-8222-222222222222"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func notificationDelivery() Delivery {
	id, due := "33333333-3333-4333-8333-333333333333", "2026-11-04"
	return Delivery{ID: deliveryID, From: "sender@example.com", To: "verified@example.com", Candidate: notifications.Candidate{
		Kind: notifications.Renewal, SourceKey: "renewal:" + id + ":" + due, ContractID: &id, Service: "Synthetic <service>", DueDate: &due, DateSource: "receipt_estimate", ScheduledAt: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), Email: true,
	}}
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestResendNotificationFixedTemplateAndEndpoint(t *testing.T) {
	calls := 0
	delivery := notificationDelivery()
	content, e := notifications.RenderEmail(delivery.Candidate)
	if e != nil {
		t.Fatal(e)
	}
	client := &http.Client{Timeout: time.Hour, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.GetBody != nil {
			t.Error("delivery body is replayable")
		}
		if r.Method != "POST" || r.URL.String() != "https://api.resend.com/emails" || r.Header.Get("Authorization") != "Bearer synthetic-resend-key" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Idempotency-Key") != "cycrio-notification-"+deliveryID {
			t.Error("unsafe request metadata")
		}
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 8*time.Second {
			t.Error("request is not bounded")
		}
		var got struct {
			From    string   `json:"from"`
			To      []string `json:"to"`
			Subject string   `json:"subject"`
			Text    string   `json:"text"`
			HTML    string   `json:"html"`
		}
		if json.NewDecoder(r.Body).Decode(&got) != nil || got.From != delivery.From || len(got.To) != 1 || got.To[0] != delivery.To || got.Subject != content.Subject || got.Text != content.Text || got.HTML != content.HTML || strings.Contains(got.HTML, "<service>") {
			t.Error("fixed rendered template changed")
		}
		return response(200, `{"id":"`+providerID+`"}`), nil
	})}
	s := NewResendSender(client)
	got := s.Send(context.Background(), []byte("synthetic-resend-key"), delivery)
	if got.State != Accepted || got.ProviderID != providerID || calls != 1 {
		t.Fatal(got, calls)
	}
	if client.Timeout != time.Hour || client.CheckRedirect != nil {
		t.Fatal("constructor mutated caller client")
	}
}

func TestResendNotificationRejectsUnsafeInputBeforeNetwork(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Delivery)
		key    string
	}{
		{"empty delivery", func(d *Delivery) { d.ID = "" }, "key"},
		{"nil uuid", func(d *Delivery) { d.ID = "00000000-0000-0000-0000-000000000000" }, "key"},
		{"compact uuid", func(d *Delivery) { d.ID = "11111111111141118111111111111111" }, "key"},
		{"recipient name", func(d *Delivery) { d.To = "Other <verified@example.com>" }, "key"},
		{"recipient list", func(d *Delivery) { d.To = "a@example.com,b@example.com" }, "key"},
		{"recipient injection", func(d *Delivery) { d.To = "verified@example.com\r\nBcc: other@example.com" }, "key"},
		{"recipient whitespace", func(d *Delivery) { d.To = " verified@example.com" }, "key"},
		{"sender name", func(d *Delivery) { d.From = "Other <sender@example.com>" }, "key"},
		{"sender injection", func(d *Delivery) { d.From = "sender@example.com\nX-Test: value" }, "key"},
		{"disabled email", func(d *Delivery) { d.Candidate.Email = false }, "key"},
		{"bad candidate", func(d *Delivery) { d.Candidate.SourceKey = "arbitrary-content" }, "key"},
		{"empty key", func(*Delivery) {}, ""},
		{"key header injection", func(*Delivery) {}, "key\r\nX-Injected: true"},
		{"key whitespace", func(*Delivery) {}, " key"},
		{"key control", func(*Delivery) {}, "key\x7f"},
		{"key too long", func(*Delivery) {}, strings.Repeat("x", 8193)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := NewResendSender(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return response(200, `{"id":"`+providerID+`"}`), nil
			})})
			d := notificationDelivery()
			tc.change(&d)
			got := s.Send(context.Background(), []byte(tc.key), d)
			if got.State != Invalid || got.ProviderID != "" || calls != 0 {
				t.Fatal("invalid preflight reached network", got, calls)
			}
		})
	}
}

func TestResendNotificationOutcomeDoesNotRetry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		err    error
		want   SendState
	}{
		{"accepted", 200, `{"id":"` + providerID + `"}`, nil, Accepted},
		{"bad json", 200, `invalid`, nil, Unknown},
		{"missing id", 200, `{}`, nil, Unknown},
		{"invalid id", 200, `{"id":"arbitrary"}`, nil, Unknown},
		{"nil id", 200, `{"id":"00000000-0000-0000-0000-000000000000"}`, nil, Unknown},
		{"duplicate ack", 200, `{"id":"` + providerID + `","id":"` + providerID + `"}`, nil, Unknown},
		{"extra ack", 200, `{"id":"` + providerID + `","private":"opaque"}`, nil, Unknown},
		{"oversize ack", 200, strings.Repeat(" ", 4097), nil, Unknown},
		{"bad request", 400, `{"message":"private"}`, nil, Rejected},
		{"unauthorized", 401, "", nil, Rejected},
		{"forbidden", 403, "", nil, Rejected},
		{"unprocessable", 422, "", nil, Rejected},
		{"rate limited", 429, "", nil, Rejected},
		{"request timeout", 408, "", nil, Unknown},
		{"idempotency conflict", 409, "", nil, Unknown},
		{"too early", 425, "", nil, Unknown},
		{"server error", 500, "", nil, Unknown},
		{"unexpected success", 201, `{"id":"` + providerID + `"}`, nil, Unknown},
		{"network failure", 0, "", errors.New("opaque key and recipient"), Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := NewResendSender(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if tc.err != nil {
					return nil, tc.err
				}
				return response(tc.status, tc.body), nil
			})})
			got := s.Send(context.Background(), []byte("synthetic-key"), notificationDelivery())
			if calls != 1 || got.State != tc.want || (got.ProviderID != "") != (tc.want == Accepted) {
				t.Fatal(got, calls)
			}
		})
	}
}

func TestResendNotificationDoesNotFollowRedirectsOrOutliveCancellation(t *testing.T) {
	calls := 0
	s := NewResendSender(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		r := response(307, "")
		r.Header.Set("Location", "https://arbitrary.example/leak")
		return r, nil
	})})
	if got := s.Send(context.Background(), []byte("synthetic-key"), notificationDelivery()); got.State != Unknown || calls != 1 {
		t.Fatal("redirect followed", got, calls)
	}
	s = NewResendSender(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := s.Send(ctx, []byte("synthetic-key"), notificationDelivery()); got.State != Unknown {
		t.Fatal(got)
	}
}

type trackedBody struct {
	reader io.Reader
	read   int
	closed bool
}

func (b *trackedBody) Read(p []byte) (int, error) { n, e := b.reader.Read(p); b.read += n; return n, e }
func (b *trackedBody) Close() error               { b.closed = true; return nil }

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("opaque response contents") }

func TestResendNotificationBoundsReadsAndClosesEveryResponse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		reader io.Reader
	}{
		{"accepted", 200, strings.NewReader(`{"id":"` + providerID + `"}`)},
		{"too large", 200, strings.NewReader(strings.Repeat("x", 1<<20))},
		{"read failure", 200, errorReader{}},
		{"rejected body unread", 403, errorReader{}},
		{"server error body unread", 500, errorReader{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackedBody{reader: tc.reader}
			s := NewResendSender(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: http.Header{}, Body: body}, nil
			})})
			got := s.Send(context.Background(), []byte("synthetic-key"), notificationDelivery())
			if !body.closed || body.read > 4097 || (tc.status != 200 && body.read != 0) {
				t.Fatal("response not bounded or closed", body.read, body.closed)
			}
			if tc.name == "read failure" && got.State != Unknown {
				t.Fatal(got)
			}
		})
	}
}

func TestResendNotificationCanonicalizesDeliveryUUID(t *testing.T) {
	d := notificationDelivery()
	d.ID = "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"
	s := NewResendSender(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Idempotency-Key") != "cycrio-notification-aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
			t.Error("idempotency UUID was not canonical")
		}
		return response(200, `{"id":"`+providerID+`"}`), nil
	})})
	if got := s.Send(context.Background(), []byte("synthetic-key"), d); got.State != Accepted {
		t.Fatal(got)
	}
}
