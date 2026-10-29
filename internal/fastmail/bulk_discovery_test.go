package fastmail

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailsync"
)

type bulkReplyCounter struct {
	net.Conn
	total *int
}

func (c bulkReplyCounter) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	*c.total += n
	return n, err
}

// A legal dense SEARCH with 4096 ten-digit UIDs occupies about 45 KiB.
// The number of messages fetched per page must still stay at 100.
func TestYearHistoricalDiscoveryAcceptsLongSearchLinesAndCompletesAllPages(t *testing.T) {
	const base uint32 = 4_000_000_000
	const total uint32 = 10001
	for _, extended := range []bool{false, true} {
		t.Run(fmt.Sprintf("ESEARCH=%t", extended), func(t *testing.T) {
			after := base
			discovered := uint32(0)
			pages := 0
			bytesReceived := 0
			for after < base+total {
				first := after + 1
				last := min(after+100, base+total)
				windowEnd := min(after+4096, base+total)
				c, done := scriptedClient(t, func(server net.Conn) {
					server = bulkReplyCounter{Conn: server, total: &bytesReceived}
					fmt.Fprint(server, "* OK [CAPABILITY IMAP4rev1] synthetic\r\n")
					reader := bufio.NewReader(server)
					for {
						line, e := reader.ReadString('\n')
						if e != nil {
							return
						}
						f := strings.Fields(line)
						if len(f) < 2 {
							return
						}
						tag := f[0]
						switch f[1] {
						case "CAPABILITY":
							fmt.Fprintf(server, "* CAPABILITY IMAP4rev1\r\n%s OK done\r\n", tag)
						case "LOGIN":
							fmt.Fprintf(server, "%s OK authenticated\r\n", tag)
						case "EXAMINE":
							fmt.Fprintf(server, "* %d EXISTS\r\n* OK [UIDVALIDITY 77] validity\r\n* OK [UIDNEXT %d] next\r\n%s OK selected\r\n", total, base+total+1, tag)
						case "UID":
							switch f[2] {
							case "SEARCH":
								rangeText := strconv.FormatUint(uint64(first), 10)
								if first != windowEnd {
									rangeText += ":" + strconv.FormatUint(uint64(windowEnd), 10)
								}
								if !strings.Contains(" "+strings.Join(f[3:], " ")+" ", " UID "+rangeText+" ") {
									t.Error("wrong continuation range")
									return
								}
								uids := []string{}
								for uid := first; uid <= windowEnd; uid++ {
									uids = append(uids, strconv.FormatUint(uint64(uid), 10))
								}
								if extended {
									fmt.Fprintf(server, "* ESEARCH (TAG %q) UID ALL %s\r\n%s OK searched\r\n", tag, strings.Join(uids, ","), tag)
								} else {
									fmt.Fprintf(server, "* SEARCH %s\r\n%s OK searched\r\n", strings.Join(uids, " "), tag)
								}
							case "FETCH":
								expected := strconv.FormatUint(uint64(first), 10)
								if first != last {
									expected += ":" + strconv.FormatUint(uint64(last), 10)
								}
								if len(f) < 4 || f[3] != expected || strings.Contains(line, "BODY") || strings.Contains(line, "HEADER") {
									t.Error("metadata fetch exceeded its page or accessed content")
									return
								}
								for uid := first; uid <= last; uid++ {
									if _, e := fmt.Fprintf(server, "* %d FETCH (UID %d INTERNALDATE \"01-May-2025 00:00:00 +0900\")\r\n", uid-base, uid); e != nil {
										return
									}
								}
								fmt.Fprintf(server, "%s OK fetched\r\n", tag)
							default:
								t.Error("unexpected UID operation")
								return
							}
						default:
							t.Error("unexpected operation")
							return
						}
					}
				})
				req := mailsync.PageRequest{Mode: "historical", Initialized: true, Folder: "INBOX", Since: "2025-01-01", Before: "2026-01-01", UIDValidity: 77, AfterUID: after, UpperUID: base + total, Limit: 100}
				page, e := c.Discover(context.Background(), "synthetic", []byte("synthetic"), req)
				waitClosed(t, done)
				if e != nil || !mailsync.ValidPage(req, page) {
					t.Fatal("normal yearly discovery page rejected", pages, adapterCode(e))
				}
				if len(page.UIDs) != int(last-first+1) || page.AfterUID != last || page.Done != (last == base+total) {
					t.Fatal("page lost candidates or cursor", page)
				}
				for i, uid := range page.UIDs {
					if uid != first+uint32(i) {
						t.Fatal("missing or duplicate UID")
					}
				}
				discovered += uint32(len(page.UIDs))
				after = page.AfterUID
				pages++
			}
			if discovered != total || pages != 101 || bytesReceived <= maxResponseBytes {
				t.Fatal("yearly import did not complete", discovered, pages)
			}
		})
	}
}

func TestAutomaticDiscoveryStillClosesAnOverBudgetTransport(t *testing.T) {
	c, done := scriptedClient(t, func(server net.Conn) { io.WriteString(server, "* OK "+strings.Repeat("x", maxResponseBytes+1)) })
	_, e := c.Discover(context.Background(), "synthetic", []byte("synthetic"), mailsync.PageRequest{Mode: "bootstrap", Folder: "INBOX", Limit: 100})
	waitClosed(t, done)
	if adapterCode(e) != "response_too_large" {
		t.Fatal("unbounded discovery response accepted", adapterCode(e))
	}
}
