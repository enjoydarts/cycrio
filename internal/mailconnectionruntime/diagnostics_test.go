package mailconnectionruntime

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"strings"
	"testing"
)

func TestConnectionConfigurationFailurePreservesPrivateCause(t *testing.T) {
	for _, mode := range []string{"resolve", "json"} {
		t.Run(mode, func(t *testing.T) {
			f := &Factory{repo: &connectionFixture{s: mailaccounts.ConnectionSecret{Provider: "outlook", Password: []byte("refresh-secret"), AccessToken: []byte("access-secret")}}, resolve: func(context.Context, string) ([]byte, error) {
				if mode == "resolve" {
					return nil, errors.New("encryption backend unavailable: access-secret")
				}
				return []byte(`{"ClientSecret":`), nil
			}}
			_, err := f.Checker(context.Background(), mailaccounts.CredentialVersion{})
			var adapter mailaccounts.AdapterError
			if !errors.As(err, &adapter) || adapter.Code != "credentials_unavailable" {
				t.Fatalf("public classification changed: %v", err)
			}
			cause := errors.Unwrap(err)
			want := "encryption backend unavailable"
			if mode == "json" {
				want = "unexpected end"
			}
			if cause == nil || !strings.Contains(cause.Error(), want) {
				t.Fatalf("configuration cause discarded: %v", cause)
			}
			for _, secret := range []string{"access-secret", "refresh-secret"} {
				if strings.Contains(cause.Error(), secret) {
					t.Errorf("credential leaked: %s", secret)
				}
			}
		})
	}
}
