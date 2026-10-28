package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureRejectsMissingServiceEvenWhenOtherServicesHaveReplicas(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	bin := filepath.Join(directory, "bin")
	if os.Mkdir(bin, 0700) != nil {
		t.Fatal("fake Docker fixture")
	}
	// Aggregate ps has three IDs despite the missing api. No real Docker runs.
	fake := `#!/bin/sh
case "$*" in
 'compose ps --status running -q api worker hatchet') printf 'worker1\nworker2\nhatchet1\n' ;;
 'compose ps --status running -q api') exit 0 ;;
 *'ps --status running'*) printf 'existing-container\n' ;;
 *) exit 0 ;;
esac
`
	if os.WriteFile(filepath.Join(bin, "docker"), []byte(fake), 0700) != nil {
		t.Fatal("fake Docker fixture")
	}
	command := exec.Command("sh", filepath.Join(root, "deploy/local/evaluate-hatchet.sh"))
	command.Dir = directory
	command.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "must all be running") {
		t.Fatal("missing service accepted")
	}
	complete, _ := filepath.Glob(filepath.Join(directory, ".local/evaluation/hatchet-*/complete.json"))
	if len(complete) != 0 {
		t.Fatal("missing service produced success artifact")
	}
}
