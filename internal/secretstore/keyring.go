// Package secretstore encrypts secrets with a local, operator-managed keyring.
package secretstore

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

var (
	ErrMissing     = errors.New("secret keyring missing")
	ErrUnavailable = errors.New("secret keyring unavailable")
)

type Envelope struct {
	KeyID      string `json:"-"`
	Ciphertext []byte `json:"-"`
}
type Keyring struct {
	active string
	keys   map[string]cipher.AEAD
}
type keyFile struct {
	ActiveKeyID string            `json:"active_key_id"`
	Keys        map[string]string `json:"keys"`
}

func Load(path string) (*Keyring, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrMissing
	}
	if err != nil || !validKeyFileMode(info.Mode()) {
		return nil, ErrUnavailable
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || !validKeyFileMode(opened.Mode()) {
		return nil, ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(data) > 65536 {
		return nil, ErrUnavailable
	}
	if !validObjectMembers(json.NewDecoder(bytes.NewReader(data)), true) {
		return nil, ErrUnavailable
	}
	var doc keyFile
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&doc) != nil || d.Decode(new(any)) != io.EOF || doc.ActiveKeyID == "" || len(doc.Keys) == 0 {
		return nil, ErrUnavailable
	}
	k := &Keyring{active: doc.ActiveKeyID, keys: make(map[string]cipher.AEAD, len(doc.Keys))}
	for id, encoded := range doc.Keys {
		key, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(key) != 32 || id == "" {
			return nil, ErrUnavailable
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, ErrUnavailable
		}
		aead, err := cipher.NewGCMWithRandomNonce(block)
		if err != nil {
			return nil, ErrUnavailable
		}
		k.keys[id] = aead
	}
	if k.keys[k.active] == nil {
		return nil, ErrUnavailable
	}
	return k, nil
}

func validKeyFileMode(mode os.FileMode) bool {
	return mode == 0600 || mode == 0400
}

// encoding/json accepts duplicate members and case-insensitive struct fields.
// Validate the two object levels before decoding their typed values below.
func validObjectMembers(d *json.Decoder, root bool) bool {
	opening, err := d.Token()
	if err != nil || opening != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] || root && name != "active_key_id" && name != "keys" {
			return false
		}
		seen[name] = true
		if root && name == "keys" {
			if !validObjectMembers(d, false) {
				return false
			}
		} else {
			var value json.RawMessage
			if d.Decode(&value) != nil {
				return false
			}
		}
	}
	closing, err := d.Token()
	return err == nil && closing == json.Delim('}')
}

// Initialize atomically creates a keyring and never replaces an existing file.
func Initialize(path string) error {
	if _, err := Load(path); err == nil {
		return nil
	} else if !errors.Is(err, ErrMissing) {
		return ErrUnavailable
	}
	if os.MkdirAll(filepath.Dir(path), 0700) != nil {
		return ErrUnavailable
	}
	key := make([]byte, 32)
	id := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return ErrUnavailable
	}
	if _, err := rand.Read(id); err != nil {
		return ErrUnavailable
	}
	name := hex.EncodeToString(id)
	data, err := json.Marshal(keyFile{ActiveKeyID: name, Keys: map[string]string{name: base64.StdEncoding.EncodeToString(key)}})
	if err != nil {
		return ErrUnavailable
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".keyring-*")
	if err != nil {
		return ErrUnavailable
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return ErrUnavailable
	}
	if f.Sync() != nil {
		f.Close()
		return ErrUnavailable
	}
	if f.Close() != nil {
		return ErrUnavailable
	}
	if err := os.Link(tmp, path); err != nil && !errors.Is(err, os.ErrExist) {
		return ErrUnavailable
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return ErrUnavailable
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		return ErrUnavailable
	}
	if err := directory.Close(); err != nil {
		return ErrUnavailable
	}
	_, err = Load(path)
	return err
}

func boundAAD(id string, aad []byte) []byte {
	data, _ := json.Marshal(struct {
		KeyID   string
		Context []byte
	}{id, aad})
	return data
}
func (k *Keyring) Seal(plaintext, aad []byte) (Envelope, error) {
	if k == nil || k.keys[k.active] == nil {
		return Envelope{}, ErrUnavailable
	}
	return Envelope{KeyID: k.active, Ciphertext: k.keys[k.active].Seal(nil, nil, plaintext, boundAAD(k.active, aad))}, nil
}
func (k *Keyring) Open(envelope Envelope, aad []byte) ([]byte, error) {
	if k == nil || k.keys[envelope.KeyID] == nil {
		return nil, ErrUnavailable
	}
	plain, err := k.keys[envelope.KeyID].Open(nil, nil, envelope.Ciphertext, boundAAD(envelope.KeyID, aad))
	if err != nil {
		return nil, ErrUnavailable
	}
	return plain, nil
}
