package aibudget

import "testing"

func TestNormalize(t *testing.T) {
	for _, value := range []string{"0", "12.345678901234", "999999999999999999999999.999999999999"} {
		got, err := Normalize(Input{MonthlyLimitUSD: &value, Confirmed: true})
		if err != nil || got.MonthlyLimitUSD == nil || *got.MonthlyLimitUSD != value {
			t.Fatal(value, got, err)
		}
	}
	for _, value := range []string{"", "-1", "01", "1e2", "1.1234567890123", "1000000000000000000000000", " 1"} {
		if _, err := Normalize(Input{MonthlyLimitUSD: &value, Confirmed: true}); err != ErrInvalid {
			t.Fatal(value, err)
		}
	}
	if _, err := Normalize(Input{Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := Normalize(Input{}); err != ErrInvalid {
		t.Fatal(err)
	}
	if _, err := Normalize(Input{ExpectedVersion: -1, Confirmed: true}); err != ErrInvalid {
		t.Fatal(err)
	}
	if _, err := Normalize(Input{ExpectedVersion: 9007199254740991, Confirmed: true}); err != ErrInvalid {
		t.Fatal(err)
	}
}
