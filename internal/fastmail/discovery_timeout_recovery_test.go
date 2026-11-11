package fastmail

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

// Model an already-received rejection whose decoding completes at the first
// attempt's deadline. Its authoritative status must win over a local timeout.
type delayedRejectionConn struct{ net.Conn }

func (c delayedRejectionConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if bytes.Contains(p[:n], []byte(" NO ")) || bytes.Contains(p[:n], []byte(" BAD ")) {
		time.Sleep(15 * time.Second)
	}
	return n, err
}

func TestDiscoveryDoesNotRetryRejectionDecodedAtAttemptDeadline(t *testing.T) {
	for _, status := range []string{"NO", "BAD"} {
		t.Run(status, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, closed := scriptedClient(t, func(server net.Conn) {
					if tag := untilCommand(t, server, "LOGIN"); tag != "" {
						_, _ = fmt.Fprintf(server, "%s %s refused\r\n", tag, status)
					}
				})
				dial := c.dial
				attempts := 0
				c.dial = func(ctx context.Context, address string, config *tls.Config) (net.Conn, error) {
					attempts++
					if attempts > 1 {
						return nil, io.EOF
					}
					conn, err := dial(ctx, address, config)
					return delayedRejectionConn{conn}, err
				}
				_, err := c.Discover(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"), discoveryInput())
				waitClosed(t, closed)
				want := "invalid_response"
				if status == "NO" {
					want = "authentication_failed"
				}
				if attempts != 1 || adapterCode(err) != want {
					t.Fatalf("authoritative rejection retried: attempts=%d code=%s", attempts, adapterCode(err))
				}
			})
		})
	}
}

// Exercise the real decoder/socket deadlines: a peer accepts LOGIN but never
// responds. A closed peer alone cannot reproduce this production failure.
func TestDiscoveryReconnectsUnresponsiveSessionWithinTotalBudget(t *testing.T) {
	for _, phase := range []string{"greeting", "LOGIN", "EXAMINE", "UID"} {
		t.Run(phase, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, recovered, _ := discoveryClient(t, 77, 10, "2 5")
				healthyDial := c.dial
				attempts := 0
				closed := make(chan struct{})
				c.dial = func(ctx context.Context, address string, config *tls.Config) (net.Conn, error) {
					attempts++
					if attempts > 1 {
						return healthyDial(ctx, address, config)
					}
					client, server := net.Pipe()
					go func() {
						defer close(closed)
						defer server.Close()
						if phase != "greeting" {
							partial := ""
							if phase == "UID" {
								partial = "* SEARCH 8\r\n"
							}
							interruptDiscoveryCommand(t, server, phase, partial)
						}
						_, _ = io.Copy(io.Discard, server)
					}()
					return client, nil
				}
				start := time.Now()
				page, err := c.Discover(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"), discoveryInput())
				waitClosed(t, closed)
				if err != nil {
					t.Fatalf("silent %s did not recover: attempts=%d code=%s", phase, attempts, adapterCode(err))
				}
				waitClosed(t, recovered)
				if attempts != 2 || !reflect.DeepEqual(page.UIDs, []uint32{2, 5}) || !page.Done || time.Since(start) >= 30*time.Second {
					t.Fatalf("incorrect recovery: attempts=%d page=%+v elapsed=%v", attempts, page, time.Since(start))
				}
			})
		})
	}
}

func TestDiscoveryPersistentSilenceStopsWithinTotalBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewClient()
		attempts := 0
		var closed []<-chan struct{}
		c.dial = func(ctx context.Context, _ string, _ *tls.Config) (net.Conn, error) {
			attempts++
			client, server := net.Pipe()
			done := make(chan struct{})
			closed = append(closed, done)
			go func() {
				defer close(done)
				defer server.Close()
				_, _ = io.Copy(io.Discard, server)
			}()
			return client, nil
		}
		start := time.Now()
		page, err := c.Discover(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"), discoveryInput())
		for _, done := range closed {
			waitClosed(t, done)
		}
		if attempts != 2 || adapterCode(err) != "timeout" || len(page.UIDs) != 0 || time.Since(start) > 30*time.Second {
			t.Fatalf("persistent silence escaped bounds: attempts=%d code=%s elapsed=%v", attempts, adapterCode(err), time.Since(start))
		}
	})
}

func TestDiscoveryDoesNotReconnectAfterParentDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewClient()
		attempts := 0
		var closed []<-chan struct{}
		c.dial = func(ctx context.Context, _ string, _ *tls.Config) (net.Conn, error) {
			attempts++
			client, server := net.Pipe()
			done := make(chan struct{})
			closed = append(closed, done)
			go func() {
				defer close(done)
				defer server.Close()
				_, _ = io.Copy(io.Discard, server)
			}()
			return client, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := c.Discover(ctx, "synthetic@example.test", []byte("synthetic-private-password"), discoveryInput())
		for _, done := range closed {
			waitClosed(t, done)
		}
		if attempts != 1 || adapterCode(err) != "timeout" {
			t.Fatalf("parent expiry retried: attempts=%d code=%s", attempts, adapterCode(err))
		}
	})
}
