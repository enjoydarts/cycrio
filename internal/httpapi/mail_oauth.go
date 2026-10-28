package httpapi

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/enjoydarts/cycrio/internal/bootstrap"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/mailoauth"
)

type MailOAuthService interface {
	Providers(context.Context) ([]mailoauth.ProviderStatus, error)
	Begin(context.Context, string, string, mailoauth.BeginInput) (mailoauth.BeginResult, error)
	Callback(context.Context, string, string, string, string) error
}

func WithMailOAuth(service MailOAuthService) Option {
	return func(options *handlerOptions) { options.mailOAuth = service }
}

const mailOAuthCookiePath = "/v1/mail/oauth/callback"
const mailOAuthCookiePrefix = "cycrio_mail_oauth_"

func registerMailOAuth(mux *http.ServeMux, service MailOAuthService, protect protectFunc, config bootstrap.Config) {
	mux.HandleFunc("GET /v1/mail/oauth/providers", protect(false, func(w http.ResponseWriter, r *http.Request, _ database.Principal) {
		if r.URL.RawQuery != "" {
			writeError(w, 400, "invalid_request")
			return
		}
		if service == nil {
			writeError(w, 503, "dependency_unavailable")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		providers, err := service.Providers(ctx)
		if err != nil {
			writeMailError(w, err)
			return
		}
		if providers == nil {
			providers = []mailoauth.ProviderStatus{}
		}
		writeJSON(w, 200, struct {
			Providers []mailoauth.ProviderStatus `json:"providers"`
		}{providers})
	}))
	mux.HandleFunc("POST /v1/mail/accounts/{id}/oauth", protect(false, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
		if r.URL.RawQuery != "" {
			writeError(w, 400, "invalid_request")
			return
		}
		if service == nil {
			writeError(w, 503, "dependency_unavailable")
			return
		}
		var in mailoauth.BeginInput
		if err := readAdminJSON(w, r, &in, "provider", "label", "expected_version"); err != nil {
			writeMailError(w, err)
			return
		}
		if !mailOAuthProvider(in.Provider) || in.ExpectedVersion < 0 {
			writeError(w, 400, "invalid_request")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		result, err := service.Begin(ctx, p.ID, r.PathValue("id"), in)
		if err != nil {
			writeMailError(w, err)
			return
		}
		if !mailOAuthProof(result.State) || !mailOAuthCookieValue(result.BrowserToken) || result.AuthorizationURL == "" {
			writeError(w, 503, "dependency_unavailable")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: mailOAuthCookiePrefix + result.State, Value: result.BrowserToken, Path: mailOAuthCookiePath, MaxAge: 600, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: mailOAuthSecure(r, config, result.AuthorizationURL)})
		// Only the authorization URL crosses the API boundary; browser proof and
		// private service fields are never serialized even if the service grows.
		writeJSON(w, 200, struct {
			AuthorizationURL string `json:"authorization_url"`
		}{result.AuthorizationURL})
	}))
	mux.HandleFunc("GET /v1/mail/oauth/callback/{provider}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		redirect := func(ok bool) {
			status := "failed"
			if ok {
				status = "connected"
			}
			origin := ""
			if len(config.AllowedOrigins) > 0 {
				origin = config.AllowedOrigins[0]
			}
			http.Redirect(w, r, origin+"/settings/mail?oauth="+status, http.StatusSeeOther)
		}
		if len(r.URL.RawQuery) > 64<<10 {
			redirect(false)
			return
		}
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			redirect(false)
			return
		}
		states := q["state"]
		if len(states) != 1 || !mailOAuthProof(states[0]) {
			redirect(false)
			return
		}
		state := states[0]
		// Every valid state has its own cookie, so concurrent account connections
		// cannot overwrite one another's browser binding.
		http.SetCookie(w, &http.Cookie{Name: mailOAuthCookiePrefix + state, Value: "", Path: mailOAuthCookiePath, MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: mailOAuthSecure(r, config, "")})
		provider := r.PathValue("provider")
		codes, providerErrors := q["code"], q["error"]
		if !mailOAuthProvider(provider) || len(codes) > 1 || len(providerErrors) > 1 || len(codes) == 1 && len(providerErrors) == 1 || len(codes) == 0 && len(providerErrors) == 0 {
			redirect(false)
			return
		}
		code := ""
		if len(codes) == 1 {
			code = codes[0]
			if code == "" || len(code) > 64<<10 {
				redirect(false)
				return
			}
		}
		if len(providerErrors) == 1 && (providerErrors[0] == "" || len(providerErrors[0]) > 64<<10) {
			redirect(false)
			return
		}
		cookies := r.CookiesNamed(mailOAuthCookiePrefix + state)
		if len(cookies) != 1 || !mailOAuthCookieValue(cookies[0].Value) || service == nil {
			redirect(false)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		err = service.Callback(ctx, provider, state, code, cookies[0].Value)
		redirect(err == nil && len(providerErrors) == 0)
	})
}

func mailOAuthProvider(provider string) bool { return provider == "gmail" || provider == "outlook" }
func mailOAuthProof(proof string) bool {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(proof)
	return err == nil && len(raw) == 32 && len(proof) == 43
}
func mailOAuthCookieValue(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func mailOAuthSecure(r *http.Request, config bootstrap.Config, authorizationURL string) bool {
	if r.TLS != nil {
		return true
	}
	if len(config.AllowedOrigins) > 0 && strings.HasPrefix(config.AllowedOrigins[0], "https://") {
		return true
	}
	// The redirect URI was validated by the OAuth service from an administrator
	// configuration. Forwarded headers are intentionally never consulted.
	u, err := url.Parse(authorizationURL)
	if err == nil {
		redirect, err := url.Parse(u.Query().Get("redirect_uri"))
		return err == nil && redirect.Scheme == "https"
	}
	return false
}
