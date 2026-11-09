package fastmail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"
)

// These checks exercise the SDK decoder too: it can wrap socket errors as text.
func TestCheckClassifiesServerDisconnect(t *testing.T) {
	for _, stage := range []string{"greeting", "LOGIN", "LIST", "literal"} {
		t.Run(stage, func(t *testing.T) {
			c, done := scriptedClient(t, func(server net.Conn) {
				if stage == "greeting" {
					return
				}
				command := stage
				if stage == "literal" {
					command = "LIST"
				}
				if untilCommand(t, server, command) == "" {
					return
				}
				if stage == "literal" {
					fmt.Fprint(server, "* LIST () NIL {10}\r\nshort")
				}
			})
			_, err := c.Check(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"))
			waitClosed(t, done)
			if adapterCode(err) != "connection_failed" || strings.Contains(err.Error(), "synthetic-private") {
				t.Fatalf("disconnect classified as %q, want connection_failed", adapterCode(err))
			}
		})
	}
}

type resetOnEOFConn struct{ net.Conn }

func (c resetOnEOFConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if errors.Is(err, io.EOF) {
		err = &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
	}
	return n, err
}

func TestDiscoveryClassifiesServerDisconnect(t *testing.T) {
	for _, command := range []string{"LOGIN", "EXAMINE", "UID"} {
		for _, reset := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reset=%t", command, reset), func(t *testing.T) {
				c := NewClient()
				attempts := 0
				var closed []<-chan struct{}
				c.dial = func(ctx context.Context, address string, config *tls.Config) (net.Conn, error) {
					attempts++
					if attempts > 2 {
						t.Fatal("persistent disconnect exceeded reconnect bound")
					}
					script, done := discoveryTCPClient(t, func(server net.Conn) {
						interruptDiscoveryCommand(t, server, command, "")
					})
					closed = append(closed, done)
					conn, err := script.dial(ctx, address, config)
					if reset {
						return resetOnEOFConn{conn}, err
					}
					return conn, err
				}
				page, err := c.Discover(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"), discoveryInput())
				for _, done := range closed {
					waitClosed(t, done)
				}
				if adapterCode(err) != "connection_failed" || attempts != 2 || len(page.UIDs) != 0 {
					t.Fatalf("persistent disconnect: code=%s attempts=%d results=%d", adapterCode(err), attempts, len(page.UIDs))
				}
			})
		}
	}
}
