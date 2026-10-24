package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/bootstrap"
)

func TestConfigureAuthStoresOnlyPublicBootstrap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap.json")
	key := "pk_test_" + base64.StdEncoding.EncodeToString([]byte("example.clerk.accounts.dev$"))
	var out bytes.Buffer
	err := run(context.Background(), []string{"configure-auth", "-bootstrap-file", path}, strings.NewReader(key+"\nhttp://localhost:3100\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	c, err := bootstrap.Load(path)
	if err != nil || c.ClerkPublishableKey != key || !c.Configured() {
		t.Fatal("bootstrap was not saved")
	}
	if strings.Contains(out.String(), key) {
		t.Fatal("input echoed to output")
	}
}

func TestConfigureRejectsSecretAndIncompleteInput(t *testing.T) {
	for _, input := range []string{"sk_test_private\nhttp://localhost:3100\n", "", "pk_test_invalid\n\n"} {
		var out bytes.Buffer
		err := run(context.Background(), []string{"configure-auth", "-bootstrap-file", filepath.Join(t.TempDir(), "bootstrap.json")}, strings.NewReader(input), &out)
		if err == nil {
			t.Fatal("invalid bootstrap input accepted")
		}
		if strings.Contains(out.String(), "sk_test_private") || strings.Contains(err.Error(), "sk_test_private") {
			t.Fatal("secret input was exposed")
		}
	}
}

func TestManagementCommandsValidateSubjectBeforeDatabaseAccess(t *testing.T) {
	for _, cmd := range []string{"allow-user", "grant-admin", "revoke-admin", "disable-user"} {
		var out bytes.Buffer
		if err := run(context.Background(), []string{cmd, "-user-id", "not-a-clerk-id"}, strings.NewReader(""), &out); err == nil || err.Error() != "valid Clerk user ID is required" {
			t.Fatalf("%s did not validate subject", cmd)
		}
	}
	if err := run(context.Background(), []string{"unexpected"}, strings.NewReader(""), &bytes.Buffer{}); err == nil {
		t.Fatal("unknown command accepted")
	}
}
