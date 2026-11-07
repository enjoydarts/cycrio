package fastmail

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
)

type diagnosticSecretsKey struct{}

func withMailDiagnostics(ctx context.Context, login string, password []byte, folder string) context.Context {
	return context.WithValue(ctx, diagnosticSecretsKey{}, []string{login, string(password), folder})
}

var protocolQuotedValue = regexp.MustCompile(`(?s)"[^"\r\n]*"|'[^'\r\n]*'`)
var protocolGotValue = regexp.MustCompile(`(?is)\bgot\b.*`)
var protocolPanicValue = regexp.MustCompile(`(?is)(panic reading response:).*`)
var protocolContext = regexp.MustCompile(`\bin ([^:\s]+):`)

func imapDiagnostic(ctx context.Context, err error) error {
	secrets, _ := ctx.Value(diagnosticSecretsKey{}).([]string)
	var status *imap.Error
	var rawStatus, safeStatus string
	if errors.As(err, &status) && status != nil {
		rawStatus = status.Error()
		safeStatus = fmt.Sprintf("imap: %s", status.Type)
		switch string(status.Code) {
		case "AUTHENTICATIONFAILED", "AUTHORIZATIONFAILED", "UNAVAILABLE", "EXPIRED", "PRIVACYREQUIRED", "CONTACTADMIN", "NOPERM", "INUSE", "EXPUNGE", "ALERT", "BADCHARSET", "CAPABILITY", "PARSE", "READ-ONLY", "READ-WRITE", "TRYCREATE", "UIDNEXT", "UIDVALIDITY", "UNSEEN", "SERVERBUG", "CLIENTBUG", "CANNOT", "LIMIT", "OVERQUOTA", "ALREADYEXISTS", "NONEXISTENT":
			safeStatus += " [" + string(status.Code) + "]"
		}
		// Keep the server's technical explanation; the same protocol/credential
		// scrubber below removes any quoted values, payload fields or echoes.
		safeStatus += " - " + status.Text
	}
	return diagnostics.Snapshot(err, func(text string) string {
		if rawStatus != "" {
			text = strings.ReplaceAll(text, rawStatus, safeStatus)
		}
		text = protocolPanicValue.ReplaceAllString(text, "$1 [protocol payload omitted]")
		text = protocolGotValue.ReplaceAllString(text, "got [protocol value omitted]")
		text = protocolQuotedValue.ReplaceAllString(text, "[protocol value omitted]")
		text = protocolContext.ReplaceAllStringFunc(text, func(value string) string {
			part := protocolContext.FindStringSubmatch(value)[1]
			switch part {
			case "response", "continue-req", "resp-text-code", "resp-code-apnd", "resp-code-copy", "capability-data", "resp-text", "resp-cond-state", "FETCH", "LIST", "STATUS", "SEARCH", "ESEARCH", "ENABLED", "NAMESPACE":
				return "IMAP " + part + " parser -"
			}
			return "in [protocol token omitted]:"
		})
		return diagnostics.ScrubKnown(text, secrets...)
	})
}
