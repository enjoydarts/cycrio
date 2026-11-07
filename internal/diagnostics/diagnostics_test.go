package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestWrapKeepsPrivateCauseAndPublicFormatting(t *testing.T) {
	public := errors.New("service unavailable")
	cause := errors.New("upstream technical failure: private-value")
	err := Wrap(public, cause)
	if !errors.Is(err, public) || !errors.Is(err, cause) {
		t.Fatal("lost error identities")
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		if strings.Contains(fmt.Sprintf(format, err), "private-value") {
			t.Fatal("private cause formatted", format)
		}
	}
	raw, e := json.Marshal(err)
	if e != nil || strings.Contains(string(raw), "private-value") {
		t.Fatal("cause exposed in JSON", e)
	}
	if err.Error() != public.Error() {
		t.Fatal(err.Error())
	}
	if Wrap(public, nil) != public {
		t.Fatal("nil cause changed public error")
	}
	frames := runtime.CallersFrames(Stack(err))
	found := false
	for {
		f, more := frames.Next()
		if strings.Contains(f.Function, "TestWrapKeepsPrivateCauseAndPublicFormatting") {
			found = true
		}
		if !more {
			break
		}
	}
	if !found {
		t.Fatal("creation stack missing")
	}
}

type classifiedFailure struct{ code string }

func (e classifiedFailure) Error() string { return "public failure" }
func TestWrapPreservesPublicConcreteClassification(t *testing.T) {
	original := errors.New("dial tcp: connection reset by peer")
	wrapped := Wrap(classifiedFailure{code: "connection_failed"}, original)
	var classified classifiedFailure
	if !errors.As(wrapped, &classified) || classified.code != "connection_failed" || !errors.Is(wrapped, original) {
		t.Fatal("public error classification lost")
	}
}

type serializableFailure struct{ Code string }

func (e serializableFailure) Error() string { return "public failure" }
func TestWrapPreservesPublicJSONShape(t *testing.T) {
	for _, public := range []error{errors.New("public failure"), serializableFailure{Code: "connection_failed"}} {
		expected, _ := json.Marshal(public)
		got, err := json.Marshal(Wrap(public, errors.New("private cause value")))
		if err != nil || string(got) != string(expected) {
			t.Fatalf("public JSON changed: got=%s expected=%s err=%v", got, expected, err)
		}
	}
}

func TestScopeRecordsFirstCauseAndItsOrigin(t *testing.T) {
	ctx, scope := WithScope(context.Background())
	first := errors.New("first technical cause")
	Record(ctx, first)
	Record(ctx, errors.New("later failure"))
	if !errors.Is(scope.Error(), first) || len(Stack(scope.Error())) == 0 {
		t.Fatal("original cause missing")
	}
	Record(context.Background(), first)
	_, empty := WithScope(ctx)
	if empty.Error() != nil {
		t.Fatal("scope crossed request boundary")
	}
	var workers sync.WaitGroup
	for i := 0; i < 20; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); Record(ctx, errors.New("concurrent failure")) }()
	}
	workers.Wait()
	if !errors.Is(scope.Error(), first) {
		t.Fatal("first cause overwritten")
	}
}

func TestScrubTextPreservesTechnicalMessage(t *testing.T) {
	text := ScrubText("String should have at most 32768 characters; field state. email=person@example.com")
	if !strings.Contains(text, "32768") || strings.Contains(text, "person@example.com") {
		t.Fatal(text)
	}
}

type badUnwrap struct{}

func (badUnwrap) Error() string { return "technical error" }
func (badUnwrap) Unwrap() error { panic("private panic") }
func TestStackContainsHostileUnwrap(t *testing.T) {
	if Stack(badUnwrap{}) != nil {
		t.Fatal("unexpected trace")
	}
}

func TestScrubTextRemovesParserInputWithoutLosingParserReason(t *testing.T) {
	for _, message := range []string{`strconv.ParseFloat: parsing "John Doe private note": invalid syntax`, `parsing time "John Doe private note" as "2006-01-02": cannot parse "John Doe private note" as "2006"`} {
		got := ScrubText(message)
		if strings.Contains(got, "John Doe") || strings.Contains(got, "private note") {
			t.Fatal("parser input exposed", got)
		}
		if !strings.Contains(got, "parsing") {
			t.Fatal("technical parser context lost", got)
		}
	}
}
