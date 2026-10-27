// Package autoregistration registers saved AI facts without owner confirmation.
package autoregistration

type Preference struct {
	Enabled bool  `json:"enabled"`
	Version int64 `json:"version"`
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
