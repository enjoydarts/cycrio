package apimail

import (
	"context"
	"fmt"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"net/http"
	"testing"
)

func TestProviderDateQueriesUseDisplayedJapaneseCalendarBoundaries(t *testing.T) {
	for _, provider := range []string{"gmail", "outlook"} {
		t.Run(provider, func(t *testing.T) {
			c := fixture(t, provider, func(w http.ResponseWriter, r *http.Request) {
				if provider == "gmail" {
					if r.URL.Query().Get("q") != "after:1793113199 before:1793199600" {
						t.Fatal("Gmail date query does not match JST range", r.URL.Query().Get("q"))
					}
					fmt.Fprint(w, `{"messages":[]}`)
				} else {
					if r.URL.Query().Get("$filter") != "receivedDateTime ge 2026-10-27T15:00:00Z and receivedDateTime lt 2026-10-28T15:00:00Z" {
						t.Fatal("Graph date query does not match JST range", r.URL.Query().Get("$filter"))
					}
					fmt.Fprint(w, `{"value":[]}`)
				}
			})
			if _, e := c.Discover(context.Background(), "", nil, mailsync.PageRequest{Provider: provider, Folder: "inbox", Since: "2026-10-28", Before: "2026-10-29", Limit: 10}); e != nil {
				t.Fatal(e)
			}
		})
	}
}
