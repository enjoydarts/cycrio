package mailcontent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/mailaccounts"
)

func code(err error) string {
	var e mailaccounts.AdapterError
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestNormalizeDecodesTextAndHTML(t *testing.T) {
	for _, tc := range []struct{ name, raw, media, charset, encoding, want string }{
		{"plain", "hello\r\nworld\rbye", "text/plain", "UTF-8", "7bit", "hello\nworld\nbye"},
		{"base64", "aGVsbG8=", "text/plain", "utf-8", "base64", "hello"},
		{"quoted-printable", "caf=C3=A9", "text/plain", "utf-8", "quoted-printable", "café"},
		{"shift-jis", "\x93\xfa\x96\x7b", "text/plain", "Shift_JIS", "8bit", "日本"},
		{"iso2022jp", "\x1b$BF|K\\\x1b(B", "text/plain", "ISO-2022-JP", "7bit", "日本"},
		{"latin1", "caf\xe9", "text/plain", "ISO-8859-1", "binary", "café"},
		{"html", `<head><title>secret</title></head><div>Hello &amp; <b>world</b></div><script>secret</script><style>secret</style><template>secret</template><p>next<img src="https://private.invalid/a" alt="hidden"><br>line</p>`, "text/html", "utf-8", "8bit", "Hello & world\nnext\nline"},
		{"html-self-closing-script", `<script/>secret</script><p>visible</p>`, "text/html", "utf-8", "8bit", "visible"},
		{"html-self-closing-style", `<style/>secret</style><p>visible</p>`, "text/html", "utf-8", "8bit", "visible"},
		{"html-self-closing-template", `<template/>secret</template><p>visible</p>`, "text/html", "utf-8", "8bit", "visible"},
		{"html-omitted-head-end", `<head><title>hidden</title><body>visible</body>`, "text/html", "utf-8", "8bit", "visible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Normalize([]byte(tc.raw), tc.media, tc.charset, tc.encoding, MaxTextBytes)
			if err != nil || string(got) != tc.want {
				t.Fatalf("unexpected normalization: %q %v", got, err)
			}
		})
	}
}

func TestNormalizeRejectsInvalidAndOversizedContent(t *testing.T) {
	for _, tc := range []struct {
		raw, media, charset, encoding string
		max                           int
		want                          string
	}{
		{"x", "image/png", "utf-8", "8bit", 10, "unsupported_content"},
		{"x", "text/plain", "private-charset", "8bit", 10, "unsupported_content"},
		{"x", "text/plain", "utf-8", "private-encoding", 10, "unsupported_content"},
		{"\xff", "text/plain", "utf-8", "8bit", 10, "invalid_response"},
		{"\xff", "text/plain", "us-ascii", "7bit", 10, "invalid_response"},
		{"\x82", "text/plain", "Shift_JIS", "8bit", 10, "invalid_response"},
		{"\xff", "text/plain", "Shift_JIS", "8bit", 10, "invalid_response"},
		{"\x1b$B\x46", "text/plain", "ISO-2022-JP", "7bit", 10, "invalid_response"},
		{"!invalid-base64!", "text/plain", "utf-8", "base64", 100, "invalid_response"},
		{"hello", "text/plain", "utf-8", "8bit", 4, "input_too_large"},
		{"日本", "text/plain", "utf-8", "8bit", 5, "input_too_large"},
		{strings.Repeat("x", MaxTextBytes+1), "text/plain", "utf-8", "8bit", MaxTextBytes + 1, "input_too_large"},
		{strings.Repeat("x", MaxWireBytes+1), "text/html", "utf-8", "8bit", 10, "input_too_large"},
	} {
		_, err := Normalize([]byte(tc.raw), tc.media, tc.charset, tc.encoding, tc.max)
		if code(err) != tc.want || strings.Contains(err.Error(), "private-") {
			t.Fatalf("expected %s, got %v", tc.want, err)
		}
	}
	got, err := Normalize([]byte("日本"), "text/plain", "utf-8", "8bit", 6)
	if err != nil || string(got) != "日本" {
		t.Fatal("exact UTF-8 boundary rejected", err)
	}
}

func TestSourceNeverSerializesPrivateReference(t *testing.T) {
	b, err := json.Marshal(Source{Folder: "private-folder", UIDValidity: 77, UID: 9, MaxTextBytes: 10})
	if err != nil || string(b) != "{}" {
		t.Fatalf("private source leaked: %s %v", b, err)
	}
}
