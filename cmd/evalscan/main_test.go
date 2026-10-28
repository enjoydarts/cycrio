package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enjoydarts/cycrio/internal/evaluation"
)

type failedInput struct{}

func (failedInput) Read([]byte) (int, error) { return 0, errors.New("private_reader_error") }
func TestScanCommandReportsOnlySuccessfulMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probes.json")
	if evaluation.WriteProbes(path, []evaluation.Probe{{ID: "body", Value: []byte("sensitive_marker")}}) != nil {
		t.Fatal("probe fixture")
	}
	var out bytes.Buffer
	if err := scan([]string{"-probes", path, "-scope", "evaluation_worker"}, strings.NewReader("safe worker metadata"), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"passed":true`) || !strings.Contains(out.String(), `"bytes":20`) {
		t.Fatal("missing metadata", out.String())
	}
	for _, input := range []string{"sensitive_marker", "c2Vuc2l0aXZlX21hcmtlcg==", ""} {
		out.Reset()
		err := scan([]string{"-probes", path, "-scope", "evaluation_worker"}, strings.NewReader(input), &out)
		if err == nil || out.Len() != 0 || strings.Contains(err.Error(), "sensitive_marker") {
			t.Fatal("unsafe scan failure")
		}
	}
	out.Reset()
	err := scan([]string{"-probes", path, "-scope", "docker_services"}, failedInput{}, &out)
	if err == nil || strings.Contains(err.Error(), "private_reader_error") || out.Len() != 0 {
		t.Fatal("reader error exposed")
	}
}
func TestScanCommandRejectsMissingOrUnrecognizedScope(t *testing.T) {
	for _, args := range [][]string{nil, {"-scope", "all_private_data"}, {"-probes", "missing", "-scope", "docker_services"}} {
		var out bytes.Buffer
		if scan(args, strings.NewReader("safe"), &out) == nil || out.Len() != 0 {
			t.Fatal("invalid arguments accepted")
		}
	}
}

func TestDockerCaptureCanExplicitlyReportNoLogLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probes.json")
	if evaluation.WriteProbes(path, []evaluation.Probe{{ID: "body", Value: []byte("synthetic_marker")}}) != nil {
		t.Fatal("probe fixture")
	}
	var out bytes.Buffer
	if err := scan([]string{"-probes", path, "-scope", "docker_services", "-allow-empty"}, strings.NewReader(""), &out); err != nil {
		t.Fatal("explicit empty Docker capture rejected")
	}
	if !strings.Contains(out.String(), `"bytes":0`) || !strings.Contains(out.String(), `"empty":true`) {
		t.Fatal("empty capture not disclosed")
	}
	out.Reset()
	if scan([]string{"-probes", path, "-scope", "evaluation_worker", "-allow-empty"}, strings.NewReader(""), &out) == nil || out.Len() != 0 {
		t.Fatal("empty evaluation worker capture accepted")
	}
}
