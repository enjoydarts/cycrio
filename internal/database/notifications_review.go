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

type notificationReviewSource struct{ message, fingerprint string }

// 一度のバッチ評価を件数・元情報固定・送信前判定で共有する。
func currentReviewSourceGroups(ctx context.Context, tx pgx.Tx, owner string, messages []string) (map[notificationReviewSource][]string, error) {
	groups := map[notificationReviewSource][]string{}
	if len(messages) == 0 {
		return groups, nil
	}
	rows, err := tx.Query(ctx, candidateMessagesCTE()+`SELECT message_id,notification_review_fingerprint(event),group_key::text FROM candidates`, owner, "", messages)
	if err != nil {
		return nil, notificationError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var s notificationReviewSource
		var group string
		if err = rows.Scan(&s.message, &s.fingerprint, &group); err != nil {
			return nil, notificationError(err)
		}
		groups[s] = append(groups[s], group)
	}
	return groups, notificationError(rows.Err())
}

// 各意味を最初の未処理完了runで消費し、元情報の初回チャネルを維持する。
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
	runIDs := []string{}
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
		runIDs = append(runIDs, r.id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return notificationError(err)
	}
	if len(runs) == 0 {
		return nil
	}
	sources := map[string][]notificationReviewSource{}
	messageSet := map[string]bool{}
	messages := []string{}
	rows, err = tx.Query(ctx, `SELECT i.run_id,i.message_id,notification_review_fingerprint(e.event) FROM mail_processing_items i CROSS JOIN LATERAL jsonb_array_elements(i.events) e(event) WHERE i.owner_id=$1 AND i.run_id=ANY($2::uuid[]) AND i.state='succeeded'`, owner, runIDs)
	if err != nil {
		return notificationError(err)
	}
	for rows.Next() {
		var id string
		var s notificationReviewSource
		if err = rows.Scan(&id, &s.message, &s.fingerprint); err != nil {
			rows.Close()
			return notificationError(err)
		}
		sources[id] = append(sources[id], s)
		if !messageSet[s.message] {
			messageSet[s.message] = true
			messages = append(messages, s.message)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return notificationError(err)
	}
	groups, err := currentReviewSourceGroups(ctx, tx, owner, messages)
	if err != nil {
		return err
	}
	seen := map[notificationReviewSource]bool{}
	rows, err = tx.Query(ctx, `SELECT message_id,fingerprint FROM notification_review_seen WHERE owner_id=$1 AND message_id=ANY($2::uuid[])`, owner, messages)
	if err != nil {
		return notificationError(err)
	}
	for rows.Next() {
		var s notificationReviewSource
		if err = rows.Scan(&s.message, &s.fingerprint); err != nil {
			rows.Close()
			return notificationError(err)
		}
		seen[s] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return notificationError(err)
	}
	for _, r := range runs {
		candidateMessages, candidateFingerprints, allMessages, allFingerprints := []string{}, []string{}, []string{}, []string{}
		countGroups := map[string]bool{}
		for _, s := range sources[r.id] {
			allMessages = append(allMessages, s.message)
			allFingerprints = append(allFingerprints, s.fingerprint)
			if seen[s] {
				continue
			}
			seen[s] = true
			if len(groups[s]) == 0 {
				continue
			}
			candidateMessages = append(candidateMessages, s.message)
			candidateFingerprints = append(candidateFingerprints, s.fingerprint)
			for _, group := range groups[s] {
				countGroups[group] = true
			}
		}
		// 現行RegisterDueは明示承認を要求し、旧enabledだけでは自動登録待ちにしない。
		channels := p.Kinds[notifications.PendingReview]
		r.channels.InApp = r.channels.InApp && channels.InApp
		r.channels.Email = r.channels.Email && channels.Email && p.EmailOptIn
		r.channels.Push = r.channels.Push && channels.Push && p.PushOptIn
		if _, err = tx.Exec(ctx, `INSERT INTO notification_review_runs(owner_id,account_id,run_id,review_count,in_app,email,push,completed_at,processed_at,resolved_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::timestamptz,CASE WHEN NOT ($5 OR $6 OR $7) THEN $9::timestamptz END)`, owner, r.account, r.id, len(countGroups), r.channels.InApp, r.channels.Email, r.channels.Push, r.completed, now); err != nil {
			return notificationError(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO notification_review_seen(owner_id,account_id,message_id,fingerprint) SELECT $1,$2,source.message_id,source.fingerprint FROM unnest($3::uuid[],$4::text[]) source(message_id,fingerprint) ON CONFLICT DO NOTHING`, owner, r.account, allMessages, allFingerprints); err != nil {
			return notificationError(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO notification_review_sources(owner_id,run_id,account_id,message_id,fingerprint) SELECT $1,$2,$3,source.message_id,source.fingerprint FROM unnest($4::uuid[],$5::text[]) source(message_id,fingerprint) ON CONFLICT DO NOTHING`, owner, r.id, r.account, candidateMessages, candidateFingerprints); err != nil {
			return notificationError(err)
		}
	}
	_, err = tx.Exec(ctx, `DELETE FROM notification_review_pending WHERE owner_id=$1 AND run_id=ANY($2::uuid[])`, owner, runIDs)
	return notificationError(err)
}

// 固定した元情報だけを最新の共通候補集合と照合する。送信時は1通知へ限定する。
func notificationReviewRuns(ctx context.Context, tx pgx.Tx, owner string, onlyRun ...string) ([]notifications.PendingReviewRun, error) {
	query := `SELECT r.run_id,r.review_count,
 r.in_app AND coalesce(r.completed_at>=s.in_app_since,false),
 r.email AND coalesce(r.completed_at>=s.email_since,false),
 r.push AND coalesce(r.completed_at>=s.push_since,false),source.message_id,source.fingerprint
 FROM notification_review_runs r JOIN notification_review_state s ON s.owner_id=r.owner_id
 JOIN notification_review_sources source ON source.owner_id=r.owner_id AND source.run_id=r.run_id
 WHERE r.owner_id=$1 AND r.review_count>0 AND r.resolved_at IS NULL`
	args := []any{owner}
	if len(onlyRun) > 0 {
		query += ` AND r.run_id=$2::uuid`
		args = append(args, onlyRun[0])
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, notificationError(err)
	}
	runs := map[string]*notifications.PendingReviewRun{}
	sources := map[string][]notificationReviewSource{}
	messages := []string{}
	messageSet := map[string]bool{}
	for rows.Next() {
		var r notifications.PendingReviewRun
		var s notificationReviewSource
		if err = rows.Scan(&r.RunID, &r.Count, &r.Channels.InApp, &r.Channels.Email, &r.Channels.Push, &s.message, &s.fingerprint); err != nil {
			rows.Close()
			return nil, notificationError(err)
		}
		if runs[r.RunID] == nil {
			runs[r.RunID] = &r
		}
		sources[r.RunID] = append(sources[r.RunID], s)
		if !messageSet[s.message] {
			messageSet[s.message] = true
			messages = append(messages, s.message)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, notificationError(err)
	}
	groups, err := currentReviewSourceGroups(ctx, tx, owner, messages)
	if err != nil {
		return nil, err
	}
	out := []notifications.PendingReviewRun{}
	for id, r := range runs {
		for _, s := range sources[id] {
			if len(groups[s]) > 0 {
				r.Pending = true
				break
			}
		}
		out = append(out, *r)
	}
	return out, nil
}

func resolveNotificationReviews(ctx context.Context, tx pgx.Tx, owner string, now time.Time, reviews []notifications.PendingReviewRun) error {
	ids := []string{}
	for _, r := range reviews {
		if !r.Pending {
			ids = append(ids, r.RunID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE notification_review_runs SET resolved_at=$3 WHERE owner_id=$1 AND run_id=ANY($2::uuid[]) AND resolved_at IS NULL`, owner, ids, now)
	return notificationError(err)
}
