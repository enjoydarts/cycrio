package diagnostics

import (
	"context"
	"errors"
	"testing"
)

func TestUnexpectedPromotesExpectedAndPreservesFirstGenuineCause(t *testing.T) {
	ctx, scope := WithScope(context.Background())
	expected := errors.New("credentials are not configured")
	first := errors.New("decryption failed")
	second := errors.New("result recording failed")
	Record(ctx, expected)
	if IsUnexpected(scope.Error()) {
		t.Fatal("ordinary recorded rejection marked unexpected")
	}
	RecordUnexpected(ctx, first)
	RecordUnexpected(ctx, second)
	Record(ctx, expected)
	if !IsUnexpected(scope.Error()) || !errors.Is(scope.Error(), first) || errors.Is(scope.Error(), second) || errors.Is(scope.Error(), expected) {
		t.Fatalf("genuine first cause lost: %v", scope.Error())
	}
	if len(Stack(scope.Error())) == 0 {
		t.Fatal("origin stack lost")
	}
}

func TestUnexpectedLookupBoundsHostileUnwrap(t *testing.T) {
	if IsUnexpected(unwrapPanic{}) {
		t.Fatal("hostile error marked unexpected")
	}
}

type unwrapPanic struct{}

func (unwrapPanic) Error() string { return "network failed" }
func (unwrapPanic) Unwrap() error { panic("private") }
