package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/posthog/posthog-go"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

var testConfig = Config{ProjectToken: "phc_12345678", Host: "https://us.i.posthog.com"}

const testOwner = "788bd216-e440-46fd-aef9-20c381957aa0"

func waitConfig(t *testing.T, r *Reporter) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for r.PublicConfig(context.Background()) == (Config{}) {
		if time.Now().After(deadline) {
			t.Fatal("configuration not loaded")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestReporterWirePrivacyAndGuard(t *testing.T) {
	wire := make(chan []byte, 4)
	var inside atomic.Bool
	guard := func(ctx context.Context, owner string, fn func(context.Context) error) error {
		if owner != testOwner {
			t.Errorf("unexpected owner %q", owner)
		}
		inside.Store(true)
		defer inside.Store(false)
		return fn(ctx)
	}
	transport := transportFunc(func(req *http.Request) (*http.Response, error) {
		if !inside.Load() {
			t.Error("transport escaped erasure guard")
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Error(err)
		}
		wire <- body
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})
	r := newReporter(context.Background(), true, func(context.Context) (Config, error) { return testConfig, nil }, guard, "api", transport, time.Hour)
	defer r.Close()
	waitConfig(t, r)
	r.Report(testOwner, "api_request", "api_server_error")
	select {
	case body := <-wire:
		var envelope struct {
			Batch []struct {
				Event      string                     `json:"event"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"batch"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatal(err)
		}
		if len(envelope.Batch) != 1 || envelope.Batch[0].Event != "$exception" {
			t.Fatalf("unexpected wire %s", body)
		}
		props := envelope.Batch[0].Properties
		for key := range props {
			if !allowedWireProperty(key) {
				t.Fatalf("unexpected property %s", key)
			}
		}
		if string(props["$process_person_profile"]) != "false" || string(props["$geoip_disable"]) != "true" {
			t.Fatalf("privacy controls missing: %s", body)
		}
		if strings.Contains(string(body), "/app/") || strings.Contains(string(body), "/Users/") || strings.Contains(string(body), "$ip") {
			t.Fatalf("sensitive wire %s", body)
		}
		if !strings.Contains(string(body), "reporter_test.go") {
			t.Fatalf("source stack missing: %s", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("event not sent")
	}
	r.Report(testOwner, "api_request", "api_server_error")
	select {
	case <-wire:
		t.Fatal("duplicate sent")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestReporterWireOmitsStacktraceWithoutFrames(t *testing.T) {
	for _, frames := range [][]posthog.StackFrame{nil, {}} {
		wire := make(chan []byte, 1)
		r := newReporter(context.Background(), true, func(context.Context) (Config, error) { return testConfig, nil }, nil, "worker", transportFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Error(err)
			}
			wire <- body
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
		}), time.Hour)
		waitConfig(t, r)
		r.mu.RLock()
		version := r.version
		r.mu.RUnlock()
		r.send(event{owner: "cycrio-system", operation: "worker_check", kind: "task_failed", version: version, frames: frames})
		r.Close()
		select {
		case body := <-wire:
			var envelope struct {
				Batch []struct {
					Properties struct {
						Exceptions []map[string]json.RawMessage `json:"$exception_list"`
					} `json:"properties"`
				} `json:"batch"`
			}
			if err := json.Unmarshal(body, &envelope); err != nil {
				t.Fatal(err)
			}
			if len(envelope.Batch) != 1 || len(envelope.Batch[0].Properties.Exceptions) != 1 {
				t.Fatalf("invalid exception: %s", body)
			}
			item := envelope.Batch[0].Properties.Exceptions[0]
			if _, ok := item["stacktrace"]; ok {
				t.Fatalf("frameless exception must omit stacktrace: %s", body)
			}
			if string(item["type"]) != `"task_failed"` || string(item["value"]) != `"task_failed"` {
				t.Fatalf("classification missing: %s", body)
			}
		case <-time.After(time.Second):
			t.Fatal("frameless event was not sent")
		}
	}
}

func TestSafeFrameRecognizesTrimmedMainAndRejectsExternalPaths(t *testing.T) {
	for _, command := range []string{"api", "worker"} {
		frame, ok := safeFrame(runtime.Frame{Function: "main.capturePollerPanic", File: "github.com/enjoydarts/cycrio/cmd/" + command + "/telemetry.go", Line: 37})
		if !ok || frame.Filename != "cmd/"+command+"/telemetry.go" || frame.Function != "cmd/"+command+".capturePollerPanic" || frame.LineNo != 37 {
			t.Fatalf("trimpath main frame missing: %+v %v", frame, ok)
		}
	}
	for _, file := range []string{"/Users/private/cmd/worker/main.go", "other.example/cmd/worker/main.go", "github.com/enjoydarts/cycrio/cmd/unknown/main.go", "github.com/enjoydarts/cycrio/cmd/worker/../../private.go"} {
		if _, ok := safeFrame(runtime.Frame{Function: "main.main", File: file, Line: 1}); ok {
			t.Fatalf("untrusted main source accepted: %s", file)
		}
	}
}

func TestReporterDropsDisabledInvalidAndInactiveEvents(t *testing.T) {
	for _, mode := range []string{"development", "disabled", "source_failure", "inactive", "invalid_owner", "invalid_operation", "invalid_kind"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			source := func(context.Context) (Config, error) {
				if mode == "source_failure" {
					return Config{}, errors.New("secret")
				}
				if mode == "disabled" {
					return Config{}, nil
				}
				return testConfig, nil
			}
			guard := func(ctx context.Context, _ string, fn func(context.Context) error) error {
				if mode == "inactive" {
					return errors.New("inactive")
				}
				return fn(ctx)
			}
			r := newReporter(context.Background(), mode != "development", source, guard, "api", transportFunc(func(*http.Request) (*http.Response, error) { calls.Add(1); return nil, errors.New("secret") }), time.Hour)
			if mode != "development" && mode != "disabled" && mode != "source_failure" {
				waitConfig(t, r)
			}
			owner, op, kind := testOwner, "api_request", "api_server_error"
			if mode == "invalid_owner" {
				owner = "private@example.com"
			}
			if mode == "invalid_operation" {
				op = "private@example.com"
			}
			if mode == "invalid_kind" {
				kind = "secret"
			}
			r.Report(owner, op, kind)
			r.Close()
			if calls.Load() != 0 {
				t.Fatal("unexpected send")
			}
		})
	}
}

func TestReporterRecoverPreservesPanicAndOmitsValue(t *testing.T) {
	r := New(context.Background(), false, nil, nil, "api")
	defer r.Close()
	defer func() {
		if got := recover(); got != "private message" {
			t.Fatalf("panic changed: %v", got)
		}
	}()
	defer r.Recover(testOwner, "api_request")()
	panic("private message")
}

func TestReporterQueueIsBoundedAndDisableDropsPending(t *testing.T) {
	var disabled atomic.Bool
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	guard := func(ctx context.Context, _ string, fn func(context.Context) error) error {
		close(entered)
		<-release
		return fn(ctx)
	}
	r := newReporter(context.Background(), true, func(context.Context) (Config, error) {
		if disabled.Load() {
			return Config{}, nil
		}
		return testConfig, nil
	}, guard, "worker", transportFunc(func(*http.Request) (*http.Response, error) { calls.Add(1); return nil, errors.New("secret") }), time.Millisecond)
	defer r.Close()
	waitConfig(t, r)
	r.Report(testOwner, "worker_check", "task_failed")
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("guard not entered")
	}
	start := time.Now()
	for i := 0; i < queueSize*100; i++ {
		r.Report(testOwner, "worker_check", "claim_failed")
	}
	if time.Since(start) > time.Second {
		t.Fatal("report blocked on sender")
	}
	if len(r.queue) != queueSize {
		t.Fatalf("queue size %d", len(r.queue))
	}
	disabled.Store(true)
	deadline := time.Now().Add(time.Second)
	for r.PublicConfig(context.Background()) != (Config{}) {
		if time.Now().After(deadline) {
			t.Fatal("disable not refreshed")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	r.Close()
	if calls.Load() != 0 {
		t.Fatal("disabled event escaped guard")
	}
}

func TestReporterSendFailureNeverRetriesAndShutdownBounded(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{})
	r := newReporter(context.Background(), true, func(context.Context) (Config, error) { return testConfig, nil }, nil, "worker", transportFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		close(entered)
		return nil, errors.New("private credential")
	}), time.Hour)
	waitConfig(t, r)
	r.Report("", "worker_check", "task_failed")
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("system send missing")
	}
	start := time.Now()
	r.Close()
	if time.Since(start) > 3*time.Second {
		t.Fatal("close exceeded bound")
	}
	if calls.Load() != 1 {
		t.Fatalf("retried %d times", calls.Load())
	}
}

func TestReporterTransportRejectsRedirectHost(t *testing.T) {
	r := &Reporter{config: testConfig, version: 1}
	transport := &privateTransport{reporter: r, event: event{owner: "cycrio-system", version: 1}, base: transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("untrusted host reached"); return nil, nil })}
	req, _ := http.NewRequest(http.MethodPost, "https://attacker.example/batch/", strings.NewReader(`{"api_key":"phc_12345678","batch":[{"properties":{}}]}`))
	if _, err := transport.RoundTrip(req); err == nil {
		t.Fatal("untrusted endpoint accepted")
	}
}

func TestEventHistoryExpiresAndStoresOnlyHashes(t *testing.T) {
	history := make(eventHistory)
	now := time.Now()
	e := event{owner: testOwner, operation: "api_request", kind: "api_server_error"}
	if !history.admit(e, now) {
		t.Fatal("first event suppressed")
	}
	key := sha256.Sum256([]byte(e.owner + "/" + e.operation + "/" + e.kind))
	if _, ok := history[key]; !ok {
		t.Fatal("owner was not hashed")
	}
	if history.admit(e, now.Add(time.Second)) {
		t.Fatal("duplicate admitted")
	}
	other := e
	other.kind = "api_panic"
	if !history.admit(other, now.Add(time.Minute)) {
		t.Fatal("different kind suppressed")
	}
	if len(history) != 1 {
		t.Fatal("expired owner retained below capacity")
	}
	history.expire(now.Add(2 * time.Minute))
	if len(history) != 0 {
		t.Fatal("idle history retained expired entries")
	}
}

func TestReporterSourcePanicFailsClosedAndRefreshRecovers(t *testing.T) {
	var calls atomic.Int32
	r := newReporter(context.Background(), true, func(context.Context) (Config, error) {
		if calls.Add(1) == 1 {
			panic("private source panic")
		}
		return testConfig, nil
	}, nil, "api", http.DefaultTransport, time.Millisecond)
	defer r.Close()
	waitConfig(t, r)
	if calls.Load() < 2 {
		t.Fatal("source did not resume after panic")
	}
}

func TestReporterTransportAndGuardPanicAreIsolated(t *testing.T) {
	for _, mode := range []string{"guard", "transport"} {
		t.Run(mode, func(t *testing.T) {
			r := &Reporter{config: testConfig, version: 1, guard: func(ctx context.Context, _ string, fn func(context.Context) error) error {
				if mode == "guard" {
					panic("private guard panic")
				}
				return fn(ctx)
			}}
			transport := &privateTransport{reporter: r, event: event{owner: testOwner, version: 1}, base: transportFunc(func(*http.Request) (*http.Response, error) { panic("private transport panic") })}
			req, _ := http.NewRequest(http.MethodPost, testConfig.Host+"/batch/", strings.NewReader(`{"api_key":"phc_12345678","batch":[{"properties":{}}]}`))
			response, err := transport.RoundTrip(req)
			if err == nil || response != nil || strings.Contains(err.Error(), "private") {
				t.Fatalf("panic not isolated: response=%v err=%v", response, err)
			}
		})
	}
}
