package fastmail

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailsync"
)

func discoveryClient(t *testing.T, validity, next uint32, result string) (*Client, <-chan struct{}, *[]string) {
	t.Helper()
	commands := []string{}
	c, done := scriptedClient(t, func(server net.Conn) {
		fmt.Fprint(server, "* OK [CAPABILITY IMAP4rev1] synthetic\r\n")
		r := bufio.NewReader(server)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				t.Error("incomplete command")
				return
			}
			tag, command := fields[0], fields[1]
			if command != "LOGIN" {
				commands = append(commands, line)
			}
			switch command {
			case "CAPABILITY":
				fmt.Fprintf(server, "* CAPABILITY IMAP4rev1\r\n%s OK done\r\n", tag)
			case "LOGIN":
				if result == "auth-refused" {
					fmt.Fprintf(server, "%s NO synthetic-private-password\r\n", tag)
					continue
				}
				fmt.Fprintf(server, "%s OK authenticated\r\n", tag)
			case "EXAMINE":
				fmt.Fprintf(server, "* 3 EXISTS\r\n* OK [UIDVALIDITY %d] validity\r\n* OK [UIDNEXT %d] next\r\n%s OK [READ-ONLY] selected\r\n", validity, next, tag)
			case "UID":
				if len(fields) < 3 || fields[2] != "SEARCH" {
					t.Error("discovery modified mailbox")
					return
				}
				if strings.HasPrefix(result, "ESEARCH:") {
					body := "ALL " + strings.TrimPrefix(result, "ESEARCH:")
					if result == "ESEARCH:EMPTY" {
						body = ""
					}
					if result == "ESEARCH:COUNT0" {
						body = "COUNT 0"
					}
					if result == "ESEARCH:COUNT1" {
						body = "COUNT 1"
					}
					if body != "" {
						body = " " + body
					}
					fmt.Fprintf(server, "* ESEARCH (TAG \"%s\") UID%s\r\n%s OK searched\r\n", tag, body, tag)
				} else {
					fmt.Fprintf(server, "* SEARCH %s\r\n%s OK searched\r\n", result, tag)
				}
			default:
				t.Error("discovery issued forbidden mail operation", command)
				return
			}
		}
	})
	return c, done, &commands
}

func TestDiscoveryAcceptsEmptyESearchWithoutAll(t *testing.T) {
	for _, response := range []string{"ESEARCH:EMPTY", "ESEARCH:COUNT0", "ESEARCH:COUNT1"} {
		c, done, _ := discoveryClient(t, 77, 10, response)
		p, err := c.Discover(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"), discoveryInput())
		waitClosed(t, done)
		if response == "ESEARCH:COUNT1" {
			if adapterCode(err) != "invalid_response" {
				t.Fatal("missing nonempty UID set accepted", err)
			}
			continue
		}
		if err != nil || len(p.UIDs) != 0 || p.AfterUID != 9 || !p.Done {
			t.Fatal("valid empty ESEARCH rejected", p, err)
		}
	}
}

func discoveryInput() mailsync.PageRequest {
	return mailsync.PageRequest{Folder: "日本語", Since: "2026-10-01", Before: "2026-11-01", Limit: 2}
}

func TestDiscoveryUsesReadOnlyBoundedUIDSearch(t *testing.T) {
	c, done, commands := discoveryClient(t, 77, 9000, "7 2 5")
	p, err := c.Discover(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"), discoveryInput())
	waitClosed(t, done)
	if err != nil || p.UIDValidity != 77 || p.UpperUID != 8999 || p.AfterUID != 5 || p.Done || !reflect.DeepEqual(p.UIDs, []uint32{2, 5}) {
		t.Fatal(p, err)
	}
	joined := strings.Join(*commands, "")
	for _, part := range []string{"EXAMINE ", "UID SEARCH ", "UID 1:4096", "SINCE \"1-Oct-2026\"", "BEFORE \"1-Nov-2026\""} {
		if !strings.Contains(joined, part) {
			t.Fatalf("missing read-only discovery criterion %s", part)
		}
	}
}

func TestDiscoverySnapshotsResetsAndAdvancesEmptyPages(t *testing.T) {
	for _, tc := range []struct {
		name           string
		validity, next uint32
		in             mailsync.PageRequest
		result         string
		after, upper   uint32
		done           bool
		query          string
	}{
		{"empty", 77, 1, discoveryInput(), "", 0, 0, true, ""},
		{"empty-window", 77, 9000, discoveryInput(), "", 4096, 8999, false, "UID 1:4096"},
		{"resume", 77, 9000, mailsync.PageRequest{Folder: "INBOX", Since: "2026-10-01", Before: "2026-11-01", UIDValidity: 77, AfterUID: 4096, UpperUID: 8000, Limit: 100}, "5000", 8000, 8000, true, "UID 4097:8000"},
		{"new-generation", 78, 21, mailsync.PageRequest{Folder: "INBOX", Since: "2026-10-01", Before: "2026-11-01", UIDValidity: 77, AfterUID: 4096, UpperUID: 8000, Limit: 100}, "2", 20, 20, true, "UID 1:20"},
		{"uint32-boundary", 77, 4294967295, mailsync.PageRequest{Folder: "INBOX", Since: "2026-10-01", Before: "2026-11-01", UIDValidity: 77, AfterUID: 4294967293, UpperUID: 4294967294, Limit: 100}, "4294967294", 4294967294, 4294967294, true, "UID 4294967294"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, closed, commands := discoveryClient(t, tc.validity, tc.next, tc.result)
			p, err := c.Discover(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"), tc.in)
			waitClosed(t, closed)
			if err != nil || p.UIDValidity != tc.validity || p.AfterUID != tc.after || p.UpperUID != tc.upper || p.Done != tc.done {
				t.Fatal(p, err)
			}
			joined := strings.Join(*commands, "")
			if tc.query == "" {
				if strings.Contains(joined, "UID SEARCH") {
					t.Fatal("empty mailbox searched")
				}
			} else if !strings.Contains(joined, tc.query) {
				t.Fatal("cursor search mismatch", joined)
			}
		})
	}
}

func TestDiscoveryRejectsInvalidMetadataAndOutOfRangeResults(t *testing.T) {
	for _, tc := range []struct {
		validity, next uint32
		result         string
	}{{0, 10, ""}, {77, 0, ""}, {77, 10, "10"}, {77, 10, "4294967295"}, {77, 10, "0"}} {
		c, done, _ := discoveryClient(t, tc.validity, tc.next, tc.result)
		_, err := c.Discover(context.Background(), "synthetic@example.test", []byte("synthetic-private-password"), discoveryInput())
		waitClosed(t, done)
		if adapterCode(err) != "invalid_response" {
			t.Fatal("unsafe search metadata accepted", err)
		}
	}
}
