package main

import (
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseSourceAndConnectionIDFromJournalLogShape(t *testing.T) {
	message := "-0700 2026-08-03 03:09:37 \x1b[36mINFO\x1b[0m [\x1b[38;5;29m1781284324\x1b[0m 0ms] inbound/vless[vless-in]: inbound connection from 192.0.2.10:8280"
	if got := parseConnectionID(message); got != "1781284324" {
		t.Fatalf("connection id = %q", got)
	}
	ip, ok := parseSourceLog(message)
	if !ok || ip != "192.0.2.10" {
		t.Fatalf("source = %q, ok=%v", ip, ok)
	}
	authLine := "-0700 INFO [\x1b[38;5;29m1781284324\x1b[0m 453ms] inbound/vless[vless-in]: [user-a:node-a] inbound connection to service.example:443"
	auth, target, ok := parseAccessLog(authLine)
	if !ok || auth != "user-a:node-a" || target != "service.example" || parseConnectionID(authLine) != "1781284324" {
		t.Fatalf("auth=%q target=%q ok=%v", auth, target, ok)
	}
}

func TestIPPolicyAutoBindsFirstSourceAndAlertsViolation(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	s := &State{Users: []User{{Name: "alice", Enabled: true, IPPolicy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "auto", MaxIPs: 1}}}}
	u := &s.Users[0]
	if !recordUserSourceIP(s, u, "Node A", "203.0.113.10", now) {
		t.Fatal("first source was not recorded")
	}
	if len(u.IPPolicy.BoundIPs) != 1 || u.IPPolicy.BoundIPs[0] != "203.0.113.10" || !s.IPApplyPending {
		t.Fatalf("first source not bound: %#v", u.IPPolicy)
	}
	if len(s.Alerts) != 1 || s.Alerts[0].Kind != "ip_bound" {
		t.Fatalf("missing bind alert: %#v", s.Alerts)
	}
	recordUserSourceIP(s, u, "Relay A", "198.51.100.20", now.Add(time.Minute))
	if len(u.IPPolicy.BoundIPs) != 1 || u.SourceIPs["198.51.100.20"].Violations != 1 {
		t.Fatalf("second source was not treated as violation: %#v", u)
	}
	if len(s.Alerts) != 2 || s.Alerts[1].Kind != "ip_violation" {
		t.Fatalf("missing violation alert: %#v", s.Alerts)
	}
}

func TestDynamicSingleActiveIPAllowsSameIPAndSafeHandover(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	s := &State{
		ActiveConnections: map[string]ActiveConnection{
			"phone": {ID: "phone", User: "alice", Device: "手机", SourceIP: "203.0.113.10", LastSeen: now.Format(time.RFC3339)},
		},
		Users: []User{{Name: "alice", Enabled: true, IPPolicy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "dynamic", MaxIPs: 1}}},
	}
	u := &s.Users[0]
	recordUserSourceIP(s, u, "Node A", "203.0.113.10", now)
	if got := u.IPPolicy.BoundIPs; len(got) != 1 || got[0] != "203.0.113.10" || !s.IPApplyPending {
		t.Fatalf("initial dynamic IP not learned: %#v", u.IPPolicy)
	}

	s.IPApplyPending = false
	s.ActiveConnections["pc"] = ActiveConnection{ID: "pc", User: "alice", Device: "电脑", SourceIP: "203.0.113.10", LastSeen: now.Add(time.Minute).Format(time.RFC3339)}
	recordUserSourceIP(s, u, "Relay A", "203.0.113.10", now.Add(time.Minute))
	if u.SourceIPs["203.0.113.10"].Violations != 0 || s.IPApplyPending {
		t.Fatalf("same public IP was treated as a conflict: %#v", u.SourceIPs)
	}

	s.ActiveConnections["other-ip"] = ActiveConnection{ID: "other-ip", User: "alice", Device: "平板", SourceIP: "198.51.100.20", LastSeen: now.Add(2 * time.Minute).Format(time.RFC3339)}
	recordUserSourceIP(s, u, "Relay B", "198.51.100.20", now.Add(2*time.Minute))
	if got := u.IPPolicy.BoundIPs; len(got) != 1 || got[0] != "203.0.113.10" {
		t.Fatalf("competing IP replaced the active IP: %#v", got)
	}
	if u.SourceIPs["198.51.100.20"].Violations != 1 {
		t.Fatalf("competing IP was not recorded as a violation: %#v", u.SourceIPs)
	}

	delete(s.ActiveConnections, "phone")
	delete(s.ActiveConnections, "pc")
	recordUserSourceIP(s, u, "Relay B", "198.51.100.20", now.Add(3*time.Minute))
	if got := u.IPPolicy.BoundIPs; len(got) != 1 || got[0] != "198.51.100.20" || !s.IPApplyPending {
		t.Fatalf("new IP did not take over after the old IP became inactive: %#v", u.IPPolicy)
	}
	if len(s.Alerts) == 0 || s.Alerts[len(s.Alerts)-1].Kind != "ip_switched" {
		t.Fatalf("handover alert missing: %#v", s.Alerts)
	}
}

func TestDynamicSingleActiveIPIgnoresRejectedCandidatesDuringHandover(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	s := &State{
		ActiveConnections: map[string]ActiveConnection{
			// The formerly bound address has already been quiet long enough to
			// stop being active, but remains in the persisted tracking map until
			// the next prune.
			"old": {
				ID: "old", User: "alice", Device: "phone", SourceIP: "203.0.113.10",
				LastSeen: now.Add(-activeConnectionTTL - time.Second).Format(time.RFC3339),
			},
			// A mobile network or a retrying client can present more than one new
			// public address.  Both were observed before the source-IP reject rule
			// ran, so neither is proof of an accepted competing session.
			"candidate": {
				ID: "candidate", User: "alice", Device: "phone", SourceIP: "198.51.100.20",
				LastSeen: now.Format(time.RFC3339),
			},
			"rejected-racer": {
				ID: "rejected-racer", User: "alice", Device: "phone", SourceIP: "192.0.2.30",
				LastSeen: now.Format(time.RFC3339),
			},
		},
		Users: []User{{
			Name: "alice", Enabled: true,
			IPPolicy: IPPolicy{
				Enabled: true, Mode: "enforce", Binding: "dynamic", MaxIPs: 1,
				BoundIPs: []string{"203.0.113.10"},
			},
		}},
	}

	u := &s.Users[0]
	recordUserSourceIP(s, u, "Node A", "198.51.100.20", now)
	if got := u.IPPolicy.BoundIPs; len(got) != 1 || got[0] != "198.51.100.20" {
		t.Fatalf("rejected candidate prevented safe handover: %#v", got)
	}
	if !s.IPApplyPending {
		t.Fatal("successful handover did not request a config apply")
	}
	if got := u.SourceIPs["198.51.100.20"].Violations; got != 0 {
		t.Fatalf("successful handover was recorded as a violation: %d", got)
	}
}

func TestDynamicSingleActiveIPHandoverGraceWithoutCloseLog(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	const oldIP = "203.0.113.10"
	const newIP = "198.51.100.20"

	for _, tc := range []struct {
		name       string
		lastSeen   time.Time
		wantIP     string
		violations int64
	}{
		{
			name:       "incumbent one second inside grace is protected",
			lastSeen:   now.Add(-(defaultIPPolicyHandoverSeconds - 1) * time.Second),
			wantIP:     oldIP,
			violations: 1,
		},
		{
			name:       "incumbent one second beyond grace is replaced despite missing close",
			lastSeen:   now.Add(-(defaultIPPolicyHandoverSeconds + 1) * time.Second),
			wantIP:     newIP,
			violations: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &State{
				ActiveConnections: map[string]ActiveConnection{
					// Deliberately retain the old entry.  Clean transport closes are
					// commonly absent at info log level, so handover cannot rely on a
					// close record arriving.
					"old-without-close": {
						ID: "old-without-close", User: "alice", Device: "phone", SourceIP: oldIP,
						LastSeen: tc.lastSeen.Format(time.RFC3339),
					},
					"new": {
						ID: "new", User: "alice", Device: "phone", SourceIP: newIP,
						LastSeen: now.Format(time.RFC3339),
					},
				},
				Users: []User{{
					Name: "alice", Enabled: true,
					IPPolicy: IPPolicy{
						Enabled: true, Mode: "enforce", Binding: "dynamic", MaxIPs: 1,
						BoundIPs: []string{oldIP},
					},
				}},
			}

			u := &s.Users[0]
			recordUserSourceIP(s, u, "Node A", newIP, now)
			if got := u.IPPolicy.BoundIPs; len(got) != 1 || got[0] != tc.wantIP {
				t.Fatalf("bound IP = %#v, want %s", got, tc.wantIP)
			}
			if got := u.SourceIPs[newIP].Violations; got != tc.violations {
				t.Fatalf("violations = %d, want %d", got, tc.violations)
			}
		})
	}
}

func TestDynamicSingleActiveIPUsesConfiguredHandoverGrace(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	s := &State{
		ActiveConnections: map[string]ActiveConnection{
			"old": {
				ID: "old", User: "alice", SourceIP: "203.0.113.10",
				LastSeen: now.Add(-90 * time.Second).Format(time.RFC3339),
			},
			"new": {
				ID: "new", User: "alice", SourceIP: "198.51.100.20",
				LastSeen: now.Format(time.RFC3339),
			},
		},
		Users: []User{{
			Name: "alice", Enabled: true,
			IPPolicy: IPPolicy{
				Enabled: true, Mode: "enforce", Binding: "dynamic", MaxIPs: 1,
				HandoverSeconds: 120, BoundIPs: []string{"203.0.113.10"},
			},
		}},
	}

	u := &s.Users[0]
	recordUserSourceIP(s, u, "Node A", "198.51.100.20", now)
	if got := u.IPPolicy.BoundIPs; len(got) != 1 || got[0] != "203.0.113.10" {
		t.Fatalf("configured grace was ignored: %#v", got)
	}

	u.IPPolicy.HandoverSeconds = 30
	recordUserSourceIP(s, u, "Node A", "198.51.100.20", now)
	if got := u.IPPolicy.BoundIPs; len(got) != 1 || got[0] != "198.51.100.20" {
		t.Fatalf("short configured grace did not permit handover: %#v", got)
	}
}

func TestMonitorOnlyDynamicIPChangeDoesNotQueueConfigRestart(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	s := &State{Users: []User{{
		Name: "alice", Enabled: true,
		IPPolicy: IPPolicy{Enabled: true, Mode: "monitor", Binding: "dynamic", MaxIPs: 1},
	}}}
	u := &s.Users[0]
	recordUserSourceIP(s, u, "Node A", "198.51.100.20", now)
	if got := u.IPPolicy.BoundIPs; len(got) != 1 || got[0] != "198.51.100.20" {
		t.Fatalf("monitor-only policy did not learn the source: %#v", got)
	}
	if s.IPApplyPending {
		t.Fatal("monitor-only source change queued an unnecessary service restart")
	}
}

func TestHasEnforcedDynamicIPPolicy(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	dynamic := IPPolicy{Enabled: true, Mode: "enforce", Binding: "dynamic", MaxIPs: 1}
	monitor := IPPolicy{Enabled: true, Mode: "monitor", Binding: "dynamic", MaxIPs: 1}
	auto := IPPolicy{Enabled: true, Mode: "enforce", Binding: "auto", MaxIPs: 1}

	for _, tc := range []struct {
		name string
		s    *State
		want bool
	}{
		{name: "nil state", s: nil, want: false},
		{name: "no policy", s: &State{Users: []User{{Name: "alice", Enabled: true}}}, want: false},
		{name: "user dynamic", s: &State{Users: []User{{Name: "alice", Enabled: true, IPPolicy: dynamic}}}, want: true},
		{name: "monitor only", s: &State{Users: []User{{Name: "alice", Enabled: true, IPPolicy: monitor}}}, want: false},
		{name: "automatic binding", s: &State{Users: []User{{Name: "alice", Enabled: true, IPPolicy: auto}}}, want: false},
		{name: "disabled user", s: &State{Users: []User{{Name: "alice", Enabled: false, IPPolicy: dynamic}}}, want: false},
		{name: "device dynamic", s: &State{Users: []User{{Name: "alice", Enabled: true, Devices: []Device{{Name: "phone", Enabled: true, IPPolicy: dynamic}}}}}, want: true},
		{name: "disabled device", s: &State{Users: []User{{Name: "alice", Enabled: true, Devices: []Device{{Name: "phone", Enabled: false, IPPolicy: dynamic}}}}}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasEnforcedDynamicIPPolicy(tc.s, now); got != tc.want {
				t.Fatalf("hasEnforcedDynamicIPPolicy() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDynamicSingleActiveIPStillProtectsRecentlyActiveBoundAddress(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	s := &State{
		ActiveConnections: map[string]ActiveConnection{
			"incumbent": {
				ID: "incumbent", User: "alice", SourceIP: "203.0.113.10",
				LastSeen: now.Format(time.RFC3339),
			},
			"candidate": {
				ID: "candidate", User: "alice", SourceIP: "198.51.100.20",
				LastSeen: now.Format(time.RFC3339),
			},
			"rejected-racer": {
				ID: "rejected-racer", User: "alice", SourceIP: "192.0.2.30",
				LastSeen: now.Format(time.RFC3339),
			},
		},
		Users: []User{{
			Name: "alice", Enabled: true,
			IPPolicy: IPPolicy{
				Enabled: true, Mode: "enforce", Binding: "dynamic", MaxIPs: 1,
				BoundIPs: []string{"203.0.113.10"},
			},
		}},
	}

	u := &s.Users[0]
	recordUserSourceIP(s, u, "Node A", "198.51.100.20", now)
	if got := u.IPPolicy.BoundIPs; len(got) != 1 || got[0] != "203.0.113.10" {
		t.Fatalf("candidate replaced a recently active bound address: %#v", got)
	}
	if got := u.SourceIPs["198.51.100.20"].Violations; got != 1 {
		t.Fatalf("blocked candidate violations = %d, want 1", got)
	}
}

func TestDynamicIPPolicyLearnsBeforeGeneratingRestrictionRule(t *testing.T) {
	s := &State{Users: []User{{
		Name: "alice", Enabled: true,
		IPPolicy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "dynamic", MaxIPs: 1},
		Nodes:    []Node{{AuthUser: "alice:node-a"}},
	}}}
	if rules := ipRestrictionRules(s, time.Now()); len(rules) != 0 {
		t.Fatalf("unlearned dynamic policy rejected the first connection: %#v", rules)
	}
	s.Users[0].IPPolicy.BoundIPs = []string{"203.0.113.10"}
	if rules := ipRestrictionRules(s, time.Now()); len(rules) != 1 {
		t.Fatalf("learned dynamic policy did not generate a restriction: %#v", rules)
	}
	invalid := s.Users[0].IPPolicy
	invalid.MaxIPs = 2
	if err := validateIPPolicy(invalid); err == nil {
		t.Fatal("dynamic policy accepted more than one active IP")
	}
}

func TestNewUserDefaultsToDynamicSingleActiveIP(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(statePath, &State{Version: stateVersion}); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.userCmd([]string{"add", "alice", "--quota", "20G", "--node-name", "Node A"}); err != nil {
		t.Fatal(err)
	}
	s, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	policy := s.Users[0].IPPolicy
	if !policy.Enabled || policy.Mode != "enforce" || policy.Binding != "dynamic" || policy.MaxIPs != 1 || policy.HandoverSeconds != defaultIPPolicyHandoverSeconds {
		t.Fatalf("new user policy = %#v", policy)
	}
}

func TestUserAndDeviceIPPolicyHandoverSecondsPersist(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(statePath, &State{
		Version: stateVersion,
		Users: []User{{
			Name: "alice", Enabled: true,
			Devices: []Device{{
				Name: defaultDeviceName, Enabled: true, CreatedAt: time.Now().Format(time.RFC3339),
				SubscriptionToken: strings.Repeat("a", 32),
			}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.userCmd([]string{"set", "alice", "--ip-handover-seconds", "45"}); err != nil {
		t.Fatal(err)
	}
	if err := a.deviceCmd([]string{"set", "alice", defaultDeviceName, "--ip-handover-seconds", "30"}); err != nil {
		t.Fatal(err)
	}
	s, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Users[0].IPPolicy.HandoverSeconds; got != 45 {
		t.Fatalf("user handover seconds = %d, want 45", got)
	}
	if got := s.Users[0].Devices[0].IPPolicy.HandoverSeconds; got != 30 {
		t.Fatalf("device handover seconds = %d, want 30", got)
	}
}

func TestEnablingDynamicPolicySeedsSingleActiveSource(t *testing.T) {
	now := time.Now()
	statePath := filepath.Join(t.TempDir(), "state.json")
	s := &State{
		Version: stateVersion,
		ActiveConnections: map[string]ActiveConnection{
			"one": {ID: "one", User: "alice", SourceIP: "203.0.113.10", LastSeen: now.Format(time.RFC3339)},
			"two": {ID: "two", User: "alice", SourceIP: "203.0.113.10", LastSeen: now.Format(time.RFC3339)},
		},
		Users: []User{{Name: "alice", Enabled: true}},
	}
	if err := saveState(statePath, s); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.userCmd([]string{"set", "alice", "--ip-enabled", "true", "--ip-mode", "enforce", "--ip-binding", "dynamic", "--ip-max", "1", "--ip-allowed", ""}); err != nil {
		t.Fatal(err)
	}
	s, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	policy := s.Users[0].IPPolicy
	if len(policy.BoundIPs) != 1 || policy.BoundIPs[0] != "203.0.113.10" || !s.IPApplyPending {
		t.Fatalf("active source was not seeded: policy=%#v pending=%v", policy, s.IPApplyPending)
	}
}

func TestIPRestrictionRuleCombinesAuthAndInvertedSource(t *testing.T) {
	s := &State{Users: []User{{
		Name: "alice", Enabled: true,
		IPPolicy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "manual", MaxIPs: 1, BoundIPs: []string{"203.0.113.10"}},
		Nodes:    []Node{{AuthUser: "alice:node-a"}, {AuthUser: "alice:relay-a"}},
	}}}
	rules := ipRestrictionRules(s, time.Now())
	if len(rules) != 1 {
		t.Fatalf("rules = %#v", rules)
	}
	b, _ := json.Marshal(rules[0])
	text := string(b)
	for _, want := range []string{`"type":"logical"`, `"mode":"and"`, `"action":"reject"`, `"auth_user":["alice:node-a","alice:relay-a"]`, `"source_ip_cidr":["203.0.113.10"]`, `"invert":true`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
	s.Users[0].IPPolicy.Mode = "monitor"
	if got := ipRestrictionRules(s, time.Now()); len(got) != 0 {
		t.Fatalf("monitor mode generated reject rules: %#v", got)
	}
}

func TestTemporaryIPOverrideExpiresAndRestoresBinding(t *testing.T) {
	now := time.Now()
	s := &State{Users: []User{{Name: "alice", IPPolicy: IPPolicy{
		Enabled: true, Mode: "enforce", Binding: "manual", MaxIPs: 1,
		BoundIPs: []string{"203.0.113.10"}, TemporaryIPs: []string{"198.51.100.20"}, TemporaryUntil: now.Add(-time.Second).Format(time.RFC3339Nano),
	}}}}
	if !expireTemporaryIPPolicies(s, now) || !s.IPApplyPending {
		t.Fatal("expired override did not request config apply")
	}
	if len(s.Users[0].IPPolicy.TemporaryIPs) != 0 || len(s.Alerts) != 1 || s.Alerts[0].Kind != "ip_override_expired" {
		t.Fatalf("override not cleared: %#v", s.Users[0].IPPolicy)
	}
	if got := activePolicyIPs(s.Users[0].IPPolicy, now); len(got) != 1 || got[0] != "203.0.113.10" {
		t.Fatalf("fixed binding not restored: %#v", got)
	}
}

func TestIPPolicyCUIFormPersistsCustomRules(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(statePath, &State{Version: stateVersion, Users: []User{{Name: "alice", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	m := tuiModel{a: a, state: &State{Users: []User{{Name: "alice", Enabled: true}}}}
	m.openIPPolicyForm(m.state.Users[0])
	m.form.fields[0].value = "开启"
	m.form.fields[1].value = "强制限制"
	m.form.fields[2].value = "手动指定"
	m.form.fields[3].value = "2"
	m.form.fields[4].value = "45"
	m.form.fields[5].value = "203.0.113.10,198.51.100.20"
	m.form.fields[6].value = "192.0.2.30"
	m.form.fields[7].value = "60"
	_, cmd := m.submitForm()
	msg := cmd().(tuiActionMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	s, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	policy := s.Users[0].IPPolicy
	if !policy.Enabled || policy.Mode != "enforce" || policy.Binding != "manual" || policy.MaxIPs != 2 || policy.HandoverSeconds != 45 || len(policy.BoundIPs) != 2 || len(policy.TemporaryIPs) != 1 || !s.IPApplyPending {
		t.Fatalf("unexpected policy: %#v", policy)
	}
}
