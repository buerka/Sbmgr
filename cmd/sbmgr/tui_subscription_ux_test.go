package main

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestSubscriptionOverviewNavigationQRCodeReturnAndRotation(t *testing.T) {
	state := &State{Subscription: SubscriptionSettings{Enabled: true, Listen: "127.0.0.1:18080"}, Users: []User{
		{Name: "bob", Enabled: true, Devices: []Device{{Name: "phone", Enabled: true, SubscriptionToken: strings.Repeat("b", 32)}}},
		{Name: "alice", Enabled: true, Devices: []Device{{Name: "laptop", Enabled: true, SubscriptionToken: strings.Repeat("a", 32)}, {Name: "tablet", Enabled: true, SubscriptionToken: strings.Repeat("c", 32)}}},
	}}
	m := tuiModel{state: state, mode: tuiSubscriptions, width: 100, height: 24, checkedUsers: map[string]bool{}}

	model, _ := m.updateSubscriptions(formSpecialKey(tea.KeyDown))
	m = model.(tuiModel)
	if m.subscriptionCursor != 1 {
		t.Fatalf("down cursor = %d, want 1", m.subscriptionCursor)
	}
	model, _ = m.updateSubscriptions(formSpecialKey(tea.KeyEnter))
	m = model.(tuiModel)
	if m.mode != tuiQRCode || m.qrReturnMode != tuiSubscriptions || m.qrUser != "alice" || m.qrDevice != "tablet" {
		t.Fatalf("subscription QR context = %#v", m)
	}
	model, _ = m.Update(formSpecialKey(tea.KeyEscape))
	m = model.(tuiModel)
	if m.mode != tuiSubscriptions {
		t.Fatalf("QR escape returned to %v, want subscriptions", m.mode)
	}

	model, _ = m.updateSubscriptions(tea.KeyPressMsg(tea.Key{Text: "r", Code: 'r'}))
	m = model.(tuiModel)
	if m.mode != tuiConfirmMode || m.confirm.action != confirmRotateSubscription || m.confirm.user != "alice" || m.confirm.device != "tablet" || m.confirm.returnMode != tuiSubscriptions {
		t.Fatalf("rotate confirmation = %#v", m.confirm)
	}
	model, _ = m.updateConfirm(formSpecialKey(tea.KeyEscape))
	if got := model.(tuiModel).mode; got != tuiSubscriptions {
		t.Fatalf("cancel rotation returned to %v", got)
	}
}

func TestSubscriptionOverviewFits64x18AndShowsOperationalFields(t *testing.T) {
	users := make([]User, 5)
	for index := range users {
		users[index] = User{
			Name: "user-" + string(rune('a'+index)), Enabled: true, QuotaBytes: 20 << 30, QuotaMode: quotaModeDownload, Expires: "2026-12-31",
			Devices: []Device{{Name: "phone", Enabled: true, SubscriptionToken: strings.Repeat(string(rune('a'+index)), 32)}},
			Nodes:   []Node{{Name: "Node A", Device: "phone", Upload: 1 << 20, Download: 2 << 20}},
		}
	}
	m := tuiModel{state: &State{Subscription: SubscriptionSettings{Enabled: true, Listen: "127.0.0.1:18080"}, Users: users}, mode: tuiSubscriptions, width: 64, height: 18, checkedUsers: map[string]bool{}}
	rendered := m.renderSubscriptions()
	lines := strings.Split(rendered, "\n")
	if len(lines) > m.height {
		t.Fatalf("subscription overview height = %d, want <= %d:\n%s", len(lines), m.height, rendered)
	}
	for index, line := range lines {
		if width := lipgloss.Width(line); width > m.width {
			t.Fatalf("subscription line %d width = %d, want <= %d: %q", index+1, width, m.width, line)
		}
	}
	for _, want := range []string{"设备流量", "用户计费", "到期", "URL", "enter/z 二维码", "r/u 撤销旧链接"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("subscription overview missing %q:\n%s", want, rendered)
		}
	}
}

func TestSubscriptionOverviewUsesRuntimeAvailabilityReasons(t *testing.T) {
	now := time.Now()
	state := &State{Subscription: SubscriptionSettings{Enabled: true, Listen: "127.0.0.1:18080"}, Users: []User{
		{Name: "available", Enabled: true, Devices: []Device{{Name: "phone", Enabled: true, SubscriptionToken: strings.Repeat("a", 32)}}, Nodes: []Node{{Name: "Node A", Device: "phone"}}},
		{Name: "quota", Enabled: true, Upload: 10, QuotaBytes: 10, Devices: []Device{{Name: "phone", Enabled: true, SubscriptionToken: strings.Repeat("b", 32)}}, Nodes: []Node{{Name: "Node A", Device: "phone"}}},
		{Name: "blocked", Enabled: true, BlockedUntil: now.Add(time.Hour).Format(time.RFC3339Nano), Burst: BurstPolicy{Enabled: true, Action: burstActionHard}, Devices: []Device{{Name: "phone", Enabled: true, SubscriptionToken: strings.Repeat("c", 32)}}, Nodes: []Node{{Name: "Node A", Device: "phone"}}},
		{Name: "empty", Enabled: true, Devices: []Device{{Name: "phone", Enabled: true, SubscriptionToken: strings.Repeat("d", 32)}}},
	}}
	m := tuiModel{state: state, mode: tuiSubscriptions, width: 140, height: 60, checkedUsers: map[string]bool{}}
	rendered := m.renderSubscriptions()
	for _, want := range []string{"available / phone", "可订阅", "用户已用完流量", "用户因异常流量被临时封禁至", "没有可导出的节点"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("subscription availability missing %q:\n%s", want, rendered)
		}
	}
}

func TestQRCodeCompactFallbackFits64x18WithFooter(t *testing.T) {
	state := qrTUITestState()
	m := tuiModel{state: state, mode: tuiQRCode, selected: "alice", qrUser: "alice", qrDevice: "phone", qrReturnMode: tuiSubscriptions, width: 64, height: 18, checkedUsers: map[string]bool{}}
	rendered := m.renderQRCode()
	assertTUIRenderBounds(t, rendered, m.width, m.height)
	wantLink := subscriptionURL(state, state.Users[0].Devices[0])
	for _, want := range []string{"URL", "不足以完整显示二维码", "复制上方订阅 URL", "esc 返回"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("compact QR view missing %q:\n%s", want, rendered)
		}
	}
	if got := compactQRVisibleURL(rendered); got != wantLink {
		t.Fatalf("visible wrapped URL = %q, want complete %q:\n%s", got, wantLink, rendered)
	}
	if strings.Contains(compactQRVisibleURL(rendered), "…") {
		t.Fatalf("compact QR URL contains an ellipsis:\n%s", rendered)
	}
	if strings.Contains(rendered, "██") {
		t.Fatalf("compact QR view rendered a clipped QR:\n%s", rendered)
	}
}

func TestQRCodeOnlyRendersFullCodeWhenItFits(t *testing.T) {
	state := qrTUITestState()
	m := tuiModel{state: state, mode: tuiQRCode, selected: "alice", qrUser: "alice", qrDevice: "phone", width: 180, height: 100, checkedUsers: map[string]bool{}}
	rendered := m.renderQRCode()
	assertTUIRenderBounds(t, rendered, m.width, m.height)
	if !strings.Contains(rendered, "██") || strings.Contains(rendered, "不足以完整显示二维码") {
		t.Fatalf("large QR view did not render the full code:\n%s", rendered)
	}
}

func qrTUITestState() *State {
	return &State{Subscription: SubscriptionSettings{Enabled: true, Listen: "127.0.0.1:18080"}, Users: []User{{
		Name: "alice", Enabled: true,
		Devices: []Device{{Name: "phone", Enabled: true, SubscriptionToken: strings.Repeat("a", 32)}},
		Nodes:   []Node{{Name: "Node A", Device: "phone"}},
	}}}
}

func assertTUIRenderBounds(t *testing.T, rendered string, width, height int) {
	t.Helper()
	lines := strings.Split(rendered, "\n")
	if len(lines) > height {
		t.Fatalf("render height = %d, want <= %d:\n%s", len(lines), height, rendered)
	}
	for index, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", index+1, got, width, line)
		}
	}
}

func compactQRVisibleURL(rendered string) string {
	parts := []string{}
	collecting := false
	for _, line := range strings.Split(rendered, "\n") {
		plain := strings.TrimSpace(line)
		if plain == "URL" {
			collecting = true
			continue
		}
		if !collecting {
			continue
		}
		if plain == "" {
			break
		}
		parts = append(parts, plain)
	}
	return strings.Join(parts, "")
}

func TestSubscriptionOverviewShowsTLSCertificateWithoutPrivateKeyPath(t *testing.T) {
	certPath, keyPath := writeTestSubscriptionKeyPair(t, "subscription.example")
	state := &State{Subscription: SubscriptionSettings{Enabled: true, Listen: "0.0.0.0:18443", TLSCertFile: certPath, TLSKeyFile: keyPath}}
	m := tuiModel{state: state, mode: tuiSubscriptions, width: 120, height: 26, checkedUsers: map[string]bool{}}
	rendered := m.renderSubscriptions()
	for _, want := range []string{"原生 HTTPS", "证书到期", "剩余", "证书 SAN", "subscription.example", "私钥 已配置（内容永不显示）"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("TLS overview missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, keyPath) {
		t.Fatalf("private-key path leaked into overview: %s", rendered)
	}
}

func TestSubscriptionSettingsFormPersistsNativeTLSPaths(t *testing.T) {
	certPath, keyPath := writeTestSubscriptionKeyPair(t, "subscription.example")
	testDir := t.TempDir()
	statePath := filepath.Join(testDir, "state.json")
	systemctlLog := filepath.Join(testDir, "systemctl.args")
	fakeBinDir := filepath.Join(testDir, "bin")
	if err := os.Mkdir(fakeBinDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fakeSystemctl := filepath.Join(fakeBinDir, "systemctl")
	if err := os.WriteFile(fakeSystemctl, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$SBMGR_TEST_SYSTEMCTL_LOG\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SBMGR_TEST_SYSTEMCTL_LOG", systemctlLog)
	t.Setenv("PATH", fakeBinDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	state := &State{Version: stateVersion, Subscription: SubscriptionSettings{Listen: "127.0.0.1:18080"}}
	if err := saveState(statePath, state); err != nil {
		t.Fatal(err)
	}
	m := tuiModel{a: &app{statePath: statePath, out: io.Discard, err: io.Discard}, state: state, mode: tuiSubscriptions, width: 100, height: 24, checkedUsers: map[string]bool{}}
	model, _ := m.updateSubscriptions(tea.KeyPressMsg(tea.Key{Text: "e", Code: 'e'}))
	m = model.(tuiModel)
	if len(m.form.fields) != 5 || m.form.fields[3].label != "TLS 证书绝对路径" || m.form.fields[4].label != "TLS 私钥绝对路径" {
		t.Fatalf("subscription TLS fields = %#v", m.form.fields)
	}
	m.form.fields[0].value = "开启"
	m.form.fields[3].value = certPath
	m.form.fields[4].value = keyPath
	model, cmd := m.submitForm()
	if cmd == nil || model.(tuiModel).mode != tuiSubscriptions {
		t.Fatal("subscription settings form did not submit")
	}
	msg := cmd().(tuiActionMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	stored, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Subscription.TLSCertFile != certPath || stored.Subscription.TLSKeyFile != keyPath {
		t.Fatalf("stored TLS settings = %#v", stored.Subscription)
	}
	if runtime.GOOS == "linux" {
		called, err := os.ReadFile(systemctlLog)
		if err != nil {
			t.Fatalf("fake systemctl was not called: %v", err)
		}
		if got, want := string(called), "restart\nsbmgr\n"; got != want {
			t.Fatalf("systemctl args = %q, want %q", got, want)
		}
	}
}
