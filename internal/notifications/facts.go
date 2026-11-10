package notifications

import (
	"math/big"
	"reflect"
	"time"
)

// SameFacts compares immutable event meaning. Scheduling and enabled channels
// can change between generation passes and cannot establish a new occurrence.
func SameFacts(a, b Candidate) bool {
	normalize := func(c Candidate) (Candidate, bool) {
		if !validMoney(c.Amount, c.Currency) || !validMoney(c.OldAmount, c.Currency) || !validMoney(c.NewAmount, c.Currency) {
			return c, false
		}
		amount := func(v *string) *string {
			if v == nil {
				return nil
			}
			r, ok := new(big.Rat).SetString(*v)
			if !ok {
				return nil
			}
			s := r.RatString()
			return &s
		}
		c.Amount, c.OldAmount, c.NewAmount = amount(c.Amount), amount(c.OldAmount), amount(c.NewAmount)
		c.ScheduledAt = time.Time{}
		c.InApp, c.Email, c.Push = false, false, false
		return c, true
	}
	var validA, validB bool
	a, validA = normalize(a)
	b, validB = normalize(b)
	return validA && validB && reflect.DeepEqual(a, b)
}
