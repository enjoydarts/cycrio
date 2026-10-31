package mailprocessingruntime

import "testing"

func TestConstructionDoesNotContactDatabaseOrProviders(t *testing.T) {
	service := New(nil, nil)
	if service == nil {
		t.Fatal("service not constructed")
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}
