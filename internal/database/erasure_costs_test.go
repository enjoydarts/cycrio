package database

import "testing"

func TestErasedCallHashUsesCanonicalUUID(t *testing.T) {
	if erasedCallHash("A00B0000-0000-4000-8000-000000000001") != erasedCallHash("a00b0000-0000-4000-8000-000000000001") {
		t.Fatal("equivalent call IDs cannot resolve the same late cost receipt")
	}
}
