// Package mailcontent handles transient, bounded mail text for inference.
package mailcontent

import (
	"context"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"time"
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

// Message includes only transient normalized text and provider receipt metadata.
// ReceivedAt is nil when the provider has no receipt timestamp.
type Message struct {
	Text       []byte `json:"-"`
	ReceivedAt *time.Time
}

// MessageReader preserves receipt time without exposing message headers.
// A body processing error may return valid ReceivedAt metadata with nil Text.
type MessageReader interface {
	ReadMessage(context.Context, string, []byte, Source) (Message, error)
}

type ReaderFactory func(context.Context, mailaccounts.CredentialVersion) (Reader, error)
