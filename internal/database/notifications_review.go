package database

import (
	"context"
	"time"

	"github.com/enjoydarts/cycrio/internal/notifications"
	"github.com/jackc/pgx/v5"
)

func ensureNotificationReviewState(ctx context.Context, tx pgx.Tx, owner string) error {
	_, err := tx.Exec(ctx, `INSERT INTO notification_review_state(owner_id,baseline_at,in_app_since) SELECT id,created_at,created_at FROM users WHERE id=$1 ON CONFLICT(owner_id) DO NOTHING`, owner)
	return notificationError(err)
}

func updateNotificationReviewChannels(ctx context.Context, tx pgx.Tx, owner string, prior, next notifications.Preferences, now time.Time) error {
	if err := ensureNotificationReviewState(ctx, tx, owner); err != nil {
		return err
	}
	before, after := prior.Kinds[notifications.PendingReview], next.Kinds[notifications.PendingReview]
	_, err := tx.Exec(ctx, `UPDATE notification_review_state SET
 in_app_since=CASE WHEN NOT $2 THEN NULL WHEN NOT $3 THEN $8 ELSE in_app_since END,
 email_since=CASE WHEN NOT $4 THEN NULL WHEN NOT $5 THEN $8 ELSE email_since END,
 push_since=CASE WHEN NOT $6 THEN NULL WHEN NOT $7 THEN $8 ELSE push_since END WHERE owner_id=$1`, owner, after.InApp, before.InApp, after.Email && next.EmailOptIn, before.Email && prior.EmailOptIn, after.Push && next.PushOptIn, before.Push && prior.PushOptIn, now)
	return notificationError(err)
}

// 完了runと元情報の一意制約で、再解析や設定切替による過去分の再送を防ぐ。
func observeNotificationReviews(ctx context.Context, tx pgx.Tx, owner string, p notifications.Preferences, now time.Time) error {
	if err := ensureNotificationReviewState(ctx, tx, owner); err != nil {
		return err
	}
	type run struct {
		id, account string
		completed   time.Time
		channels    notifications.Channels
	}
	runs := []run{}
	rows, err := tx.Query(ctx, `SELECT r.id,r.account_id,coalesce(r.finished_at,r.created_at),
 coalesce(coalesce(r.finished_at,r.created_at)>=s.in_app_since,false),
 coalesce(coalesce(r.finished_at,r.created_at)>=s.email_since,false),
 coalesce(coalesce(r.finished_at,r.created_at)>=s.push_since,false)
 FROM notification_review_pending pending JOIN mail_processing_runs r ON r.owner_id=pending.owner_id AND r.id=pending.run_id JOIN notification_review_state s ON s.owner_id=r.owner_id
 WHERE pending.owner_id=$1 AND r.state='completed' AND pending.completed_at<=$2
 ORDER BY pending.completed_at,pending.run_id LIMIT 50`, owner, now)
	if err != nil {
		return notificationError(err)
	}
	for rows.Next() {
		var r run
		if err = rows.Scan(&r.id, &r.account, &r.completed, &r.channels.InApp, &r.channels.Email, &r.channels.Push); err != nil {
			rows.Close()
			return notificationError(err)
		}
		runs = append(runs, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return notificationError(err)
	}
	for _, r := range runs {
		// RegisterDueは現在明示承認を要求する。旧enabled設定は未処理待ちを意味しない。
		// 登録・除外・予約済みの除去は、画面と共通のcandidateCTEへ委ねる。
		var count int
		err = tx.QueryRow(ctx, candidateCTE+`SELECT count(DISTINCT c.group_key) FROM candidates c JOIN mail_processing_items i ON i.owner_id=$1 AND i.id=c.result_id WHERE i.run_id=$3 AND NOT EXISTS(SELECT 1 FROM notification_review_seen s WHERE s.owner_id=$1 AND s.message_id=c.message_id AND s.fingerprint=notification_review_fingerprint(c.event))`, owner, "", r.id).Scan(&count)
		if err != nil {
			return notificationError(err)
		}
		channels := p.Kinds[notifications.PendingReview]
		r.channels.InApp = r.channels.InApp && channels.InApp
		r.channels.Email = r.channels.Email && channels.Email && p.EmailOptIn
		r.channels.Push = r.channels.Push && channels.Push && p.PushOptIn
		if _, err = tx.Exec(ctx, `INSERT INTO notification_review_runs(owner_id,account_id,run_id,review_count,in_app,email,push,completed_at,processed_at,resolved_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::timestamptz,CASE WHEN NOT ($5 OR $6 OR $7) THEN $9::timestamptz END)`, owner, r.account, r.id, count, r.channels.InApp, r.channels.Email, r.channels.Push, r.completed, now); err != nil {
			return notificationError(err)
		}
		// 新規候補を固定してから、解決済みを含む現在の元情報を既知にする。
		// 最新外の結果で先に既知化すると、初回poll前の再解析で新着が消える。
		if _, err = tx.Exec(ctx, candidateCTE+`INSERT INTO notification_review_sources(owner_id,run_id,account_id,message_id,fingerprint) SELECT DISTINCT $1::uuid,$3::uuid,c.account_id,c.message_id,notification_review_fingerprint(c.event) FROM candidates c JOIN mail_processing_items i ON i.owner_id=$1 AND i.id=c.result_id WHERE i.run_id=$3 AND NOT EXISTS(SELECT 1 FROM notification_review_seen s WHERE s.owner_id=$1 AND s.message_id=c.message_id AND s.fingerprint=notification_review_fingerprint(c.event)) ON CONFLICT DO NOTHING`, owner, "", r.id); err != nil {
			return notificationError(err)
		}
		if _, err = tx.Exec(ctx, candidateCTE+`INSERT INTO notification_review_seen(owner_id,account_id,message_id,fingerprint) SELECT DISTINCT owner_id,account_id,message_id,notification_review_fingerprint(e.event) FROM current_items i CROSS JOIN LATERAL jsonb_array_elements(i.events) e(event) WHERE i.run_id=$3 AND i.state='succeeded' ON CONFLICT DO NOTHING`, owner, "", r.id); err != nil {
			return notificationError(err)
		}
		if _, err = tx.Exec(ctx, `DELETE FROM notification_review_pending WHERE owner_id=$1 AND run_id=$2`, owner, r.id); err != nil {
			return notificationError(err)
		}
	}
	return nil
}

const pendingReviewRunPredicate = `r.owner_id=$1 AND r.review_count>0 AND r.resolved_at IS NULL AND EXISTS(
 SELECT 1 FROM candidates c JOIN notification_review_sources s ON s.owner_id=$1 AND s.run_id=r.run_id AND s.message_id=c.message_id AND s.fingerprint=notification_review_fingerprint(c.event))`

func notificationReviewRuns(ctx context.Context, tx pgx.Tx, owner string) ([]notifications.PendingReviewRun, error) {
	rows, err := tx.Query(ctx, candidateCTE+`SELECT r.run_id,r.review_count,
 r.in_app AND coalesce(r.completed_at>=s.in_app_since,false),
 r.email AND coalesce(r.completed_at>=s.email_since,false),
 r.push AND coalesce(r.completed_at>=s.push_since,false)
 FROM notification_review_runs r JOIN notification_review_state s ON s.owner_id=r.owner_id WHERE `+pendingReviewRunPredicate, owner, "")
	if err != nil {
		return nil, notificationError(err)
	}
	defer rows.Close()
	out := []notifications.PendingReviewRun{}
	for rows.Next() {
		var r notifications.PendingReviewRun
		r.Pending = true
		if err = rows.Scan(&r.RunID, &r.Count, &r.Channels.InApp, &r.Channels.Email, &r.Channels.Push); err != nil {
			return nil, notificationError(err)
		}
		out = append(out, r)
	}
	return out, notificationError(rows.Err())
}

func resolveNotificationReviews(ctx context.Context, tx pgx.Tx, owner string, now time.Time) error {
	_, err := tx.Exec(ctx, candidateCTE+`UPDATE notification_review_runs r SET resolved_at=$3 WHERE r.owner_id=$1 AND r.review_count>0 AND r.resolved_at IS NULL AND NOT EXISTS(SELECT 1 FROM candidates c JOIN notification_review_sources s ON s.owner_id=$1 AND s.run_id=r.run_id AND s.message_id=c.message_id AND s.fingerprint=notification_review_fingerprint(c.event))`, owner, "", now)
	return notificationError(err)
}
