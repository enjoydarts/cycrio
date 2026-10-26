package mailprocessingruntime

import "testing"

func TestConstructionDoesNotContactDatabaseOrProviders(t *testing.T) {
	if New(nil, nil) == nil {
		t.Fatal("service not constructed")
	}
}
