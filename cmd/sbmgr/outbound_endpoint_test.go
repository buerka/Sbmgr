package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const outboundEndpointFixture = `{
  "log": {"level": "info", "timestamp": true},
  "inbounds": [{"type":"vless","tag":"vless-in","users":[]}],
  "outbounds": [
    {"type":"direct","tag":"direct"},
    {"type":"socks","tag":"to-relay-b","server":"relay-b.example","server_port":"8443","username":"unchanged","password":"old-relay-b-secret"},
    {"type":"shadowsocks","tag":"to-relay-a","server":"192.0.2.10","server_port":443,"method":"chacha20-ietf-poly1305","password":"keep-this-secret","udp_over_tcp":{"enabled":true}}
  ],
  "route": {"final":"direct","rules":[{"auth_user":["Relay A"],"action":"route","outbound":"to-relay-a"}]}
}`

func writeOutboundEndpointFixture(t *testing.T, raw string) (string, *State) {
	t.Helper()
	directory := t.TempDir()
	base := filepath.Join(directory, "config.base.json")
	if err := os.WriteFile(base, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return base, &State{BaseConfig: base}
}

func TestListOutboundEndpointsReturnsOnlyRemoteSummaries(t *testing.T) {
	_, state := writeOutboundEndpointFixture(t, outboundEndpointFixture)
	state.Users = []User{
		{Name: "alice", Nodes: []Node{{Outbound: "to-relay-a"}, {Outbound: "to-relay-a"}}},
		{Name: "bob", Nodes: []Node{{Outbound: "to-relay-b"}, {Outbound: ""}}},
	}
	got, err := listOutboundEndpoints(state)
	if err != nil {
		t.Fatal(err)
	}
	want := []OutboundEndpointSummary{
		{Tag: "to-relay-a", Type: "shadowsocks", Server: "192.0.2.10", Port: 443, UserCount: 1, NodeCount: 2},
		{Tag: "to-relay-b", Type: "socks", Server: "relay-b.example", Port: 8443, UserCount: 1, NodeCount: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("summaries = %#v, want %#v", got, want)
	}
	for _, endpoint := range got {
		serialized, _ := json.Marshal(endpoint)
		if bytes.Contains(serialized, []byte("password")) || bytes.Contains(serialized, []byte("keep-this-secret")) {
			t.Fatalf("summary leaked credentials: %s", serialized)
		}
	}
}

func TestWriteOutboundEndpointBacksUpAndChangesOnlyTargetFields(t *testing.T) {
	base, state := writeOutboundEndpointFixture(t, outboundEndpointFixture)
	original, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 12, 34, 56, 123, time.UTC)
	change, err := writeOutboundEndpointBase(state, "to-relay-a", "relay-a-new.example", 9443, now)
	if err != nil {
		t.Fatal(err)
	}
	if !change.Changed || change.Applied || change.Before.Server != "192.0.2.10" || change.After.Server != "relay-a-new.example" || change.After.Port != 9443 {
		t.Fatalf("unexpected change: %#v", change)
	}
	if filepath.Dir(change.BackupPath) != filepath.Join(filepath.Dir(base), "backups") {
		t.Fatalf("backup path = %q", change.BackupPath)
	}
	backup, err := os.ReadFile(change.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, original) {
		t.Fatal("backup is not an exact copy of the previous base config")
	}
	updated, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	if err := json.Unmarshal(original, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(updated, &after); err != nil {
		t.Fatal(err)
	}
	afterTarget := findRawOutbound(t, after, "to-relay-a")
	if afterTarget["server"] != "relay-a-new.example" || afterTarget["server_port"] != float64(9443) || afterTarget["password"] != "keep-this-secret" {
		t.Fatalf("target was not updated safely: %#v", afterTarget)
	}
	beforeTarget := findRawOutbound(t, before, "to-relay-a")
	afterTarget["server"] = beforeTarget["server"]
	afterTarget["server_port"] = beforeTarget["server_port"]
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("fields other than target server/server_port changed\nbefore=%#v\nafter=%#v", before, after)
	}

	beforeNoop := append([]byte(nil), updated...)
	noop, err := writeOutboundEndpointBase(state, "to-relay-a", "relay-a-new.example", 9443, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if noop.Changed || noop.BackupPath != "" {
		t.Fatalf("no-op created a mutation: %#v", noop)
	}
	afterNoop, _ := os.ReadFile(base)
	if !bytes.Equal(beforeNoop, afterNoop) {
		t.Fatal("no-op rewrote the base config")
	}
}

func TestWriteOutboundEndpointCredentialsPreservesSecretsAndUnrelatedFields(t *testing.T) {
	base, state := writeOutboundEndpointFixture(t, outboundEndpointFixture)
	username := "new-account"
	password := " p:@\"\\密🔑 "
	change, err := writeOutboundEndpointBaseWithCredentials(state, "to-relay-b", "relay-b.example", 8443, OutboundCredentialUpdate{
		Username: &username,
		Password: &password,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !change.Changed || !change.UsernameChanged || !change.PasswordChanged || change.BackupPath == "" {
		t.Fatalf("credential-only change was not recorded: %#v", change)
	}
	var config map[string]any
	raw, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	target := findRawOutbound(t, config, "to-relay-b")
	if target["username"] != username || target["password"] != password || target["server"] != "relay-b.example" || target["server_port"] != float64(8443) {
		t.Fatalf("SOCKS credentials were not updated exactly: %#v", target)
	}
	relayA := findRawOutbound(t, config, "to-relay-a")
	if relayA["password"] != "keep-this-secret" || relayA["method"] != "chacha20-ietf-poly1305" {
		t.Fatalf("unrelated outbound changed: %#v", relayA)
	}

	noop, err := writeOutboundEndpointBaseWithCredentials(state, "to-relay-b", "relay-b.example", 8443, OutboundCredentialUpdate{
		Username: &username,
		Password: &password,
	}, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if noop.Changed || noop.BackupPath != "" || noop.UsernameChanged || noop.PasswordChanged {
		t.Fatalf("same credentials were not treated as a no-op: %#v", noop)
	}
}

func TestOutboundCredentialValidationAndClearing(t *testing.T) {
	base, state := writeOutboundEndpointFixture(t, outboundEndpointFixture)
	username := ""
	change, err := writeOutboundEndpointBaseWithCredentials(state, "to-relay-b", "relay-b.example", 8443, OutboundCredentialUpdate{Username: &username}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !change.UsernameChanged || change.PasswordChanged {
		t.Fatalf("username clear flags = %#v", change)
	}
	var config map[string]any
	raw, _ := os.ReadFile(base)
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if _, exists := findRawOutbound(t, config, "to-relay-b")["username"]; exists {
		t.Fatal("cleared username field is still present")
	}

	unsupported := "not-allowed"
	if _, err := writeOutboundEndpointBaseWithCredentials(state, "to-relay-a", "192.0.2.10", 443, OutboundCredentialUpdate{Username: &unsupported}, time.Now()); err == nil {
		t.Fatal("Shadowsocks username update was accepted")
	}
	empty := ""
	if _, err := writeOutboundEndpointBaseWithCredentials(state, "to-relay-a", "192.0.2.10", 443, OutboundCredentialUpdate{Password: &empty}, time.Now()); err == nil {
		t.Fatal("empty Shadowsocks password was accepted")
	}
}

func TestSetOutboundEndpointTransactionApplyFailure(t *testing.T) {
	base, state := writeOutboundEndpointFixture(t, outboundEndpointFixture)
	original, _ := os.ReadFile(base)
	applyError := errors.New("candidate rejected")
	applySawCandidate := false
	restartSeen := false
	change, err := setOutboundEndpointOnState(state, "to-relay-a", "203.0.113.8", 443, true, nil, func(_ *State, _ bool, restart bool, _ io.Writer) error {
		restartSeen = restart
		raw, readErr := os.ReadFile(base)
		if readErr != nil {
			return readErr
		}
		applySawCandidate = strings.Contains(string(raw), "203.0.113.8")
		return applyError
	}, time.Now())
	if !errors.Is(err, applyError) || !strings.Contains(err.Error(), "基础模板已恢复") {
		t.Fatalf("error = %v", err)
	}
	if !change.Changed || change.Applied || !applySawCandidate || restartSeen {
		t.Fatalf("transaction state = %#v candidateSeen=%v restart=%v", change, applySawCandidate, restartSeen)
	}
	restored, _ := os.ReadFile(base)
	if !bytes.Equal(restored, original) {
		t.Fatal("apply failure did not restore the exact previous base config")
	}
	if _, statErr := os.Stat(change.BackupPath); statErr != nil {
		t.Fatalf("rollback backup was not retained: %v", statErr)
	}
}

func TestCredentialChangeForcesRestartAndRollsBackOnApplyFailure(t *testing.T) {
	base, state := writeOutboundEndpointFixture(t, outboundEndpointFixture)
	original, _ := os.ReadFile(base)
	password := "replacement-secret"
	restartSeen := false
	change, err := setOutboundEndpointOnStateWithCredentials(state, "to-relay-b", "relay-b.example", 8443, OutboundCredentialUpdate{Password: &password}, true, nil, func(_ *State, _, restart bool, _ io.Writer) error {
		restartSeen = restart
		return errors.New("restart rejected")
	}, time.Now())
	if err == nil || !restartSeen || !change.PasswordChanged || change.Applied {
		t.Fatalf("credential failure transaction = %#v restart=%v err=%v", change, restartSeen, err)
	}
	restored, _ := os.ReadFile(base)
	if !bytes.Equal(restored, original) {
		t.Fatal("credential apply failure did not restore exact base config")
	}
}

func TestOutboundRollbackRefusesToOverwriteConcurrentBaseEdit(t *testing.T) {
	base, state := writeOutboundEndpointFixture(t, outboundEndpointFixture)
	concurrent := []byte(`{"outbounds":[{"type":"socks","tag":"external","server":"external.example","server_port":1080}]}`)
	change, err := setOutboundEndpointOnState(state, "to-relay-a", "203.0.113.8", 443, true, nil, func(_ *State, _, _ bool, _ io.Writer) error {
		if writeErr := os.WriteFile(base, concurrent, 0600); writeErr != nil {
			return writeErr
		}
		return errors.New("apply failed after concurrent edit")
	}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "其他进程修改") || !change.Changed {
		t.Fatalf("concurrent rollback result = %#v err=%v", change, err)
	}
	current, _ := os.ReadFile(base)
	if !bytes.Equal(current, concurrent) {
		t.Fatalf("rollback overwrote concurrent edit:\n%s", current)
	}
}

func TestOutboundEndpointValidationRejectsUnsafeInputsWithoutMutation(t *testing.T) {
	tests := []struct {
		name   string
		tag    string
		server string
		port   int
	}{
		{name: "empty tag", tag: "", server: "example.com", port: 443},
		{name: "tag whitespace", tag: "to relay-a", server: "example.com", port: 443},
		{name: "unknown tag", tag: "missing", server: "example.com", port: 443},
		{name: "local direct is not remote", tag: "direct", server: "example.com", port: 443},
		{name: "url", tag: "to-relay-a", server: "https://example.com", port: 443},
		{name: "host and port", tag: "to-relay-a", server: "example.com:443", port: 443},
		{name: "invalid ipv4", tag: "to-relay-a", server: "999.1.1.1", port: 443},
		{name: "bad hostname label", tag: "to-relay-a", server: "-bad.example", port: 443},
		{name: "zero port", tag: "to-relay-a", server: "example.com", port: 0},
		{name: "large port", tag: "to-relay-a", server: "example.com", port: 65536},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base, state := writeOutboundEndpointFixture(t, outboundEndpointFixture)
			original, _ := os.ReadFile(base)
			if _, err := writeOutboundEndpointBase(state, test.tag, test.server, test.port, time.Now()); err == nil {
				t.Fatal("unsafe update was accepted")
			}
			after, _ := os.ReadFile(base)
			if !bytes.Equal(after, original) {
				t.Fatal("rejected update modified the base config")
			}
			entries, err := os.ReadDir(filepath.Join(filepath.Dir(base), "backups"))
			if err == nil && len(entries) != 0 {
				t.Fatalf("rejected update created backups: %#v", entries)
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		})
	}
}

func TestOutboundEndpointAcceptsIPv6AndRejectsDuplicateTags(t *testing.T) {
	_, state := writeOutboundEndpointFixture(t, outboundEndpointFixture)
	if _, err := writeOutboundEndpointBase(state, "to-relay-a", "2001:db8::10", 443, time.Now()); err != nil {
		t.Fatalf("valid IPv6 was rejected: %v", err)
	}
	duplicate := `{"outbounds":[{"type":"socks","tag":"dup","server":"one.example","server_port":1},{"type":"socks","tag":"dup","server":"two.example","server_port":2}]}`
	base, duplicateState := writeOutboundEndpointFixture(t, duplicate)
	original, _ := os.ReadFile(base)
	if _, err := listOutboundEndpoints(duplicateState); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("duplicate tags were accepted by list: %v", err)
	}
	if _, err := writeOutboundEndpointBase(duplicateState, "dup", "new.example", 3, time.Now()); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("duplicate tags were accepted by update: %v", err)
	}
	after, _ := os.ReadFile(base)
	if !bytes.Equal(after, original) {
		t.Fatal("duplicate-tag rejection modified the base config")
	}
}

func TestListOutboundEndpointsCountsRouteFinalUsers(t *testing.T) {
	raw := `{"outbounds":[{"type":"socks","tag":"landing","server":"landing.example","server_port":443}],"route":{"final":"landing"}}`
	_, state := writeOutboundEndpointFixture(t, raw)
	state.Users = []User{{Name: "alice", Nodes: []Node{{Outbound: ""}, {Outbound: "landing"}}}, {Name: "bob", Nodes: []Node{{Outbound: ""}}}}
	endpoints, err := listOutboundEndpoints(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 || endpoints[0].UserCount != 2 || endpoints[0].NodeCount != 3 {
		t.Fatalf("route.final references were not counted: %#v", endpoints)
	}
}

func TestAppSetOutboundEndpointUsesStateLockAndAuditsOldAndNewTargets(t *testing.T) {
	directory := t.TempDir()
	base := filepath.Join(directory, "config.base.json")
	if err := os.WriteFile(base, []byte(outboundEndpointFixture), 0600); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(directory, "state.json")
	state := &State{Version: stateVersion, BaseConfig: base, ConfigPath: filepath.Join(directory, "sing-box.json"), InboundTag: "vless-in"}
	if err := saveState(statePath, state); err != nil {
		t.Fatal(err)
	}
	application := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	change, err := application.setOutboundEndpoint("to-relay-a", "new-relay-a.example", 9443, false)
	if err != nil {
		t.Fatal(err)
	}
	if !change.Changed || change.Applied || application.stateLockHeld {
		t.Fatalf("unexpected app transaction: change=%#v lockHeld=%v", change, application.stateLockHeld)
	}
	records, err := readAuditRecords(statePath, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Action != "outbound.endpoint.update" {
		t.Fatalf("audit record missing: %#v", records)
	}
	joined := strings.Join(records[0].Args, " ")
	if !strings.Contains(joined, "192.0.2.10:443") || !strings.Contains(joined, "new-relay-a.example:9443") || strings.Contains(joined, "keep-this-secret") {
		t.Fatalf("audit did not safely record before/after targets: %#v", records[0].Args)
	}
}

func TestAppCredentialAuditNeverContainsValues(t *testing.T) {
	directory := t.TempDir()
	base := filepath.Join(directory, "config.base.json")
	if err := os.WriteFile(base, []byte(outboundEndpointFixture), 0600); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(directory, "state.json")
	state := &State{Version: stateVersion, BaseConfig: base, ConfigPath: filepath.Join(directory, "sing-box.json"), InboundTag: "vless-in"}
	if err := saveState(statePath, state); err != nil {
		t.Fatal(err)
	}
	username := "new-private-account"
	password := "new-private-password"
	application := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	change, err := application.setOutboundEndpointWithCredentials("to-relay-b", "relay-b.example", 8443, OutboundCredentialUpdate{Username: &username, Password: &password}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !change.UsernameChanged || !change.PasswordChanged {
		t.Fatalf("credential flags missing: %#v", change)
	}
	records, err := readAuditRecords(statePath, 10)
	if err != nil {
		t.Fatal(err)
	}
	serialized, _ := json.Marshal(records)
	for _, secret := range []string{"old-relay-b-secret", username, password} {
		if bytes.Contains(serialized, []byte(secret)) {
			t.Fatalf("audit leaked credential %q: %s", secret, serialized)
		}
	}
	if !bytes.Contains(serialized, []byte("--username-change")) || !bytes.Contains(serialized, []byte("--password-change")) {
		t.Fatalf("audit omitted non-secret change metadata: %s", serialized)
	}
}

func findRawOutbound(t *testing.T, config map[string]any, tag string) map[string]any {
	t.Helper()
	items, ok := config["outbounds"].([]any)
	if !ok {
		t.Fatal("outbounds missing")
	}
	for _, item := range items {
		outbound, _ := item.(map[string]any)
		if outbound["tag"] == tag {
			return outbound
		}
	}
	t.Fatalf("outbound %q missing", tag)
	return nil
}
