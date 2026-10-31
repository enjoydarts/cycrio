package ai

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const maxBody = 2 << 20

var decimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,17})(\.[0-9]{1,18})?$`)

const currencyCodes = " AED AFN ALL AMD ANG AOA ARS AUD AWG AZN BAM BBD BDT BGN BHD BIF BMD BND BOB BOV BRL BSD BTN BWP BYN BZD CAD CDF CHE CHF CHW CLF CLP CNY COP COU CRC CUP CVE CZK DJF DKK DOP DZD EGP ERN ETB EUR FJD FKP GBP GEL GHS GIP GMD GNF GTQ GYD HKD HNL HTG HUF IDR ILS INR IQD IRR ISK JMD JOD JPY KES KGS KHR KMF KPW KRW KWD KYD KZT LAK LBP LKR LRD LSL LYD MAD MDL MGA MKD MMK MNT MOP MRU MUR MVR MWK MXN MXV MYR MZN NAD NGN NIO NOK NPR NZD OMR PAB PEN PGK PHP PKR PLN PYG QAR RON RSD RUB RWF SAR SBD SCR SDG SEK SGD SHP SLE SOS SRD SSP STN SVC SYP SZL THB TJS TMT TND TOP TRY TTD TWD TZS UAH UGX USD USN UYI UYU UYW UZS VED VES VND VUV WST XAF XCD XCG XDR XOF XPF XSU XUA YER ZAR ZMW ZWG "

// decodeJSON rejects duplicate keys, trailing values, excessive nesting and invalid UTF-8.
func decodeJSON(b []byte) (any, error) {
	if len(b) > maxBody || !utf8.Valid(b) {
		return nil, &Error{Code: "invalid_json"}
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	v, err := readValue(d, 0)
	if err != nil {
		return nil, &Error{Code: "invalid_json"}
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, &Error{Code: "invalid_json"}
	}
	return v, nil
}
func readValue(d *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, io.ErrUnexpectedEOF
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t {
	case json.Delim('{'):
		m := map[string]any{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return nil, e
			}
			s, ok := k.(string)
			if !ok || strings.ContainsRune(s, '\uFFFD') {
				return nil, io.ErrUnexpectedEOF
			}
			if _, ok = m[s]; ok {
				return nil, io.ErrUnexpectedEOF
			}
			v, e := readValue(d, depth+1)
			if e != nil {
				return nil, e
			}
			m[s] = v
		}
		_, err = d.Token()
		return m, err
	case json.Delim('['):
		a := []any{}
		for d.More() {
			v, e := readValue(d, depth+1)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		_, err = d.Token()
		return a, err
	default:
		if s, ok := t.(string); ok && strings.ContainsRune(s, '\uFFFD') {
			return nil, io.ErrUnexpectedEOF
		}
		if n, ok := t.(json.Number); ok {
			if _, ok := decimalRat(string(n)); !ok {
				return nil, io.ErrUnexpectedEOF
			}
		}
		return t, nil
	}
}

func ExtractionSchema() map[string]any {
	nullableString := func() map[string]any { return map[string]any{"type": []string{"string", "null"}} }
	// Keep nullable enums as separate branches: some upstream schema adapters
	// reject null values inside enum even when the field type includes null.
	intervalUnit := map[string]any{"anyOf": []any{map[string]any{"type": "string", "enum": []string{"day", "week", "month", "year"}}, map[string]any{"type": "null"}}}
	props := map[string]any{"service": nullableString(), "plan": nullableString(), "event_type": map[string]any{"type": "string", "enum": []string{"invoice", "payment", "renewal", "refund", "change", "cancellation", "trial", "payment_failed", "payment_reminder", "suspension"}}, "amount": nullableString(), "currency": nullableString(), "billed_at": nullableString(), "period_start": nullableString(), "period_end": nullableString(), "next_billing_at": nullableString(), "interval_unit": intervalUnit, "interval_count": map[string]any{"type": []string{"integer", "null"}}, "recurring_amount": nullableString(), "recurring_currency": nullableString(), "effective_date": nullableString(), "access_end_date": nullableString(), "contract_kind": map[string]any{"anyOf": []any{map[string]any{"type": "string", "enum": []string{"recurring", "fixed_term"}}, map[string]any{"type": "null"}}}}
	required := []string{"service", "plan", "event_type", "amount", "currency", "billed_at", "period_start", "period_end", "next_billing_at", "interval_unit", "interval_count", "recurring_amount", "recurring_currency", "effective_date", "access_end_date", "contract_kind"}
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"events"}, "properties": map[string]any{"events": map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": props}}}}
}
func ValidateExtraction(b []byte) (Extraction, error) {
	bad := func() (Extraction, error) { return Extraction{}, &Error{Code: "invalid_output"} }
	v, err := decodeJSON(b)
	if err != nil {
		return bad()
	}
	root, ok := v.(map[string]any)
	if !ok || len(root) != 1 {
		return bad()
	}
	events, ok := root["events"].([]any)
	if !ok || len(events) > 32 {
		return bad()
	}
	keys := map[string]bool{"service": true, "plan": true, "event_type": true, "amount": true, "currency": true, "billed_at": true, "period_start": true, "period_end": true, "next_billing_at": true, "interval_unit": true, "interval_count": true}
	newKeys := map[string]bool{"recurring_amount": true, "recurring_currency": true, "effective_date": true, "access_end_date": true, "contract_kind": true}
	for _, v := range events {
		m, ok := v.(map[string]any)
		if !ok || (len(m) != len(keys) && len(m) != len(keys)+len(newKeys)) {
			return bad()
		}
		for k := range keys {
			if _, exists := m[k]; !exists {
				return bad()
			}
		}
		if len(m) != len(keys) {
			for k := range newKeys {
				if _, exists := m[k]; !exists {
					return bad()
				}
			}
		}
		for k, x := range m {
			if !keys[k] && !newKeys[k] {
				return bad()
			}
			if k == "interval_count" {
				if x != nil {
					n, ok := x.(json.Number)
					if !ok {
						return bad()
					}
					i, e := n.Int64()
					if e != nil || i < 1 || i > 10000 {
						return bad()
					}
				}
				continue
			}
			if x == nil {
				if k == "event_type" {
					return bad()
				}
				continue
			}
			s, ok := x.(string)
			if !ok {
				return bad()
			}
			switch k {
			case "service", "plan":
				if utf8.RuneCountInString(s) > 128 || strings.TrimSpace(s) == "" {
					return bad()
				}
			case "event_type":
				if !oneOf(s, "invoice", "payment", "renewal", "refund", "change", "cancellation", "trial", "payment_failed", "payment_reminder", "suspension") {
					return bad()
				}
			case "amount", "recurring_amount":
				if !decimalPattern.MatchString(s) {
					return bad()
				}
			case "currency", "recurring_currency":
				if len(s) != 3 || !strings.Contains(currencyCodes, " "+s+" ") {
					return bad()
				}
			case "interval_unit":
				if !oneOf(s, "day", "week", "month", "year") {
					return bad()
				}
			case "contract_kind":
				if !oneOf(s, "recurring", "fixed_term") {
					return bad()
				}
			default:
				if len(s) != 10 {
					return bad()
				}
				d, e := time.Parse("2006-01-02", s)
				if e != nil || d.Year() < 1 {
					return bad()
				}
			}
		}
	}
	var out Extraction
	if json.Unmarshal(b, &out) != nil {
		return bad()
	}
	return out, nil
}
