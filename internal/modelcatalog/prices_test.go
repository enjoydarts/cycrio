package modelcatalog

import (
	"encoding/json"
	"testing"
)

func TestPricingExactUnitsAndRawMetadata(t *testing.T) {
	raw := json.RawMessage(`{"prompt":"0","completion":"0.000001234567890123456789","discount":0.1234567890123456789,"overrides":[{"prompt":"0.0000001","conditions":{"region":"x"}}],"future":"1.2"}`)
	p, err := ParseOpenRouterPricing(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.Rates["input"].Amount != "0" || p.Rates["output"].Amount != "0.000001234567890123456789" || p.Rates["output"].Unit != "USD/token" {
		t.Fatalf("rates: %#v", p.Rates)
	}
	if _, exists := p.Rates["cache_read"]; exists {
		t.Fatal("missing price became zero")
	}
	if string(p.RawPricing) != string(raw) {
		t.Fatal("raw pricing lost")
	}
	a, _ := PricingHash(p)
	b, _ := PricingHash(Price{Currency: p.Currency, Rates: p.Rates, RawPricing: json.RawMessage(`{"future":"1.2","overrides":[{"conditions":{"region":"x"},"prompt":"0.0000001"}],"discount":0.1234567890123456789,"completion":"0.000001234567890123456789","prompt":"0"}`)})
	if a != b {
		t.Fatal("object ordering changed hash")
	}
	amount, err := PerMillion("0.123456789012345678901234")
	if err != nil || amount != "0.000000123456789012345678901234" {
		t.Fatal(amount, err)
	}
}

func TestPricingRejectsInvalidAndPreservesUnknown(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"prompt":"-2","completion":"1"}`, `{"prompt":0,"completion":"1"}`, `{"prompt":"NaN","completion":"1"}`} {
		if _, err := ParseOpenRouterPricing(json.RawMessage(raw)); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	for _, v := range []string{"-1", "1e3", "NaN", ".1", "1.", "1234567890123456789012345678901234567", "0.1234567890123456789012345"} {
		if ValidDecimal(v) {
			t.Fatal("accepted", v)
		}
	}
}

func TestPricingVariableSentinelIsUnknownNotZero(t *testing.T) {
	raw := json.RawMessage(`{"prompt":"-1","completion":"-1","request":"0"}`)
	p, err := ParseOpenRouterPricing(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Rates["input"]; ok {
		t.Fatal("variable input normalized as fixed price")
	}
	if _, ok := p.Rates["output"]; ok {
		t.Fatal("variable output normalized as fixed price")
	}
	if p.Rates["request"].Amount != "0" || string(p.RawPricing) != string(raw) {
		t.Fatal(p)
	}
}

func TestPricingHashNormalizesJSONNumbersExactly(t *testing.T) {
	a := Price{Currency: "USD", Rates: map[string]Rate{}, RawPricing: json.RawMessage(`{"discount":1e-7,"nested":[-0,1.2500,12345678901234567890.1234567890123456789]}`)}
	b := a
	b.RawPricing = json.RawMessage(`{"discount":0.0000001,"nested":[0,1.25,123456789012345678901234567890123456789e-19]}`)
	ah, _ := PricingHash(a)
	bh, _ := PricingHash(b)
	if ah != bh {
		t.Fatal("semantically identical JSON numbers hash differently")
	}
}
