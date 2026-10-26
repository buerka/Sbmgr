package main

import (
	"io"
	"strings"
	"testing"
)

func TestAppVersionCanBeInjectedForDisplay(t *testing.T) {
	originalVersion, originalCommit := appVersion, gitCommit
	t.Cleanup(func() { appVersion, gitCommit = originalVersion, originalCommit })
	appVersion = "git-test-build"
	gitCommit = "0123456789abcdef"

	var out strings.Builder
	a := &app{out: &out, err: io.Discard}
	if err := a.run([]string{"version"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "sbmgr git-test-build" {
		t.Fatalf("version output = %q", got)
	}
	out.Reset()
	if err := a.run([]string{"version", "--verbose"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "sbmgr git-test-build\ncommit 0123456789abcdef" {
		t.Fatalf("verbose version output = %q", got)
	}
	if err := a.run([]string{"version", "unexpected"}); err == nil || !strings.Contains(err.Error(), "version [--verbose]") {
		t.Fatalf("unexpected version argument was accepted: %v", err)
	}
}
