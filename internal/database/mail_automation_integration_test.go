//go:build integration

package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailautomation"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/google/uuid"
	"testing"
)

func automaticFixture(t *testing.T) (processingFixture, *MailAutomationRepository, mailautomation.Input) {
	t.Helper()
	f := processingSetup(t)
	s := fullEmailPolicy(t, f, true, 1)
	in := mailautomation.Input{ExpectedAccountVersion: 1, RuntimeVersion: f.in.RuntimeVersion, ProfileID: f.in.ProfileID, PolicyVersion: s.Policy.Version, Since: "2025-01-01", Before: "2026-01-01", FullEmail: true, IntervalSeconds: 300, Confirmed: true, AutoRegister: true, RequestID: uuid.NewString()}
	return f, NewMailAutomationRepository(f.runtime.pool), in
}

func automaticBootstrap(t *testing.T, f processingFixture, r *MailAutomationRepository, in mailautomation.Input, upper uint32) mailautomation.State {
	t.Helper()
	ctx := context.Background()
	v, e := r.Save(ctx, f.owner, f.account, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Tick(ctx, 10); e != nil {
		t.Fatal(e)
	}
	sync := NewMailSyncRepository(f.runtime.pool)
	l, w := syncStart(t, sync)
	if w.Page.Mode != "bootstrap" {
		t.Fatal(w.Page)
	}
	if e = sync.Finish(ctx, l, mailsync.Page{UIDValidity: 77, UpperUID: upper, Done: true, Initialized: true}, nil, ""); e != nil {
		t.Fatal(e)
	}
	return v
}
func automaticDiscover(t *testing.T, f processingFixture, r *MailAutomationRepository, refs []mailsync.MessageRef, upper uint32) {
	t.Helper()
	ctx := context.Background()
	if _, e := r.Tick(ctx, 10); e != nil {
		t.Fatal(e)
	}
	sync := NewMailSyncRepository(f.runtime.pool)
	l, w := syncStart(t, sync)
	if w.Page.Mode != "historical" {
		t.Fatal(w.Page)
	}
	uids := []uint32{}
	for i := range refs {
		uids = append(uids, uint32(i+1))
	}
	if e := sync.Finish(ctx, l, mailsync.Page{UIDValidity: 77, UpperUID: upper, AfterUID: upper, UIDs: uids, Done: true, Initialized: true}, refs, ""); e != nil {
		t.Fatal(e)
	}
	if _, e := r.Tick(ctx, 10); e != nil {
		t.Fatal(e)
	}
}
func TestMailAutomationMembershipExcludesOutsideAndAddsDeduplicatedIdentity(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	existing := f.add(t, 2)
	var fingerprint, key string
	var ciphertext []byte
	if e := f.runtime.pool.QueryRow(ctx, `SELECT fingerprint,key_id,ciphertext FROM mail_discovered_messages WHERE id=$1`, existing[1]).Scan(&fingerprint, &key, &ciphertext); e != nil {
		t.Fatal(e)
	}
	automaticBootstrap(t, f, r, in, 10)
	refs := syncRefs(1)
	refs[0].Fingerprint = fingerprint
	automaticDiscover(t, f, r, refs, 10)
	var member string
	var unchanged bool
	if e := f.runtime.pool.QueryRow(ctx, `SELECT a.message_id,m.key_id=$1 AND m.ciphertext=$2 FROM mail_automation_messages a JOIN mail_discovered_messages m ON m.id=a.message_id`, key, ciphertext).Scan(&member, &unchanged); e != nil || member != existing[1] || !unchanged {
		t.Fatal(member, unchanged, e)
	}
	var queued int
	var selected string
	if e := f.runtime.pool.QueryRow(ctx, `SELECT count(*),min(message_id::text) FROM mail_processing_items`).Scan(&queued, &selected); e != nil || queued != 1 || selected != existing[1] {
		t.Fatal("outside period manual mail selected", queued, selected, e)
	}
}
func TestMailAutomationStopCancelsUnsentButKeepsLateCostAndNeverManual(t *testing.T) {
	for _, sent := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsent", true: "sent"}[sent], func(t *testing.T) {
			f, r, in := automaticFixture(t)
			ctx := context.Background()
			v := automaticBootstrap(t, f, r, in, 10)
			automaticDiscover(t, f, r, syncRefs(1), 10)
			l, _ := f.start(t)
			var call airuntime.Call
			var e error
			if sent {
				call, e = f.repo.BeginCall(ctx, l, "classification")
				if e != nil {
					t.Fatal(e)
				}
			}
			v, e = r.Stop(ctx, f.owner, f.account, mailautomation.Control{ExpectedSettingsVersion: v.Version, RequestID: uuid.NewString()})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.repo.BeginCall(ctx, l, "classification"); !errors.Is(e, mailprocessing.ErrNotFound) {
				t.Fatal("stopped run called", e)
			}
			var state string
			if e = f.runtime.pool.QueryRow(ctx, `SELECT state FROM mail_processing_items WHERE run_id=$1`, l.RunID).Scan(&state); e != nil || state != map[bool]string{false: "cancelled", true: "unknown"}[sent] {
				t.Fatal(state, e)
			}
			if _, e = r.Resume(ctx, f.owner, f.account, mailautomation.Control{ExpectedSettingsVersion: v.Version, RequestID: uuid.NewString()}); e != nil {
				t.Fatal(e)
			}
			if _, e = f.repo.BeginCall(ctx, l, "classification"); !errors.Is(e, mailprocessing.ErrNotFound) {
				t.Fatal("stale run revived", e)
			}
			if sent {
				if _, e = f.repo.FinishCall(ctx, call.ID, airuntime.CallUpdate{Outcome: "success", Cost: ai.Cost{Status: "unknown"}}); e != nil {
					t.Fatal("late ledger rejected", e)
				}
			}
		})
	}
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	f.add(t, 1)
	f.in.PolicyVersion = in.PolicyVersion
	manual, e := f.repo.Request(ctx, f.owner, f.account, f.in)
	if e != nil {
		t.Fatal(e)
	}
	v, e := r.Save(ctx, f.owner, f.account, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Stop(ctx, f.owner, f.account, mailautomation.Control{ExpectedSettingsVersion: v.Version, RequestID: uuid.NewString()}); e != nil {
		t.Fatal(e)
	}
	var state string
	if e = f.runtime.pool.QueryRow(ctx, `SELECT state FROM mail_processing_runs WHERE id=$1`, manual.ID).Scan(&state); e != nil || state != "queued" {
		t.Fatal("manual cancelled", state, e)
	}
}
func TestMailAutomationDraftVersionDoesNotInvalidateSavedConsent(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	automaticBootstrap(t, f, r, in, 10)
	automaticDiscover(t, f, r, syncRefs(1), 10)
	// A draft-only update changes the settings counter, keeping the active basis.
	if _, e := f.runtime.pool.Exec(ctx, `UPDATE ai_runtime_settings SET version=version+1 WHERE singleton`); e != nil {
		t.Fatal(e)
	}
	l, _ := f.start(t)
	if _, e := f.repo.BeginCall(ctx, l, "classification"); e != nil {
		t.Fatal("draft update revoked stable active consent", e)
	}
}
func TestMailAutomationConsentCASReplayIsolationAndStopResume(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	v, e := r.Read(ctx, f.owner, f.account)
	if e != nil || v.Version != 0 || v.State != "not_configured" || !v.Ready {
		t.Fatal(v, e)
	}
	if _, e = r.Save(ctx, f.other, f.account, in); !errors.Is(e, mailautomation.ErrNotFound) {
		t.Fatal("ownership", e)
	}
	bad := in
	bad.Confirmed = false
	if _, e = r.Save(ctx, f.owner, f.account, bad); !errors.Is(e, mailautomation.ErrInvalid) {
		t.Fatal("consent", e)
	}
	v, e = r.Save(ctx, f.owner, f.account, in)
	if e != nil || v.Version != 1 || v.Generation != 1 || v.State != "enabled" {
		t.Fatal(v, e)
	}
	replay, e := r.Save(ctx, f.owner, f.account, in)
	if e != nil || replay.Version != 1 || replay.ID != v.ID {
		t.Fatal("replay", replay, e)
	}
	bad = in
	bad.Before = "2026-01-02"
	if _, e = r.Save(ctx, f.owner, f.account, bad); !errors.Is(e, mailautomation.ErrConflict) {
		t.Fatal("nonce", e)
	}
	var enabled bool
	if e = f.runtime.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subscription_automation_preferences WHERE owner_id=$1 AND enabled)`, f.owner).Scan(&enabled); e != nil || enabled {
		t.Fatal(enabled, e)
	}
	stop := mailautomation.Control{ExpectedSettingsVersion: 1, RequestID: uuid.NewString()}
	v, e = r.Stop(ctx, f.owner, f.account, stop)
	if e != nil || v.State != "stopped" || v.Version != 2 || v.Generation != 1 {
		t.Fatal(v, e)
	}
	v, e = r.Resume(ctx, f.owner, f.account, mailautomation.Control{ExpectedSettingsVersion: 2, RequestID: uuid.NewString()})
	if e != nil || v.State != "enabled" || v.Generation != 1 || v.Version != 3 {
		t.Fatal(v, e)
	}
	replay, e = r.Stop(ctx, f.owner, f.account, stop)
	if e != nil || replay.Version != 2 || replay.State != "stopped" {
		t.Fatal("lost initial result", replay, e)
	}
}
func TestMailAutomationBootstrapEmptyFolderAndManualListIsolation(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	v, e := r.Save(ctx, f.owner, f.account, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Tick(ctx, 10); e != nil {
		t.Fatal(e)
	}
	sync := NewMailSyncRepository(f.runtime.pool)
	l, w := syncStart(t, sync)
	if w.Page.Mode != "bootstrap" || w.Page.Since != "" || w.Page.Before != "" {
		t.Fatal("bootstrap must have no date/search/body", w.Page)
	}
	if e = sync.Finish(ctx, l, mailsync.Page{UIDValidity: 77, Initialized: true, Done: true, UIDs: []uint32{}}, nil, ""); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Tick(ctx, 10); e != nil {
		t.Fatal(e)
	}
	_, w = syncStart(t, sync)
	if w.Page.Mode != "historical" || !w.Page.Initialized || w.Page.UpperUID != 0 || w.Page.UIDValidity != 77 {
		t.Fatal("empty snapshot resampled", w.Page)
	}
	runs, e := sync.List(ctx, f.owner, f.account)
	if e != nil || len(runs) != 0 {
		t.Fatal("legacy list includes automatic", runs, e)
	}
	got, e := r.Read(ctx, f.owner, f.account)
	if e != nil || got.ID != v.ID || !got.Progress.BootstrapComplete {
		t.Fatal(got, e)
	}
}
