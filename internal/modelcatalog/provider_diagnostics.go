package modelcatalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/enjoydarts/cycrio/internal/diagnostics"
)

// This package is imported by ai, so its fetcher cannot depend on ai's adapter.
// Keep this boundary limited to documented error scalars and safe snapshots.
func catalogText(text string, key []byte) string {
	return diagnostics.ScrubKnown(text, string(key))
}
func catalogCause(err error, key []byte) error {
	return diagnostics.Snapshot(err, func(text string) string { return catalogText(text, key) })
}
func catalogDecodeFailure(raw []byte, key []byte) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return catalogCause(err, key)
	}
	return errors.New("catalog JSON has invalid, duplicate or unexpected fields")
}

var catalogRequestID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var catalogHTML = regexp.MustCompile(`(?i)<\s*(?:!doctype\b|/?[a-z][a-z0-9]*\b[^>]*>)`)

func catalogHTTPDiagnostic(raw []byte, status int, headers http.Header, key []byte) error {
	lines := []string{fmt.Sprintf("catalog provider HTTP status: %d", status)}
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&root) == nil {
		if wire, ok := root["error"].(map[string]any); ok {
			for _, field := range []string{"type", "code", "param", "message"} {
				switch value := wire[field].(type) {
				case string:
					if !catalogHTML.MatchString(value) {
						lines = append(lines, "provider."+field+": "+catalogText(value, key))
					}
				case json.Number:
					lines = append(lines, "provider."+field+": "+catalogText(value.String(), key))
				}
			}
		}
	}
	for _, field := range []string{"X-Request-Id", "Request-Id"} {
		id := headers.Get(field)
		if catalogRequestID.MatchString(id) && catalogText(id, key) == id {
			lines = append(lines, "request_id: "+id)
			break
		}
	}
	return errors.New(strings.Join(lines, "\n"))
}
