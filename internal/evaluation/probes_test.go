package evaluation

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProbeFileRoundTripAndPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probes.json")
	probes := []Probe{{ID: "body", Value: []byte("synthetic_private_marker")}}
	if err := WriteProbes(path, probes); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("probe permissions")
	}
	loaded, err := ReadProbes(path)
	if err != nil || len(loaded) != 1 || !bytes.Equal(loaded[0].Value, probes[0].Value) {
		t.Fatal("probe roundtrip")
	}
	if WriteProbes(path, probes) == nil {
		t.Fatal("probe file overwritten")
	}
}
func TestProbeFileRejectsInvalidDataWithoutEcho(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probes.json")
	for _, raw := range []string{`{"version":2,"probes":[]}`, `{"version":1,"probes":[]}`, `{"version":1,"secret":"never_echo_this"}`, strings.Repeat("x", 131073)} {
		if os.WriteFile(path, []byte(raw), 0600) != nil {
			t.Fatal("fixture write")
		}
		_, err := ReadProbes(path)
		if err == nil || strings.Contains(err.Error(), "never_echo_this") {
			t.Fatal("unsafe probe input")
		}
	}
	if WriteProbes(filepath.Join(t.TempDir(), "invalid"), nil) == nil {
		t.Fatal("empty probe file written")
	}
}
