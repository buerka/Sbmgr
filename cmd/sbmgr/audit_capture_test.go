package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCaptureAuditScreens(t *testing.T) {
	dir := os.Getenv("SBMGR_AUDIT_DIR")
	if dir == "" {
		t.Skip("set SBMGR_AUDIT_DIR to capture TUI audit screens")
	}
	now := time.Date(2026, 8, 25, 20, 45, 0, 0, applicationLocation())
	users := []User{}
	for index, name := range []string{"alice", "bob", "carol", "dave", "erin", "frank"} {
		u := User{
			Name: name, Enabled: true, QuotaBytes: 20 << 30,
			Upload: int64(index+1) * 620 << 20, Download: int64(index+1) * 410 << 20,
			UploadMbps: 100, DownloadMbps: 300,
			Billing:  BillingPolicy{Enabled: true, CycleDay: 3, TimeZone: "Asia/Shanghai", NextReset: "2026-09-03T00:00:00+08:00"},
			Throttle: ThrottlePolicy{Enabled: true, Tier1Usage: 80, Tier1Speed: 50, Tier2Usage: 95, Tier2Speed: 20},
			Burst:    BurstPolicy{Enabled: true, WindowMinutes: 30, LimitBytes: 2 << 30, BlockMinutes: 20, Action: burstActionSoft, SoftUploadKbps: 16, SoftDownloadKbps: 2},
			Devices:  []Device{{Name: "默认设备", Enabled: true}},
			Nodes: []Node{
				{Name: "Node A", Device: "默认设备", AuthUser: name + ":node-a", UUID: "11111111-1111-4111-8111-111111111111", UploadMbps: 100, DownloadMbps: 300, Upload: 1 << 30, Download: 2 << 30, RateMark: rateMarkPrefix | uint32(index*3+1)},
				{Name: "Relay A via Node A", Device: "默认设备", AuthUser: name + ":relay-a", UUID: "22222222-2222-4222-8222-222222222222", Outbound: "to-relay-a", UploadMbps: 25, DownloadMbps: 25, Upload: 700 << 20, Download: 900 << 20, RateMark: rateMarkPrefix | uint32(index*3+2)},
				{Name: "Relay B via Node A", Device: "默认设备", AuthUser: name + ":relay-b", UUID: "33333333-3333-4333-8333-333333333333", Outbound: "to-relay-b", UploadMbps: 25, DownloadMbps: 25, Upload: 90 << 20, Download: 160 << 20, RateMark: rateMarkPrefix | uint32(index*3+3)},
			},
		}
		if name == "frank" {
			u.CurrentUploadMbps, u.CurrentDownloadMbps = 3.2, 8.6
			u.TrafficSamples = []TrafficSample{{At: now.Add(-10 * time.Minute).Format(time.RFC3339), Bytes: 60 << 20}, {At: now.Format(time.RFC3339), Bytes: 120 << 20}}
		}
		users = append(users, u)
	}
	s := &State{
		Client: ClientSettings{Server: "198.51.100.10", Port: 443, ServerName: "www.example.com"},
		Users:  users, ReservedAuthUsers: []string{"Node A", "Relay A via Node A", "Relay B via Node A"},
		Health: HealthSettings{Mode: "auto", IntervalMinutes: 1, TimeoutSeconds: 3, AlertAfterFailures: 3},
		OutboundHealth: map[string]OutboundHealth{
			"to-relay-a": {Tag: "to-relay-a", Target: "192.0.2.20:443", Healthy: true, LatencyMS: 7, CheckedAt: now.Format(time.RFC3339)},
			"to-relay-b": {Tag: "to-relay-b", Target: "203.0.113.20:443", Healthy: true, LatencyMS: 18, CheckedAt: now.Format(time.RFC3339)},
		}, LastHealthCheck: now.Format(time.RFC3339),
	}
	basePath := filepath.Join(t.TempDir(), "config.base.json")
	baseConfig := `{
  "outbounds": [
    {"type":"direct","tag":"direct"},
    {"type":"shadowsocks","tag":"to-relay-a","server":"192.0.2.20","server_port":443,"method":"chacha20-ietf-poly1305","password":"fixture-secret"},
    {"type":"shadowsocks","tag":"to-relay-b","server":"203.0.113.20","server_port":443,"method":"chacha20-ietf-poly1305","password":"fixture-secret"}
  ],
  "route": {"final":"direct"}
}`
	if err := os.WriteFile(basePath, []byte(baseConfig), 0600); err != nil {
		t.Fatal(err)
	}
	s.BaseConfig = basePath
	models := map[string]tuiModel{
		"01-user-list.ansi":   {state: s, width: 100, height: 30, mode: tuiList, status: "就绪", checkedUsers: map[string]bool{}},
		"02-user-detail.ansi": {state: s, width: 100, height: 30, mode: tuiDetail, selected: "frank", nodeCursor: 2, status: "就绪", checkedUsers: map[string]bool{}},
		"03-edit-form.ansi": {state: s, width: 100, height: 30, mode: tuiFormMode, status: "就绪", form: tuiForm{title: "出口健康与通知", active: 4, fields: []tuiField{
			{label: "健康探测", value: "自动探测", options: []string{"自动探测", "关闭"}},
			{label: "间隔（分钟）", value: "1"},
			{label: "超时（秒）", value: "3"},
			{label: "失败告警次数", value: "3"},
			{label: "自定义目标", value: "to-relay-a=192.0.2.20:443,to-relay-b=203.0.113.20:443,backup=very-long-endpoint.example.com:443", cursor: 43, cursorSet: true},
			{label: "Webhook URL", placeholder: "留空关闭外部通知"},
		}}},
		"04-route-management.ansi": {state: s, width: 100, height: 30, mode: tuiHealth, healthCursor: 2, status: "就绪", checkedUsers: map[string]bool{}},
		"05-user-actions.ansi":     {state: s, width: 100, height: 30, mode: tuiUserMenu, selected: "frank", menuCursor: 7, status: "就绪", checkedUsers: map[string]bool{}},
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, model := range models {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(model.render()), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
