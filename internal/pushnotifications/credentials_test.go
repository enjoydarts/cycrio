package pushnotifications

import (
	"strings"
	"testing"
)

const testAppID = "aa427041-a738-4caf-a8f7-192aca0cba98"
const testSubscriptionID = "ab427041-a738-4caf-a8f7-192aca0cba98"
const testOtherID = "ac427041-a738-4caf-a8f7-192aca0cba98"
const testKey = "private-api-key"

func testCredential() []byte {
	return []byte(`{"app_id":"` + testAppID + `","api_key":"` + testKey + `"}`)
}

func TestCredentialCanonicalEncoding(t *testing.T) {
	input := ` { "api_key": "private-api-key", "app_id": "AA427041-A738-4CAF-A8F7-192ACA0CBA98" } `
	got, err := NormalizeCredential(input)
	if err != nil || got != string(testCredential()) {
		t.Fatalf("canonical credential mismatch: %v", err)
	}
	parsed, err := ParseCredential([]byte(got))
	if err != nil || parsed.AppID != testAppID || parsed.APIKey != testKey {
		t.Fatalf("credential round trip failed: %v", err)
	}
}

func TestCredentialRejectsInvalidWithoutLeakingSecret(t *testing.T) {
	for name, input := range map[string]string{
		"app id":        `{"app_id":"invalid","api_key":"private-api-key"}`,
		"nil uuid":      `{"app_id":"00000000-0000-0000-0000-000000000000","api_key":"private-api-key"}`,
		"missing":       `{"app_id":"` + testAppID + `"}`,
		"unknown field": `{"app_id":"` + testAppID + `","api_key":"private-api-key","secret":"private-api-key"}`,
		"duplicate":     `{"app_id":"` + testAppID + `","api_key":"private-api-key","api_key":"replacement"}`,
		"trailing":      string(testCredential()) + ` {}`,
		"control":       `{"app_id":"` + testAppID + `","api_key":"private-api-key\r\n"}`,
		"unicode":       `{"app_id":"` + testAppID + `","api_key":"private-api-key日本語"}`,
		"space":         `{"app_id":"` + testAppID + `","api_key":"private api key"}`,
		"empty":         `{"app_id":"` + testAppID + `","api_key":""}`,
		"large":         `{"app_id":"` + testAppID + `","api_key":"` + strings.Repeat("x", 8193) + `"}`,
		"null":          `null`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := NormalizeCredential(input)
			if err == nil || got != "" {
				t.Fatal("invalid credential accepted")
			}
			if strings.Contains(err.Error(), "private-api-key") || strings.Contains(err.Error(), "replacement") {
				t.Fatal("secret leaked in validation error")
			}
			parsed, err := ParseCredential([]byte(input))
			if err == nil || parsed != (Credentials{}) {
				t.Fatal("invalid credential parsed")
			}
		})
	}
}

func TestCredentialCanonicalEncodingRemainsParseableAtKeyBoundary(t *testing.T) {
	input := `{"app_id":"` + testAppID + `","api_key":"` + strings.Repeat("<", 4096) + `"}`
	canonical, err := NormalizeCredential(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ParseCredential([]byte(canonical)); err != nil {
		t.Fatal("canonical credential could not be parsed")
	}
	if again, err := NormalizeCredential(canonical); err != nil || again != canonical {
		t.Fatal("canonical encoding not idempotent")
	}
}
