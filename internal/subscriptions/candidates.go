package subscriptions

import "github.com/enjoydarts/cycrio/internal/ai"

type Candidate struct {
	ResultID     string   `json:"result_id"`
	EventIndex   int      `json:"event_index"`
	AccountID    string   `json:"account_id"`
	AccountLabel string   `json:"account_label"`
	Event        ai.Event `json:"event"`
	MailCount    int      `json:"mail_count"`
}
type CandidatePage struct {
	Candidates []Candidate `json:"candidates"`
	Total      int         `json:"total"`
	Limit      int         `json:"limit"`
	Offset     int         `json:"offset"`
}
type DismissInput struct {
	SourceResultID   string `json:"source_result_id"`
	SourceEventIndex int    `json:"source_event_index"`
	Confirmed        bool   `json:"confirmed"`
}
type ProcessingSummary struct {
	MailCount         int `json:"mail_count"`
	PendingCount      int `json:"pending_count"`
	SucceededCount    int `json:"succeeded_count"`
	UnrelatedCount    int `json:"unrelated_count"`
	FailedCount       int `json:"failed_count"`
	UnknownCount      int `json:"unknown_count"`
	CandidateCount    int `json:"candidate_count"`
	SubscriptionCount int `json:"subscription_count"`
}
