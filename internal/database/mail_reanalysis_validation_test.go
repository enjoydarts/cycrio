package database

import (
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"github.com/google/uuid"
)

func TestReanalysisInputStrictPastUTC(t *testing.T) {
	base := mailprocessing.ReanalysisInput{Input: mailprocessing.Input{ExpectedVersion: 1, RuntimeVersion: 1, ProfileID: uuid.NewString(), PolicyVersion: 1, RequestID: uuid.NewString(), MaxMessages: 100, Confirmed: true, FullEmail: true}, Before: "2020-01-01T00:00:00.123456789Z"}
	if !mailprocessing.ValidReanalysis(base) {
		t.Fatal("valid RFC3339Nano rejected")
	}
	for _, before := range []string{"", "2020-01-01", "2020-01-01T00:00:00+00:00", "2020-01-01T00:00:00,123Z", time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)} {
		in := base
		in.Before = before
		if mailprocessing.ValidReanalysis(in) {
			t.Errorf("invalid cutoff accepted: %s", before)
		}
	}
	for _, change := range []func(*mailprocessing.ReanalysisInput){func(in *mailprocessing.ReanalysisInput) { in.FullEmail = false }, func(in *mailprocessing.ReanalysisInput) { in.Confirmed = false }, func(in *mailprocessing.ReanalysisInput) { in.HistoryID = "manual:legacy" }, func(in *mailprocessing.ReanalysisInput) { in.MaxMessages = 101 }} {
		in := base
		change(&in)
		if mailprocessing.ValidReanalysis(in) {
			t.Fatal("invalid confirmation/scope accepted")
		}
	}
}
