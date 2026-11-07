package notificationruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var diagnosticHTML = regexp.MustCompile(`(?i)<\s*(?:!doctype\b|/?[a-z][a-z0-9]*\b[^>]*>)`)

// Resend's structured error fields are technical metadata. Recipient, request
// bodies, arbitrary fields, and response headers are deliberately not retained.
func resendHTTPDiagnostic(status int, raw []byte, scrub func(string) string) error {
	lines := []string{fmt.Sprintf("Resend HTTP %d", status)}
	var value map[string]json.RawMessage
	if len(raw) <= 4096 && json.Unmarshal(raw, &value) == nil {
		for _, field := range []string{"name", "type", "code", "message"} {
			var text string
			if json.Unmarshal(value[field], &text) == nil && text != "" && !diagnosticHTML.MatchString(text) {
				lines = append(lines, field+": "+scrub(text))
			}
		}
	}
	return errors.New(strings.Join(lines, "\n"))
}
