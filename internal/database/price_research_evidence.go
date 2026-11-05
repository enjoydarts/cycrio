package database

import (
	"context"
	"encoding/json"

	"github.com/enjoydarts/cycrio/internal/priceresearch"
	"github.com/jackc/pgx/v5"
)

func safeResearchEnum(value string, allowed ...string) string {
	if value == "" {
		return ""
	}
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	return "unknown"
}

func safeResearchFailure(value string) string {
	return safeResearchEnum(value, "invalid_results", "incomplete_response", "search_unverified", "provider_unavailable", "invalid_response", "invalid_request", "search_limit_exceeded", "http_error", "request_failed")
}

func researchFailureOutcome(code, failure string) (string, string) {
	if code == "" {
		return "succeeded", "success"
	}
	switch code {
	case "configuration_changed":
		return "failed", "configuration_unavailable"
	case "credentials_required":
		return "failed", "credentials_unavailable"
	}
	switch safeResearchFailure(failure) {
	case "invalid_results", "incomplete_response", "search_unverified", "invalid_response", "search_limit_exceeded":
		return "failed", "invalid_output"
	}
	return "failed", "provider_unavailable"
}

func saveResearchResponseEvidence(ctx context.Context, tx pgx.Tx, id string, result priceresearch.Result) error {
	var limit, http *int
	if result.ToolCallLimit >= 1 && result.ToolCallLimit <= 8 {
		limit = &result.ToolCallLimit
	}
	if result.HTTPStatus >= 100 && result.HTTPStatus <= 599 {
		http = &result.HTTPStatus
	}
	actions := make([]priceresearch.SearchAction, 0, min(32, len(result.ToolActions)))
	for _, action := range result.ToolActions {
		if len(actions) == 32 {
			break
		}
		actionName := safeResearchEnum(action.Action, "search", "open_page", "find_in_page")
		status := safeResearchEnum(action.Status, "completed", "failed", "in_progress", "searching")
		if actionName == "" {
			actionName = "unknown"
		}
		if status == "" {
			status = "unknown"
		}
		actions = append(actions, priceresearch.SearchAction{Action: actionName, Status: status})
	}
	raw, _ := json.Marshal(actions)
	state := safeResearchEnum(result.ResponseState, "completed", "incomplete", "failed", "cancelled", "queued", "in_progress")
	failure := safeResearchFailure(result.FailureCode)
	// Replayed empty/unknown responses must not erase stronger saved evidence.
	_, err := tx.Exec(ctx, `UPDATE research_call_costs SET request_tool_limit=coalesce(request_tool_limit,$2),tool_actions=CASE WHEN tool_actions='[]'::jsonb THEN $3 ELSE tool_actions END,response_state=CASE WHEN response_state IN ('','unknown') THEN $4 ELSE response_state END,failure_code=CASE WHEN failure_code IN ('','unknown') THEN $5 ELSE failure_code END,http_status=coalesce(http_status,$6) WHERE call_id=$1`, id, limit, raw, state, failure, http)
	return err
}
