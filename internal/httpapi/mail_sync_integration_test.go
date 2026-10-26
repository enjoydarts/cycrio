//go:build integration

package httpapi

import (
	"context"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/google/uuid"
)

type httpSyncScanner struct{ calls int }

func (s *httpSyncScanner) Discover(_ context.Context, login string, password []byte, in mailsync.PageRequest) (mailsync.Page, error) {
	s.calls++
	if login != "synthetic-login@example.test" || string(password) != "synthetic-private-app-password" || in.Folder != "INBOX" {
		return mailsync.Page{}, mailsync.ErrInvalid
	}
	p := mailsync.Page{UIDValidity: 42, UpperUID: 4, AfterUID: in.AfterUID, UIDs: []uint32{}}
	for len(p.UIDs) < in.Limit && len(p.UIDs) < 2 && p.AfterUID < 4 {
		p.AfterUID++
		p.UIDs = append(p.UIDs, p.AfterUID)
	}
	p.Done = p.AfterUID == 4
	return p, nil
}

func verifyMailSyncHTTP(t *testing.T, ctx context.Context, service *mailsync.Service, scanner *httpSyncScanner, base string, request func(string, string, any, string, int, any)) {
	t.Helper()
	in := mailsync.Input{ExpectedVersion: 2, RequestID: uuid.NewString(), Since: "2026-10-01", Before: "2026-11-01", MaxMessages: 3}
	var run, again mailsync.Run
	request("POST", base+"/sync-runs", in, "mail_owner_a", 202, &run)
	request("POST", base+"/sync-runs", in, "mail_owner_a", 202, &again)
	if run.ID != again.ID || scanner.calls != 0 {
		t.Fatal("API duplicated or executed discovery")
	}
	request("GET", base+"/sync-runs", nil, "mail_owner_b", 404, nil)
	request("POST", base+"/sync-runs/"+run.ID+"/cancel", struct{}{}, "mail_owner_b", 404, nil)
	runPages := func() {
		for i := 0; i < 5; i++ {
			leases, err := service.Claim(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(leases) == 0 {
				return
			}
			for _, lease := range leases {
				if err := service.Execute(ctx, lease); err != nil {
					t.Fatal(err)
				}
			}
		}
		t.Fatal("discovery did not reach terminal state")
	}
	runPages()
	var listing struct {
		Runs []mailsync.Run `json:"runs"`
	}
	request("GET", base+"/sync-runs", nil, "mail_owner_a", 200, &listing)
	if len(listing.Runs) != 1 || listing.Runs[0].State != "limited" || listing.Runs[0].ExaminedCount != 3 || listing.Runs[0].AddedCount != 3 {
		t.Fatal("limit/progress mismatch", listing)
	}
	in.RequestID = uuid.NewString()
	request("POST", base+"/sync-runs", in, "mail_owner_a", 202, &run)
	runPages()
	request("GET", base+"/sync-runs", nil, "mail_owner_a", 200, &listing)
	if listing.Runs[0].ID != run.ID || listing.Runs[0].State != "completed" || listing.Runs[0].AddedCount != 1 || listing.Runs[0].ExaminedCount != 1 {
		t.Fatal("resume lost or duplicated references", listing)
	}
	in.RequestID = uuid.NewString()
	request("POST", base+"/sync-runs", in, "mail_owner_a", 202, &run)
	request("POST", base+"/sync-runs/"+run.ID+"/cancel", struct{}{}, "mail_owner_a", 200, &run)
	if run.State != "cancelled" {
		t.Fatal("sync not cancelled", run)
	}
}
