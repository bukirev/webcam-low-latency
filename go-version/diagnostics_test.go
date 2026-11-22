package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCrashEvidenceSurvivesBackgroundPanic(t *testing.T) {
	if os.Getenv("WEBCAM_GO_TEST_CRASH") == "1" {
		_ = startDiagnostics()
		go func() { panic("diagnostic worker test") }()
		select {}
	}
	dir := t.TempDir()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(exe, "-test.run=^TestCrashEvidenceSurvivesBackgroundPanic$")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "WEBCAM_GO_TEST_CRASH=1")
	if e := cmd.Run(); e == nil {
		t.Fatal("expected fatal worker panic")
	}
	files, e := filepath.Glob(filepath.Join(dir, "logs", "session-*.log"))
	if e != nil || len(files) != 1 {
		t.Fatal("crash evidence missing", files, e)
	}
	b, e := os.ReadFile(files[0])
	if e != nil || !strings.Contains(string(b), "panic: diagnostic worker test") || !strings.Contains(string(b), "diagnostics_test.go") {
		t.Fatalf("fatal stack not captured: %s %v", b, e)
	}
}
