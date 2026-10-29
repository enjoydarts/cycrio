package subscriptions

import (
	"slices"
	"time"
)

type MessageFilter struct {
	AccountID string
	Stage     string
	Limit     int
	Offset    int
}

func InvalidMessageFilter(f MessageFilter) bool {
	return f.Limit < 1 || f.Limit > 100 || f.Offset < 0 || f.AccountID != "" && !ValidID(f.AccountID) || !slices.Contains([]string{"", "all", "primary_waiting", "primary_running", "secondary_waiting", "secondary_running", "completed", "attention", "not_related"}, f.Stage)
}

type StageCounts struct {
	NotStarted  int `json:"not_started"`
	Waiting     int `json:"waiting"`
	Running     int `json:"running"`
	Completed   int `json:"completed"`
	Skipped     int `json:"skipped"`
	NeedsReview int `json:"needs_review"`
	Failed      int `json:"failed"`
	Unknown     int `json:"unknown"`
	Cancelled   int `json:"cancelled"`
}

func (c *StageCounts) Add(state string, n int) {
	switch state {
	case "not_started":
		c.NotStarted += n
	case "waiting":
		c.Waiting += n
	case "running":
		c.Running += n
	case "completed":
		c.Completed += n
	case "skipped":
		c.Skipped += n
	case "needs_review":
		c.NeedsReview += n
	case "failed":
		c.Failed += n
	case "unknown":
		c.Unknown += n
	case "cancelled":
		c.Cancelled += n
	}
}

type MessageSummary struct {
	Discovered   int         `json:"discovered"`
	Primary      StageCounts `json:"primary"`
	Secondary    StageCounts `json:"secondary"`
	Registration StageCounts `json:"registration"`
}
type ImportMessage struct {
	ID           string    `json:"id"`
	AccountID    string    `json:"account_id"`
	AccountLabel string    `json:"account_label"`
	DiscoveredAt time.Time `json:"discovered_at"`
	Primary      string    `json:"primary"`
	Secondary    string    `json:"secondary"`
	Registration string    `json:"registration"`
	ResultID     *string   `json:"result_id"`
	Service      *string   `json:"service"`
	Amount       *string   `json:"amount"`
	Currency     *string   `json:"currency"`
	EventType    *string   `json:"event_type"`
	ErrorCode    string    `json:"error_code"`
}
type MessagePage struct {
	Messages []ImportMessage `json:"messages"`
	Total    int             `json:"total"`
	Limit    int             `json:"limit"`
	Offset   int             `json:"offset"`
	Summary  MessageSummary  `json:"summary"`
}
type MessageDetail struct {
	Message ImportMessage   `json:"message"`
	Result  *AnalysisResult `json:"result"`
}
