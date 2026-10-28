package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/bootstrap"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/health"
	"github.com/google/uuid"
)

type PrincipalLookup func(context.Context, string, string) (database.Principal, error)

func NewHandler(config bootstrap.Config, verifier auth.Verifier, lookup PrincipalLookup, ready func(context.Context) error, options ...Option) http.Handler {
	var settings handlerOptions
	for _, option := range options {
		option(&settings)
	}
	mux := http.NewServeMux()
	healthHandler := health.NewHandler(ready)
	mux.Handle("GET /healthz", healthHandler)
	mux.Handle("GET /readyz", healthHandler)
	mux.HandleFunc("GET /v1/public/config", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, struct {
			Configured     bool   `json:"configured"`
			PublishableKey string `json:"clerk_publishable_key"`
		}{config.Configured(), config.ClerkPublishableKey})
	})
	protect := func(admin bool, next func(http.ResponseWriter, *http.Request, database.Principal)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			values := r.Header.Values("Authorization")
			parts := strings.Fields(r.Header.Get("Authorization"))
			if !config.Configured() || verifier == nil || len(values) != 1 || len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 64<<10 {
				writeError(w, http.StatusUnauthorized, "authentication_required")
				return
			}
			identity, err := verifier.Verify(r.Context(), parts[1])
			if err != nil {
				if errors.Is(err, auth.ErrUnavailable) {
					writeError(w, http.StatusServiceUnavailable, "dependency_unavailable")
				} else {
					writeError(w, http.StatusUnauthorized, "authentication_required")
				}
				return
			}
			p, err := lookup(r.Context(), identity.Issuer, identity.Subject)
			if err != nil {
				if errors.Is(err, database.ErrForbidden) {
					writeError(w, http.StatusForbidden, "access_denied")
				} else {
					writeError(w, http.StatusServiceUnavailable, "dependency_unavailable")
				}
				return
			}
			if admin && p.Role != "platform_admin" {
				writeError(w, http.StatusForbidden, "access_denied")
				return
			}
			next(w, r, p)
		}
	}
	mux.HandleFunc("GET /v1/me", protect(false, func(w http.ResponseWriter, _ *http.Request, p database.Principal) { writeJSON(w, http.StatusOK, p) }))
	mux.HandleFunc("GET /v1/admin/status", protect(true, func(w http.ResponseWriter, _ *http.Request, p database.Principal) {
		writeJSON(w, http.StatusOK, struct {
			Role string `json:"role"`
		}{p.Role})
	}))
	registerCredentials(mux, settings.credentials, protect)
	registerOverview(mux, settings.overview, protect)
	registerIntegrations(mux, settings.integrations, protect)
	registerModelCatalog(mux, settings.catalog, protect)
	registerAIRuntime(mux, settings.runtime, protect)
	registerAIRoutes(mux, settings.routes, protect)
	registerMailAccounts(mux, settings.mail, protect)
	registerMailOAuth(mux, settings.mailOAuth, protect, config)
	registerMailSync(mux, settings.mailSync, protect)
	registerMailProcessing(mux, settings.mailProcessing, protect)
	registerMailAutomation(mux, settings.mailAutomation, protect)
	registerSubscriptions(mux, settings.subscriptions, protect)
	registerSubscriptionAutomation(mux, settings.subscriptions, protect)
	registerBilling(mux, settings.billing, protect)
	registerBillingSchedule(mux, settings.billingSchedule, protect)
	registerAnalysisFeed(mux, settings.analysisFeed, protect)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { writeError(w, http.StatusNotFound, "not_found") })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", uuid.NewString())
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		providerWebhook := r.Method == http.MethodPost && (r.URL.Path == "/v1/webhooks/clerk" || r.URL.Path == "/v1/webhooks/resend")
		if strings.HasPrefix(r.URL.Path, "/v1/") && !providerWebhook {
			origin := r.Header.Get("Origin")
			w.Header().Add("Vary", "Origin")
			if origin != "" {
				if !slices.Contains(config.AllowedOrigins, origin) {
					writeError(w, http.StatusForbidden, "access_denied")
					return
				}
				w.Header().Set("Access-Control-Allow-Origin", origin)
				probe := r.Clone(r.Context())
				if r.Method == http.MethodOptions {
					probe.Method = r.Header.Get("Access-Control-Request-Method")
				}
				if _, pattern := mux.Handler(probe); pattern == "POST /v1/mail/accounts/{id}/oauth" {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
			}
			if r.Method == http.MethodOptions {
				probe := r.Clone(r.Context())
				requested := r.Header.Get("Access-Control-Request-Method")
				probe.Method = requested
				_, pattern := mux.Handler(probe)
				if pattern == "/" || pattern == "" {
					knownPath := false
					for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPost} {
						probe.Method = method
						if _, found := mux.Handler(probe); found != "/" && found != "" {
							knownPath = true
							break
						}
					}
					if !knownPath {
						writeError(w, http.StatusNotFound, "not_found")
						return
					}
					writeError(w, http.StatusForbidden, "access_denied")
					return
				}
				if origin == "" || !slices.Contains([]string{"GET", "PUT", "DELETE", "POST"}, requested) {
					writeError(w, http.StatusForbidden, "access_denied")
					return
				}
				for _, header := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
					if h := strings.ToLower(strings.TrimSpace(header)); h != "" && h != "authorization" && h != "content-type" {
						writeError(w, http.StatusForbidden, "access_denied")
						return
					}
				}
				w.Header().Add("Vary", "Access-Control-Request-Method")
				w.Header().Add("Vary", "Access-Control-Request-Headers")
				w.Header().Set("Access-Control-Allow-Methods", requested)
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead && origin == "" {
				writeError(w, http.StatusForbidden, "access_denied")
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, struct {
		Code      string `json:"code"`
		RequestID string `json:"request_id"`
	}{code, w.Header().Get("X-Request-ID")})
}
