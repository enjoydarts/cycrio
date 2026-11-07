package diagnostics

import (
	"reflect"
	"strconv"
)

type panicDetail struct{ typeName, message string }

func (e *panicDetail) Error() string          { return e.message }
func (e *panicDetail) DiagnosticType() string { return e.typeName }

// Panic snapshots only error text and primitive values. Arbitrary objects keep
// their concrete type, without invoking String/Format or serializing any fields.
func Panic(value any, secrets ...string) error {
	scrub := func(text string) string { return ScrubKnown(text, secrets...) }
	if err, ok := value.(error); ok {
		return Snapshot(err, scrub)
	}
	typeName := "nil"
	if value != nil {
		typeName = reflect.TypeOf(value).String()
	}
	text := "panic value of type " + typeName
	if value != nil {
		v := reflect.ValueOf(value)
		switch v.Kind() {
		case reflect.String:
			text = v.String()
		case reflect.Bool:
			text = strconv.FormatBool(v.Bool())
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			text = strconv.FormatInt(v.Int(), 10)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			text = strconv.FormatUint(v.Uint(), 10)
		case reflect.Float32, reflect.Float64:
			text = strconv.FormatFloat(v.Float(), 'g', -1, 64)
		case reflect.Complex64, reflect.Complex128:
			text = strconv.FormatComplex(v.Complex(), 'g', -1, 128)
		}
	}
	return &panicDetail{typeName: typeName, message: scrub(text)}
}
