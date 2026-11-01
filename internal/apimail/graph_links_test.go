package apimail

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailsync"
)

func TestGraphBootstrapResumesODataFolderLinksAndKeepsCheckpoint(t *testing.T) {
	calls := 0
	c := fixture(t, "outlook", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			fmt.Fprint(w, `{"value":[{"id":"old"}],"@odata.nextLink":"https://graph.microsoft.com/v1.0/me/mailfolders('Folder-AbC')/messages/delta?$skiptoken=next%2B%2F%3D"}`)
		} else if calls == 2 {
			if r.URL.Path != "/v1.0/me/mailfolders('Folder-AbC')/messages/delta" || r.URL.RawQuery != "$skiptoken=next%2B%2F%3D" {
				t.Error("continuation URL was rewritten")
			}
			fmt.Fprint(w, `{"value":[],"@odata.deltaLink":"https://graph.microsoft.com/v1.0/me/mailfolders('Folder-AbC')/messages/delta?$deltatoken=checkpoint"}`)
		} else {
			if r.URL.Query().Get("$deltatoken") != "checkpoint" {
				t.Error("delta checkpoint lost")
			}
			fmt.Fprint(w, `{"value":[{"id":"new"}],"@odata.deltaLink":"https://graph.microsoft.com/v1.0/me/mailfolders('Folder-AbC')/messages/delta?$deltatoken=latest"}`)
		}
	})
	r := mailsync.PageRequest{Mode: "bootstrap", Folder: "Folder-AbC", Limit: 100}
	p, e := c.Discover(context.Background(), "", nil, r)
	if e != nil || p.Done || len(p.RemoteIDs) != 0 {
		t.Fatalf("first bootstrap: done=%v error=%v", p.Done, e)
	}
	r.Cursor = p.Cursor
	p, e = c.Discover(context.Background(), "", nil, r)
	if e != nil || !p.Done || len(p.RemoteIDs) != 0 {
		t.Fatalf("final bootstrap: done=%v error=%v", p.Done, e)
	}
	r.Cursor = p.Cursor
	r.Mode = "incremental"
	p, e = c.Discover(context.Background(), "", nil, r)
	if e != nil || !p.Done || len(p.RemoteIDs) != 1 || p.RemoteIDs[0] != "new" || calls != 3 {
		t.Fatal("incremental checkpoint did not resume", e, calls)
	}
}

func TestGraphFolderPredicatePreservesEncodedIDsAndEscapedQuotes(t *testing.T) {
	for _, tc := range []struct {
		folder, link string
		want         bool
	}{
		{"Folder+AbC==", "https://graph.microsoft.com/v1.0/me/mailfolders('Folder%2BAbC%3D%3D')/messages/delta?$deltatoken=x", true},
		{"Folder+AbC==", "https://graph.microsoft.com/v1.0/me/mailFolders/Folder%2BAbC%3D%3D/messages/delta?$deltatoken=x", true},
		{"folder'one", "https://graph.microsoft.com/v1.0/me/mailfolders(%27folder%27%27one%27)/messages/delta?$deltatoken=x", true},
		{"folder'one", "https://graph.microsoft.com/v1.0/me/mailfolders(%27folder%27one%27)/messages/delta?$deltatoken=x", false},
		{"Folder+AbC==", "https://graph.microsoft.com/v1.0/me/mailfolders('Folder%2Babc%3D%3D')/messages/delta?$deltatoken=x", false},
	} {
		if got := graphMessageLink(tc.link, tc.folder, true); got != tc.want {
			t.Fatalf("encoded folder acceptance = %v, want %v", got, tc.want)
		}
	}
}

func TestGraphManualDiscoveryAcceptsSameFolderODataNextLink(t *testing.T) {
	calls := 0
	c := fixture(t, "outlook", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			fmt.Fprint(w, `{"value":[{"id":"first"}],"@odata.nextLink":"https://graph.microsoft.com/v1.0/me/mailfolders('inbox')/messages?$skiptoken=next"}`)
		} else {
			fmt.Fprint(w, `{"value":[{"id":"second"}]}`)
		}
	})
	r := mailsync.PageRequest{Mode: "historical", Folder: "inbox", Limit: 100, Since: "2026-01-01", Before: "2026-02-01"}
	p, e := c.Discover(context.Background(), "", nil, r)
	if e != nil || p.Done {
		t.Fatal("historical first page", e)
	}
	r.Cursor = p.Cursor
	p, e = c.Discover(context.Background(), "", nil, r)
	if e != nil || !p.Done || len(p.RemoteIDs) != 1 || p.RemoteIDs[0] != "second" {
		t.Fatal("historical continuation", e)
	}
}

func TestGraphODataLinksNeverChangeHostFolderOrCollection(t *testing.T) {
	for _, link := range []string{
		"https://graph.microsoft.com/v1.0/me/mailfolders('other')/messages/delta?$skiptoken=x",
		"https://graph.microsoft.com/v1.0/me/mailfolders('folder-abc')/messages/delta?$skiptoken=x",
		"https://graph.microsoft.com/v1.0/me/mailfolders('Folder-AbC')/messages?$skiptoken=x",
		"https://graph.microsoft.com/v1.0/users('other')/mailfolders('Folder-AbC')/messages/delta?$skiptoken=x",
		"https://graph.microsoft.com.evil.example/v1.0/me/mailfolders('Folder-AbC')/messages/delta?$skiptoken=x",
		"https://user@graph.microsoft.com/v1.0/me/mailfolders('Folder-AbC')/messages/delta?$skiptoken=x",
		"https://graph.microsoft.com/v1.0/me/mailfolders('Folder-AbC')/messages/delta?$skiptoken=x#fragment",
		"http://graph.microsoft.com/v1.0/me/mailfolders('Folder-AbC')/messages/delta?$skiptoken=x",
	} {
		calls := 0
		c := fixture(t, "outlook", func(w http.ResponseWriter, r *http.Request) {
			calls++
			fmt.Fprintf(w, `{"value":[],"@odata.nextLink":%q}`, link)
		})
		_, e := c.Discover(context.Background(), "", nil, mailsync.PageRequest{Mode: "bootstrap", Folder: "Folder-AbC", Limit: 100})
		if errorCode(e) != "invalid_response" || calls != 1 {
			t.Fatalf("unsafe continuation accepted, calls=%d code=%s", calls, errorCode(e))
		}
	}
}
