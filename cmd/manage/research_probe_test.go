package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestResearchProbeRequiresBoundedExplicitSelection(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	for _, args := range [][]string{
		{"probe-research"},
		{"probe-research", "-model-ids", "gpt-6", "-max-usd", "0.59"},
		{"probe-research", "-model-ids", "gpt-6", "-max-usd", "1.01"},
		{"probe-research", "-model-ids", "gpt-6", "-max-usd", "NaN"},
		{"probe-research", "-model-ids", "gpt-6,gpt-6", "-max-usd", "0.60"},
		{"probe-research", "-model-ids", "gpt-6,", "-max-usd", "0.60"},
		{"probe-research", "-model-ids", strings.Repeat("gpt-6,", 16) + "gpt-6-luna", "-max-usd", "0.60"},
	} {
		var out bytes.Buffer
		if err := run(context.Background(), args, strings.NewReader(""), &out); err == nil || err.Error() != "invalid research probe options" || out.Len() != 0 {
			t.Fatal(args, err, out.String())
		}
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"probe-research", "-model-ids", "gpt-6,gpt-6-luna", "-max-usd", "0.60"}, strings.NewReader(""), &out); err == nil || err.Error() != "database connection is required" || out.Len() != 0 {
		t.Fatal(err, out.String())
	}
	if err := run(context.Background(), []string{"migrate", "-max-usd", "0.60"}, strings.NewReader(""), &out); err == nil || err.Error() != "invalid command options" {
		t.Fatal(err)
	}
}
