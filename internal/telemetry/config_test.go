package telemetry

import (
	"strings"
	"testing"
)

func TestParseConfig(t *testing.T) {
	for _, raw := range []string{
		`{"project_token":"phc_12345678","host":"https://us.i.posthog.com"}`,
		`{"project_token":"phc_abCD_-12","host":"https://eu.i.posthog.com"}`,
	} {
		if _, err := ParseConfig([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{
		`null`, `{}`, `{"project_token":"phx_12345678","host":"https://us.i.posthog.com"}`,
		`{"project_token":"phc_12345678","host":"https://us.i.posthog.com/"}`,
		`{"project_token":"phc_12345678","host":"https://us.i.posthog.com","secret":"private"}`,
		`{"project_token":"phc_12345678","host":"https://us.i.posthog.com"} {}`,
		`{"PROJECT_TOKEN":"phc_12345678","host":"https://us.i.posthog.com"}`,
		`{"project_token":"phc_12345678","project_token":"phc_abcdefgh","host":"https://us.i.posthog.com"}`,
	} {
		if _, err := ParseConfig([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, err := ParseConfig([]byte(`{"project_token":"phc_` + strings.Repeat("a", 1021) + `","host":"https://us.i.posthog.com"}`)); err == nil {
		t.Fatal("oversized token accepted")
	}
}
