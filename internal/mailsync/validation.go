package mailsync

import (
	"bytes"
	"github.com/google/uuid"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxCursorBytes = 128 << 10

func APIProvider(provider string) bool { return provider == "gmail" || provider == "outlook" }

func ValidRemoteID(id string) bool {
	return id != "" && len(id) <= 2048 && utf8.ValidString(id) && !strings.ContainsAny(id, "/\\") && !strings.ContainsFunc(id, unicode.IsControl)
}

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
	if APIProvider(req.Provider) {
		if req.Mode != "" && req.Mode != "manual" && req.Mode != "bootstrap" && req.Mode != "historical" && req.Mode != "incremental" {
			return false
		}
		if req.UIDValidity != 0 || req.AfterUID != 0 || req.UpperUID != 0 {
			return false
		}
		cursor := p.Cursor != "" || p.CursorEnvelope.KeyID != "" && len(p.CursorEnvelope.Ciphertext) > 0
		unchanged := p.Cursor != "" && req.Cursor == p.Cursor || p.CursorEnvelope.KeyID != "" && req.CursorEnvelope.KeyID == p.CursorEnvelope.KeyID && bytes.Equal(req.CursorEnvelope.Ciphertext, p.CursorEnvelope.Ciphertext)
		if req.Limit < 1 || req.Limit > 100 || len(p.RemoteIDs) > req.Limit || !p.Initialized || !cursor || len(p.Cursor) > MaxCursorBytes || len(p.CursorEnvelope.Ciphertext) > MaxCursorBytes+128 || !p.Done && unchanged || p.UIDValidity != 0 || p.AfterUID != 0 || p.UpperUID != 0 || len(p.UIDs) != 0 || req.Mode == "bootstrap" && len(p.RemoteIDs) != 0 {
			return false
		}
		seen := map[string]bool{}
		for _, id := range p.RemoteIDs {
			if !ValidRemoteID(id) || seen[id] {
				return false
			}
			seen[id] = true
		}
		return true
	}
	if len(p.RemoteIDs) != 0 || p.Cursor != "" || p.CursorEnvelope.KeyID != "" {
		return false
	}
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
