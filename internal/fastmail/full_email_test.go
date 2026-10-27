package fastmail

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailcontent"
)

func TestReadWholeEmailPreservesLargePlainAndHTML(t *testing.T) {
	for _, media := range []string{"PLAIN", "HTML"} {
		t.Run(media, func(t *testing.T) {
			want := strings.Repeat("日本語", 60000) + "末尾確認"
			body := want
			if media == "HTML" {
				body = "<p>" + want + "</p>"
			}
			structure := fmt.Sprintf(`("TEXT" "%s" ("CHARSET" "UTF-8") NIL NIL "8BIT" %d 1 NIL NIL NIL NIL)`, media, len(body))
			for _, full := range []bool{false, true} {
				c, done, commands := contentClient(t, 77, structure, body, "TEXT")
				source := contentSource()
				source.FullEmail = full
				source.MaxTextBytes = 8192
				got, err := c.ReadText(context.Background(), "synthetic", []byte("synthetic"), source)
				waitClosed(t, done)
				if full {
					if err != nil || string(got) != want {
						t.Fatal("whole body rejected or changed", len(got), err)
					}
				} else if err == nil {
					t.Fatal("legacy limit bypassed")
				}
				if len(*commands) != 3 || !strings.Contains((*commands)[2], "BODY.PEEK[TEXT]") {
					t.Fatal("unsafe fetch", *commands)
				}
			}
		})
	}
}

func TestReadWholeEmailPhysicalWireBudgetIncludesProtocolOverhead(t *testing.T) {
	for _, size := range []int{mailcontent.MaxWireBytes - 4096, mailcontent.MaxWireBytes} {
		body := strings.Repeat("x", size)
		structure := fmt.Sprintf(`("TEXT" "PLAIN" ("CHARSET" "UTF-8") NIL NIL "8BIT" %d 1 NIL NIL NIL NIL)`, len(body))
		c, done, _ := contentClient(t, 77, structure, body, "TEXT")
		source := contentSource()
		source.FullEmail = true
		got, err := c.ReadText(context.Background(), "synthetic", []byte("synthetic"), source)
		waitClosed(t, done)
		if size == mailcontent.MaxWireBytes {
			if adapterCode(err) != "input_too_large" || got != nil {
				t.Fatal("connection overhead excluded from physical budget", len(got), err)
			}
		} else if err != nil || string(got) != body {
			t.Fatal("body within wire budget lost", len(got), err)
		}
	}
}

func TestReadWholeEmailRetainsUIDAndAttachmentGuards(t *testing.T) {
	for _, tc := range []struct {
		name, structure, body string
		validity              uint32
		code                  string
	}{
		{"generation", plainStructure, "hello", 78, "source_missing"},
		{"unrequested_uid", plainStructure, "STALL:* 1 FETCH (UID 8 BODY[TEXT] {5}\r\n", 77, "invalid_response"},
		{"attachment", strings.Replace(plainStructure, "NIL NIL NIL NIL)", `NIL ("ATTACHMENT" ("FILENAME" "a.txt")) NIL NIL)`, 1), "hello", 77, "unsupported_content"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, done, _ := contentClient(t, tc.validity, tc.structure, tc.body, "TEXT")
			source := contentSource()
			source.FullEmail = true
			got, err := c.ReadText(context.Background(), "synthetic", []byte("synthetic"), source)
			waitClosed(t, done)
			if got != nil || adapterCode(err) != tc.code {
				t.Fatal("whole mode bypassed guard", adapterCode(err))
			}
		})
	}
}
