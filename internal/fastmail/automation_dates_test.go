package fastmail

import (
	"bufio"
	"context"
	"fmt"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"net"
	"reflect"
	"strings"
	"testing"
)

func TestAutomaticHistoricalFiltersExactJapanInstantsUsingOnlyMetadata(t *testing.T) {
	responses := []string{
		`* 1 FETCH (UID 1 INTERNALDATE "31-Dec-2024 14:59:59 +0000")`,
		`* 2 FETCH (UID 2 INTERNALDATE "31-Dec-2024 15:00:00 +0000")`,
		`* 3 FETCH (UID 3 INTERNALDATE "01-Jan-2025 14:59:59 +0000")`,
		`* 4 FETCH (UID 4 INTERNALDATE "01-Jan-2025 15:00:00 +0000")`,
		`* 5 FETCH (UID 5 INTERNALDATE "01-Jan-2025 00:00:00 +0900")`,
	}
	for _, tc := range []struct {
		name      string
		responses []string
		valid     bool
	}{
		{"JST boundaries", responses, true},
		{"unrequested UID", []string{`* 1 FETCH (UID 99 INTERNALDATE "01-Jan-2025 00:00:00 +0900")`}, false},
		{"duplicate UID", append(append([]string{}, responses...), responses[0]), false},
		{"duplicate date", []string{`* 1 FETCH (UID 1 INTERNALDATE "01-Jan-2025 00:00:00 +0900" INTERNALDATE "01-Jan-2025 00:00:00 +0900")`}, false},
		{"invalid date", []string{`* 1 FETCH (UID 1 INTERNALDATE "not a date")`}, false},
		{"missing UID", responses[:4], false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, done := scriptedClient(t, func(server net.Conn) {
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
						fmt.Fprintf(server, "* CAPABILITY IMAP4rev1\r\n%s OK capabilities\r\n", tag)
					case "LOGIN":
						fmt.Fprintf(server, "%s OK logged in\r\n", tag)
					case "EXAMINE":
						fmt.Fprintf(server, "* 5 EXISTS\r\n* OK [UIDVALIDITY 77] validity\r\n* OK [UIDNEXT 6] next\r\n%s OK [READ-ONLY] selected\r\n", tag)
					case "UID":
						switch f[2] {
						case "SEARCH":
							if !strings.Contains(line, `SINCE "31-Dec-2024"`) || !strings.Contains(line, `BEFORE "3-Jan-2025"`) {
								t.Error("historical search not broadened", line)
							}
							fmt.Fprintf(server, "* SEARCH 1 2 3 4 5\r\n%s OK searched\r\n", tag)
						case "FETCH":
							if strings.Contains(line, "BODY") || strings.Contains(line, "HEADER") || !strings.Contains(line, "INTERNALDATE") {
								t.Error("unexpected message access", line)
							}
							for _, response := range tc.responses {
								if _, e = fmt.Fprint(server, response+"\r\n"); e != nil {
									return
								}
							}
							fmt.Fprintf(server, "%s OK fetched\r\n", tag)
						default:
							t.Error("unexpected UID operation", line)
							return
						}
					default:
						t.Error("unexpected operation", line)
						return
					}
				}
			})
			p, e := c.Discover(context.Background(), "synthetic", []byte("secret"), mailsync.PageRequest{Mode: "historical", Initialized: true, Folder: "INBOX", Since: "2025-01-01", Before: "2025-01-02", UIDValidity: 77, UpperUID: 5, Limit: 100})
			<-done
			if tc.valid {
				if e != nil || !reflect.DeepEqual(p.UIDs, []uint32{2, 3, 5}) || p.AfterUID != 5 || !p.Done {
					t.Fatal(p, e)
				}
			} else if e == nil {
				t.Fatal("untrusted metadata accepted", p)
			}
		})
	}
}
