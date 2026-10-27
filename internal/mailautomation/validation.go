package mailautomation

import (
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"time"
)

const MaxSafeInteger int64 = 9007199254740991

var Japan = time.FixedZone("Asia/Tokyo", 9*60*60)

func ValidVersion(v int64, zero bool) bool { return v >= 0 && (zero || v > 0) && v < MaxSafeInteger }
func DefaultScope(now time.Time) Scope {
	now = now.In(Japan)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, Japan)
	day := today.Day()
	last := time.Date(today.Year()-1, today.Month()+1, 0, 0, 0, 0, 0, Japan).Day()
	if day > last {
		day = last
	}
	since := time.Date(today.Year()-1, today.Month(), day, 0, 0, 0, 0, Japan)
	return Scope{Since: since.Format(time.DateOnly), Before: today.AddDate(0, 0, 1).Format(time.DateOnly), Timezone: "Asia/Tokyo", FullEmail: true, IntervalSeconds: 300}
}
func Bounds(s Scope) (time.Time, time.Time, error) {
	a, e := time.ParseInLocation(time.DateOnly, s.Since, Japan)
	b, f := time.ParseInLocation(time.DateOnly, s.Before, Japan)
	if e != nil || f != nil || a.Year() < 1900 || b.Year() > 9999 || !b.After(a) {
		return a, b, ErrInvalid
	}
	return a, b, nil
}

type Window struct{ Since, Before string }

func Windows(s Scope) ([]Window, error) {
	a, b, e := Bounds(s)
	if e != nil {
		return nil, e
	}
	out := []Window{}
	for a.Before(b) {
		end := a.AddDate(0, 0, 366)
		if end.After(b) {
			end = b
		}
		out = append(out, Window{a.Format(time.DateOnly), end.Format(time.DateOnly)})
		a = end
	}
	return out, nil
}
func ValidInput(in Input) bool {
	_, _, e := Bounds(Scope{Since: in.Since, Before: in.Before})
	return e == nil && ValidVersion(in.ExpectedSettingsVersion, true) && ValidVersion(in.ExpectedAccountVersion, false) && ValidVersion(in.RuntimeVersion, false) && ValidVersion(in.PolicyVersion, false) && mailsync.ValidID(in.ProfileID) && mailsync.ValidID(in.RequestID) && in.Confirmed && in.FullEmail && in.IntervalSeconds >= 60 && in.IntervalSeconds <= 86400
}
func ValidControl(in Control) bool {
	return ValidVersion(in.ExpectedSettingsVersion, false) && mailsync.ValidID(in.RequestID)
}

// Discovery retries are safe UID/metadata reads, never inference attempts.
func DiscoveryBackoff(failures int) (time.Duration, bool) {
	if failures < 1 || failures > 5 {
		return 0, false
	}
	return (5 * time.Second) << uint(failures-1), true
}
