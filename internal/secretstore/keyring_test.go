package secretstore

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func TestKeyringRequiresExactOwnerPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keyring.json")
	if err := Initialize(path); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0000, 0100, 0200, 0300, 0500, 0700, 0640, 0644} {
		t.Run(strconv.FormatUint(uint64(mode), 8), func(t *testing.T) {
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("mode %o accepted: %v", mode, err)
			}
			if err := Initialize(path); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("initialization reused unsafe mode %o: %v", mode, err)
			}
		})
	}
	for _, mode := range []os.FileMode{0400, 0600} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err != nil {
			t.Fatalf("valid mode %o rejected: %v", mode, err)
		}
	}
}

func TestKeyringRejectsDuplicateAndCaseVariantMembers(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	for name, data := range map[string]string{
		"duplicate_active_id":      `{"active_key_id":"old","active_key_id":"old","keys":{"old":"` + key + `"}}`,
		"duplicate_keys":           `{"active_key_id":"old","keys":{"old":"` + key + `"},"keys":{"old":"` + key + `"}}`,
		"duplicate_key_id":         `{"active_key_id":"old","keys":{"old":"` + key + `","old":"` + key + `"}}`,
		"escaped_duplicate_key_id": `{"active_key_id":"old","keys":{"old":"` + key + `","\u006fld":"` + key + `"}}`,
		"uppercase_active_id":      `{"ACTIVE_KEY_ID":"old","keys":{"old":"` + key + `"}}`,
		"uppercase_keys":           `{"active_key_id":"old","KEYS":{"old":"` + key + `"}}`,
		"case_variant_collision":   `{"active_key_id":"old","Active_Key_Id":"old","keys":{"old":"` + key + `"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "keyring.json")
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("ambiguous member accepted: %v", err)
			}
			if err := Initialize(path); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("initialization reused ambiguous keyring: %v", err)
			}
		})
	}
}

func TestInitializeAndAuthenticatedEncryption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "keyring.json")
	if _, err := Load(path); !errors.Is(err, ErrMissing) {
		t.Fatalf("missing: %v", err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Initialize(path); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	before, _ := os.ReadFile(path)
	if err := Initialize(path); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("existing key overwritten")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("permissions")
	}
	dir, _ := os.Stat(filepath.Dir(path))
	if dir.Mode().Perm() != 0700 {
		t.Fatal("directory permissions")
	}
	k, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	aad := []byte("platform|provider-api-key/v1|openai|id|1")
	a, err := k.Seal([]byte("dummy-secret"), aad)
	if err != nil {
		t.Fatal(err)
	}
	b, err := k.Seal([]byte("dummy-secret"), aad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.Ciphertext, b.Ciphertext) {
		t.Fatal("nonce reused")
	}
	plain, err := k.Open(a, aad)
	if err != nil || string(plain) != "dummy-secret" {
		t.Fatal("roundtrip")
	}
	for _, bad := range []string{"tenant|provider-api-key/v1|openai|id|1", "platform|other|openai|id|1", "platform|provider-api-key/v1|resend|id|1", "platform|provider-api-key/v1|openai|other|1", "platform|provider-api-key/v1|openai|id|2"} {
		if _, err := k.Open(a, []byte(bad)); err == nil {
			t.Fatal("AAD mismatch accepted")
		}
	}
	a.Ciphertext[0] ^= 1
	if _, err := k.Open(a, aad); err == nil {
		t.Fatal("tamper accepted")
	}
	encoded, _ := json.Marshal(b)
	if string(encoded) != "{}" {
		t.Fatal("envelope must not serialize")
	}
}

func TestStrictKeyringLoadingAndRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keyring.json")
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	valid := `{"active_key_id":"old","keys":{"old":"` + key + `"}}`
	for _, bad := range []string{`{}`, valid + ` {}`, `{"active_key_id":"old","keys":{"old":"bad"}}`, `{"active_key_id":"missing","keys":{"old":"` + key + `"}}`, `{"active_key_id":"old","keys":{"old":"` + key + `"},"unknown":1}`, string(bytes.Repeat([]byte{' '}, 65537))} {
		os.WriteFile(path, []byte(bad), 0600)
		if _, err := Load(path); err == nil {
			t.Fatal("invalid keyring accepted")
		}
		if err := Initialize(path); err == nil {
			t.Fatal("invalid existing keyring replaced")
		}
	}
	os.WriteFile(path, []byte(valid), 0600)
	os.Chmod(path, 0640)
	if _, err := Load(path); err == nil {
		t.Fatal("unsafe permissions")
	}
	os.Chmod(path, 0400)
	old, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	env, _ := old.Seal([]byte("dummy-secret"), nil)
	os.Chmod(path, 0600)
	os.WriteFile(path, []byte(`{"active_key_id":"new","keys":{"old":"`+key+`","new":"`+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))+`"}}`), 0600)
	rotated, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotated.Open(env, nil); err != nil {
		t.Fatal("old key unreadable")
	}
	swapped := env
	swapped.KeyID = "new"
	if _, err := rotated.Open(swapped, nil); err == nil {
		t.Fatal("key ID swap accepted")
	}
	link := path + ".link"
	os.Symlink(path, link)
	if _, err := Load(link); err == nil {
		t.Fatal("symlink accepted")
	}
	if _, err := Load(filepath.Dir(path)); err == nil {
		t.Fatal("directory accepted")
	}
}
