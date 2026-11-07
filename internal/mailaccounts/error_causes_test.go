package mailaccounts

import (
	"errors"
	"testing"
)

func TestSafeErrorPreservesPrivateCause(t *testing.T) {
	cause := errors.New("private database failure")
	err := safeError(cause)
	if !errors.Is(err, cause) || !errors.Is(err, ErrUnavailable) {
		t.Fatal("private cause or public classification was discarded")
	}
	if err.Error() != ErrUnavailable.Error() {
		t.Fatal("private cause leaked into public error")
	}
}
