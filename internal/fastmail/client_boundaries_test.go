package fastmail

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func scriptedClient(t *testing.T, serve func(net.Conn)) (*Client, <-chan struct{}) {
	t.Helper()
	c := NewClient()
	done := make(chan struct{})
	c.dial = func(ctx context.Context, _ string, _ *tls.Config) (net.Conn, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 30*time.Second {
			t.Error("connection check lacks total 30-second timeout")
		}
		client, server := net.Pipe()
		go func() {
			defer close(done)
			defer server.Close()
			serve(server)
		}()
		return client, nil
	}
	return c, done
}

// untilCommand serves only LOGIN, CAPABILITY and LIST, stopping at one command.
func untilCommand(t *testing.T, server net.Conn, target string) string {
	t.Helper()
	fmt.Fprint(server, "* OK [CAPABILITY IMAP4rev1] synthetic server\r\n")
	r := bufio.NewReader(server)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return ""
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			t.Error("invalid command")
			return ""
		}
		command := strings.ToUpper(fields[1])
		if command == target {
			return fields[0]
		}
		switch command {
		case "LOGIN":
			fmt.Fprintf(server, "%s OK [CAPABILITY IMAP4rev1] authenticated\r\n", fields[0])
		case "CAPABILITY":
			fmt.Fprintf(server, "* CAPABILITY IMAP4rev1\r\n%s OK done\r\n", fields[0])
		default:
			t.Error("unexpected mail operation", command)
			return ""
		}
	}
}

func TestCancellationClosesLoginAndList(t *testing.T) {
	for _, command := range []string{"LOGIN", "LIST"} {
		for _, timeout := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/timeout=%t", command, timeout), func(t *testing.T) {
				reached := make(chan struct{})
				c, closed := scriptedClient(t, func(server net.Conn) {
					if untilCommand(t, server, command) == "" {
						return
					}
					close(reached)
					io.Copy(io.Discard, server)
				})
				ctx, cancel := context.WithCancel(context.Background())
				if timeout {
					ctx, cancel = context.WithTimeout(context.Background(), 60*time.Millisecond)
				}
				defer cancel()
				result := make(chan error, 1)
				go func() {
					_, err := c.Check(ctx, "synthetic@example.test", []byte("synthetic-private-password"))
					result <- err
				}()
				select {
				case <-reached:
				case <-time.After(time.Second):
					t.Fatal("command not reached")
				}
				if !timeout {
					cancel()
				}
				select {
				case err := <-result:
					want := "cancelled"
					if timeout {
						want = "timeout"
					}
					if adapterCode(err) != want {
						t.Fatal(err, adapterCode(err), want)
					}
				case <-time.After(time.Second):
					t.Fatal("cancellation did not finish check")
				}
				waitClosed(t, closed)
			})
		}
	}
}

func TestOversizedFragmentedLiteralClosesBeforeContent(t *testing.T) {
	for _, declaration := range []string{"{1048577}\r\n", "{2147483648}\r\n", "{999999999999999999999999999999999999999999}\r\n"} {
		t.Run(declaration, func(t *testing.T) {
			c, done := scriptedClient(t, func(server net.Conn) {
				if untilCommand(t, server, "LIST") == "" {
					return
				}
				fmt.Fprint(server, "* LIST () \"/\" ")
				for _, b := range []byte(declaration) {
					if _, err := server.Write([]byte{b}); err != nil {
						return
					}
				}
				io.Copy(io.Discard, server) // The declared content never arrives.
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := c.Check(ctx, "synthetic@example.test", []byte("synthetic-private-password"))
			waitClosed(t, done)
			if adapterCode(err) != "response_too_large" {
				t.Fatal("literal declaration was not rejected immediately", err, adapterCode(err))
			}
		})
	}
}

func TestCheckFolderBoundariesAndCanonicalNames(t *testing.T) {
	for _, entry := range []struct {
		name, response, code string
		count                int
	}{
		{"empty", "", "", 0},
		{"nil-delimiter", "* LIST () NIL \"Archive\"\r\n", "", 1},
		{"dot-and-literal", "* LIST (\\nOsElEcT) \".\" {6}\r\nA.{10}\r\n* LIST () \".\" \"&ZeVnLIqe-\"\r\n", "", 2},
		{"inbox-case", "* LIST () NIL \"inbox\"\r\n", "", 1},
		{"inbox-duplicate", "* LIST () NIL \"inbox\"\r\n* LIST () NIL \"INBOX\"\r\n", "invalid_response", 0},
		{"invalid-utf7", "* LIST () NIL \"&broken\"\r\n", "invalid_response", 0},
		{"invalid-delimiter", "* LIST () \"ab\" \"Archive\"\r\n", "invalid_response", 0},
		{"control-delimiter", "* LIST () \"\x01\" \"Archive\"\r\n", "invalid_response", 0},
		{"empty-name", "* LIST () NIL \"\"\r\n", "invalid_response", 0},
		{"max-name", "* LIST () NIL \"" + strings.Repeat("x", 1024) + "\"\r\n", "", 1},
		{"utf8-byte-limit", "* LIST () NIL {1026}\r\n" + strings.Repeat("日", 342) + "\r\n", "invalid_response", 0},
	} {
		t.Run(entry.name, func(t *testing.T) {
			c, done := protocolClient(t, entry.response, false)
			folders, err := c.Check(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"))
			waitClosed(t, done)
			if adapterCode(err) != entry.code || len(folders) != entry.count {
				t.Fatal(folders, err, adapterCode(err), entry.code)
			}
			if entry.name == "dot-and-literal" && (folders[0].Selectable || folders[0].Delimiter != "." || folders[1].Name != "日本語") {
				t.Fatal(folders)
			}
			if entry.name == "inbox-case" && folders[0].Name != "INBOX" {
				t.Fatal(folders)
			}
		})
	}
}

func TestCheckRejectsUnsafeGreetings(t *testing.T) {
	for _, greeting := range []string{"* PREAUTH synthetic-private-password\r\n", "* BROKEN synthetic-private-password\r\n", "* BYE synthetic-private-password\r\n"} {
		c, done := scriptedClient(t, func(server net.Conn) {
			fmt.Fprint(server, greeting)
			r := bufio.NewReader(server)
			for {
				line, readErr := r.ReadString('\n')
				// Inspect partial reads too, without printing received secrets.
				if strings.Contains(line, "synthetic@example.test") || strings.Contains(line, "synthetic-private-password") {
					t.Error("unsafe greeting received login credentials")
				}
				if line != "" {
					fields := strings.Fields(line)
					if len(fields) != 2 || fields[1] != "CAPABILITY" || !strings.HasSuffix(line, "\r\n") {
						t.Error("unsafe greeting received a command other than CAPABILITY")
						return
					}
					tag := strings.TrimPrefix(fields[0], "T")
					if _, err := strconv.ParseUint(tag, 10, 64); err != nil || tag == fields[0] {
						t.Error("invalid CAPABILITY command tag")
						return
					}
					// The SDK may request capabilities before the adapter sees
					// PREAUTH. Reply and keep inspecting until the adapter closes,
					// so a subsequent LOGIN or LIST regression cannot be hidden.
					fmt.Fprintf(server, "* CAPABILITY IMAP4rev1\r\n%s OK done\r\n", fields[0])
				}
				if readErr != nil {
					return
				}
			}
		})
		before := os.Stderr
		r, w, pipeErr := os.Pipe()
		if pipeErr != nil {
			t.Fatal(pipeErr)
		}
		os.Stderr = w
		_, err := c.Check(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"))
		os.Stderr = before
		w.Close()
		captured, _ := io.ReadAll(r)
		r.Close()
		waitClosed(t, done)
		if adapterCode(err) != "invalid_response" || strings.Contains(err.Error(), "synthetic-private") || len(captured) != 0 {
			t.Fatal(err, adapterCode(err))
		}
	}
}

func TestCheckAcceptsExactlyOneThousandFolders(t *testing.T) {
	var response strings.Builder
	for i := range 1000 {
		fmt.Fprintf(&response, "* LIST () NIL \"folder-%d\"\r\n", i)
	}
	c, done := protocolClient(t, response.String(), false)
	folders, err := c.Check(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"))
	waitClosed(t, done)
	if err != nil || len(folders) != 1000 {
		t.Fatal(len(folders), err)
	}
}

func TestCheckEnforcesTotalWireBudget(t *testing.T) {
	response := strings.Repeat("* OK "+strings.Repeat("x", 1016)+"\r\n", 1027)
	c, done := protocolClient(t, response, false)
	_, err := c.Check(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"))
	waitClosed(t, done)
	if adapterCode(err) != "response_too_large" {
		t.Fatal(err, adapterCode(err))
	}
}

func TestCheckRejectsTruncatedListCompletion(t *testing.T) {
	for _, ending := range []string{"", "\r", "\rX"} {
		c, done := scriptedClient(t, func(server net.Conn) {
			tag := untilCommand(t, server, "LIST")
			if tag != "" {
				fmt.Fprintf(server, "* LIST () NIL \"INBOX\"\r\n%s OK listed%s", tag, ending)
			}
		})
		_, err := c.Check(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"))
		waitClosed(t, done)
		if adapterCode(err) != "invalid_response" {
			t.Fatal("accepted unterminated completion", err, adapterCode(err))
		}
	}
}

func TestCheckAcceptsFragmentedListCompletion(t *testing.T) {
	c, done := scriptedClient(t, func(server net.Conn) {
		tag := untilCommand(t, server, "LIST")
		if tag == "" {
			return
		}
		response := "* LIST () NIL \"INBOX\"\r\n" + tag + " OK listed\r\n"
		for _, b := range []byte(response) {
			if _, err := server.Write([]byte{b}); err != nil {
				t.Error("valid fragmented completion was closed early")
				return
			}
		}
		io.Copy(io.Discard, server)
	})
	folders, err := c.Check(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"))
	waitClosed(t, done)
	if err != nil || len(folders) != 1 {
		t.Fatal(folders, err)
	}
}

func TestDeadlineInterruptsBlockedLoginWrite(t *testing.T) {
	release := make(chan struct{})
	c, done := scriptedClient(t, func(server net.Conn) {
		fmt.Fprint(server, "* OK [CAPABILITY IMAP4rev1] synthetic server\r\n")
		<-release // No command bytes are read, so LOGIN's write must block.
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := c.Check(ctx, "synthetic@example.test", []byte("synthetic-private-password"))
	close(release)
	waitClosed(t, done)
	if adapterCode(err) != "timeout" {
		t.Fatal("blocked LOGIN write misclassified", err, adapterCode(err))
	}
}
