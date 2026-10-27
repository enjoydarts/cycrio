//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/airoutes"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/modelcatalog"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
)

func TestStandardPolicyHTTPRealServicePersistsOnlyExplicitCurrentConsent(t *testing.T) {
	ctx := context.Background()
	pool := processingHTTPPool(t, ctx)
	store := database.NewStore(pool)
	admin, e := store.AllowIdentity(ctx, configured().Issuer(), "standard_admin", true)
	if e != nil {
		t.Fatal(e)
	}
	member, e := store.AllowIdentity(ctx, configured().Issuer(), "standard_user", false)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "keys.json")
	if e = secretstore.Initialize(path); e != nil {
		t.Fatal(e)
	}
	keys, e := secretstore.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	secrets := credentials.NewService(database.NewCredentialRepository(pool), keys)
	for _, p := range []string{"liquid", "openrouter"} {
		if _, e = secrets.Put(ctx, admin.ID, p, 0, "synthetic-private-"+p); e != nil {
			t.Fatal(e)
		}
	}
	catalog := database.NewModelCatalogRepository(pool)
	yes := true
	model, e := catalog.AddModel(ctx, admin.ID, modelcatalog.ManualModelInput{Provider: "openrouter", ModelID: "synthetic-route", DisplayName: "Synthetic", TextInput: &yes, StructuredOutput: &yes})
	if e != nil {
		t.Fatal(e)
	}
	selected, e := catalog.SaveSettings(ctx, admin.ID, modelcatalog.SettingsInput{PrimaryModelID: "bff14486-c992-44eb-b80f-c599d8edddba", SecondaryModelID: model.ID})
	if e != nil {
		t.Fatal(e)
	}
	routes := database.NewAIRoutesRepository(pool)
	lease, e := routes.Claim(ctx, admin.ID)
	if e != nil {
		t.Fatal(e)
	}
	rows, e := airoutes.Parse([]byte(httpRouteJSON), nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = routes.Apply(ctx, admin.ID, lease, rows); e != nil {
		t.Fatal(e)
	}
	list, e := routes.List(ctx, airoutes.Filter{})
	if e != nil || len(list.Routes) != 1 {
		t.Fatal(list, e)
	}
	runtime := database.NewAIRuntimeRepository(pool)
	state, e := runtime.SaveProfile(ctx, admin.ID, airuntime.ConfigInput{CatalogSettingsID: *selected.ID, MaxInputBytes: 16384, MaxOutputTokens: 1024, TimeoutSeconds: 30, RouteID: list.Routes[0].ID})
	if e != nil {
		t.Fatal(e)
	}
	// Only this isolated fixture is activated; no inference is requested.
	if _, e = pool.Exec(ctx, `UPDATE ai_runtime_settings SET active_id=$1 WHERE singleton`, state.Draft.ID); e != nil {
		t.Fatal(e)
	}
	service := mailprocessing.NewService(database.NewMailProcessingRepository(pool), keys, nil, nil, nil)
	handler := NewHandler(configured(), verifyFunc(func(_ context.Context, token string) (auth.Identity, error) {
		return auth.Identity{Issuer: configured().Issuer(), Subject: token, SessionID: "synthetic"}, nil
	}), store.FindPrincipal, pool.Ping, WithMailProcessing(service))
	in := mailprocessing.StandardPolicyInput{RuntimeVersion: state.Version, ProfileID: state.Draft.ID, Enabled: true, PrimaryMode: "standard", PrimaryTermsID: mailprocessing.CurrentPrimaryTerms().ID, PrimaryConfirmed: true, SecondaryRouteID: list.Routes[0].ID, SecondaryConfirmed: true}
	body, _ := json.Marshal(in)
	url := "/v1/admin/mail-processing/standard-policy"
	for _, tc := range []struct {
		token  string
		status int
	}{{"", 401}, {"Bearer standard_user", 403}} {
		w := credentialRequest(handler, "PUT", url, string(body), tc.token, "http://localhost:3100", "application/json")
		if w.Code != tc.status {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, bad := range []string{strings.Replace(string(body), in.PrimaryTermsID, "obsolete", 1), strings.Replace(string(body), in.ProfileID, "not-a-uuid", 1), strings.Replace(string(body), `"primary_confirmed":true`, `"primary_confirmed":false`, 1)} {
		w := credentialRequest(handler, "PUT", url, bad, "Bearer standard_admin", "http://localhost:3100", "application/json")
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := credentialRequest(handler, "PUT", url, string(body), "Bearer standard_admin", "http://localhost:3100", "application/json")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var got mailprocessing.PolicyState
	if json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Policy.StandardProof == nil || got.Policy.StandardProof.PolicyVersion != 1 || !got.Policy.Enabled || got.StandardTerms == nil || !got.StandardTerms.Ready {
		t.Fatal(w.Body.String())
	}
	if got.Profile.Primary.RetentionSourceURL != mailprocessing.CurrentPrimaryTerms().SourceURL || got.Profile.Secondary.RetentionSourceURL != airoutes.SourceURL {
		t.Fatal("admin public profile omitted official sources", w.Body.String())
	}
	for _, secret := range []string{"synthetic-private", "ciphertext", "credential_id", "key_id"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("private metadata exposed", secret)
		}
	}
	w = credentialRequest(handler, "PUT", url, string(body), "Bearer standard_admin", "http://localhost:3100", "application/json")
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = credentialRequest(handler, "GET", "/v1/admin/mail-processing/policy", "", "Bearer standard_admin", "http://localhost:3100", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	account := uuid.NewString()
	if _, e = pool.Exec(ctx, `INSERT INTO mail_accounts(id,owner_id,label,state,version,credential_version)VALUES($1,$2,'Synthetic','ready',1,1)`, account, member.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO mail_account_credentials(id,account_id,owner_id,version,key_id,ciphertext,state)VALUES(gen_random_uuid(),$1,$2,1,'synthetic','synthetic','active')`, account, member.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selectable,available,selected)VALUES(gen_random_uuid(),$1,'INBOX','/',true,true,true)`, account); e != nil {
		t.Fatal(e)
	}
	w = credentialRequest(handler, "GET", "/v1/mail/accounts/"+account+"/processing-preview", "", "Bearer standard_user", "http://localhost:3100", "")
	var preview mailprocessing.Preview
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || !preview.Ready || preview.Primary.RetentionSourceURL != mailprocessing.CurrentPrimaryTerms().SourceURL || preview.Secondary.RetentionSourceURL != airoutes.SourceURL {
		t.Fatal("member could not read official retention sources", w.Code, w.Body.String())
	}
	for _, private := range []string{"actor_id", "standard_proof", "credential_id", "ciphertext", "workspace_id", "synthetic-private"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("member preview exposed private fields", private)
		}
	}
	var count int
	if e = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM ai_test_runs)+(SELECT count(*) FROM ai_calls)+(SELECT count(*) FROM mail_processing_runs)`).Scan(&count); e != nil || count != 0 {
		t.Fatal("read/save triggered inference or processing", count, e)
	}
}
