package fastmail

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

// A Reader may return the final response bytes and EOF in the same call.
type greetingWithEOFConn struct {
	net.Conn
	response string
}

func (c *greetingWithEOFConn) Read(p []byte) (int, error) {
	n := copy(p, c.response)
	c.response = c.response[n:]
	return n, io.EOF
}

func TestDiscoveryDoesNotReconnectMalformedGreetingDeliveredWithEOF(t *testing.T) {
	for _, greeting := range []string{"* INVALID greeting\r\n", "* INVALID greeting"} {
		t.Run(greeting, func(t *testing.T) {
			c := NewClient()
			attempts := 0
			c.dial = func(context.Context, string, *tls.Config) (net.Conn, error) {
				attempts++
				client, server := net.Pipe()
				server.Close()
				return &greetingWithEOFConn{Conn: client, response: greeting}, nil
			}
			_, err := c.Discover(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"), discoveryInput())
			if attempts != 1 || adapterCode(err) != "invalid_response" {
				t.Fatalf("malformed greeting retried or misclassified: attempts=%d code=%s", attempts, adapterCode(err))
			}
		})
	}
}

func TestDiscoveryReconnectsAfterInterruptedGreeting(t *testing.T) {
	for _, scenario := range []struct {
		greeting string
		together bool
	}{
		{"", false},
		{"* OK interrupted", false},
		{"* OK interrupted", true},
	} {
		t.Run(fmt.Sprintf("greeting=%q/together=%t", scenario.greeting, scenario.together), func(t *testing.T) {
			c, done, _ := discoveryClient(t, 77, 10, "2 5")
			dial := c.dial
			attempts := 0
			interrupted := make(chan struct{})
			c.dial = func(ctx context.Context, address string, config *tls.Config) (net.Conn, error) {
				attempts++
				if attempts > 1 {
					return dial(ctx, address, config)
				}
				client, server := net.Pipe()
				if scenario.together {
					server.Close()
					close(interrupted)
					return &greetingWithEOFConn{Conn: client, response: scenario.greeting}, nil
				}
				go func() {
					defer close(interrupted)
					fmt.Fprint(server, scenario.greeting)
					server.Close()
				}()
				return client, nil
			}
			page, err := c.Discover(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"), discoveryInput())
			waitClosed(t, interrupted)
			if err != nil {
				t.Fatalf("discovery did not recover interrupted greeting: %v", err)
			}
			waitClosed(t, done)
			if attempts != 2 || !reflect.DeepEqual(page.UIDs, []uint32{2, 5}) || page.UIDValidity != 77 || !page.Done {
				t.Fatalf("unexpected recovery: attempts=%d page=%+v", attempts, page)
			}
		})
	}
}

func TestDiscoveryDoesNotRetryPersistentGreetingDisconnectForever(t *testing.T) {
	c := NewClient()
	attempts := 0
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	var closed []<-chan struct{}
	c.dial = func(ctx context.Context, _ string, _ *tls.Config) (net.Conn, error) {
		attempts++
		if attempts > 2 {
			t.Fatal("persistent disconnect exceeded reconnect bound")
		}
		currentDeadline, ok := ctx.Deadline()
		if !ok || currentDeadline.After(deadline) {
			t.Fatal("reconnect reset the operation deadline")
		}
		client, server := net.Pipe()
		done := make(chan struct{})
		closed = append(closed, done)
		go func() {
			defer close(done)
			server.Close()
		}()
		return client, nil
	}
	page, err := c.Discover(ctx, "synthetic@example.test", []byte("synthetic-private-password"), discoveryInput())
	for _, done := range closed {
		waitClosed(t, done)
	}
	if attempts != 2 || adapterCode(err) != "connection_failed" || len(page.UIDs) != 0 {
		t.Fatalf("unexpected persistent failure: attempts=%d page=%+v code=%s", attempts, page, adapterCode(err))
	}
}

func TestDiscoveryDoesNotReconnectRejectedOrCancelledSession(t *testing.T) {
	for _, scenario := range []struct {
		name, code string
	}{
		{"authentication", "authentication_failed"},
		{"protocol", "invalid_response"},
		{"framing", "invalid_response"},
		{"cancelled", "cancelled"},
		{"oversized", "response_too_large"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c, done := scriptedClient(t, func(server net.Conn) {
				switch scenario.name {
				case "authentication":
					if tag := untilCommand(t, server, "LOGIN"); tag != "" {
						fmt.Fprintf(server, "%s NO [AUTHENTICATIONFAILED] refused\r\n", tag)
					}
				case "protocol":
					fmt.Fprint(server, "* INVALID greeting\r\n")
				case "framing":
					fmt.Fprint(server, "* OK broken\r")
				case "cancelled":
					cancel()
				case "oversized":
					fmt.Fprint(server, "* OK "+strings.Repeat("x", maxResponseBytes)+"\r\n")
				}
			})
			dial := c.dial
			attempts := 0
			c.dial = func(ctx context.Context, address string, config *tls.Config) (net.Conn, error) {
				attempts++
				if attempts > 1 {
					t.Fatal("reconnected a rejected or cancelled session")
				}
				return dial(ctx, address, config)
			}
			_, err := c.Discover(ctx, "synthetic@example.test", []byte("synthetic-private-password"), discoveryInput())
			waitClosed(t, done)
			if attempts != 1 || adapterCode(err) != scenario.code {
				t.Fatalf("attempts=%d code=%q, want one attempt and %q", attempts, adapterCode(err), scenario.code)
			}
		})
	}
}
