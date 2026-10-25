package fastmail

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
)

func protocolClient(t *testing.T, response string, rejectLogin bool) (*Client, <-chan struct{}) {
	t.Helper()
	c := NewClient()
	done := make(chan struct{})
	c.dial = func(_ context.Context, address string, config *tls.Config) (net.Conn, error) {
		if address != "imap.fastmail.com:993" || config.ServerName != "imap.fastmail.com" || config.InsecureSkipVerify || config.MinVersion < tls.VersionTLS12 {
			t.Error("unsafe Fastmail transport configuration")
		}
		client, server := net.Pipe()
		go func() {
			defer close(done)
			defer server.Close()
			fmt.Fprint(server, "* OK [CAPABILITY IMAP4rev1] synthetic server\r\n")
			r := bufio.NewReader(server)
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				fields := strings.Fields(line)
				if len(fields) < 2 {
					return
				}
				tag, command := fields[0], strings.ToUpper(fields[1])
				switch command {
				case "CAPABILITY":
					fmt.Fprintf(server, "* CAPABILITY IMAP4rev1\r\n%s OK done\r\n", tag)
				case "LOGIN":
					if rejectLogin {
						fmt.Fprintf(server, "%s NO [AUTHENTICATIONFAILED] synthetic-private-password\r\n", tag)
					} else {
						fmt.Fprintf(server, "%s OK authenticated\r\n", tag)
					}
				case "LIST":
					fmt.Fprint(server, response)
					fmt.Fprintf(server, "%s OK listed\r\n", tag)
				case "LOGOUT":
					fmt.Fprintf(server, "* BYE closing\r\n%s OK logout\r\n", tag)
					return
				default:
					t.Error("connection check issued a mail operation", command)
					return
				}
			}
		}()
		return client, nil
	}
	return c, done
}
func waitClosed(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("connection check left a server/goroutine open")
	}
}
func adapterCode(err error) string {
	var value mailaccounts.AdapterError
	if errors.As(err, &value) {
		return value.Code
	}
	var pointer *mailaccounts.AdapterError
	if errors.As(err, &pointer) && pointer != nil {
		return pointer.Code
	}
	return ""
}

func TestCheckUsesFixedTLSAndOnlyListsFolders(t *testing.T) {
	c, done := protocolClient(t, "* LIST (\\HasNoChildren) \"/\" \"INBOX\"\r\n* LIST (\\NoSelect) \"/\" \"Container\"\r\n* LIST () \"/\" \"&ZeVnLIqe-\"\r\n", false)
	folders, err := c.Check(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"))
	waitClosed(t, done)
	if err != nil || len(folders) != 3 {
		t.Fatal(folders, err)
	}
	byName := map[string]mailaccounts.RemoteFolder{}
	for _, folder := range folders {
		byName[folder.Name] = folder
	}
	if !byName["INBOX"].Selectable || byName["INBOX"].Delimiter != "/" || byName["Container"].Selectable || !byName["日本語"].Selectable {
		t.Fatal(folders)
	}
}
func TestCheckRefusalAndMalformedResponseNeverExposeProtocolText(t *testing.T) {
	for _, entry := range []struct {
		response string
		reject   bool
	}{
		{"", true}, {"* LIST () \"/\" \"same\"\r\n* LIST () \"/\" \"same\"\r\n", false}, {"* LIST () \"/\" \"synthetic-private-password\x00\"\r\n", false}, {"* SYNTHETIC-BAD-RESPONSE synthetic-private-password\r\n", false},
	} {
		c, done := protocolClient(t, entry.response, entry.reject)
		before := os.Stderr
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Stderr = w
		_, checkErr := c.Check(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"))
		os.Stderr = before
		w.Close()
		captured, _ := io.ReadAll(r)
		r.Close()
		waitClosed(t, done)
		if checkErr == nil || adapterCode(checkErr) == "" || strings.Contains(checkErr.Error(), "synthetic-private") || strings.Contains(string(captured), "synthetic-private") {
			t.Fatal("raw protocol response leaked or accepted", adapterCode(checkErr), string(captured))
		}
		if entry.reject && adapterCode(checkErr) != "authentication_failed" {
			t.Fatal("authentication refusal misclassified", checkErr)
		}
	}
}
func TestCheckBoundsFolderCountAndLiteralSize(t *testing.T) {
	var many strings.Builder
	for i := range 1001 {
		fmt.Fprintf(&many, "* LIST () \"/\" \"folder-%d\"\r\n", i)
	}
	for _, entry := range []struct{ response, code string }{
		{many.String(), "folder_limit"},
		{"* LIST () \"/\" {1048577}\r\n", "response_too_large"},
		{"* LIST () \"/\" \"" + strings.Repeat("x", 1025) + "\"\r\n", "invalid_response"},
	} {
		c, done := protocolClient(t, entry.response, false)
		_, err := c.Check(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"))
		waitClosed(t, done)
		if adapterCode(err) != entry.code {
			t.Fatal("response limits", entry.code, err)
		}
	}
}
func TestCheckCancellationClosesSocketDuringGreeting(t *testing.T) {
	c := NewClient()
	closed := make(chan struct{})
	c.dial = func(context.Context, string, *tls.Config) (net.Conn, error) {
		client, server := net.Pipe()
		go func() { defer close(closed); defer server.Close(); _, _ = io.Copy(io.Discard, server) }()
		return client, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Check(ctx, "synthetic@example.test", []byte("synthetic-private-password"))
	waitClosed(t, closed)
	if adapterCode(err) != "timeout" || time.Since(start) > 2*time.Second {
		t.Fatal("greeting cancellation", err, time.Since(start))
	}
}
func TestCheckCertificateFailuresUseFixedSafeError(t *testing.T) {
	c := NewClient()
	c.dial = func(context.Context, string, *tls.Config) (net.Conn, error) {
		return nil, &tls.CertificateVerificationError{Err: x509.HostnameError{Host: "synthetic-private-password"}}
	}
	_, err := c.Check(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"))
	if adapterCode(err) != "tls_failed" || strings.Contains(err.Error(), "synthetic-private") {
		t.Fatal("TLS error was exposed", err)
	}
}
