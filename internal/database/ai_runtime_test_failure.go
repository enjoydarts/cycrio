package database

import (
	"context"
	"encoding/json"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
)

func runtimeTestFailures(ctx context.Context, q runtimeQuery, run *airuntime.TestRun) error {
	// Each stage uses its own latest attempt. A secondary failure must never
	// overwrite the primary stage or change outcome/usage/cost ledger data.
	for _, stage := range []string{"classification", "extraction"} {
		var raw []byte
		if e := q.QueryRow(ctx, `SELECT coalesce((SELECT to_jsonb(http_status) FROM ai_calls WHERE run_id=$1 AND stage=$2 ORDER BY attempt_no DESC,started_at DESC,id DESC LIMIT 1),'null'::jsonb)`, run.ID, stage).Scan(&raw); e != nil {
			return diagnostics.Wrap(airuntime.ErrUnavailable, e)
		}
		var status *int
		if diagnosticErr := json.Unmarshal(raw, &status); diagnosticErr != nil {
			return diagnostics.Wrap(airuntime.ErrUnavailable, diagnosticErr)
		}
		if status == nil {
			continue
		}
		hint := airuntime.TestFailureForStatus(*status)
		if stage == "classification" {
			run.PrimaryFailure = hint
		} else {
			run.SecondaryFailure = hint
		}
	}
	return nil
}
