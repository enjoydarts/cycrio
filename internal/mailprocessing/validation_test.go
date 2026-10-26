package mailprocessing

import (
	"github.com/google/uuid"
	"strings"
	"testing"
)

func input() Input {
	return Input{ExpectedVersion: 1, RuntimeVersion: 1, ProfileID: uuid.NewString(), PolicyVersion: 1, RequestID: uuid.NewString(), MaxMessages: 1, Confirmed: true}
}
func TestValidInput(t *testing.T) {
	for _, n := range []int{1, 100} {
		in := input()
		in.MaxMessages = n
		if !ValidInput(in) {
			t.Fatal("valid boundary", n)
		}
	}
	for _, mutate := range []func(*Input){func(i *Input) { i.MaxMessages = 0 }, func(i *Input) { i.MaxMessages = 101 }, func(i *Input) { i.Confirmed = false }, func(i *Input) { i.ExpectedVersion = 0 }, func(i *Input) { i.RuntimeVersion = -1 }, func(i *Input) { i.PolicyVersion = 0 }, func(i *Input) { i.RequestID = "urn:uuid:" + i.RequestID }, func(i *Input) { i.ProfileID = "" }} {
		in := input()
		mutate(&in)
		if ValidInput(in) {
			t.Fatalf("accepted %+v", in)
		}
	}
}
func TestValidPolicy(t *testing.T) {
	in := PolicyInput{ProfileID: uuid.NewString(), Enabled: true, PrimaryRetention: "contract: no retention", SecondaryRetention: "contract: 30 days", PrimaryConfirmed: true, SecondaryConfirmed: true}
	if !ValidPolicy(in) {
		t.Fatal("valid policy")
	}
	for _, mutate := range []func(*PolicyInput){func(p *PolicyInput) { p.ExpectedVersion = -1 }, func(p *PolicyInput) { p.PrimaryRetention = "" }, func(p *PolicyInput) { p.PrimaryRetention = strings.Repeat("あ", 501) }, func(p *PolicyInput) { p.PrimaryRetention = "x\ny" }, func(p *PolicyInput) { p.PrimaryRetention = string([]byte{255}) }, func(p *PolicyInput) { p.SecondaryConfirmed = false }} {
		bad := in
		mutate(&bad)
		if ValidPolicy(bad) {
			t.Fatal("invalid policy accepted")
		}
	}
	if !ValidPolicy(PolicyInput{ProfileID: in.ProfileID}) {
		t.Fatal("disabled empty policy")
	}
}
