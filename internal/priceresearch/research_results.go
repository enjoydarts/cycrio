package priceresearch

import (
	"bytes"
	"encoding/json"
)

// ParseResearchModels isolates invalid model entries while rejecting ambiguous
// response framing. An identity mentioned twice is never adopted.
func ParseResearchModels(text string, targets []Target) (models []Quote, prices []Quote, err error) {
	raw := []byte(researchJSON(text))
	if !json.Valid(raw) {
		return nil, nil, ErrInvalid
	}
	raw = bytes.TrimSpace(raw)
	var entries []json.RawMessage
	switch raw[0] {
	case '[':
		if json.Unmarshal(raw, &entries) != nil {
			return nil, nil, ErrInvalid
		}
	case '{':
		fields, ok := researchRootFields(raw)
		if !ok {
			return nil, nil, ErrInvalid
		}
		if value, wrapped := fields["models"]; wrapped {
			if len(fields) != 1 || len(bytes.TrimSpace(value)) == 0 || bytes.TrimSpace(value)[0] != '[' || json.Unmarshal(value, &entries) != nil {
				return nil, nil, ErrInvalid
			}
		} else if len(targets) == 1 {
			entries = []json.RawMessage{raw}
		} else {
			return nil, nil, ErrInvalid
		}
	default:
		return nil, nil, ErrInvalid
	}
	if len(entries) > 2*ResearchBatchSize {
		return nil, nil, ErrInvalid
	}
	counts := map[string]int{}
	for _, entry := range entries {
		for id := range researchEntryIDs(entry) {
			counts[id]++
		}
	}
	invalidEntry := false
	for _, entry := range entries {
		var q Quote
		if strictJSON(entry, &q) != nil || counts[q.ModelID] != 1 {
			invalidEntry = true
			continue
		}
		target, ok := findTarget(targets, q)
		if !ok {
			invalidEntry = true
			continue
		}
		canonical, ok := officialURL(q.SourceURL, q.Provider)
		if !ok {
			continue
		}
		q.SourceURL = canonical
		if q.ModelKind != "text_generation" && q.ModelKind != "non_text" {
			q.ModelKind = "unknown"
		}
		q.TextInput, q.StructuredOutput = nil, nil
		models = append(models, q)
		if q.ModelKind == "text_generation" && validQuote(q, target) {
			prices = append(prices, q)
		}
	}
	if len(models) == 0 && invalidEntry {
		return nil, nil, ErrInvalid
	}
	return models, prices, nil
}

// Decode only the root fields: duplicate fields within an entry are handled
// separately so that they cannot invalidate unrelated entries.
func researchRootFields(raw []byte) (map[string]json.RawMessage, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, false
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, false
		}
		key, ok := tok.(string)
		if !ok || fields[key] != nil {
			return nil, false
		}
		var value json.RawMessage
		if dec.Decode(&value) != nil {
			return nil, false
		}
		fields[key] = value
	}
	_, err := dec.Token()
	return fields, err == nil
}

func researchEntryIDs(raw []byte) map[string]bool {
	ids := map[string]bool{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return ids
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		var value json.RawMessage
		if dec.Decode(&value) != nil {
			break
		}
		if tok == "model_id" {
			var id string
			if json.Unmarshal(value, &id) == nil && id != "" {
				ids[id] = true
			}
		}
	}
	return ids
}

// ResearchOutputSchema keeps output concise and all uncertain prices nullable.
// Capability flags are deliberately absent; research does not configure them.
func ResearchOutputSchema(targets []Target) map[string]any {
	ids, providers := []string{}, []string{}
	seenID, seenProvider := map[string]bool{}, map[string]bool{}
	for _, target := range targets {
		if !seenID[target.ModelID] {
			ids = append(ids, target.ModelID)
			seenID[target.ModelID] = true
		}
		if !seenProvider[target.Provider] {
			providers = append(providers, target.Provider)
			seenProvider[target.Provider] = true
		}
	}
	properties := map[string]any{
		"model_id":         map[string]any{"type": "string", "enum": ids},
		"provider":         map[string]any{"type": "string", "enum": providers},
		"model_kind":       map[string]any{"type": "string", "enum": []string{"text_generation", "non_text", "unknown"}},
		"currency":         map[string]any{"type": "string", "enum": []string{"USD"}},
		"conditions":       map[string]any{"type": "string", "enum": []string{"standard_global"}},
		"source_url":       map[string]any{"type": []string{"string", "null"}},
		"max_input_tokens": map[string]any{"type": []string{"integer", "null"}},
	}
	required := []string{"model_id", "provider", "model_kind", "currency", "conditions", "source_url", "max_input_tokens"}
	for _, name := range []string{"input_per_million", "output_per_million", "cache_read_per_million", "cache_write_per_million", "cache_write_30m_per_million", "cache_write_1h_per_million", "cache_write_12h_per_million"} {
		properties[name] = map[string]any{"type": []string{"string", "null"}}
		required = append(required, name)
	}
	return map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"models"},
		"properties": map[string]any{"models": map[string]any{
			"type": "array", "minItems": len(targets), "maxItems": len(targets),
			"items": map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": properties},
		}},
	}
}
