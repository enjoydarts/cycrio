package billingschedule

import "github.com/enjoydarts/cycrio/internal/subscriptions"

const HistoryAlgorithmVersion = "history-exact-v1"

type HistoryFilter struct {
	StartDate string `json:"start_date"`
	Currency  string `json:"currency"`
}
type HistoryBasis struct {
	Filter           HistoryFilter `json:"filter"`
	Token            string        `json:"token"`
	AlgorithmVersion string        `json:"algorithm_version"`
}
type HistorySample struct {
	ID         string `json:"id"`
	Version    int64  `json:"version"`
	OccurredOn string `json:"occurred_on"`
	Amount     string `json:"amount"`
	Currency   string `json:"currency"`
}
type HistoryEntry struct {
	HistorySample
	Kind   string `json:"kind"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}
type HistoryCounts struct {
	Total             int `json:"total"`
	Eligible          int `json:"eligible"`
	Sampled           int `json:"sampled"`
	OlderEligible     int `json:"older_eligible"`
	Voided            int `json:"voided"`
	Invoices          int `json:"invoices"`
	Refunds           int `json:"refunds"`
	OtherCurrency     int `json:"other_currency"`
	Refunded          int `json:"refunded"`
	ReferencedRefunds int `json:"referenced_refunds"`
}
type HistoryCandidate struct {
	Rule          Rule    `json:"rule"`
	CandidateDate *string `json:"candidate_date"`
	Reason        string  `json:"reason"`
}
type HistoryPreview struct {
	Subscription     subscriptions.Subscription `json:"subscription"`
	Filter           HistoryFilter              `json:"filter"`
	AsOf             string                     `json:"as_of"`
	Timezone         string                     `json:"timezone"`
	AlgorithmVersion string                     `json:"algorithm_version"`
	Token            string                     `json:"token"`
	Status           string                     `json:"status"`
	Reason           string                     `json:"reason"`
	Counts           *HistoryCounts             `json:"counts"`
	Samples          []HistorySample            `json:"samples"`
	Candidates       []HistoryCandidate         `json:"candidates"`
	Entries          []HistoryEntry             `json:"entries"`
}
type HistoryEvidence struct {
	HistoryBasis
	AsOf         string          `json:"as_of"`
	Samples      []HistorySample `json:"samples"`
	SelectedRule Rule            `json:"selected_rule"`
}
