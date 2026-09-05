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

func TestSecurityIPGraceSurvivesRestartAndAutoSlotsExpire(t *testing.T) {
	now := time.Now()
	s := &State{Users: []User{{Name: "alice", Enabled: true, IPPolicy: IPPolicy{Enabled: true, Binding: "dynamic", HandoverSeconds: 3600}}}}
	u := &s.Users[0]
	recordUserSourceIP(s, u, "node", "192.0.2.1", now)
	path := filepath.Join(t.TempDir(), "state.db")
	if err := saveState(path, s); err != nil {
		t.Fatal(err)
	}
	s, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	u = &s.Users[0]
	s.ActiveConnections = nil
	recordUserSourceIP(s, u, "node", "192.0.2.2", now.Add(time.Minute))
	if !containsIP(u.IPPolicy.BoundIPs, "192.0.2.1") {
		t.Fatal("restart bypassed handover grace")
	}
	recordUserSourceIP(s, u, "node", "192.0.2.2", now.Add(time.Hour+time.Second))
	if !containsIP(u.IPPolicy.BoundIPs, "192.0.2.2") {
		t.Fatal("expired grace never released binding")
	}
	u.IPPolicy.Binding = "auto"
	if !releaseIdleBindings(s, now.Add(time.Hour+autoBindingTTL+2*time.Second)) || len(u.IPPolicy.BoundIPs) != 0 || !s.IPApplyPending {
		t.Fatal("idle automatic binding was not released")
	}
}

func TestSecurityConnectionBlockRecoversWithoutEnablingManualDisable(t *testing.T) {
	now := time.Now()
	s := &State{Users: []User{{Name: "alice", Enabled: true, Devices: []Device{{Name: "phone", Enabled: true, Access: AccessPolicy{MaxConnections: 1, ConnectionAction: "disable-device"}}}}}}
	u := &s.Users[0]
	d := &u.Devices[0]
	if !evaluateConnectionPolicy(s, u, d, &d.Access, 2, now) || d.Enabled || d.Access.ConnectionBlockedUntil == "" {
		t.Fatal("device punishment lacks recovery deadline")
	}
	if !recoverConnectionBlocks(s, now.Add(connectionBlockDuration+time.Second)) || !d.Enabled || !s.StatsApplyPending {
		t.Fatal("device did not recover")
	}
	u.Access = AccessPolicy{MaxConnections: 1, ConnectionAction: "disable-user"}
	if !evaluateConnectionPolicy(s, u, nil, &u.Access, 2, now) {
		t.Fatal("user punishment missing")
	}
	u.DisabledReason = "manual"
	recoverConnectionBlocks(s, now.Add(connectionBlockDuration+time.Second))
	if u.Enabled {
		t.Fatal("manual disable was overwritten")
	}
}

func TestSecuritySchemaAndPolicyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s := sqliteFixtureState(2)
	s.Users[0].IPPolicy = IPPolicy{Enabled: true, Binding: "dynamic", BoundIPs: []string{"203.0.113.10"}}
	if err := saveState(path, s); err != nil {
		t.Fatal(err)
	}
	s.Version = 9
	s.Users[0].IPPolicy.BoundLastSeen = nil
	if err := saveSQLiteState(path, s); err != nil {
		t.Fatal(err)
	}
	db := openSQLiteForTest(t, path)
	for _, q := range []string{"DROP INDEX devices_subscription_token_idx", "PRAGMA user_version=1", "UPDATE metadata SET value='1' WHERE key='schema_version'"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	a := &app{statePath: path, out: io.Discard, err: io.Discard}
	migrated, err := a.loadCanonicalState()
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Version != stateVersion || migrated.Users[0].IPPolicy.BoundLastSeen["203.0.113.10"] == "" {
		t.Fatal("policy version migration lost trusted activity")
	}
	db = openSQLiteForTest(t, path)
	var count int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='devices_subscription_token_idx'").Scan(&count); err != nil || count != 1 {
		t.Fatal("token index migration missing")
	}
	db.Close()
	if _, err := a.loadCanonicalState(); err != nil {
		t.Fatal("migration not idempotent", err)
	}
}

func TestSecurityImportRetainsOtherIdentityRoutesAndRejectsCollision(t *testing.T) {
	cfg := map[string]any{"inbounds": []any{
		map[string]any{"type": "vless", "tag": "managed", "users": []any{map[string]any{"name": "alice", "uuid": newUUID()}}},
		map[string]any{"type": "trojan", "tag": "other", "users": []any{map[string]any{"name": "bob", "password": "test-value"}}},
	}, "route": map[string]any{"rules": []any{
		map[string]any{"auth_user": []any{"alice", "bob"}, "outbound": "private-exit"},
		map[string]any{"auth_user": []any{"bob"}, "domain_suffix": []any{"example.com"}, "outbound": "other-exit"},
		map[string]any{"auth_user": "bob", "outbound": "private-exit"},
	}}}
	s, clean, err := importConfig(cfg, "managed", true)
	if err != nil {
		t.Fatal(err)
	}
	if routeMap(clean)["bob"] != "private-exit" || len(clean["route"].(map[string]any)["rules"].([]any)) != 3 {
		t.Fatal("unmanaged route removed")
	}
	if uniqueAuthUser(s, "bob") == "bob" {
		t.Fatal("other inbound identity was not reserved")
	}
	s.Users[0].Nodes[0].AuthUser = "bob"
	if validateBaseIdentities(s, clean) == nil {
		t.Fatal("base template identity collision accepted")
	}
	path := filepath.Join(t.TempDir(), "base.json")
	raw, _ := json.Marshal(clean)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	s.BaseConfig = path
	if _, err := renderConfig(s); err == nil {
		t.Fatal("render accepted base template identity collision")
	}
}

func TestSecurityMarkKeysPreventLongNamesAndLabelCollisions(t *testing.T) {
	s := &State{Users: []User{
		{Name: strings.Repeat("中", 40), Nodes: []Node{{Name: strings.Repeat("文", 40), Device: strings.Repeat("设", 40), RateMark: rateMarkPrefix | 1}}},
		{Name: "second", Nodes: []Node{{Name: "node", RateMark: rateMarkPrefix | 2}}},
	}}
	rules, err := renderNftables(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(rules, "\n") {
		if p := strings.Index(line, " comment "); p >= 0 && len(line[p+9:]) > 128 {
			t.Fatal("nft comment exceeds byte limit")
		}
	}
	legacy := `{"nftables":[{"rule":{"chain":"upload","comment":"collision upload","expr":[{"match":{"op":"==","left":{"meta":{"key":"mark"}},"right":1396834305}},{"counter":{"bytes":11}}]}},{"rule":{"chain":"upload","comment":"collision upload","expr":[{"match":{"op":"==","left":{"meta":{"key":"mark"}},"right":1396834306}},{"counter":{"bytes":22}}]}}]}`
	counters, err := parseNftCounterJSON([]byte(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if counters["sbmgr:53420001 upload"] != 11 || counters["sbmgr:53420002 upload"] != 22 {
		t.Fatal("mark-based legacy counter migration collided")
	}
}

func TestSecurityConnectionTrackingBoundAndMigration(t *testing.T) {
	now := time.Now()
	s := &State{}
	for i := 0; i < activeConnectionLimit+100; i++ {
		trackActiveConnection(s, ActiveConnection{ID: fmt.Sprint(i), LastSeen: now.Add(time.Duration(i) * time.Second).Format(time.RFC3339)})
	}
	if len(s.ActiveConnections) != activeConnectionLimit {
		t.Fatal("active tracking exceeded limit")
	}
	if _, old := s.ActiveConnections["0"]; old {
		t.Fatal("oldest connection was retained")
	}
	for i := 0; i < 10000; i++ {
		trackActiveConnection(s, ActiveConnection{ID: "hot", LastSeen: now.Add(time.Hour).Format(time.RFC3339)})
	}
	if s.connectionIndex.Len() > activeConnectionLimit {
		t.Fatal("heap grew on repeated updates")
	}
}

func TestSecurityDefaultViewsAndAuditHideCredentials(t *testing.T) {
	s := sqliteFixtureState(0)
	s.Subscription.Enabled = true
	u := &s.Users[0]
	m := tuiModel{state: s, selected: u.Name, width: 160, height: 45}
	for _, view := range []string{m.renderDetail(), m.renderDevices(), m.renderSubscriptions()} {
		for _, secret := range []string{u.Nodes[0].UUID, u.Nodes[0].AuthUser, u.Devices[0].SubscriptionToken} {
			if strings.Contains(view, secret) {
				t.Fatal("default view disclosed credentials")
			}
		}
	}
	value := "https://example.com/secret-webhook-token"
	for _, args := range [][]string{{"set", "--webhook", value}, {"set", "--webhook=" + value}, {"set", "--future-secret", value}} {
		if strings.Contains(strings.Join(sanitizeAuditArgs(args), " "), "secret-webhook-token") {
			t.Fatal("audit leaked URL credential")
		}
	}
}

func BenchmarkSecurityRecentAccessHotTarget(b *testing.B) {
	for _, size := range []int{1, 1000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			u := User{}
			now := time.Now()
			for i := 0; i < size; i++ {
				recordRecentAccess(&u, nil, "node", fmt.Sprintf("site-%d.example", i), now)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				recordRecentAccess(&u, nil, "node", "site-0.example", now)
			}
		})
	}
}
