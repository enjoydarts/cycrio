//go:build integration

package database

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
)

func TestMailAPISyncEncryptedCursorResumeAndAccountDeduplication(t *testing.T) {
	_, pool, _, r, owner, _, account := syncFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE mail_accounts SET provider='gmail' WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	in := syncInput()
	if _, err := r.Request(ctx, owner, account, in); err != nil {
		t.Fatal(err)
	}
	lease, work := syncStart(t, r)
	if work.Page.Provider != "gmail" || work.Credential.Provider != "gmail" {
		t.Fatal("provider missing", work)
	}
	refs := syncRefs(1)
	refs[0].RemoteFingerprint = refs[0].Fingerprint
	envelope := secretstore.Envelope{KeyID: "test", Ciphertext: []byte("encrypted-cursor-one")}
	if err := r.Finish(ctx, lease, mailsync.Page{RemoteIDs: []string{"message"}, CursorEnvelope: envelope, Initialized: true}, refs, ""); err != nil {
		t.Fatal(err)
	}
	lease, work = syncStart(t, r)
	if work.Page.Cursor != "" || !bytes.Equal(work.Page.CursorEnvelope.Ciphertext, envelope.Ciphertext) {
		t.Fatal("cursor not resumed", work.Page)
	}
	if err := r.Finish(ctx, lease, mailsync.Page{CursorEnvelope: secretstore.Envelope{KeyID: "test", Ciphertext: []byte("encrypted-cursor-two")}, Initialized: true, Done: true}, nil, ""); err != nil {
		t.Fatal(err)
	}
	// A second selected label must reuse the existing encrypted source identity.
	second := uuid.NewString()
	if _, err := pool.Exec(ctx, `UPDATE mail_account_folders SET selected=false WHERE account_id=$1`, account); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selectable,available,selected) VALUES($1,$2,'another-label','',true,true,true)`, second, account); err != nil {
		t.Fatal(err)
	}
	in.RequestID = uuid.NewString()
	if _, err := r.Request(ctx, owner, account, in); err != nil {
		t.Fatal(err)
	}
	lease, _ = syncStart(t, r)
	repeated := syncRefs(1)
	repeated[0].Fingerprint = refs[0].Fingerprint
	repeated[0].RemoteFingerprint = refs[0].RemoteFingerprint
	if err := r.Finish(ctx, lease, mailsync.Page{RemoteIDs: []string{"message"}, CursorEnvelope: envelope, Initialized: true, Done: true}, repeated, ""); err != nil {
		t.Fatal(err)
	}
	var count int
	var message, folder, key string
	var cipher []byte
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM mail_discovered_messages WHERE account_id=$1`, account).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err := pool.QueryRow(ctx, `SELECT id,folder_id,key_id,ciphertext FROM mail_discovered_messages WHERE account_id=$1`, account).Scan(&message, &folder, &key, &cipher); err != nil || message != refs[0].ID || folder != work.FolderID || key != refs[0].Envelope.KeyID || !bytes.Equal(cipher, refs[0].Envelope.Ciphertext) {
		t.Fatal("source identity replaced", err)
	}
	// Manual range checkpoints are encrypted and survive a later run.
	if _, err := pool.Exec(ctx, `UPDATE mail_account_folders SET selected=(id=$2) WHERE account_id=$1`, account, work.FolderID); err != nil {
		t.Fatal(err)
	}
	in.RequestID = uuid.NewString()
	if _, err := r.Request(ctx, owner, account, in); err != nil {
		t.Fatal(err)
	}
	_, work = syncStart(t, r)
	if work.Page.CursorEnvelope.KeyID != "test" || string(work.Page.CursorEnvelope.Ciphertext) != "encrypted-cursor-two" {
		t.Fatal("manual durable cursor missing", work.Page)
	}
}

func TestMailAPIAutomationBootstrapCursorCopiesAndEmptyPagesAdvance(t *testing.T) {
	f, automation, in := automaticFixture(t)
	ctx := context.Background()
	pool := f.runtime.pool
	if _, err := pool.Exec(ctx, `UPDATE mail_accounts SET provider='outlook' WHERE id=$1`, f.account); err != nil {
		t.Fatal(err)
	}
	if _, err := automation.Save(ctx, f.owner, f.account, in); err != nil {
		t.Fatal(err)
	}
	if _, err := automation.Tick(ctx, 10); err != nil {
		t.Fatal(err)
	}
	r := NewMailSyncRepository(pool)
	lease, work := syncStart(t, r)
	if work.Page.Mode != "bootstrap" || work.Page.Provider != "outlook" {
		t.Fatal(work.Page)
	}
	first := secretstore.Envelope{KeyID: "test", Ciphertext: []byte("bootstrap-next")}
	if err := r.Finish(ctx, lease, mailsync.Page{Initialized: true, CursorEnvelope: first}, nil, ""); err != nil {
		t.Fatal(err)
	}
	lease, work = syncStart(t, r)
	if !bytes.Equal(work.Page.CursorEnvelope.Ciphertext, first.Ciphertext) {
		t.Fatal("bootstrap page cursor lost")
	}
	final := secretstore.Envelope{KeyID: "test", Ciphertext: []byte("delta-checkpoint")}
	if err := r.Finish(ctx, lease, mailsync.Page{Initialized: true, CursorEnvelope: final, Done: true}, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := automation.Tick(ctx, 10); err != nil {
		t.Fatal(err)
	}
	var key string
	var cipher []byte
	var initialized bool
	if err := pool.QueryRow(ctx, `SELECT cursor_key_id,cursor_ciphertext,initialized FROM mail_automation_progress WHERE account_id=$1 AND lane='incremental'`, f.account).Scan(&key, &cipher, &initialized); err != nil || key != final.KeyID || !bytes.Equal(cipher, final.Ciphertext) || !initialized {
		t.Fatal("delta checkpoint not copied", err)
	}
	if err := pool.QueryRow(ctx, `SELECT cursor_key_id,cursor_ciphertext FROM mail_automation_progress WHERE account_id=$1 AND lane='historical'`, f.account).Scan(&key, &cipher); err != nil || key != "" || len(cipher) != 0 {
		t.Fatal("historical inherited delta cursor", err)
	}
	lease, work = syncStart(t, r)
	if work.Page.Mode != "historical" {
		t.Fatal(work.Page)
	}
	if err := r.Finish(ctx, lease, mailsync.Page{Initialized: true, CursorEnvelope: secretstore.Envelope{KeyID: "test", Ciphertext: []byte("historical-end")}, Done: true}, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE mail_automations SET next_check_at=clock_timestamp()-interval '1 second' WHERE account_id=$1`, f.account); err != nil {
		t.Fatal(err)
	}
	if _, err := automation.Tick(ctx, 10); err != nil {
		t.Fatal(err)
	}
	lease, work = syncStart(t, r)
	if work.Page.Mode != "incremental" || !bytes.Equal(work.Page.CursorEnvelope.Ciphertext, final.Ciphertext) {
		t.Fatal("incremental cursor lost", work.Page)
	}
	if err := r.Finish(ctx, lease, mailsync.Page{Initialized: true, CursorEnvelope: secretstore.Envelope{KeyID: "test", Ciphertext: []byte("empty-delta-next")}}, nil, ""); err != nil {
		t.Fatal(err)
	}
	var pages, examined int
	if err := pool.QueryRow(ctx, `SELECT discovery_page_count,examined_count FROM mail_sync_runs WHERE id=$1`, lease.RunID).Scan(&pages, &examined); err != nil || pages != 1 || examined != 0 {
		t.Fatal(pages, examined, err)
	}
	lease, work = syncStart(t, r)
	if string(work.Page.CursorEnvelope.Ciphertext) != "empty-delta-next" {
		t.Fatal("empty page lost progress")
	}
	if err := r.Finish(ctx, lease, mailsync.Page{}, nil, "connection_failed"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE mail_automations SET retry_after=clock_timestamp()-interval '1 second' WHERE account_id=$1`, f.account); err != nil {
		t.Fatal(err)
	}
	if _, err := automation.Tick(ctx, 10); err != nil {
		t.Fatal(err)
	}
	_, work = syncStart(t, r)
	if string(work.Page.CursorEnvelope.Ciphertext) != "empty-delta-next" {
		t.Fatal("retry lost cursor")
	}
}

func TestMailAPIAutomationMapsExistingReferenceAcrossLabels(t *testing.T) {
	f, automation, in := automaticFixture(t)
	ctx := context.Background()
	pool := f.runtime.pool
	if _, err := pool.Exec(ctx, `UPDATE mail_accounts SET provider='gmail' WHERE id=$1`, f.account); err != nil {
		t.Fatal(err)
	}
	original := f.add(t, 1)[0]
	if _, err := pool.Exec(ctx, `UPDATE mail_discovered_messages SET remote_fingerprint=fingerprint WHERE id=$1`, original); err != nil {
		t.Fatal(err)
	}
	second := uuid.NewString()
	if _, err := pool.Exec(ctx, `UPDATE mail_account_folders SET selected=false WHERE id=$1`, f.folder); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selectable,available,selected) VALUES($1,$2,'other-label','',true,true,true)`, second, f.account); err != nil {
		t.Fatal(err)
	}
	if _, err := automation.Save(ctx, f.owner, f.account, in); err != nil {
		t.Fatal(err)
	}
	if _, err := automation.Tick(ctx, 10); err != nil {
		t.Fatal(err)
	}
	r := NewMailSyncRepository(pool)
	lease, _ := syncStart(t, r)
	cursor := secretstore.Envelope{KeyID: "test", Ciphertext: []byte("bootstrap-checkpoint")}
	if err := r.Finish(ctx, lease, mailsync.Page{Initialized: true, CursorEnvelope: cursor, Done: true}, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := automation.Tick(ctx, 10); err != nil {
		t.Fatal(err)
	}
	lease, work := syncStart(t, r)
	if work.FolderID != second || work.Page.Mode != "historical" {
		t.Fatal(work.Page)
	}
	refs := syncRefs(1)
	refs[0].Fingerprint = original
	refs[0].RemoteFingerprint = original
	if err := r.Finish(ctx, lease, mailsync.Page{RemoteIDs: []string{"remote"}, Initialized: true, CursorEnvelope: cursor, Done: true}, refs, ""); err != nil {
		t.Fatal(err)
	}
	var message, folder string
	if err := pool.QueryRow(ctx, `SELECT member.message_id,m.folder_id FROM mail_automation_messages member JOIN mail_discovered_messages m ON m.id=member.message_id WHERE member.account_id=$1`, f.account).Scan(&message, &folder); err != nil || message != original || folder != f.folder {
		t.Fatal("cross-label source identity not retained", message, folder, err)
	}
	if _, err := automation.Tick(ctx, 10); err != nil {
		t.Fatal(err)
	}
	_, processing := f.start(t)
	if processing.FolderID != f.folder || processing.FolderName != "other-label" {
		t.Fatal("eligible member did not preserve original AAD", processing.FolderID, processing.FolderName)
	}
}

func TestMailAPIProcessingRequiresSelectedDiscoveredMembership(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	pool := f.runtime.pool
	if _, err := pool.Exec(ctx, `UPDATE mail_accounts SET provider='gmail' WHERE id=$1`, f.account); err != nil {
		t.Fatal(err)
	}
	message := f.add(t, 1)[0]
	if _, err := pool.Exec(ctx, `UPDATE mail_discovered_messages SET remote_fingerprint=fingerprint WHERE id=$1`, message); err != nil {
		t.Fatal(err)
	}
	preview, err := f.repo.Preview(ctx, f.owner, f.account)
	if err != nil || preview.PendingCount != 0 {
		t.Fatal("undiscovered membership eligible", preview.PendingCount, err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO mail_message_folder_memberships(owner_id,account_id,message_id,folder_id) VALUES($1,$2,$3,$4)`, f.owner, f.account, message, f.folder); err != nil {
		t.Fatal(err)
	}
	preview, err = f.repo.Preview(ctx, f.owner, f.account)
	if err != nil || preview.PendingCount != 1 {
		t.Fatal(preview.PendingCount, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE mail_account_folders SET selected=false WHERE id=$1`, f.folder); err != nil {
		t.Fatal(err)
	}
	other := uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selectable,available,selected) VALUES($1,$2,'unrelated-label','',true,true,true)`, other, f.account); err != nil {
		t.Fatal(err)
	}
	preview, err = f.repo.Preview(ctx, f.owner, f.account)
	if err != nil || preview.PendingCount != 0 {
		t.Fatal("unrelated selected label granted access", preview.PendingCount, err)
	}
	if _, err = f.repo.Request(ctx, f.owner, f.account, f.in); !errors.Is(err, mailprocessing.ErrConflict) {
		t.Fatal("unselected membership sent to processing", err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO mail_message_folder_memberships(owner_id,account_id,message_id,folder_id) VALUES($1,$2,$3,$4)`, f.owner, f.account, message, other); err != nil {
		t.Fatal(err)
	}
	f.in.RequestID = uuid.NewString()
	if _, err = f.repo.Request(ctx, f.owner, f.account, f.in); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE mail_account_folders SET selected=false WHERE account_id=$1`, f.account); err != nil {
		t.Fatal(err)
	}
	jobs, err := f.repo.Claim(ctx)
	if err != nil || len(jobs) != 0 {
		t.Fatal("stale membership lease dispatched", jobs, err)
	}
}

func TestMailAPIProcessingLoadsProviderFromAccount(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_accounts SET provider='gmail' WHERE id=$1`, f.account); err != nil {
		t.Fatal(err)
	}
	message := f.add(t, 1)[0]
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_discovered_messages SET remote_fingerprint=fingerprint WHERE id=$1`, message); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO mail_message_folder_memberships(owner_id,account_id,message_id,folder_id) VALUES($1,$2,$3,$4)`, f.owner, f.account, message, f.folder); err != nil {
		t.Fatal(err)
	}
	for _, folder := range []struct {
		name                            string
		selected, available, selectable bool
	}{
		{"second-label", true, true, true}, {"not-selected", false, true, true}, {"unavailable", false, false, true}, {"not-selectable", false, true, false},
	} {
		id := uuid.NewString()
		if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selectable,available,selected) VALUES($1,$2,$3,'',$4,$5,$6)`, id, f.account, folder.name, folder.selectable, folder.available, folder.selected); err != nil {
			t.Fatal(err)
		}
		if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO mail_message_folder_memberships(owner_id,account_id,message_id,folder_id) VALUES($1,$2,$3,$4)`, f.owner, f.account, message, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.repo.Request(ctx, f.owner, f.account, f.in); err != nil {
		t.Fatal(err)
	}
	_, work := f.start(t)
	if work.Credential.Provider != "gmail" {
		t.Fatal("processing factory lost provider")
	}
	if len(work.ReaderFolders) != 2 {
		t.Fatal("reader did not receive every eligible membership", work.ReaderFolders)
	}
	seen := map[string]bool{}
	for _, folder := range work.ReaderFolders {
		seen[folder] = true
	}
	if !seen["INBOX"] || !seen["second-label"] || work.FolderID != f.folder {
		t.Fatal("eligible membership/AAD mismatch", work.ReaderFolders, work.FolderID)
	}
}

func TestMailAPIMigrationRoundTripPreservesUIDRecords(t *testing.T) {
	_, pool, _, r, owner, _, account := syncFixture(t)
	ctx := context.Background()
	if _, err := r.Request(ctx, owner, account, syncInput()); err != nil {
		t.Fatal(err)
	}
	lease, _ := syncStart(t, r)
	if err := r.Finish(ctx, lease, mailsync.Page{UIDValidity: 7, AfterUID: 1, UpperUID: 1, Done: true, UIDs: []uint32{1}}, syncRefs(1), ""); err != nil {
		t.Fatal(err)
	}
	const snapshot = `SELECT jsonb_build_array((SELECT jsonb_agg(to_jsonb(c)-'cursor_key_id'-'cursor_ciphertext' ORDER BY folder_id) FROM mail_sync_cursors c),(SELECT jsonb_agg(to_jsonb(m)-'remote_fingerprint' ORDER BY id) FROM mail_discovered_messages m),(SELECT jsonb_agg(to_jsonb(f)-'cursor_key_id'-'cursor_ciphertext' ORDER BY folder_id) FROM mail_sync_run_folders f))`
	var before, after string
	if err := pool.QueryRow(ctx, snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
	p := automationMigrationProvider(t, pool)
	if results, err := p.DownTo(ctx, 19); err != nil || len(results) != 1 || results[0].Source.Version != 20 {
		t.Fatal(results, err)
	}
	if err := pool.QueryRow(ctx, snapshot).Scan(&after); err != nil || before != after {
		t.Fatal("UID records changed during downgrade", err)
	}
	if results, err := p.Up(ctx); err != nil || len(results) != 1 || results[0].Source.Version != 20 {
		t.Fatal(results, err)
	}
	if err := pool.QueryRow(ctx, snapshot).Scan(&after); err != nil || before != after {
		t.Fatal("UID records changed during upgrade", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE mail_discovered_messages SET remote_fingerprint='opaque-fingerprint' WHERE account_id=$1`, account); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 19); err == nil || !strings.Contains(err.Error(), "API mail progress and references must be preserved") {
		t.Fatal("destructive API downgrade permitted", err)
	}
	var version int
	if err := pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != 20 {
		t.Fatal(version, err)
	}
}
