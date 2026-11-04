package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestResearchCostRecalculationRequiresDatabaseButNoIdentityOrSecrets(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	for _, args := range [][]string{{"recalculate-research-costs"}, {"recalculate-research-costs", "-apply"}} {
		var out bytes.Buffer
		err := run(context.Background(), args, strings.NewReader(""), &out)
		if err == nil || err.Error() != "database connection is required" || out.Len() != 0 {
			t.Fatalf("invalid offline recalculation requirements: %v %q", err, out.String())
		}
	}
}

func TestRecalculationApplyFlagCannotChangeOtherCommands(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	err := run(context.Background(), []string{"migrate", "-apply"}, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || err.Error() != "invalid command options" {
		t.Fatal(err)
	}
}
