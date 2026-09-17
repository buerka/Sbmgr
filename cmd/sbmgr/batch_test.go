package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApplyBatchUserSettingsChangesEverySelectedUserAndPreservesOtherFields(t *testing.T) {
	s := batchTestState()
	s.Users[0].Access.BlockedDomains = []string{"keep.example"}
	s.Users[1].ExtraQuotaBytes = 3 << 30
	quota := int64(20 << 30)
	expires := "2026-12-31"
	billingEnabled := true
	billingDay := 8
	throttleEnabled := true
	tier1Usage, tier1Speed := 50.0, 50.0
	tier2Usage, tier2Speed := 80.0, 20.0
	op := batchOperation{Kind: batchUserSettings, Users: []string{"bob", "alice"}, User: batchUserSettingsPatch{
		QuotaBytes: &quota, Expires: &expires, BillingEnabled: &billingEnabled, BillingDay: &billingDay,
		ThrottleEnabled: &throttleEnabled, Tier1Usage: &tier1Usage, Tier1Speed: &tier1Speed, Tier2Usage: &tier2Usage, Tier2Speed: &tier2Speed,
	}}

	updated, result, err := applyBatchOperation(s, op, time.Date(2026, 8, 4, 10, 0, 0, 0, applicationLocation()))
	if err != nil {
		t.Fatal(err)
	}
	if result.Users != 2 {
		t.Fatalf("changed users = %d, want 2", result.Users)
	}
	for _, name := range []string{"alice", "bob"} {
		u := findUser(updated, name)
		if u.QuotaBytes != quota || u.Expires != expires || !u.Billing.Enabled || u.Billing.CycleDay != billingDay || !u.Throttle.Enabled {
			t.Fatalf("user %s did not receive shared settings: %#v", name, *u)
		}
	}
	if got := findUser(updated, "bob").ExtraQuotaBytes; got != 3<<30 {
		t.Fatalf("unspecified extra quota changed: %d", got)
	}
	if got := findUser(updated, "alice").Access.BlockedDomains; len(got) != 1 || got[0] != "keep.example" {
		t.Fatalf("unrelated access policy changed: %#v", got)
	}
}

func TestApplyBatchNodeRatesTargetsNamedLineAcrossUsers(t *testing.T) {
	s := batchTestState()
	upload, download := 100.0, 300.0
	op := batchOperation{Kind: batchNodeRates, Users: []string{"alice", "bob"}, Node: batchNodeRatesPatch{NodeName: "Node A", UploadMbps: &upload, DownloadMbps: &download}}
	updated, result, err := applyBatchOperation(s, op, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Users != 2 || result.Nodes != 2 {
		t.Fatalf("unexpected result: %#v", result)
	}
	for _, name := range []string{"alice", "bob"} {
		u := findUser(updated, name)
		nodeA, err := findUserNode(u, defaultDeviceName, "Node A")
		if err != nil {
			t.Fatal(err)
		}
		via, err := findUserNode(u, defaultDeviceName, "Via")
		if err != nil {
			t.Fatal(err)
		}
		if nodeA.UploadMbps != upload || nodeA.DownloadMbps != download || !validRateMark(nodeA.RateMark) {
			t.Fatalf("Node A rate not updated for %s: %#v", name, *nodeA)
		}
		if via.UploadMbps != 25 || via.DownloadMbps != 25 {
			t.Fatalf("non-target Via rate changed for %s: %#v", name, *via)
		}
	}
}

func TestBatchNodeRateMigratesLegacyFallbackWithoutChangingOtherLines(t *testing.T) {
	s := batchTestState()
	s.Users = s.Users[:1]
	u := &s.Users[0]
	u.UploadMbps, u.DownloadMbps, u.RateMark = 10, 20, rateMarkPrefix+99
	for index := range u.Nodes {
		u.Nodes[index].UploadMbps, u.Nodes[index].DownloadMbps, u.Nodes[index].RateMark = 0, 0, 0
	}
	unlimited := 0.0
	op := batchOperation{Kind: batchNodeRates, Users: []string{"alice"}, Node: batchNodeRatesPatch{NodeName: "Node A", UploadMbps: &unlimited, DownloadMbps: &unlimited}}
	updated, _, err := applyBatchOperation(s, op, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	u = findUser(updated, "alice")
	nodeA, _ := findUserNode(u, defaultDeviceName, "Node A")
	via, _ := findUserNode(u, defaultDeviceName, "Via")
	if u.UploadMbps != 0 || u.DownloadMbps != 0 || nodeA.UploadMbps != 0 || nodeA.DownloadMbps != 0 {
		t.Fatalf("selected legacy line was not made unlimited: user=%#v node=%#v", *u, *nodeA)
	}
	if via.UploadMbps != 10 || via.DownloadMbps != 20 || !validRateMark(via.RateMark) {
		t.Fatalf("untouched legacy line lost its effective rate: %#v", *via)
	}
}

func TestApplyBatchPoliciesSupportBurstIPAndAccess(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		op    batchOperation
		check func(*testing.T, *State)
	}{
		{
			name: "burst",
			op: func() batchOperation {
				enabled, window, limit, block := true, 30, int64(2<<30), 45
				action, softUp, softDown := burstActionSoft, 16.0, 2.0
				return batchOperation{Kind: batchBurstPolicy, Users: []string{"alice", "bob"}, Burst: batchBurstPolicyPatch{Enabled: &enabled, Action: &action, WindowMinutes: &window, LimitBytes: &limit, BlockMinutes: &block, SoftUploadKbps: &softUp, SoftDownloadKbps: &softDown}}
			}(),
			check: func(t *testing.T, s *State) {
				for _, u := range s.Users {
					if !u.Burst.Enabled || u.Burst.Action != burstActionSoft || u.Burst.WindowMinutes != 30 || u.Burst.LimitBytes != 2<<30 || u.Burst.BlockMinutes != 45 || u.Burst.SoftUploadKbps != 16 || u.Burst.SoftDownloadKbps != 2 {
						t.Fatalf("burst policy not applied: %#v", u.Burst)
					}
				}
			},
		},
		{
			name: "dynamic IP keeps each user's bound address",
			op: func() batchOperation {
				enabled, binding := true, "dynamic"
				return batchOperation{Kind: batchIPPolicy, Users: []string{"alice", "bob"}, IP: batchIPPolicyPatch{Enabled: &enabled, Binding: &binding}}
			}(),
			check: func(t *testing.T, s *State) {
				for index, want := range []string{"203.0.113.10", "203.0.113.11"} {
					u := s.Users[index]
					if !u.IPPolicy.Enabled || u.IPPolicy.Binding != "dynamic" || u.IPPolicy.MaxIPs != 1 || len(u.IPPolicy.BoundIPs) != 1 || u.IPPolicy.BoundIPs[0] != want {
						t.Fatalf("unexpected IP policy for %s: %#v", u.Name, u.IPPolicy)
					}
				}
			},
		},
		{
			name: "access",
			op: func() batchOperation {
				domains := []string{"tracker.example"}
				ports := []int{25, 445}
				connections, action := 12, "disable-user"
				return batchOperation{Kind: batchAccessPolicy, Users: []string{"alice", "bob"}, Access: batchAccessPolicyPatch{BlockedDomains: &domains, BlockedPorts: &ports, MaxConnections: &connections, ConnectionAction: &action}}
			}(),
			check: func(t *testing.T, s *State) {
				for _, u := range s.Users {
					if u.Access.MaxConnections != 12 || u.Access.ConnectionAction != "disable-user" || len(u.Access.BlockedDomains) != 1 || len(u.Access.BlockedPorts) != 2 {
						t.Fatalf("access policy not applied: %#v", u.Access)
					}
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			updated, result, err := applyBatchOperation(batchTestState(), test.op, now)
			if err != nil {
				t.Fatal(err)
			}
			if result.Users != 2 {
				t.Fatalf("changed users = %d, want 2", result.Users)
			}
			test.check(t, updated)
		})
	}
}

func TestBatchOperationIsAtomicWhenOneUserFails(t *testing.T) {
	s := batchTestState()
	findUser(s, "bob").Nodes = findUser(s, "bob").Nodes[1:]
	before, _ := json.Marshal(s)
	upload := 100.0
	op := batchOperation{Kind: batchNodeRates, Users: []string{"alice", "bob"}, Node: batchNodeRatesPatch{NodeName: "Node A", UploadMbps: &upload}}
	updated, _, err := applyBatchOperation(s, op, time.Now())
	if err == nil || !strings.Contains(err.Error(), "批量修改已取消") {
		t.Fatalf("expected atomic cancellation, got state=%#v err=%v", updated, err)
	}
	after, _ := json.Marshal(s)
	if !bytes.Equal(before, after) {
		t.Fatal("source state changed after a rejected batch operation")
	}
}

func TestBatchCommandPersistsOnceAndWritesAudit(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	s := batchTestState()
	if err := saveState(statePath, s); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	a := &app{statePath: statePath, out: &output, err: &output}
	quota := int64(40 << 30)
	op := batchOperation{Kind: batchUserSettings, Users: []string{"alice", "bob"}, User: batchUserSettingsPatch{QuotaBytes: &quota}}
	if err := a.batchUsers(op); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if findUser(loaded, "alice").QuotaBytes != quota || findUser(loaded, "bob").QuotaBytes != quota {
		t.Fatal("batch command did not persist both users")
	}
	auditData, err := os.ReadFile(auditPath(statePath))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(auditData, []byte(`"action":"user.batch"`)) {
		t.Fatalf("batch audit record missing: %s", auditData)
	}
	if !strings.Contains(output.String(), "已批量更新 2 个用户") {
		t.Fatalf("unexpected output: %q", output.String())
	}
}

func batchTestState() *State {
	users := []User{}
	ips := []string{"203.0.113.10", "203.0.113.11"}
	for index, name := range []string{"alice", "bob"} {
		users = append(users, User{
			Name: name, Enabled: true, QuotaBytes: 10 << 30,
			IPPolicy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "auto", MaxIPs: 1, BoundIPs: []string{ips[index]}},
			Devices:  []Device{{Name: defaultDeviceName, Enabled: true, SubscriptionToken: strings.Repeat(string(rune('a'+index)), 32), CreatedAt: "2026-08-01T00:00:00Z"}},
			Nodes: []Node{
				{Name: "Node A", Device: defaultDeviceName, AuthUser: name + "-node-a", UUID: name + "-node-a-uuid"},
				{Name: "Via", Device: defaultDeviceName, AuthUser: name + "-via", UUID: name + "-via-uuid", UploadMbps: 25, DownloadMbps: 25},
			},
		})
	}
	return &State{Version: stateVersion, ConfigPath: filepath.Join(os.TempDir(), "sbmgr-batch-sing-box.json"), Service: "sing-box", Client: ClientSettings{Server: "example.com", Port: 443}, Users: users}
}
