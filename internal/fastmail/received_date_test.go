package fastmail

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailcontent"
)

func TestReadMessagePreservesInternalDateWithoutExtraFetch(t *testing.T) {
	for _, date := range []string{` INTERNALDATE "01-Jan-2024 09:00:00 +0900"`, "", ` INTERNALDATE "private-invalid"`, ` INTERNALDATE "01-Jan-2024 09:00:00 +0900" INTERNALDATE "01-Jan-2024 09:00:00 +0900"`} {
		t.Run(date, func(t *testing.T) {
			c, done, commands := contentClient(t, 77, plainStructure+date, "hello", "TEXT")
			got, err := c.ReadMessage(context.Background(), "synthetic", []byte("secret"), contentSource())
			waitClosed(t, done)
			if strings.Contains(date, "private") || strings.Count(date, "INTERNALDATE") > 1 {
				if err == nil || got.Text != nil || got.ReceivedAt != nil || strings.Contains(err.Error(), "private") {
					t.Fatalf("invalid date accepted/leaked: %+v %v", got, err)
				}
				return
			}
			if err != nil || string(got.Text) != "hello" {
				t.Fatalf("read: %+v %v", got, err)
			}
			if date == "" {
				if got.ReceivedAt != nil {
					t.Fatal("missing timestamp not nil")
				}
			} else if got.ReceivedAt == nil || got.ReceivedAt.Format(time.RFC3339) != "2024-01-01T00:00:00Z" || got.ReceivedAt.Location() != time.UTC {
				t.Fatalf("wrong timestamp: %v", got.ReceivedAt)
			}
			if len(*commands) != 3 || !strings.Contains((*commands)[1], "INTERNALDATE") {
				t.Fatalf("extra or missing metadata request: %v", *commands)
			}
		})
	}
}

func TestPooledReadMessagePreservesInternalDate(t *testing.T) {
	s, c := newPoolServer(t, true)
	s.internalDate = `01-Jan-2024 09:00:00 +0900`
	p := NewContentPool()
	t.Cleanup(func() { p.Close(); s.wg.Wait() })
	r, ok := p.Reader(poolScope(), c).(mailcontent.MessageReader)
	if !ok {
		t.Fatal("pooled reader drops timestamp capability")
	}
	for i := 0; i < 2; i++ {
		got, err := r.ReadMessage(context.Background(), "synthetic", []byte("secret"), contentSource())
		if err != nil || string(got.Text) != "hello" || got.ReceivedAt == nil || got.ReceivedAt.Format(time.RFC3339) != "2024-01-01T00:00:00Z" {
			t.Fatalf("pooled read: %+v %v", got, err)
		}
	}
	if s.count("BODYSTRUCTURE") != 2 || s.count("INTERNALDATE") != 2 {
		t.Fatal("missing dates or unexpected fetches")
	}
}

func TestReadMessageKeepsReceiptWhenBodyIsUnsupported(t *testing.T) {
	structure := strings.Replace(plainStructure, "NIL NIL NIL NIL)", `NIL ("ATTACHMENT" NIL) NIL NIL)`, 1)
	c, done, _ := contentClient(t, 77, structure+` INTERNALDATE "01-Jan-2024 09:00:00 +0900"`, "hello", "TEXT")
	got, err := c.ReadMessage(context.Background(), "synthetic", []byte("secret"), contentSource())
	waitClosed(t, done)
	if adapterCode(err) != "unsupported_content" || got.Text != nil || got.ReceivedAt == nil || got.ReceivedAt.Format(time.RFC3339) != "2024-01-01T00:00:00Z" {
		t.Fatalf("failed body lost receipt or exposed text: %+v %v", got, err)
	}
}

func TestReadMessageRejectsReceiptFromInvalidMetadataFetch(t *testing.T) {
	for _, mode := range []string{"wrong_uid", "failed_command"} {
		t.Run(mode, func(t *testing.T) {
			c, done := scriptedClient(t, func(server net.Conn) {
				fmt.Fprint(server, "* OK [CAPABILITY IMAP4rev1] synthetic\r\n")
				r := bufio.NewReader(server)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					fields := strings.Fields(line)
					tag := fields[0]
					switch fields[1] {
					case "CAPABILITY":
						fmt.Fprintf(server, "* CAPABILITY IMAP4rev1\r\n%s OK done\r\n", tag)
					case "LOGIN":
						fmt.Fprintf(server, "%s OK authenticated\r\n", tag)
					case "EXAMINE":
						fmt.Fprintf(server, "* 1 EXISTS\r\n* OK [UIDVALIDITY 77] validity\r\n%s OK selected\r\n", tag)
					case "UID":
						uid := 9
						if mode == "wrong_uid" {
							uid = 8
						}
						fmt.Fprintf(server, "* 1 FETCH (INTERNALDATE \"01-Jan-2024 09:00:00 +0900\" UID %d BODYSTRUCTURE %s)\r\n%s NO private-server-error\r\n", uid, plainStructure, tag)
						return
					default:
						t.Error("unexpected command", fields[1])
						return
					}
				}
			})
			got, err := c.ReadMessage(context.Background(), "synthetic", []byte("secret"), contentSource())
			waitClosed(t, done)
			if err == nil || got.Text != nil || got.ReceivedAt != nil || strings.Contains(err.Error(), "private") {
				t.Fatalf("invalid metadata exposed: %+v %v", got, err)
			}
		})
	}
}

func TestPooledReadMessageKeepsReceiptWhenBodyFails(t *testing.T) {
	s, c := newPoolServer(t, true)
	s.internalDate = `01-Jan-2024 09:00:00 +0900`
	s.body = "private-body"
	p := NewContentPool()
	t.Cleanup(func() { p.Close(); s.wg.Wait() })
	r := p.Reader(poolScope(), c).(mailcontent.MessageReader)
	got, err := r.ReadMessage(context.Background(), "synthetic", []byte("secret"), contentSource())
	if err == nil || got.Text != nil || got.ReceivedAt == nil || got.ReceivedAt.Format(time.RFC3339) != "2024-01-01T00:00:00Z" || strings.Contains(err.Error(), "private") {
		t.Fatalf("failed body lost receipt or exposed text: %+v %v", got, err)
	}
}
