package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestResearchPriceRepairRequiresScopedRunAndDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	for _, args := range [][]string{{"repair-research-prices"}, {"repair-research-prices", "-run-id", "invalid"}} {
		err := run(context.Background(), args, strings.NewReader(""), &bytes.Buffer{})
		if err == nil || err.Error() != "valid research run ID is required" {
			t.Fatal(args, err)
		}
	}
	for _, apply := range []bool{false, true} {
		args := []string{"repair-research-prices", "-run-id", "11111111-1111-4111-8111-111111111111"}
		if apply {
			args = append(args, "-apply")
		}
		var out bytes.Buffer
		err := run(context.Background(), args, strings.NewReader(""), &out)
		if err == nil || err.Error() != "database connection is required" || out.Len() != 0 {
			t.Fatal(err, out.String())
		}
	}
	if err := run(context.Background(), []string{"migrate", "-run-id", "11111111-1111-4111-8111-111111111111"}, strings.NewReader(""), &bytes.Buffer{}); err == nil || err.Error() != "invalid command options" {
		t.Fatal(err)
	}
}
