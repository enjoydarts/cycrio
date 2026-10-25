package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/secretstore"
)

func TestInitializeKeyringCreatesOnceAndNeverPrintsKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets", "keyring.json")
	var out bytes.Buffer
	if err := initializeKeyring(context.Background(), path, func(context.Context) (bool, error) { return false, nil }, &out); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secretstore.Load(path); err != nil {
		t.Fatal("generated keyring is invalid")
	}
	if err := run(context.Background(), []string{"init-keyring", "-keyring-file", path}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("reinitialization changed encryption keys")
	}
	if strings.Contains(out.String(), "active_key_id") || strings.Contains(out.String(), "keys") || bytes.Contains(out.Bytes(), before) {
		t.Fatal("key material was printed")
	}
}

func TestMissingKeyringIsNotRegeneratedWhenCredentialsExist(t *testing.T) {
	for _, tc := range []struct {
		exists bool
		err    error
	}{{true, nil}, {false, errors.New("private database password")}} {
		path := filepath.Join(t.TempDir(), "keyring.json")
		var out bytes.Buffer
		err := initializeKeyring(context.Background(), path, func(context.Context) (bool, error) { return tc.exists, tc.err }, &out)
		if err == nil {
			t.Fatal("missing keyring was regenerated without a safe empty database")
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("unexpected key file")
		}
		if strings.Contains(err.Error(), "password") || out.Len() != 0 {
			t.Fatal("private error leaked")
		}
	}
}

func TestInvalidKeyringIsNeverReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keyring.json")
	if os.WriteFile(path, []byte("invalid-secret-file"), 0600) != nil {
		t.Fatal("fixture write failed")
	}
	err := initializeKeyring(context.Background(), path, func(context.Context) (bool, error) {
		t.Fatal("invalid file must fail before database access")
		return false, nil
	}, &bytes.Buffer{})
	data, _ := os.ReadFile(path)
	if err == nil || string(data) != "invalid-secret-file" || strings.Contains(err.Error(), "invalid-secret-file") {
		t.Fatal("invalid file was exposed or overwritten")
	}
}
