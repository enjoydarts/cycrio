package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/exchangerates"
)

type exchangeRatesFixture struct {
	calls int
	err   error
}

func (f *exchangeRatesFixture) Latest(context.Context) (exchangerates.Snapshot, error) {
	f.calls++
	return exchangerates.Snapshot{Source: "ECB", BaseCurrency: "JPY", Rates: []exchangerates.Rate{{Currency: "JPY", Rate: "1"}}}, f.err
}
func exchangeRatesHandler(service ExchangeRatesService, role string) http.Handler {
	return NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
		if role == "disabled" {
			return database.Principal{}, database.ErrForbidden
		}
		return database.Principal{ID: subscriptionUUID, Role: role}, nil
	}, nil, WithExchangeRates(service))
}
func TestExchangeRatesHTTPAuthenticationValidationAndFailure(t *testing.T) {
	for _, test := range []struct {
		name, role, token, path, body string
		err                           error
		status, calls                 int
	}{
		{"user", "user", "Bearer valid", "/v1/exchange-rates", "", nil, 200, 1},
		{"admin", "platform_admin", "Bearer valid", "/v1/exchange-rates", "", nil, 200, 1},
		{"anonymous", "user", "", "/v1/exchange-rates", "", nil, 401, 0},
		{"disabled", "disabled", "Bearer valid", "/v1/exchange-rates", "", nil, 403, 0},
		{"empty_query", "user", "Bearer valid", "/v1/exchange-rates?", "", nil, 400, 0},
		{"query", "user", "Bearer valid", "/v1/exchange-rates?owner_id=foreign", "", nil, 400, 0},
		{"body", "user", "Bearer valid", "/v1/exchange-rates", "{}", nil, 400, 0},
		{"source_failure", "user", "Bearer valid", "/v1/exchange-rates", "", errors.New("private upstream detail"), 503, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := &exchangeRatesFixture{err: test.err}
			w := credentialRequest(exchangeRatesHandler(f, test.role), "GET", test.path, test.body, test.token, "http://localhost:3100", "")
			if w.Code != test.status || f.calls != test.calls || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "private upstream detail") {
				t.Fatal(w.Code, w.Body.String(), f)
			}
			if w.Code == 200 {
				var body exchangerates.Snapshot
				if json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Source != "ECB" || body.Rates[0].Rate != "1" {
					t.Fatal(w.Body.String())
				}
			}
		})
	}
	w := credentialRequest(exchangeRatesHandler(nil, "user"), "GET", "/v1/exchange-rates", "", "Bearer valid", "http://localhost:3100", "")
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}
