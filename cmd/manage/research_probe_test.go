package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestResearchProbeRequiresExplicitPaidConfirmationAndSelection(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	for _, args := range [][]string{
		{"probe-research"},
		{"probe-research", "-model-ids", "gpt-6"},
		{"probe-research", "-model-ids", "gpt-6", "-confirm-paid=false"},
		{"probe-research", "-confirm-paid"},
		{"probe-research", "-model-ids", "gpt-6,gpt-6", "-confirm-paid"},
		{"probe-research", "-model-ids", "gpt-6,", "-confirm-paid"},
		{"probe-research", "-model-ids", strings.Repeat("gpt-6,", 16) + "gpt-6-luna", "-confirm-paid"},
	} {
		var out bytes.Buffer
		if err := run(context.Background(), args, strings.NewReader(""), &out); err == nil || err.Error() != "invalid research probe options" || out.Len() != 0 {
			t.Fatal(args, err, out.String())
		}
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"probe-research", "-model-ids", "gpt-6,gpt-6-luna", "-confirm-paid"}, strings.NewReader(""), &out); err == nil || err.Error() != "database connection is required" || out.Len() != 0 {
		t.Fatal(err, out.String())
	}
	for _, args := range [][]string{{"migrate", "-confirm-paid"}, {"probe-research", "-model-ids", "gpt-6", "-max-usd", "0.60"}} {
		if err := run(context.Background(), args, strings.NewReader(""), &out); err == nil || err.Error() != "invalid command options" {
			t.Fatal(err)
		}
	}
}
