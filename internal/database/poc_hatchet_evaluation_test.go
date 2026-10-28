//go:build integration && evaluation && hatchet_evaluation

package database

import (
	"flag"
	"testing"
)

var hatchetEvaluationDirectory = flag.String("hatchet-evaluation-dir", "", "private artifact directory for synthetic Hatchet evaluation")
var hatchetEvaluationDatabase = flag.String("hatchet-evaluation-database", "", "local Hatchet database for read-only history inspection")

func TestHatchetEvaluation(t *testing.T) {
	if *hatchetEvaluationDirectory == "" || *hatchetEvaluationDatabase == "" {
		t.Fatal("Hatchet evaluation requires explicit local artifact and database flags")
	}
	runHatchetEvaluation(t, *hatchetEvaluationDirectory, *hatchetEvaluationDatabase)
}
