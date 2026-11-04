package secretstore

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFingerprintsRetainOldKeysAndSeparatePurposes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	write := func(active string, keys map[string]string) *Keyring {
		t.Helper()
		data, _ := json.Marshal(keyFile{ActiveKeyID: active, Keys: keys})
		if e := os.WriteFile(path, data, 0600); e != nil {
			t.Fatal(e)
		}
		k, e := Load(path)
		if e != nil {
			t.Fatal(e)
		}
		return k
	}
	old := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	newer := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
	a := write("old", map[string]string{"old": old})
	first, e := a.Fingerprints("identity/v1", []byte("subject"))
	if e != nil {
		t.Fatal(e)
	}
	b := write("new", map[string]string{"old": old, "new": newer})
	all, e := b.Fingerprints("identity/v1", []byte("subject"))
	if e != nil || len(all) != 2 {
		t.Fatal(all, e)
	}
	found := false
	for _, f := range all {
		if f == first[0] {
			found = true
		}
		if len(f.Digest) != 64 {
			t.Fatal(f)
		}
	}
	if !found || !b.HasKey("old") || b.HasKey("missing") {
		t.Fatal("rotation lost old identity")
	}
	other, _ := b.Fingerprints("other/v1", []byte("subject"))
	for i := range all {
		if all[i].Digest == other[i].Digest {
			t.Fatal("purposes share key")
		}
	}
	if _, e = b.Fingerprints("", nil); e == nil {
		t.Fatal("empty purpose accepted")
	}
	var missing *Keyring
	if _, e = missing.Fingerprints("identity/v1", nil); e == nil {
		t.Fatal("missing keys accepted")
	}
}
