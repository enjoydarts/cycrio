package erasure

import "testing"

func TestNormalizeRequiresExplicitConfirmationAndCanonicalNonce(t *testing.T) {
	good := Input{RequestID: "12345678-1234-4234-8234-123456789012", Confirmed: true, Confirmation: "退会する"}
	if _, e := Normalize(good); e != nil {
		t.Fatal(e)
	}
	for _, in := range []Input{{}, {RequestID: good.RequestID, Confirmation: good.Confirmation}, {RequestID: good.RequestID, Confirmed: true, Confirmation: "退会"}, {RequestID: "00000000-0000-0000-0000-000000000000", Confirmed: true, Confirmation: good.Confirmation}} {
		if _, e := Normalize(in); e != ErrInvalid {
			t.Fatalf("accepted %#v: %v", in, e)
		}
	}
}
