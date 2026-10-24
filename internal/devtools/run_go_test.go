package devtools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunGoBuildsCheckoutOwnedByRunner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires the root development container to reproduce runner ownership")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	script, err := filepath.Abs("../../deploy/local/run-go.sh")
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "cmd", "api"), 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":          "module example.invalid/ci-fixture\n\ngo 1.27.3\n",
		"cmd/api/main.go": "package main\nimport (\"fmt\"; \"os\")\nfunc main(){fmt.Printf(\"%s|%s\",os.Args[1],os.Args[2])}\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var environment []string
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		switch key {
		case "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM", "DATABASE_URL_FILE", "HATCHET_CLIENT_TOKEN_FILE":
			continue
		}
		environment = append(environment, value)
	}
	environment = append(environment, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	run := func(name string, args ...string) ([]byte, error) {
		command := exec.CommandContext(ctx, name, args...)
		command.Dir = repo
		command.Env = environment
		return command.CombinedOutput()
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "."},
		{"-c", "user.name=CI fixture", "-c", "user.email=ci@example.invalid", "commit", "-qm", "fixture"},
	} {
		if output, err := run("git", args...); err != nil {
			t.Fatalf("fixture setup: %v: %s", err, output)
		}
	}
	// Hosted runners own the checkout, while Compose builds it as container root.
	if err := filepath.WalkDir(repo, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chown(path, 1001, 1001)
	}); err != nil {
		t.Fatal(err)
	}
	if output, err := run("git", "status", "--porcelain"); err == nil || !strings.Contains(string(output), "dubious ownership") {
		t.Fatalf("fixture must reproduce Git's ownership rejection: %v: %s", err, output)
	}
	output, err := run("sh", script, "api", "sentinel", "two words")
	if err != nil {
		t.Fatalf("development launcher must work without VCS metadata: %v: %s", err, output)
	}
	if string(output) != "sentinel|two words" {
		t.Fatalf("launcher must execute the binary with original arguments: %q", output)
	}
}
