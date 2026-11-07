package diagnostics

import (
	"errors"
	"reflect"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var knownWord = regexp.MustCompile(`[\p{L}\p{N}_]+`)

func ScrubKnown(text string, secrets ...string) string {
	// Recognize private-field payloads before echo removal can alter a label
	// that happens to occur in the submitted text.
	text = ScrubText(text)
	lowerSecrets := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
			lowerSecrets = append(lowerSecrets, strings.ToLower(secret))
		}
	}
	// Providers sometimes quote just a fragment of submitted text. Remove known
	// words, and partial identifiers of four or more characters. Short quoted
	// words require token boundaries. Only cache candidates from the bounded
	// diagnostic; tokenizing a multi-megabyte mail would allocate in proportion
	// to private source words that never appear in the diagnostic.
	matched := map[string]bool{}
	text = knownWord.ReplaceAllStringFunc(text, func(word string) string {
		if word == "redacted" {
			return word
		}
		lower := strings.ToLower(word)
		echo, checked := matched[lower]
		if !checked {
			partial := utf8.RuneCountInString(word) >= 4
			for _, secret := range lowerSecrets {
				if partial && strings.Contains(secret, lower) || !partial && containsKnownToken(secret, lower) {
					echo = true
					break
				}
			}
			matched[lower] = echo
		}
		if echo {
			return "[redacted]"
		}
		return word
	})
	text = ScrubText(text)
	// ScrubText intentionally removes everything after a private-field label,
	// including multiline bodies. Do not leave a new private-field assignment in
	// an already sanitized scalar: a later whole-cause scrub would erase sibling
	// technical fields. This changes only the diagnostic representation.
	return strings.ReplaceAll(text, "=[redacted]", " (private value removed)")
}

// containsKnownToken checks just the diagnostic's short candidate without
// allocating a word list for the private source. Boundaries match knownWord.
func containsKnownToken(source, word string) bool {
	wordRune := func(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) }
	for offset := 0; offset < len(source); {
		index := strings.Index(source[offset:], word)
		if index < 0 {
			return false
		}
		index += offset
		end := index + len(word)
		before, after := false, false
		if index > 0 {
			r, _ := utf8.DecodeLastRuneInString(source[:index])
			before = wordRune(r)
		}
		if end < len(source) {
			r, _ := utf8.DecodeRuneInString(source[end:])
			after = wordRune(r)
		}
		if !before && !after {
			return true
		}
		offset = end
	}
	return false
}

type snapshot struct {
	typeName, message string
	cause             error
}

func (e *snapshot) Error() string          { return e.message }
func (e *snapshot) Unwrap() error          { return e.cause }
func (e *snapshot) DiagnosticType() string { return e.typeName }

// Snapshot keeps concrete type names and safe messages without retaining the
// original error object, request, credentials or transport-specific fields.
func Snapshot(err error, scrub func(string) string) (result error) {
	defer func() {
		if recover() != nil {
			result = errors.New("diagnostic detail unavailable")
		}
	}()
	var copyCause func(error, int) error
	copyCause = func(current error, depth int) error {
		if current == nil || depth >= 16 {
			return nil
		}
		return &snapshot{typeName: reflect.TypeOf(current).String(), message: scrub(current.Error()), cause: copyCause(errors.Unwrap(current), depth+1)}
	}
	return copyCause(err, 0)
}
