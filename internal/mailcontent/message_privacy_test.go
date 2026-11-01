package mailcontent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMessageJSONExcludesTransientBody(t *testing.T) {
	date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	encoded, err := json.Marshal(Message{Text: []byte("private-body"), ReceivedAt: &date})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, present := fields["Text"]; present || strings.Contains(string(encoded), "private-body") {
		t.Fatal("transient body serialized")
	}
}
