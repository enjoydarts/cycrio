package modelcatalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"regexp"
	"strings"
)

var decimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?$`)

func ValidDecimal(s string) bool {
	if !decimalPattern.MatchString(s) {
		return false
	}
	parts := strings.Split(s, ".")
	digits := len(parts[0])
	if len(parts) == 2 {
		if len(parts[1]) > 24 {
			return false
		}
		digits += len(parts[1])
	}
	return digits <= 36
}

// PerMillion converts a validated decimal without floating point rounding.
func PerMillion(s string) (string, error) {
	if !ValidDecimal(s) {
		return "", ErrInvalid
	}
	parts := strings.Split(s, ".")
	whole := parts[0]
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	digits := whole + frac
	scale := len(frac) + 6
	for len(digits) <= scale {
		digits = "0" + digits
	}
	result := strings.TrimLeft(digits[:len(digits)-scale], "0")
	if result == "" {
		result = "0"
	}
	fraction := strings.TrimRight(digits[len(digits)-scale:], "0")
	if fraction != "" {
		result += "." + fraction
	}
	return result, nil
}
func decodeJSON(raw []byte, v any) error {
	strict := json.NewDecoder(bytes.NewReader(raw))
	strict.UseNumber()
	if validateJSONValue(strict, 0) != nil {
		return ErrInvalid
	}
	if _, err := strict.Token(); err != io.EOF {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(v) != nil {
		return ErrInvalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return ErrInvalid
	}
	return nil
}
func validateJSONValue(d *json.Decoder, depth int) error {
	if depth > 64 {
		return ErrInvalid
	}
	token, err := d.Token()
	if err != nil {
		return ErrInvalid
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			keyToken, err := d.Token()
			if err != nil {
				return ErrInvalid
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return ErrInvalid
			}
			seen[key] = true
			if validateJSONValue(d, depth+1) != nil {
				return ErrInvalid
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return ErrInvalid
		}
	case '[':
		for d.More() {
			if validateJSONValue(d, depth+1) != nil {
				return ErrInvalid
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
func ParseOpenRouterPricing(raw json.RawMessage) (Price, error) {
	var fields map[string]json.RawMessage
	if decodeJSON(raw, &fields) != nil || fields == nil {
		return Price{}, ErrInvalid
	}
	if _, ok := fields["prompt"]; !ok {
		return Price{}, ErrInvalid
	}
	if _, ok := fields["completion"]; !ok {
		return Price{}, ErrInvalid
	}
	p := Price{Currency: "USD", Source: "api", Rates: map[string]Rate{}, RawPricing: append(json.RawMessage(nil), raw...)}
	mapping := map[string][2]string{"prompt": {"input", "USD/token"}, "completion": {"output", "USD/token"}, "input_cache_read": {"cache_read", "USD/token"}, "input_cache_write": {"cache_write", "USD/token"}, "input_cache_write_1h": {"cache_write_1h", "USD/token"}, "request": {"request", "USD/request"}, "image": {"image", "USD/image"}, "image_output": {"image_output", "USD/image"}, "image_token": {"image_token", "USD/token"}, "audio": {"audio", "USD/token"}, "audio_output": {"audio_output", "USD/token"}, "input_audio_cache": {"audio_cache", "USD/token"}, "web_search": {"web_search", "USD/search"}, "internal_reasoning": {"reasoning", "USD/token"}}
	for key, target := range mapping {
		v, exists := fields[key]
		if !exists {
			continue
		}
		var amount string
		if json.Unmarshal(v, &amount) != nil {
			return Price{}, ErrInvalid
		}
		// OpenRouter's variable-price routers use -1 as an unknown rate sentinel.
		if amount == "-1" {
			continue
		}
		if !ValidDecimal(amount) {
			return Price{}, ErrInvalid
		}
		p.Rates[target[0]] = Rate{amount, target[1]}
	}
	return p, nil
}

// PricingHash includes all raw fields, preserving JSON numbers exactly and normalizing object order.
func PricingHash(p Price) (string, error) {
	var raw any
	if decodeJSON(p.RawPricing, &raw) != nil {
		return "", ErrInvalid
	}
	raw = normalizeJSONNumbers(raw)
	canonical, err := json.Marshal(struct {
		Currency  string
		Rates     map[string]Rate
		Raw       any
		Effective any
		URL       string
	}{p.Currency, p.Rates, raw, p.EffectiveFrom, p.SourceURL})
	if err != nil {
		return "", ErrInvalid
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// JSONB may expand exponents or trim fractions. Canonical decimal coefficients
// preserve arbitrarily precise numeric values without expanding huge exponents.
func canonicalNumber(value json.Number) json.Number {
	s := string(value)
	negative := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	exponent := new(big.Int)
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		exponent.SetString(s[i+1:], 10)
		s = s[:i]
	}
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(s)-dot-1)))
		s = s[:dot] + s[dot+1:]
	}
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return json.Number("0")
	}
	trimmed := strings.TrimRight(s, "0")
	exponent.Add(exponent, big.NewInt(int64(len(s)-len(trimmed))))
	s = trimmed
	if negative {
		s = "-" + s
	}
	if exponent.Sign() != 0 {
		s += "e" + exponent.String()
	}
	return json.Number(s)
}
func normalizeJSONNumbers(value any) any {
	switch v := value.(type) {
	case json.Number:
		return canonicalNumber(v)
	case []any:
		for i, item := range v {
			v[i] = normalizeJSONNumbers(item)
		}
	case map[string]any:
		for key, item := range v {
			v[key] = normalizeJSONNumbers(item)
		}
	}
	return value
}
