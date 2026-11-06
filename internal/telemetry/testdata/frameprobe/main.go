// This executable checks symbol names and exception serialization using the
// production compiler flags without contacting an external service.
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/enjoydarts/cycrio/internal/telemetry"
)

type transport struct{ wire chan []byte }

func (t transport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	t.wire <- body
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
}

func main() {
	pc, file, line, _ := runtime.Caller(0)
	wire := make(chan []byte, 1)
	http.DefaultTransport = transport{wire: wire}
	r := telemetry.New(context.Background(), true, func(context.Context) (telemetry.Config, error) {
		return telemetry.Config{ProjectToken: "phc_probe12345678", Host: "https://us.i.posthog.com"}, nil
	}, nil, "worker")
	defer r.Close()
	deadline := time.Now().Add(3 * time.Second)
	for r.PublicConfig(context.Background()).ProjectToken == "" {
		if time.Now().After(deadline) {
			os.Exit(1)
		}
		time.Sleep(time.Millisecond)
	}
	r.Report("cycrio-system", "worker_check", "task_failed")
	select {
	case body := <-wire:
		_ = json.NewEncoder(os.Stdout).Encode(struct {
			Function string          `json:"function"`
			File     string          `json:"file"`
			Line     int             `json:"line"`
			Wire     json.RawMessage `json:"wire"`
		}{runtime.FuncForPC(pc).Name(), file, line, body})
	case <-time.After(3 * time.Second):
		os.Exit(1)
	}
}
