package fastmail

import (
	"bufio"
	"context"
	"fmt"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"net"
	"strconv"
	"strings"
	"testing"
)

func TestAutomaticHistoricalBoundsRawCandidatesBeforeFilteringAndKeepsNextMatch(t *testing.T) {
	for _, after := range []uint32{0, 100} {
		fetched := 0
		c, done := scriptedClient(t, func(server net.Conn) {
			fmt.Fprint(server, "* OK [CAPABILITY IMAP4rev1] synthetic\r\n")
			reader := bufio.NewReader(server)
			for {
				line, e := reader.ReadString('\n')
				if e != nil {
					return
				}
				fields := strings.Fields(line)
				if len(fields) < 2 {
					return
				}
				tag := fields[0]
				switch fields[1] {
				case "CAPABILITY":
					fmt.Fprintf(server, "* CAPABILITY IMAP4rev1\r\n%s OK done\r\n", tag)
				case "LOGIN":
					fmt.Fprintf(server, "%s OK authenticated\r\n", tag)
				case "EXAMINE":
					fmt.Fprintf(server, "* 101 EXISTS\r\n* OK [UIDVALIDITY 77] validity\r\n* OK [UIDNEXT 102] next\r\n%s OK selected\r\n", tag)
				case "UID":
					switch fields[2] {
					case "SEARCH":
						uids := []string{}
						for uid := after + 1; uid <= 101; uid++ {
							uids = append(uids, strconv.Itoa(int(uid)))
						}
						fmt.Fprintf(server, "* SEARCH %s\r\n%s OK searched\r\n", strings.Join(uids, " "), tag)
					case "FETCH":
						if strings.Contains(line, "BODY") {
							t.Error("candidate check fetched body")
							return
						}
						if after == 0 && !strings.Contains(line, "UID FETCH 1:100 ") || after == 100 && !strings.Contains(line, "UID FETCH 101 ") {
							t.Error("raw candidates exceeded requested page", line)
							return
						}
						last := uint32(100)
						date := "31-Dec-2024 14:00:00 +0000"
						if after == 100 {
							last = 101
							date = "01-Jan-2025 00:00:00 +0900"
						}
						for uid := after + 1; uid <= last; uid++ {
							fetched++
							fmt.Fprintf(server, "* %d FETCH (UID %d INTERNALDATE %q)\r\n", uid, uid, date)
						}
						fmt.Fprintf(server, "%s OK fetched\r\n", tag)
					default:
						t.Error("unexpected UID command", line)
						return
					}
				default:
					t.Error("unexpected command", line)
					return
				}
			}
		})
		req := mailsync.PageRequest{Mode: "historical", Folder: "INBOX", Since: "2025-01-01", Before: "2025-01-02", UIDValidity: 77, AfterUID: after, UpperUID: 101, Initialized: true, Limit: 100}
		page, e := c.Discover(context.Background(), "synthetic", []byte("synthetic"), req)
		waitClosed(t, done)
		if e != nil || !mailsync.ValidPage(req, page) {
			t.Fatal(page, e)
		}
		if after == 0 {
			if fetched != 100 || len(page.UIDs) != 0 || page.AfterUID != 100 || page.Done {
				t.Fatal("filtered raw page lost work bound/cursor", fetched, page)
			}
		} else {
			if fetched != 1 || len(page.UIDs) != 1 || page.UIDs[0] != 101 || !page.Done {
				t.Fatal("next valid match lost after filtering", fetched, page)
			}
		}
	}
	req := mailsync.PageRequest{Mode: "historical", UIDValidity: 77, UpperUID: 101, Initialized: true, Limit: 101}
	if mailsync.ValidPage(req, mailsync.Page{UIDValidity: 77, AfterUID: 100, UpperUID: 101, Initialized: true}) {
		t.Fatal("over-budget private page request accepted")
	}
}
