//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
)

func traceFixture(t *testing.T) (*PriceResearchRepository, *secretstore.Keyring, string, priceresearch.Lease, string, priceresearch.Execution) {
	t.Helper()
	r, _, actor, m, _ := researchFixture(t)
	ctx := context.Background()
	researchTestReady(t, r, actor, m)
	path := filepath.Join(t.TempDir(), "trace-keys.json")
	if err := secretstore.Initialize(path); err != nil {
		t.Fatal(err)
	}
	keys, err := secretstore.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	r = NewPriceResearchRepository(r.pool, keys)
	s, _ := r.State(ctx)
	_, err = r.Request(ctx, actor, "update", priceresearch.RunInput{ExpectedVersion: s.Version, TargetProvider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	ls, err := r.ClaimDue(ctx)
	if err != nil || len(ls) != 1 {
		t.Fatal(ls, err)
	}
	e, err := r.Prepare(ctx, ls[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Begin(ctx, ls[0]); err != nil {
		t.Fatal(err)
	}
	call, err := r.BeginCall(ctx, ls[0], 0)
	if err != nil {
		t.Fatal(err)
	}
	return r, keys, actor, ls[0], call, e
}

func traceResult() priceresearch.Result {
	r := researchResult()
	r.Trace = &priceresearch.TraceDocument{Request: json.RawMessage(`{"input":"synthetic trace prompt"}`), Response: `{"output":"synthetic trace response"}`, Validation: map[string]string{"future-model": "verified"}}
	return r
}

func TestResearchTraceEncryptedAtomicAndAdministratorOnly(t *testing.T) {
	r, _, actor, lease, call, _ := traceFixture(t)
	ctx := context.Background()
	result := traceResult()
	if err := r.FinishCall(ctx, lease, call, result, ""); err != nil {
		t.Fatal(err)
	}
	var ciphertext []byte
	var keyID string
	var captured, expires time.Time
	if err := r.pool.QueryRow(ctx, `SELECT key_id,ciphertext,captured_at,expires_at FROM research_call_traces WHERE call_id=$1`, call).Scan(&keyID, &ciphertext, &captured, &expires); err != nil {
		t.Fatal(err)
	}
	if keyID == "" || strings.Contains(string(ciphertext), "synthetic trace") || expires.Sub(captured) != 7*24*time.Hour {
		t.Fatal("trace encryption/TTL invalid")
	}
	list, err := r.TraceCalls(ctx, actor, lease.RunID)
	if err != nil || len(list) != 1 || list[0].CallID != call || !list[0].Available {
		t.Fatal(list, err)
	}
	view, err := r.Trace(ctx, actor, lease.RunID, call)
	if err != nil || view.Document.Response != result.Trace.Response || string(view.Document.Request) != string(result.Trace.Request) {
		t.Fatal(view, err)
	}
	if err = r.FinishCall(ctx, lease, call, researchResult(), ""); err != nil {
		t.Fatal(err)
	}
	empty := researchResult()
	empty.Trace = &priceresearch.TraceDocument{}
	if err = r.FinishCall(ctx, lease, call, empty, ""); err != nil {
		t.Fatal(err)
	}
	view, err = r.Trace(ctx, actor, lease.RunID, call)
	if err != nil || view.Document.Response != result.Trace.Response {
		t.Fatal("late empty erased trace", err)
	}
	for _, ids := range [][2]string{{"invalid", call}, {uuid.NewString(), call}, {lease.RunID, "invalid"}, {lease.RunID, uuid.NewString()}} {
		if _, err = r.Trace(ctx, actor, ids[0], ids[1]); !errors.Is(err, priceresearch.ErrNotFound) {
			t.Fatal("invalid/wrong-run trace must be absent", err)
		}
	}
	if _, err = r.TraceCalls(ctx, actor, "invalid"); !errors.Is(err, priceresearch.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = r.pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = r.TraceCalls(ctx, actor, lease.RunID); !errors.Is(err, priceresearch.ErrConflict) {
		t.Fatal("non-admin listed trace", err)
	}
	if _, err = r.Trace(ctx, actor, lease.RunID, call); !errors.Is(err, priceresearch.ErrConflict) {
		t.Fatal("non-admin read trace", err)
	}
}

func TestResearchTraceMissingKeyOrTamperingFailsClosed(t *testing.T) {
	r, _, actor, lease, call, _ := traceFixture(t)
	ctx := context.Background()
	withoutKey := NewPriceResearchRepository(r.pool)
	if err := withoutKey.FinishCall(ctx, lease, call, traceResult(), ""); err == nil {
		t.Fatal("stored trace without encryption key")
	}
	var details int
	var state string
	if err := r.pool.QueryRow(ctx, `SELECT status,(SELECT count(*) FROM research_call_costs WHERE call_id=$1) FROM ai_calls WHERE id=$1`, call).Scan(&state, &details); err != nil || state != "calling" || details != 0 {
		t.Fatal("partial cost commit", state, details, err)
	}
	if err := r.FinishCall(ctx, lease, call, traceResult(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := withoutKey.Trace(ctx, actor, lease.RunID, call); err == nil {
		t.Fatal("decrypted without keys")
	}
	var otherCall, otherRun string
	if err := r.pool.QueryRow(ctx, `SELECT id,operation_id FROM ai_calls WHERE operation_id<>$1 AND stage='price_research' LIMIT 1`, lease.RunID).Scan(&otherCall, &otherRun); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO research_call_traces(call_id,key_id,ciphertext,captured_at,expires_at) SELECT $2,key_id,ciphertext,captured_at,expires_at FROM research_call_traces WHERE call_id=$1`, call, otherCall); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Trace(ctx, actor, otherRun, otherCall); err == nil {
		t.Fatal("ciphertext was not bound to call identity")
	}
	if _, err := r.pool.Exec(ctx, `UPDATE research_call_traces SET ciphertext=set_byte(ciphertext,0,get_byte(ciphertext,0)#1) WHERE call_id=$1`, call); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Trace(ctx, actor, lease.RunID, call); err == nil {
		t.Fatal("accepted tampered trace")
	}
}

func TestResearchTraceLegacyCompleteAndPayloadBound(t *testing.T) {
	t.Run("legacy_complete", func(t *testing.T) {
		r, _, actor, lease, call, execution := traceFixture(t)
		ctx := context.Background()
		if err := r.Complete(ctx, lease, execution, traceResult(), ""); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Trace(ctx, actor, lease.RunID, call); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("oversized", func(t *testing.T) {
		r, _, _, lease, call, _ := traceFixture(t)
		ctx := context.Background()
		result := traceResult()
		result.Trace.Response = strings.Repeat("x", 3<<20)
		if err := r.FinishCall(ctx, lease, call, result, ""); err == nil {
			t.Fatal("accepted oversized encrypted document")
		}
		var count int
		if err := r.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM research_call_traces WHERE call_id=$1)+(SELECT count(*) FROM research_call_costs WHERE call_id=$1)`, call).Scan(&count); err != nil || count != 0 {
			t.Fatal("oversized trace partially committed", count, err)
		}
	})
}

func TestResearchTraceExpirationDeletesCiphertext(t *testing.T) {
	r, _, actor, lease, call, _ := traceFixture(t)
	ctx := context.Background()
	if err := r.FinishCall(ctx, lease, call, traceResult(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE research_call_traces SET captured_at=t.at-interval '8 days',expires_at=t.at-interval '1 day' FROM (SELECT clock_timestamp() AS at) t WHERE call_id=$1`, call); err != nil {
		t.Fatal(err)
	}
	list, err := r.TraceCalls(ctx, actor, lease.RunID)
	if err != nil || len(list) != 1 || list[0].Available || list[0].CapturedAt != nil || list[0].ExpiresAt != nil {
		t.Fatal(list, err)
	}
	var count int
	if err = r.pool.QueryRow(ctx, `SELECT count(*) FROM research_call_traces WHERE call_id=$1`, call).Scan(&count); err != nil || count != 0 {
		t.Fatal("expired ciphertext retained", count, err)
	}
	if _, err = r.Trace(ctx, actor, lease.RunID, call); !errors.Is(err, priceresearch.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestResearchTraceMigrationAndKeyringGuard(t *testing.T) {
	r, _, _, lease, call, _ := traceFixture(t)
	ctx := context.Background()
	p := automationMigrationProvider(t, r.pool)
	down, err := p.DownTo(ctx, 43)
	assertMigrationRange(t, down, err, latestMigrationVersion, 44)
	up, err := p.Up(ctx)
	assertMigrationRange(t, up, err, 44, latestMigrationVersion)
	if err = r.FinishCall(ctx, lease, call, traceResult(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err = p.DownTo(ctx, 43); err == nil {
		t.Fatal("discarded retained encrypted trace")
	}
	// Isolate this new guard branch in the test-only schema while preserving
	// the existing credential FK target by its PostgreSQL relation identity.
	if _, err = r.pool.Exec(ctx, `ALTER TABLE credential_versions RENAME TO trace_saved_credentials; CREATE TABLE credential_versions(id uuid)`); err != nil {
		t.Fatal(err)
	}
	if present, err := NewCredentialRepository(r.pool).HasEncryptedCredentials(ctx); err != nil || !present {
		t.Fatal("trace-only ciphertext did not protect keyring", present, err)
	}
	if _, err = r.pool.Exec(ctx, `DELETE FROM research_call_traces`); err != nil {
		t.Fatal(err)
	}
	if present, err := NewCredentialRepository(r.pool).HasEncryptedCredentials(ctx); err != nil || present {
		t.Fatal("trace-only guard fixture was not isolated", present, err)
	}
}
