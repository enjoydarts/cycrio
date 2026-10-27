//go:build integration

package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailautomation"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/google/uuid"
	"testing"
)

type automaticSourceReader struct {
	t     *testing.T
	code  string
	calls int
}

func (r *automaticSourceReader) ReadText(_ context.Context, _ string, _ []byte, source mailcontent.Source) ([]byte, error) {
	r.calls++
	if !source.StrictGeneration {
		r.t.Error("automatic source generation guard missing")
	}
	return nil, mailaccounts.AdapterError{Code: r.code}
}
func TestMailAutomationProcessingFirstSourceFailuresPauseBeforeMoreMessagesOrCalls(t *testing.T) {
	for _, code := range []string{"uid_validity_changed", "authentication_failed", "credentials_unavailable"} {
		t.Run(code, func(t *testing.T) {
			f, r, in := automaticFixture(t)
			ctx := context.Background()
			v := automaticBootstrap(t, f, r, in, 2)
			refs := syncRefs(2)
			for i := range refs {
				var e error
				refs[i].Envelope, e = f.keys.Seal([]byte(`{"UIDValidity":77,"UID":1}`), mailsync.MessageAAD(mailsync.Work{OwnerID: f.owner, Run: mailsync.Run{AccountID: f.account}, FolderID: f.folder}, refs[i].ID))
				if e != nil {
					t.Fatal(e)
				}
			}
			automaticDiscover(t, f, r, refs, 2)
			reader := &automaticSourceReader{t: t, code: code}
			inference := processingInference(func(context.Context, ai.Request) (ai.Response, error) {
				t.Error("source failure reached AI")
				return ai.Response{}, nil
			})
			service := mailprocessing.NewService(f.repo, f.keys, reader, inference, func(context.Context, string) ([]byte, error) { return []byte("synthetic"), nil })
			leases, e := service.Claim(ctx)
			if e != nil || len(leases) != 1 {
				t.Fatal(leases, e)
			}
			if e = service.Execute(ctx, leases[0]); e != nil {
				t.Fatal(e)
			}
			got, e := r.Read(ctx, f.owner, f.account)
			if e != nil || got.State != "needs_confirmation" || got.Reason != code || got.Ready || !got.PreparationReady || got.PreparationBlockedReasons == nil || len(got.PreparationBlockedReasons) != 0 || got.Generation != v.Generation {
				t.Fatal(got, e)
			}
			if _, e = r.Tick(ctx, 20); e != nil {
				t.Fatal(e)
			}
			leases, e = service.Claim(ctx)
			if e != nil || len(leases) != 0 || reader.calls != 1 {
				t.Fatal("remaining messages continued", leases, reader.calls, e)
			}
			var failed, cancelled, calls int
			if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE state='failed'),count(*) FILTER(WHERE state='cancelled'),(SELECT count(*) FROM ai_calls WHERE scope='user') FROM mail_processing_items`).Scan(&failed, &cancelled, &calls); e != nil || failed != 1 || cancelled != 1 || calls != 0 {
				t.Fatal("source failure mass-failed or sent data", failed, cancelled, calls, e)
			}
			got, e = r.Stop(ctx, f.owner, f.account, mailautomation.Control{ExpectedSettingsVersion: got.Version, RequestID: uuid.NewString()})
			if e != nil || got.State != "needs_confirmation" {
				t.Fatal(got, e)
			}
			if _, e = r.Resume(ctx, f.owner, f.account, mailautomation.Control{ExpectedSettingsVersion: got.Version, RequestID: uuid.NewString()}); !errors.Is(e, mailautomation.ErrConflict) {
				t.Fatal("source recovery revived old consent", e)
			}
			in.ExpectedSettingsVersion = got.Version
			in.RequestID = uuid.NewString()
			fresh, e := r.Save(ctx, f.owner, f.account, in)
			if e != nil || !fresh.PreparationReady || !fresh.Ready || fresh.Generation != v.Generation+1 {
				t.Fatal("fresh confirmation blocked by old scope", fresh, e)
			}
		})
	}
}
func TestMailAutomationPreparationReadinessUsesCurrentBasisBeforeOldScopeBlocks(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	initial, e := r.Read(ctx, f.owner, f.account)
	if e != nil || !initial.PreparationReady || initial.PreparationBlockedReasons == nil {
		t.Fatal(initial, e)
	}
	v, e := r.Save(ctx, f.owner, f.account, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.runtime.pool.Exec(ctx, `UPDATE mail_accounts SET version=version+1 WHERE id=$1`, f.account); e != nil {
		t.Fatal(e)
	}
	got, e := r.Read(ctx, f.owner, f.account)
	if e != nil || got.Ready || !got.PreparationReady || got.State != "needs_confirmation" || len(got.PreparationBlockedReasons) != 0 {
		t.Fatal("saved basis contaminated preparation", got, e)
	}
	in.ExpectedSettingsVersion = v.Version
	in.ExpectedAccountVersion = got.Basis.AccountVersion
	in.RequestID = uuid.NewString()
	fresh, e := r.Save(ctx, f.owner, f.account, in)
	if e != nil || !fresh.Ready || !fresh.PreparationReady || fresh.Generation != 2 {
		t.Fatal(fresh, e)
	}
}

func TestMailAutomationPreparationReadinessAllowsReplacingActiveAutomaticScope(t *testing.T) {
	f, r, in := automaticFixture(t)
	ctx := context.Background()
	v := automaticBootstrap(t, f, r, in, 1)
	automaticDiscover(t, f, r, syncRefs(1), 1)
	preview, e := f.repo.Preview(ctx, f.owner, f.account)
	if e != nil || preview.Ready || preview.BlockedReason != "processing_active" {
		t.Fatal(preview, e)
	}
	current, e := r.Read(ctx, f.owner, f.account)
	if e != nil || !current.PreparationReady || len(current.PreparationBlockedReasons) != 0 {
		t.Fatal("active snapshot blocked new consent preparation", current, e)
	}
	in.ExpectedSettingsVersion = v.Version
	in.Before = "2026-01-02"
	in.RequestID = uuid.NewString()
	fresh, e := r.Save(ctx, f.owner, f.account, in)
	if e != nil || !fresh.PreparationReady || fresh.Generation != v.Generation+1 {
		t.Fatal(fresh, e)
	}
	var active int
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM mail_processing_runs WHERE automation_id=$1 AND state IN ('queued','leased','running')`, v.ID).Scan(&active); e != nil || active != 0 {
		t.Fatal("old automatic snapshot survived scope replacement", active, e)
	}
}
