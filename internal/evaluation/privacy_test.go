package evaluation

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("private upstream payload") }
func TestPrivacyScanDetectsRawAndEncodedMarkersAcrossReadBoundaries(t *testing.T) {
	marker := []byte("PRIVATE_SYNTHETIC_MARKER_123456789")
	for _, content := range []string{string(marker), hex.EncodeToString(marker), base64.StdEncoding.EncodeToString(marker)} {
		input := strings.Repeat("x", 32760) + content + strings.Repeat("z", 40000)
		hits, e := Scan(strings.NewReader(input), []Probe{{ID: "body", Value: marker}, {ID: "secret", Value: []byte("another-secret")}})
		if e != nil || len(hits) != 1 || hits[0] != "body" {
			t.Fatal("missed marker", hits, e)
		}
	}
	clean, e := Scan(bytes.NewBufferString("safe metadata"), []Probe{{ID: "body", Value: marker}})
	if e != nil || len(clean) != 0 {
		t.Fatal(clean, e)
	}
	raw, _ := json.Marshal(Probe{ID: "body", Value: marker})
	if bytes.Contains(raw, marker) || strings.Contains(string(raw), base64.StdEncoding.EncodeToString(marker)) {
		t.Fatal("probe value serialized")
	}
}
func TestPrivacyScanFailsClosedWithoutReaderDetails(t *testing.T) {
	for _, reader := range []io.Reader{failingReader{}, nil} {
		_, e := Scan(reader, []Probe{{ID: "body", Value: []byte("secret")}})
		if e == nil || strings.Contains(e.Error(), "private") {
			t.Fatal("unsafe scan error", e)
		}
	}
	for _, p := range []Probe{{ID: "body"}, {ID: "invalid\nname", Value: []byte("secret")}} {
		if _, e := Scan(strings.NewReader("metadata"), []Probe{p}); e == nil {
			t.Fatal("invalid probe accepted")
		}
	}
}
