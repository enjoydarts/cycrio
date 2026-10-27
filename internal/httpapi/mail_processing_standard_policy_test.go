package httpapi

import (
	"context"
	"encoding/json"
	"github.com/enjoydarts/cycrio/internal/mailprocessing"
	"strings"
	"testing"
)

type standardProcessingFixture struct {
	processingFixture
	standard mailprocessing.StandardPolicyInput
}

func (f *standardProcessingFixture) SaveStandardPolicy(ctx context.Context, a string, in mailprocessing.StandardPolicyInput) (mailprocessing.PolicyState, error) {
	f.record(ctx, a, "")
	f.standard = in
	return mailprocessing.PolicyState{}, f.err
}

const standardPolicyBody = `{"expected_version":0,"runtime_version":3,"profile_id":"11111111-1111-4111-8111-111111111111","enabled":true,"primary_mode":"standard","primary_terms_id":"liquid-standard-2026-09-30","primary_retention":"","primary_confirmed":true,"secondary_route_id":"22222222-2222-4222-8222-222222222222","secondary_confirmed":true}`

func TestStandardPolicyHTTPAuthorizationAndStrictJSON(t *testing.T) {
	path := "/v1/admin/mail-processing/standard-policy"
	for _, tc := range []struct {
		role, token string
		status      int
	}{{"platform_admin", "Bearer valid", 200}, {"platform_admin", "", 401}, {"member", "Bearer valid", 403}} {
		f := &standardProcessingFixture{}
		w := credentialRequest(processingHandler(f, tc.role), "PUT", path, standardPolicyBody, tc.token, "http://localhost:3100", "application/json")
		if w.Code != tc.status {
			t.Fatal(w.Code, w.Body.String())
		}
		if tc.status == 200 && (f.standard.RuntimeVersion != 3 || f.actor != "authenticated-owner") {
			t.Fatal(f)
		}
		if tc.status != 200 && f.calls != 0 {
			t.Fatal("unauthorized called service")
		}
	}
	var members map[string]json.RawMessage
	if json.Unmarshal([]byte(standardPolicyBody), &members) != nil {
		t.Fatal("fixture")
	}
	for key := range members {
		for _, bad := range []string{"null", "missing"} {
			copy := map[string]json.RawMessage{}
			for k, v := range members {
				copy[k] = v
			}
			if bad == "missing" {
				delete(copy, key)
			} else {
				copy[key] = json.RawMessage("null")
			}
			body, _ := json.Marshal(copy)
			f := &standardProcessingFixture{}
			w := credentialRequest(processingHandler(f, "platform_admin"), "PUT", path, string(body), "Bearer valid", "http://localhost:3100", "application/json")
			if w.Code != 400 || f.calls != 0 {
				t.Fatal(key, bad, w.Code, w.Body.String())
			}
		}
	}
	for _, body := range []string{strings.TrimSuffix(standardPolicyBody, "}") + `,"unknown":true}`, strings.Replace(standardPolicyBody, `"runtime_version":3`, `"runtime_version":3.1`, 1), strings.TrimSuffix(standardPolicyBody, "}") + `,"enabled":false}`} {
		f := &standardProcessingFixture{}
		w := credentialRequest(processingHandler(f, "platform_admin"), "PUT", path, body, "Bearer valid", "http://localhost:3100", "application/json")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
