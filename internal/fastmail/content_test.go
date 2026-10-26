package fastmail

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/mailcontent"
)

const plainStructure = `("TEXT" "PLAIN" ("CHARSET" "UTF-8") NIL NIL "8BIT" 5 1 NIL NIL NIL NIL)`

func contentSource() mailcontent.Source {
	return mailcontent.Source{Folder: "INBOX", UIDValidity: 77, UID: 9, MaxTextBytes: mailcontent.MaxTextBytes}
}

func contentClient(t *testing.T, validity uint32, structure, body, bodySection string) (*Client, <-chan struct{}, *[]string) {
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
			f := strings.Fields(line)
			tag := f[0]
			if f[1] != "LOGIN" && f[1] != "CAPABILITY" {
				commands = append(commands, line)
			}
			switch f[1] {
			case "CAPABILITY":
				fmt.Fprintf(server, "* CAPABILITY IMAP4rev1\r\n%s OK done\r\n", tag)
			case "LOGIN":
				fmt.Fprintf(server, "%s OK authenticated\r\n", tag)
			case "EXAMINE":
				fmt.Fprintf(server, "* 1 EXISTS\r\n* OK [UIDVALIDITY %d] validity\r\n* OK [UIDNEXT 10] next\r\n%s OK [READ-ONLY] selected\r\n", validity, tag)
			case "UID":
				if !strings.Contains(line, "UID FETCH 9 ") {
					t.Error("unsafe UID command", line)
					return
				}
				if strings.Contains(line, "BODYSTRUCTURE") {
					if structure != "" {
						fmt.Fprintf(server, "* 1 FETCH (UID 9 BODYSTRUCTURE %s)\r\n", structure)
					}
				} else if strings.HasPrefix(body, "STALL:") {
					fmt.Fprint(server, strings.TrimPrefix(body, "STALL:"))
					io.Copy(io.Discard, server)
					return
				} else if strings.HasPrefix(body, "RAW:") {
					fmt.Fprint(server, strings.TrimPrefix(body, "RAW:"))
				} else {
					fmt.Fprintf(server, "* 1 FETCH (UID 9 BODY[%s] {%d}\r\n%s)\r\n", bodySection, len(body), body)
				}
				fmt.Fprintf(server, "%s OK fetched\r\n", tag)
			default:
				t.Error("forbidden content command", line)
				return
			}
		}
	})
	return c, done, &commands
}

func TestReadTextFetchesStructureThenOnlySelectedPlainPart(t *testing.T) {
	structure := `(` + strings.Replace(plainStructure, `"PLAIN"`, `"HTML"`, 1) + ` ` + plainStructure + ` "ALTERNATIVE" NIL NIL NIL NIL)`
	c, done, commands := contentClient(t, 77, structure, "hello", "2")
	got, err := c.ReadText(context.Background(), "synthetic@example.test", []byte("private-password"), contentSource())
	waitClosed(t, done)
	if err != nil || string(got) != "hello" {
		t.Fatal("read failed", err)
	}
	if len(*commands) != 3 || !strings.Contains((*commands)[0], "EXAMINE") || !strings.Contains((*commands)[1], "BODYSTRUCTURE") || !strings.Contains((*commands)[2], "BODY.PEEK[2]") {
		t.Fatal("unsafe command order", *commands)
	}
}

func TestReadTextSingleRootAndHTMLFallback(t *testing.T) {
	for _, tc := range []struct{ structure, body, want string }{
		{plainStructure, "hello", "hello"},
		{strings.Replace(strings.Replace(plainStructure, `"PLAIN"`, `"HTML"`, 1), "5 1", "12 1", 1), "<p>hello</p>", "hello"},
	} {
		c, done, commands := contentClient(t, 77, tc.structure, tc.body, "TEXT")
		got, err := c.ReadText(context.Background(), "synthetic@example.test", []byte("private-password"), contentSource())
		waitClosed(t, done)
		if err != nil || string(got) != tc.want || !strings.Contains((*commands)[2], "BODY.PEEK[TEXT]") {
			t.Fatal("single-part read failed", err, *commands)
		}
	}
}

func TestReadTextRejectsMissingGenerationAndOversizedStructureBeforeBody(t *testing.T) {
	for _, tc := range []struct {
		validity        uint32
		structure, code string
	}{
		{78, plainStructure, "source_missing"},
		{77, "", "source_missing"},
		{77, strings.Replace(plainStructure, "5 1", "5242881 1", 1), "input_too_large"},
		{77, strings.Replace(plainStructure, "NIL NIL NIL NIL)", `NIL ("ATTACHMENT" NIL) NIL NIL)`, 1), "unsupported_content"},
		{77, strings.Replace(plainStructure, `("CHARSET" "UTF-8")`, `("CHARSET" "UTF-8" "NAME" "private.txt")`, 1), "unsupported_content"},
	} {
		c, done, commands := contentClient(t, tc.validity, tc.structure, "hello", "TEXT")
		got, err := c.ReadText(context.Background(), "synthetic@example.test", []byte("private-password"), contentSource())
		waitClosed(t, done)
		if adapterCode(err) != tc.code || got != nil || len(*commands) > 2 {
			t.Fatal("unsafe structure accepted", adapterCode(err), *commands)
		}
	}
}

func TestReadTextRejectsMismatchedDuplicatedAndOversizedBodyResponses(t *testing.T) {
	for _, tc := range []struct{ body, code string }{
		{"RAW:* 1 FETCH (UID 8 BODY[TEXT] {5}\r\nhello)\r\n", "invalid_response"},
		{"RAW:* 1 FETCH (UID 9 BODY[1] {5}\r\nhello)\r\n", "invalid_response"},
		{"RAW:* 1 FETCH (UID 9 BODY[TEXT] {5}\r\nhello)\r\n* 1 FETCH (UID 9 BODY[TEXT] {5}\r\nhello)\r\n", "invalid_response"},
		{"RAW:* 1 FETCH (UID 9 BODY[TEXT] {5242881}\r\n", "input_too_large"},
	} {
		c, done, _ := contentClient(t, 77, plainStructure, tc.body, "TEXT")
		got, err := c.ReadText(context.Background(), "synthetic@example.test", []byte("private-password"), contentSource())
		waitClosed(t, done)
		if adapterCode(err) != tc.code || got != nil || strings.Contains(err.Error(), "hello") {
			t.Fatalf("unsafe body response %q accepted: %s", tc.body, adapterCode(err))
		}
	}
}

func TestReadTextRejectsAmbiguousMixedBodyBeforeFetching(t *testing.T) {
	for _, subtype := range []string{"MIXED", "RELATED"} {
		structure := `(` + plainStructure + ` ` + plainStructure + ` "` + subtype + `" NIL NIL NIL NIL)`
		c, done, commands := contentClient(t, 77, structure, "hello", "1")
		got, err := c.ReadText(context.Background(), "synthetic@example.test", []byte("private-password"), contentSource())
		waitClosed(t, done)
		if got != nil || adapterCode(err) != "unsupported_content" || len(*commands) != 2 {
			t.Fatal("ambiguous multipart body silently lost", adapterCode(err), *commands)
		}
	}
}

func TestReadTextExcludesAttachmentsAndEmbeddedMessages(t *testing.T) {
	html := strings.Replace(strings.Replace(plainStructure, `"PLAIN"`, `"HTML"`, 1), "5 1", "12 1", 1)
	attachment := strings.Replace(plainStructure, "NIL NIL NIL NIL)", `NIL ("ATTACHMENT" NIL) NIL NIL)`, 1)
	embedded := `("MESSAGE" "RFC822" NIL NIL NIL "7BIT" 100 (NIL NIL NIL NIL NIL NIL NIL NIL NIL NIL) ` + plainStructure + ` 1 NIL NIL NIL NIL)`
	structure := `(` + attachment + ` ` + embedded + ` ` + html + ` "MIXED" NIL NIL NIL NIL)`
	c, done, commands := contentClient(t, 77, structure, "<p>hello</p>", "3")
	got, err := c.ReadText(context.Background(), "synthetic@example.test", []byte("private-password"), contentSource())
	waitClosed(t, done)
	if err != nil || string(got) != "hello" || !strings.Contains((*commands)[2], "BODY.PEEK[3]") {
		t.Fatal("attachment exclusion failed", adapterCode(err), *commands)
	}
}

func TestReadTextBoundsStructurePartsAndDepth(t *testing.T) {
	for _, tc := range []struct{ name, structure, section, code string }{
		{"64-parts", "(" + strings.Repeat(plainStructure+" ", 63) + `"ALTERNATIVE" NIL NIL NIL NIL)`, "1", ""},
		{"65-parts", "(" + strings.Repeat(plainStructure+" ", 64) + `"ALTERNATIVE" NIL NIL NIL NIL)`, "1", "input_too_large"},
		{"16-depth", nestedContentStructure(15), strings.TrimSuffix(strings.Repeat("1.", 15), "."), ""},
		{"17-depth", nestedContentStructure(16), "", "input_too_large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, done, _ := contentClient(t, 77, tc.structure, "hello", tc.section)
			got, err := c.ReadText(context.Background(), "synthetic@example.test", []byte("private-password"), contentSource())
			waitClosed(t, done)
			if adapterCode(err) != tc.code || (tc.code == "" && string(got) != "hello") || (tc.code != "" && got != nil) {
				t.Fatal("structure limit mismatch", adapterCode(err))
			}
		})
	}
}

func nestedContentStructure(levels int) string {
	structure := plainStructure
	for range levels {
		structure = "(" + structure + ` "MIXED" NIL NIL NIL NIL)`
	}
	return structure
}

func TestReadTextUsesFixedTLSAndCancellationClosesTransport(t *testing.T) {
	c := NewClient()
	closed := make(chan struct{})
	c.dial = func(ctx context.Context, address string, config *tls.Config) (net.Conn, error) {
		if address != "imap.fastmail.com:993" || config.ServerName != "imap.fastmail.com" || config.InsecureSkipVerify || config.MinVersion < tls.VersionTLS12 {
			t.Error("unsafe content transport")
		}
		client, server := net.Pipe()
		go func() { defer close(closed); defer server.Close(); io.Copy(io.Discard, server) }()
		return client, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(25*time.Millisecond, cancel)
	got, err := c.ReadText(ctx, "synthetic@example.test", []byte("private-password"), contentSource())
	waitClosed(t, closed)
	if got != nil || adapterCode(err) != "cancelled" {
		t.Fatal("cancelled read succeeded", err)
	}
}

func TestReadTextRejectsPREAUTHAndDoesNotExposeServerErrors(t *testing.T) {
	for _, response := range []string{"* PREAUTH [CAPABILITY IMAP4rev1] private-body\r\n", "* private-body\r\n"} {
		c, done := scriptedClient(t, func(server net.Conn) { fmt.Fprint(server, response); io.Copy(io.Discard, server) })
		previous := os.Stderr
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Stderr = w
		got, readErr := c.ReadText(context.Background(), "synthetic@example.test", []byte("private-password"), contentSource())
		os.Stderr = previous
		w.Close()
		captured, _ := io.ReadAll(r)
		r.Close()
		waitClosed(t, done)
		if got != nil || adapterCode(readErr) != "invalid_response" || strings.Contains(readErr.Error(), "private-") || len(captured) != 0 {
			t.Fatal("private response exposed", adapterCode(readErr))
		}
	}
}

func TestContentGuardBoundsNestingBeforeResponseCompletes(t *testing.T) {
	guard := &contentResponseGuard{uid: 9}
	guard.phase.Store(1)
	var err error
	for _, b := range []byte("* 1 FETCH (UID 9 BODYSTRUCTURE " + strings.Repeat("(", 21)) {
		err = guard.accept(b, false, false)
		if err != nil {
			break
		}
	}
	if err != errResponseTooLarge {
		t.Fatal("incomplete recursive response reached SDK without bound", err)
	}
}

func TestReadTextRejectsUnrequestedUIDBeforeLiteralPayload(t *testing.T) {
	for _, prefix := range []string{"UID 8 ", ""} {
		c, done, _ := contentClient(t, 77, plainStructure, "STALL:* 1 FETCH ("+prefix+"BODY[TEXT] {5}\r\n", "TEXT")
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		got, err := c.ReadText(ctx, "synthetic@example.test", []byte("private-password"), contentSource())
		cancel()
		waitClosed(t, done)
		if got != nil || adapterCode(err) != "invalid_response" {
			t.Fatal("unrequested literal reached SDK", adapterCode(err))
		}
	}
}

func TestReadTextRejectsUIDValidityChangesDuringFetch(t *testing.T) {
	for _, validity := range []uint32{77, 78} {
		body := fmt.Sprintf("RAW:* OK [UIDVALIDITY %d] generation\r\n* 1 FETCH (UID 9 BODY[TEXT] {5}\r\nhello)\r\n", validity)
		c, done, _ := contentClient(t, 77, plainStructure, body, "TEXT")
		got, err := c.ReadText(context.Background(), "synthetic@example.test", []byte("private-password"), contentSource())
		waitClosed(t, done)
		if validity == 77 {
			if err != nil || string(got) != "hello" {
				t.Fatal("same UID generation rejected", adapterCode(err))
			}
		} else if got != nil || adapterCode(err) != "source_missing" {
			t.Fatal("changed UID generation accepted", adapterCode(err))
		}
	}
}

func TestReadTextUsesInputLimitCodeDuringAuthentication(t *testing.T) {
	c, done := scriptedClient(t, func(server net.Conn) {
		if untilCommand(t, server, "LOGIN") == "" {
			return
		}
		io.WriteString(server, strings.Repeat("x", mailcontent.MaxWireBytes+1))
	})
	got, err := c.ReadText(context.Background(), "synthetic@example.test", []byte("private-password"), contentSource())
	waitClosed(t, done)
	if got != nil || adapterCode(err) != "input_too_large" {
		t.Fatal("wire budget has inconsistent public error", adapterCode(err))
	}
}
