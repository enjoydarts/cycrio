package priceresearch

import (
	"bytes"
	"encoding/json"
)

// InspectOpenAISearchUsage reads the Responses API output array. Only completed
// search actions incur search fees; opening or finding within a page does not.
// Evidence deliberately excludes call IDs, queries, URLs and response content.
func InspectOpenAISearchUsage(raw json.RawMessage, limit int) (observed int, known bool, actions []SearchAction) {
	var output []json.RawMessage
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &output) != nil {
		return 0, false, nil
	}
	type callMetadata struct {
		action string
		status string
	}
	seen := make(map[string]callMetadata)
	uncertain, invalid := false, false
	completedBuiltins := 0
	for _, item := range output {
		var entry struct {
			Type   string          `json:"type"`
			ID     string          `json:"id"`
			Status string          `json:"status"`
			Action json.RawMessage `json:"action"`
		}
		if json.Unmarshal(item, &entry) != nil || entry.Type == "" {
			invalid = true
			continue
		}
		if entry.Type != "web_search_call" {
			continue
		}
		var action struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(entry.Action, &action) != nil {
			action.Type = ""
		}
		metadata := callMetadata{action.Type, entry.Status}
		duplicate := false
		if entry.ID != "" {
			if previous, exists := seen[entry.ID]; exists {
				if previous == metadata {
					continue
				}
				invalid, duplicate = true, true
			} else {
				seen[entry.ID] = metadata
			}
		}
		evidence := SearchAction{Action: "unknown", Status: "unknown"}
		switch action.Type {
		case "search", "open_page", "find_in_page":
			evidence.Action = action.Type
		}
		switch entry.Status {
		case "completed", "in_progress", "searching", "failed":
			evidence.Status = entry.Status
		}
		if len(actions) < 32 {
			actions = append(actions, evidence)
		}
		if !duplicate && evidence.Action != "unknown" && evidence.Status == "completed" {
			completedBuiltins++
		}
		switch evidence.Action {
		case "search":
			if evidence.Status == "completed" {
				if !duplicate {
					observed++
				}
			} else {
				uncertain = true
			}
		case "unknown":
			uncertain = true
		}
	}
	// max_tool_calls bounds processed built-in tool calls; further attempts are
	// ignored. Completed searches, page opens and page finds all consume this
	// cap, proving the search total even when an unfinished attempt also appears.
	// Do not clamp an unexpectedly larger completed total or override malformed
	// evidence. Only searches contribute to the billable observed count.
	return observed, !invalid && (!uncertain || (limit > 0 && completedBuiltins == limit)), actions
}
