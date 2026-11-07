package mailsync

import (
	"context"
	"errors"
	"fmt"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"strings"
	"testing"
)

func TestScannerDiagnosticPreservesOriginalDespiteSuccessfulFinish(t *testing.T) {
	r, k, l := fixture(t)
	original := errors.New("scanner TLS handshake: original detail")
	scan := scannerFunc(func(context.Context, string, []byte, PageRequest) (Page, error) { return Page{}, original })
	ctx, scope := diagnostics.WithScope(context.Background())
	err := NewService(r, k, scan).Execute(ctx, l)
	if err != nil || r.code != "connection_failed" {
		t.Fatalf("state/return changed: %v %s", err, r.code)
	}
	if !errors.Is(scope.Error(), original) {
		t.Fatalf("cause lost: %v", scope.Error())
	}
}

func TestDiscoveryPanicScrubsKnownCredentialsAndPreservesPanicFlow(t *testing.T) {
	r, k, l := fixture(t)
	scan := scannerFunc(func(context.Context, string, []byte, PageRequest) (Page, error) {
		panic("discovery protocol failed dummy-password")
	})
	var value any
	func() {
		defer func() { value = recover() }()
		_ = NewService(r, k, scan).Execute(context.Background(), l)
	}()
	text := fmt.Sprint(value)
	if err, ok := value.(error); ok {
		for n := 0; err != nil && n < 16; n++ {
			text += "\n" + err.Error()
			err = errors.Unwrap(err)
		}
	}
	if value == nil || strings.Contains(text, "dummy-password") || !strings.Contains(text, "discovery protocol failed") {
		t.Fatalf("panic unsafe/lost: %s", text)
	}
}

func TestDiscoveryDecryptionErrorIsUnexpectedDespiteCredentialOutcome(t *testing.T) {
	r, k, l := fixture(t)
	r.work.Credential.Envelope.Ciphertext[0] ^= 1
	ctx, scope := diagnostics.WithScope(context.Background())
	if err := NewService(r, k, nil).Execute(ctx, l); err != nil || r.code != "credentials_unavailable" {
		t.Fatalf("outcome changed: %v %s", err, r.code)
	}
	if !diagnostics.IsUnexpected(scope.Error()) {
		t.Fatal("decryption failure was treated as missing credentials")
	}
}
