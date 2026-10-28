package apimail

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"net/http"
	"strings"
	"testing"
)

func TestReadTextRejectsMessageMovedOutsideSelectedFolder(t *testing.T) {
	for _, provider := range []string{"gmail", "outlook"} {
		t.Run(provider, func(t *testing.T) {
			for _, member := range []bool{false, true} {
				c := fixture(t, provider, func(w http.ResponseWriter, r *http.Request) {
					folder := "other"
					if member {
						folder = "inbox"
					}
					if provider == "gmail" {
						fmt.Fprintf(w, `{"labelIds":[%q],"raw":%q}`, folder, base64.RawURLEncoding.EncodeToString([]byte("Content-Type: text/plain\r\n\r\nselected body")))
						return
					}
					if strings.HasSuffix(r.URL.Path, "/$value") {
						fmt.Fprint(w, "Content-Type: text/plain\r\n\r\nselected body")
						return
					}
					fmt.Fprintf(w, `{"id":"message","parentFolderId":%q}`, folder)
				})
				text, e := c.ReadText(context.Background(), "", nil, mailcontent.Source{RemoteID: "message", Folder: "inbox", MaxTextBytes: 100})
				if !member && (e == nil || len(text) > 0) {
					t.Fatal("out-of-scope body returned")
				}
				if member && (e != nil || string(text) != "selected body") {
					t.Fatal("in-scope body rejected", e)
				}
			}
		})
	}
}

func TestReadTextAcceptsAnyCurrentlySelectedDiscoveredMembership(t *testing.T) {
	for _, provider := range []string{"gmail", "outlook"} {
		t.Run(provider, func(t *testing.T) {
			c := fixture(t, provider, func(w http.ResponseWriter, r *http.Request) {
				if provider == "gmail" {
					fmt.Fprintf(w, `{"labelIds":["remaining"],"raw":%q}`, base64.RawURLEncoding.EncodeToString([]byte("Content-Type: text/plain\r\n\r\nselected body")))
					return
				}
				if strings.HasSuffix(r.URL.Path, "/$value") {
					fmt.Fprint(w, "Content-Type: text/plain\r\n\r\nselected body")
					return
				}
				fmt.Fprint(w, `{"parentFolderId":"remaining"}`)
			})
			body, e := c.ReadText(context.Background(), "", nil, mailcontent.Source{RemoteID: "message", Folder: "removed", AllowedFolders: []string{"removed", "remaining"}, MaxTextBytes: 100})
			if e != nil || string(body) != "selected body" {
				t.Fatal("another selected membership was ignored", e)
			}
		})
	}
}
