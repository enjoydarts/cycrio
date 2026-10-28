// Package mailcontent handles transient, bounded mail text for inference.
package mailcontent

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
)

const (
	MaxWireBytes = 5 << 20
	MaxTextBytes = 128 << 10
)

// Source is a private reference to an already discovered message.
type Source struct {
	AllowedFolders   []string `json:"-"`
	RemoteID         string   `json:"-"`
	StrictGeneration bool     `json:"-"`
	FullEmail        bool     `json:"-"`
	Folder           string   `json:"-"`
	UIDValidity      uint32   `json:"-"`
	UID              uint32   `json:"-"`
	MaxTextBytes     int      `json:"-"`
}

// Reader returns only transient normalized body text, never message headers.
type Reader interface {
	ReadText(context.Context, string, []byte, Source) ([]byte, error)
}
type ReaderFactory func(context.Context, mailaccounts.CredentialVersion) (Reader, error)
