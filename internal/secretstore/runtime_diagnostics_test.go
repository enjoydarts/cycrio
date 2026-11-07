package secretstore

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRuntimeOpenRetainsAuthenticationFailureWithoutPrivateValues(t *testing.T) {
	block, err := aes.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		t.Fatal(err)
	}
	keyring := &Keyring{active: "private-key-id", keys: map[string]cipher.AEAD{"private-key-id": aead}}
	envelope, err := keyring.Seal([]byte("private-plaintext"), []byte("private-aad"))
	if err != nil {
		t.Fatal(err)
	}
	envelope.Ciphertext[len(envelope.Ciphertext)-1] ^= 1
	_, err = keyring.Open(envelope, []byte("private-aad"))
	cause := errors.Unwrap(err)
	if !errors.Is(err, ErrUnavailable) || cause == nil || !strings.Contains(cause.Error(), "authentication failed") {
		t.Fatalf("cipher cause lost: %T", err)
	}
	public, _ := json.Marshal(err)
	for _, value := range []string{err.Error(), fmt.Sprintf("%+v", err), string(public), cause.Error()} {
		for _, secret := range []string{"private-key-id", "private-plaintext", "private-aad"} {
			if strings.Contains(value, secret) {
				t.Fatal("private crypto value exposed")
			}
		}
	}
	if err.Error() != ErrUnavailable.Error() {
		t.Fatal("public error changed")
	}
}
