package mailcontent

import (
	"bytes"
	"strings"
	"testing"
)

func TestWholeEmailNormalizationPhysicalAndUTF8Boundaries(t *testing.T) {
	source := Source{FullEmail: true}
	exact := strings.Repeat("x", MaxWireBytes-6) + "末尾"
	got, err := NormalizeSource([]byte(exact), "text/plain", "utf-8", "8bit", source)
	if err != nil || string(got) != exact {
		t.Fatal("exact physical UTF-8 boundary rejected", len(got), err)
	}
	for _, tc := range []struct {
		body, media, charset string
		want                 string
	}{
		{exact + "x", "text/plain", "utf-8", "input_too_large"},
		{strings.Repeat("\xe9", MaxWireBytes/2+1), "text/plain", "ISO-8859-1", "input_too_large"},
		{"\xff", "text/plain", "utf-8", "invalid_response"},
		{"\x82", "text/plain", "Shift_JIS", "invalid_response"},
		{"body", "image/png", "utf-8", "unsupported_content"},
	} {
		if _, err := NormalizeSource([]byte(tc.body), tc.media, tc.charset, "8bit", source); code(err) != tc.want {
			t.Fatal(tc.media, tc.charset, code(err), tc.want)
		}
	}
	if _, err := Normalize([]byte(strings.Repeat("x", MaxTextBytes+1)), "text/plain", "utf-8", "8bit", MaxWireBytes); code(err) != "input_too_large" {
		t.Fatal("legacy clamp removed", err)
	}
	for _, size := range []int{8193, 65537, MaxTextBytes + 1, (512 << 10) + 1} {
		raw := bytes.Repeat([]byte("x"), size)
		for _, media := range []string{"text/plain", "text/html"} {
			encoded := raw
			if media == "text/html" {
				encoded = append(append([]byte("<p>"), raw...), []byte("</p>")...)
			}
			got, err := NormalizeSource(encoded, media, "utf-8", "8bit", source)
			if err != nil || !bytes.Equal(got, raw) {
				t.Fatal("whole body changed", size, media, err)
			}
		}
	}
}
