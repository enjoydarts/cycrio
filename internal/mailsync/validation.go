package mailsync

import (
	"github.com/google/uuid"
	"math"
	"time"
)

func ValidID(s string) bool {
	v, e := uuid.Parse(s)
	return e == nil && v != uuid.Nil && v.String() == s
}

// ValidInput uses calendar dates with an exclusive upper boundary in IMAP's date domain.
func ValidInput(in Input) bool {
	a, e := time.Parse(time.DateOnly, in.Since)
	b, f := time.Parse(time.DateOnly, in.Before)
	return e == nil && f == nil && a.Year() >= 1900 && a.Year() <= 9999 && b.Year() >= 1900 && b.Year() <= 9999 && b.After(a) && b.Sub(a) <= 366*24*time.Hour && in.ExpectedVersion > 0 && in.ExpectedVersion < math.MaxInt64 && ValidID(in.RequestID) && in.MaxMessages >= 1 && in.MaxMessages <= 1000
}

// ValidPage checks a complete adapter result before any durable state advances.
func ValidPage(req PageRequest, p Page) bool {
	if req.Mode != "" && req.Mode != "manual" {
		if !p.Initialized || req.UIDValidity != 0 && req.UIDValidity != p.UIDValidity {
			return false
		}
		if req.Mode == "bootstrap" {
			return req.Limit >= 1 && req.Limit <= 100 && p.UIDValidity > 0 && p.Done && p.AfterUID == 0 && len(p.UIDs) == 0
		}
		if req.Initialized && p.UpperUID != req.UpperUID {
			return false
		}
	}
	if p.UIDValidity == 0 || req.Limit < 1 || req.Limit > 100 || len(p.UIDs) > req.Limit || p.AfterUID > p.UpperUID {
		return false
	}
	after := uint32(0)
	if req.UIDValidity == p.UIDValidity {
		after = req.AfterUID
		if (req.Initialized || (req.Mode == "" || req.Mode == "manual") && req.UpperUID > 0) && p.UpperUID != req.UpperUID {
			return false
		}
	}
	if p.AfterUID < after || !p.Done && p.AfterUID <= after || p.Done && p.AfterUID != p.UpperUID {
		return false
	}
	prev := after
	for _, uid := range p.UIDs {
		if uid <= prev || uid > p.AfterUID || uid > p.UpperUID {
			return false
		}
		prev = uid
	}
	return true
}
