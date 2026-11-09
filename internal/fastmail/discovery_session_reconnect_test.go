package fastmail

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TCP distinguishes a peer disconnect from the local close used by response
// guards. net.Pipe can return the same io.ErrClosedPipe for both cases.
func discoveryTCPClient(t *testing.T, serve func(net.Conn)) (*Client, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		_ = listener.Close()
		if err != nil {
			t.Error("synthetic discovery accept failed", err)
			return
		}
		defer conn.Close()
		serve(conn)
	}()
	c := NewClient()
	c.dial = func(ctx context.Context, _ string, _ *tls.Config) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}
	return c, done
}

// Interrupt one read-only session at a command, optionally after partial data
// or an explicit rejection. No credentials or mailbox names are recorded.
func interruptDiscoveryCommand(t *testing.T, server net.Conn, target, response string) {
	t.Helper()
	fmt.Fprint(server, "* OK [CAPABILITY IMAP4rev1] synthetic\r\n")
	r := bufio.NewReader(server)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			t.Error("incomplete discovery command")
			return
		}
		tag, command := fields[0], fields[1]
		if command == target {
			if response == "BAD" || response == "NO" {
				fmt.Fprintf(server, "%s %s refused\r\n", tag, response)
			} else {
				fmt.Fprint(server, response)
			}
			return
		}
		switch command {
		case "LOGIN":
			fmt.Fprintf(server, "%s OK authenticated\r\n", tag)
		case "CAPABILITY":
			fmt.Fprintf(server, "* CAPABILITY IMAP4rev1\r\n%s OK done\r\n", tag)
		case "EXAMINE":
			fmt.Fprintf(server, "* 3 EXISTS\r\n* OK [UIDVALIDITY 77] validity\r\n* OK [UIDNEXT 10] next\r\n%s OK [READ-ONLY] selected\r\n", tag)
		default:
			t.Error("discovery issued unexpected operation", command)
			return
		}
	}
}

func TestDiscoveryReconnectsInterruptedReadOnlyCommands(t *testing.T) {
	for _, phase := range []string{"LOGIN", "EXAMINE", "UID"} {
		t.Run(phase, func(t *testing.T) {
			first, firstClosed := discoveryTCPClient(t, func(server net.Conn) {
				partial := ""
				if phase == "UID" {
					partial = "* SEARCH 8\r\n"
				}
				interruptDiscoveryCommand(t, server, phase, partial)
			})
			c, closed, _ := discoveryClient(t, 77, 10, "2 5")
			next := c.dial
			attempts := 0
			var deadline time.Time
			c.dial = func(ctx context.Context, address string, config *tls.Config) (net.Conn, error) {
				attempts++
				current, ok := ctx.Deadline()
				if !ok || !deadline.IsZero() && !current.Equal(deadline) {
					t.Fatal("reconnect changed operation deadline")
				}
				deadline = current
				if attempts == 1 {
					return first.dial(ctx, address, config)
				}
				if attempts != 2 {
					t.Fatal("discovery exceeded two attempts")
				}
				return next(ctx, address, config)
			}
			page, err := c.Discover(context.Background(), "synthetic@example.test", []byte("synthetic-password"), discoveryInput())
			waitClosed(t, firstClosed)
			if err != nil {
				t.Fatalf("read-only discovery did not recover: attempts=%d code=%s", attempts, adapterCode(err))
			}
			waitClosed(t, closed)
			if attempts != 2 || !reflect.DeepEqual(page.UIDs, []uint32{2, 5}) || page.UIDValidity != 77 || !page.Done {
				t.Fatalf("partial failed results leaked into recovery: attempts=%d page=%+v", attempts, page)
			}
		})
	}
}

func TestDiscoveryReconnectPreservesAutomaticSnapshotAndGeneration(t *testing.T) {
	for _, validity := range []uint32{77, 78} {
		t.Run(fmt.Sprint(validity), func(t *testing.T) {
			first, firstClosed := discoveryTCPClient(t, func(server net.Conn) {
				interruptDiscoveryCommand(t, server, "UID", "* SEARCH 8\r\n")
			})
			c, closed, _ := discoveryClient(t, validity, 20, "2 5")
			next, attempts := c.dial, 0
			c.dial = func(ctx context.Context, address string, config *tls.Config) (net.Conn, error) {
				attempts++
				if attempts == 1 {
					return first.dial(ctx, address, config)
				}
				if attempts != 2 {
					t.Fatal("generation failure was retried")
				}
				return next(ctx, address, config)
			}
			input := discoveryInput()
			input.Mode, input.Since, input.Before = "incremental", "", ""
			input.UIDValidity, input.AfterUID, input.UpperUID, input.Initialized = 77, 1, 8, true
			page, err := c.Discover(context.Background(), "synthetic@example.test", []byte("synthetic-password"), input)
			waitClosed(t, firstClosed)
			waitClosed(t, closed)
			if validity != 77 {
				if attempts != 2 || adapterCode(err) != "uid_validity_changed" || len(page.UIDs) != 0 {
					t.Fatalf("generation drift accepted: attempts=%d code=%s page=%+v", attempts, adapterCode(err), page)
				}
				return
			}
			if err != nil || attempts != 2 || page.UpperUID != 8 || page.AfterUID != 8 || !page.Done || !reflect.DeepEqual(page.UIDs, []uint32{2, 5}) {
				t.Fatalf("automatic snapshot widened or partial result reused: attempts=%d page=%+v err=%v", attempts, page, err)
			}
		})
	}
}

func TestDiscoveryCommandRejectionsAreNotRetried(t *testing.T) {
	for _, phase := range []string{"LOGIN", "EXAMINE", "UID"} {
		for _, response := range []string{"NO", "BAD"} {
			t.Run(phase+"/"+response, func(t *testing.T) {
				c, closed := discoveryTCPClient(t, func(server net.Conn) {
					interruptDiscoveryCommand(t, server, phase, response)
				})
				dial, attempts := c.dial, 0
				c.dial = func(ctx context.Context, address string, config *tls.Config) (net.Conn, error) {
					attempts++
					if attempts != 1 {
						t.Fatal("explicit rejection was retried")
					}
					return dial(ctx, address, config)
				}
				_, err := c.Discover(context.Background(), "synthetic@example.test", []byte("synthetic-password"), discoveryInput())
				waitClosed(t, closed)
				want := "invalid_response"
				if phase == "LOGIN" && response == "NO" {
					want = "authentication_failed"
				}
				if attempts != 1 || adapterCode(err) != want {
					t.Fatalf("rejection changed: attempts=%d code=%s want=%s", attempts, adapterCode(err), want)
				}
			})
		}
	}
}
