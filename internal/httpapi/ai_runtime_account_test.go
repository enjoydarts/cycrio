package httpapi

import "testing"

func TestRuntimeMailAccountFilter(t *testing.T) {
	user := "10000000-0000-4000-8000-000000000001"
	for _, account := range []string{"unattributed", "20000000-0000-4000-8000-000000000001"} {
		f, err := runtimeFilter("month=2026-10&user_id=" + user + "&account_id=" + account)
		if err != nil {
			t.Fatalf("valid account filter rejected: %v", err)
		}
		if f.AccountID != account {
			t.Fatal("account filter lost")
		}
	}
	for _, query := range []string{"account_id=unattributed", "user_id=" + user + "&account_id=invalid", "user_id=" + user + "&account_id=unattributed&account_id=unattributed"} {
		if _, err := runtimeFilter("month=2026-10&" + query); err == nil {
			t.Fatal("invalid filter accepted", query)
		}
	}
}

func TestRuntimeMailAccountRoutesRemainAdministratorOnly(t *testing.T) {
	for _, path := range []string{"/v1/admin/ai/costs", "/v1/admin/ai/calls"} {
		fixture := &runtimeFixture{}
		role := "user"
		h := runtimeHandler(fixture, &role)
		query := "?month=2026-10&user_id=10000000-0000-4000-8000-000000000001&account_id=unattributed"
		if w := credentialRequest(h, "GET", path+query, "", "Bearer valid", "http://localhost:3100", ""); w.Code != 403 || fixture.calls != 0 {
			t.Fatal("non-admin accessed mail costs", w.Code)
		}
		role = "platform_admin"
		if w := credentialRequest(h, "GET", path+query, "", "Bearer valid", "http://localhost:3100", ""); w.Code != 200 || fixture.filter.AccountID != "unattributed" {
			t.Fatal("admin filter not forwarded", w.Code)
		}
	}
}
