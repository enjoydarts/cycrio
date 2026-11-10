//go:build integration

package database

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/notifications"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type reviewScopeTransaction struct {
	pgx.Tx
	candidateQueries   []string
	candidateArguments [][]any
}

func (t *reviewScopeTransaction) record(sql string, args []any) {
	if strings.Contains(sql, "WITH current_items AS") {
		t.candidateQueries = append(t.candidateQueries, sql)
		t.candidateArguments = append(t.candidateArguments, args)
	}
}
func (t *reviewScopeTransaction) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	t.record(sql, args)
	return t.Tx.Query(ctx, sql, args...)
}
func (t *reviewScopeTransaction) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	t.record(sql, args)
	return t.Tx.QueryRow(ctx, sql, args...)
}
func (t *reviewScopeTransaction) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	t.record(sql, args)
	return t.Tx.Exec(ctx, sql, args...)
}

func TestNotificationReviewBatchSharesScopedEvaluationAndSendScope(t *testing.T) {
	f, r, now := notificationReviewFixture(t)
	ctx := context.Background()
	event := ai.Event{Service: "Same group", EventType: "renewal"}
	template := notificationReviewResult(t, f, now.Add(-48*time.Hour), event)
	cloneNotificationReviewRun(t, f, template, now.Add(-48*time.Hour), false, 60, event)
	if _, err := r.GenerateNotifications(ctx, f.owner); err != nil {
		t.Fatal(err)
	}
	r = enableNotificationReviewPush(t, f, r, now)
	runIDs := []string{}
	for range 50 {
		runIDs = append(runIDs, cloneNotificationReviewRun(t, f, template, *now, false, 1, event))
	}
	tx, err := r.begin(ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	tracked := &reviewScopeTransaction{Tx: tx}
	settings, err := notificationPreferences(ctx, tx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	if err = observeNotificationReviews(ctx, tracked, f.owner, settings, *now); err != nil {
		t.Fatal(err)
	}
	if len(tracked.candidateQueries) != 1 {
		t.Fatal("50runの候補評価を1回に共有する", len(tracked.candidateQueries))
	}
	args := tracked.candidateArguments[0]
	if len(args) != 3 {
		t.Fatal("対象message集合をSQLへ渡す", args)
	}
	messages, ok := args[2].([]string)
	if !ok || len(messages) != 50 {
		t.Fatal("過去60メールは対象へ含めない", args[2])
	}
	if !strings.Contains(tracked.candidateQueries[0], "CROSS JOIN LATERAL") {
		t.Fatal("各messageの索引で最新を選ぶ")
	}

	// 小規模fixtureでも利用可能な索引経路を実測し、対象messageで絞れていることを確かめる。
	if _, err = tx.Exec(ctx, `ANALYZE mail_processing_items`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SET LOCAL enable_seqscan=off`); err != nil {
		t.Fatal(err)
	}
	var planRaw []byte
	if err = tx.QueryRow(ctx, `EXPLAIN (ANALYZE,FORMAT JSON,COSTS OFF) `+tracked.candidateQueries[0], args...).Scan(&planRaw); err != nil {
		t.Fatal(err)
	}
	var plans []struct{ Plan map[string]any }
	if err = json.Unmarshal(planRaw, &plans); err != nil {
		t.Fatal(err)
	}
	indexed := false
	var walk func(map[string]any)
	walk = func(plan map[string]any) {
		if plan["Index Name"] == "mail_processing_latest" && strings.Contains(plan["Index Cond"].(string), "target.message_id") {
			indexed = true
			if plan["Actual Loops"] != float64(50) || plan["Actual Rows"] != float64(1) {
				t.Fatal("最新探索は対象50メールへ各1回", plan)
			}
		}
		if children, ok := plan["Plans"].([]any); ok {
			for _, child := range children {
				walk(child.(map[string]any))
			}
		}
	}
	walk(plans[0].Plan)
	if !indexed {
		t.Fatal("mail_processing_latestの対象message索引を使わない")
	}
	t.Log("50runを1回の候補評価で共有。最新探索はmail_processing_latestで対象50messageへ各1行、過去60messageは対象外。")

	tracked.candidateQueries = nil
	tracked.candidateArguments = nil
	count := 1
	id := runIDs[0]
	facts := notifications.Candidate{Kind: notifications.PendingReview, SourceKey: "pending_review:" + id, ReviewRunID: &id, ReviewCount: &count, ScheduledAt: *now}
	_, reason, err := r.notificationPushInput(ctx, tracked, f.owner, facts)
	if err != nil || reason != "" {
		t.Fatal(reason, err)
	}
	if len(tracked.candidateQueries) != 1 || len(tracked.candidateArguments[0][2].([]string)) != 1 {
		t.Fatal("送信前は当該通知の固定元情報1messageだけ", tracked.candidateArguments)
	}
	t.Log("Push送信前の候補評価は1回、当該集約の固定元情報1messageだけ。")
}

func TestNotificationReviewScopedCandidatesMatchScreenRules(t *testing.T) {
	f, r, now := notificationReviewFixture(t)
	ctx := context.Background()
	old := notificationReviewResult(t, f, *now, ai.Event{Service: "Old", EventType: "renewal"})
	*now = now.Add(time.Minute)
	cloneNotificationReviewRun(t, f, old, *now, true, 1, ai.Event{Service: "Latest", EventType: "renewal"})
	other := notificationReviewResult(t, f, *now, ai.Event{Service: "Other", EventType: "invoice"})
	var message string
	if err := r.pool.QueryRow(ctx, `SELECT message_id FROM mail_processing_items WHERE id=$1`, old).Scan(&message); err != nil {
		t.Fatal(err)
	}
	tx, err := r.begin(ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for _, owner := range []string{f.owner, f.other} {
		var expected, actual []byte
		if err := tx.QueryRow(ctx, candidateCTE+`SELECT coalesce(jsonb_agg(jsonb_build_array(result_id,event_index,event) ORDER BY result_id,event_index),'[]') FROM candidates WHERE message_id=$3`, owner, "", message).Scan(&expected); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(ctx, candidateMessagesCTE()+`SELECT coalesce(jsonb_agg(jsonb_build_array(result_id,event_index,event) ORDER BY result_id,event_index),'[]') FROM candidates`, owner, "", []string{message}).Scan(&actual); err != nil || string(expected) != string(actual) {
			t.Fatal(owner, string(expected), string(actual), err)
		}
	}
	if strings.Contains(stringMustJSON(t, tx, ctx, f.owner, message), "Old") {
		t.Fatal("旧版を最新扱いしない", other)
	}
}

func stringMustJSON(t *testing.T, tx pgx.Tx, ctx context.Context, owner, message string) string {
	t.Helper()
	var events string
	if err := tx.QueryRow(ctx, candidateMessagesCTE()+`SELECT coalesce(jsonb_agg(event),'[]')::text FROM candidates`, owner, "", []string{message}).Scan(&events); err != nil {
		t.Fatal(err)
	}
	return events
}
