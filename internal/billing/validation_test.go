package billing

import (
	"strings"
	"testing"
)

func TestFinancialValidation(t *testing.T) {
	good := Values{Kind: "payment", OccurredOn: "2024-02-29", Amount: "0001.00", Currency: "JPY", State: "active"}
	if _, e := Normalize(good); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*Values){func(v *Values) { v.OccurredOn = "2023-02-29" }, func(v *Values) { v.OccurredOn = "0000-01-01" }, func(v *Values) { v.Amount = "-1" }, func(v *Values) { v.Amount = "1." }, func(v *Values) { v.Amount = strings.Repeat("1", 25) }, func(v *Values) { v.Amount = "0.1234567890123" }, func(v *Values) { v.Kind = "unknown" }, func(v *Values) { v.State = "deleted" }, func(v *Values) { v.Currency = "jpy" }, func(v *Values) { v.Kind = "refund" }, func(v *Values) { s := "invalid"; v.RefundOfID = &s }} {
		v := good
		change(&v)
		if _, e := Normalize(v); e == nil {
			t.Errorf("accepted %+v", v)
		}
	}
	for _, amount := range []string{"0", "0000", "0.000000000001", strings.Repeat("9", 24) + ".123456789012"} {
		v := good
		v.Amount = amount
		if _, e := Normalize(v); e != nil {
			t.Fatal(amount, e)
		}
	}
	id := "00000000-0000-0000-0000-000000000001"
	v := good
	v.RefundOfID = &id
	if _, e := Normalize(v); e == nil {
		t.Fatal("payment with refund reference")
	}
	v.Kind = "refund"
	if _, e := Normalize(v); e != nil {
		t.Fatal(e)
	}
	if _, e := NormalizeCreate(CreateInput{Values: good, RequestID: id}); e == nil {
		t.Fatal("unconfirmed create")
	}
	if _, e := NormalizeUpdate(UpdateInput{Values: good, ExpectedVersion: 1}); e == nil {
		t.Fatal("unconfirmed update")
	}
	if !ValidSourceInput(SourceInput{ExpectedVersion: 1, SourceResultID: id, SourceEventIndex: 0, Confirmed: true}) {
		t.Fatal("zero source")
	}
}
func TestDecimalExactArithmetic(t *testing.T) {
	a := Decimal("999999999999999999999999.999999999999")
	b := Decimal("0.000000000001")
	if got := Canonical(Add(a, b)); got != "1000000000000000000000000" {
		t.Fatal(got)
	}
	if got := Canonical(Sub(Decimal("1.00"), Decimal("2.000"))); got != "-1" {
		t.Fatal(got)
	}
	if Decimal("0001.00").Cmp(Decimal("1")) != 0 || Canonical(Decimal("0.000")) != "0" {
		t.Fatal("normalization")
	}
}
func TestMonthBounds(t *testing.T) {
	for _, c := range []struct{ month, start, end string }{{"2024-02", "2024-02-01", "2024-03-01"}, {"2024-12", "2024-12-01", "2025-01-01"}, {"9999-12", "9999-12-01", "10000-01-01"}} {
		s, e, err := MonthBounds(c.month)
		if err != nil || s != c.start || e != c.end {
			t.Fatal(s, e, err)
		}
	}
	for _, m := range []string{"2024-2", "2024-13", "0000-01"} {
		if _, _, e := MonthBounds(m); e == nil {
			t.Fatal(m)
		}
	}
}
