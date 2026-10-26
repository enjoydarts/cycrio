package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/enjoydarts/cycrio/internal/billingschedule"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/jackc/pgx/v5"
)

func (r *BillingScheduleRepository) HistoryPreview(ctx context.Context, owner, id string, filter billingschedule.HistoryFilter) (billingschedule.HistoryPreview, error) {
	if !subscriptions.ValidID(id) || !billingschedule.ValidHistoryFilter(filter) {
		return billingschedule.HistoryPreview{}, billingschedule.ErrInvalid
	}
	tx, e := r.contracts.begin(ctx, owner)
	if e != nil {
		return billingschedule.HistoryPreview{}, scheduleError(e)
	}
	defer tx.Rollback(ctx)
	s, e := scheduleSubscription(ctx, tx, owner, id, false)
	if e != nil {
		return billingschedule.HistoryPreview{}, e
	}
	return scheduleHistory(ctx, tx, owner, s, filter, r.today())
}

// historyRecord includes every stored value that influences eligibility or evidence.
// The source/audit data and email contents are deliberately outside this snapshot.
type historyRecord struct {
	billingschedule.HistorySample
	Kind       string  `json:"kind"`
	State      string  `json:"state"`
	RefundOfID *string `json:"refund_of_id"`
	Primary    bool    `json:"primary"`
}

func scheduleHistory(ctx context.Context, tx pgx.Tx, owner string, s subscriptions.Subscription, f billingschedule.HistoryFilter, today string) (billingschedule.HistoryPreview, error) {
	p := billingschedule.HistoryPreview{Subscription: s, Filter: f, AsOf: today, Timezone: billingschedule.Timezone, AlgorithmVersion: billingschedule.HistoryAlgorithmVersion, Samples: []billingschedule.HistorySample{}, Candidates: []billingschedule.HistoryCandidate{}, Entries: []billingschedule.HistoryEntry{}}
	if s.Status == "ended" {
		p.Reason = "ended"
	}
	if f.StartDate > today {
		return p, billingschedule.ErrInvalid
	}
	// The owner mutation lock serializes all supported financial writes. One bounded
	// statement also gives the primary rows and older referenced refunds one snapshot.
	rows, e := tx.Query(ctx, `WITH primary_events AS MATERIALIZED (
 SELECT id,version,occurred_on,amount,currency,kind,state,refund_of_id FROM billing_events
 WHERE owner_id=$1 AND subscription_id=$2 AND occurred_on BETWEEN $3::text::date AND $4::text::date ORDER BY occurred_on,id LIMIT 1001
), scoped AS (
 SELECT *,true AS primary_record FROM primary_events
 UNION ALL
 (SELECT b.id,b.version,b.occurred_on,b.amount,b.currency,b.kind,b.state,b.refund_of_id,false FROM billing_events b
 WHERE b.owner_id=$1 AND b.subscription_id=$2 AND b.kind='refund' AND b.state='active' AND b.occurred_on<$3::text::date AND b.occurred_on<=$4::text::date
 AND EXISTS(SELECT 1 FROM primary_events p WHERE p.kind='payment' AND p.id=b.refund_of_id) ORDER BY b.occurred_on,b.id LIMIT 1001)
) SELECT id,version,to_char(occurred_on,'YYYY-MM-DD'),amount,currency,kind,state,refund_of_id,primary_record FROM scoped ORDER BY occurred_on,id LIMIT 1001`, owner, s.ID, f.StartDate, today)
	if e != nil {
		return p, scheduleError(e)
	}
	defer rows.Close()
	records := []historyRecord{}
	for rows.Next() {
		var v historyRecord
		if e = rows.Scan(&v.ID, &v.Version, &v.OccurredOn, &v.Amount, &v.Currency, &v.Kind, &v.State, &v.RefundOfID, &v.Primary); e != nil {
			return p, scheduleError(e)
		}
		records = append(records, v)
	}
	if e = rows.Err(); e != nil {
		return p, scheduleError(e)
	}
	if len(records) > 1000 {
		p.Status = "too_many"
		return p, nil
	}
	counts := &billingschedule.HistoryCounts{}
	p.Counts = counts
	refunded := map[string]bool{}
	for _, v := range records {
		if v.Kind == "refund" && v.State == "active" && v.RefundOfID != nil {
			refunded[*v.RefundOfID] = true
		}
	}
	eligible := []billingschedule.HistorySample{}
	duplicate := false
	dates := map[string]bool{}
	for _, v := range records {
		if !v.Primary {
			counts.ReferencedRefunds++
			continue
		}
		counts.Total++
		reason := "eligible"
		switch {
		case v.State == "voided":
			reason = "voided"
			counts.Voided++
		case v.Kind == "invoice":
			reason = "invoice"
			counts.Invoices++
		case v.Kind == "refund":
			reason = "refund"
			counts.Refunds++
		case v.Currency != f.Currency:
			reason = "other_currency"
			counts.OtherCurrency++
		case refunded[v.ID]:
			reason = "refunded"
			counts.Refunded++
		default:
			eligible = append(eligible, v.HistorySample)
			counts.Eligible++
			duplicate = duplicate || dates[v.OccurredOn]
			dates[v.OccurredOn] = true
		}
		p.Entries = append(p.Entries, billingschedule.HistoryEntry{HistorySample: v.HistorySample, Kind: v.Kind, State: v.State, Reason: reason})
	}
	// All rows, not only the displayed five, bind the token and duplicate decision.
	tokenDoc, e := json.Marshal(struct {
		Owner        string                        `json:"owner"`
		Subscription string                        `json:"subscription"`
		Version      int64                         `json:"version"`
		Filter       billingschedule.HistoryFilter `json:"filter"`
		AsOf         string                        `json:"as_of"`
		Algorithm    string                        `json:"algorithm"`
		Records      []historyRecord               `json:"records"`
	}{strings.ToLower(owner), s.ID, s.Version, f, today, billingschedule.HistoryAlgorithmVersion, records})
	if e != nil {
		return p, scheduleError(e)
	}
	digest := sha256.Sum256(tokenDoc)
	p.Token = hex.EncodeToString(digest[:])
	if len(eligible) > 5 {
		counts.OlderEligible = len(eligible) - 5
		eligible = eligible[len(eligible)-5:]
	}
	p.Samples = eligible
	counts.Sampled = len(eligible)
	switch {
	case duplicate:
		p.Status = "duplicate_dates"
	case len(eligible) < 3:
		p.Status = "insufficient"
	default:
		sampleDates := make([]string, len(eligible))
		for i, v := range eligible {
			sampleDates[i] = v.OccurredOn
		}
		rules, e := billingschedule.InferHistory(sampleDates)
		if e != nil {
			return p, e
		}
		for _, rule := range rules {
			c := billingschedule.HistoryCandidate{Rule: rule}
			if s.Status == "ended" {
				c.Reason = "ended"
			} else {
				c.CandidateDate, e = billingschedule.Next(rule, today)
				if e != nil {
					return p, e
				}
				if c.CandidateDate == nil {
					c.Reason = "out_of_range"
				}
			}
			p.Candidates = append(p.Candidates, c)
		}
		switch len(p.Candidates) {
		case 0:
			p.Status = "no_match"
		case 1:
			p.Status = "ready"
		default:
			p.Status = "ambiguous"
		}
	}
	if s.Status == "ended" {
		p.Reason = "ended"
	}
	return p, nil
}
