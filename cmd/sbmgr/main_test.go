package main

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
)

const sampleConfig = `{
  "inbounds": [{
    "type": "vless", "tag": "vless-in", "listen_port": 443,
    "users": [{"name":"LAX","uuid":"11111111-1111-4111-8111-111111111111"}],
    "tls": {"server_name":"www.cloudflare.com","reality":{"short_id":["abcd1234"]}}
  }],
  "outbounds": [{"type":"direct","tag":"direct"},{"type":"shadowsocks","tag":"to-att"}],
  "route": {"rules":[
    {"auth_user":["LAX"],"action":"route","outbound":"to-att"},
    {"ip_is_private":true,"action":"route","outbound":"direct"}
  ],"final":"direct"}
}`

func TestImportAndRender(t *testing.T) {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(sampleConfig), &cfg); err != nil {
		t.Fatal(err)
	}
	s, base, err := importConfig(cfg, "vless-in", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Users) != 1 || s.Users[0].Nodes[0].Outbound != "to-att" {
		t.Fatalf("bad import: %#v", s.Users)
	}
	dir := t.TempDir()
	s.BaseConfig = filepath.Join(dir, "base.json")
	b, _ := json.Marshal(base)
	if err := os.WriteFile(s.BaseConfig, b, 0600); err != nil {
		t.Fatal(err)
	}
	s.Users = append(s.Users, User{Name: "alice", Enabled: true, Nodes: []Node{{Name: "ATT", AuthUser: "alice:att", UUID: "22222222-2222-4222-8222-222222222222", Outbound: "to-att"}}})
	out, err := renderConfig(s)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "alice:att") {
		t.Fatal("managed user missing")
	}
	if strings.Count(text, "auth_user") != 2 {
		t.Fatalf("expected 2 managed rules: %s", text)
	}
	if !strings.Contains(text, "ip_is_private") {
		t.Fatal("unmanaged rule was removed")
	}
}

func TestInitPreservesExistingCredentialsAsUnmanaged(t *testing.T) {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(sampleConfig), &cfg); err != nil {
		t.Fatal(err)
	}
	s, base, err := importConfig(cfg, "vless-in", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Users) != 0 || len(s.ReservedAuthUsers) != 1 || s.ReservedAuthUsers[0] != "LAX" {
		t.Fatalf("existing credential was not reserved: %#v", s)
	}
	inbounds := base["inbounds"].([]any)
	users := inbounds[0].(map[string]any)["users"].([]any)
	if len(users) != 1 || users[0].(map[string]any)["name"] != "LAX" {
		t.Fatalf("existing inbound user was removed: %#v", users)
	}
	rules := base["route"].(map[string]any)["rules"].([]any)
	if len(rules) != 2 || rules[0].(map[string]any)["auth_user"] == nil {
		t.Fatalf("existing auth route was removed: %#v", rules)
	}
}

func TestQuotaAndExpiry(t *testing.T) {
	u := User{Enabled: true, QuotaBytes: 100, Upload: 40, Download: 60}
	if !overQuota(u) {
		t.Fatal("quota not enforced")
	}
	u = User{Expires: "2026-12-31"}
	now, _ := time.Parse("2006-01-02", "2026-12-31")
	if expired(u, now) {
		t.Fatal("expiry day should remain valid")
	}
	next, _ := time.Parse("2006-01-02", "2027-01-01")
	if !expired(u, next) {
		t.Fatal("user should expire next day")
	}
}

func TestParseSize(t *testing.T) {
	v, err := parseSize("1.5G")
	if err != nil {
		t.Fatal(err)
	}
	if v != int64(1.5*(1<<30)) {
		t.Fatalf("got %d", v)
	}
}

func TestParseSizeSupportsExplicitBytesAndOverlappingSuffixes(t *testing.T) {
	tests := map[string]int64{
		"2B":    2,
		"2GB":   2 << 30,
		"2GiB":  2 << 30,
		"500MB": 500 << 20,
	}
	for input, want := range tests {
		got, err := parseSize(input)
		if err != nil {
			t.Fatalf("parseSize(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("parseSize(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestDeriveRealityPublicKey(t *testing.T) {
	privateBytes := make([]byte, 32)
	for i := range privateBytes {
		privateBytes[i] = byte(i + 1)
	}
	privateKey, err := ecdh.X25519().NewPrivateKey(privateBytes)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(privateBytes)
	got, err := deriveRealityPublicKey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	want := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes())
	if got != want {
		t.Fatalf("public key mismatch: got %q want %q", got, want)
	}
}

func TestMihomoOnlyExportsOwnNodes(t *testing.T) {
	s := &State{Client: ClientSettings{Server: "example.com", Port: 443, ServerName: "www.cloudflare.com", PublicKey: "pub", ShortID: "abcd"}}
	u := User{Name: "alice", Enabled: true, Nodes: []Node{{Name: "ATT", UUID: "u1"}}}
	b, err := renderMihomo(s, u)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "alice-ATT") || strings.Contains(string(b), "bob") {
		t.Fatal(string(b))
	}
}

func TestFormatDisplayTimeUsesBeijingTime(t *testing.T) {
	got := formatDisplayTime("2026-08-03T09:19:25.67943655-07:00")
	if want := "2026-08-04 00:19:25"; got != want {
		t.Fatalf("display time = %q want %q", got, want)
	}
	if got := formatDisplayTime(""); got != "-" {
		t.Fatalf("empty display time = %q", got)
	}
	if got := formatDisplayTime("2026-08-04"); got != "2026-08-04" {
		t.Fatalf("plain date changed to %q", got)
	}
}

func TestParseUserStatName(t *testing.T) {
	auth, direction, ok := parseUserStatName("user>>>alice:att>>>traffic>>>downlink")
	if !ok || auth != "alice:att" || direction != "downlink" {
		t.Fatalf("got %q %q %v", auth, direction, ok)
	}
	if _, _, ok := parseUserStatName("outbound>>>direct>>>traffic>>>uplink"); ok {
		t.Fatal("accepted non-user counter")
	}
}

func TestSyncIfEnabledSkipsMissingAPI(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(statePath, &State{Version: stateVersion}); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.syncCmd([]string{"--apply", "--if-enabled"}); err != nil {
		t.Fatal(err)
	}
}

func TestPublicCLIStaysSmall(t *testing.T) {
	var out strings.Builder
	a := &app{out: &out, err: io.Discard}
	if err := a.run([]string{"version"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), appVersion) {
		t.Fatalf("version missing from %q", out.String())
	}
	out.Reset()
	usage(&out)
	if strings.Contains(out.String(), "user add") || strings.Contains(out.String(), "rate apply") {
		t.Fatalf("maintenance commands leaked into normal help:\n%s", out.String())
	}
	if err := a.run([]string{"user", "list"}); err == nil {
		t.Fatal("legacy maintenance command remained public")
	}
}

func TestDefaultStatePathUsesApplicationHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SBMGR_HOME", home)
	want := filepath.Join(home, "state.json")
	if got := defaultStatePath(); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestAddUserFormTreatsClearedOptionalNumbersAsUnlimited(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(statePath, &State{Version: stateVersion}); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	m := tuiModel{a: a, state: &State{Version: stateVersion}}
	m.openAddUserForm()
	m.form.fields[0].value = "alice"
	m.form.fields[1].value = ""
	m.form.fields[5].value = ""
	m.form.fields[6].value = ""

	_, cmd := m.submitForm()
	if cmd == nil {
		t.Fatal("form did not submit")
	}
	msg, ok := cmd().(tuiActionMsg)
	if !ok {
		t.Fatal("unexpected TUI command response")
	}
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	s, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	u := findUser(s, "alice")
	if u == nil || u.QuotaBytes != 0 {
		t.Fatalf("cleared optional values were not normalized: %#v", u)
	}
	if len(u.Nodes) != 1 || u.Nodes[0].Name != "默认线路" || u.Nodes[0].UploadMbps != 0 || u.Nodes[0].DownloadMbps != 0 {
		t.Fatalf("default node was not restored: %#v", u.Nodes)
	}
}

func TestCommandParseErrorsUseAppErrorWriter(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(statePath, &State{Version: stateVersion}); err != nil {
		t.Fatal(err)
	}
	var errOut strings.Builder
	a := &app{statePath: statePath, out: io.Discard, err: &errOut}
	if err := a.userCmd([]string{"add", "alice", "--up-mbps", ""}); err == nil {
		t.Fatal("empty float unexpectedly accepted by command parser")
	}
	if !strings.Contains(errOut.String(), "invalid value") {
		t.Fatalf("parse diagnostics did not use app error writer: %q", errOut.String())
	}
}

func TestCUIBareQuotaUsesGiB(t *testing.T) {
	if got := normalizeQuotaInput("200"); got != "200G" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeQuotaInput("500M"); got != "500M" {
		t.Fatalf("explicit unit changed to %q", got)
	}
	if got := normalizeQuotaInput(""); got != "0" {
		t.Fatalf("blank quota changed to %q", got)
	}
}

func TestTieredThrottleUsesQuotaStages(t *testing.T) {
	u := User{QuotaBytes: 1000, Upload: 850, Throttle: ThrottlePolicy{Enabled: true}}
	n := Node{UploadMbps: 100, DownloadMbps: 50, RateMark: rateMarkPrefix | 1}
	up, down, _ := effectiveNodeRate(u, n)
	if up != 50 || down != 25 || throttleStage(u) != 1 {
		t.Fatalf("unexpected first tier: up=%v down=%v stage=%d", up, down, throttleStage(u))
	}
	u.Upload = 960
	up, down, _ = effectiveNodeRate(u, n)
	if up != 20 || down != 10 || throttleStage(u) != 2 {
		t.Fatalf("unexpected second tier: up=%v down=%v stage=%d", up, down, throttleStage(u))
	}
	if err := validateThrottle(ThrottlePolicy{Enabled: true, Tier1Usage: 95, Tier1Speed: 50, Tier2Usage: 80, Tier2Speed: 20}); err == nil {
		t.Fatal("invalid tier ordering accepted")
	}
}

func TestParseNftCountersAndAccessLog(t *testing.T) {
	raw := []byte(`{"nftables":[{"rule":{"comment":"alice/LAX upload","expr":[{"match":{"op":"=="}},{"counter":{"packets":3,"bytes":4242}}]}}]}`)
	counters, err := parseNftCounterJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if counters["alice/LAX upload"] != 4242 {
		t.Fatalf("unexpected counters: %#v", counters)
	}
	auth, target, ok := parseAccessLog(`inbound/vless[vless-in]: [alice:lax] inbound connection to api.openai.com:443`)
	if !ok || auth != "alice:lax" || target != "api.openai.com" {
		t.Fatalf("unexpected access parse: %q %q %v", auth, target, ok)
	}
	encoded, _ := json.Marshal([]byte(`inbound/vless[vless-in]: [alice:lax] inbound connection to api.openai.com:443`))
	if decoded := decodeJournalMessage(encoded); !strings.Contains(decoded, "alice:lax") {
		t.Fatalf("journal byte-array message was not decoded: %q", decoded)
	}
}

func TestPendingSourceArchiveIsBounded(t *testing.T) {
	values := map[string]PendingSource{}
	for i := 0; i < 10; i++ {
		values[fmt.Sprintf("id-%d", i)] = PendingSource{At: fmt.Sprintf("2026-08-03T12:%02d:00Z", i)}
	}
	trimPendingSources(values, 4)
	if len(values) != 4 {
		t.Fatalf("pending source count = %d", len(values))
	}
	if _, exists := values["id-9"]; !exists {
		t.Fatal("newest pending source was removed")
	}
	if _, exists := values["id-0"]; exists {
		t.Fatal("oldest pending source was retained")
	}
}

func TestSourceAfterAccessStillArchivesAndEnforcesIP(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	s := &State{
		ActiveConnections: map[string]ActiveConnection{
			"42": {ID: "42", User: "alice", Device: "phone", Node: "LAX", AuthUser: "alice:lax", LastSeen: now.Format(time.RFC3339)},
		},
		Users: []User{{
			Name: "alice", Enabled: true,
			IPPolicy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "dynamic", MaxIPs: 1},
			Devices:  []Device{{Name: "phone", Enabled: true}},
			Nodes:    []Node{{Name: "LAX", Device: "phone", AuthUser: "alice:lax"}},
		}},
	}
	if !attachSourceToKnownConnection(s, "42", "203.0.113.10", now.Add(time.Second)) {
		t.Fatal("late source line was not attached")
	}
	connection := s.ActiveConnections["42"]
	if connection.SourceIP != "203.0.113.10" || s.Users[0].SourceIPs["203.0.113.10"].Count != 1 || s.Users[0].Devices[0].SourceIPs["203.0.113.10"].Count != 1 {
		t.Fatalf("late source was not archived: connection=%#v user=%#v device=%#v", connection, s.Users[0].SourceIPs, s.Users[0].Devices[0].SourceIPs)
	}
	if got := s.Users[0].IPPolicy.BoundIPs; len(got) != 1 || got[0] != "203.0.113.10" || !s.IPApplyPending {
		t.Fatalf("late source did not drive dynamic binding: %#v", s.Users[0].IPPolicy)
	}
}

func TestStaleConnectionsAndPendingSourcesArePruned(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	s := &State{
		PendingSources: map[string]PendingSource{
			"old": {IP: "203.0.113.10", At: now.Add(-pendingSourceTTL - time.Second).Format(time.RFC3339Nano)},
			"new": {IP: "203.0.113.11", At: now.Format(time.RFC3339Nano)},
		},
		ActiveConnections: map[string]ActiveConnection{
			"old": {LastSeen: now.Add(-activeConnectionTTL - time.Second).Format(time.RFC3339)},
			"new": {LastSeen: now.Format(time.RFC3339)},
		},
	}
	if !pruneConnectionTracking(s, now) {
		t.Fatal("prune reported no change")
	}
	if len(s.PendingSources) != 1 || s.PendingSources["new"].IP == "" || len(s.ActiveConnections) != 1 || s.ActiveConnections["new"].LastSeen == "" {
		t.Fatalf("unexpected tracking state: pending=%#v active=%#v", s.PendingSources, s.ActiveConnections)
	}
}

func TestDefaultExportPathIncludesTimestamp(t *testing.T) {
	now := time.Date(2026, 8, 2, 18, 15, 30, 0, time.UTC)
	path := defaultExportPath(filepath.Join("root", "sbmgr", "state.json"), "Alice Smith", now)
	if got := filepath.Base(path); got != "alice-smith-20260803-021530.yaml" {
		t.Fatalf("got %q", got)
	}
}

func TestInitForceBacksUpExistingBaseConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "sing-box.json")
	statePath := filepath.Join(dir, "state.json")
	basePath := filepath.Join(dir, "config.base.json")
	oldBase := []byte("{\n  \"old\": \"base configuration\"\n}\n")
	if err := os.WriteFile(configPath, []byte(sampleConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveState(statePath, &State{Version: stateVersion}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(basePath, oldBase, 0600); err != nil {
		t.Fatal(err)
	}

	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.initCmd([]string{"--config", configPath, "--base", basePath, "--force"}); err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(filepath.Join(dir, "backups", "config.base-pre-endpoint-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("base backups = %v, want exactly one", backups)
	}
	backedUp, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(backedUp) != string(oldBase) {
		t.Fatalf("base backup differs from original:\n%s", backedUp)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(backups[0])
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("base backup permissions = %o, want 600", info.Mode().Perm())
		}
	}
}

func TestInitForceWithoutExistingBaseDoesNotCreateBaseBackup(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "sing-box.json")
	statePath := filepath.Join(dir, "state.json")
	basePath := filepath.Join(dir, "config.base.json")
	if err := os.WriteFile(configPath, []byte(sampleConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveState(statePath, &State{Version: stateVersion}); err != nil {
		t.Fatal(err)
	}

	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.initCmd([]string{"--config", configPath, "--base", basePath, "--force"}); err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(filepath.Join(dir, "backups", "config.base-pre-endpoint-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Fatalf("unexpected base backups without an existing base: %v", backups)
	}
}

func TestCLIWorkflow(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	statePath := filepath.Join(dir, "state.json")
	basePath := filepath.Join(dir, "base.json")
	if err := os.WriteFile(configPath, []byte(sampleConfig), 0600); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.initCmd([]string{"--config", configPath, "--base", basePath, "--server", "lax.example.com", "--public-key", "pub"}); err != nil {
		t.Fatal(err)
	}
	if err := a.userCmd([]string{"add", "alice", "--quota", "100G", "--expire", "2026-12-31"}); err != nil {
		t.Fatal(err)
	}
	immediateExport := filepath.Join(dir, "alice-immediate.yaml")
	if err := a.exportCmd([]string{"alice", "--output", immediateExport}); err != nil {
		t.Fatal("new user was not immediately exportable:", err)
	}
	if b, err := os.ReadFile(immediateExport); err != nil || !strings.Contains(string(b), "alice-default") {
		t.Fatalf("default node missing from immediate export: %v\n%s", err, b)
	}
	if err := a.userCmd([]string{"set", "alice", "--quota", "200G", "--clear-expire", "--up-mbps", "12.5", "--down-mbps", "50"}); err != nil {
		t.Fatal(err)
	}
	if err := a.nodeCmd([]string{"add", "alice", "--name", "ATT", "--outbound", "to-att"}); err != nil {
		t.Fatal(err)
	}
	exportPath := filepath.Join(dir, "alice.yaml")
	if err := a.exportCmd([]string{"alice", "--output", exportPath}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "alice-ATT") || strings.Contains(string(b), "LAX-LAX") {
		t.Fatalf("unexpected export:\n%s", b)
	}
	s, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	alice := findUser(s, "alice")
	if alice.QuotaBytes != 200*(1<<30) || alice.Expires != "" || alice.UploadMbps != 0 || alice.DownloadMbps != 0 || len(alice.Nodes) != 2 || alice.Nodes[0].UploadMbps != 12.5 || alice.Nodes[0].DownloadMbps != 50 || !validRateMark(alice.Nodes[0].RateMark) {
		t.Fatal("quota flag after positional argument was not parsed")
	}
	rendered, err := renderConfig(s)
	if err != nil {
		t.Fatal(err)
	}
	var renderedConfig map[string]any
	if err := json.Unmarshal(rendered, &renderedConfig); err != nil {
		t.Fatal(err)
	}
	inboundUsers := renderedConfig["inbounds"].([]any)[0].(map[string]any)["users"].([]any)
	if len(inboundUsers) != 3 { // One preserved credential plus Alice's two UUIDs.
		t.Fatalf("managed render did not preserve legacy credential: %s", rendered)
	}
}

func TestTUIRendersEmptyAndUserViews(t *testing.T) {
	m := tuiModel{
		state:  &State{ReservedAuthUsers: []string{"LAX", "ATT via LAX", "Frontier via LAX"}},
		width:  110,
		height: 30,
	}
	empty := m.render()
	if !strings.Contains(empty, "尚未添加受管用户") || !strings.Contains(empty, "旧节点") {
		t.Fatalf("empty TUI missing onboarding copy:\n%s", empty)
	}
	m.state.Users = []User{{Name: "alice", Enabled: true, Nodes: []Node{{Name: "default", UUID: "uuid"}}}}
	list := m.render()
	if !strings.Contains(list, "alice") || !strings.Contains(list, "enter 详情") {
		t.Fatalf("user list TUI missing content:\n%s", list)
	}
	m.selected, m.mode = "alice", tuiDetail
	detail := m.render()
	if !strings.Contains(detail, "UUID") || !strings.Contains(detail, "x 导出") {
		t.Fatalf("detail TUI missing actions:\n%s", detail)
	}
}

func TestRenderStatsConfiguration(t *testing.T) {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(sampleConfig), &cfg); err != nil {
		t.Fatal(err)
	}
	s, base, err := importConfig(cfg, "vless-in", true)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s.BaseConfig = filepath.Join(dir, "base.json")
	s.StatsAPI = "127.0.0.1:8080"
	b, _ := json.Marshal(base)
	if err := os.WriteFile(s.BaseConfig, b, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := renderConfig(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"v2ray_api"`) || !strings.Contains(string(out), `"127.0.0.1:8080"`) {
		t.Fatalf("stats configuration missing: %s", out)
	}
}

func TestRenderRateLimitedOutboundAndNftables(t *testing.T) {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(sampleConfig), &cfg); err != nil {
		t.Fatal(err)
	}
	s, base, err := importConfig(cfg, "vless-in", true)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s.BaseConfig = filepath.Join(dir, "base.json")
	s.Users[0].Nodes[0].UploadMbps = 12.5
	s.Users[0].Nodes[0].DownloadMbps = 50
	s.Users[0].Nodes[0].RateMark = rateMarkPrefix | 1
	b, _ := json.Marshal(base)
	if err := os.WriteFile(s.BaseConfig, b, 0600); err != nil {
		t.Fatal(err)
	}

	rendered, err := renderConfig(s)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(rendered, &output); err != nil {
		t.Fatal(err)
	}
	outbounds, _ := output["outbounds"].([]any)
	var clone map[string]any
	for _, item := range outbounds {
		m, _ := item.(map[string]any)
		if strings.HasPrefix(stringValue(m["tag"]), "sbmgr-rate-0001-") {
			clone = m
			break
		}
	}
	if clone == nil || uint32(clone["routing_mark"].(float64)) != s.Users[0].Nodes[0].RateMark {
		t.Fatalf("marked outbound clone missing: %s", rendered)
	}
	route := output["route"].(map[string]any)
	rules := route["rules"].([]any)
	first := rules[0].(map[string]any)
	if !strings.HasPrefix(stringValue(first["outbound"]), "sbmgr-rate-0001-") {
		t.Fatalf("auth_user was not routed to marked clone: %#v", first)
	}

	nft, err := renderNftables(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"meta mark 0x53420001", "ct mark 0x53420001", "1562500 bytes/second", "6250000 bytes/second"} {
		if !strings.Contains(nft, want) {
			t.Fatalf("nft rules missing %q:\n%s", want, nft)
		}
	}
}

func TestPerNodeLimitsUseIndependentMarks(t *testing.T) {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(sampleConfig), &cfg); err != nil {
		t.Fatal(err)
	}
	s, base, err := importConfig(cfg, "vless-in", false)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s.BaseConfig = filepath.Join(dir, "base.json")
	b, _ := json.Marshal(base)
	if err := os.WriteFile(s.BaseConfig, b, 0600); err != nil {
		t.Fatal(err)
	}
	s.Users = []User{{Name: "alice", Enabled: true, Nodes: []Node{
		{Name: "LAX", AuthUser: "alice:lax", UUID: "u1", Outbound: "direct", UploadMbps: 10, DownloadMbps: 20, RateMark: rateMarkPrefix | 1},
		{Name: "ATT", AuthUser: "alice:att", UUID: "u2", Outbound: "to-att", UploadMbps: 30, DownloadMbps: 40, RateMark: rateMarkPrefix | 2},
	}}}

	rendered, err := renderConfig(s)
	if err != nil {
		t.Fatal(err)
	}
	text := string(rendered)
	for _, want := range []string{"sbmgr-rate-0001-direct", "sbmgr-rate-0002-to-att", "alice:lax", "alice:att"} {
		if !strings.Contains(text, want) {
			t.Fatalf("rendered config missing %q:\n%s", want, text)
		}
	}
	nft, err := renderNftables(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"meta mark 0x53420001", "meta mark 0x53420002", "alice/LAX upload", "alice/ATT download", "1250000 bytes/second", "5000000 bytes/second"} {
		if !strings.Contains(nft, want) {
			t.Fatalf("nft rules missing %q:\n%s", want, nft)
		}
	}
}

func TestNodeTemplatesComeFromPreservedServerRoutes(t *testing.T) {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(sampleConfig), &cfg); err != nil {
		t.Fatal(err)
	}
	s, base, err := importConfig(cfg, "vless-in", false)
	if err != nil {
		t.Fatal(err)
	}
	s.BaseConfig = filepath.Join(t.TempDir(), "base.json")
	b, _ := json.Marshal(base)
	if err := os.WriteFile(s.BaseConfig, b, 0600); err != nil {
		t.Fatal(err)
	}
	templates := nodeTemplates(s)
	if len(templates) != 1 || templates[0].Name != "LAX" || templates[0].Outbound != "to-att" {
		t.Fatalf("unexpected templates: %#v", templates)
	}
}

func TestNodeTemplatesIncludeAdditionalOutboundsButNotEndpoints(t *testing.T) {
	directory := t.TempDir()
	base := filepath.Join(directory, "base.json")
	raw := `{
  "outbounds": [
    {"type":"direct","tag":"direct"},
    {"type":"socks","tag":"existing","server":"127.0.0.1","server_port":1080},
    {"type":"urltest","tag":"automatic","outbounds":["existing"]}
  ],
  "endpoints": [{"type":"wireguard","tag":"wg-egress"}],
  "route": {"final":"direct","rules":[{"auth_user":["LAX"],"outbound":"existing"}]}
}`
	if err := os.WriteFile(base, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	s := &State{BaseConfig: base, ReservedAuthUsers: []string{"LAX"}}
	templates := nodeTemplates(s)
	want := map[string]string{"LAX": "existing", "automatic": "automatic"}
	if len(templates) != len(want) {
		t.Fatalf("templates = %#v, want %d", templates, len(want))
	}
	for _, template := range templates {
		if outbound, ok := want[template.Name]; !ok || outbound != template.Outbound {
			t.Fatalf("unexpected template %#v in %#v", template, templates)
		}
	}
}

func TestLegacyDefaultNodeUsesDefaultServerTemplateName(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.json")
	if err := os.WriteFile(basePath, []byte(`{"route":{"final":"direct"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := &State{BaseConfig: basePath, ReservedAuthUsers: []string{"LAX"}, Users: []User{{Name: "alice", Nodes: []Node{{Name: "default"}}}}}
	normalizeLegacyNodeNames(s)
	if s.Users[0].Nodes[0].Name != "LAX" {
		t.Fatalf("legacy default node was not normalized: %#v", s.Users[0].Nodes[0])
	}
}

func TestRateMarkAllocationAndValidation(t *testing.T) {
	s := &State{Users: []User{{Name: "one", Nodes: []Node{{Name: "a", UploadMbps: 1, RateMark: rateMarkPrefix | 1}}}}}
	if got := allocateRateMark(s); got != rateMarkPrefix|2 {
		t.Fatalf("got mark 0x%x", got)
	}
	s.Users = append(s.Users, User{Name: "two", Nodes: []Node{{Name: "b", DownloadMbps: 1, RateMark: rateMarkPrefix | 1}}})
	if err := validateRateMarks(s); err == nil {
		t.Fatal("duplicate rate mark accepted")
	}
}

func TestDuplicateRateMarkAcrossSameNamedDeviceNodesIsRejected(t *testing.T) {
	mark := rateMarkPrefix | 9
	s := &State{Users: []User{{Name: "alice", Nodes: []Node{
		{Name: "LAX", Device: "phone", UploadMbps: 1, RateMark: mark},
		{Name: "LAX", Device: "pc", UploadMbps: 1, RateMark: mark},
	}}}}
	if err := validateRateMarks(s); err == nil {
		t.Fatal("duplicate mark across devices was accepted")
	}
}

func TestNftCountersExcludeDroppedBytesAndSurviveReplacement(t *testing.T) {
	mark := rateMarkPrefix | 1
	s := &State{
		Counters: map[string]int64{fmt.Sprintf("nft:%08x:upload", mark): 12345},
		Users:    []User{{Name: "alice", Nodes: []Node{{Name: "LAX", UploadMbps: 10, RateMark: mark}}}},
	}
	rules, err := renderNftables(s)
	if err != nil {
		t.Fatal(err)
	}
	drop := strings.Index(rules, "limit rate over 1250000 bytes/second")
	counter := strings.Index(rules, "counter packets 0 bytes 12345 comment \"alice/LAX upload\"")
	if drop < 0 || counter < 0 || counter <= drop || strings.Contains(rules[drop:counter], " counter ") {
		t.Fatalf("accepted-byte counter is not after the drop rule:\n%s", rules)
	}
}

func TestImportPreservesComplexAuthUserRules(t *testing.T) {
	cfg := map[string]any{"route": map[string]any{"rules": []any{
		map[string]any{"auth_user": []any{"alice"}, "outbound": "direct"},
		map[string]any{"auth_user": []any{"alice"}, "domain_suffix": []any{"example.com"}, "action": "reject"},
		map[string]any{"auth_user": []any{"alice"}, "outbound": "proxy", "domain_suffix": []any{"internal.example"}},
	}}}
	stripManagedRoutes(cfg)
	rules := cfg["route"].(map[string]any)["rules"].([]any)
	if len(rules) != 2 {
		t.Fatalf("complex auth_user rules were removed: %#v", rules)
	}
	if routes := routeMap(cfg); len(routes) != 0 {
		t.Fatalf("complex conditional route was treated as a default user route: %#v", routes)
	}
}

func TestRateTopologyChangeDetection(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	mark := rateMarkPrefix | 7
	s := &State{ConfigPath: configPath, Users: []User{{
		Name: "alice", Enabled: true,
		Nodes: []Node{{Name: "default", AuthUser: "alice:default", UUID: "u", UploadMbps: 10, RateMark: mark}},
	}}}
	configured := fmt.Sprintf(`{"outbounds":[{"type":"direct","tag":"sbmgr-rate","routing_mark":%d}]}`, mark)
	if err := os.WriteFile(configPath, []byte(configured), 0600); err != nil {
		t.Fatal(err)
	}
	if rateTopologyChanged(s) {
		t.Fatal("unchanged mark topology requested restart")
	}
	s.Users[0].Nodes[0].UploadMbps = 20
	if rateTopologyChanged(s) {
		t.Fatal("rate-only change requested restart")
	}
	s.Users[0].Nodes[0].UploadMbps = 0
	if rateTopologyChanged(s) {
		t.Fatal("removing a rate changed a mark that is retained for usage accounting")
	}
}

type fakeStatsService interface {
	QueryStats(context.Context, *queryStatsRequest) (*queryStatsResponse, error)
}

type fakeStatsServer struct{}

func (fakeStatsServer) QueryStats(_ context.Context, req *queryStatsRequest) (*queryStatsResponse, error) {
	if req.Pattern != "user>>>" {
		return nil, errors.New("unexpected pattern")
	}
	return &queryStatsResponse{Stat: []*stat{{Name: "user>>>alice:att>>>traffic>>>uplink", Value: 42}}}, nil
}

func TestQueryUserStatsWireCompatibility(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: "v2ray.core.app.stats.command.StatsService",
		HandlerType: (*fakeStatsService)(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: "QueryStats",
			Handler: func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
				req := new(queryStatsRequest)
				if err := dec(req); err != nil {
					return nil, err
				}
				return srv.(fakeStatsService).QueryStats(ctx, req)
			},
		}},
	}, fakeStatsServer{})
	go server.Serve(listener)
	defer server.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stats, err := queryUserStats(ctx, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].Value != 42 {
		t.Fatalf("unexpected response: %#v", stats)
	}
}
