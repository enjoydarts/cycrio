//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"github.com/enjoydarts/cycrio/internal/auth"
	"github.com/enjoydarts/cycrio/internal/database"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailautomation"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"path/filepath"
	"strings"
	"testing"
)

func TestMailAutomationHTTPRealOwnerNonceCASAndDefault(t *testing.T) {
	ctx := context.Background()
	pool := processingHTTPPool(t, ctx)
	identities := database.NewStore(pool)
	owner, e := identities.AllowIdentity(ctx, configured().Issuer(), "automatic_owner", false)
	if e != nil {
		t.Fatal(e)
	}
	admin, e := identities.AllowIdentity(ctx, configured().Issuer(), "automatic_admin", true)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "keyring.json")
	if e = secretstore.Initialize(path); e != nil {
		t.Fatal(e)
	}
	keys, e := secretstore.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	_, runtime := processingHTTPProfile(t, ctx, pool, keys, admin.ID)
	full := true
	processing := database.NewMailProcessingRepository(pool)
	policy, e := processing.SavePolicy(ctx, admin.ID, mailprocessing.PolicyInput{ProfileID: runtime.Active.ID, Enabled: true, FullEmailAllowed: &full, PrimaryConfirmed: true, SecondaryConfirmed: true, PrimaryRetention: "Synthetic contract", SecondaryRetention: "Synthetic contract"})
	if e != nil {
		t.Fatal(e)
	}
	account := uuid.NewString()
	cv := mailaccounts.CredentialVersion{ID: uuid.NewString(), OwnerID: owner.ID, AccountID: account, Version: 1}
	cv.Envelope, e = keys.Seal([]byte(`{"login":"c3ludGhldGlj","password":"c2VjcmV0"}`), mailaccounts.CredentialAAD(cv))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = database.NewMailAccountRepository(pool).Save(ctx, owner.ID, account, 0, "Synthetic", cv); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE mail_accounts SET state='ready' WHERE id=$1`, account); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selectable,available,selected)VALUES($1,$2,'INBOX','/',true,true,true)`, uuid.NewString(), account); e != nil {
		t.Fatal(e)
	}
	service := mailautomation.NewService(database.NewMailAutomationRepository(pool))
	h := NewHandler(configured(), verifyFunc(func(_ context.Context, token string) (auth.Identity, error) {
		return auth.Identity{Issuer: configured().Issuer(), Subject: token}, nil
	}), identities.FindPrincipal, pool.Ping, WithMailAutomation(service))
	endpoint := "/v1/mail/accounts/" + account + "/automation"
	request := func(method, path string, in any, token string, status int) mailautomation.State {
		t.Helper()
		body := ""
		if in != nil {
			raw, _ := json.Marshal(in)
			body = string(raw)
		}
		w := credentialRequest(h, method, path, body, "Bearer "+token, "http://localhost:3100", "application/json")
		if w.Code != status {
			t.Fatal(w.Code, w.Body.String())
		}
		for _, secret := range []string{"ciphertext", "credential_id", "key_id", "UIDNEXT", "synthetic-private"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("private metadata leak", secret)
			}
		}
		var out mailautomation.State
		if status == 200 && json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal("invalid state")
		}
		return out
	}
	initial := request("GET", endpoint, nil, "automatic_owner", 200)
	if initial.Version != 0 || initial.State != "not_configured" || !initial.Ready || !initial.PreparationReady || initial.PreparationBlockedReasons == nil || initial.Scope != nil || initial.RecentSyncRuns == nil {
		t.Fatal(initial)
	}
	request("GET", endpoint, nil, "automatic_admin", 404)
	input := mailautomation.Input{ExpectedAccountVersion: 1, RuntimeVersion: runtime.Version, ProfileID: runtime.Active.ID, PolicyVersion: policy.Policy.Version, Since: "2025-01-01", Before: "2026-01-01", FullEmail: true, IntervalSeconds: 300, Confirmed: true, AutoRegister: true, RequestID: uuid.NewString()}
	value := request("PUT", endpoint, input, "automatic_owner", 200)
	request("PUT", endpoint, input, "automatic_admin", 404)
	replay := request("PUT", endpoint, input, "automatic_owner", 200)
	if value.ID != replay.ID || value.Version != replay.Version {
		t.Fatal("nonce replay")
	}
	changed := input
	changed.Before = "2026-01-02"
	request("PUT", endpoint, changed, "automatic_owner", 409)
	changed.RequestID = uuid.NewString()
	request("PUT", endpoint, changed, "automatic_owner", 409)
	stop := mailautomation.Control{ExpectedSettingsVersion: value.Version, RequestID: uuid.NewString()}
	stopped := request("POST", endpoint+"/stop", stop, "automatic_owner", 200)
	resumed := request("POST", endpoint+"/resume", mailautomation.Control{ExpectedSettingsVersion: stopped.Version, RequestID: uuid.NewString()}, "automatic_owner", 200)
	if resumed.Generation != value.Generation || resumed.State != "enabled" {
		t.Fatal(resumed)
	}
	if _, e = pool.Exec(ctx, `UPDATE mail_accounts SET version=version+1 WHERE id=$1`, account); e != nil {
		t.Fatal(e)
	}
	current := request("GET", endpoint, nil, "automatic_owner", 200)
	if current.Ready || !current.PreparationReady || current.PreparationBlockedReasons == nil || len(current.PreparationBlockedReasons) != 0 || current.State != "needs_confirmation" {
		t.Fatal("old scope blocked fresh preparation", current)
	}
	input.ExpectedSettingsVersion = current.Version
	input.ExpectedAccountVersion = current.Basis.AccountVersion
	input.RequestID = uuid.NewString()
	fresh := request("PUT", endpoint, input, "automatic_owner", 200)
	if !fresh.Ready || !fresh.PreparationReady || fresh.Generation != value.Generation+1 {
		t.Fatal("explicit fresh confirmation could not proceed", fresh)
	}
	request("GET", endpoint, nil, "unknown_not_allowed", 403)
}
