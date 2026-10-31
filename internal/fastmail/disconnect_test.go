package fastmail

import (
	"bufio"
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
	for _, command := range []string{"EXAMINE", "UID"} {
		for _, reset := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reset=%t", command, reset), func(t *testing.T) {
				c, done := scriptedClient(t, func(server net.Conn) {
					if tag := untilCommand(t, server, "EXAMINE"); tag != "" && command == "UID" {
						fmt.Fprintf(server, "* 1 EXISTS\r\n* OK [UIDVALIDITY 77] validity\r\n* OK [UIDNEXT 10] next\r\n%s OK [READ-ONLY] selected\r\n", tag)
						line, err := bufio.NewReader(server).ReadString('\n')
						if err != nil || !strings.Contains(line, " UID SEARCH ") {
							t.Error("UID SEARCH not reached")
						}
					}
				})
				if reset {
					dial := c.dial
					c.dial = func(ctx context.Context, address string, config *tls.Config) (net.Conn, error) {
						conn, err := dial(ctx, address, config)
						return resetOnEOFConn{conn}, err
					}
				}
				_, err := c.Discover(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"), discoveryInput())
				waitClosed(t, done)
				if adapterCode(err) != "connection_failed" {
					t.Fatalf("disconnect classified as %q, want connection_failed", adapterCode(err))
				}
			})
		}
	}
}
