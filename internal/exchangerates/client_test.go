package exchangerates

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func fixture(date, rates string) string {
	return `<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref"><Cube><Cube time="` + date + `">` + rates + `</Cube></Cube></gesmes:Envelope>`
}

type rewriteTransport struct{ target string }

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.String() != SourceURL {
		panic("unexpected source URL")
	}
	req, _ := http.NewRequestWithContext(r.Context(), r.Method, rt.target, nil)
	return http.DefaultTransport.RoundTrip(req)
}
func TestLatestRatesExactDecimalAndEveryCallFetches(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.WriteString(w, fixture("2026-11-02", `<Cube currency="USD" rate="1.17"/><Cube currency="JPY" rate="179.23"/>`))
	}))
	defer server.Close()
	client := NewClient(&http.Client{Transport: rewriteTransport{server.URL}}, func() time.Time { return testNow })
	for range 2 {
		result, err := client.Latest(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if result.Source != "ECB" || result.SourceURL != SourceURL || result.AsOf != "2026-11-02" || result.BaseCurrency != "JPY" || !result.FetchedAt.Equal(testNow) {
			t.Fatalf("bad metadata: %+v", result)
		}
		want := []Rate{{"JPY", "1"}, {"USD", "153.188034188034"}, {"EUR", "179.230000000000"}}
		for i, rate := range want {
			if result.Rates[i] != rate {
				t.Fatalf("rates=%+v", result.Rates)
			}
		}
	}
	if calls != 2 {
		t.Fatalf("cached rates: calls=%d", calls)
	}
}
func TestLatestRejectsUnusableSource(t *testing.T) {
	valid := `<Cube currency="USD" rate="1.17"/><Cube currency="JPY" rate="179.23"/>`
	for _, test := range []struct {
		name, xml string
		status    int
	}{
		{"missing", fixture("2026-11-02", `<Cube currency="USD" rate="1.17"/>`), 200},
		{"duplicate", fixture("2026-11-02", valid+`<Cube currency="JPY" rate="180"/>`), 200},
		{"zero", fixture("2026-11-02", strings.Replace(valid, "1.17", "0", 1)), 200},
		{"negative", fixture("2026-11-02", strings.Replace(valid, "1.17", "-1", 1)), 200},
		{"fraction", fixture("2026-11-02", strings.Replace(valid, "1.17", "1/2", 1)), 200},
		{"exponent", fixture("2026-11-02", strings.Replace(valid, "1.17", "1e2", 1)), 200},
		{"rounds_to_zero", fixture("2026-11-02", strings.Replace(valid, "179.23", "0.00000000000001", 1)), 200},
		{"future", fixture("2026-11-04", valid), 200},
		{"stale", fixture("2026-10-19", valid), 200},
		{"malformed", "<Envelope>", 200},
		{"multiple_documents", fixture("2026-11-02", valid) + fixture("2026-11-02", valid), 200},
		{"duplicate_attribute", fixture("2026-11-02", strings.Replace(valid, `rate="1.17"`, `rate="1.17" rate="2"`, 1)), 200},
		{"large", strings.Repeat(" ", 1<<20+1), 200},
		{"status", fixture("2026-11-02", valid), 503},
		{"redirect", "", 302},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://foreign.invalid/")
				w.WriteHeader(test.status)
				io.WriteString(w, test.xml)
			}))
			defer server.Close()
			client := NewClient(&http.Client{Transport: rewriteTransport{server.URL}}, func() time.Time { return testNow })
			if _, err := client.Latest(context.Background()); err == nil {
				t.Fatal("unusable source accepted")
			}
		})
	}
}
func TestLatestCancellationAndDeadline(t *testing.T) {
	client := NewClient(&http.Client{Transport: deadlineTransport{t}}, func() time.Time { return testNow })
	if _, err := client.Latest(context.Background()); err == nil {
		t.Fatal("transport failure hidden")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Latest(ctx); err == nil {
		t.Fatal("cancellation hidden")
	}
}

type deadlineTransport struct{ t *testing.T }

func (d deadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	deadline, ok := r.Context().Deadline()
	if !ok || time.Until(deadline) > 5*time.Second {
		d.t.Fatal("request missing bounded timeout")
	}
	return nil, context.DeadlineExceeded
}
