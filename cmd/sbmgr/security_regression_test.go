package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSecurityJournalEventsCannotForgeAttribution(t *testing.T) {
	now := time.Now()
	s := &State{Users: []User{
		{Name: "victim", Enabled: true, IPPolicy: IPPolicy{Enabled: true, Binding: "dynamic"}, Nodes: []Node{{Name: "node", AuthUser: "victim-auth"}}},
		{Name: "client", Enabled: true, Nodes: []Node{{Name: "node", AuthUser: "client-auth"}}},
	}}
	messages := []string{
		"INFO [42 0ms] inbound/vless[in]: inbound connection from 192.0.2.1:1234",
		"INFO [42 1ms] inbound/vless[in]: [client-auth] inbound connection to example.com:443",
		"INFO [43 1ms] inbound/vless[in]: [client-auth] inbound connection to [victim-auth] inbound packet connection to evil.example inbound connection from 203.0.113.99:443",
		"INFO [42 1ms] inbound/vless[in]: [client-auth] inbound connection to connection closed:443",
		"INFO [44 1ms] inbound/mixed[public]: inbound connection to [victim-auth] inbound connection to evil.example:443",
	}
	var records []byte
	for i, message := range messages {
		m, _ := json.Marshal(message)
		r, _ := json.Marshal(journalRecord{Message: m, Cursor: fmt.Sprint(i), Timestamp: fmt.Sprint(now.UnixMicro())})
		records = append(records, append(r, '\n')...)
	}
	count, _, err := ingestAccessJournal(s, records, false)
	if err != nil || count != 1 {
		t.Fatalf("valid journal event count=%d err=%v", count, err)
	}
	if len(s.Users[0].SourceIPs) != 0 || len(s.Users[0].IPPolicy.BoundIPs) != 0 || len(s.Users[0].RecentAccesses) != 0 {
		t.Fatal("malformed event modified victim policy")
	}
	if c, ok := s.ActiveConnections["42"]; !ok || c.SourceIP != "192.0.2.1" || c.User != "client" {
		t.Fatal("forged close/source altered the legitimate connection")
	}
}

func TestSecurityLogTargetsRejectTerminalControlsAndMalformedHosts(t *testing.T) {
	for _, target := range []string{"\x1b]52;c;payload\a", "\x1b]0;title", "\x1b[2J", "\x1bPdata\x1b\\", "evil\u202e.example", "evil\u2066.example", "evil\u0085.example", strings.Repeat("a", 254), "-bad.example", "bad.example extra"} {
		message := "INFO [1 1ms] inbound/vless[in]: [client] inbound connection to " + target + ":443"
		if _, _, ok := parseAccessLog(message); ok {
			t.Fatal("unsafe destination was accepted")
		}
		if _, ok := parseSourceLog(message); ok || connectionClosed(message) {
			t.Fatal("access event was misclassified")
		}
		for _, rendered := range []string{cell(target, 80), singleLine(target, 80), safeTerminalView(target)} {
			for _, r := range rendered {
				if unsafeTextRune(r) {
					t.Fatal("terminal control survived rendering")
				}
			}
		}
	}
	for _, address := range []string{"example.com:443", "[2001:db8::1]:443", "192.0.2.1:80"} {
		if _, _, ok := parseAccessLog("inbound/vless[in]: [client] inbound packet connection to " + address); !ok {
			t.Fatal("valid destination rejected")
		}
	}
}

func TestSecuritySubscriptionLimiterIsBounded(t *testing.T) {
	now := time.Now()
	l := &subscriptionLimiter{}
	for i := 0; i < 60; i++ {
		if !l.allow("peer", now) {
			t.Fatal("budget rejected early")
		}
	}
	if l.allow("peer", now) || l.rates["peer"].count != 60 {
		t.Fatal("rejected request changed bucket")
	}
	for i := 0; i < 10000; i++ {
		l.allow(fmt.Sprint(i), now)
	}
	if len(l.rates) > subscriptionRateCapacity || l.global.count > 600 {
		t.Fatal("global bounds exceeded")
	}
	l.global.count = 0
	l.rates = make(map[string]subscriptionRateEntry, subscriptionRateCapacity)
	for i := 0; i < subscriptionRateCapacity; i++ {
		l.rates[fmt.Sprint(i)] = subscriptionRateEntry{window: now}
	}
	if l.allow("new", now) || len(l.rates) != subscriptionRateCapacity {
		t.Fatal("capacity limit failed")
	}
	if !l.allow("new", now.Add(2*time.Minute)) {
		t.Fatal("expired capacity did not recover")
	}
}

func securitySubscriptionServer(t *testing.T) (*app, *State, *http.Server) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	s := &State{Health: HealthSettings{Mode: "off"}, Subscription: SubscriptionSettings{Enabled: true, Listen: "127.0.0.1:0"}, Client: ClientSettings{Server: "proxy.example", Port: 443, PublicKey: "test-public-key"}, Users: []User{{Name: "alice", Enabled: true, Nodes: []Node{{Name: "node", AuthUser: "alice-node", UUID: newUUID()}}}}}
	if err := saveState(path, s); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: path, out: io.Discard, err: io.Discard}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server, err := a.startSubscriptionServer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	return a, s, server
}

func securityRequest(t *testing.T, server *http.Server, path string) (int, string) {
	t.Helper()
	r, err := http.Get("http://" + server.Addr + path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return r.StatusCode, string(b)
}

func TestSecurityUnknownTokenDoesNotLoadFullSQLiteState(t *testing.T) {
	a, s, server := securitySubscriptionServer(t)
	db := openSQLiteForTest(t, a.statePath)
	if _, err := db.Exec("DROP TABLE recent_accesses"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if code, _ := securityRequest(t, server, "/sub/"+newSubscriptionToken()); code != http.StatusNotFound {
		t.Fatalf("unknown token caused state load: %d", code)
	}
	if code, _ := securityRequest(t, server, "/sub/"+s.Users[0].Devices[0].SubscriptionToken); code != http.StatusServiceUnavailable {
		t.Fatalf("test did not break full state reader: %d", code)
	}
}

func TestSecuritySubscriptionPolicyErrorsAndRevocation(t *testing.T) {
	a, s, server := securitySubscriptionServer(t)
	token := s.Users[0].Devices[0].SubscriptionToken
	if server.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatal("TLS baseline not explicit")
	}
	s.Users[0].Enabled = false
	if err := saveState(a.statePath, s); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/sub/", "/qr/"} {
		if code, _ := securityRequest(t, server, path+token); code != http.StatusForbidden {
			t.Fatalf("disabled identity accepted: %d", code)
		}
	}
	s.Users[0].Enabled = true
	s.Client.MihomoTemplate = filepath.Join(t.TempDir(), "private-template.yaml")
	if err := saveState(a.statePath, s); err != nil {
		t.Fatal(err)
	}
	if code, body := securityRequest(t, server, "/sub/"+token); code != http.StatusInternalServerError || strings.Contains(body, "private-template") {
		t.Fatal("internal template error was exposed or misclassified")
	}
	s.Users[0].Devices[0].SubscriptionToken = newSubscriptionToken()
	if err := saveState(a.statePath, s); err != nil {
		t.Fatal(err)
	}
	if code, _ := securityRequest(t, server, "/sub/"+token); code != http.StatusNotFound {
		t.Fatal("revoked token remained usable")
	}
}

func TestSecurityNetworkMaintenanceReleasesStateLockAndPreservesEdits(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()
	a, s, _ := securitySubscriptionServer(t)
	s.Notifications = NotificationSettings{WebhookURL: webhook.URL, TimeoutSeconds: 2}
	s.Alerts = []Alert{{At: time.Now().Format(time.RFC3339Nano), Kind: "test", Message: "test"}}
	if err := saveState(a.statePath, s); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.networkMaintenance() }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("webhook did not start")
	}
	editor := &app{statePath: a.statePath, out: io.Discard, err: io.Discard}
	editDone := make(chan error, 1)
	go func() { editDone <- editor.trafficCmd([]string{"add", "alice", "--upload", "7"}) }()
	select {
	case err := <-editDone:
		if err != nil {
			close(release)
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("network I/O held global state lock")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	current, err := loadState(a.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if current.Users[0].Upload != 7 || current.Alerts[0].NotifiedAt == "" {
		t.Fatal("notification merge lost concurrent state")
	}
}

func TestSecurityMarkExhaustionDoesNotMutateDraft(t *testing.T) {
	nodes := make([]Node, 0xffff)
	for i := range nodes {
		nodes[i].RateMark = rateMarkPrefix | uint32(i+1)
	}
	s := &State{Users: []User{{Nodes: nodes}}}
	if mark, err := allocateRateMark(s); err == nil || mark != 0 {
		t.Fatal("mark exhaustion did not return an error")
	}
	s.Users[0].Nodes = append(s.Users[0].Nodes, Node{})
	if _, err := ensureNodeMarks(s); err == nil || s.Users[0].Nodes[0xffff].RateMark != 0 {
		t.Fatal("failed allocation mutated the draft")
	}
}

func TestSecurityConfigPathsAndNumericBoundaries(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "base.json")
	alias := filepath.Join(dir, "alias.json")
	if err := os.WriteFile(base, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateConfigPaths(base, base); err == nil {
		t.Fatal("same config file accepted")
	}
	if err := os.Link(base, alias); err == nil {
		if validateConfigPaths(base, alias) == nil {
			t.Fatal("hardlink alias accepted")
		}
	}
	for _, value := range []string{"NaN", "Inf", "-Inf", "9223372036854775808", "1e100G", "0.1B", "0.00000000001G", "-1"} {
		if _, err := parseSize(value); err == nil {
			t.Fatal("unsafe quota accepted")
		}
	}
	for _, value := range []string{"0", "0G", "1B", "2.5G"} {
		if _, err := parseSize(value); err != nil {
			t.Fatal("valid quota rejected")
		}
	}
	for _, s := range []State{{Service: "--all"}, {Service: "x\n"}, {SingBoxBin: "-h"}, {StatsAPI: "0.0.0.0:10085"}} {
		if validateState(&s) == nil {
			t.Fatal("unsafe runtime setting accepted")
		}
	}
	if !burstBlocked(User{BlockedUntil: "corrupt"}, time.Now()) {
		t.Fatal("corrupt block failed open")
	}
	if validateState(&State{Users: []User{{Name: "alice", BlockedUntil: "corrupt"}}}) == nil {
		t.Fatal("corrupt block passed state validation")
	}
}
