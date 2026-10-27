package fastmail

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"strings"
	"testing"
)

func TestAutomaticBootstrapOnlyExaminesAndPreservesEmptySnapshot(t *testing.T) {
	for _, next := range []uint32{1, 1101} {
		c, done, commands := discoveryClient(t, 77, next, "")
		page, e := c.Discover(context.Background(), "synthetic", []byte("secret"), mailsync.PageRequest{Mode: "bootstrap", Folder: "INBOX", Limit: 100})
		if e != nil {
			t.Fatal("bootstrap rejected", e)
		}
		<-done
		if e != nil || !page.Initialized || !page.Done || page.AfterUID != 0 || page.UpperUID != next-1 || len(page.UIDs) != 0 {
			t.Fatal(page, e)
		}
		for _, command := range *commands {
			if strings.Contains(command, "SEARCH") || strings.Contains(command, "FETCH") {
				t.Fatal("bootstrap read messages", command)
			}
		}
	}
}
func TestAutomaticUIDValidityChangeFailsBeforeSearch(t *testing.T) {
	c, done, commands := discoveryClient(t, 78, 1101, "1 2")
	_, e := c.Discover(context.Background(), "synthetic", []byte("secret"), mailsync.PageRequest{Mode: "incremental", Initialized: true, Folder: "INBOX", UIDValidity: 77, AfterUID: 1000, UpperUID: 1100, Limit: 100})
	var adapter mailaccounts.AdapterError
	if !errors.As(e, &adapter) || adapter.Code != "uid_validity_changed" {
		t.Fatal("reset must require explicit consent", e)
	}
	<-done
	for _, command := range *commands {
		if strings.Contains(command, "SEARCH") || strings.Contains(command, "FETCH") {
			t.Fatal("UID reset searched old messages", command)
		}
	}
}
func TestAutomaticIncrementalHasNoDateFilterAndDoesNotResnapshotEmptyUpper(t *testing.T) {
	c, done, commands := discoveryClient(t, 77, 7, "5 6")
	p, e := c.Discover(context.Background(), "synthetic", []byte("secret"), mailsync.PageRequest{Mode: "incremental", Folder: "INBOX", UIDValidity: 77, AfterUID: 4, UpperUID: 4, Limit: 100})
	if e != nil {
		t.Fatal("incremental rejected", e)
	}
	<-done
	if e != nil || p.AfterUID != 6 || p.UpperUID != 6 || len(p.UIDs) != 2 || !p.Initialized {
		t.Fatal(p, e)
	}
	for _, command := range *commands {
		if strings.Contains(command, "SINCE") || strings.Contains(command, "BEFORE") || strings.Contains(command, "FETCH") {
			t.Fatal("incremental lost old dated arrival", command)
		}
	}
	c, done, commands = discoveryClient(t, 77, 7, "")
	p, e = c.Discover(context.Background(), "synthetic", []byte("secret"), mailsync.PageRequest{Mode: "historical", Initialized: true, Folder: "INBOX", Since: "2025-01-01", Before: "2026-01-01", UIDValidity: 77, Limit: 100})
	if e != nil {
		t.Fatal("historical rejected", e)
	}
	<-done
	if e != nil || p.UpperUID != 0 || !p.Done {
		t.Fatal("empty frozen history expanded", p, e)
	}
	for _, command := range *commands {
		if strings.Contains(command, "SEARCH") || strings.Contains(command, "FETCH") {
			t.Fatal(command)
		}
	}
}
