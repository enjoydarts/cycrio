package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/reports"
)

type ReportsService interface {
	Read(context.Context, string, string) (reports.Report, error)
}

func WithReports(service ReportsService) Option {
	return func(o *handlerOptions) { o.reports = service }
}

func registerReports(mux *http.ServeMux, service ReportsService, protect protectFunc) {
	mux.HandleFunc("GET /v1/reports", protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
		month, err := readReportMonth(r)
		if err != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		if service == nil {
			writeError(w, 503, "dependency_unavailable")
			return
		}
		report, err := service.Read(r.Context(), p.ID, month)
		if err != nil {
			recordTelemetryError(w, err)
			switch {
			case errors.Is(err, reports.ErrInvalid):
				writeError(w, 400, "invalid_request")
			case errors.Is(err, reports.ErrNotFound):
				writeError(w, 404, "not_found")
			default:
				writeError(w, 503, "dependency_unavailable")
			}
			return
		}
		if report.Contracts == nil {
			report.Contracts = []reports.Contract{}
		}
		if report.Currencies == nil {
			report.Currencies = []reports.CurrencySummary{}
		}
		if report.History == nil {
			report.History = []reports.HistoryBucket{}
		}
		if report.MonthlyServices == nil {
			report.MonthlyServices = []reports.MonthlyService{}
		}
		if report.Upcoming == nil {
			report.Upcoming = []reports.Due{}
		}
		for i := range report.History {
			if report.History[i].Currencies == nil {
				report.History[i].Currencies = []reports.ActualCurrency{}
			}
		}
		writeJSON(w, 200, report)
	}))
}

func readReportMonth(r *http.Request) (string, error) {
	if r.ContentLength != 0 || r.URL.ForceQuery && r.URL.RawQuery == "" {
		return "", reports.ErrInvalid
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", reports.ErrInvalid
	}
	if len(query) == 0 {
		return "", nil
	}
	if len(query) != 1 || len(query["month"]) != 1 {
		return "", reports.ErrInvalid
	}
	month := query.Get("month")
	parsed, err := time.Parse("2006-01", month)
	if err != nil || len(month) != 7 || parsed.Year() < 2000 || parsed.Year() > 2199 {
		return "", reports.ErrInvalid
	}
	return month, nil
}
