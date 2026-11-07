package httpapi

import (
	"context"
	"net/http"

	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/exchangerates"
)

type ExchangeRatesService interface {
	Latest(context.Context) (exchangerates.Snapshot, error)
}

func WithExchangeRates(service ExchangeRatesService) Option {
	return func(options *handlerOptions) { options.exchangeRates = service }
}

func registerExchangeRates(mux *http.ServeMux, service ExchangeRatesService, protect protectFunc) {
	mux.HandleFunc("GET /v1/exchange-rates", protect(false, func(w http.ResponseWriter, r *http.Request, _ database.Principal) {
		if r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength != 0 {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if service == nil {
			writeError(w, http.StatusServiceUnavailable, "dependency_unavailable")
			return
		}
		snapshot, err := service.Latest(r.Context())
		if err != nil {
			recordTelemetryError(w, err)
			writeError(w, http.StatusServiceUnavailable, "dependency_unavailable")
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	}))
}
