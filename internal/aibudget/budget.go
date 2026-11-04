package aibudget

import (
	"errors"
	"regexp"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid budget input")
	ErrConflict    = errors.New("budget conflict")
	ErrNotFound    = errors.New("budget user not found")
	ErrUnavailable = errors.New("budget unavailable")
	ErrForbidden   = errors.New("budget forbidden")
	ErrExceeded    = errors.New("budget exceeded")
	ErrUnknown     = errors.New("budget cost unknown")
	ErrPending     = errors.New("budget call pending")
)

type Input struct {
	ExpectedVersion int64   `json:"expected_version"`
	MonthlyLimitUSD *string `json:"monthly_limit_usd"`
	Confirmed       bool    `json:"confirmed"`
}
type State struct {
	UserID          string    `json:"user_id"`
	Version         int64     `json:"version"`
	MonthlyLimitUSD *string   `json:"monthly_limit_usd"`
	Month           string    `json:"month"`
	Timezone        string    `json:"timezone"`
	From            time.Time `json:"from"`
	To              time.Time `json:"to"`
	SpentUSD        string    `json:"spent_usd"`
	UnknownCalls    int64     `json:"unknown_calls"`
	PendingCalls    int64     `json:"pending_calls"`
	NonUSDCalls     int64     `json:"non_usd_calls"`
	BlockedReason   string    `json:"blocked_reason"`
}

var amountPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,23})(\.[0-9]{1,12})?$`)

func Normalize(in Input) (Input, error) {
	if !in.Confirmed || in.ExpectedVersion < 0 || in.ExpectedVersion >= 9007199254740991 || in.MonthlyLimitUSD != nil && !amountPattern.MatchString(*in.MonthlyLimitUSD) {
		return Input{}, ErrInvalid
	}
	return in, nil
}
