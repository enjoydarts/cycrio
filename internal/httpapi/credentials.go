package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/database"
)

type CredentialService interface {
	List(context.Context) ([]credentials.Summary, error)
	Put(context.Context, string, string, int64, string) (credentials.Summary, error)
	Disable(context.Context, string, string, int64) (credentials.Summary, error)
	Audit(context.Context) ([]credentials.AuditEntry, error)
}

type handlerOptions struct {
	overview        OverviewService
	integrations    IntegrationService
	credentials     CredentialService
	catalog         ModelCatalogService
	runtime         AIRuntimeService
	routes          AIRoutesService
	mail            MailAccountService
	mailSync        MailSyncService
	mailProcessing  MailProcessingService
	mailAutomation  MailAutomationService
	subscriptions   SubscriptionService
	analysisFeed    AnalysisFeedService
	billing         BillingService
	billingSchedule BillingScheduleService
}
type Option func(*handlerOptions)

func WithCredentials(service CredentialService) Option {
	return func(options *handlerOptions) { options.credentials = service }
}

type protectFunc func(bool, func(http.ResponseWriter, *http.Request, database.Principal)) http.HandlerFunc

func registerCredentials(mux *http.ServeMux, service CredentialService, protect protectFunc) {
	available := func(w http.ResponseWriter) bool {
		if service == nil {
			writeError(w, http.StatusServiceUnavailable, "dependency_unavailable")
			return false
		}
		return true
	}
	mux.HandleFunc("GET /v1/admin/credentials", protect(true, func(w http.ResponseWriter, r *http.Request, _ database.Principal) {
		if !available(w) {
			return
		}
		items, err := service.List(r.Context())
		if err != nil {
			writeCredentialError(w, err)
			return
		}
		if items == nil {
			items = []credentials.Summary{}
		}
		writeJSON(w, http.StatusOK, struct {
			Credentials []credentials.Summary `json:"credentials"`
		}{items})
	}))
	mux.HandleFunc("GET /v1/admin/audit", protect(true, func(w http.ResponseWriter, r *http.Request, _ database.Principal) {
		if !available(w) {
			return
		}
		items, err := service.Audit(r.Context())
		if err != nil {
			writeCredentialError(w, err)
			return
		}
		if items == nil {
			items = []credentials.AuditEntry{}
		}
		writeJSON(w, http.StatusOK, struct {
			Events []credentials.AuditEntry `json:"events"`
		}{items})
	}))
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		mux.HandleFunc(method+" /v1/admin/credentials/{provider}", protect(true, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
			if !available(w) {
				return
			}
			secret, expected, err := readCredentialInput(w, r)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_request")
				return
			}
			var summary credentials.Summary
			if r.Method == http.MethodPut {
				summary, err = service.Put(r.Context(), p.ID, r.PathValue("provider"), expected, secret)
			} else {
				summary, err = service.Disable(r.Context(), p.ID, r.PathValue("provider"), expected)
			}
			if err != nil {
				writeCredentialError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, summary)
		}))
	}
}

// Read a bounded object explicitly so duplicate and differently cased fields
// cannot be interpreted differently by the browser, API or downstream tools.
func readCredentialInput(w http.ResponseWriter, r *http.Request) (string, int64, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return "", 0, credentials.ErrInvalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	d := json.NewDecoder(r.Body)
	opening, err := d.Token()
	if err != nil || opening != json.Delim('{') {
		return "", 0, credentials.ErrInvalid
	}
	seen := map[string]bool{}
	var secret *string
	var expected *int64
	for d.More() {
		token, err := d.Token()
		field, ok := token.(string)
		if err != nil || !ok || seen[field] {
			return "", 0, credentials.ErrInvalid
		}
		seen[field] = true
		switch field {
		case "secret":
			if r.Method != http.MethodPut || d.Decode(&secret) != nil {
				return "", 0, credentials.ErrInvalid
			}
		case "expected_version":
			if d.Decode(&expected) != nil {
				return "", 0, credentials.ErrInvalid
			}
		default:
			return "", 0, credentials.ErrInvalid
		}
	}
	closing, err := d.Token()
	if err != nil || closing != json.Delim('}') || d.Decode(new(any)) != io.EOF || expected == nil || *expected < 0 || r.Method == http.MethodPut && secret == nil {
		return "", 0, credentials.ErrInvalid
	}
	if secret == nil {
		return "", *expected, nil
	}
	return *secret, *expected, nil
}

func writeCredentialError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, credentials.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, credentials.ErrConflict):
		writeError(w, http.StatusConflict, "conflict")
	case errors.Is(err, credentials.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	default:
		writeError(w, http.StatusServiceUnavailable, "dependency_unavailable")
	}
}
