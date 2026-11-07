package diagnostics

import (
	"strings"
	"testing"
)

func TestScrubKnownAllocationsDoNotFollowPrivateSourceWordCount(t *testing.T) {
	// Mail can approach 5 MiB while diagnostic text is at most 2048 bytes.
	// Building a token table for the private source makes allocations follow
	// the mail size even when only a few diagnostic words need checking.
	const sourceWords = "CONFIDENTIAL_PRIVATE_SOURCE mail AI あいう "
	source := strings.Repeat(sourceWords, (5<<20)/len(sourceWords))
	var result string
	measurement := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			result = ScrubKnown("validation_error SOURCE AI あいう constraint", source)
		}
	})
	t.Logf("%d-byte source: %d allocations, %d bytes per scrub", len(source), measurement.AllocsPerOp(), measurement.AllocedBytesPerOp())
	if measurement.AllocsPerOp() > 512 || measurement.AllocedBytesPerOp() > int64(2*len(source)+256*1024) {
		t.Fatalf("source-sized allocation repeated: %d allocations, %d bytes", measurement.AllocsPerOp(), measurement.AllocedBytesPerOp())
	}
	if strings.Contains(result, "SOURCE") || strings.Contains(result, "AI") || strings.Contains(result, "あいう") || !strings.Contains(result, "validation_error") {
		t.Fatal("source echo masking or technical detail changed")
	}
}

func TestScrubKnownKeepsShortTokenBoundariesAndPartialIdentifiers(t *testing.T) {
	for _, test := range []struct{ name, text, source, want string }{
		{"partial", "error CUSTOMER code", "prefix_CUSTOMER_suffix", "error [redacted] code"},
		{"short exact", "AI ID sql", "AI and ID", "[redacted] [redacted] sql"},
		{"short substring", "AI ID sql", "RAIL paid", "AI ID sql"},
		{"unicode exact", "あいう 仕様 エラー", " あいう 仕様 ", "[redacted] [redacted] エラー"},
		{"unicode substring", "仕様 エラー", "新仕様書", "仕様 エラー"},
		{"label", "invalid input=private contents", "input private contents", "invalid [redacted] (private value removed)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ScrubKnown(test.text, test.source); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}
