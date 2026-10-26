// Package mailsync lists bounded mail UID references without retrieving messages.
package mailsync

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"time"
)

var (
	ErrInvalid     = mailaccounts.ErrInvalid
	ErrConflict    = mailaccounts.ErrConflict
	ErrNotFound    = mailaccounts.ErrNotFound
	ErrUnavailable = mailaccounts.ErrUnavailable
)

type Input struct {
	ExpectedVersion int64  `json:"expected_version"`
	RequestID       string `json:"request_id"`
	Since           string `json:"since"`
	Before          string `json:"before"`
	MaxMessages     int    `json:"max_messages"`
}
type Run struct {
	ID               string     `json:"id"`
	AccountID        string     `json:"account_id"`
	State            string     `json:"state"`
	Since            string     `json:"since"`
	Before           string     `json:"before"`
	ErrorCode        string     `json:"error_code"`
	MaxMessages      int        `json:"max_messages"`
	ExaminedCount    int        `json:"examined_count"`
	AddedCount       int        `json:"added_count"`
	FolderCount      int        `json:"folder_count"`
	CompletedFolders int        `json:"completed_folders"`
	CreatedAt        time.Time  `json:"created_at"`
	StartedAt        *time.Time `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at"`
}

// Lease is the entire durable queue payload; it contains no mailbox data.
type Lease struct {
	RunID   string `json:"run_id"`
	LeaseID string `json:"lease_id"`
}
type PageRequest struct {
	Folder      string `json:"-"`
	Since       string `json:"-"`
	Before      string `json:"-"`
	UIDValidity uint32 `json:"-"`
	AfterUID    uint32 `json:"-"`
	UpperUID    uint32 `json:"-"`
	Limit       int    `json:"-"`
}
type Page struct {
	UIDValidity uint32   `json:"-"`
	AfterUID    uint32   `json:"-"`
	UpperUID    uint32   `json:"-"`
	Done        bool     `json:"-"`
	UIDs        []uint32 `json:"-"`
}
type Work struct {
	Run        Run                            `json:"-"`
	OwnerID    string                         `json:"-"`
	FolderID   string                         `json:"-"`
	Credential mailaccounts.CredentialVersion `json:"-"`
	Page       PageRequest                    `json:"-"`
}
type MessageRef struct {
	ID          string               `json:"-"`
	Fingerprint string               `json:"-"`
	Envelope    secretstore.Envelope `json:"-"`
}
type Scanner interface {
	Discover(context.Context, string, []byte, PageRequest) (Page, error)
}
type Repository interface {
	Request(context.Context, string, string, Input) (Run, error)
	List(context.Context, string, string) ([]Run, error)
	Cancel(context.Context, string, string, string) (Run, error)
	Claim(context.Context) ([]Lease, error)
	Start(context.Context, Lease) (Work, error)
	Finish(context.Context, Lease, Page, []MessageRef, string) error
}
