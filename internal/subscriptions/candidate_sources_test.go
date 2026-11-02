package subscriptions

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestAdditionalSourcesRequireExplicitPrimary(t *testing.T) {
	in := CreateInput{Values: validValues(), RequestID: "11111111-1111-4111-8111-111111111111", Confirmed: true}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		t.Fatal("fixture")
	}
	fields["additional_sources"] = json.RawMessage(`[{"result_id":"22222222-2222-4222-8222-222222222222","event_index":0}]`)
	raw, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(raw, &in) != nil {
		t.Fatal("fixture")
	}
	if _, err = NormalizeCreate(in); err == nil {
		t.Fatal("additional sources accepted without a primary source")
	}
}

func TestAdditionalSourcesAreBoundedUniqueAndExplicit(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	index := 0
	refs := make([]CandidateSourceRef, 99)
	for i := range refs {
		refs[i] = CandidateSourceRef{ResultID: fmt.Sprintf("%08x-2222-4222-8222-222222222222", i/32+1), EventIndex: i % 32}
	}
	if !ValidAdditionalSources(&id, &index, refs) {
		t.Fatal("100 explicitly selected sources rejected")
	}
	for _, bad := range [][]CandidateSourceRef{append(append([]CandidateSourceRef{}, refs...), CandidateSourceRef{ResultID: id, EventIndex: 1}), {{ResultID: id, EventIndex: 0}}, {{ResultID: "not-uuid", EventIndex: 0}}, {{ResultID: id, EventIndex: 32}}, {{ResultID: id, EventIndex: -1}}, {{ResultID: id, EventIndex: 1}, {ResultID: id, EventIndex: 1}}} {
		if ValidAdditionalSources(&id, &index, bad) {
			t.Fatal("invalid/duplicate source accepted", bad)
		}
	}
	id = "a1111111-1111-4111-8111-111111111111"
	if ValidAdditionalSources(&id, &index, []CandidateSourceRef{{ResultID: strings.ToUpper(id), EventIndex: 0}}) {
		t.Fatal("UUID casing bypassed duplicate detection")
	}
	for _, raw := range []string{`null`, `{}`, `{"result_id":"` + id + `"}`, `{"event_index":0}`, `{"result_id":"` + id + `","event_index":null}`, `{"result_id":"` + id + `","event_index":0,"extra":true}`, `{"result_id":"` + id + `","event_index":0,"event_index":1}`} {
		var ref CandidateSourceRef
		if json.Unmarshal([]byte(raw), &ref) == nil {
			t.Fatal("ambiguous selection accepted", raw)
		}
	}
}
