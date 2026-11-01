//go:build integration

package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"io/fs"
	"testing"
	"time"
)

func TestAIRuntimeMailAttributionPersistsAndRejectsReassignment(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	l, _ := processingBatch(t, f, 1)
	var item string
	if err := f.runtime.pool.QueryRow(ctx, `SELECT id FROM mail_processing_items WHERE run_id=$1`, l.RunID).Scan(&item); err != nil {
		t.Fatal(err)
	}
	c, err := f.runtime.BeginCall(ctx, airuntime.BeginCallInput{OperationID: item, ProfileID: f.in.ProfileID, Scope: "user", ConsumingUserID: &f.owner, Stage: "classification", AttemptNo: 1})
	if err != nil {
		t.Fatal(err)
	}
	var account *string
	if err := f.runtime.pool.QueryRow(ctx, `SELECT mail_account_id FROM ai_calls WHERE id=$1`, c.ID).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if account == nil || *account != f.account {
		t.Fatal("old worker call not attributed")
	}
	raw, _ := json.Marshal(c)
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	if doc["mail_account_id"] != f.account {
		t.Fatal("call API omitted account")
	}
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE ai_calls SET mail_account_id=NULL WHERE id=$1`, c.ID); err == nil {
		t.Fatal("attribution mutable")
	}
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE ai_calls SET consuming_user_id=$2 WHERE id=$1`, c.ID, f.other); err == nil {
		t.Fatal("ownership mutable")
	}
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE ai_calls SET operation_id=$2 WHERE id=$1`, c.ID, uuid.NewString()); err == nil {
		t.Fatal("operation mutable")
	}
	for _, input := range []struct {
		scope string
		owner *string
	}{{"user", &f.other}, {"system", nil}} {
		if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO ai_calls(id,operation_id,profile_id,scope,consuming_user_id,provider,model_id,model_name,requested_model_id,credential_id,stage,attempt_no,status,mail_account_id) SELECT $2,$3,profile_id,$4,$5,provider,model_id,model_name,requested_model_id,credential_id,stage,1,'calling',$6 FROM ai_calls WHERE id=$1`, c.ID, uuid.NewString(), uuid.NewString(), input.scope, input.owner, f.account); err == nil {
			t.Fatal("invalid explicit attribution accepted", input.scope)
		}
	}
	retry, err := f.runtime.BeginCall(ctx, airuntime.BeginCallInput{OperationID: item, ProfileID: f.in.ProfileID, Scope: "user", ConsumingUserID: &f.owner, Stage: "classification", AttemptNo: 2})
	if err != nil {
		t.Fatal("retry", err)
	}
	if _, err := f.runtime.BeginCall(ctx, airuntime.BeginCallInput{OperationID: item, ProfileID: f.in.ProfileID, Scope: "user", ConsumingUserID: &f.other, Stage: "extraction", AttemptNo: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.Cancel(ctx, f.owner, f.account, l.RunID); err != nil {
		t.Fatal(err)
	}
	// Analysis records can be deleted independently of the retained call ledger.
	if _, err := f.runtime.pool.Exec(ctx, `DELETE FROM mail_processing_items WHERE run_id=$1`, l.RunID); err != nil {
		t.Fatal(err)
	}
	usd, amount := "USD", "0.25"
	finished, err := f.runtime.FinishCall(ctx, c.ID, airuntime.CallUpdate{Outcome: "success", GenerationID: "synthetic-mail-generation", Cost: ai.Cost{Status: "estimated", Amount: &amount, Currency: &usd}})
	if err != nil {
		t.Fatal("finish after deletion", err)
	}
	if finished.MailAccountID == nil || *finished.MailAccountID != f.account {
		t.Fatal("finish lost API attribution")
	}
	// Use a synthetic OpenRouter ledger row to exercise its later cost reconciliation.
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE ai_calls SET provider='openrouter' WHERE id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	amount = "0.5"
	reconciled, err := f.runtime.ReconcileCall(ctx, f.admin, c.ID, ai.Reconciliation{GenerationID: "synthetic-mail-generation", Cost: ai.Cost{Status: "provider_reported", Amount: &amount, Currency: &usd}})
	if err != nil || reconciled.MailAccountID == nil || *reconciled.MailAccountID != f.account || reconciled.Cost.Amount == nil || *reconciled.Cost.Amount != "0.5" {
		t.Fatal("reconciliation lost attribution", err)
	}
	for _, id := range []string{c.ID, retry.ID} {
		if err := f.runtime.pool.QueryRow(ctx, `SELECT mail_account_id FROM ai_calls WHERE id=$1`, id).Scan(&account); err != nil || account == nil || *account != f.account {
			t.Fatal("deletion lost attribution", err)
		}
	}
	db, err := sql.Open("pgx", f.runtime.pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.DownTo(ctx, 25); err == nil {
		t.Fatal("downgrade discarded retained account attribution")
	}
	var version int
	if err = f.runtime.pool.QueryRow(ctx, `SELECT mail_account_id,(SELECT max(version_id) FROM goose_db_version WHERE is_applied) FROM ai_calls WHERE id=$1`, c.ID).Scan(&account, &version); err != nil || version != 26 || account == nil || *account != f.account {
		t.Fatal("refused downgrade changed data or schema", err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE ai_calls SET mail_account_id=NULL WHERE id=$1`, c.ID); err == nil {
		t.Fatal("refused downgrade removed attribution trigger")
	}
}

func TestAIRuntimeMailAccountCostsAndFiltering(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	second := uuid.NewString()
	if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO mail_accounts(id,owner_id,label,state,version,credential_version) VALUES($1,$2,'Second','disconnected',1,1)`, second, f.owner); err != nil {
		t.Fatal(err)
	}
	makeCall := func(account *string, stage, kind, amount, currency, outcome string) string {
		t.Helper()
		c, err := f.runtime.BeginCall(ctx, airuntime.BeginCallInput{OperationID: uuid.NewString(), ProfileID: f.in.ProfileID, Scope: "user", ConsumingUserID: &f.owner, Stage: stage, AttemptNo: 1})
		if err != nil {
			t.Fatal(err)
		}
		// Explicit account insertion models retained legacy calls without analysis records.
		if account != nil {
			// Assignment must happen on INSERT, before the immutable attribution guard.
			var attributed string
			if err = f.runtime.pool.QueryRow(ctx, `INSERT INTO ai_calls(id,operation_id,profile_id,scope,consuming_user_id,provider,model_id,model_name,requested_model_id,credential_id,price_id,stage,attempt_no,status,mail_account_id) SELECT $2,$3,profile_id,scope,consuming_user_id,provider,model_id,model_name,requested_model_id,credential_id,price_id,stage,attempt_no,status,$4 FROM ai_calls WHERE id=$1 RETURNING id`, c.ID, uuid.NewString(), uuid.NewString(), account).Scan(&attributed); err != nil {
				t.Fatal(err)
			}
			if _, err = f.runtime.pool.Exec(ctx, `DELETE FROM ai_calls WHERE id=$1`, c.ID); err != nil {
				t.Fatal(err)
			}
			c.ID = attributed
		}
		cost := ai.Cost{Status: kind}
		if kind != "unknown" {
			cost.Amount = &amount
			cost.Currency = &currency
		}
		if _, err = f.runtime.FinishCall(ctx, c.ID, airuntime.CallUpdate{Outcome: outcome, Cost: cost}); err != nil {
			t.Fatal(err)
		}
		if _, err = f.runtime.pool.Exec(ctx, `UPDATE ai_calls SET started_at='2026-10-02T00:00:00Z' WHERE id=$1`, c.ID); err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	makeCall(&f.account, "classification", "estimated", "1", "USD", "success")
	makeCall(&f.account, "extraction", "provider_reported", "2", "EUR", "success")
	makeCall(&second, "extraction", "unknown", "", "", "invalid_output")
	makeCall(nil, "classification", "unknown", "", "", "success")
	for _, date := range []string{"2026-09-30T14:59:59.999999Z", "2026-10-31T15:00:00Z"} {
		id := makeCall(&f.account, "classification", "estimated", "100", "USD", "success")
		if _, err := f.runtime.pool.Exec(ctx, `UPDATE ai_calls SET started_at=$2::timestamptz WHERE id=$1`, id, date); err != nil {
			t.Fatal(err)
		}
	}
	filter := airuntime.CostFilter{Month: "2026-10", UserID: f.owner}
	report, err := f.runtime.Costs(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(report.Users[0])
	var user struct {
		Calls    int64 `json:"calls"`
		Accounts []struct {
			AccountID *string            `json:"account_id"`
			Label     string             `json:"label"`
			Calls     int64              `json:"calls"`
			Unknown   int64              `json:"unknown_calls"`
			Failures  int64              `json:"failures"`
			Totals    []airuntime.Totals `json:"totals"`
			Stages    []struct {
				Stage  string             `json:"stage"`
				Totals []airuntime.Totals `json:"totals"`
			} `json:"stages"`
		} `json:"mail_accounts"`
	}
	_ = json.Unmarshal(raw, &user)
	if user.Calls != 4 || len(user.Accounts) != 3 {
		t.Fatal("account groups missing", string(raw))
	}
	if user.Accounts[len(user.Accounts)-1].AccountID != nil {
		t.Fatal("unattributed account must follow known accounts")
	}
	assertAmounts := func(totals []airuntime.Totals, want map[string]string) {
		t.Helper()
		if len(totals) != len(want) {
			t.Fatal("currency group count", totals)
		}
		for _, total := range totals {
			currency := "unknown"
			if total.Currency != nil {
				currency = *total.Currency
			}
			if amount, ok := want[currency]; !ok || amount != total.Amount {
				t.Fatal("wrong currency amount", currency, total.Amount)
			}
		}
	}
	assertAmounts(report.Users[0].Totals, map[string]string{"unknown": "0", "USD": "1", "EUR": "2"})
	var accountCalls, accountUnknown, accountFailures int64
	for _, a := range user.Accounts {
		accountCalls += a.Calls
		accountUnknown += a.Unknown
		accountFailures += a.Failures
		switch {
		case a.AccountID == nil:
			if a.Label != "" || a.Calls != 1 || a.Unknown != 1 {
				t.Fatal("unknown account", a)
			}
		case *a.AccountID == f.account:
			if a.Label != "Synthetic" || a.Calls != 2 || len(a.Totals) != 2 || len(a.Stages) != 2 {
				t.Fatal("known account", a)
			}
			assertAmounts(a.Totals, map[string]string{"USD": "1", "EUR": "2"})
			for _, stage := range a.Stages {
				if stage.Stage == "classification" {
					assertAmounts(stage.Totals, map[string]string{"USD": "1"})
				} else if stage.Stage == "extraction" {
					assertAmounts(stage.Totals, map[string]string{"EUR": "2"})
				} else {
					t.Fatal("unexpected stage", stage.Stage)
				}
			}
		case *a.AccountID == second:
			if a.Calls != 1 || a.Unknown != 1 || a.Failures != 1 {
				t.Fatal("disconnected account", a)
			}
		default:
			t.Fatal("unexpected account")
		}
	}
	if accountCalls != report.Users[0].Calls || accountUnknown != report.Users[0].UnknownCalls || accountFailures != report.Users[0].Failures {
		t.Fatal("account and user counters differ")
	}
	// Every user page includes account rows belonging only to that page.
	for offset := 0; offset < 3; offset++ {
		paged, e := f.runtime.Costs(ctx, airuntime.CostFilter{Month: "2026-10", Limit: 1, Offset: offset})
		if e != nil || len(paged.Users) != 1 {
			t.Fatal("user pagination", e)
		}
		u := paged.Users[0]
		if u.UserID == f.owner && len(u.MailAccounts) != 3 || u.UserID != f.owner && len(u.MailAccounts) != 0 {
			t.Fatal("accounts crossed user page")
		}
	}
	empty, e := f.runtime.Costs(ctx, airuntime.CostFilter{Month: "2026-08", UserID: f.owner, AccountID: f.account})
	if e != nil || len(empty.Users) != 1 || empty.Users[0].Calls != 0 || len(empty.Users[0].MailAccounts) != 0 {
		t.Fatal("empty account month", e)
	}
	for _, account := range []string{f.account, second, "unattributed"} {
		filter.AccountID = account
		report, err = f.runtime.Costs(ctx, filter)
		if err != nil {
			t.Fatal(err)
		}
		page, err := f.runtime.Calls(ctx, filter)
		if err != nil || int64(page.Total) != report.Users[0].Calls || len(report.Users[0].MailAccounts) != 1 {
			t.Fatal("filter populations differ", err)
		}
		for _, c := range page.Calls {
			if account == "unattributed" && c.MailAccountID != nil || account != "unattributed" && (c.MailAccountID == nil || *c.MailAccountID != account) {
				t.Fatal("wrong account in calls")
			}
		}
	}
	filter.AccountID = f.account
	filter.Stage = "classification"
	filtered, err := f.runtime.Costs(ctx, filter)
	if err != nil || filtered.Users[0].Calls != 1 || filtered.Users[0].MailAccounts[0].Totals[0].Amount != "1" {
		t.Fatal("stage/month filter", err)
	}
	filter.Stage = ""
	filter.Limit = 1
	filter.Offset = 1
	page, err := f.runtime.Calls(ctx, filter)
	if err != nil || page.Total != 2 || len(page.Calls) != 1 {
		t.Fatal("account pagination", err)
	}
	filter.Offset = 0
	filter.AccountID = uuid.NewString()
	if _, err = f.runtime.Costs(ctx, filter); !errors.Is(err, airuntime.ErrInvalid) {
		t.Fatal("missing account accepted", err)
	}
	filter.UserID = f.other
	filter.AccountID = f.account
	if _, err = f.runtime.Costs(ctx, filter); !errors.Is(err, airuntime.ErrInvalid) {
		t.Fatal("cross owner cost filter", err)
	}
	if _, err = f.runtime.Calls(ctx, filter); !errors.Is(err, airuntime.ErrInvalid) {
		t.Fatal("cross owner call filter", err)
	}
}

func TestAIRuntimeMailAccountMigrationBackfill(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	l, _ := processingBatch(t, f, 1)
	var item string
	if err := f.runtime.pool.QueryRow(ctx, `SELECT id FROM mail_processing_items WHERE run_id=$1`, l.RunID).Scan(&item); err != nil {
		t.Fatal(err)
	}
	c, err := f.runtime.BeginCall(ctx, airuntime.BeginCallInput{OperationID: item, ProfileID: f.in.ProfileID, Scope: "user", ConsumingUserID: &f.owner, Stage: "classification", AttemptNo: 1})
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.runtime.BeginCall(ctx, airuntime.BeginCallInput{OperationID: item, ProfileID: f.in.ProfileID, Scope: "user", ConsumingUserID: &f.other, Stage: "extraction", AttemptNo: 1})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", f.runtime.pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	files, _ := fs.Sub(migrations, "migrations")
	p, err := goose.NewProvider(goose.DialectPostgres, db, files)
	if err != nil {
		t.Fatal(err)
	}
	down, err := p.DownTo(ctx, 25)
	if err != nil || len(down) != 1 || down[0].Source.Version != 26 || down[0].Direction != "down" {
		t.Fatal("migration down", down, err)
	}
	up, err := p.Up(ctx)
	assertMigrationRange(t, up, err, 26, 26)
	var account *string
	if err = f.runtime.pool.QueryRow(ctx, `SELECT mail_account_id FROM ai_calls WHERE id=$1`, c.ID).Scan(&account); err != nil || account == nil || *account != f.account {
		t.Fatal("legacy backfill", err)
	}
	if err = f.runtime.pool.QueryRow(ctx, `SELECT mail_account_id FROM ai_calls WHERE id=$1`, other.ID).Scan(&account); err != nil || account != nil {
		t.Fatal("cross owner backfill", err)
	}
}

func TestAIRuntimeMailAccountDowngradeLocksReconstructionSource(t *testing.T) {
	f := processingSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", f.runtime.pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, files)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := f.runtime.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err = blocker.Exec(ctx, `LOCK TABLE ai_calls IN SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, e := p.DownTo(ctx, 25); done <- e }()
	for {
		var waiting bool
		err = f.runtime.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks l JOIN pg_class c ON c.oid=l.relation JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND c.relname='ai_calls' AND l.mode='AccessExclusiveLock' AND NOT l.granted)`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("downgrade did not reach calls lock")
		case <-time.After(5 * time.Millisecond):
		}
	}
	probe, err := f.runtime.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, lockErr := probe.Exec(ctx, `LOCK TABLE mail_processing_items IN ROW EXCLUSIVE MODE NOWAIT`)
	_ = probe.Rollback(ctx)
	_ = blocker.Rollback(ctx)
	if err = <-done; err != nil {
		t.Fatal("downgrade", err)
	}
	if lockErr == nil {
		t.Fatal("downgrade allowed concurrent deletion of reconstruction source")
	}
}
