package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDryRunDoesNotNeedDatabase(t *testing.T) {
	var b bytes.Buffer
	if err := run(nil, &b); err != nil || !strings.Contains(b.String(), "128") || !strings.Contains(b.String(), "dry-run") {
		t.Fatalf("%s %v", b.String(), err)
	}
}
func TestExecuteRequiresActorBeforeIO(t *testing.T) {
	var b bytes.Buffer
	if err := run([]string{"-execute"}, &b); err == nil || err.Error() != "execute_requires_actor_uuid" {
		t.Fatal(err)
	}
}
func TestReportExclusivePrivateAndLocal(t *testing.T) {
	base := filepath.Join(t.TempDir(), ".local")
	if err := os.Mkdir(base, 0700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(base, "result.json")
	f, err := openReport(base, name)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	st, _ := os.Stat(name)
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
	if f, e := openReport(base, name); e == nil {
		_ = f.Close()
		t.Fatal("overwritten")
	}
	if f, e := openReport(base, filepath.Join(base, "..", "escape.json")); e == nil {
		_ = f.Close()
		t.Fatal("escaped")
	}
}
