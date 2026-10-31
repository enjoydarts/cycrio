// Package autoregistration describes historical automatic registration metadata.
package autoregistration

type Preference struct {
	ApprovalRequired bool  `json:"approval_required"`
	Enabled          bool  `json:"enabled"`
	Version          int64 `json:"version"`
}
type PreferenceInput struct {
	Enabled         bool  `json:"enabled"`
	ExpectedVersion int64 `json:"expected_version"`
}
type Outcome struct {
	EventIndex     int     `json:"event_index"`
	State          string  `json:"state"`
	Reason         string  `json:"reason"`
	SubscriptionID *string `json:"subscription_id"`
}
