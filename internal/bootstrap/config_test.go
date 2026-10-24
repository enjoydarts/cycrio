package bootstrap

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testKey(host string) string {
	return "pk_test_" + base64.StdEncoding.EncodeToString([]byte(host+"$"))
}

func TestMissingConfigIsUnconfigured(t *testing.T) {
	config, err := Load(filepath.Join(t.TempDir(), "bootstrap.json"))
	if err != nil || config.Configured() {
		t.Fatal("missing configuration must stay unconfigured")
	}
}

func TestConfigDerivesClerkIssuer(t *testing.T) {
	c := Config{ClerkPublishableKey: testKey("pleasant-example.clerk.accounts.dev"), AllowedOrigins: []string{"http://localhost:3100", "https://cycrio.example"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if !c.Configured() || c.Issuer() != "https://pleasant-example.clerk.accounts.dev" {
		t.Fatal("issuer must come from configured publishable key")
	}
}

func TestConfigRejectsInvalidKeysAndOrigins(t *testing.T) {
	cases := []Config{
		{ClerkPublishableKey: "sk_test_never-accept-secret", AllowedOrigins: []string{"http://localhost:3100"}},
		{ClerkPublishableKey: testKey("https://clerk.example.com/path"), AllowedOrigins: []string{"http://localhost:3100"}},
		{ClerkPublishableKey: testKey("127.0.0.1:8080"), AllowedOrigins: []string{"http://localhost:3100"}},
		{ClerkPublishableKey: testKey("clerk.example.com"), AllowedOrigins: nil},
		{ClerkPublishableKey: testKey("clerk.example.com"), AllowedOrigins: []string{"https://example.com/path"}},
		{ClerkPublishableKey: testKey("clerk.example.com"), AllowedOrigins: []string{"https://user:password@example.com"}},
		{ClerkPublishableKey: testKey("clerk.example.com"), AllowedOrigins: []string{"null"}},
		{ClerkPublishableKey: testKey("clerk.example.com"), AllowedOrigins: []string{"http://example.com"}},
		{ClerkPublishableKey: testKey("clerk.example.com"), AllowedOrigins: []string{"https://example.com/"}},
	}
	for _, c := range cases {
		if err := c.Validate(); err == nil {
			t.Error("invalid configuration was accepted")
		} else if strings.Contains(err.Error(), c.ClerkPublishableKey) {
			t.Error("configuration error exposed input")
		}
	}
}

func TestLoadRejectsUnexpectedJSON(t *testing.T) {
	for _, data := range []string{`{"clerk_secret_key":"do-not-accept"}`, `{} {}`, `{"clerk_publishable_key":42}`} {
		path := filepath.Join(t.TempDir(), "bootstrap.json")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Error("unexpected bootstrap data accepted")
		}
	}
}

func TestSaveAndLoadPreservesConfigWithPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "bootstrap.json")
	c := Config{ClerkPublishableKey: testKey("pleasant-example.clerk.accounts.dev"), AllowedOrigins: []string{"http://localhost:3100"}}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got.ClerkPublishableKey != c.ClerkPublishableKey || got.Issuer() != c.Issuer() {
		t.Fatal("configuration did not round-trip")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("bootstrap file must be mode 0600")
	}
}
