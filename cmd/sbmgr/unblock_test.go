package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUserUnblockAllClearsOnlyRuntimeBlockAndWritesAudit(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	until := time.Now().Add(time.Hour).Format(time.RFC3339Nano)
	policy := BurstPolicy{Enabled: true, WindowMinutes: 30, LimitBytes: 2 << 30, BlockMinutes: 20}
	s := &State{Version: stateVersion, Users: []User{
		{Name: "alice", Enabled: true, Burst: policy, BlockedUntil: until, BlockReason: "threshold", TrafficSamples: []TrafficSample{{At: time.Now().Format(time.RFC3339Nano), Bytes: 123}}},
		{Name: "bob", Enabled: true, Burst: policy, BlockedUntil: until, BlockReason: "threshold"},
		{Name: "carol", Enabled: true, Burst: policy},
	}}
	if err := saveState(statePath, s); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	a := &app{statePath: statePath, out: &output, err: &output}
	if err := a.userCmd([]string{"unblock", "--all"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alice", "bob"} {
		u := findUser(loaded, name)
		if u.BlockedUntil != "" || u.BlockReason != "" || len(u.TrafficSamples) != 0 {
			t.Fatalf("runtime block not cleared for %s: %#v", name, *u)
		}
		if u.Burst != policy || !u.Enabled {
			t.Fatalf("policy or enabled state changed for %s: %#v", name, *u)
		}
	}
	if len(loaded.Alerts) != 2 || loaded.Alerts[0].Kind != "burst_unblocked_manual" {
		t.Fatalf("manual unblock alerts missing: %#v", loaded.Alerts)
	}
	if !loaded.BurstApplyPending {
		t.Fatal("unblock without --apply did not schedule an automatic apply retry")
	}
	if !strings.Contains(output.String(), "已解除 2 个用户") || !strings.Contains(output.String(), "异常保护规则保持不变") {
		t.Fatalf("unexpected output: %q", output.String())
	}
	auditData, err := os.ReadFile(auditPath(statePath))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(auditData, []byte(`"action":"user.unblock"`)) {
		t.Fatalf("unblock audit record missing: %s", auditData)
	}
}

func TestUserUnblockSpecificRejectsUnknownAndLeavesOthersBlocked(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	until := time.Now().Add(time.Hour).Format(time.RFC3339Nano)
	if err := saveState(statePath, &State{Version: stateVersion, Users: []User{{Name: "alice", BlockedUntil: until}, {Name: "bob", BlockedUntil: until}}}); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: new(bytes.Buffer), err: new(bytes.Buffer)}
	if err := a.userCmd([]string{"unblock", "nobody"}); err == nil {
		t.Fatal("unknown user unexpectedly accepted")
	}
	if err := a.userCmd([]string{"unblock", "alice"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if findUser(loaded, "alice").BlockedUntil != "" || findUser(loaded, "bob").BlockedUntil == "" {
		t.Fatalf("specific unblock affected wrong users: %#v", loaded.Users)
	}
}
