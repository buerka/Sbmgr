package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVersionTwoMigratesNodesToDefaultDevice(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	raw := `{"version":2,"users":[{"name":"alice","enabled":true,"nodes":[{"name":"ATT","auth_user":"alice:att","uuid":"11111111-1111-4111-8111-111111111111"}]}]}`
	if err := os.WriteFile(statePath, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if s.Version != stateVersion || len(s.Users[0].Devices) != 1 || s.Users[0].Devices[0].Name != defaultDeviceName {
		t.Fatalf("device migration failed: %#v", s.Users[0])
	}
	if s.Users[0].Nodes[0].Device != defaultDeviceName || s.Users[0].Nodes[0].UUID != "11111111-1111-4111-8111-111111111111" || s.Users[0].Nodes[0].AuthUser != "alice:att" {
		t.Fatalf("legacy credentials changed: %#v", s.Users[0].Nodes[0])
	}
}

func TestDeviceIPPolicyOnlyTargetsThatDevice(t *testing.T) {
	now := time.Now()
	s := &State{Users: []User{{Name: "alice", Enabled: true, Devices: []Device{
		{Name: "电脑", Enabled: true, IPPolicy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "manual", MaxIPs: 1, BoundIPs: []string{"203.0.113.8"}}},
		{Name: "手机", Enabled: true},
	}, Nodes: []Node{
		{Name: "ATT", Device: "电脑", AuthUser: "alice-pc", UUID: "11111111-1111-4111-8111-111111111111"},
		{Name: "ATT", Device: "手机", AuthUser: "alice-phone", UUID: "22222222-2222-4222-8222-222222222222"},
	}}}}
	raw, _ := json.Marshal(ipRestrictionRules(s, now))
	if !strings.Contains(string(raw), "alice-pc") || !strings.Contains(string(raw), "203.0.113.8") || strings.Contains(string(raw), "alice-phone") {
		t.Fatalf("device IP rule leaked to another device: %s", raw)
	}
	device := findDevice(&s.Users[0], "电脑")
	device.IPPolicy = IPPolicy{Enabled: true, Mode: "enforce", Binding: "auto", MaxIPs: 1}
	if !recordDeviceSourceIP(s, &s.Users[0], device, "ATT", "198.51.100.9", now) {
		t.Fatal("device source IP was not recorded")
	}
	if len(device.SourceIPs) != 1 || !containsIP(device.IPPolicy.BoundIPs, "198.51.100.9") || device.LastSeen == "" || !s.IPApplyPending {
		t.Fatalf("device IP auto-binding failed: %#v", device)
	}
}

func TestDeviceAddCreatesIndependentCredentialsAndRotateIsScoped(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	s := &State{Users: []User{{Name: "alice", Enabled: true, Devices: []Device{{Name: defaultDeviceName, Enabled: true}}, Nodes: []Node{
		{Name: "LAX", Device: defaultDeviceName, AuthUser: "alice:lax", UUID: "11111111-1111-4111-8111-111111111111", UploadMbps: 10},
		{Name: "ATT", Device: defaultDeviceName, AuthUser: "alice:att", UUID: "22222222-2222-4222-8222-222222222222", DownloadMbps: 20},
	}}}}
	if err := saveState(statePath, s); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.deviceCmd([]string{"add", "alice", "--name", "手机"}); err != nil {
		t.Fatal(err)
	}
	s, _ = loadState(statePath)
	u := &s.Users[0]
	phone := nodesForDevice(*u, "手机")
	if len(phone) != 2 {
		t.Fatalf("cloned nodes = %d", len(phone))
	}
	originalUUIDs := map[string]bool{}
	originalAuth := map[string]bool{}
	for _, node := range nodesForDevice(*u, defaultDeviceName) {
		originalUUIDs[node.UUID], originalAuth[node.AuthUser] = true, true
	}
	for _, node := range phone {
		if originalUUIDs[node.UUID] || originalAuth[node.AuthUser] || !validRateMark(node.RateMark) {
			t.Fatalf("device credential was reused: %#v", node)
		}
	}
	beforeDefault := nodesForDevice(*u, defaultDeviceName)[0].UUID
	beforePhone := phone[0].UUID
	if err := a.deviceCmd([]string{"rotate", "alice", "手机"}); err != nil {
		t.Fatal(err)
	}
	s, _ = loadState(statePath)
	if nodesForDevice(s.Users[0], defaultDeviceName)[0].UUID != beforeDefault {
		t.Fatal("rotating phone changed the default device UUID")
	}
	if nodesForDevice(s.Users[0], "手机")[0].UUID == beforePhone {
		t.Fatal("phone UUID was not rotated")
	}
}

func TestDisabledDeviceIsExcludedFromConfigAndDeviceExport(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "base.json")
	if err := os.WriteFile(base, []byte(`{"inbounds":[{"type":"vless","tag":"vless-in","users":[]}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct","rules":[]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := &State{BaseConfig: base, InboundTag: "vless-in", Client: ClientSettings{Server: "example.com", Port: 443, ServerName: "example.com", PublicKey: "pub", ShortID: "abcd"}, Users: []User{{
		Name: "alice", Enabled: true,
		Devices: []Device{{Name: "电脑", Enabled: true}, {Name: "手机", Enabled: false}},
		Nodes: []Node{
			{Name: "ATT", Device: "电脑", AuthUser: "alice-pc", UUID: "11111111-1111-4111-8111-111111111111"},
			{Name: "ATT", Device: "手机", AuthUser: "alice-phone", UUID: "22222222-2222-4222-8222-222222222222"},
		},
	}}}
	rendered, err := renderConfig(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), "alice-pc") || strings.Contains(string(rendered), "alice-phone") {
		t.Fatalf("disabled device leaked into sing-box config:\n%s", rendered)
	}
	exported, err := renderMihomoDevice(s, s.Users[0], "电脑")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(exported), "11111111-1111-4111-8111-111111111111") || strings.Contains(string(exported), "22222222-2222-4222-8222-222222222222") {
		t.Fatalf("device export was not isolated:\n%s", exported)
	}
	if _, err := renderMihomoDevice(s, s.Users[0], "手机"); err == nil {
		t.Fatal("disabled device was exportable")
	}
}

func TestNodeSelectionRequiresDeviceWhenNamesRepeat(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	s := &State{Users: []User{{Name: "alice", Enabled: true, Devices: []Device{{Name: "电脑", Enabled: true}, {Name: "手机", Enabled: true}}, Nodes: []Node{
		{Name: "ATT", Device: "电脑", AuthUser: "pc", UUID: "11111111-1111-4111-8111-111111111111"},
		{Name: "ATT", Device: "手机", AuthUser: "phone", UUID: "22222222-2222-4222-8222-222222222222"},
	}}}}
	if err := saveState(statePath, s); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.nodeCmd([]string{"set", "alice", "ATT", "--up-mbps", "8"}); err == nil || !strings.Contains(err.Error(), "--device") {
		t.Fatalf("ambiguous node did not require a device: %v", err)
	}
	if err := a.nodeCmd([]string{"set", "alice", "ATT", "--device", "手机", "--up-mbps", "8"}); err != nil {
		t.Fatal(err)
	}
	s, _ = loadState(statePath)
	node, err := findUserNode(&s.Users[0], "手机", "ATT")
	if err != nil || node.UploadMbps != 8 {
		t.Fatalf("target device node was not updated: node=%#v err=%v", node, err)
	}
	pc, _ := findUserNode(&s.Users[0], "电脑", "ATT")
	if pc.UploadMbps != 0 {
		t.Fatal(fmt.Sprintf("other device node was changed: %#v", pc))
	}
}
