package mailprocessing

import (
	"github.com/google/uuid"
	"strings"
	"unicode"
	"unicode/utf8"
)

func ValidID(s string) bool {
	u, e := uuid.Parse(s)
	return e == nil && s == u.String() && u != uuid.Nil
}
func version(v int64) bool { return v >= 0 && v <= 9007199254740991 }
func ValidInput(in Input) bool {
	return version(in.ExpectedVersion) && in.ExpectedVersion > 0 && version(in.RuntimeVersion) && in.RuntimeVersion > 0 && version(in.PolicyVersion) && in.PolicyVersion > 0 && ValidID(in.ProfileID) && ValidID(in.RequestID) && in.MaxMessages >= 1 && in.MaxMessages <= 100 && in.Confirmed
}
func ValidRetry(in RetryInput) bool {
	return ValidInput(in.Input) && in.MaxMessages == 1 && ValidID(in.ExpectedResultID)
}
func ValidPolicy(in PolicyInput) bool {
	text := func(s string) bool {
		if !utf8.ValidString(s) || utf8.RuneCountInString(s) > 500 {
			return false
		}
		for _, r := range s {
			if unicode.IsControl(r) {
				return false
			}
		}
		return !in.Enabled || strings.TrimSpace(s) != ""
	}
	return version(in.ExpectedVersion) && ValidID(in.ProfileID) && text(in.PrimaryRetention) && text(in.SecondaryRetention) && (!in.Enabled || in.PrimaryConfirmed && in.SecondaryConfirmed)
}
