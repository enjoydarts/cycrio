package subscriptions

import (
	"encoding/json"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailprocessing"
)

func TestReceivedAtRemainsPublicWhenUnknown(t *testing.T) {
	for _, value := range []any{Candidate{}, Source{}, mailprocessing.Result{}} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if string(fields["received_at"]) != "null" {
			t.Fatalf("%T: unknown received_at must be exposed as null", value)
		}
	}
}
