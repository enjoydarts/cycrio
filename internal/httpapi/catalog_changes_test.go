package httpapi

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/modelchanges"
	"net/http"
	"testing"
)

type changesFixture struct {
	catalogFixture
	changeCalls  int
	changeFilter modelchanges.Filter
}

func (f *changesFixture) CatalogChanges(_ context.Context, in modelchanges.Filter) (modelchanges.Page, error) {
	f.changeCalls++
	f.changeFilter = in
	return modelchanges.Page{Changes: []modelchanges.Change{}, Limit: in.Limit, Offset: in.Offset}, nil
}
func TestCatalogChangesRequireAdminAndValidateFilters(t *testing.T) {
	f := &changesFixture{}
	role := "user"
	h := NewHandler(configured(), verifyFunc(func(context.Context, string) (auth.Identity, error) { return auth.Identity{}, nil }), func(context.Context, string, string) (database.Principal, error) {
		return database.Principal{ID: "admin", Role: role}, nil
	}, func(context.Context) error { return nil }, WithModelCatalog(f))
	for _, authorization := range []string{"", "Bearer valid"} {
		w := credentialRequest(h, "GET", "/v1/admin/ai/catalog-changes", "", authorization, "http://localhost:3100", "")
		want := http.StatusForbidden
		if authorization == "" {
			want = http.StatusUnauthorized
		}
		if w.Code != want || f.changeCalls != 0 {
			t.Fatal("history exposed", w.Code)
		}
	}
	role = "platform_admin"
	for _, query := range []string{"provider=bad", "kind=bad", "model_id=bad", "offset=-1", "limit=0", "limit=101", "kind=added&kind=added", "unknown=1"} {
		w := credentialRequest(h, "GET", "/v1/admin/ai/catalog-changes?"+query, "", "Bearer valid", "http://localhost:3100", "")
		if w.Code != 400 || f.changeCalls != 0 {
			t.Fatal("invalid history filter accepted", query, w.Code)
		}
	}
	w := credentialRequest(h, "GET", "/v1/admin/ai/catalog-changes?provider=liquid&kind=price_changed&limit=1&offset=20", "", "Bearer valid", "http://localhost:3100", "")
	if w.Code != 200 || f.changeCalls != 1 || f.changeFilter.Provider != "liquid" || f.changeFilter.Kind != "price_changed" || f.changeFilter.Offset != 20 {
		t.Fatal("history contract lost", w.Code)
	}
}
