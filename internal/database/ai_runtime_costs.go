package database

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/jackc/pgx/v5"
	"time"
)

var _ airuntime.Repository = (*AIRuntimeRepository)(nil)

const runtimeWhere = ` WHERE started_at >= $1 AND started_at < $2 AND ($3='' OR consuming_user_id=NULLIF($3,'')::uuid) AND ($4='' OR scope=$4) AND ($5='' OR provider=$5) AND ($6='' OR model_id=NULLIF($6,'')::uuid) AND ($7='' OR stage=$7) AND ($8='' OR ($8='unattributed' AND scope='user' AND mail_account_id IS NULL) OR mail_account_id=NULLIF(NULLIF($8,''),'unattributed')::uuid)`

func runtimeFilterArgs(f airuntime.CostFilter) []any {
	from, to, _ := airuntime.MonthRange(f.Month)
	return []any{from, to, f.UserID, f.Scope, f.Provider, f.ModelID, f.Stage, f.AccountID}
}

func runtimeAccountFilter(ctx context.Context, tx pgx.Tx, f airuntime.CostFilter) error {
	if f.AccountID == "" || f.AccountID == "unattributed" {
		return nil
	}
	var id string
	if err := tx.QueryRow(ctx, `SELECT id FROM mail_accounts WHERE owner_id=$1 AND id=$2`, f.UserID, f.AccountID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return airuntime.ErrInvalid
		}
		return runtimeError(err)
	}
	return nil
}

const runtimeAggregate = `currency,COALESCE(sum(adopted_amount),0)::text,COALESCE(sum(adopted_amount) FILTER(WHERE cost_kind='estimated'),0)::text,COALESCE(sum(adopted_amount) FILTER(WHERE cost_kind='provider_reported'),0)::text,count(*),count(*) FILTER(WHERE cost_kind='unknown'),count(*) FILTER(WHERE status IN ('failed','unknown'))`

func normalizeRuntimeTotals(t *airuntime.Totals) error {
	for _, s := range []*string{&t.Amount, &t.EstimatedAmount, &t.ReportedAmount} {
		v, err := ai.NormalizeAmount(*s)
		if err != nil {
			return airuntime.ErrUnavailable
		}
		*s = v
	}
	return nil
}
func (r *AIRuntimeRepository) Costs(ctx context.Context, f airuntime.CostFilter) (airuntime.CostReport, error) {
	f, err := airuntime.ValidateFilter(f)
	if err != nil {
		return airuntime.CostReport{}, err
	}
	from, to, _ := airuntime.MonthRange(f.Month)
	report := airuntime.CostReport{Month: f.Month, Timezone: "Asia/Tokyo", From: from, To: to, Provisional: time.Now().In(time.FixedZone("Asia/Tokyo", 9*3600)).Format("2006-01") == f.Month, Totals: []airuntime.Totals{}, Users: []airuntime.ConsumerCost{}, Administration: []airuntime.ScopeCost{{Scope: "admin_test", Totals: []airuntime.Totals{}}, {Scope: "system", Totals: []airuntime.Totals{}}}, Breakdown: []airuntime.Breakdown{}}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return report, runtimeError(err)
	}
	defer tx.Rollback(ctx)
	if err := runtimeAccountFilter(ctx, tx, f); err != nil {
		return report, err
	}
	args := runtimeFilterArgs(f)
	if err := tx.QueryRow(ctx, `SELECT max(updated_at) FROM ai_calls`+runtimeWhere, args...).Scan(&report.UpdatedAt); err != nil {
		return report, runtimeError(err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE ($1='' OR id=NULLIF($1,'')::uuid)`, f.UserID).Scan(&report.UsersTotal); err != nil {
		return report, runtimeError(err)
	}
	// Only grouped rows leave PostgreSQL; costs never load the month's call ledger.
	query := `WITH filtered AS (SELECT * FROM ai_calls` + runtimeWhere + `) SELECT 'total','','','','',` + runtimeAggregate + ` FROM filtered GROUP BY currency UNION ALL SELECT 'scope',scope,'','','',` + runtimeAggregate + ` FROM filtered WHERE scope IN ('admin_test','system') GROUP BY scope,currency UNION ALL SELECT 'model',provider,model_id::text,max(model_name),stage,` + runtimeAggregate + ` FROM filtered GROUP BY provider,model_id,stage,currency ORDER BY 1,2,3,4,5,6 NULLS FIRST`
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return report, runtimeError(err)
	}
	breakdowns := map[string]int{}
	for rows.Next() {
		var section, a, b, c, d string
		var t airuntime.Totals
		if err := rows.Scan(&section, &a, &b, &c, &d, &t.Currency, &t.Amount, &t.EstimatedAmount, &t.ReportedAmount, &t.Calls, &t.UnknownCalls, &t.Failures); err != nil {
			rows.Close()
			return report, runtimeError(err)
		}
		if err := normalizeRuntimeTotals(&t); err != nil {
			rows.Close()
			return report, err
		}
		switch section {
		case "total":
			report.Totals = append(report.Totals, t)
		case "scope":
			idx := 0
			if a == "system" {
				idx = 1
			}
			report.Administration[idx].Totals = append(report.Administration[idx].Totals, t)
		case "model":
			key := a + "\x00" + b + "\x00" + d
			i, exists := breakdowns[key]
			if !exists {
				i = len(report.Breakdown)
				breakdowns[key] = i
				report.Breakdown = append(report.Breakdown, airuntime.Breakdown{Provider: a, ModelID: b, ModelName: c, Stage: d, Totals: []airuntime.Totals{}})
			}
			report.Breakdown[i].Totals = append(report.Breakdown[i].Totals, t)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return report, runtimeError(err)
	}
	// Limit users first, then aggregate only their calls. LEFT JOIN retains users
	// with no usage and count(call.id) distinguishes them from unknown-cost calls.
	userQuery := `WITH filtered AS (SELECT * FROM ai_calls` + runtimeWhere + `),page AS (SELECT id FROM users WHERE ($3='' OR id=NULLIF($3,'')::uuid) ORDER BY id LIMIT $9 OFFSET $10) SELECT p.id,c.currency,COALESCE(sum(c.adopted_amount),0)::text,COALESCE(sum(c.adopted_amount) FILTER(WHERE c.cost_kind='estimated'),0)::text,COALESCE(sum(c.adopted_amount) FILTER(WHERE c.cost_kind='provider_reported'),0)::text,count(c.id),count(c.id) FILTER(WHERE c.cost_kind='unknown'),count(c.id) FILTER(WHERE c.status IN ('failed','unknown')) FROM page p LEFT JOIN filtered c ON c.consuming_user_id=p.id GROUP BY p.id,c.currency ORDER BY p.id,c.currency NULLS FIRST`
	rows, err = tx.Query(ctx, userQuery, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return report, runtimeError(err)
	}
	for rows.Next() {
		var id string
		var t airuntime.Totals
		if err := rows.Scan(&id, &t.Currency, &t.Amount, &t.EstimatedAmount, &t.ReportedAmount, &t.Calls, &t.UnknownCalls, &t.Failures); err != nil {
			rows.Close()
			return report, runtimeError(err)
		}
		if err := normalizeRuntimeTotals(&t); err != nil {
			rows.Close()
			return report, err
		}
		i := len(report.Users) - 1
		if i < 0 || report.Users[i].UserID != id {
			report.Users = append(report.Users, airuntime.ConsumerCost{UserID: id, Totals: []airuntime.Totals{}, MailAccounts: []airuntime.MailAccountCost{}})
			i++
		}
		u := &report.Users[i]
		u.Calls += t.Calls
		u.UnknownCalls += t.UnknownCalls
		u.Failures += t.Failures
		if t.Calls > 0 {
			u.Totals = append(u.Totals, t)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return report, runtimeError(err)
	}
	if err := runtimeMailAccountCosts(ctx, tx, f, &report); err != nil {
		return report, err
	}
	return report, runtimeCommit(ctx, tx)
}

func runtimeMailAccountCosts(ctx context.Context, tx pgx.Tx, f airuntime.CostFilter, report *airuntime.CostReport) error {
	if len(report.Users) == 0 {
		return nil
	}
	ids := make([]string, 0, len(report.Users))
	users := map[string]int{}
	for i, u := range report.Users {
		ids = append(ids, u.UserID)
		users[u.UserID] = i
	}
	// Restrict the grouped ledger to the selected user page in PostgreSQL.
	query := `WITH filtered AS (SELECT * FROM ai_calls` + runtimeWhere + ` AND scope='user' AND consuming_user_id=ANY($9::uuid[])), named AS (SELECT c.*,COALESCE(a.label,'') AS account_label,COALESCE(a.provider,'') AS account_provider FROM filtered c LEFT JOIN mail_accounts a ON a.owner_id=c.consuming_user_id AND a.id=c.mail_account_id) SELECT consuming_user_id,mail_account_id,account_label,account_provider,COALESCE(stage,''),GROUPING(stage)=1,` + runtimeAggregate + ` FROM named GROUP BY GROUPING SETS ((consuming_user_id,mail_account_id,account_label,account_provider,currency),(consuming_user_id,mail_account_id,account_label,account_provider,stage,currency)) ORDER BY consuming_user_id,mail_account_id NULLS LAST,GROUPING(stage) DESC,stage,currency NULLS FIRST`
	rows, err := tx.Query(ctx, query, append(runtimeFilterArgs(f), ids)...)
	if err != nil {
		return runtimeError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var user, label, provider, stage string
		var account *string
		var total bool
		var t airuntime.Totals
		if err := rows.Scan(&user, &account, &label, &provider, &stage, &total, &t.Currency, &t.Amount, &t.EstimatedAmount, &t.ReportedAmount, &t.Calls, &t.UnknownCalls, &t.Failures); err != nil {
			return runtimeError(err)
		}
		if err := normalizeRuntimeTotals(&t); err != nil {
			return err
		}
		u := &report.Users[users[user]]
		i := len(u.MailAccounts) - 1
		if i < 0 || !sameRuntimeAccount(u.MailAccounts[i].AccountID, account) {
			u.MailAccounts = append(u.MailAccounts, airuntime.MailAccountCost{AccountID: account, Label: label, Provider: provider, Totals: []airuntime.Totals{}, Stages: []airuntime.StageCost{}})
			i++
		}
		a := &u.MailAccounts[i]
		if total {
			a.Calls += t.Calls
			a.UnknownCalls += t.UnknownCalls
			a.Failures += t.Failures
			a.Totals = append(a.Totals, t)
		} else {
			i := len(a.Stages) - 1
			if i < 0 || a.Stages[i].Stage != stage {
				a.Stages = append(a.Stages, airuntime.StageCost{Stage: stage, Totals: []airuntime.Totals{}})
				i++
			}
			a.Stages[i].Totals = append(a.Stages[i].Totals, t)
		}
	}
	if err := rows.Err(); err != nil {
		return runtimeError(err)
	}
	return nil
}
func sameRuntimeAccount(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func (r *AIRuntimeRepository) Calls(ctx context.Context, f airuntime.CostFilter) (airuntime.CallPage, error) {
	result := airuntime.CallPage{Calls: []airuntime.Call{}}
	f, err := airuntime.ValidateFilter(f)
	if err != nil {
		return result, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, runtimeError(err)
	}
	defer tx.Rollback(ctx)
	if err := runtimeAccountFilter(ctx, tx, f); err != nil {
		return result, err
	}
	args := runtimeFilterArgs(f)
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM ai_calls`+runtimeWhere, args...).Scan(&result.Total); err != nil {
		return result, runtimeError(err)
	}
	rows, err := tx.Query(ctx, `SELECT `+runtimeCallColumns+` FROM ai_calls`+runtimeWhere+` ORDER BY started_at DESC,id DESC LIMIT $9 OFFSET $10`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return result, runtimeError(err)
	}
	for rows.Next() {
		c, err := scanRuntimeCall(rows)
		if err != nil {
			rows.Close()
			return result, err
		}
		result.Calls = append(result.Calls, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, runtimeError(err)
	}
	return result, runtimeCommit(ctx, tx)
}
func (r *AIRuntimeRepository) Revisions(ctx context.Context, id string) ([]airuntime.UsageRevision, error) {
	result := []airuntime.UsageRevision{}
	if !runtimeID(id) {
		return result, airuntime.ErrInvalid
	}
	if _, err := r.GetCall(ctx, id); err != nil {
		return result, err
	}
	rows, err := r.pool.Query(ctx, `SELECT id,call_id,usage_doc,cost_kind,adopted_amount::text,currency,cost_reason,captured_at FROM ai_usage_revisions WHERE call_id=$1 ORDER BY captured_at DESC,id DESC LIMIT 50`, id)
	if err != nil {
		return result, runtimeError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var v airuntime.UsageRevision
		var raw []byte
		if err := rows.Scan(&v.ID, &v.CallID, &raw, &v.Cost.Status, &v.Cost.Amount, &v.Cost.Currency, &v.Cost.Reason, &v.CapturedAt); err != nil {
			return result, runtimeError(err)
		}
		if json.Unmarshal(raw, &v.Usage) != nil {
			return result, airuntime.ErrUnavailable
		}
		if v.Cost.Amount != nil {
			a, err := ai.NormalizeAmount(*v.Cost.Amount)
			if err != nil {
				return result, airuntime.ErrUnavailable
			}
			v.Cost.Amount = &a
		}
		result = append(result, v)
	}
	if rows.Err() != nil {
		return result, airuntime.ErrUnavailable
	}
	return result, nil
}
