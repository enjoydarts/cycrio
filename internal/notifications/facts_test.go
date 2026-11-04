package notifications

import (
	"testing"
	"time"
)

func TestSameFactsExcludeTimingAndChannelsButPreserveCurrentPriceAndSource(t *testing.T) {
	a := Candidate{Kind: PriceIncrease, SourceKey: "source", ContractID: ptr(testContractID), ResultID: ptr(testResultID), EventIndex: ptr(0), Service: "Example", OldAmount: ptr("10"), NewAmount: ptr("20"), Currency: ptr("USD"), ScheduledAt: notificationNow(), InApp: true}
	b := a
	b.ScheduledAt = b.ScheduledAt.Add(time.Hour)
	b.InApp = false
	b.Email = true
	b.OldAmount = ptr("10.00")
	if !SameFacts(a, b) {
		t.Fatal("time/preferences/decimal representation are not different event facts")
	}
	b.OldAmount = ptr("15")
	if SameFacts(a, b) {
		t.Fatal("a stale baseline must not be reactivated")
	}
	b = a
	b.ResultID = ptr(testContractID)
	if SameFacts(a, b) {
		t.Fatal("different extraction source")
	}
	b = a
	b.OldAmount = ptr("invalid")
	if SameFacts(a, b) {
		t.Fatal("invalid prices cannot match")
	}
}
