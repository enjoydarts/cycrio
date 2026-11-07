package airuntime

import (
	"context"
	"errors"
	"fmt"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"strings"
	"testing"
)

func TestProviderFailureDiagnosticSurvivesRecordingFailure(t *testing.T) {
	for _, recordFails := range []bool{false, true} {
		s, r, c, _ := runtimeServiceFixture()
		original := errors.New("provider TLS handshake: original detail")
		c.providerErrors = map[string]error{"liquid": original}
		if recordFails {
			r.finishErr = errors.New("recording also failed")
		}
		ctx, scope := diagnostics.WithScope(context.Background())
		err := s.Run(ctx, Lease{TestRunID: testID, LeaseID: testID})
		if recordFails && !errors.Is(err, ErrUnavailable) || !recordFails && err != nil {
			t.Fatalf("return changed: %v", err)
		}
		if !errors.Is(scope.Error(), original) {
			t.Fatalf("original cause lost: %v", scope.Error())
		}
		if !diagnostics.IsUnexpected(scope.Error()) {
			t.Fatal("provider fault not marked unexpected")
		}
	}
}

type diagnosticPanicClient struct{ InferenceClient }

func (diagnosticPanicClient) Do(context.Context, ai.Request) (ai.Response, error) {
	panic("provider protocol failed opaquecredential")
}

func TestProviderPanicScrubsKnownKeyAndPreservesPanicFlow(t *testing.T) {
	s, _, _, _ := runtimeServiceFixture()
	s.client = diagnosticPanicClient{}
	s.resolve = func(context.Context, string) ([]byte, error) { return []byte("opaquecredential"), nil }
	var value any
	func() {
		defer func() { value = recover() }()
		_ = s.Run(context.Background(), Lease{TestRunID: testID, LeaseID: testID})
	}()
	text := fmt.Sprint(value)
	if err, ok := value.(error); ok {
		for n := 0; err != nil && n < 16; n++ {
			text += "\n" + err.Error()
			err = errors.Unwrap(err)
		}
	}
	if value == nil || strings.Contains(text, "opaquecredential") || !strings.Contains(text, "provider protocol failed") {
		t.Fatalf("panic unsafe/lost: %s", text)
	}
}
