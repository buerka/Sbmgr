package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDeviceAccessRulesOnlyTargetDeviceAuthUsers(t *testing.T) {
	s := &State{Users: []User{{Name: "alice", Enabled: true, Devices: []Device{
		{Name: "phone", Enabled: true, Access: AccessPolicy{AllowedDomains: []string{"example.com"}, BlockedDomains: []string{"tracker.test"}, BlockedPorts: []int{25}}},
		{Name: "pc", Enabled: true},
	}, Nodes: []Node{
		{Name: "Relay A", Device: "phone", AuthUser: "alice-phone", UUID: "11111111-1111-4111-8111-111111111111"},
		{Name: "Relay A", Device: "pc", AuthUser: "alice-pc", UUID: "22222222-2222-4222-8222-222222222222"},
	}}}}
	raw, _ := json.Marshal(accessRestrictionRules(s, time.Now()))
	text := string(raw)
	if !strings.Contains(text, "alice-phone") || !strings.Contains(text, "example.com") || !strings.Contains(text, "tracker.test") || !strings.Contains(text, "25") || strings.Contains(text, "alice-pc") {
		t.Fatalf("device access policy leaked or was incomplete: %s", text)
	}
}

func TestConnectionLimitCanDisableOnlyOffendingDevice(t *testing.T) {
	now := time.Now()
	s := &State{
		ActiveConnections: map[string]ActiveConnection{
			"1": {User: "alice", Device: "phone", LastSeen: now.Format(time.RFC3339)},
			"2": {User: "alice", Device: "phone", LastSeen: now.Format(time.RFC3339)},
		},
		Users: []User{{Name: "alice", Enabled: true, Devices: []Device{
			{Name: "phone", Enabled: true, Access: AccessPolicy{MaxConnections: 1, ConnectionAction: "disable-device"}},
			{Name: "pc", Enabled: true},
		}}},
	}
	stateChanged, configChanged := evaluateConnectionPolicies(s, now)
	if !stateChanged || !configChanged || s.Users[0].Devices[0].Enabled || !s.Users[0].Devices[1].Enabled || !s.Users[0].Enabled {
		t.Fatalf("device connection action had wrong scope: %#v", s.Users[0])
	}
	if len(s.Alerts) != 1 || s.Alerts[0].Kind != "connection_limit" {
		t.Fatalf("connection alert missing: %#v", s.Alerts)
	}
	if changed, _ := evaluateConnectionPolicies(s, now.Add(time.Minute)); changed || len(s.Alerts) != 1 {
		t.Fatal("connection limit alert ignored cooldown")
	}
}

func TestConnectionLimitIgnoresStaleEntries(t *testing.T) {
	now := time.Now()
	s := &State{
		ActiveConnections: map[string]ActiveConnection{
			"old-1": {User: "alice", LastSeen: now.Add(-activeConnectionTTL - time.Second).Format(time.RFC3339)},
			"old-2": {User: "alice", LastSeen: now.Add(-activeConnectionTTL - time.Second).Format(time.RFC3339)},
		},
		Users: []User{{Name: "alice", Enabled: true, Access: AccessPolicy{MaxConnections: 1, ConnectionAction: "disable-user"}}},
	}
	stateChanged, configChanged := evaluateConnectionPolicies(s, now)
	if stateChanged || configChanged || !s.Users[0].Enabled || len(s.Alerts) != 0 {
		t.Fatalf("stale entries triggered enforcement: %#v", s)
	}
}

func TestAccessPolicyParsersNormalizeInputs(t *testing.T) {
	domains := parseDomainList(" *.Example.COM,example.com,.Other.test ")
	if strings.Join(domains, ",") != "example.com,other.test" {
		t.Fatalf("domains = %#v", domains)
	}
	ports, err := parsePortList("443, 25,443")
	if err != nil || len(ports) != 2 || ports[0] != 25 || ports[1] != 443 {
		t.Fatalf("ports=%#v err=%v", ports, err)
	}
	if _, err := parsePortList("70000"); err == nil {
		t.Fatal("invalid port accepted")
	}
}
