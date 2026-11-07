package telemetry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/enjoydarts/cycrio/internal/diagnostics"
	"github.com/google/uuid"
	"github.com/posthog/posthog-go"
)

const queueSize = 64
const sendTimeout = 2 * time.Second

type Source func(context.Context) (Config, error)
type Guard func(context.Context, string, func(context.Context) error) error

type event struct {
	owner, operation, kind string
	version                uint64
	frames                 []posthog.StackFrame
	exceptions             []posthog.ExceptionItem
}

// Reporter isolates telemetry failures and delivery from application operations.
type Reporter struct {
	mu          sync.RWMutex
	config      Config
	version     uint64
	closed      bool
	production  bool
	runtime     string
	source      Source
	guard       Guard
	transport   http.RoundTripper
	queue       chan event
	stop        chan struct{}
	done        chan struct{}
	refreshDone chan struct{}
	cancel      context.CancelFunc
	once        sync.Once
}

func New(ctx context.Context, production bool, source func(context.Context) (Config, error), guard func(context.Context, string, func(context.Context) error) error, runtime string) *Reporter {
	return newReporter(ctx, production, source, guard, runtime, http.DefaultTransport, 30*time.Second)
}

func newReporter(ctx context.Context, production bool, source Source, guard Guard, runtimeName string, transport http.RoundTripper, interval time.Duration) *Reporter {
	ctx, cancel := context.WithCancel(ctx)
	r := &Reporter{production: production && (runtimeName == "api" || runtimeName == "worker"), runtime: runtimeName, source: source, guard: guard, transport: transport, queue: make(chan event, queueSize), stop: make(chan struct{}), done: make(chan struct{}), refreshDone: make(chan struct{}), cancel: cancel}
	go r.refreshLoop(ctx, interval)
	go r.run()
	return r
}

func (r *Reporter) refreshLoop(ctx context.Context, interval time.Duration) {
	defer close(r.refreshDone)
	defer containPanic()
	if !r.production || r.source == nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		loadCtx, cancel := context.WithTimeout(ctx, sendTimeout)
		config := r.loadConfig(loadCtx)
		cancel()
		r.mu.Lock()
		if r.config != config {
			r.config = config
			r.version++
		}
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// A failing observability dependency must not terminate the application or
// preserve a previously enabled configuration after its read fails.
func (r *Reporter) loadConfig(ctx context.Context) (config Config) {
	defer func() {
		if recover() != nil {
			config = Config{}
		}
	}()
	loaded, err := r.source(ctx)
	if err == nil && loaded.valid() {
		return loaded
	}
	return Config{}
}

func containPanic() { _ = recover() }

// PublicConfig exposes only the ingestion configuration, never management credentials.
func (r *Reporter) PublicConfig(_ context.Context) Config {
	if r == nil {
		return Config{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return Config{}
	}
	return r.config
}

// Report preserves the fixed-metadata fallback for callers without an error.
func (r *Reporter) Report(ownerID, operation, kind string) {
	r.report(ownerID, operation, kind, nil)
}

// ReportException immediately sanitizes technical error details. No original
// error, panic value, request, headers or body are retained in the queue.
func (r *Reporter) ReportException(ownerID, operation, kind string, value any) {
	defer containPanic()
	r.report(ownerID, operation, kind, value)
}

func (r *Reporter) report(ownerID, operation, kind string, value any) {
	if r == nil || !r.production || !allowedOperation(operation) || !allowedKind(kind) {
		return
	}
	if ownerID == "" {
		ownerID = "cycrio-system"
	}
	if ownerID != "cycrio-system" {
		id, err := uuid.Parse(ownerID)
		if err != nil || id == uuid.Nil || id.String() != ownerID {
			return
		}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed || !r.config.valid() {
		return
	}
	e := event{owner: ownerID, operation: operation, kind: kind, version: r.version, frames: safeFramesFor(value)}
	if value != nil {
		e.exceptions = sanitizeExceptions(value, kind)
	}
	select {
	case r.queue <- e:
	default:
	}
}

// Recover records only a fixed panic classification and preserves the original panic.
func (r *Reporter) Recover(ownerID, operation string) func() {
	return func() {
		if value := recover(); value != nil {
			kind := "task_panic"
			if r != nil && r.runtime == "api" {
				kind = "api_panic"
			}
			r.ReportException(ownerID, operation, kind, value)
			panic(value)
		}
	}
}

func allowedOperation(value string) bool {
	switch value {
	case "api_request", "worker_check", "worker_catalog_sync", "worker_price_research", "worker_ai_test", "worker_mail_probe", "worker_mail_discovery", "worker_mail_processing", "worker_notification_delivery", "worker_subscription_register", "worker_subscription_changes", "worker_billing_record", "worker_notification_generate", "worker_erasure_purge", "worker_clerk_erasure", "worker_mail_automation", "worker_heartbeat":
		return true
	}
	return false
}

func allowedKind(value string) bool {
	switch value {
	case "api_panic", "api_server_error", "claim_failed", "submit_failed", "task_failed", "task_panic", "poller_failed", "poller_panic", "outcome_unavailable":
		return true
	}
	return false
}

func safeFrames() []posthog.StackFrame {
	pcs := make([]uintptr, 64)
	n := runtime.Callers(3, pcs)
	return framesFromPCs(pcs[:n])
}

func safeFramesFor(value any) []posthog.StackFrame {
	if err, ok := value.(error); ok {
		if pcs := diagnostics.Stack(err); len(pcs) > 0 {
			return framesFromPCs(pcs)
		}
	}
	return safeFrames()
}

func framesFromPCs(pcs []uintptr) []posthog.StackFrame {
	frames := runtime.CallersFrames(pcs)
	result := make([]posthog.StackFrame, 0, len(pcs))
	for {
		f, more := frames.Next()
		if frame, ok := safeFrame(f); ok {
			result = append(result, frame)
		}
		if !more {
			break
		}
	}
	// PostHog expects the outermost frame first.
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

func safeFrame(f runtime.Frame) (posthog.StackFrame, bool) {
	const module = "github.com/enjoydarts/cycrio/"
	var filename, fn string
	switch {
	case strings.HasPrefix(f.Function, module):
		fn = strings.TrimPrefix(f.Function, module)
		dir := strings.SplitN(fn, ".", 2)[0]
		if !(strings.HasPrefix(dir, "internal/") || strings.HasPrefix(dir, "cmd/")) || strings.Contains(dir, "..") {
			return posthog.StackFrame{}, false
		}
		filename = dir + "/" + path.Base(f.File)
	case strings.HasPrefix(f.Function, "main.") && strings.HasPrefix(f.File, module):
		// Executable symbols are named main.*, even with -trimpath. Accept
		// only source paths emitted by our two production command packages.
		filename = strings.TrimPrefix(f.File, module)
		dir := path.Dir(filename)
		if (dir != "cmd/api" && dir != "cmd/worker") || path.Clean(filename) != filename || !strings.HasSuffix(filename, ".go") {
			return posthog.StackFrame{}, false
		}
		fn = dir + "." + strings.TrimPrefix(f.Function, "main.")
	default:
		// Derive the package from its compiled symbol rather than preserving
		// machine-specific GOROOT, module-cache or home directory prefixes.
		lastSlash := strings.LastIndex(f.Function, "/")
		packageEnd := strings.Index(f.Function[lastSlash+1:], ".")
		if packageEnd < 0 || strings.HasPrefix(f.Function, "main.") {
			return posthog.StackFrame{}, false
		}
		packageName := f.Function[:lastSlash+1+packageEnd]
		if packageName == "" || strings.Contains(packageName, "..") || strings.ContainsAny(packageName, "\\ \t\n@") {
			return posthog.StackFrame{}, false
		}
		filename = packageName + "/" + path.Base(f.File)
		fn = f.Function
	}
	inApp := strings.HasPrefix(f.Function, module) || strings.HasPrefix(filename, "cmd/")
	// Keep the complete stack, but distinguish instrumentation from the
	// business code whose failure the exception is intended to diagnose.
	if strings.HasPrefix(filename, "internal/telemetry/") || strings.HasPrefix(filename, "internal/diagnostics/") || filename == "internal/httpapi/telemetry.go" || filename == "cmd/worker/telemetry.go" {
		inApp = false
	}
	return posthog.StackFrame{Filename: filename, LineNo: f.Line, Function: fn, InApp: inApp, Platform: "go", Lang: "go"}, true
}

type eventHistory map[[sha256.Size]byte]time.Time

func (h eventHistory) expire(now time.Time) {
	for key, at := range h {
		if now.Sub(at) >= time.Minute {
			delete(h, key)
		}
	}
}

func (h eventHistory) admit(e event, now time.Time) bool {
	h.expire(now)
	fingerprint := e.owner + "/" + e.operation + "/" + e.kind
	if len(e.exceptions) > 0 {
		details, _ := json.Marshal(struct {
			Items  []posthog.ExceptionItem
			Frames []posthog.StackFrame
		}{e.exceptions, e.frames})
		fingerprint += "/" + string(details)
	}
	key := sha256.Sum256([]byte(fingerprint))
	if at, exists := h[key]; exists && now.Sub(at) < time.Minute {
		return false
	}
	if len(h) >= 1024 {
		return false
	}
	h[key] = now
	return true
}

func (r *Reporter) run() {
	defer close(r.done)
	defer containPanic()
	recent := make(eventHistory)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case now := <-ticker.C:
			recent.expire(now)
		case e := <-r.queue:
			if !recent.admit(e, time.Now()) {
				continue
			}
			r.send(e)
		}
	}
}

func (r *Reporter) send(e event) {
	defer containPanic()
	r.mu.RLock()
	config, version, closed := r.config, r.version, r.closed
	r.mu.RUnlock()
	if closed || version != e.version || !config.valid() {
		return
	}
	client, err := posthog.NewWithConfig(config.ProjectToken, posthog.Config{
		Endpoint: config.Host, BatchSize: 1, MaxQueueSize: 1, MaxEnqueuedRequests: 1, MaxRetries: posthog.Ptr(0),
		ShutdownTimeout: sendTimeout, BatchUploadTimeout: sendTimeout, Compression: posthog.CompressionNone,
		Transport: &privateTransport{reporter: r, event: e, base: r.transport}, Logger: posthog.StdLogger(log.New(io.Discard, "", 0), false),
		BeforeSend: func(message posthog.Message) posthog.Message {
			exception, ok := message.(posthog.Exception)
			if !ok {
				return nil
			}
			exception.Properties = posthog.Properties{"operation": e.operation, "error_kind": e.kind, "runtime": r.runtime, "$process_person_profile": false, "$geoip_disable": true}
			exception.DebugImages = nil
			return exception
		},
	})
	if err != nil {
		return
	}
	defer func() { _ = client.Close() }()
	exception := posthog.NewDefaultException(time.Now().UTC(), e.owner, e.kind, e.kind)
	exception.DebugImages = nil
	items := e.exceptions
	if len(items) == 0 {
		items = []posthog.ExceptionItem{{Type: e.kind, Value: e.kind}}
	}
	// Empty stack slices become null during SDK cloning; PostHog requires an
	// array whenever stacktrace is present. A frameless exception remains valid.
	if len(e.frames) > 0 {
		items[0].Stacktrace = &posthog.ExceptionStacktrace{Type: "raw", Frames: e.frames}
	}
	exception.ExceptionList = items
	_ = client.Enqueue(exception)
}

type privateTransport struct {
	reporter *Reporter
	event    event
	base     http.RoundTripper
}

func allowedWireProperty(key string) bool {
	switch key {
	case "distinct_id", "$exception_list", "$exception_fingerprint", "$process_person_profile", "$geoip_disable", "$is_server", "$lib", "$lib_version", "operation", "error_kind", "runtime":
		return true
	}
	return false
}

func (t *privateTransport) RoundTrip(req *http.Request) (response *http.Response, err error) {
	defer func() {
		if recover() != nil {
			response = nil
			err = errors.New("telemetry delivery unavailable")
		}
	}()
	t.reporter.mu.RLock()
	host := t.reporter.config.Host
	t.reporter.mu.RUnlock()
	if req.URL.Scheme != "https" || req.URL.User != nil || "https://"+req.URL.Host != host {
		return nil, errors.New("untrusted telemetry endpoint")
	}
	// SDK serialization adds host/OS details after BeforeSend. Strip them at the final boundary.
	var envelope map[string]json.RawMessage
	if err := json.NewDecoder(io.LimitReader(req.Body, 128*1024)).Decode(&envelope); err != nil {
		return nil, errors.New("invalid telemetry envelope")
	}
	_ = req.Body.Close()
	var batch []map[string]json.RawMessage
	if err := json.Unmarshal(envelope["batch"], &batch); err != nil || len(batch) != 1 {
		return nil, errors.New("invalid telemetry batch")
	}
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(batch[0]["properties"], &properties); err != nil {
		return nil, errors.New("invalid telemetry properties")
	}
	for key := range properties {
		if !allowedWireProperty(key) {
			delete(properties, key)
		}
	}
	for key := range batch[0] {
		switch key {
		case "event", "library", "library_version", "properties", "timestamp", "type", "uuid":
		default:
			delete(batch[0], key)
		}
	}
	for key := range envelope {
		if key != "api_key" && key != "batch" {
			delete(envelope, key)
		}
	}
	properties["$process_person_profile"] = json.RawMessage("false")
	properties["$geoip_disable"] = json.RawMessage("true")
	batch[0]["properties"], _ = json.Marshal(properties)
	envelope["batch"], _ = json.Marshal(batch)
	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, errors.New("invalid telemetry encoding")
	}
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	clone.GetBody = nil
	send := func(ctx context.Context) error {
		t.reporter.mu.RLock()
		active := !t.reporter.closed && t.reporter.version == t.event.version && t.reporter.config.valid()
		t.reporter.mu.RUnlock()
		if !active {
			return errors.New("telemetry disabled")
		}
		response, err = t.base.RoundTrip(clone.WithContext(ctx))
		return err
	}
	if t.event.owner == "cycrio-system" {
		err = send(req.Context())
	} else if t.reporter.guard != nil {
		err = t.reporter.guard(req.Context(), t.event.owner, send)
	} else {
		err = errors.New("telemetry guard unavailable")
	}
	return response, err
}

// Close stops admission, cancels configuration reads and bounds pending delivery.
func (r *Reporter) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.config = Config{}
		r.mu.Unlock()
		r.cancel()
		close(r.stop)
	})
	select {
	case <-r.done:
	case <-time.After(sendTimeout + 100*time.Millisecond):
	}
}
