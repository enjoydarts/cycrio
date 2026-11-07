package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/enjoydarts/cycrio/internal/diagnostics"
)

var diagnosticHTML = regexp.MustCompile(`(?i)<\s*(?:!doctype\b|/?[a-z][a-z0-9]*\b[^>]*>)`)

// ProviderDiagnostic selects and sanitizes technical error fields for callers
// that use another official endpoint but share the same provider protocols.
func ProviderDiagnostic(body map[string]any, headers http.Header, request Request) error {
	return providerDiagnostic(body, headers, request)
}

func TransportDiagnostic(err error, request Request) error {
	if err == nil {
		return nil
	}
	return transportDiagnostic(err, request)
}

func diagnosticText(text string, r Request) string {
	return diagnostics.ScrubKnown(text, append([]string{string(r.Key), r.Input.Text, r.Input.Instructions}, criteriaValues(r.Input.ClassificationCriteria)...)...)
}

func criteriaValues(m map[string]string) []string {
	var out []string
	for _, value := range m {
		out = append(out, value)
	}
	return out
}

// Select documented technical fields only. In particular FastAPI's input and
// arbitrary context values may contain the complete submitted mail.
func providerDiagnostic(m map[string]any, headers http.Header, r Request) error {
	details := map[string]any{}
	scalar := func(value any) any {
		switch v := value.(type) {
		case string:
			// Even an error message can contain a reverse proxy HTML response.
			if diagnosticHTML.MatchString(v) {
				return nil
			}
			return diagnosticText(v, r)
		case json.Number:
			return diagnosticText(v.String(), r)
		}
		return nil
	}
	selectError := func(value any) map[string]any {
		selected := map[string]any{}
		for _, key := range []string{"message", "type", "code", "param"} {
			if v := scalar(obj(value)[key]); v != nil {
				selected[key] = v
			}
		}
		return selected
	}
	if e := selectError(m); len(e) > 0 {
		details["provider"] = e
	}
	if e := selectError(m["error"]); len(e) > 0 {
		details["error"] = e
	}
	if message, ok := m["error"].(string); ok {
		if value := scalar(message); value != nil {
			details["error_message"] = value
		}
	}
	if message, ok := m["detail"].(string); ok {
		if value := scalar(message); value != nil {
			details["detail_message"] = value
		}
	}
	if e := selectError(m["detail"]); len(e) > 0 {
		details["detail_error"] = e
	}
	for _, choice := range arr(m["choices"]) {
		if e := selectError(obj(choice)["error"]); len(e) > 0 {
			details["choice_error"] = e
			break
		}
	}
	var validation []any
	validationItems := arr(m["detail"])
	if detail := obj(m["detail"]); detail != nil {
		validationItems = []any{detail}
	}
	for _, value := range validationItems {
		if len(validation) >= 8 {
			break
		}
		item := obj(value)
		selected := map[string]any{}
		for _, key := range []string{"type", "msg"} {
			if v := scalar(item[key]); v != nil {
				selected[key] = v
			}
		}
		var loc []any
		for _, part := range arr(item["loc"]) {
			// These are protocol field names, not values echoed from input.
			if name, ok := part.(string); ok && len(loc) < 16 {
				switch name {
				case "body", "state", "model", "questions", "classification", "criteria", "instructions", "messages", "content", "role", "text", "input", "prompt":
					loc = append(loc, name)
					continue
				}
			}
			if v := scalar(part); v != nil && len(loc) < 16 {
				loc = append(loc, v)
			}
		}
		if len(loc) > 0 {
			var parts []string
			for _, part := range loc {
				parts = append(parts, fmt.Sprint(part))
			}
			selected["loc"] = strings.Join(parts, ".")
			if str(item["type"]) == "string_too_long" && strings.Join(parts, ".") == "body.state" && r.Provider == "liquid" {
				// FastAPI need not return actual_length. This local character
				// count explains the constraint failure without storing input.
				count := utf8.RuneCountInString(r.Input.Text)
				if !numericSecret(fmt.Sprint(count), r) {
					selected["submitted_characters"] = count
				}
			}
		}
		constraints := map[string]any{}
		for _, key := range []string{"max_length", "min_length", "actual_length", "limit_value", "le", "lt", "ge", "gt", "multiple_of", "max_digits", "decimal_places"} {
			if n, ok := obj(item["ctx"])[key].(json.Number); ok && !numericSecret(n.String(), r) {
				constraints[key] = n
			}
		}
		if len(constraints) > 0 {
			selected["constraints"] = constraints
		}
		if len(selected) > 0 {
			validation = append(validation, selected)
		}
	}
	if len(validation) > 0 {
		details["validation"] = validation
	}
	for _, key := range []string{"X-Request-Id", "Request-Id"} {
		if id := headers.Get(key); id != "" && safeMetadata(id, r) != "" && diagnosticText(id, r) == id {
			details["request_id"] = id
			break
		}
	}
	if len(details) == 0 {
		return nil
	}
	// Plain technical lines survive the shared scrubber's deliberate rejection
	// of arbitrary JSON payloads when this cause is reported later.
	var lines []string
	var flatten func(string, any)
	flatten = func(prefix string, value any) {
		switch v := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(v))
			for key := range v {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				label := key
				if prefix != "" {
					label = prefix + "." + key
				}
				flatten(label, v[key])
			}
		case []any:
			for index, item := range v {
				flatten(fmt.Sprintf("%s.%d", prefix, index), item)
			}
		default:
			lines = append(lines, fmt.Sprintf("%s: %v", prefix, v))
		}
	}
	flatten("", details)
	return errors.New(strings.Join(lines, "\n"))
}

func numericSecret(value string, r Request) bool {
	for _, secret := range append([]string{string(r.Key), r.Input.Text, r.Input.Instructions}, criteriaValues(r.Input.ClassificationCriteria)...) {
		if secret != "" && strings.Contains(value, secret) {
			return true
		}
	}
	return false
}

// diagnosticSnapshot retains types and chain shape without retaining a raw
// transport error, whose exported URL/address fields can carry credentials.
func transportDiagnostic(err error, r Request) error {
	return diagnostics.Snapshot(err, func(text string) string { return diagnosticText(text, r) })
}

func responseDiagnostic(reason string, status int, headers http.Header, r Request) error {
	if extra := providerDiagnostic(nil, headers, r); extra != nil {
		return fmt.Errorf("%s (HTTP %d): %s", reason, status, extra)
	}
	return fmt.Errorf("%s (HTTP %d)", reason, status)
}

// Every failure retains fixed protocol context even when the provider returns
// an unknown schema or no usable diagnostic fields. These labels originate in
// this adapter, never in user input or untrusted provider messages.
func failureDiagnostic(code string, status int, m map[string]any, headers http.Header, r Request) error {
	provider := "unknown"
	if oneOf(r.Provider, "liquid", "openrouter", "openai", "anthropic") {
		provider = r.Provider
	}
	prefix := fmt.Sprintf("%s (provider=%s, HTTP %d)", code, provider, status)
	if extra := providerDiagnostic(m, headers, r); extra != nil {
		return errors.New(prefix + "\n" + extra.Error())
	}
	return errors.New(prefix)
}
