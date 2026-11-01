package ai

import "encoding/json"

// ParseResearchUsage shares the provider token accounting rules with normal AI
// calls. The JSON must be the usage object, not the complete provider response.
func ParseResearchUsage(provider string, raw json.RawMessage) Usage {
	v, err := decodeJSON(raw)
	if err != nil {
		return Usage{}
	}
	return parseUsage(provider, obj(v))
}
