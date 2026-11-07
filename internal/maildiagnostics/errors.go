// Package maildiagnostics retains private, sanitized adapter failure details.
package maildiagnostics

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"net/http"
	"regexp"
	"strings"
)

var html = regexp.MustCompile(`(?i)<\s*(?:!doctype\b|/?[a-z][a-z0-9]*\b[^>]*>)`)
var requestID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var endpointURL = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)

// Failure accepts error envelopes only; successful mail/token payloads must
// never be supplied as body. Snapshots retain no raw transport object.
func Failure(code, provider, stage string, status int, headers http.Header, body []byte, cause error, secrets ...string) error {
	public := mailaccounts.AdapterError{Code: code}
	switch provider {
	case "gmail", "google", "google_workspace", "gmail_workspace", "outlook", "microsoft":
	default:
		provider = "mail"
	}
	prefix := fmt.Sprintf("%s (provider=%s, stage=%s, HTTP %d)", code, provider, stage, status)
	scrub := func(value string) string {
		return diagnostics.ScrubKnown(endpointURL.ReplaceAllString(value, "[mail endpoint]"), secrets...)
	}
	lines := []string{prefix}
	scalar := func(label string, value any) {
		switch v := value.(type) {
		case string:
			if v != "" && !html.MatchString(v) {
				lines = append(lines, label+": "+scrub(v))
			}
		case json.Number:
			lines = append(lines, label+": "+scrub(v.String()))
		}
	}
	var root map[string]any
	var envelopeRequestID string
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if len(body) > 0 && decoder.Decode(&root) == nil {
		if e, ok := root["error"].(map[string]any); ok {
			for _, key := range []string{"code", "message", "status", "type"} {
				scalar("provider."+key, e[key])
			}
			if inner, ok := e["innerError"].(map[string]any); ok {
				scalar("provider.inner_code", inner["code"])
				envelopeRequestID, _ = inner["request-id"].(string)
			}
			if entries, ok := e["errors"].([]any); ok {
				for i, item := range entries {
					if i >= 4 {
						break
					}
					if entry, ok := item.(map[string]any); ok {
						for _, key := range []string{"reason", "domain", "message"} {
							scalar("provider."+key, entry[key])
						}
					}
				}
			}
		} else {
			scalar("provider.code", root["error"])
		}
		scalar("provider.description", root["error_description"])
	}
	for _, id := range []string{headers.Get("Request-Id"), headers.Get("X-Request-Id"), headers.Get("X-Goog-Request-Id"), envelopeRequestID} {
		if requestID.MatchString(id) && scrub(id) == id {
			lines = append(lines, "request_id: "+id)
			break
		}
	}
	detail := errors.New(strings.Join(lines, "\n"))
	if cause != nil {
		detail = fmt.Errorf("%s\n%w", detail, diagnostics.Snapshot(cause, scrub))
	}
	return diagnostics.Wrap(public, detail)
}
