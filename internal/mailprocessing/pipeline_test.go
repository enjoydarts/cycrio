package mailprocessing

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailaccounts"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
	"path/filepath"
	"strings"
	"testing"
)

type pipelineRepo struct {
	Repository
	w              Work
	started        bool
	completion     Completion
	calls, records int
	recordFail     bool
	updates        []airuntime.CallUpdate
}

func (r *pipelineRepo) Start(context.Context, Lease) (Work, error) {
	if r.started {
		return Work{}, ErrNotFound
	}
	r.started = true
	return r.w, nil
}
func (r *pipelineRepo) BeginCall(context.Context, Lease, string) (airuntime.Call, error) {
	r.calls++
	return airuntime.Call{ID: uuid.NewString()}, nil
}
func (r *pipelineRepo) FinishCall(_ context.Context, _ string, u airuntime.CallUpdate) (airuntime.Call, error) {
	r.records++
	r.updates = append(r.updates, u)
	if r.recordFail {
		return airuntime.Call{}, ErrUnavailable
	}
	return airuntime.Call{}, nil
}
func (r *pipelineRepo) Finish(_ context.Context, _ Lease, c Completion) error {
	r.completion = c
	return nil
}

type textReader func(context.Context, string, []byte, mailcontent.Source) ([]byte, error)

func (f textReader) ReadText(c context.Context, l string, p []byte, s mailcontent.Source) ([]byte, error) {
	return f(c, l, p, s)
}

type inference func(context.Context, ai.Request) (ai.Response, error)

func (f inference) Do(c context.Context, r ai.Request) (ai.Response, error) { return f(c, r) }
func pipelineFixture(t *testing.T) (*pipelineRepo, *secretstore.Keyring, Lease) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keys.json")
	if e := secretstore.Initialize(path); e != nil {
		t.Fatal(e)
	}
	k, e := secretstore.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	w := Work{Run: Run{ID: uuid.NewString(), AccountID: uuid.NewString()}, OwnerID: uuid.NewString(), FolderID: uuid.NewString(), MessageID: uuid.NewString(), AttemptID: uuid.NewString(), FolderName: "INBOX", Profile: airuntime.Profile{ID: uuid.NewString(), MaxInputBytes: 4096, MaxOutputTokens: 512, TimeoutSeconds: 30, Primary: airuntime.ModelRef{Provider: "liquid", ModelID: "d1", CredentialID: uuid.NewString()}, Secondary: airuntime.ModelRef{Provider: "openai", ModelID: "synthetic", CredentialID: uuid.NewString()}}}
	w.Credential = mailaccounts.CredentialVersion{ID: uuid.NewString(), OwnerID: w.OwnerID, AccountID: w.Run.AccountID, Version: 1}
	w.Credential.Envelope, e = k.Seal([]byte(`{"login":"c3ludGhldGlj","password":"c2VjcmV0"}`), mailaccounts.CredentialAAD(w.Credential))
	if e != nil {
		t.Fatal(e)
	}
	w.Envelope, e = k.Seal([]byte(`{"UIDValidity":7,"UID":9}`), mailsync.MessageAAD(mailsync.Work{OwnerID: w.OwnerID, Run: mailsync.Run{AccountID: w.Run.AccountID}, FolderID: w.FolderID}, w.MessageID))
	if e != nil {
		t.Fatal(e)
	}
	return &pipelineRepo{w: w}, k, Lease{w.Run.ID, uuid.NewString()}
}
func TestExecuteGatesSecondCallAndKeepsSecretsTransient(t *testing.T) {
	for _, label := range []string{"not_related", "subscription_related", "uncertain"} {
		t.Run(label, func(t *testing.T) {
			r, k, l := pipelineFixture(t)
			var calls int
			var text, key []byte
			reader := textReader(func(_ context.Context, login string, password []byte, s mailcontent.Source) ([]byte, error) {
				if login != "synthetic" || string(password) != "secret" || s.UID != 9 || s.UIDValidity != 7 || s.MaxTextBytes != 4096 {
					t.Fatal("reference/credential decoding")
				}
				text = []byte("sensitive synthetic body")
				return text, nil
			})
			client := inference(func(_ context.Context, q ai.Request) (ai.Response, error) {
				calls++
				key = q.Key
				if q.Input.Text != "sensitive synthetic body" || !strings.Contains(q.Input.Instructions, "untrusted") || !strings.Contains(q.Input.Instructions, "URLs") {
					t.Fatal("unsafe request")
				}
				return ai.Response{Classification: &ai.Classification{Label: label}, Extraction: &ai.Extraction{Events: []ai.Event{{Service: "Synthetic", EventType: "payment"}}}}, nil
			})
			s := NewService(r, k, reader, client, func(context.Context, string) ([]byte, error) { return []byte("synthetic-key"), nil })
			if e := s.Execute(context.Background(), l); e != nil {
				t.Fatal(e)
			}
			want := 2
			state := "succeeded"
			if label == "not_related" {
				want = 1
				state = "not_related"
			}
			if calls != want || r.calls != want || r.records != want || r.completion.State != state {
				t.Fatal(calls, r.calls, r.records, r.completion)
			}
			if strings.Trim(string(text), "\x00") != "" || strings.Trim(string(key), "\x00") != "" {
				t.Fatal("transient buffers retained")
			}
			raw, _ := json.Marshal(r.completion)
			if strings.Contains(string(raw), "sensitive") {
				t.Fatal("body persisted")
			}
			raw, _ = json.Marshal(r.w)
			if string(raw) != "{}" {
				t.Fatal("work serialized", string(raw))
			}
			if e := s.Execute(context.Background(), l); e != nil || calls != want {
				t.Fatal("duplicate resent", calls, e)
			}
		})
	}
}
func TestExecuteStopsAfterUnknownRecordingFailureOrPanic(t *testing.T) {
	for _, kind := range []string{"timeout", "record", "reader_panic", "ai_panic", "invalid_label", "invalid_extraction", "source_missing"} {
		t.Run(kind, func(t *testing.T) {
			r, k, l := pipelineFixture(t)
			r.recordFail = kind == "record"
			calls := 0
			reader := textReader(func(context.Context, string, []byte, mailcontent.Source) ([]byte, error) {
				if kind == "reader_panic" {
					panic("sensitive body")
				}
				if kind == "source_missing" {
					return nil, mailaccounts.AdapterError{Code: "source_missing"}
				}
				return []byte("synthetic"), nil
			})
			client := inference(func(context.Context, ai.Request) (ai.Response, error) {
				calls++
				if kind == "ai_panic" {
					panic("sensitive key")
				}
				if kind == "timeout" {
					return ai.Response{}, context.DeadlineExceeded
				}
				if kind == "invalid_label" {
					return ai.Response{}, nil
				}
				out := ai.Response{Classification: &ai.Classification{Label: "subscription_related"}, Extraction: &ai.Extraction{Events: []ai.Event{{Service: "Synthetic", EventType: "wrong"}}}}
				return out, nil
			})
			s := NewService(r, k, reader, client, func(context.Context, string) ([]byte, error) { return []byte("key"), nil })
			err := s.Execute(context.Background(), l)
			if err != nil && !errors.Is(err, ErrUnavailable) {
				t.Fatal("unsafe error", err)
			}
			state := "unknown"
			if kind == "invalid_label" || kind == "invalid_extraction" || kind == "source_missing" {
				state = "failed"
			}
			if r.completion.State != state {
				t.Fatal(kind, r.completion)
			}
			if kind != "invalid_extraction" && calls > 1 {
				t.Fatal("resent after ambiguity")
			}
			raw, _ := json.Marshal(r.completion)
			if strings.Contains(string(raw), "sensitive") {
				t.Fatal("panic leaked")
			}
			before := calls
			_ = s.Execute(context.Background(), l)
			if calls != before {
				t.Fatal("duplicate resent")
			}
			if kind == "ai_panic" && r.records != 1 {
				t.Fatal("panic lost billing finalization")
			}
		})
	}
}

func TestSourceReferenceRequiresExactlyTwoCanonicalIntegerKeys(t *testing.T) {
	for _, raw := range []string{`{"UIDValidity":7,"UID":9}`, `{"UID":4294967295,"UIDValidity":1}`} {
		validity, uid, ok := decodeSourceReference([]byte(raw))
		if !ok || validity == 0 || uid == 0 {
			t.Fatal("valid source reference", raw)
		}
	}
	for _, raw := range []string{`{}`, `{"UIDValidity":7}`, `{"uidvalidity":7,"UID":9}`, `{"UIDValidity":7,"uid":9}`, `{"UIDValidity":7,"UID":9,"UID":10}`, `{"UIDValidity":7,"UID":9,"extra":1}`, `{"UIDValidity":7,"UID":9} {}`, `{"UIDValidity":7,"UID":0}`, `{"UIDValidity":0,"UID":9}`, `{"UIDValidity":7,"UID":4294967296}`, `{"UIDValidity":7,"UID":1.5}`, `{"UIDValidity":7,"UID":null}`, `{"UIDValidity":7,"UID":"9"}`, `{"UIDValidity":7,"UID":-1}`, `[]`} {
		if _, _, ok := decodeSourceReference([]byte(raw)); ok {
			t.Fatal("non-canonical source accepted", raw)
		}
	}
}

func TestExecuteDiscardsSuccessfulResponseAfterCancellationAndKeepsUsage(t *testing.T) {
	r, k, l := pipelineFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	reader := textReader(func(context.Context, string, []byte, mailcontent.Source) ([]byte, error) {
		return []byte("synthetic"), nil
	})
	client := inference(func(context.Context, ai.Request) (ai.Response, error) {
		calls++
		cancel()
		usd, amount := "USD", "0.01"
		return ai.Response{Classification: &ai.Classification{Label: "not_related"}, Cost: ai.Cost{Status: "provider_reported", Amount: &amount, Currency: &usd}}, nil
	})
	s := NewService(r, k, reader, client, func(context.Context, string) ([]byte, error) { return []byte("synthetic-key"), nil })
	if e := s.Execute(ctx, l); e != nil {
		t.Fatal(e)
	}
	if calls != 1 || r.completion.State != "unknown" || r.completion.ErrorCode != "request_cancelled" || r.records != 1 || r.updates[0].Cost.Amount == nil || *r.updates[0].Cost.Amount != "0.01" {
		t.Fatal("late success adopted or usage lost", r.completion, r.updates, calls)
	}
}
