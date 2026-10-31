package database

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/enjoydarts/cycrio/internal/billingschedule"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type BillingScheduleRepository struct {
	contracts *SubscriptionRepository
	now       func() time.Time
}

func NewBillingScheduleRepository(pool *pgxpool.Pool, now ...func() time.Time) *BillingScheduleRepository {
	clock := time.Now
	if len(now) > 0 && now[0] != nil {
		clock = now[0]
	}
	return &BillingScheduleRepository{NewSubscriptionRepository(pool), clock}
}
func (r *BillingScheduleRepository) today() string {
	return r.now().In(time.FixedZone(billingschedule.Timezone, 9*3600)).Format("2006-01-02")
}
func (r *BillingScheduleRepository) Get(ctx context.Context, owner, id string) (billingschedule.View, error) {
	if !subscriptions.ValidID(id) {
		return billingschedule.View{}, billingschedule.ErrInvalid
	}
	tx, e := r.contracts.begin(ctx, owner)
	if e != nil {
		return billingschedule.View{}, scheduleError(e)
	}
	defer tx.Rollback(ctx)
	s, e := scheduleSubscription(ctx, tx, owner, id, false)
	if e != nil {
		return billingschedule.View{}, e
	}
	return scheduleView(ctx, tx, owner, s, r.today())
}
func (r *BillingScheduleRepository) Preview(ctx context.Context, owner, id string, rule billingschedule.Rule) (billingschedule.Preview, error) {
	normalized, e := billingschedule.Normalize(rule)
	if e != nil {
		return billingschedule.Preview{}, e
	}
	v, e := r.Get(ctx, owner, id)
	if e != nil {
		return billingschedule.Preview{}, e
	}
	p := billingschedule.Preview{Subscription: v.Subscription, Rule: normalized, AsOf: v.AsOf, Timezone: v.Timezone, AlgorithmVersion: billingschedule.AlgorithmVersion, Overdue: v.Overdue}
	if v.Subscription.ContractKind == "fixed_term" {
		p.Reason = "fixed_term"
		return p, nil
	}
	if v.Subscription.Status == "ended" {
		p.Reason = "ended"
		return p, nil
	}
	p.CandidateDate, e = billingschedule.Next(normalized, v.AsOf)
	if e != nil {
		return p, e
	}
	if p.CandidateDate == nil {
		p.Reason = "out_of_range"
	}
	return p, nil
}
func (r *BillingScheduleRepository) Save(ctx context.Context, owner, id string, in billingschedule.SaveInput) (billingschedule.SaveResult, error) {
	if !subscriptions.ValidID(id) || !subscriptions.ValidID(in.RequestID) {
		return billingschedule.SaveResult{}, billingschedule.ErrInvalid
	}
	raw, _ := json.Marshal(in)
	tx, e := r.contracts.begin(ctx, owner)
	if e != nil {
		return billingschedule.SaveResult{}, scheduleError(e)
	}
	defer tx.Rollback(ctx)
	// Receipts precede mutable state checks. Successful retries never reapply a write.
	var receipt billingschedule.Receipt
	var target string
	var same bool
	var ruleJSON []byte
	var evidenceJSON []byte
	e = tx.QueryRow(ctx, `SELECT subscription_id,request_doc=$3::jsonb,request_id,applied_version,rule_doc,to_char(as_of,'YYYY-MM-DD'),to_char(candidate_date,'YYYY-MM-DD'),algorithm_version,created_at,history_evidence FROM billing_schedule_requests WHERE owner_id=$1 AND request_id=$2`, owner, in.RequestID, raw).Scan(&target, &same, &receipt.RequestID, &receipt.AppliedVersion, &ruleJSON, &receipt.AsOf, &receipt.CandidateDate, &receipt.AlgorithmVersion, &receipt.CreatedAt, &evidenceJSON)
	if e == nil {
		if !same || !strings.EqualFold(target, id) {
			return billingschedule.SaveResult{}, &billingschedule.Conflict{Reason: "request_conflict"}
		}
		if json.Unmarshal(ruleJSON, &receipt.Rule) != nil || len(evidenceJSON) > 0 && json.Unmarshal(evidenceJSON, &receipt.HistoryEvidence) != nil {
			return billingschedule.SaveResult{}, billingschedule.ErrUnavailable
		}
		s, e := scheduleSubscription(ctx, tx, owner, id, false)
		if e != nil {
			return billingschedule.SaveResult{}, e
		}
		v, e := scheduleView(ctx, tx, owner, s, r.today())
		return billingschedule.SaveResult{Receipt: receipt, Current: v}, e
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return billingschedule.SaveResult{}, scheduleError(e)
	}
	if _, e = billingschedule.NormalizeSave(in); e != nil {
		return billingschedule.SaveResult{}, e
	}
	s, e := scheduleSubscription(ctx, tx, owner, id, true)
	if e != nil {
		return billingschedule.SaveResult{}, e
	}
	if s.Version != in.ExpectedVersion {
		return billingschedule.SaveResult{}, &billingschedule.Conflict{Reason: "version_conflict"}
	}
	if s.ContractKind == "fixed_term" {
		return billingschedule.SaveResult{}, &billingschedule.Conflict{Reason: "fixed_term"}
	}
	today := r.today() // Read after all mutation locks, so a waiting preview may expire.
	if in.ExpectedAsOf != today {
		return billingschedule.SaveResult{}, &billingschedule.Conflict{Reason: "preview_expired"}
	}
	if s.Status == "ended" {
		return billingschedule.SaveResult{}, &billingschedule.Conflict{Reason: "ended"}
	}
	var evidence *billingschedule.HistoryEvidence
	if in.HistoryBasis != nil {
		p, e := scheduleHistory(ctx, tx, owner, s, in.HistoryBasis.Filter, today)
		if e != nil {
			return billingschedule.SaveResult{}, e
		}
		if p.Token == "" || p.Token != in.HistoryBasis.Token {
			return billingschedule.SaveResult{}, &billingschedule.Conflict{Reason: "history_changed"}
		}
		member := false
		for _, c := range p.Candidates {
			member = member || c.Rule == in.Rule
		}
		if !member {
			return billingschedule.SaveResult{}, billingschedule.ErrInvalid
		}
		evidence = &billingschedule.HistoryEvidence{HistoryBasis: billingschedule.HistoryBasis{Filter: p.Filter, Token: p.Token, AlgorithmVersion: p.AlgorithmVersion}, AsOf: p.AsOf, Samples: p.Samples, SelectedRule: in.Rule}
		evidenceJSON, _ = json.Marshal(evidence)
	}
	candidate, e := billingschedule.Next(in.Rule, today)
	if e != nil {
		return billingschedule.SaveResult{}, e
	}
	if candidate == nil || *candidate != in.ExpectedCandidate {
		return billingschedule.SaveResult{}, &billingschedule.Conflict{Reason: "preview_changed"}
	}
	_, e = tx.Exec(ctx, `UPDATE subscriptions SET provenance='owner_confirmed',interval_unit=$3,interval_count=$4,next_billing_date=$5::text::date,version=version+1,updated_at=clock_timestamp() WHERE owner_id=$1 AND id=$2`, owner, id, in.IntervalUnit, in.IntervalCount, *candidate)
	if e != nil {
		return billingschedule.SaveResult{}, scheduleError(e)
	}
	_, e = tx.Exec(ctx, `INSERT INTO billing_schedule_settings(owner_id,subscription_id,interval_unit,interval_count,anchor_date,month_end,confirmed_as_of,confirmed_candidate,algorithm_version,confirmed_version,history_evidence) VALUES($1,$2,$3,$4,$5::text::date,$6,$7::text::date,$8::text::date,$9,$10,$11)
 ON CONFLICT(owner_id,subscription_id) DO UPDATE SET interval_unit=EXCLUDED.interval_unit,interval_count=EXCLUDED.interval_count,anchor_date=EXCLUDED.anchor_date,month_end=EXCLUDED.month_end,confirmed_as_of=EXCLUDED.confirmed_as_of,confirmed_candidate=EXCLUDED.confirmed_candidate,algorithm_version=EXCLUDED.algorithm_version,confirmed_version=EXCLUDED.confirmed_version,history_evidence=EXCLUDED.history_evidence,stale=false,manual_next_override=false,manual_next_override_at=NULL,confirmed_at=clock_timestamp(),updated_at=clock_timestamp()`, owner, id, in.IntervalUnit, in.IntervalCount, in.AnchorDate, in.MonthEnd, today, *candidate, billingschedule.AlgorithmVersion, s.Version+1, evidenceJSON)
	if e != nil {
		return billingschedule.SaveResult{}, scheduleError(e)
	}
	s, e = scheduleSubscription(ctx, tx, owner, id, false)
	if e != nil {
		return billingschedule.SaveResult{}, e
	}
	snapshot, _ := json.Marshal(s)
	ruleJSON, _ = json.Marshal(in.Rule)
	receipt = billingschedule.Receipt{HistoryEvidence: evidence, RequestID: in.RequestID, AppliedVersion: s.Version, Rule: in.Rule, AsOf: today, CandidateDate: *candidate, AlgorithmVersion: billingschedule.AlgorithmVersion}
	e = tx.QueryRow(ctx, `INSERT INTO billing_schedule_requests(owner_id,actor_id,subscription_id,request_id,request_doc,applied_version,rule_doc,as_of,candidate_date,algorithm_version,subscription_snapshot,history_evidence) VALUES($1,$1,$2,$3,$4,$5,$6,$7::text::date,$8::text::date,$9,$10,$11) RETURNING request_id,created_at`, owner, id, in.RequestID, raw, s.Version, ruleJSON, today, *candidate, billingschedule.AlgorithmVersion, snapshot, evidenceJSON).Scan(&receipt.RequestID, &receipt.CreatedAt)
	if e != nil {
		return billingschedule.SaveResult{}, scheduleError(e)
	}
	if e = auditSchedule(ctx, tx, owner, id, "confirmed", &in.RequestID); e != nil {
		return billingschedule.SaveResult{}, e
	}
	view, e := scheduleView(ctx, tx, owner, s, today)
	if e != nil {
		return billingschedule.SaveResult{}, e
	}
	if _, e = completeSubscription(ctx, tx, owner, id, "updated"); e != nil {
		return billingschedule.SaveResult{}, scheduleError(e)
	}
	return billingschedule.SaveResult{Receipt: receipt, Current: view}, nil
}
