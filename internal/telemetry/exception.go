package telemetry

import (
	"reflect"
	"strconv"

	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"github.com/posthog/posthog-go"
)

func scrubText(text string) string { return diagnostics.ScrubText(text) }

func safeErrorText(value error) (text string) {
	text = "error detail unavailable"
	defer containPanic()
	return scrubText(value.Error())
}

func sanitizeExceptions(value any, fallback string) (items []posthog.ExceptionItem) {
	defer containPanic()
	if value == nil {
		return []posthog.ExceptionItem{{Type: fallback, Value: fallback}}
	}
	pending := []any{value}
	for len(pending) > 0 && len(items) < 16 {
		current := pending[0]
		pending = pending[1:]
		if current == nil {
			continue
		}
		typeName := reflect.TypeOf(current).String()
		if typed, ok := current.(interface{ DiagnosticType() string }); ok {
			typeName = scrubText(typed.DiagnosticType())
		}
		text := "panic value unavailable"
		switch v := current.(type) {
		case error:
			text = safeErrorText(v)
		case string:
			text = scrubText(v)
		case bool:
			if v {
				text = "true"
			} else {
				text = "false"
			}
		default:
			// Arbitrary structs may contain request bodies, tokens or user data.
			// Their concrete type remains useful without serializing fields.
			reflected := reflect.ValueOf(current)
			switch reflected.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				text = strconv.FormatInt(reflected.Int(), 10)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
				text = strconv.FormatUint(reflected.Uint(), 10)
			case reflect.Float32, reflect.Float64:
				text = strconv.FormatFloat(reflected.Float(), 'g', -1, 64)
			case reflect.Complex64, reflect.Complex128:
				text = strconv.FormatComplex(reflected.Complex(), 'g', -1, 128)
			default:
				text = "panic value of type " + typeName
			}
		}
		items = append(items, posthog.ExceptionItem{Type: typeName, Value: text})
		switch v := current.(type) {
		case interface{ Unwrap() []error }:
			for _, cause := range v.Unwrap() {
				if len(pending) >= 16 {
					break
				}
				pending = append(pending, cause)
			}
		case interface{ Unwrap() error }:
			pending = append(pending, v.Unwrap())
		}
	}
	return items
}
