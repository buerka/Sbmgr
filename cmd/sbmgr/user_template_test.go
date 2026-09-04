package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestCloneUserTemplateCopiesConfigurationWithFreshCredentials(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	s := &State{Version: stateVersion, Users: []User{{
		Name: "source", Enabled: false, QuotaBytes: 20 << 30, ExtraQuotaBytes: 2 << 30, Expires: "2026-12-31",
		Upload: 123, Download: 456, CurrentUploadMbps: 3, CurrentDownloadMbps: 4,
		Throttle:       ThrottlePolicy{Enabled: true, Tier1Usage: 50, Tier1Speed: 50, Tier2Usage: 80, Tier2Speed: 20},
		Burst:          BurstPolicy{Enabled: true, WindowMinutes: 30, LimitBytes: 2 << 30, BlockMinutes: 30},
		IPPolicy:       IPPolicy{Enabled: true, Mode: "enforce", Binding: "manual", MaxIPs: 1, BoundIPs: []string{"203.0.113.10"}, TemporaryIPs: []string{"198.51.100.20"}, TemporaryUntil: now.Add(time.Hour).Format(time.RFC3339Nano)},
		Access:         AccessPolicy{BlockedDomains: []string{"tracker.test"}, MaxConnections: 5, ConnectionAction: "alert", LastConnectionAlert: now.Format(time.RFC3339Nano)},
		Billing:        BillingPolicy{Enabled: true, CycleDay: 5, TimeZone: "Asia/Shanghai", LastReset: now.Add(-24 * time.Hour).Format(time.RFC3339), NextReset: now.Add(24 * time.Hour).Format(time.RFC3339)},
		SourceIPs:      map[string]SourceIPStat{"203.0.113.10": {Count: 10}},
		RecentAccesses: []RecentAccess{{Target: "example.com", Device: "phone", Node: "Node A", FirstSeen: now.Format(time.RFC3339), LastSeen: now.Format(time.RFC3339), Count: 8}},
		Devices:        []Device{{Name: "phone", Enabled: true, CreatedAt: now.Add(-time.Hour).Format(time.RFC3339), LastSeen: now.Format(time.RFC3339), SubscriptionToken: strings.Repeat("a", 32), SourceIPs: map[string]SourceIPStat{"203.0.113.10": {Count: 10}}, Access: AccessPolicy{BlockedPorts: []int{25}, LastConnectionAlert: now.Format(time.RFC3339Nano)}, IPPolicy: IPPolicy{Enabled: true, Mode: "monitor", Binding: "manual", MaxIPs: 1, BoundIPs: []string{"203.0.113.10"}, TemporaryIPs: []string{"198.51.100.20"}, TemporaryUntil: now.Add(time.Hour).Format(time.RFC3339Nano)}}},
		Nodes:          []Node{{Name: "Node A", Device: "phone", AuthUser: "source-phone-node-a", UUID: "11111111-1111-4111-8111-111111111111", Outbound: "direct", UploadMbps: 100, DownloadMbps: 300, Upload: 99, Download: 100, Destinations: map[string]AccessStat{"example.com": {Count: 8, LastSeen: now.Format(time.RFC3339)}}}},
	}}}
	if err := saveState(statePath, s); err != nil {
		t.Fatal(err)
	}
	before, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	sourceBefore := *findUser(before, "source")
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.userCmd([]string{"clone", "new-user", "--from", "source"}); err != nil {
		t.Fatal(err)
	}
	after, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	cloned := findUser(after, "new-user")
	if cloned == nil {
		t.Fatal("cloned user missing")
	}
	if !cloned.Enabled || cloned.QuotaBytes != sourceBefore.QuotaBytes || cloned.ExtraQuotaBytes != sourceBefore.ExtraQuotaBytes || cloned.Expires != sourceBefore.Expires || cloned.Throttle != sourceBefore.Throttle || cloned.Burst != sourceBefore.Burst {
		t.Fatalf("template configuration was not copied: %#v", cloned)
	}
	if len(cloned.Access.BlockedDomains) != 1 || cloned.IPPolicy.BoundIPs[0] != "203.0.113.10" || len(cloned.IPPolicy.TemporaryIPs) != 0 || cloned.Access.LastConnectionAlert != "" {
		t.Fatalf("policy copy/reset mismatch: access=%#v ip=%#v", cloned.Access, cloned.IPPolicy)
	}
	if cloned.Upload != 0 || cloned.Download != 0 || cloned.CurrentUploadMbps != 0 || len(cloned.SourceIPs) != 0 || len(cloned.RecentAccesses) != 0 || len(cloned.BillingHistory) != 0 || cloned.Billing.LastReset != "" || cloned.Billing.NextReset != "" {
		t.Fatalf("runtime history leaked into clone: %#v", cloned)
	}
	if len(cloned.Devices) != 1 || len(cloned.Nodes) != 1 {
		t.Fatalf("device/node layout not copied: %#v", cloned)
	}
	if cloned.Devices[0].SubscriptionToken == sourceBefore.Devices[0].SubscriptionToken || len(cloned.Devices[0].SourceIPs) != 0 || cloned.Devices[0].LastSeen != "" || len(cloned.Devices[0].IPPolicy.TemporaryIPs) != 0 {
		t.Fatalf("device identity/history leaked: %#v", cloned.Devices[0])
	}
	if cloned.Nodes[0].UUID == sourceBefore.Nodes[0].UUID || cloned.Nodes[0].AuthUser == sourceBefore.Nodes[0].AuthUser || cloned.Nodes[0].RateMark == sourceBefore.Nodes[0].RateMark || !validRateMark(cloned.Nodes[0].RateMark) {
		t.Fatalf("node credentials were reused: %#v", cloned.Nodes[0])
	}
	if cloned.Nodes[0].Upload != 0 || cloned.Nodes[0].Download != 0 || len(cloned.Nodes[0].Destinations) != 0 || cloned.Nodes[0].UploadMbps != 100 || cloned.Nodes[0].DownloadMbps != 300 {
		t.Fatalf("node settings/history reset mismatch: %#v", cloned.Nodes[0])
	}
	if current := findUser(after, "source"); current == nil || current.Nodes[0].UUID != sourceBefore.Nodes[0].UUID || current.Upload != sourceBefore.Upload {
		t.Fatalf("source user changed: %#v", current)
	}
}

func TestCloneUserFormCreatesFromSelectedTemplate(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	s := &State{Version: stateVersion, Users: []User{{Name: "source", Enabled: true, Devices: []Device{{Name: defaultDeviceName, Enabled: true}}, Nodes: []Node{{Name: "Node A", Device: defaultDeviceName, AuthUser: "source", UUID: "11111111-1111-4111-8111-111111111111"}}}}}
	if err := saveState(statePath, s); err != nil {
		t.Fatal(err)
	}
	s, _ = loadState(statePath)
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	m := tuiModel{a: a, state: s, width: 100, height: 24, mode: tuiList}
	m.openCloneUserForm()
	if m.mode != tuiFormMode || m.form.kind != formCloneUser {
		t.Fatalf("clone form did not open: %#v", m.form)
	}
	m.form.fields[1].value = "copied"
	model, cmd := m.submitForm()
	if cmd == nil {
		t.Fatal("clone form did not submit")
	}
	updated := model.(tuiModel)
	if updated.mode != tuiDetail || updated.selected != "copied" {
		t.Fatalf("clone form did not navigate to new user: mode=%v selected=%q", updated.mode, updated.selected)
	}
	message, ok := cmd().(tuiActionMsg)
	if !ok || message.err != nil {
		t.Fatalf("clone action failed: %#v", message)
	}
	loaded, _ := loadState(statePath)
	if findUser(loaded, "copied") == nil {
		t.Fatal("clone form did not create user")
	}
}

func TestUserListTemplateShortcutOpensCloneForm(t *testing.T) {
	m := tuiModel{state: &State{Users: []User{{Name: "source"}}}, width: 100, height: 24, mode: tuiList}
	model, _ := m.updateList(tea.KeyPressMsg(tea.Key{Text: "N", Code: 'N'}))
	updated := model.(tuiModel)
	if updated.mode != tuiFormMode || updated.form.kind != formCloneUser {
		t.Fatalf("template shortcut did not open clone form: %#v", updated.form)
	}
}

func TestCloneTemplateClearsLearnedAutomaticIPButKeepsManualIP(t *testing.T) {
	s := &State{Users: []User{{
		Name:     "source",
		IPPolicy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "dynamic", MaxIPs: 1, BoundIPs: []string{"203.0.113.10"}},
		Devices:  []Device{{Name: "phone", Enabled: true, IPPolicy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "manual", MaxIPs: 1, BoundIPs: []string{"198.51.100.20"}}}},
		Nodes:    []Node{{Name: "Node A", Device: "phone", AuthUser: "source", UUID: "11111111-1111-4111-8111-111111111111"}},
	}}}
	cloned, err := cloneUserFromTemplate(s, "source", "new-user", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(cloned.IPPolicy.BoundIPs) != 0 {
		t.Fatalf("learned dynamic user IP was copied: %#v", cloned.IPPolicy)
	}
	if got := cloned.Devices[0].IPPolicy.BoundIPs; len(got) != 1 || got[0] != "198.51.100.20" {
		t.Fatalf("manual device IP was not preserved: %#v", cloned.Devices[0].IPPolicy)
	}
}
