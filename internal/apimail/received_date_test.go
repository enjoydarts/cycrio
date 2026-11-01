package apimail

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailcontent"
)

func TestReadMessageUsesProviderReceivedDateAndIgnoresHeaderDate(t *testing.T) {
	for _, provider := range []string{"gmail", "outlook"} {
		for _, date := range []string{"valid", "missing", "invalid", "empty", "negative", "overflow"} {
			t.Run(provider+"/"+date, func(t *testing.T) {
				calls := 0
				raw := "Date: Mon, 1 Jan 2001 00:00:00 +0000\r\nContent-Type: text/plain\r\n\r\nhello"
				c := fixture(t, provider, func(w http.ResponseWriter, r *http.Request) {
					calls++
					if provider == "gmail" {
						field := `,"internalDate":"1704067200123"`
						switch date {
						case "missing":
							field = ""
						case "invalid":
							field = `,"internalDate":"private-invalid"`
						case "empty":
							field = `,"internalDate":""`
						case "negative":
							field = `,"internalDate":"-1"`
						case "overflow":
							field = `,"internalDate":"9223372036854775807"`
						}
						fmt.Fprintf(w, `{"labelIds":["inbox"],"raw":%q%s}`, base64.RawURLEncoding.EncodeToString([]byte(raw)), field)
					} else if strings.HasSuffix(r.URL.Path, "/$value") {
						fmt.Fprint(w, raw)
					} else {
						if r.URL.Query().Get("$select") != "parentFolderId,receivedDateTime" {
							t.Error("received timestamp must share membership request")
						}
						field := `,"receivedDateTime":"2024-01-01T09:00:00.123+09:00"`
						switch date {
						case "missing":
							field = ""
						case "invalid", "negative", "overflow":
							field = `,"receivedDateTime":"private-invalid"`
						case "empty":
							field = `,"receivedDateTime":""`
						}
						fmt.Fprintf(w, `{"parentFolderId":"inbox"%s}`, field)
					}
				})
				got, err := c.ReadMessage(context.Background(), "", nil, mailcontent.Source{RemoteID: "a", Folder: "inbox", MaxTextBytes: 100})
				if date != "valid" && date != "missing" {
					if errorCode(err) != "invalid_response" || strings.Contains(fmt.Sprint(err), "private") || got.Text != nil || got.ReceivedAt != nil {
						t.Fatalf("invalid timestamp leaked or accepted: %+v %v", got, err)
					}
					return
				}
				if err != nil || string(got.Text) != "hello" {
					t.Fatalf("read: %+v %v", got, err)
				}
				if date == "missing" {
					if got.ReceivedAt != nil {
						t.Fatal("header date used as fallback")
					}
				} else if got.ReceivedAt == nil || got.ReceivedAt.Format(time.RFC3339Nano) != "2024-01-01T00:00:00.123Z" || got.ReceivedAt.Location() != time.UTC {
					t.Fatalf("wrong timestamp: %v", got.ReceivedAt)
				}
				want := 1
				if provider == "outlook" {
					want = 2
				}
				if calls != want {
					t.Fatalf("extra fetches: %d", calls)
				}
			})
		}
	}
}

func TestReadMessageKeepsReceiptWhenBodyIsUnsupported(t *testing.T) {
	for _, provider := range []string{"gmail", "outlook"} {
		t.Run(provider, func(t *testing.T) {
			raw := "Content-Type: application/octet-stream\r\n\r\nprivate-body"
			c := fixture(t, provider, func(w http.ResponseWriter, r *http.Request) {
				if provider == "gmail" {
					fmt.Fprintf(w, `{"labelIds":["inbox"],"raw":%q,"internalDate":"1704067200123"}`, base64.RawURLEncoding.EncodeToString([]byte(raw)))
				} else if strings.HasSuffix(r.URL.Path, "/$value") {
					fmt.Fprint(w, raw)
				} else {
					fmt.Fprint(w, `{"parentFolderId":"inbox","receivedDateTime":"2024-01-01T00:00:00.123Z"}`)
				}
			})
			got, err := c.ReadMessage(context.Background(), "", nil, mailcontent.Source{RemoteID: "a", Folder: "inbox", MaxTextBytes: 100})
			if errorCode(err) != "unsupported_content" || got.Text != nil || got.ReceivedAt == nil || got.ReceivedAt.Format(time.RFC3339Nano) != "2024-01-01T00:00:00.123Z" || strings.Contains(err.Error(), "private") {
				t.Fatalf("failed body lost receipt or exposed text: %+v %v", got, err)
			}
		})
	}
}
