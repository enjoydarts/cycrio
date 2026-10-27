package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStrictAdminJSONAcceptsOptionalTagOptions(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{{`{"route_id":"selected"}`, true}, {`{}`, true}, {`{"route_id,omitempty":"selected"}`, false}, {`{"route_id":"a","route_id":"b"}`, false}, {`{"Route_id":"a"}`, false}, {`{"route_id":"a","key":"secret"}`, false}} {
		r := httptest.NewRequest("PUT", "/", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		var in struct {
			RouteID string `json:"route_id,omitempty"`
		}
		e := readAdminJSON(w, r, &in)
		if (e == nil) != tc.valid {
			t.Fatal(tc.body, e)
		}
	}
}
