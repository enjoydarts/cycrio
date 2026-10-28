package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/enjoydarts/cycrio/internal/billing"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/overview"
)

type OverviewService interface {
	Read(context.Context, string) (overview.Report, error)
}

func WithOverview(service OverviewService) Option {
	return func(o *handlerOptions) { o.overview = service }
}
func registerOverview(mux *http.ServeMux, service OverviewService, protect protectFunc) {
	mux.HandleFunc("GET /v1/overview", protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
		if r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength != 0 {
			writeError(w, 400, "invalid_request")
			return
		}
		if service == nil {
			writeError(w, 503, "dependency_unavailable")
			return
		}
		report, err := service.Read(r.Context(), p.ID)
		if err != nil {
			switch {
			case errors.Is(err, overview.ErrInvalid):
				writeError(w, 400, "invalid_request")
			case errors.Is(err, overview.ErrNotFound):
				writeError(w, 404, "not_found")
			default:
				writeError(w, 503, "dependency_unavailable")
			}
			return
		}
		if report.Upcoming == nil {
			report.Upcoming = []overview.Upcoming{}
		}
		if report.Connections == nil {
			report.Connections = []overview.Connection{}
		}
		for i := range report.Connections {
			if report.Connections[i].BlockedReasons == nil {
				report.Connections[i].BlockedReasons = []string{}
			}
		}
		if report.BillingMonth.Currencies == nil {
			report.BillingMonth.Currencies = []billing.CurrencySummary{}
		}
		writeJSON(w, 200, report)
	}))
}
