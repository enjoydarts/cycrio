// Package fastmail performs read-only connection checks against Fastmail IMAP.
package fastmail

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
)

const checkTimeout = 30 * time.Second

type Client struct {
	dial      func(context.Context, string, *tls.Config) (net.Conn, error)
	host      string
	port      int
	lookup    func(context.Context, string) ([]netip.Addr, error)
	tlsConfig *tls.Config
}

var _ mailaccounts.Checker = (*Client)(nil)

func NewClient() *Client {
	return &Client{dial: dialTLS}
}

func dialTLS(ctx context.Context, address string, config *tls.Config) (net.Conn, error) {
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	conn := tls.Client(raw, config)
	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, &tlsHandshakeError{cause: err}
	}
	return conn, nil
}

type tlsHandshakeError struct{ cause error }

func (e *tlsHandshakeError) Error() string { return "mail TLS handshake failed" }
func (e *tlsHandshakeError) Unwrap() error { return e.cause }

func (c *Client) Check(ctx context.Context, login string, password []byte) ([]mailaccounts.RemoteFolder, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	conn, err := c.connect(ctx)
	if err != nil {
		code := "connection_failed"
		var handshakeErr *tlsHandshakeError
		var certErr *tls.CertificateVerificationError
		var tlsErr tls.RecordHeaderError
		if errors.As(err, &handshakeErr) || errors.As(err, &certErr) || errors.As(err, &tlsErr) {
			code = "tls_failed"
		}
		return nil, checkError(ctx, nil, err, code)
	}
	defer conn.Close()
	// Closing the socket interrupts greeting, command writes and response reads.
	// Join an already-running callback before returning from this check.
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		conn.Close()
		close(closed)
	})
	defer func() {
		if !stop() {
			<-closed
		}
	}()
	deadline, _ := ctx.Deadline()
	wire := &boundedConn{Conn: conn, deadline: deadline}
	client := imapclient.New(wire, &imapclient.Options{DebugWriter: nil})
	defer client.Close()
	if err := client.WaitGreeting(); err != nil {
		return nil, checkError(ctx, wire, err, "invalid_response")
	}
	if client.State() != imap.ConnStateNotAuthenticated {
		return nil, mailaccounts.AdapterError{Code: "invalid_response"}
	}
	if err := client.Login(login, string(password)).Wait(); err != nil {
		code := "invalid_response"
		var status *imap.Error
		if errors.As(err, &status) && status.Type == imap.StatusResponseTypeNo {
			code = "authentication_failed"
		}
		return nil, checkError(ctx, wire, err, code)
	}
	cmd := client.List("", "*", nil)
	defer cmd.Close()
	folders := make([]mailaccounts.RemoteFolder, 0)
	seen := make(map[string]bool)
	for data := cmd.Next(); data != nil; data = cmd.Next() {
		code := ""
		if len(folders) == 1000 {
			code = "folder_limit"
		} else if !validName(data.Mailbox) || seen[data.Mailbox] || (data.Delim != 0 && unicode.IsControl(data.Delim)) {
			code = "invalid_response"
		}
		if code != "" {
			// LIST's decoder uses a bounded channel. Close the transport before
			// the deferred command drain, then join the decoder via client.Close.
			conn.Close()
			return nil, mailaccounts.AdapterError{Code: code}
		}
		seen[data.Mailbox] = true
		folder := mailaccounts.RemoteFolder{Name: data.Mailbox, Selectable: true}
		if data.Delim != 0 {
			folder.Delimiter = string(data.Delim)
		}
		for _, attr := range data.Attrs {
			if strings.EqualFold(string(attr), string(imap.MailboxAttrNoSelect)) {
				folder.Selectable = false
			}
		}
		folders = append(folders, folder)
	}
	if err := cmd.Close(); err != nil {
		return nil, checkError(ctx, wire, err, "invalid_response")
	}
	if ctx.Err() != nil {
		return nil, checkError(ctx, wire, ctx.Err(), "invalid_response")
	}
	return folders, nil
}

func validName(name string) bool {
	if name == "" || len(name) > 1024 || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func checkError(ctx context.Context, wire *boundedConn, err error, fallback string) error {
	code := fallback
	var netErr net.Error
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		code = "timeout"
	case errors.Is(ctx.Err(), context.Canceled), errors.Is(err, context.Canceled):
		code = "cancelled"
	case wire != nil && wire.exceeded.Load():
		code = "response_too_large"
	case wire != nil && wire.timedOut.Load():
		// The SDK wraps some socket errors as text. Keep their timeout
		// classification even when the context timer has not fired yet.
		code = "timeout"
	case errors.As(err, &netErr) && netErr.Timeout():
		code = "timeout"
	case wire != nil && wire.disconnected.Load() && !wire.invalidFraming.Load() && fallback != "authentication_failed" && fallback != "tls_failed":
		// The SDK formats some decoder/socket errors with %v, losing their
		// identity. Use the transport evidence without inspecting protocol text.
		code = "connection_failed"
	}
	return mailaccounts.AdapterError{Code: code}
}
