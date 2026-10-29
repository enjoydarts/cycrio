package ai

import (
	"strings"
	"testing"
)

func TestExtractionNullableIntervalUsesStringEnumAndSeparateNullBranch(t *testing.T) {
	schema := ExtractionSchema()
	item := schema["properties"].(map[string]any)["events"].(map[string]any)["items"].(map[string]any)
	unit := item["properties"].(map[string]any)["interval_unit"].(map[string]any)
	branches, ok := unit["anyOf"].([]any)
	if !ok || len(branches) != 2 {
		t.Fatal("nullable interval must separate the string enum from the null branch")
	}
	values := branches[0].(map[string]any)
	if values["type"] != "string" || branches[1].(map[string]any)["type"] != "null" {
		t.Fatal("invalid interval union")
	}
	enum, ok := values["enum"].([]string)
	if !ok || len(enum) != 4 {
		t.Fatal("enum must contain only day, week, month and year strings")
	}
	for i, want := range []string{"day", "week", "month", "year"} {
		if enum[i] != want {
			t.Fatal("unexpected recurrence unit")
		}
	}
}

func TestExtractionIntervalStillAcceptsMissingFactsAndRejectsUnknownUnits(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{{`"day"`, true}, {`"week"`, true}, {`"month"`, true}, {`"year"`, true}, {`null`, true}, {`"fortnight"`, false}, {`"null"`, false}, {`1`, false}} {
		t.Run(tc.value, func(t *testing.T) {
			_, err := ValidateExtraction([]byte(strings.Replace(validExtraction, `"interval_unit":"month"`, `"interval_unit":`+tc.value, 1)))
			if (err == nil) != tc.valid {
				t.Fatal("unexpected interval validation result", err)
			}
		})
	}
}
