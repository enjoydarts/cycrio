package mailprocessing

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enjoydarts/cycrio/internal/ai"
	"github.com/enjoydarts/cycrio/internal/airuntime"
	"github.com/enjoydarts/cycrio/internal/mailcontent"
	"github.com/enjoydarts/cycrio/internal/mailsync"
	"github.com/enjoydarts/cycrio/internal/secretstore"
	"github.com/google/uuid"
)

type batchPipelineRepo struct {
	Repository
	mu           sync.Mutex
	work         map[string][]Work
	started      map[string]bool
	finished     map[string]Completion
	calls        map[string]int
	batchStarts  int
	legacyStarts int
	stopped      bool
	stopSignal   chan struct{}
}

func (r *batchPipelineRepo) StartBatch(_ context.Context, l Lease, limit int) ([]Work, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started[l.RunID] {
		return nil, ErrNotFound
	}
	r.started[l.RunID] = true
	r.batchStarts++
	w := r.work[l.RunID]
	return w[:min(len(w), limit)], nil
}

func (r *batchPipelineRepo) Start(_ context.Context, l Lease) (Work, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.legacyStarts++
	return r.work[l.RunID][0], nil
}

func (r *batchPipelineRepo) BeginCall(_ context.Context, l Lease, stage string) (airuntime.Call, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return airuntime.Call{}, ErrNotFound
	}
	for _, w := range r.work[l.RunID] {
		if w.AttemptID == l.AttemptID {
			r.calls[w.AttemptID+stage]++
			return airuntime.Call{ID: uuid.NewString()}, nil
		}
	}
	return airuntime.Call{}, ErrInvalid
}

func (r *batchPipelineRepo) FinishCall(_ context.Context, _ string, _ airuntime.CallUpdate) (airuntime.Call, error) {
	return airuntime.Call{}, nil
}

func (r *batchPipelineRepo) Finish(_ context.Context, l Lease, c Completion) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finished[l.AttemptID] = c
	if c.State == "unknown" {
		r.stopped = true
		if r.stopSignal != nil {
			close(r.stopSignal)
			r.stopSignal = nil
		}
	}
	return nil
}

func TestBatchJoinsPanickingChildAndStopsNewAICallsWithoutResending(t *testing.T) {
	r, keys, lease := batchFixture(t, 4)
	stopped := make(chan struct{})
	r.stopSignal = stopped
	reader := textReader(func(ctx context.Context, _ string, _ []byte, source mailcontent.Source) ([]byte, error) {
		if source.UID == 1 {
			panic("private adapter detail")
		}
		select {
		case <-stopped:
			return []byte("sensitive"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	var sends atomic.Int64
	s := NewService(r, keys, reader, inference(func(context.Context, ai.Request) (ai.Response, error) { sends.Add(1); return ai.Response{}, nil }), func(context.Context, string) ([]byte, error) { return []byte("key"), nil }, WithBatchConcurrency(4))
	done := make(chan error, 1)
	go func() { done <- s.Execute(context.Background(), lease) }()
	if err := awaitBatch(t, done); err != nil {
		t.Fatal(err)
	}
	if len(r.finished) != 4 || sends.Load() != 0 {
		t.Fatal("panic leaked child or sent AI after stop", len(r.finished), sends.Load())
	}
	unknown := 0
	for _, result := range r.finished {
		if result.State == "unknown" && result.ErrorCode == "execution_panic" {
			unknown++
		}
	}
	if unknown != 1 {
		t.Fatal("panic classification lost", unknown)
	}
	if err := s.Execute(context.Background(), lease); err != nil || sends.Load() != 0 {
		t.Fatal("batch replay resent after panic", err)
	}
}

func TestBatchCancellationJoinsEveryChildAndClearsReturnedText(t *testing.T) {
	r, keys, lease := batchFixture(t, 4)
	entered := make(chan struct{}, 4)
	var mu sync.Mutex
	var buffers [][]byte
	reader := textReader(func(ctx context.Context, _ string, _ []byte, _ mailcontent.Source) ([]byte, error) {
		entered <- struct{}{}
		<-ctx.Done()
		body := []byte("private text returned alongside cancellation")
		mu.Lock()
		buffers = append(buffers, body)
		mu.Unlock()
		return body, ctx.Err()
	})
	s := NewService(r, keys, reader, nil, nil, WithBatchConcurrency(4))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Execute(ctx, lease) }()
	for range 4 {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("children not running")
		}
	}
	cancel()
	if err := awaitBatch(t, done); err != nil {
		t.Fatal(err)
	}
	if len(r.finished) != 4 || len(buffers) != 4 || len(r.calls) != 0 {
		t.Fatal("cancel did not join and finish all unsent attempts")
	}
	for _, result := range r.finished {
		if result.State != "failed" || result.ErrorCode != "cancelled" {
			t.Fatal("cancel classification", result)
		}
	}
	for _, b := range buffers {
		for _, c := range b {
			if c != 0 {
				t.Fatal("cancelled text retained")
			}
		}
	}
}

func TestBatchRejectsExternallySuppliedChildAttempt(t *testing.T) {
	r, keys, lease := batchFixture(t, 4)
	lease.AttemptID = r.work[lease.RunID][0].AttemptID
	s := NewService(r, keys, nil, nil, nil, WithBatchConcurrency(4))
	if err := s.Execute(context.Background(), lease); !errors.Is(err, ErrInvalid) || r.batchStarts != 0 {
		t.Fatal("external child bypassed batch reservation", err)
	}
}

func batchFixture(t *testing.T, count int) (*batchPipelineRepo, *secretstore.Keyring, Lease) {
	t.Helper()
	r, keys, lease := pipelineFixture(t)
	b := &batchPipelineRepo{work: map[string][]Work{}, started: map[string]bool{}, finished: map[string]Completion{}, calls: map[string]int{}}
	for i := range count {
		w := r.w
		w.AttemptID, w.MessageID = uuid.NewString(), uuid.NewString()
		var err error
		w.Envelope, err = keys.Seal([]byte(`{"UIDValidity":7,"UID":`+strconv.Itoa(i+1)+`}`), mailsync.MessageAAD(mailsync.Work{OwnerID: w.OwnerID, Run: mailsync.Run{AccountID: w.Run.AccountID}, FolderID: w.FolderID}, w.MessageID))
		if err != nil {
			t.Fatal(err)
		}
		b.work[lease.RunID] = append(b.work[lease.RunID], w)
	}
	return b, keys, lease
}

func awaitBatch(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("batch did not finish and join")
		return nil
	}
}

func TestBatchExecuteOverlapsFourReadsAndScopesEveryCallAndCompletion(t *testing.T) {
	r, keys, lease := batchFixture(t, 4)
	entered := make(chan uint32, 4)
	release := make(chan struct{})
	var mu sync.Mutex
	var buffers [][]byte
	reader := textReader(func(ctx context.Context, _ string, _ []byte, source mailcontent.Source) ([]byte, error) {
		entered <- source.UID
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		body := []byte("private synthetic text")
		mu.Lock()
		buffers = append(buffers, body)
		mu.Unlock()
		return body, nil
	})
	client := inference(func(context.Context, ai.Request) (ai.Response, error) {
		return ai.Response{Classification: &ai.Classification{Label: "not_related"}}, nil
	})
	s := NewService(r, keys, reader, client, func(context.Context, string) ([]byte, error) { return []byte("synthetic-key"), nil }, WithBatchConcurrency(4))
	done := make(chan error, 1)
	go func() { done <- s.Execute(context.Background(), lease) }()
	seen := map[uint32]bool{}
	for range 4 {
		select {
		case uid := <-entered:
			seen[uid] = true
		case <-time.After(3 * time.Second):
			close(release)
			t.Fatal("four reads did not overlap")
		}
	}
	select {
	case <-done:
		t.Fatal("Execute returned before children completed")
	default:
	}
	close(release)
	if err := awaitBatch(t, done); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 4 || len(r.finished) != 4 || r.batchStarts != 1 || r.legacyStarts != 0 {
		t.Fatal("batch lost work", len(seen), len(r.finished), r.batchStarts, r.legacyStarts)
	}
	for _, w := range r.work[lease.RunID] {
		if r.finished[w.AttemptID].State != "not_related" || r.calls[w.AttemptID+"classification"] != 1 || r.calls[w.AttemptID+"extraction"] != 0 {
			t.Fatal("attempt was confused or resent")
		}
	}
	for _, b := range buffers {
		for _, c := range b {
			if c != 0 {
				t.Fatal("mail text retained after batch join")
			}
		}
	}
	if err := s.Execute(context.Background(), lease); err != nil || len(r.calls) != 4 {
		t.Fatal("redelivery resent work", err)
	}
}

func TestBatchCapacityIsSharedAndCancelledWaiterNeverReservesMail(t *testing.T) {
	r, keys, lease := batchFixture(t, 4)
	other := Lease{RunID: uuid.NewString(), LeaseID: uuid.NewString()}
	for _, w := range r.work[lease.RunID] {
		w.Run.ID, w.AttemptID = other.RunID, uuid.NewString()
		r.work[other.RunID] = append(r.work[other.RunID], w)
	}
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	var active, maximum atomic.Int64
	reader := textReader(func(ctx context.Context, _ string, _ []byte, _ mailcontent.Source) ([]byte, error) {
		n := active.Add(1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		defer active.Add(-1)
		entered <- struct{}{}
		select {
		case <-release:
			return []byte("text"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	s := NewService(r, keys, reader, inference(func(context.Context, ai.Request) (ai.Response, error) {
		return ai.Response{Classification: &ai.Classification{Label: "not_related"}}, nil
	}), func(context.Context, string) ([]byte, error) { return []byte("key"), nil }, WithBatchConcurrency(4))
	done := make(chan error, 1)
	go func() { done <- s.Execute(context.Background(), lease) }()
	for range 4 {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			close(release)
			t.Fatal("first batch did not occupy four slots")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	second := make(chan error, 1)
	go func() { second <- s.Execute(ctx, other) }()
	cancel()
	if err := awaitBatch(t, second); !errors.Is(err, ErrUnavailable) {
		t.Fatal("cancelled capacity waiter", err)
	}
	r.mu.Lock()
	starts := r.batchStarts
	r.mu.Unlock()
	if starts != 1 || maximum.Load() != 4 {
		t.Fatal("capacity waiter reserved or exceeded limit", starts, maximum.Load())
	}
	close(release)
	if err := awaitBatch(t, done); err != nil {
		t.Fatal(err)
	}
	if err := s.Execute(context.Background(), other); err != nil {
		t.Fatal("slots not returned", err)
	}
	if maximum.Load() != 4 {
		t.Fatal("global cap exceeded", maximum.Load())
	}
}
