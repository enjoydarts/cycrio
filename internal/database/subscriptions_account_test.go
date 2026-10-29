//go:build integration

package database

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/subscriptions"
	"github.com/google/uuid"
)

func TestSubscriptionsAccountFilterOwnershipHistoryAndPagination(t *testing.T) {
	f := processingSetup(t)
	ctx := context.Background()
	repo := NewSubscriptionRepository(f.runtime.pool)
	result := subscriptionResult(t, f, 3)
	create := func(name string, index *int) subscriptions.Subscription {
		t.Helper()
		in := subscriptionInput()
		in.Service = name
		if index != nil {
			in.SourceResultID, in.SourceEventIndex = &result, index
		}
		s, err := repo.Create(ctx, f.owner, in)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	zero, one := 0, 1
	alpha := create("Alpha", &zero)
	create("Beta", &one)
	create("000manual", nil)
	if _, err := repo.AttachSource(ctx, f.owner, alpha.ID, subscriptions.SourceInput{ExpectedVersion: 1, SourceResultID: result, SourceEventIndex: 2, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	emptyAccount, foreignAccount := uuid.NewString(), uuid.NewString()
	for _, account := range []struct{ id, owner string }{{emptyAccount, f.owner}, {foreignAccount, f.other}} {
		if _, err := f.runtime.pool.Exec(ctx, `INSERT INTO mail_accounts(id,owner_id,label,state,version,credential_version) VALUES($1,$2,'Empty','disconnected',1,1)`, account.id, account.owner); err != nil {
			t.Fatal(err)
		}
	}
	second := f
	second.account, second.folder = uuid.NewString(), uuid.NewString()
	credential := mailaccounts.CredentialVersion{ID: uuid.NewString(), OwnerID: f.owner, AccountID: second.account, Version: 1}
	var err error
	credential.Envelope, err = f.keys.Seal([]byte(`{"login":"c3ludGhldGlj","password":"c2VjcmV0"}`), mailaccounts.CredentialAAD(credential))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewMailAccountRepository(f.runtime.pool).Save(ctx, f.owner, second.account, 0, "Second", credential); err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `UPDATE mail_accounts SET state='ready' WHERE id=$1`, second.account); err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.pool.Exec(ctx, `INSERT INTO mail_account_folders(id,account_id,name,delimiter,selectable,available,selected) VALUES($1,$2,'INBOX','/',true,true,true)`, second.folder, second.account); err != nil {
		t.Fatal(err)
	}
	secondResult := subscriptionResult(t, second, 2)
	if _, err = repo.AttachSource(ctx, f.owner, alpha.ID, subscriptions.SourceInput{ExpectedVersion: 2, SourceResultID: secondResult, SourceEventIndex: 0, Confirmed: true}); err != nil {
		t.Fatal(err)
	}
	result = secondResult
	create("Gamma", &one)
	// Disconnecting a connection must preserve its historical library and sources.
	if _, err := f.runtime.pool.Exec(ctx, `UPDATE mail_accounts SET state='disconnected' WHERE id=$1`, f.account); err != nil {
		t.Fatal(err)
	}
	filter := subscriptions.Filter{AccountID: strings.ToUpper(f.account), Sort: "service", Limit: 1, Offset: 1}
	page, err := repo.List(ctx, f.owner, filter)
	if err != nil || page.Total != 2 || len(page.Subscriptions) != 1 || page.Subscriptions[0].Service != "Beta" {
		t.Fatal("filter must run before count and pagination and deduplicate sources", page, err)
	}
	filter.Offset = 0
	page, err = repo.List(ctx, f.owner, filter)
	if err != nil || page.Total != 2 || len(page.Subscriptions) != 1 || page.Subscriptions[0].ID != alpha.ID || len(page.Subscriptions[0].Sources) != 3 {
		t.Fatal(page, err)
	}
	for _, source := range page.Subscriptions[0].Sources {
		wantAccount, wantLabel := f.account, "Synthetic"
		if source.ResultID == secondResult {
			wantAccount, wantLabel = second.account, "Second"
		}
		if source.AccountID == nil || *source.AccountID != wantAccount || source.AccountLabel == nil || *source.AccountLabel != wantLabel || source.Provider == nil || *source.Provider != "fastmail" || source.OriginalEvent.Service != "Original service" || source.Provenance != "owner_confirmed" {
			t.Fatal("source metadata and immutable evidence", source)
		}
	}
	got, err := repo.Get(ctx, f.owner, alpha.ID)
	if err != nil || got.Sources[0].AccountID == nil || *got.Sources[0].AccountID != f.account {
		t.Fatal("detail metadata", got, err)
	}
	filter.Offset = 99
	page, err = repo.List(ctx, f.owner, filter)
	if err != nil || page.Total != 2 || len(page.Subscriptions) != 0 {
		t.Fatal("empty page retains filtered total", page, err)
	}
	filter.AccountID, filter.Offset = "", 0
	page, err = repo.List(ctx, f.owner, filter)
	if err != nil || page.Total != 4 || page.Subscriptions[0].Service != "000manual" || len(page.Subscriptions[0].Sources) != 0 {
		t.Fatal("all includes unsourced manual entries", page, err)
	}
	filter.AccountID, filter.Offset = second.account, 1
	page, err = repo.List(ctx, f.owner, filter)
	if err != nil || page.Total != 2 || len(page.Subscriptions) != 1 || page.Subscriptions[0].Service != "Gamma" {
		t.Fatal("second connection has separate matching set", page, err)
	}
	filter.AccountID, filter.Offset, filter.Query = second.account, 0, "Alpha"
	page, err = repo.List(ctx, f.owner, filter)
	if err != nil || page.Total != 1 || len(page.Subscriptions) != 1 || page.Subscriptions[0].ID != alpha.ID {
		t.Fatal("query and account must both apply", page, err)
	}
	filter.Query = ""
	filter.AccountID = emptyAccount
	page, err = repo.List(ctx, f.owner, filter)
	if err != nil || page.Total != 0 || len(page.Subscriptions) != 0 {
		t.Fatal("owned empty connection", page, err)
	}
	for _, owner := range []string{f.owner, f.admin} {
		for _, account := range []string{foreignAccount, uuid.NewString()} {
			filter.AccountID = account
			if _, err = repo.List(ctx, owner, filter); !errors.Is(err, subscriptions.ErrNotFound) {
				t.Fatal("unowned account must not be visible, including to admin", owner, account, err)
			}
		}
	}
	filter.AccountID = "invalid"
	if _, err = repo.List(ctx, f.owner, filter); !errors.Is(err, subscriptions.ErrInvalid) {
		t.Fatal("invalid account", err)
	}
}
