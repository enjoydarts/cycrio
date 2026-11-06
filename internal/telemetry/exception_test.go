package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestScrubTextRetainsTechnicalDetailsAndRemovesPrivateValues(t *testing.T) {
	for _, message := range []string{"dial tcp: connection refused", "database transaction serialization failure", "provider returned HTTP 503: upstream unavailable", "cannot read properties of undefined (reading 'foo')", "ReferenceError: sk_foo is not defined", "database failed\ncaused by SQLSTATE 40001: serialization failure", "expected interface{}, got struct Foo{}", "cannot assign map[string]interface{} to Foo"} {
		if got := scrubText(message); got != message {
			t.Fatalf("technical message changed: %q", got)
		}
	}
	for _, message := range []string{
		"lookup secret@example.com failed", "request failed Bearer private-token-value", "signature eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJwcml2YXRlIn0.abcdefghijklmnop rejected",
		"provider rejected sk_live_privatecredential", "provider rejected sk-proj-privatecredential", "provider rejected phx_privatecredential",
		"provider failed: authorization: Basic private-authorization", "provider failed cookie=session=private-cookie", "mail subject=private subject", "mail from=private sender", "mail to=private recipient",
		"provider 503 body={\"email\":\"private@example.com\",\"text\":\"private body\"}", "provider rejected {\"unrecognized_field\":\"private content\"}", "invalid input \"private payload\"",
		"provider response:\nprivate mail body", "transport https://private-user:private-pass@api.example.com/v1/run?token=private-query#private-fragment failed",
		"open /Users/private-user/.config/private-settings.json failed", "open /root/private-user/private-settings.yaml failed",
		`Unexpected token '<', "<html>private mail body</html>" is not valid JSON`,
		`Unexpected token 'x', "private John Doe" is not valid JSON`,
		`invalid input syntax for integer: "private John Doe"`,
		`provider rejected ["private input", "another private input"]`,
		`provider rejected {"privatefield":"` + strings.Repeat("private", 4000),
	} {
		got := scrubText(message)
		if strings.Contains(got, "private") || strings.Contains(got, "secret@example.com") || strings.Contains(got, "eyJ") {
			t.Fatalf("sensitive value survived: %q", got)
		}
	}
	if got := scrubText(`Unexpected token 'x', "John Doe" is not valid JSON`); !strings.Contains(got, `token 'x'`) || strings.Contains(got, "John Doe") {
		t.Fatalf("token identifier/payload incorrectly handled: %q", got)
	}
	if got := scrubText("GET https://api.example.com/v1/run?token=private#private failed"); !strings.Contains(got, "https://api.example.com/v1/run") || !strings.Contains(got, "failed") {
		t.Fatalf("URL technical details lost: %q", got)
	}
}

func TestScrubTextRemovesOAuthMailAndWebhookCredentials(t *testing.T) {
	for _, message := range []string{
		"OAuth failed token=ya29.a0AfH6SMprivatecredential",
		"OAuth failed client_secret=GOCSPX-privatecredential",
		"OAuth failed client-secret=unprefixed-privatecredential",
		"OAuth failed client secret=unprefixed-privatecredential",
		"IMAP failed app_password=private app password",
		"IMAP failed app-password=private app password",
		"IMAP failed app password=private app password",
		"invalid Google token ya29.a0AfH6SMprivatecredential",
		"invalid Google secret GOCSPX-privatecredential",
		"invalid Resend key re_privatecredential0123456789",
		"invalid Webhook key whsec_privatecredentialbase64+/==",
		"invalid Google API key AIzaprivatecredential01234567890123456789",
		"unknown Clerk user user_privateidentifier01234567",
		"POST https://hc-ping.com/788bd216-e440-46fd-aef9-20c381957aa0: timeout",
		"GET https://api.example.com/users/private-user/preferences failed",
		"open postgresql://private-user:private-password@db.test/cycrio?password=private-query#private-fragment failed",
		"open imaps://private-user:private-password@mail.test/INBOX?token=private-query failed",
		"open postgresql://private%bad:private-password@db.test/cycrio failed",
		"authentication failed login=private-user",
		"authentication failed username=private-user",
		"authentication failed user_name=private-user",
		"authentication failed email=private-user",
		"authentication failed user=private-user",
	} {
		got := scrubText(message)
		if strings.Contains(got, "private") || strings.Contains(got, "base64") || strings.Contains(got, "+/==") || strings.Contains(got, "788bd216") {
			t.Fatalf("provider credential survived: %q", got)
		}
	}
	for _, message := range []string{"ReferenceError: sk_foo is not defined", "reading 'token' failed", "webhook handler whsec_ok missing", "source variable re_count invalid"} {
		if got := scrubText(message); got != message {
			t.Fatalf("short technical identifier changed: %q", got)
		}
	}
	if got := scrubText("open postgresql://alice:password@db.test/cycrio?sslmode=require: timeout"); strings.Contains(got, "alice") || strings.Contains(got, "password") || strings.Contains(got, "sslmode") || !strings.Contains(got, "postgresql://db.test/cycrio: timeout") {
		t.Fatalf("connection URL not safely retained: %q", got)
	}
}

func TestScrubTextRemovesInternationalizedEmail(t *testing.T) {
	for _, address := range []string{"太郎@例え.テスト", "δοκιμή@παράδειγμα.δοκιμή", "用户@xn--fsqu00a.xn--0zwm56d", "alice@example.com"} {
		if got := scrubText("send failed: " + address); strings.Contains(got, address) || got != "send failed: [redacted]" {
			t.Fatalf("internationalized email survived: %q", got)
		}
	}
	for _, message := range []string{"TypeError: reading 'foo' failed", "SQLSTATE 40001: serialization failure", "接続失敗: タイムアウト"} {
		if got := scrubText(message); got != message {
			t.Fatalf("ordinary technical text changed: %q", got)
		}
	}
}

type panickingError struct{}

func (panickingError) Error() string { panic("private") }

type unwrappingPanicError struct{}

func (unwrappingPanicError) Error() string { return "network write failed" }
func (unwrappingPanicError) Unwrap() error { panic("private unwrap payload") }

func TestSafeExceptionsRetainsCauseTypesAndIsolatesHostileErrors(t *testing.T) {
	items := sanitizeExceptions(fmt.Errorf("database lookup: %w", errors.New("connection refused for secret@example.com")), "task_failed")
	if len(items) != 2 || !strings.Contains(items[0].Type, "wrapError") || !strings.Contains(items[0].Value, "database lookup") || !strings.Contains(items[1].Value, "connection refused") {
		t.Fatalf("cause details missing: %+v", items)
	}
	if strings.Contains(fmt.Sprint(items), "secret@example.com") {
		t.Fatal("email retained")
	}
	items = sanitizeExceptions(panickingError{}, "task_panic")
	if len(items) == 0 || !strings.Contains(items[0].Type, "panickingError") {
		t.Fatalf("hostile error type lost: %+v", items)
	}
	items = sanitizeExceptions("runtime index out of range: private@example.com", "task_panic")
	if items[0].Type != "string" || !strings.Contains(items[0].Value, "index out of range") || strings.Contains(items[0].Value, "private@example.com") {
		t.Fatalf("panic detail incorrect: %+v", items)
	}
	items = sanitizeExceptions(42, "task_panic")
	if items[0].Type != "int" || items[0].Value != "42" {
		t.Fatalf("numeric panic lost: %+v", items)
	}
	items = sanitizeExceptions(map[string]string{"body": "private body"}, "task_panic")
	if strings.Contains(fmt.Sprint(items), "private body") {
		t.Fatal("structured panic leaked")
	}
	items = sanitizeExceptions(unwrappingPanicError{}, "task_failed")
	if len(items) != 1 || items[0].Value != "network write failed" {
		t.Fatalf("Unwrap panic escaped or lost technical detail: %+v", items)
	}
}

func TestReportExceptionQueueContainsOnlyImmediateSanitizedCopy(t *testing.T) {
	r := &Reporter{production: true, config: testConfig, version: 1, queue: make(chan event, 1)}
	original := errors.New("connection refused for private@example.com Bearer private-credential-value")
	r.ReportException("cycrio-system", "worker_check", "task_failed", original)
	e := <-r.queue
	encoded, err := json.Marshal(e.exceptions)
	if err != nil || strings.Contains(string(encoded), "private") || !strings.Contains(string(encoded), "connection refused") {
		t.Fatalf("queue retained raw value: %s %v", encoded, err)
	}
}

func TestReportExceptionSendsOriginalSanitizedDetailsAndDistinctFailures(t *testing.T) {
	messages := []string{
		"dial tcp: connection refused for private@example.com",
		"database transaction serialization failure",
		"OAuth failed token=ya29.a0AfH6SMprivatecredential",
		"OAuth failed client_secret=GOCSPX-privatecredential",
		"invalid Resend key re_privatecredential0123456789",
		"invalid Webhook key whsec_privatecredentialbase64+/==",
	}
	wire := make(chan []byte, len(messages))
	r := newReporter(context.Background(), true, func(context.Context) (Config, error) { return testConfig, nil }, nil, "worker", transportFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		wire <- body
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	}), time.Hour)
	defer r.Close()
	waitConfig(t, r)
	for _, message := range messages {
		r.ReportException("cycrio-system", "worker_check", "task_failed", errors.New(message))
	}
	for i := 0; i < len(messages); i++ {
		select {
		case body := <-wire:
			var envelope struct {
				Batch []struct {
					Properties struct {
						Items []struct {
							Type       string `json:"type"`
							Value      string `json:"value"`
							Stacktrace struct {
								Frames []map[string]any `json:"frames"`
							} `json:"stacktrace"`
						} `json:"$exception_list"`
					} `json:"properties"`
				} `json:"batch"`
			}
			if err := json.Unmarshal(body, &envelope); err != nil {
				t.Fatal(err)
			}
			item := envelope.Batch[0].Properties.Items[0]
			if !strings.Contains(item.Type, "errorString") || item.Value == "task_failed" || len(item.Stacktrace.Frames) == 0 || strings.Contains(string(body), "private") {
				t.Fatalf("original error details missing or private: %s", body)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("different errors were suppressed")
		}
	}
}

func TestSafeFrameKeepsStandardAndDependencyPackageDetails(t *testing.T) {
	for _, frame := range []runtime.Frame{
		{Function: "net/http.(*Client).Do", File: "/usr/local/go/src/net/http/client.go", Line: 123},
		{Function: "github.com/jackc/pgx/v5.(*Conn).Exec", File: "/go/pkg/mod/github.com/jackc/pgx/v5@v5.11.0/conn.go", Line: 45},
	} {
		got, ok := safeFrame(frame)
		if !ok || got.Function != frame.Function || !strings.HasSuffix(got.Filename, ".go") || strings.HasPrefix(got.Filename, "/") {
			t.Fatalf("technical external frame lost: %+v", got)
		}
	}
}

func TestSafeFrameKeepsInstrumentationWithoutMarkingItAsApplicationOrigin(t *testing.T) {
	for _, fixture := range []struct {
		function, file string
		inApp          bool
	}{
		{"github.com/enjoydarts/cycrio/internal/telemetry.(*Reporter).ReportException", "github.com/enjoydarts/cycrio/internal/telemetry/reporter.go", false},
		{"github.com/enjoydarts/cycrio/internal/httpapi.telemetryBoundary.func1", "github.com/enjoydarts/cycrio/internal/httpapi/telemetry.go", false},
		{"main.captureWorkerTask", "github.com/enjoydarts/cycrio/cmd/worker/telemetry.go", false},
		{"github.com/enjoydarts/cycrio/internal/httpapi.handleSubscriptions", "github.com/enjoydarts/cycrio/internal/httpapi/subscriptions.go", true},
		{"github.com/enjoydarts/cycrio/internal/mailprocessing.(*Service).Execute", "github.com/enjoydarts/cycrio/internal/mailprocessing/service.go", true},
		{"main.executeMailProcessing", "github.com/enjoydarts/cycrio/cmd/worker/mail_processing.go", true},
	} {
		frame, ok := safeFrame(runtime.Frame{Function: fixture.function, File: fixture.file, Line: 42})
		if !ok || frame.Filename == "" || frame.Function == "" || frame.LineNo != 42 || frame.InApp != fixture.inApp {
			t.Fatalf("application origin marker incorrect: fixture=%+v frame=%+v kept=%v", fixture, frame, ok)
		}
	}
}

func triggerBoundsPanic(index int) { values := []int{1}; _ = values[index] }

func TestReportExceptionKeepsOriginalRuntimePanicTypeAndThrowingFrame(t *testing.T) {
	wire := make(chan []byte, 1)
	r := newReporter(context.Background(), true, func(context.Context) (Config, error) { return testConfig, nil }, nil, "worker", transportFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		wire <- body
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	}), time.Hour)
	defer r.Close()
	waitConfig(t, r)
	func() {
		defer func() {
			if value := recover(); value != nil {
				r.ReportException("cycrio-system", "worker_check", "task_panic", value)
			}
		}()
		triggerBoundsPanic(2)
	}()
	select {
	case body := <-wire:
		for _, technical := range []string{"runtime.boundsError", "index out of range [2] with length 1", "triggerBoundsPanic", "runtime.gopanic"} {
			if !strings.Contains(string(body), technical) {
				t.Fatalf("runtime panic technical detail %q missing: %s", technical, body)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runtime panic was not sent")
	}
}
