package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
)

type ModelCatalogService interface {
	List(context.Context, modelcatalog.Filter) (modelcatalog.ListResult, error)
	GetModel(context.Context, string) (modelcatalog.Model, error)
	AddModel(context.Context, string, modelcatalog.ManualModelInput) (modelcatalog.Model, error)
	UpdateModel(context.Context, string, string, modelcatalog.ModelUpdate) (modelcatalog.Model, error)
	Prices(context.Context, string) ([]modelcatalog.Price, error)
	SavePrice(context.Context, string, string, modelcatalog.ManualPriceInput) (modelcatalog.Model, error)
	ResetPrice(context.Context, string, string, int64) (modelcatalog.Model, error)
	SyncStates(context.Context) ([]modelcatalog.SyncState, error)
	RequestSync(context.Context, string, string) (modelcatalog.SyncState, error)
	ConfigureSync(context.Context, string, string, modelcatalog.SyncConfigInput) (modelcatalog.SyncState, error)
	Settings(context.Context) (modelcatalog.SettingsState, error)
	SaveSettings(context.Context, string, modelcatalog.SettingsInput) (modelcatalog.SettingsState, error)
}

func WithModelCatalog(service ModelCatalogService) Option {
	return func(options *handlerOptions) { options.catalog = service }
}

func registerModelCatalog(mux *http.ServeMux, service ModelCatalogService, protect protectFunc) {
	register := func(pattern string, handler func(http.ResponseWriter, *http.Request, database.Principal) (any, error)) {
		mux.HandleFunc(pattern, protect(true, func(w http.ResponseWriter, r *http.Request, p database.Principal) {
			if service == nil {
				writeError(w, http.StatusServiceUnavailable, "dependency_unavailable")
				return
			}
			value, err := handler(w, r, p)
			if err != nil {
				writeCatalogError(w, err)
				return
			}
			status := http.StatusOK
			if pattern == "POST /v1/admin/ai/catalog-sync/{provider}" {
				status = http.StatusAccepted
			}
			writeJSON(w, status, value)
		}))
	}
	register("GET /v1/admin/ai/catalog", func(_ http.ResponseWriter, r *http.Request, _ database.Principal) (any, error) {
		filter, err := catalogFilter(r.URL.RawQuery)
		if err != nil {
			return nil, err
		}
		return service.List(r.Context(), filter)
	})
	register("GET /v1/admin/ai/catalog/{id}", func(_ http.ResponseWriter, r *http.Request, _ database.Principal) (any, error) {
		return service.GetModel(r.Context(), r.PathValue("id"))
	})
	register("POST /v1/admin/ai/catalog", func(w http.ResponseWriter, r *http.Request, p database.Principal) (any, error) {
		var input modelcatalog.ManualModelInput
		if err := readAdminJSON(w, r, &input, "provider", "model_id", "display_name"); err != nil {
			return nil, err
		}
		return service.AddModel(r.Context(), p.ID, input)
	})
	register("PUT /v1/admin/ai/catalog/{id}", func(w http.ResponseWriter, r *http.Request, p database.Principal) (any, error) {
		var input modelcatalog.ModelUpdate
		if err := readAdminJSON(w, r, &input, "expected_version", "display_name", "enabled"); err != nil {
			return nil, err
		}
		if input.ExpectedVersion < 0 {
			return nil, modelcatalog.ErrInvalid
		}
		return service.UpdateModel(r.Context(), p.ID, r.PathValue("id"), input)
	})
	register("GET /v1/admin/ai/catalog/{id}/prices", func(_ http.ResponseWriter, r *http.Request, _ database.Principal) (any, error) {
		items, err := service.Prices(r.Context(), r.PathValue("id"))
		if items == nil {
			items = []modelcatalog.Price{}
		}
		return struct {
			Prices []modelcatalog.Price `json:"prices"`
		}{items}, err
	})
	register("POST /v1/admin/ai/catalog/{id}/prices", func(w http.ResponseWriter, r *http.Request, p database.Principal) (any, error) {
		var input modelcatalog.ManualPriceInput
		if err := readAdminJSON(w, r, &input, "expected_version", "currency", "effective_from", "source_url"); err != nil {
			return nil, err
		}
		if input.ExpectedVersion < 0 {
			return nil, modelcatalog.ErrInvalid
		}
		return service.SavePrice(r.Context(), p.ID, r.PathValue("id"), input)
	})
	register("DELETE /v1/admin/ai/catalog/{id}/prices/override", func(w http.ResponseWriter, r *http.Request, p database.Principal) (any, error) {
		var input struct {
			ExpectedVersion int64 `json:"expected_version"`
		}
		if err := readAdminJSON(w, r, &input, "expected_version"); err != nil {
			return nil, err
		}
		if input.ExpectedVersion < 0 {
			return nil, modelcatalog.ErrInvalid
		}
		return service.ResetPrice(r.Context(), p.ID, r.PathValue("id"), input.ExpectedVersion)
	})
	register("GET /v1/admin/ai/catalog-sync", func(_ http.ResponseWriter, r *http.Request, _ database.Principal) (any, error) {
		items, err := service.SyncStates(r.Context())
		if items == nil {
			items = []modelcatalog.SyncState{}
		}
		return struct {
			Providers []modelcatalog.SyncState `json:"providers"`
		}{items}, err
	})
	register("POST /v1/admin/ai/catalog-sync/{provider}", func(w http.ResponseWriter, r *http.Request, p database.Principal) (any, error) {
		var input struct{}
		if err := readAdminJSON(w, r, &input); err != nil {
			return nil, err
		}
		return service.RequestSync(r.Context(), p.ID, r.PathValue("provider"))
	})
	register("PUT /v1/admin/ai/catalog-sync/{provider}", func(w http.ResponseWriter, r *http.Request, p database.Principal) (any, error) {
		var input modelcatalog.SyncConfigInput
		if err := readAdminJSON(w, r, &input, "expected_version", "automatic_enabled", "interval_hours"); err != nil {
			return nil, err
		}
		if input.ExpectedVersion < 0 {
			return nil, modelcatalog.ErrInvalid
		}
		return service.ConfigureSync(r.Context(), p.ID, r.PathValue("provider"), input)
	})
	register("GET /v1/admin/ai/settings", func(_ http.ResponseWriter, r *http.Request, _ database.Principal) (any, error) {
		return service.Settings(r.Context())
	})
	register("PUT /v1/admin/ai/settings", func(w http.ResponseWriter, r *http.Request, p database.Principal) (any, error) {
		var input modelcatalog.SettingsInput
		if err := readAdminJSON(w, r, &input, "expected_version", "primary_model_id", "secondary_model_id"); err != nil {
			return nil, err
		}
		if input.ExpectedVersion < 0 {
			return nil, modelcatalog.ErrInvalid
		}
		return service.SaveSettings(r.Context(), p.ID, input)
	})
}

// Inspect object members before typed decoding; encoding/json alone accepts
// duplicate keys and case-insensitive struct field names.
func readAdminJSON(w http.ResponseWriter, r *http.Request, target any, required ...string) error {
	return readBoundedJSON(w, r, target, 16<<10, required...)
}

func readBoundedJSON(w http.ResponseWriter, r *http.Request, target any, maxBytes int64, required ...string) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return modelcatalog.ErrInvalid
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
	opening, err := d.Token()
	if err != nil || opening != json.Delim('{') {
		return modelcatalog.ErrInvalid
	}
	t := reflect.TypeOf(target).Elem()
	allowed := map[string]bool{}
	nonNull := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			allowed[name] = true
			nonNull[name] = t.Field(i).Tag.Get("nonnull") == "true"
		}
	}
	values := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || !allowed[key] {
			return modelcatalog.ErrInvalid
		}
		if _, exists := values[key]; exists {
			return modelcatalog.ErrInvalid
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return modelcatalog.ErrInvalid
		}
		if nonNull[key] && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return modelcatalog.ErrInvalid
		}
		values[key] = raw
	}
	closing, err := d.Token()
	if err != nil || closing != json.Delim('}') || d.Decode(new(any)) != io.EOF {
		return modelcatalog.ErrInvalid
	}
	for _, key := range required {
		raw, exists := values[key]
		if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return modelcatalog.ErrInvalid
		}
	}
	data, err := json.Marshal(values)
	if err != nil || json.Unmarshal(data, target) != nil {
		return modelcatalog.ErrInvalid
	}
	return nil
}

func catalogFilter(raw string) (modelcatalog.Filter, error) {
	filter := modelcatalog.Filter{Limit: 50}
	query, err := url.ParseQuery(raw)
	if err != nil {
		return filter, modelcatalog.ErrInvalid
	}
	for key, values := range query {
		if len(values) != 1 {
			return filter, modelcatalog.ErrInvalid
		}
		switch key {
		case "provider", "search", "include_disabled", "limit", "offset":
		default:
			return filter, modelcatalog.ErrInvalid
		}
	}
	filter.Provider = query.Get("provider")
	filter.Search = query.Get("search")
	if filter.Provider != "" && !modelcatalog.ValidProvider(filter.Provider) || utf8.RuneCountInString(filter.Search) > 100 {
		return filter, modelcatalog.ErrInvalid
	}
	if value := query.Get("limit"); value != "" {
		filter.Limit, err = strconv.Atoi(value)
		if err != nil || filter.Limit < 1 || filter.Limit > 100 {
			return filter, modelcatalog.ErrInvalid
		}
	}
	if value := query.Get("offset"); value != "" {
		filter.Offset, err = strconv.Atoi(value)
		if err != nil || filter.Offset < 0 || filter.Offset > 1000000 {
			return filter, modelcatalog.ErrInvalid
		}
	}
	if value := query.Get("include_disabled"); value != "" {
		if value != "true" && value != "false" {
			return filter, modelcatalog.ErrInvalid
		}
		filter.IncludeDisabled = value == "true"
	}
	return filter, nil
}

func writeCatalogError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, modelcatalog.ErrInvalid):
		writeError(w, 400, "invalid_request")
	case errors.Is(err, modelcatalog.ErrConflict):
		writeError(w, 409, "conflict")
	case errors.Is(err, modelcatalog.ErrNotFound):
		writeError(w, 404, "not_found")
	default:
		writeError(w, 503, "dependency_unavailable")
	}
}
