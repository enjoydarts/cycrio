//go:build integration && evaluation

package database

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/autoregistration"
	"github.com/enjoydarts/cycrio/internal/evaluation"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/google/uuid"
)

var pocReportDirectory = flag.String("evaluation-report-dir", "", "save metadata-only synthetic evaluation reports to this directory")

func runPoCEvaluation(t *testing.T, messages int) evaluation.Report {
	t.Helper()
	if messages < 10 || messages%10 != 0 {
		t.Fatal("evaluation requires complete ten-message groups")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	f := processingSetup(t)
	f.add(t, messages)
	body := []byte("CYCRIO_EVAL_BODY_" + uuid.NewString())
	secret := []byte("CYCRIO_EVAL_SECRET_" + uuid.NewString())
	defer clear(body)
	defer clear(secret)
	probes := []evaluation.Probe{{ID: "body", Value: body}, {ID: "credential", Value: secret}}
	client := &pocInference{body: body, secret: secret}
	service := mailprocessing.NewService(f.repo, f.keys, pocReader{body: body, secret: secret}, client, func(context.Context, string) ([]byte, error) { return bytes.Clone(secret), nil })
	report := evaluation.Report{Version: 1, Mode: "synthetic", Scope: "normalized_body_to_billing", CostSource: "synthetic_adapter_values", Messages: int64(messages), StartedAt: time.Now().UTC(), Outcomes: map[string]int64{}, DurationsMS: map[string]int64{}, Costs: []evaluation.Cost{}, Checks: []evaluation.Check{}, NotChecked: evaluation.UnverifiedScopes()}
	payloadCheck := evaluation.Check{ID: "job_payload", Scope: "serialized_lease_only", Passed: true}
	resultCheck := evaluation.Check{ID: "public_results", Scope: "results_api_json", Passed: true}
	phase := time.Now()
	completed := 0
	var last mailprocessing.Lease
	for completed < messages {
		before := completed
		in := f.in
		in.RequestID = uuid.NewString()
		in.MaxMessages = min(100, messages-completed)
		run, e := service.Request(ctx, f.owner, f.account, in)
		if e != nil || run.TotalCount != in.MaxMessages {
			t.Fatal("request synthetic batch", e)
		}
		for range run.TotalCount {
			leases, e := service.Claim(ctx)
			if e != nil || len(leases) > 1 {
				t.Fatal("claim synthetic work", e, len(leases))
			}
			// Unknown outcomes terminate the batch; the next explicit synthetic request
			// selects only previously unsent messages, never the uncertain message.
			if len(leases) == 0 {
				break
			}
			last = leases[0]
			raw, e := json.Marshal(last)
			if e != nil {
				t.Fatal("serialize synthetic lease")
			}
			hits, e := evaluation.Scan(bytes.NewReader(raw), probes)
			if e != nil || len(hits) != 0 {
				t.Fatal("private probe in job payload", hits, e)
			}
			payloadCheck.Scanned++
			if e = service.Execute(ctx, last); e != nil {
				t.Fatal("execute synthetic mail", e)
			}
			completed++
			// Results returns the latest 100 attempts; scanning every 50 executions
			// and at completion covers every attempted message before it ages out.
			if completed%50 == 0 || completed == messages {
				results, err := service.Results(ctx, f.owner, f.account)
				if err != nil {
					t.Fatal("read public evaluation results")
				}
				raw, err := json.Marshal(results)
				if err != nil {
					t.Fatal("serialize public results")
				}
				hits, err := evaluation.Scan(bytes.NewReader(raw), probes)
				if err != nil || len(hits) != 0 {
					t.Fatal("private probe in public results", hits, err)
				}
				resultCheck.Scanned += int64(len(results))
			}
		}
		if completed == before {
			t.Fatal("synthetic batch made no progress")
		}
	}
	report.DurationsMS["processing"] = time.Since(phase).Milliseconds()
	report.Calls = client.calls
	if e := service.Execute(ctx, last); e != nil || client.calls != report.Calls {
		t.Fatal("duplicate lease repeated inference", e)
	}
	preview, e := service.Preview(ctx, f.owner, f.account)
	if e != nil || preview.PendingCount != 0 || preview.FailedCount != messages/5 || preview.UnknownCount != messages/10 {
		t.Fatal("remaining synthetic work", e)
	}
	rows, e := f.runtime.pool.Query(ctx, `SELECT state,count(*) FROM (SELECT DISTINCT ON(message_id) message_id,state FROM mail_processing_items WHERE owner_id=$1 AND state IN ('succeeded','not_related','failed','unknown') ORDER BY message_id,created_at DESC,id DESC) final GROUP BY state`, f.owner)
	if e != nil {
		t.Fatal("read synthetic outcomes")
	}
	for rows.Next() {
		var state string
		var count int64
		if rows.Scan(&state, &count) != nil {
			t.Fatal("read state count")
		}
		report.Outcomes[state] = count
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatal("read state count")
	}
	subscriptions := NewSubscriptionRepository(f.runtime.pool)
	if _, e = subscriptions.SaveAutomation(ctx, f.owner, autoregistration.PreferenceInput{Enabled: true}); e != nil {
		t.Fatal("enable isolated registration", e)
	}
	phase = time.Now()
	registered := 0
	for {
		n, e := subscriptions.registerLegacyDue(ctx, 100)
		if e != nil {
			t.Fatal("register synthetic results", e)
		}
		registered += n
		if n == 0 {
			break
		}
	}
	report.DurationsMS["registration"] = time.Since(phase).Milliseconds()
	report.RegisteredResults = int64(registered)
	billing := NewBillingRepository(f.runtime.pool)
	phase = time.Now()
	entries := 0
	for {
		n, e := billing.RecordDue(ctx, 100)
		if e != nil {
			t.Fatal("record synthetic bills", e)
		}
		entries += n
		if n == 0 {
			break
		}
	}
	report.DurationsMS["billing"] = time.Since(phase).Milliseconds()
	report.BillingEntries = int64(entries)
	if registered != messages/10*6 || entries != registered {
		t.Fatal("synthetic registration or billing incomplete", registered, entries)
	}
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM subscriptions WHERE owner_id=$1`, f.owner).Scan(&report.Contracts); e != nil {
		t.Fatal("read synthetic contract count")
	}
	if report.Contracts != int64(min(messages, 100)/10*6) {
		t.Fatal("synthetic contract matching changed", report.Contracts)
	}
	// An idle recovery pass must not duplicate either contracts or bills.
	if n, e := subscriptions.registerLegacyDue(ctx, 100); e != nil || n != 0 {
		t.Fatal("registration replay", n, e)
	}
	if n, e := billing.RecordDue(ctx, 100); e != nil || n != 0 {
		t.Fatal("billing replay", n, e)
	}
	var contracts, bills int64
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM subscriptions WHERE owner_id=$1`, f.owner).Scan(&contracts); e != nil || contracts != report.Contracts {
		t.Fatal("contract replay changed count")
	}
	if e = f.runtime.pool.QueryRow(ctx, `SELECT count(*) FROM billing_events WHERE owner_id=$1`, f.owner).Scan(&bills); e != nil || bills != report.BillingEntries {
		t.Fatal("billing replay changed count", e)
	}
	report.Costs = poCEvaluationCosts(t, ctx, f)
	phase = time.Now()
	check, hits, e := scanPoCDatabase(ctx, f.runtime.pool, probes)
	if e != nil || !check.Passed {
		t.Fatal("private probe retained in database", hits, e)
	}
	report.DurationsMS["privacy"] = time.Since(phase).Milliseconds()
	report.Checks = append(report.Checks, check, payloadCheck, resultCheck)
	report.FinishedAt = time.Now().UTC()
	var output bytes.Buffer
	if e = evaluation.Write(&output, report); e != nil {
		t.Fatal("invalid synthetic report", e)
	}
	if strings.Contains(output.String(), string(body)) || strings.Contains(output.String(), string(secret)) {
		t.Fatal("report retained private probe")
	}
	t.Logf("synthetic_evaluation=%s", strings.TrimSpace(output.String()))
	if *pocReportDirectory != "" {
		if os.MkdirAll(*pocReportDirectory, 0700) != nil {
			t.Fatal("create evaluation report directory")
		}
		file, e := os.CreateTemp(*pocReportDirectory, "synthetic-"+time.Now().UTC().Format("20060102T150405")+"-*.json")
		if e != nil {
			t.Fatal("create evaluation report")
		}
		if _, e = file.Write(output.Bytes()); e != nil {
			file.Close()
			t.Fatal("write evaluation report")
		}
		if file.Close() != nil {
			t.Fatal("close evaluation report")
		}
		t.Logf("report_file=%s", filepath.Base(file.Name()))
	}
	return report
}

func poCEvaluationCosts(t *testing.T, ctx context.Context, f processingFixture) []evaluation.Cost {
	t.Helper()
	rows, e := f.runtime.pool.Query(ctx, `SELECT DISTINCT to_char(started_at AT TIME ZONE 'Asia/Tokyo','YYYY-MM') FROM ai_calls WHERE consuming_user_id=$1 AND scope='user' ORDER BY 1`, f.owner)
	if e != nil {
		t.Fatal("read evaluation cost periods")
	}
	months := []string{}
	for rows.Next() {
		var month string
		if rows.Scan(&month) != nil {
			t.Fatal("read cost period")
		}
		months = append(months, month)
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatal("read cost period")
	}
	costs := []evaluation.Cost{}
	for _, month := range months {
		report, e := f.runtime.Costs(ctx, airuntime.CostFilter{Month: month, UserID: f.owner, Scope: "user", Limit: 100})
		if e != nil {
			t.Fatal("read synthetic user costs", e)
		}
		for _, total := range report.Totals {
			c := evaluation.Cost{Month: month, Timezone: report.Timezone, Currency: total.Currency, Calls: total.Calls, UnknownCalls: total.UnknownCalls}
			if total.Calls > total.UnknownCalls {
				amount := total.Amount
				c.KnownAmount = &amount
			}
			costs = append(costs, c)
		}
	}
	return costs
}
