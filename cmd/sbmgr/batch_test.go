package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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
	op := batchOperation{Kind: batchNodeRates, Users: []string{"alice", "bob"}, Node: batchNodeRatesPatch{NodeName: "LAX", UploadMbps: &upload, DownloadMbps: &download}}
	updated, result, err := applyBatchOperation(s, op, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Users != 2 || result.Nodes != 2 {
		t.Fatalf("unexpected result: %#v", result)
	}
	for _, name := range []string{"alice", "bob"} {
		u := findUser(updated, name)
		lax, err := findUserNode(u, defaultDeviceName, "LAX")
		if err != nil {
			t.Fatal(err)
		}
		via, err := findUserNode(u, defaultDeviceName, "Via")
		if err != nil {
			t.Fatal(err)
		}
		if lax.UploadMbps != upload || lax.DownloadMbps != download || !validRateMark(lax.RateMark) {
			t.Fatalf("LAX rate not updated for %s: %#v", name, *lax)
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
	op := batchOperation{Kind: batchNodeRates, Users: []string{"alice"}, Node: batchNodeRatesPatch{NodeName: "LAX", UploadMbps: &unlimited, DownloadMbps: &unlimited}}
	updated, _, err := applyBatchOperation(s, op, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	u = findUser(updated, "alice")
	lax, _ := findUserNode(u, defaultDeviceName, "LAX")
	via, _ := findUserNode(u, defaultDeviceName, "Via")
	if u.UploadMbps != 0 || u.DownloadMbps != 0 || lax.UploadMbps != 0 || lax.DownloadMbps != 0 {
		t.Fatalf("selected legacy line was not made unlimited: user=%#v node=%#v", *u, *lax)
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
	op := batchOperation{Kind: batchNodeRates, Users: []string{"alice", "bob"}, Node: batchNodeRatesPatch{NodeName: "LAX", UploadMbps: &upload}}
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

func TestTUIUserSelectionAndBatchConfirmation(t *testing.T) {
	m := tuiModel{state: batchTestState(), width: 110, height: 24, checkedUsers: map[string]bool{}, status: "就绪"}
	model, _ := m.updateList(tea.KeyPressMsg(tea.Key{Text: " ", Code: tea.KeySpace}))
	m = model.(tuiModel)
	model, _ = m.updateList(tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))
	m = model.(tuiModel)
	model, _ = m.updateList(tea.KeyPressMsg(tea.Key{Text: " ", Code: tea.KeySpace}))
	m = model.(tuiModel)
	if got := m.batchSelectedNames(); len(got) != 2 || got[0] != "alice" || got[1] != "bob" {
		t.Fatalf("unexpected selected users: %#v", got)
	}
	if rendered := m.renderList(); !strings.Contains(rendered, "[✓]") || !strings.Contains(rendered, "已选") {
		t.Fatalf("selection not visible in list:\n%s", rendered)
	}
	model, _ = m.updateList(tea.KeyPressMsg(tea.Key{Text: "B", Code: 'B'}))
	m = model.(tuiModel)
	if m.mode != tuiBatch {
		t.Fatalf("B did not open batch management: %v", m.mode)
	}
	model, _ = m.updateBatch(tea.KeyPressMsg(tea.Key{Text: "e", Code: 'e'}))
	m = model.(tuiModel)
	if m.mode != tuiFormMode || m.form.kind != formBatchUser || len(m.form.users) != 2 {
		t.Fatalf("batch user form not opened: %#v", m.form)
	}
	m.form.fields[1].value = "20G"
	model, _ = m.submitForm()
	m = model.(tuiModel)
	if m.mode != tuiConfirmMode || m.confirm.action != confirmBatch || m.confirm.batch == nil {
		t.Fatalf("batch form did not open confirmation: %#v", m.confirm)
	}
	if !strings.Contains(m.confirm.prompt, "任一用户校验失败会整批取消") || !strings.Contains(m.confirm.prompt, "alice、bob") {
		t.Fatalf("confirmation lacks atomic scope: %q", m.confirm.prompt)
	}
}

func TestBatchBurstBareThresholdUsesGiB(t *testing.T) {
	m := tuiModel{state: batchTestState(), width: 100, height: 24, status: "就绪"}
	m.openBatchBurstForm([]string{"alice", "bob"})
	m.form.fields[0].value = "开启"
	m.form.fields[1].value = "软封禁（极低速）"
	m.form.fields[2].value = "30"
	m.form.fields[3].value = "2"
	m.form.fields[4].value = "20"
	m.form.fields[5].value = "16"
	m.form.fields[6].value = "2"
	model, _ := m.submitForm()
	m = model.(tuiModel)
	if m.mode != tuiConfirmMode || m.confirm.batch == nil {
		t.Fatalf("batch burst form did not reach confirmation: %#v", m.confirm)
	}
	if got := m.confirm.batch.Burst.LimitBytes; got == nil || *got != 2<<30 {
		t.Fatalf("bare threshold parsed as %v, want %d", got, int64(2<<30))
	}
	if !strings.Contains(m.confirm.prompt, "阈值=2.0GiB") {
		t.Fatalf("confirmation did not expose normalized threshold: %q", m.confirm.prompt)
	}
}

func TestUnblockShortcutsRequireAnActiveBlock(t *testing.T) {
	m := tuiModel{state: batchTestState(), width: 100, height: 24, checkedUsers: map[string]bool{}, status: "就绪"}
	model, _ := m.updateList(tea.KeyPressMsg(tea.Key{Text: "U", Code: 'U'}))
	m = model.(tuiModel)
	if m.mode != tuiList || m.status != "当前没有临时封禁用户" {
		t.Fatalf("empty unblock shortcut state = mode %v status %q", m.mode, m.status)
	}
	m.state.Users[0].BlockedUntil = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
	model, _ = m.updateList(tea.KeyPressMsg(tea.Key{Text: "U", Code: 'U'}))
	m = model.(tuiModel)
	if m.mode != tuiConfirmMode || m.confirm.action != confirmUnblock || m.confirm.user != "" || !strings.Contains(m.confirm.prompt, "alice") {
		t.Fatalf("global unblock confirmation missing: %#v", m.confirm)
	}
	m.mode, m.selected = tuiDetail, "alice"
	model, _ = m.updateDetail(tea.KeyPressMsg(tea.Key{Text: "U", Code: 'U'}))
	m = model.(tuiModel)
	if m.mode != tuiConfirmMode || m.confirm.action != confirmUnblock || m.confirm.user != "alice" {
		t.Fatalf("user unblock confirmation missing: %#v", m.confirm)
	}
}

func TestLongBatchFormKeepsActiveFieldVisible(t *testing.T) {
	m := tuiModel{state: batchTestState(), width: 90, height: 18, checkedUsers: map[string]bool{"alice": true, "bob": true}, status: "就绪"}
	m.openBatchUserForm([]string{"alice", "bob"})
	m.form.active = len(m.form.fields) - 1
	rendered := m.renderForm()
	if height := strings.Count(rendered, "\n") + 1; height > m.height {
		t.Fatalf("batch form exceeded terminal height: got %d, want <= %d\n%s", height, m.height, rendered)
	}
	if !strings.Contains(rendered, "第二档速度 %") || !strings.Contains(rendered, "字段 12/12") {
		t.Fatalf("active field is not visible:\n%s", rendered)
	}
}

func TestUserListWithBatchControlsFitsMinimumTerminal(t *testing.T) {
	users := make([]User, 20)
	for index := range users {
		users[index] = User{Name: "user-" + string(rune('a'+index)), Enabled: true}
	}
	m := tuiModel{state: &State{Users: users}, width: 64, height: 18, checkedUsers: map[string]bool{}, status: "就绪"}
	rendered := m.renderList()
	if height := strings.Count(rendered, "\n") + 1; height > m.height {
		t.Fatalf("user list exceeded terminal height: got %d, want <= %d\n%s", height, m.height, rendered)
	}
	for _, want := range []string{"space 勾选", "B 批量管理", "1–5 / 20"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("user list missing %q:\n%s", want, rendered)
		}
	}
	for _, u := range users {
		m.checkedUsers[strings.ToLower(u.Name)] = true
	}
	m.mode = tuiBatch
	batchRendered := m.renderBatch()
	if height := strings.Count(batchRendered, "\n") + 1; height > m.height {
		t.Fatalf("batch menu exceeded terminal height: got %d, want <= %d\n%s", height, m.height, batchRendered)
	}
	if !strings.Contains(batchRendered, "已选择 20 个用户") || !strings.Contains(batchRendered, "…") {
		t.Fatalf("batch menu did not summarize a large selection:\n%s", batchRendered)
	}
}

func TestLongBatchConfirmationFitsAndScrolls(t *testing.T) {
	m := tuiModel{
		state: &State{}, width: 64, height: 18, mode: tuiConfirmMode, status: "请核对批量修改范围",
		confirm: tuiConfirm{action: confirmBatch, prompt: "用户：alice、bob、carol、dave\n内容：配额=20G；到期=2026-12-31；自动清零=开启；账期日=8；阶梯限速=开启；一档用量=50%；一档速度=50%；二档用量=80%；二档速度=20%\n影响：实际会改变 4 个用户\n保存后按 p 应用到 sing-box。"},
	}
	rendered := m.renderConfirm()
	if height := strings.Count(rendered, "\n") + 1; height > m.height {
		t.Fatalf("confirmation exceeded terminal height: got %d, want <= %d\n%s", height, m.height, rendered)
	}
	_, _, maxOffset := m.confirmPromptWindow()
	if maxOffset == 0 || !strings.Contains(rendered, "内容 1–") {
		t.Fatalf("long confirmation did not expose scrolling:\n%s", rendered)
	}
	for range maxOffset {
		model, _ := m.updateConfirm(tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))
		m = model.(tuiModel)
	}
	if m.confirmOffset != maxOffset {
		t.Fatalf("confirmation offset = %d, want %d", m.confirmOffset, maxOffset)
	}
	if rendered = m.renderConfirm(); !strings.Contains(rendered, "保存后按 p") {
		t.Fatalf("last confirmation lines not reachable:\n%s", rendered)
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
				{Name: "LAX", Device: defaultDeviceName, AuthUser: name + "-lax", UUID: name + "-lax-uuid"},
				{Name: "Via", Device: defaultDeviceName, AuthUser: name + "-via", UUID: name + "-via-uuid", UploadMbps: 25, DownloadMbps: 25},
			},
		})
	}
	return &State{Version: stateVersion, ConfigPath: filepath.Join(os.TempDir(), "sbmgr-batch-sing-box.json"), Service: "sing-box", Client: ClientSettings{Server: "example.com", Port: 443}, Users: users}
}
