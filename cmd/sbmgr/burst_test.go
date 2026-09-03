package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBurstPolicyUsesRollingWindowAndBlocks(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	s := &State{Users: []User{{
		Name:    "alice",
		Enabled: true,
		Burst:   BurstPolicy{Enabled: true, WindowMinutes: 30, LimitBytes: 2 << 30, BlockMinutes: 45},
		TrafficSamples: []TrafficSample{
			{At: now.Add(-31 * time.Minute).Format(time.RFC3339Nano), Bytes: 9 << 30},
			{At: now.Add(-20 * time.Minute).Format(time.RFC3339Nano), Bytes: 1100 << 20},
			{At: now.Add(-time.Minute).Format(time.RFC3339Nano), Bytes: 1000 << 20},
		},
	}}}
	changed, configChanged, hardDisconnect, alerts := evaluateBurstPolicies(s, now)
	if !changed || !configChanged || len(alerts) != 1 {
		t.Fatalf("expected block and alert: changed=%v config=%v alerts=%#v", changed, configChanged, alerts)
	}
	if hardDisconnect {
		t.Fatal("default soft punishment unexpectedly requested a hard disconnect")
	}
	u := s.Users[0]
	if !burstBlocked(u, now) || len(u.TrafficSamples) != 0 || unreadAlertCount(s) != 1 {
		t.Fatalf("block state not persisted: %#v", u)
	}
	until, err := time.Parse(time.RFC3339Nano, u.BlockedUntil)
	if err != nil || !until.Equal(now.Add(45*time.Minute)) {
		t.Fatalf("unexpected unblock time %q", u.BlockedUntil)
	}
}

func TestBurstPolicyIgnoresTrafficOutsideWindow(t *testing.T) {
	now := time.Now()
	s := &State{Users: []User{{Name: "alice", Enabled: true, Burst: BurstPolicy{Enabled: true, WindowMinutes: 30, LimitBytes: 2 << 30, BlockMinutes: 30}, TrafficSamples: []TrafficSample{
		{At: now.Add(-31 * time.Minute).Format(time.RFC3339Nano), Bytes: 2 << 30},
		{At: now.Add(-time.Minute).Format(time.RFC3339Nano), Bytes: 1 << 30},
	}}}}
	_, configChanged, _, alerts := evaluateBurstPolicies(s, now)
	if configChanged || len(alerts) != 0 || burstBlocked(s.Users[0], now) {
		t.Fatal("old traffic incorrectly triggered a block")
	}
	if len(s.Users[0].TrafficSamples) != 1 {
		t.Fatalf("old samples were not pruned: %#v", s.Users[0].TrafficSamples)
	}
}

func TestHardBurstPolicyRequestsConnectionDisconnect(t *testing.T) {
	now := time.Now()
	s := &State{Users: []User{{
		Name: "alice", Enabled: true,
		Burst:          BurstPolicy{Enabled: true, Action: burstActionHard, WindowMinutes: 30, LimitBytes: 100, BlockMinutes: 20},
		TrafficSamples: []TrafficSample{{At: now.Add(-time.Minute).Format(time.RFC3339Nano), Bytes: 101}},
	}}}
	changed, configChanged, hardDisconnect, alerts := evaluateBurstPolicies(s, now)
	if !changed || !configChanged || !hardDisconnect || len(alerts) != 1 {
		t.Fatalf("hard block did not request disconnect: changed=%v config=%v hard=%v alerts=%#v", changed, configChanged, hardDisconnect, alerts)
	}
	if !strings.Contains(alerts[0].Message, "硬封禁（完全断连）") {
		t.Fatalf("hard block alert does not identify the action: %q", alerts[0].Message)
	}
}

func TestBurstPolicyAutomaticallyUnblocks(t *testing.T) {
	now := time.Now()
	s := &State{Users: []User{{Name: "alice", Enabled: true, Burst: BurstPolicy{Enabled: true, WindowMinutes: 30, LimitBytes: 2 << 30, BlockMinutes: 30}, BlockedUntil: now.Add(-time.Second).Format(time.RFC3339Nano), BlockReason: "test"}}}
	changed, configChanged, _, alerts := evaluateBurstPolicies(s, now)
	if !changed || !configChanged || len(alerts) != 1 || alerts[0].Kind != "burst_unblocked" {
		t.Fatalf("expected automatic unblock: %#v", alerts)
	}
	if s.Users[0].BlockedUntil != "" || s.Users[0].BlockReason != "" {
		t.Fatalf("expired block was not cleared: %#v", s.Users[0])
	}
}

func TestHardBlockedUserIsRemovedFromServerAndClientConfigs(t *testing.T) {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(sampleConfig), &cfg); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	base := filepath.Join(dir, "base.json")
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(base, b, 0600); err != nil {
		t.Fatal(err)
	}
	s := &State{BaseConfig: base, InboundTag: "vless-in", Client: ClientSettings{Server: "example.com", Port: 443, ServerName: "example.com", PublicKey: "pub"}, Users: []User{{
		Name: "alice", Enabled: true, Burst: BurstPolicy{Enabled: true, WindowMinutes: 30, LimitBytes: 2 << 30, BlockMinutes: 30, Action: burstActionHard},
		BlockedUntil: time.Now().Add(time.Hour).Format(time.RFC3339Nano),
		Nodes:        []Node{{Name: "node", AuthUser: "alice:node", UUID: "22222222-2222-4222-8222-222222222222"}},
	}}}
	rendered, err := renderConfig(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rendered), "alice:node") {
		t.Fatal("blocked credential remained in sing-box config")
	}
	if _, err := renderMihomo(s, s.Users[0]); err == nil {
		t.Fatal("blocked user was allowed to export a client config")
	}
}

func TestSoftBlockedUserStaysConnectedAndUsesPunishmentRate(t *testing.T) {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(sampleConfig), &cfg); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	base := filepath.Join(dir, "base.json")
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(base, b, 0600); err != nil {
		t.Fatal(err)
	}
	mark := rateMarkPrefix | 77
	u := User{
		Name: "alice", Enabled: true,
		Burst:        BurstPolicy{Enabled: true, WindowMinutes: 30, LimitBytes: 2 << 30, BlockMinutes: 30, Action: burstActionSoft, SoftUploadKbps: 16, SoftDownloadKbps: 2},
		BlockedUntil: time.Now().Add(time.Hour).Format(time.RFC3339Nano),
		Devices:      []Device{{Name: defaultDeviceName, Enabled: true, SubscriptionToken: strings.Repeat("a", 32)}},
		Nodes:        []Node{{Name: "node", Device: defaultDeviceName, AuthUser: "alice:node", UUID: "22222222-2222-4222-8222-222222222222", UploadMbps: 100, DownloadMbps: 300, RateMark: mark}},
	}
	s := &State{BaseConfig: base, InboundTag: "vless-in", Client: ClientSettings{Server: "example.com", Port: 443, ServerName: "example.com", PublicKey: "pub"}, Users: []User{u}}
	rendered, err := renderConfig(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), "alice:node") {
		t.Fatal("soft-blocked credential was removed from sing-box config")
	}
	up, down, gotMark := effectiveNodeRate(u, u.Nodes[0])
	if up != 0.016 || down != 0.002 || gotMark != mark {
		t.Fatalf("unexpected soft rate: up=%v down=%v mark=%x", up, down, gotMark)
	}
	nft, err := renderNftables(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2000 bytes/second burst 4096 bytes", "250 bytes/second burst 4096 bytes"} {
		if !strings.Contains(nft, want) {
			t.Fatalf("soft nft rule missing %q:\n%s", want, nft)
		}
	}
	if _, err := renderMihomo(s, u); err != nil {
		t.Fatalf("soft-blocked user should retain export access: %v", err)
	}
}

func TestBurstProtectionCUIFormPersistsPolicy(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(statePath, &State{Version: stateVersion, Users: []User{{Name: "alice", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	m := tuiModel{a: a, state: &State{Users: []User{{Name: "alice", Enabled: true}}}}
	m.openBurstForm(m.state.Users[0])
	m.form.fields[0].value = "开启"
	m.form.fields[1].value = "软封禁（极低速）"
	m.form.fields[2].value = "30"
	m.form.fields[3].value = "2G"
	m.form.fields[4].value = "60"
	m.form.fields[5].value = "16"
	m.form.fields[6].value = "2"
	_, cmd := m.submitForm()
	msg := cmd().(tuiActionMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	s, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	policy := s.Users[0].Burst
	if !policy.Enabled || policy.Action != burstActionSoft || policy.WindowMinutes != 30 || policy.LimitBytes != 2<<30 || policy.BlockMinutes != 60 || policy.SoftUploadKbps != 16 || policy.SoftDownloadKbps != 2 {
		t.Fatalf("unexpected policy: %#v", policy)
	}
}

func TestVersionEightMigratesExistingPunishmentsToSoftMode(t *testing.T) {
	s := &State{Version: 8, Users: []User{
		{Name: "alice", Burst: BurstPolicy{Enabled: true, WindowMinutes: 30, LimitBytes: 2 << 30, BlockMinutes: 20}},
		{Name: "bob", Burst: BurstPolicy{Enabled: false}},
	}}
	if err := migrateState(s); err != nil {
		t.Fatal(err)
	}
	if s.Version != stateVersion {
		t.Fatalf("version = %d, want %d", s.Version, stateVersion)
	}
	for _, user := range s.Users {
		policy := user.Burst
		if policy.Action != burstActionSoft || policy.SoftUploadKbps != 16 || policy.SoftDownloadKbps != 2 {
			t.Fatalf("user %s was not migrated to soft punishment: %#v", user.Name, policy)
		}
	}
}
