package fastmail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
)

func diagnosticTextChain(err error) string {
	var messages []string
	for n := 0; err != nil && n < 16; n++ {
		messages = append(messages, fmt.Sprintf("%T: %s", err, err.Error()))
		if typed, ok := err.(interface{ DiagnosticType() string }); ok {
			messages = append(messages, typed.DiagnosticType())
		}
		err = errors.Unwrap(err)
	}
	return strings.Join(messages, "\n")
}

func TestCheckRetainsTransportCauseAndCodeWithoutCredentials(t *testing.T) {
	c := NewClient()
	c.dial = func(context.Context, string, *tls.Config) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused private-login private-password")}
	}
	_, err := c.Check(context.Background(), "private-login", []byte("private-password"))
	text := diagnosticTextChain(err)
	if adapterCode(err) != "connection_failed" || !strings.Contains(text, "*net.OpError") || !strings.Contains(text, "connection refused") {
		t.Fatalf("transport type/reason/code lost: %s", text)
	}
	if strings.Contains(text, "private-login") || strings.Contains(text, "private-password") || err.Error() != "mail connection check failed" {
		t.Fatalf("private/public boundary failed: %s", text)
	}
	if len(diagnostics.Stack(err)) == 0 {
		t.Fatal("origin stack missing")
	}
}

func TestCheckRetainsProtocolStatusWithoutServerText(t *testing.T) {
	c, done := protocolClient(t, "", true)
	_, err := c.Check(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"))
	waitClosed(t, done)
	text := diagnosticTextChain(err)
	if adapterCode(err) != "authentication_failed" || !strings.Contains(text, "imap.Error") || !strings.Contains(text, "AUTHENTICATIONFAILED") {
		t.Fatalf("protocol status lost: %s", text)
	}
	if strings.Contains(text, "synthetic-private-password") || strings.Contains(text, "synthetic@example.test") {
		t.Fatalf("protocol credentials leaked: %s", text)
	}
}

func TestParserDiagnosticKeepsTechnicalReasonWithoutPayload(t *testing.T) {
	err := checkError(context.Background(), nil, errors.New(`in response: expected CRLF but got "PRIVATE_MAIL_LITERAL"`), "invalid_response")
	text := diagnosticTextChain(err)
	if adapterCode(err) != "invalid_response" || !strings.Contains(text, "expected CRLF") || strings.Contains(text, "PRIVATE_MAIL_LITERAL") {
		t.Fatalf("parser detail unsafe/missing: %s", text)
	}
}

func TestProtocolDiagnosticKeepsServerExplanationWithoutPayload(t *testing.T) {
	err := checkError(context.Background(), nil, &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCode("SERVERBUG"), Text: "too many connections; body=PRIVATE_MAIL_LITERAL"}, "invalid_response")
	text := diagnosticTextChain(err)
	if !strings.Contains(text, "SERVERBUG") || !strings.Contains(text, "too many connections") || strings.Contains(text, "PRIVATE_MAIL_LITERAL") {
		t.Fatalf("server explanation unsafe/missing: %s", text)
	}
}
