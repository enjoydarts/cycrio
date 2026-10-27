package fastmail

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestAutomaticReadTextReportsUIDResetBeforeBodyAndPreservesManualMissingSource(t *testing.T) {
	for _, strict := range []bool{false, true} {
		for _, duringFetch := range []bool{false, true} {
			validity := uint32(78)
			body := "hello"
			if duringFetch {
				validity = 77
				body = fmt.Sprintf("RAW:* OK [UIDVALIDITY 78] generation\r\n* 1 FETCH (UID 9 BODY[TEXT] {5}\r\nhello)\r\n")
			}
			c, done, commands := contentClient(t, validity, plainStructure, body, "TEXT")
			source := contentSource()
			source.StrictGeneration = strict
			text, e := c.ReadText(context.Background(), "synthetic", []byte("synthetic"), source)
			waitClosed(t, done)
			want := "source_missing"
			if strict {
				want = "uid_validity_changed"
			}
			if text != nil || adapterCode(e) != want {
				t.Fatal(strict, duringFetch, adapterCode(e))
			}
			if !duringFetch {
				for _, command := range *commands {
					if strings.Contains(command, "FETCH") {
						t.Fatal("UID reset fetched a body", command)
					}
				}
			}
		}
	}
}
