package subscriptions

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

type CandidateSourceRef struct {
	ResultID   string `json:"result_id"`
	EventIndex int    `json:"event_index"`
}

// A selected source must name both fields explicitly. Missing event_index
// must not silently authorize event zero, and unknown/duplicate keys are errors.
func (v *CandidateSourceRef) UnmarshalJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	start, e := d.Token()
	if e != nil || start != json.Delim('{') {
		return ErrInvalid
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, e := d.Token()
		key, ok := token.(string)
		if e != nil || !ok || (key != "result_id" && key != "event_index") {
			return ErrInvalid
		}
		if _, ok = fields[key]; ok {
			return ErrInvalid
		}
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ErrInvalid
		}
		fields[key] = value
	}
	end, e := d.Token()
	if e != nil || end != json.Delim('}') || d.Decode(new(any)) != io.EOF || len(fields) != 2 {
		return ErrInvalid
	}
	var next CandidateSourceRef
	if json.Unmarshal(fields["result_id"], &next.ResultID) != nil || json.Unmarshal(fields["event_index"], &next.EventIndex) != nil {
		return ErrInvalid
	}
	*v = next
	return nil
}

func ValidAdditionalSources(primary *string, index *int, additional []CandidateSourceRef) bool {
	if !ValidSource(primary, index) || len(additional) > 99 {
		return false
	}
	if len(additional) == 0 {
		return true
	}
	if primary == nil {
		return false
	}
	seen := map[CandidateSourceRef]bool{{ResultID: strings.ToLower(*primary), EventIndex: *index}: true}
	for _, ref := range additional {
		if !ValidSource(&ref.ResultID, &ref.EventIndex) {
			return false
		}
		ref.ResultID = strings.ToLower(ref.ResultID)
		if seen[ref] {
			return false
		}
		seen[ref] = true
	}
	return true
}

func ValidDismissInput(in DismissInput) bool {
	return in.Confirmed && ValidAdditionalSources(&in.SourceResultID, &in.SourceEventIndex, in.AdditionalSources)
}
