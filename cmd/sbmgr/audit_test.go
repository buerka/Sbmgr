package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestMutationsAreAuditedAndSecretsRedacted(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	s := &State{Users: []User{{Name: "alice", Enabled: true, Nodes: []Node{{Name: "ATT", AuthUser: "alice", UUID: "11111111-1111-4111-8111-111111111111"}}}}}
	if err := saveState(statePath, s); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.trafficCmd([]string{"add", "alice", "--upload", "1"}); err != nil {
		t.Fatal(err)
	}
	records, err := readAuditRecords(statePath, 10)
	if err != nil || len(records) != 1 {
		t.Fatalf("audit records=%#v err=%v", records, err)
	}
	if records[0].Action != "traffic.add" || !strings.Contains(strings.Join(records[0].Args, " "), "alice") {
		t.Fatalf("bad audit record: %#v", records[0])
	}
	redacted := sanitizeAuditArgs([]string{"set", "--webhook-secret", "top-secret", "--uuid=credential", "--password", "proxy-secret", "--outbound-password=another-secret", "ok"})
	text := strings.Join(redacted, " ")
	if strings.Contains(text, "top-secret") || strings.Contains(text, "credential") || strings.Contains(text, "proxy-secret") || strings.Contains(text, "another-secret") || strings.Count(text, "[REDACTED]") != 4 {
		t.Fatalf("secret audit args leaked: %q", text)
	}
}

func TestReadOnlyCommandsDoNotPolluteAudit(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(statePath, &State{Users: []User{{Name: "alice"}}}); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.userCmd([]string{"list"}); err != nil {
		t.Fatal(err)
	}
	records, err := readAuditRecords(statePath, 10)
	if err != nil || len(records) != 0 {
		t.Fatalf("read-only command was audited: %#v err=%v", records, err)
	}
}
