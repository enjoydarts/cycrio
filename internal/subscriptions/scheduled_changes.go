package subscriptions

import "time"

func BuildScheduledChangePreview(s Subscription, source ChangeEvidence, additional []ChangeEvidence, today string) ScheduledChangePreview {
	date := ""
	if source.OriginalEvent.EffectiveDate != nil {
		date = *source.OriginalEvent.EffectiveDate
	}
	return ScheduledChangePreview{ChangePreview: buildChangePreview(s, source, additional, today, true), ScheduledFor: date}
}

type ScheduledChangePreview struct {
	ChangePreview
	ScheduledFor string `json:"scheduled_for"`
}
type ScheduleChangeInput struct {
	ChangeInput
	EffectiveOn string `json:"effective_on"`
}
type CancelScheduledChangeInput struct {
	ExpectedVersion int64 `json:"expected_version"`
	Confirmed       bool  `json:"confirmed"`
}
type ScheduledChange struct {
	ID             string                 `json:"id"`
	RequestID      string                 `json:"request_id"`
	SubscriptionID string                 `json:"subscription_id"`
	Version        int64                  `json:"version"`
	State          string                 `json:"state"`
	EffectiveOn    string                 `json:"effective_on"`
	BaseVersion    int64                  `json:"base_version"`
	Preview        ScheduledChangePreview `json:"preview"`
	SelectedFields []string               `json:"selected_fields"`
	CreatedAt      time.Time              `json:"created_at"`
	AppliedAt      *time.Time             `json:"applied_at"`
	Reason         string                 `json:"reason"`
}
type ScheduledChangeResult struct {
	Schedule ScheduledChange `json:"schedule"`
	Current  *Subscription   `json:"current"`
}
type ScheduledChangePage struct {
	Schedules []ScheduledChange `json:"schedules"`
	Total     int               `json:"total"`
	Limit     int               `json:"limit"`
	Offset    int               `json:"offset"`
}

func ValidScheduleChangeInput(in ScheduleChangeInput) bool {
	d, e := time.Parse("2006-01-02", in.EffectiveOn)
	return e == nil && d.Format("2006-01-02") == in.EffectiveOn && ValidChangeInput(in.ChangeInput)
}
func ValidCancelScheduledChangeInput(in CancelScheduledChangeInput) bool {
	return in.ExpectedVersion > 0 && in.Confirmed
}
