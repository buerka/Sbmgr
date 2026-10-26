package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const managedProxyFixture = `{
  "inbounds": [{"type":"vless","tag":"vless-in","users":[]}],
  "outbounds": [
    {"type":"direct","tag":"direct"},
    {"type":"socks","tag":"remote","server":"proxy.example","server_port":1080,"username":"private-account","password":"private-password","x_future":{"enabled":true}}
  ],
  "endpoints": [
    {"type":"wireguard","tag":"wg-home","address":["10.0.0.2/32"],"private_key":"private-key-value","peers":[{"address":"wg.example","port":51820,"public_key":"public-key"}]}
  ],
  "route": {"final":"direct","rules":[]}
}`

func writeManagedProxyFixture(t *testing.T, raw string) (string, *State) {
	t.Helper()
	directory := t.TempDir()
	base := filepath.Join(directory, "config.base.json")
	if err := os.WriteFile(base, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return base, &State{BaseConfig: base}
}

func TestManagedProxyListReturnsFullIndependentRawButSafeSummary(t *testing.T) {
	_, state := writeManagedProxyFixture(t, managedProxyFixture)
	outbounds, err := listOutboundDocuments(state)
	if err != nil {
		t.Fatal(err)
	}
	endpoints, err := listEndpointDocuments(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(outbounds) != 2 || len(endpoints) != 1 || endpoints[0].Tag != "wg-home" || endpoints[0].Type != "wireguard" {
		t.Fatalf("unexpected documents: outbounds=%#v endpoints=%#v", outbounds, endpoints)
	}
	remote, err := getOutboundDocument(state, "remote")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"private-account", "private-password", "x_future"} {
		if !bytes.Contains(remote.RawJSON, []byte(value)) {
			t.Fatalf("full raw JSON omitted %q: %s", value, remote.RawJSON)
		}
	}
	serialized, _ := json.Marshal(remote)
	printed := fmt.Sprintf("%v / %#v", remote, remote)
	for _, secret := range []string{"private-account", "private-password"} {
		if bytes.Contains(serialized, []byte(secret)) || strings.Contains(printed, secret) {
			t.Fatalf("safe document representation leaked %q: json=%s text=%s", secret, serialized, printed)
		}
	}
	remote.RawJSON[0] = 'X'
	again, err := getOutboundDocument(state, "remote")
	if err != nil || len(again.RawJSON) == 0 || again.RawJSON[0] == 'X' {
		t.Fatalf("returned raw JSON aliases stored data: %q err=%v", again.RawJSON, err)
	}
	identity, err := validateManagedProxyJSON(ManagedProxyEndpoint, endpoints[0].RawJSON)
	if err != nil || identity.Tag != "wg-home" || identity.Type != "wireguard" {
		t.Fatalf("side-effect-free validation = %#v, %v", identity, err)
	}
}

func TestManagedProxyTagsAreUniqueAcrossOutboundsAndEndpoints(t *testing.T) {
	raw := `{"outbounds":[{"type":"direct","tag":"same"}],"endpoints":[{"type":"wireguard","tag":"same"}]}`
	_, state := writeManagedProxyFixture(t, raw)
	if _, err := listOutboundDocuments(state); err == nil || !strings.Contains(err.Error(), "全局唯一") {
		t.Fatalf("cross-collection duplicate accepted: %v", err)
	}
	invalids := [][]byte{
		[]byte(`{"type":"socks"}`),
		[]byte(`{"tag":"new"}`),
		[]byte(`{"type":"socks","tag":" "}`),
		[]byte(`{"type":"socks","tag":"sbmgr-rate-0010-user"}`),
		[]byte(`{"type":"socks","tag":"one","tag":"two"}`),
		[]byte(`{"type":"socks","tag":"one","transport":{"type":"ws","type":"grpc"}}`),
		[]byte(`[]`),
	}
	base, validState := writeManagedProxyFixture(t, managedProxyFixture)
	original, _ := os.ReadFile(base)
	for _, candidate := range invalids {
		if _, err := addOutboundOnState(validState, candidate, false, nil, nil, time.Now()); err == nil {
			t.Fatalf("invalid candidate accepted: %s", candidate)
		}
	}
	after, _ := os.ReadFile(base)
	if !bytes.Equal(after, original) {
		t.Fatal("rejected candidates changed the base config")
	}
}

func TestManagedProxyJSONStructuralLimits(t *testing.T) {
	nested := `{"type":"socks","tag":"deep","value":`
	for range maxManagedProxyJSONDepth + 2 {
		nested += `[`
	}
	nested += `0`
	for range maxManagedProxyJSONDepth + 2 {
		nested += `]`
	}
	nested += `}`
	if _, err := validateManagedProxyJSON(ManagedProxyOutbound, []byte(nested)); err == nil || !strings.Contains(err.Error(), "嵌套") {
		t.Fatalf("excessive JSON nesting accepted: %v", err)
	}
	oversized := bytes.Repeat([]byte{' '}, maxManagedProxyJSONBytes+1)
	if _, err := validateManagedProxyJSON(ManagedProxyOutbound, oversized); err == nil || !strings.Contains(err.Error(), "MiB") {
		t.Fatalf("oversized JSON accepted: %v", err)
	}
}

func TestAddAndReplaceArbitraryObjectsPreserveUnknownFields(t *testing.T) {
	base, state := writeManagedProxyFixture(t, `{"outbounds":[{"type":"direct","tag":"direct"}]}`)
	endpoint := []byte(`{
      "type":"tailscale",
      "tag":"tailnet",
      "state_directory":"/var/lib/tailscale",
      "future_option":{"nested":[1,true,"keep-me"]},
      "auth_key":"secret-auth-key"
    }`)
	added, err := addEndpointOnState(state, endpoint, false, nil, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !added.Changed || added.Applied || added.After.Tag != "tailnet" || added.After.Type != "tailscale" || added.BackupPath == "" {
		t.Fatalf("unexpected add result: %#v", added)
	}
	document, err := getEndpointDocument(state, "tailnet")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"future_option", "keep-me", "auth_key", "secret-auth-key"} {
		if !bytes.Contains(document.RawJSON, []byte(field)) {
			t.Fatalf("add lost unknown field/value %q: %s", field, document.RawJSON)
		}
	}
	replacement := []byte(`{"type":"tailscale","tag":"tailnet","state_directory":"/srv/tailscale","future_option":{"nested":[1,true,"keep-me"]},"auth_key":"replacement-secret","new_version_field":42}`)
	replaced, err := replaceEndpointOnState(state, "tailnet", replacement, false, nil, nil, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !replaced.Changed || replaced.Before.Type != "tailscale" || replaced.After.Type != "tailscale" {
		t.Fatalf("unexpected replacement: %#v", replaced)
	}
	updated, _ := os.ReadFile(base)
	for _, field := range []string{"future_option", "keep-me", "new_version_field", "replacement-secret"} {
		if !bytes.Contains(updated, []byte(field)) {
			t.Fatalf("replacement lost %q: %s", field, updated)
		}
	}
	noop, err := replaceEndpointOnState(state, "tailnet", replacement, false, nil, nil, time.Now().Add(2*time.Second))
	if err != nil || noop.Changed || noop.BackupPath != "" {
		t.Fatalf("exact replacement was not a no-op: %#v err=%v", noop, err)
	}
	rename := []byte(`{"type":"tailscale","tag":"renamed"}`)
	if _, err := replaceEndpointOnState(state, "tailnet", rename, false, nil, nil, time.Now()); err == nil || !strings.Contains(err.Error(), "不能把 tag") {
		t.Fatalf("unsafe rename accepted: %v", err)
	}
}

func TestManagedProxyDeleteRejectsEveryKnownReferenceKind(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		state func(*State)
	}{
		{name: "route final", raw: `{"outbounds":[{"type":"direct","tag":"target"}],"route":{"final":"target"}}`},
		{name: "route rule", raw: `{"outbounds":[{"type":"direct","tag":"target"}],"route":{"rules":[{"action":"route","outbound":"target"}]}}`},
		{name: "detour", raw: `{"outbounds":[{"type":"direct","tag":"target"},{"type":"socks","tag":"other","server":"x","server_port":1,"detour":"target"}]}`},
		{name: "selector array", raw: `{"outbounds":[{"type":"direct","tag":"target"},{"type":"selector","tag":"group","outbounds":["target"]}]}`},
		{name: "external ui download detour", raw: `{"outbounds":[{"type":"direct","tag":"target"}],"experimental":{"clash_api":{"external_ui_download_detour":"target"}}}`},
		{name: "preferred by string", raw: `{"outbounds":[{"type":"direct","tag":"target"}],"route":{"rules":[{"preferred_by":"target"}]}}`},
		{name: "preferred by array", raw: `{"outbounds":[{"type":"direct","tag":"target"}],"route":{"rules":[{"preferred_by":["target","other"]}]}}`},
		{name: "implicit first without route", raw: `{"outbounds":[{"type":"direct","tag":"target"},{"type":"direct","tag":"other"}]}`},
		{name: "implicit first with empty final", raw: `{"outbounds":[{"type":"direct","tag":"target"},{"type":"direct","tag":"other"}],"route":{"final":""}}`},
		{name: "state node", raw: `{"outbounds":[{"type":"direct","tag":"target"}]}`, state: func(state *State) {
			state.Users = []User{{Name: "alice", Nodes: []Node{{Name: "n", Outbound: "target"}}}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base, state := writeManagedProxyFixture(t, test.raw)
			if test.state != nil {
				test.state(state)
			}
			original, _ := os.ReadFile(base)
			change, err := deleteOutboundOnState(state, "target", false, nil, nil, time.Now())
			var inUse *ManagedProxyInUseError
			if !errors.As(err, &inUse) || len(inUse.References) == 0 || change.Changed || change.BackupPath != "" {
				t.Fatalf("reference was not rejected safely: change=%#v error=%v", change, err)
			}
			after, _ := os.ReadFile(base)
			if !bytes.Equal(after, original) {
				t.Fatal("referenced delete changed the base config")
			}
		})
	}
}

func TestManagedProxyDeleteUnreferencedEndpoint(t *testing.T) {
	base, state := writeManagedProxyFixture(t, managedProxyFixture)
	change, err := deleteEndpointOnState(state, "wg-home", false, nil, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !change.Changed || change.Before.Tag != "wg-home" || change.Before.Kind != ManagedProxyEndpoint || change.After.Tag != "" {
		t.Fatalf("unexpected delete: %#v", change)
	}
	if _, err := getEndpointDocument(state, "wg-home"); err == nil {
		t.Fatal("deleted endpoint is still present")
	}
	raw, _ := os.ReadFile(base)
	if bytes.Contains(raw, []byte("private-key-value")) {
		t.Fatal("deleted endpoint secret remains in current base config")
	}
	backup, _ := os.ReadFile(change.BackupPath)
	if !bytes.Contains(backup, []byte("private-key-value")) {
		t.Fatal("backup did not preserve exact previous config")
	}
}

func TestManagedProxyDeleteRejectsEndpointReferences(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "tailscale dns server", raw: `{"outbounds":[{"type":"direct","tag":"direct"}],"endpoints":[{"type":"tailscale","tag":"target"}],"dns":{"servers":[{"type":"tailscale","tag":"tail-dns","endpoint":"target"}]}}`},
		{name: "certificate provider", raw: `{"outbounds":[{"type":"direct","tag":"direct"}],"endpoints":[{"type":"tailscale","tag":"target"}],"certificate_providers":[{"type":"test","tag":"cert","endpoint":"target"}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base, state := writeManagedProxyFixture(t, test.raw)
			original, _ := os.ReadFile(base)
			change, err := deleteEndpointOnState(state, "target", false, nil, nil, time.Now())
			var inUse *ManagedProxyInUseError
			if !errors.As(err, &inUse) || len(inUse.References) == 0 || change.Changed || change.BackupPath != "" {
				t.Fatalf("endpoint reference was not rejected safely: change=%#v error=%v", change, err)
			}
			after, _ := os.ReadFile(base)
			if !bytes.Equal(after, original) {
				t.Fatal("referenced endpoint delete changed the base config")
			}
		})
	}
}

func TestManagedProxyApplyUsesRenderedCandidateAndRollsBack(t *testing.T) {
	base, state := writeManagedProxyFixture(t, managedProxyFixture)
	original, _ := os.ReadFile(base)
	replacement := []byte(`{"type":"socks","tag":"remote","server":"new.example","server_port":2080,"username":"new-user","password":"new-secret"}`)
	restartSeen := false
	candidateSeen := false
	applyErr := errors.New("sing-box rejected candidate")
	change, err := replaceOutboundOnState(state, "remote", replacement, true, io.Discard, func(_ *State, noReload, restart bool, _ io.Writer) error {
		restartSeen = restart && !noReload
		current, readErr := os.ReadFile(base)
		candidateSeen = readErr == nil && bytes.Contains(current, []byte("new.example"))
		return applyErr
	}, time.Now())
	if !errors.Is(err, applyErr) || !change.Changed || change.Applied || !restartSeen || !candidateSeen {
		t.Fatalf("apply failure state=%#v restart=%v candidate=%v error=%v", change, restartSeen, candidateSeen, err)
	}
	restored, _ := os.ReadFile(base)
	if !bytes.Equal(restored, original) {
		t.Fatal("failed apply did not restore exact original base config")
	}
	serialized, _ := json.Marshal(change)
	for _, secret := range []string{"new-user", "new-secret", "private-account", "private-password"} {
		if bytes.Contains(serialized, []byte(secret)) || strings.Contains(err.Error(), secret) {
			t.Fatalf("change/error leaked %q: change=%s error=%v", secret, serialized, err)
		}
	}
}

func TestManagedProxyRollbackRefusesConcurrentEdit(t *testing.T) {
	base, state := writeManagedProxyFixture(t, managedProxyFixture)
	concurrent := []byte(`{"outbounds":[{"type":"direct","tag":"external"}]}`)
	change, err := addOutboundOnState(state, []byte(`{"type":"direct","tag":"new"}`), true, nil, func(_ *State, _, _ bool, _ io.Writer) error {
		if writeErr := os.WriteFile(base, concurrent, 0600); writeErr != nil {
			return writeErr
		}
		return errors.New("apply failure after concurrent edit")
	}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "其他进程修改") || !change.Changed {
		t.Fatalf("concurrent rollback was not detected: change=%#v err=%v", change, err)
	}
	current, _ := os.ReadFile(base)
	if !bytes.Equal(current, concurrent) {
		t.Fatal("rollback overwrote a concurrent config edit")
	}
}

func TestManagedProxyAppAuditNeverReceivesRawJSON(t *testing.T) {
	directory := t.TempDir()
	base := filepath.Join(directory, "config.base.json")
	if err := os.WriteFile(base, []byte(`{"inbounds":[{"type":"vless","tag":"vless-in","users":[]}],"outbounds":[{"type":"direct","tag":"direct"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(directory, "state.json")
	state := &State{Version: stateVersion, BaseConfig: base, ConfigPath: filepath.Join(directory, "sing-box.json"), InboundTag: "vless-in"}
	if err := saveState(statePath, state); err != nil {
		t.Fatal(err)
	}
	application := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	secret := "credential-that-must-not-reach-audit"
	change, err := application.addOutboundJSON([]byte(`{"type":"socks","tag":"private-proxy","server":"example.test","server_port":1080,"username":"secret-user","password":"`+secret+`","future_secret":"also-private"}`), false)
	if err != nil || !change.Changed {
		t.Fatalf("app add failed: %#v %v", change, err)
	}
	records, err := readAuditRecords(statePath, 10)
	if err != nil {
		t.Fatal(err)
	}
	serialized, _ := json.Marshal(records)
	for _, value := range []string{secret, "secret-user", "also-private", "future_secret", "server_port"} {
		if bytes.Contains(serialized, []byte(value)) {
			t.Fatalf("audit leaked raw JSON fragment %q: %s", value, serialized)
		}
	}
	if !bytes.Contains(serialized, []byte("private-proxy")) || !bytes.Contains(serialized, []byte("socks")) {
		t.Fatalf("audit omitted safe identity metadata: %s", serialized)
	}
}
