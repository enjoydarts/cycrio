package priceresearch

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

var researchSecretPattern = regexp.MustCompile(`\b(?:sk-|rk-)[A-Za-z0-9_-]{8,}`)

const maxTraceResponse = 256 << 10

func maskResearchTrace(raw []byte, key []byte) string {
	maskPlain := func(text string) string {
		if len(key) > 0 {
			text = strings.ReplaceAll(text, string(key), "[REDACTED]")
		}
		return researchSecretPattern.ReplaceAllString(text, "[REDACTED]")
	}
	escapes := regexp.MustCompile(`(?:\\(?:u[0-9a-fA-F]{4}|["\\/bfnrt]))+`)
	mask := func(text string) string {
		normalized := text
		for depth := 0; depth < 32; depth++ {
			decoded := escapes.ReplaceAllStringFunc(normalized, func(escaped string) string {
				var value string
				if json.Unmarshal([]byte(`"`+escaped+`"`), &value) != nil {
					return escaped
				}
				return value
			})
			if decoded == normalized {
				return maskPlain(text)
			}
			if maskPlain(decoded) != decoded {
				// A string can itself contain nested or incomplete JSON (for
				// example output_text). Redact it whole rather than editing
				// escapes in a way that could retain a recoverable secret.
				return "[REDACTED]"
			}
			normalized = decoded
		}
		// Bounded work must not leave deeper escape layers unchecked.
		return "[REDACTED]"
	}
	if json.Valid(raw) {
		// Inspect tokens first: decoding into a map alone would discard earlier
		// duplicate fields and could miss a credential in the overwritten value.
		tokens := json.NewDecoder(bytes.NewReader(raw))
		tokens.UseNumber()
		needsMask := false
		for {
			token, err := tokens.Token()
			if err != nil {
				break
			}
			if value, ok := token.(string); ok && mask(value) != value {
				needsMask = true
			}
		}
		if !needsMask {
			return string(raw)
		}
		var document any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&document) != nil {
			return "[REDACTED]"
		}
		var walk func(any) any
		walk = func(value any) any {
			switch value := value.(type) {
			case string:
				redacted := mask(value)
				return redacted
			case []any:
				for index, child := range value {
					value[index] = walk(child)
				}
			case map[string]any:
				redacted := make(map[string]any, len(value))
				for name, child := range value {
					safeName := mask(name)
					redacted[safeName] = walk(child)
				}
				return redacted
			}
			return value
		}
		document = walk(document)
		encoded, err := json.Marshal(document)
		if err != nil {
			return "[REDACTED]"
		}
		return string(encoded)
	}
	// Error bodies can be truncated mid-string or malformed JSON. Decode valid
	// escape runs without requiring a complete document, then fail closed if a
	// recoverable credential appears. Keep harmless malformed responses intact.
	text := string(raw)
	if mask(text) != text {
		return "[REDACTED]"
	}
	return text
}

func captureResearchTrace(body []byte, r Request, official map[string]Quote) *TraceDocument {
	refs := map[string]Quote{}
	for _, target := range r.Targets {
		if q, ok := official[target.ModelID]; ok {
			refs[target.ModelID] = q
		}
	}
	return &TraceDocument{Request: json.RawMessage(maskResearchTrace(body, r.Key)), ReferencePrices: refs, Validation: map[string]string{}}
}

func finishResearchTrace(out *Result) {
	if out.Trace == nil {
		return
	}
	for id, code := range out.ModelIssues {
		if _, exists := out.Trace.Validation[id]; !exists {
			out.Trace.Validation[id] = code
		}
	}
	for _, q := range out.Models {
		if _, exists := out.Trace.Validation[q.ModelID]; !exists {
			out.Trace.Validation[q.ModelID] = "validated_model_result"
		}
	}
	if out.FailureCode != "" {
		out.Trace.Validation["_response"] = out.FailureCode
	}
}
