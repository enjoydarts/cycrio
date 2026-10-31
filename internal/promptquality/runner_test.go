package promptquality

import (
	"context"
	"errors"
	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/google/uuid"
	"testing"
)

type fakeRepo struct {
	state                         airuntime.State
	begins, finishes              int
	failBegin, failFinish, change bool
	last                          airuntime.BeginCallInput
	saveCancelled                 bool
}

func (f *fakeRepo) State(context.Context) (airuntime.State, error) {
	s := f.state
	if f.change && f.begins > 0 {
		s.Version++
	}
	return s, nil
}
func (f *fakeRepo) BeginCall(_ context.Context, in airuntime.BeginCallInput) (airuntime.Call, error) {
	f.last = in
	if f.failBegin {
		return airuntime.Call{}, errors.New("SECRET")
	}
	f.begins++
	return airuntime.Call{ID: uuid.NewString()}, nil
}
func (f *fakeRepo) FinishCall(ctx context.Context, _ string, _ airuntime.CallUpdate) (airuntime.Call, error) {
	f.finishes++
	f.saveCancelled = ctx.Err() != nil
	if f.failFinish {
		return airuntime.Call{}, errors.New("SECRET")
	}
	return airuntime.Call{}, nil
}

type fakeClient struct {
	calls  int
	fail   bool
	cancel context.CancelFunc
	key    []byte
}

func (f *fakeClient) Do(_ context.Context, r ai.Request) (ai.Response, error) {
	f.calls++
	f.key = r.Key
	if f.cancel != nil {
		f.cancel()
	}
	if f.fail {
		return ai.Response{}, errors.New("SECRET provider response")
	}
	return ai.Response{Classification: &ai.Classification{Label: "not_related"}, Extraction: &ai.Extraction{}}, nil
}
func fixture() (*fakeRepo, *fakeClient) {
	return &fakeRepo{state: airuntime.State{Version: 2, ActiveReady: true, Active: &airuntime.Profile{ID: uuid.NewString(), MaxInputBytes: 8192, TimeoutSeconds: 5, MaxOutputTokens: 512}}}, &fakeClient{}
}
func TestRunner176CallsNoRetryLedgerAndClearing(t *testing.T) {
	r, c := fixture()
	out, err := Run(context.Background(), r, c, func(context.Context, string) ([]byte, error) { return []byte("SECRET"), nil }, uuid.NewString())
	if err != nil || c.calls != 176 || r.begins != 176 || r.finishes != 176 || out.PlannedCalls != 176 || out.Partial {
		t.Fatalf("run %d %+v %v", c.calls, out, err)
	}
	if r.last.Scope != "admin_test" || r.last.RunID != nil || r.last.ConsumingUserID != nil || r.last.InitiatedByUserID == nil {
		t.Fatal(r.last)
	}
	for _, b := range c.key {
		if b != 0 {
			t.Fatal("key not cleared")
		}
	}
}
func TestRunnerAbortsThreeTechnicalErrors(t *testing.T) {
	r, c := fixture()
	c.fail = true
	out, err := Run(context.Background(), r, c, func(context.Context, string) ([]byte, error) { return []byte("SECRET"), nil }, uuid.NewString())
	if err == nil || c.calls != 3 || out.TechnicalErrors != 3 || !out.Partial || r.finishes != 3 {
		t.Fatalf("%+v %v", out, err)
	}
}
func TestRunnerStopsBeforeCallOnPermissionOrConfigChange(t *testing.T) {
	for _, mode := range []string{"permission", "configuration", "recording"} {
		t.Run(mode, func(t *testing.T) {
			r, c := fixture()
			r.failBegin = mode == "permission"
			r.change = mode == "configuration"
			r.failFinish = mode == "recording"
			out, err := Run(context.Background(), r, c, func(context.Context, string) ([]byte, error) { return []byte("SECRET"), nil }, uuid.NewString())
			if err == nil || !out.Partial || c.calls > 1 {
				t.Fatalf("%d %v", c.calls, err)
			}
		})
	}
}
func TestRunnerCancellationStillRecords(t *testing.T) {
	r, c := fixture()
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	_, _ = Run(ctx, r, c, func(context.Context, string) ([]byte, error) { return []byte("SECRET"), nil }, uuid.NewString())
	if r.finishes != 1 || r.saveCancelled || c.calls != 1 {
		t.Fatal("cancel record")
	}
}
