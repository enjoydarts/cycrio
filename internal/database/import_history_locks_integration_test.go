//go:build integration

package database

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"testing"
	"time"
)

func TestImportHistoryReadAllowsWorkerTransitionsAndFencesPurge(t *testing.T) {
	f := processingSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	blocker, err := f.runtime.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err = blocker.Exec(ctx, `LOCK TABLE mail_processing_items IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := f.repo.ImportHistory(ctx, f.owner, f.account, 20, 0); done <- err }()
	waitProcessingLock(t, f.runtime.pool, "WITH current_items AS%")
	transition, err := f.runtime.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer transition.Rollback(context.Background())
	if _, err = transition.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE NOWAIT`, f.owner); err != nil {
		t.Fatalf("履歴を読むだけでWorkerの所有者ロックを妨げた: %v", err)
	}
	var purge bool
	if err = transition.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, "user-erasure-fence/v1:"+f.owner).Scan(&purge); err != nil || purge {
		t.Fatalf("履歴参照中に個人データ削除の排他ロックを取得できた: %v %v", purge, err)
	}
	if err = transition.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = f.runtime.pool.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, "user-erasure-fence/v1:"+f.owner).Scan(&purge); err != nil || !purge {
		t.Fatalf("履歴参照後に削除ガードが解放されなかった: %v %v", purge, err)
	}
}

func TestImportHistoryChecksCurrentOwnerAfterErasureFenceWait(t *testing.T) {
	f := processingSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	purge, err := f.runtime.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer purge.Rollback(context.Background())
	if _, err = purge.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "user-erasure-fence/v1:"+f.owner); err != nil {
		t.Fatal(err)
	}
	if _, err = purge.Exec(ctx, `UPDATE users SET state='deleting' WHERE id=$1`, f.owner); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := f.repo.ImportHistory(ctx, f.owner, f.account, 20, 0); done <- err }()
	waitProcessingLock(t, f.runtime.pool, "SELECT pg_advisory_xact_lock_shared%")
	if err = purge.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, mailprocessing.ErrNotFound) {
		t.Fatalf("削除待ちの旧snapshotから履歴を返した: %v", err)
	}
}
