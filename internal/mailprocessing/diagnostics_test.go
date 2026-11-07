package mailprocessing

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/credentials"
	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestPipelinePreservesOriginalDiagnosticsThroughFixedOutcomes(t *testing.T) {
	for _, stage := range []string{"reader", "provider", "panic", "recording"} {
		t.Run(stage, func(t *testing.T) {
			r, k, l := pipelineFixture(t)
			original := errors.New("upstream protocol failed: original detail")
			reader := textReader(func(context.Context, string, []byte, mailcontent.Source) ([]byte, error) {
				if stage == "reader" {
					return nil, original
				}
				return []byte("synthetic mail"), nil
			})
			client := inference(func(context.Context, ai.Request) (ai.Response, error) {
				if stage == "panic" {
					panic(original)
				}
				return ai.Response{}, original
			})
			r.recordFail = stage == "recording"
			s := NewService(r, k, reader, client, func(context.Context, string) ([]byte, error) { return []byte("key"), nil })
			ctx, scope := diagnostics.WithScope(context.Background())
			err := s.Execute(ctx, l)
			if stage == "recording" && !errors.Is(err, ErrUnavailable) || stage != "recording" && err != nil {
				t.Fatalf("return changed: %v", err)
			}
			if stage == "panic" && !strings.Contains(panicDiagnosticChain(scope.Error()), original.Error()) || stage != "panic" && !errors.Is(scope.Error(), original) {
				t.Fatalf("original cause lost: %v", scope.Error())
			}
		})
	}
}

func TestCredentialInfrastructureErrorsAreUnexpectedWithoutChangingOutcomes(t *testing.T) {
	for _, kind := range []string{"decrypt", "decode", "resolve", "missing"} {
		t.Run(kind, func(t *testing.T) {
			r, k, l := pipelineFixture(t)
			if kind == "decrypt" {
				r.w.Credential.Envelope.Ciphertext[0] ^= 1
			}
			if kind == "decode" {
				var err error
				r.w.Credential.Envelope, err = k.Seal([]byte("{invalid"), mailaccounts.CredentialAAD(r.w.Credential))
				if err != nil {
					t.Fatal(err)
				}
			}
			resolve := func(context.Context, string) ([]byte, error) {
				if kind == "missing" {
					return nil, credentials.ErrNotFound
				}
				return nil, errors.New("credential database unavailable")
			}
			reader := textReader(func(context.Context, string, []byte, mailcontent.Source) ([]byte, error) {
				return []byte("synthetic mail"), nil
			})
			ctx, scope := diagnostics.WithScope(context.Background())
			err := NewService(r, k, reader, inference(func(context.Context, ai.Request) (ai.Response, error) { return ai.Response{}, nil }), resolve).Execute(ctx, l)
			if err != nil || r.completion.ErrorCode != "credentials_unavailable" {
				t.Fatalf("outcome changed: %v %+v", err, r.completion)
			}
			if diagnostics.IsUnexpected(scope.Error()) != (kind != "missing") {
				t.Fatalf("unexpected classification wrong: %s %s", kind, panicDiagnosticChain(scope.Error()))
			}
		})
	}
}

type privatePanicError struct{ secret string }

func (e *privatePanicError) Error() string { return "provider protocol failed " + e.secret }

func panicDiagnosticChain(err error) string {
	var text []string
	for n := 0; err != nil && n < 16; n++ {
		text = append(text, err.Error())
		if typed, ok := err.(interface{ DiagnosticType() string }); ok {
			text = append(text, typed.DiagnosticType())
		}
		err = errors.Unwrap(err)
	}
	return strings.Join(text, "\n")
}

func TestSafeDoPanicDiagnosticsDoNotSerializeObjectsOrKnownSecrets(t *testing.T) {
	for _, value := range []any{
		map[string]string{"Opaque": "PRIVATE_OBJECT_VALUE"},
		struct{ Opaque string }{Opaque: "PRIVATE_OBJECT_VALUE"},
		"provider protocol failed credential_fragment CONFIDENTIAL_ORDER",
		&privatePanicError{secret: "credential_fragment CONFIDENTIAL_ORDER"},
	} {
		ctx, scope := diagnostics.WithScope(context.Background())
		_, err := safeDo(ctx, inference(func(context.Context, ai.Request) (ai.Response, error) { panic(value) }), ai.Request{Key: []byte("verysecret_credential_fragment_tail"), Input: ai.RequestInput{Text: "CONFIDENTIAL_ORDER_FRAGMENT suffix", Instructions: "unpublished instruction"}})
		text := panicDiagnosticChain(scope.Error())
		if err == nil || err.Error() != "transport_error" || scope.Error() == nil {
			t.Fatalf("panic outcome changed: %v %s", err, text)
		}
		for _, secret := range []string{"PRIVATE_OBJECT_VALUE", "credential_fragment", "CONFIDENTIAL_ORDER"} {
			if strings.Contains(text, secret) {
				t.Errorf("panic diagnostic leaked %q: %s", secret, text)
			}
		}
		if _, ok := value.(*privatePanicError); ok && (!strings.Contains(text, "privatePanicError") || !strings.Contains(text, "provider protocol failed")) {
			t.Errorf("original type/technical text lost: %s", text)
		}
	}
}

func TestReaderPanicDiagnosticsScrubKnownCredentialsAndSkipObjectValues(t *testing.T) {
	for _, value := range []any{"reader protocol failed synthetic secret", map[string]string{"Opaque": "PRIVATE_READER_VALUE"}} {
		r, k, l := pipelineFixture(t)
		reader := textReader(func(context.Context, string, []byte, mailcontent.Source) ([]byte, error) { panic(value) })
		ctx, scope := diagnostics.WithScope(context.Background())
		if err := NewService(r, k, reader, nil, nil).Execute(ctx, l); err != nil || r.completion.State != "unknown" || r.completion.ErrorCode != "execution_panic" {
			t.Fatalf("reader panic result changed: %v %+v", err, r.completion)
		}
		text := panicDiagnosticChain(scope.Error())
		if strings.Contains(text, "synthetic") || strings.Contains(text, "secret") || strings.Contains(text, "PRIVATE_READER_VALUE") {
			t.Fatalf("reader panic leaked: %s", text)
		}
		if _, ok := value.(string); ok && !strings.Contains(text, "reader protocol failed") {
			t.Fatalf("technical cause lost: %s", text)
		}
	}
}

type diagnosticBatchRepo struct {
	*batchPipelineRepo
	failed chan struct{}
}

func (r *diagnosticBatchRepo) Finish(ctx context.Context, l Lease, c Completion) error {
	err := r.batchPipelineRepo.Finish(ctx, l, c)
	if c.ErrorCode == "connection_failed" {
		close(r.failed)
	}
	return err
}

func TestBatchPanicDiagnosticWinsAfterEarlierOrdinaryFailure(t *testing.T) {
	r, keys, lease := batchFixture(t, 2)
	repo := &diagnosticBatchRepo{batchPipelineRepo: r, failed: make(chan struct{})}
	ordinary := errors.New("earlier connection error")
	panicErr := errors.New("later child panic")
	reader := textReader(func(ctx context.Context, _ string, _ []byte, source mailcontent.Source) ([]byte, error) {
		if source.UID == 1 {
			return nil, ordinary
		}
		select {
		case <-repo.failed:
			panic(panicErr)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	s := NewService(repo, keys, reader, nil, nil, WithBatchConcurrency(2))
	ctx, scope := diagnostics.WithScope(context.Background())
	if err := s.Execute(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(panicDiagnosticChain(scope.Error()), panicErr.Error()) || strings.Contains(panicDiagnosticChain(scope.Error()), ordinary.Error()) {
		t.Fatalf("panic hidden by earlier failure: %v", scope.Error())
	}
}

func TestBatchRejectsMixedOwnersBeforeExecutingChildren(t *testing.T) {
	r, keys, lease := batchFixture(t, 2)
	r.work[lease.RunID][1].OwnerID = uuid.NewString()
	s := NewService(r, keys, nil, nil, nil, WithBatchConcurrency(2))
	if err := s.Execute(context.Background(), lease); !errors.Is(err, ErrUnavailable) || len(r.finished) != 0 {
		t.Fatalf("mixed ownership reached processing: %v %+v", err, r.finished)
	}
}

func TestBatchFailureSelectionKeepsMostSignificantOriginalCause(t *testing.T) {
	expectedCtx, expectedScope := diagnostics.WithScope(context.Background())
	expected := mailaccounts.AdapterError{Code: "authentication_failed"}
	diagnostics.Record(expectedCtx, expected)
	faultCtx, faultScope := diagnostics.WithScope(context.Background())
	fault := errors.New("provider database unavailable")
	diagnostics.RecordUnexpected(faultCtx, fault)
	panicErr := errors.New("original child panic")
	for _, test := range []struct {
		name     string
		failures []workFailure
		want     error
	}{
		{"expected_then_genuine", []workFailure{{cause: expectedScope.Error()}, {cause: faultScope.Error()}}, fault},
		{"panic_then_genuine", []workFailure{{cause: panicErr, panicked: true}, {cause: faultScope.Error()}}, panicErr},
		{"genuine_then_panic", []workFailure{{cause: faultScope.Error()}, {cause: panicErr, panicked: true}}, panicErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			var selected workFailure
			for _, failure := range test.failures {
				selected = selectBatchFailure(selected, failure)
			}
			if !errors.Is(selected.cause, test.want) {
				t.Fatalf("significant original cause lost: got %s want %s", panicDiagnosticChain(selected.cause), test.want)
			}
		})
	}
}
