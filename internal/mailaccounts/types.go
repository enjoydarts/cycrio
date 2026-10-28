// Package mailaccounts manages owner-isolated mail connections and explicit probes.
package mailaccounts

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid mail account request")
	ErrConflict    = errors.New("mail account version conflict")
	ErrNotFound    = errors.New("mail account not found")
	ErrUnavailable = errors.New("mail account service unavailable")
)

type SaveInput struct {
	Provider        string `json:"provider,omitempty"`
	Host            string `json:"host,omitempty"`
	Port            int    `json:"port,omitempty"`
	ExpectedVersion int64  `json:"expected_version"`
	Label           string `json:"label"`
	Login           string `json:"login"`
	AppPassword     string `json:"app_password"`
}
type ProbeInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	RequestID       string `json:"request_id"`
}
type FolderInput struct {
	ExpectedVersion int64    `json:"expected_version"`
	FolderIDs       []string `json:"folder_ids"`
}
type Account struct {
	ID                string     `json:"id"`
	Provider          string     `json:"provider"`
	Host              string     `json:"host,omitempty"`
	Port              int        `json:"port,omitempty"`
	Label             string     `json:"label"`
	State             string     `json:"state"`
	Version           int64      `json:"version"`
	CredentialVersion int64      `json:"credential_version"`
	CheckedAt         *time.Time `json:"checked_at"`
	LastError         string     `json:"last_error"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	LatestProbe       *Probe     `json:"latest_probe"`
	Folders           []Folder   `json:"folders"`
}
type Folder struct {
	DisplayName string `json:"display_name,omitempty"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Delimiter   string `json:"delimiter"`
	Selectable  bool   `json:"selectable"`
	Available   bool   `json:"available"`
	Selected    bool   `json:"selected"`
}
type Probe struct {
	ID         string     `json:"id"`
	AccountID  string     `json:"account_id"`
	State      string     `json:"state"`
	ErrorCode  string     `json:"error_code"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// Lease is the complete queue payload. It contains no credential material.
type Lease struct {
	ProbeID string `json:"probe_id"`
	LeaseID string `json:"lease_id"`
}
type RemoteFolder struct {
	DisplayName     string
	Name, Delimiter string
	Selectable      bool
}
type Checker interface {
	Check(context.Context, string, []byte) ([]RemoteFolder, error)
}

// AdapterError deliberately excludes server responses and underlying errors.
type AdapterError struct{ Code string }

func (e AdapterError) Error() string { return "mail connection check failed" }

type CredentialVersion struct {
	Provider  string               `json:"-"`
	Host      string               `json:"-"`
	Port      int                  `json:"-"`
	ID        string               `json:"-"`
	OwnerID   string               `json:"-"`
	AccountID string               `json:"-"`
	Version   int64                `json:"-"`
	Envelope  secretstore.Envelope `json:"-"`
}
type Work struct {
	Credential CredentialVersion `json:"-"`
}
type Repository interface {
	List(context.Context, string) ([]Account, error)
	Save(context.Context, string, string, int64, string, CredentialVersion) (Account, error)
	Disconnect(context.Context, string, string, int64) (Account, error)
	RequestProbe(context.Context, string, string, ProbeInput) (Probe, error)
	Probe(context.Context, string, string, string) (Probe, error)
	SaveFolders(context.Context, string, string, FolderInput) (Account, error)
	Claim(context.Context) ([]Lease, error)
	Start(context.Context, Lease) (Work, error)
	Finish(context.Context, Lease, string, []RemoteFolder) error
}
