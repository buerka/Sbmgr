package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestDetailViewportKeepsSelectedNodeAndDeleteVisible(t *testing.T) {
	nodes := []Node{
		{Name: "Node A", UUID: "11111111-1111-4111-8111-111111111111", AuthUser: "alice-node-a"},
		{Name: "Relay A", UUID: "22222222-2222-4222-8222-222222222222", AuthUser: "alice-relay-a"},
		{Name: "Relay B", UUID: "33333333-3333-4333-8333-333333333333", AuthUser: "alice-relay-b"},
	}
	m := tuiModel{
		state:    &State{Users: []User{{Name: "alice", Enabled: true, Nodes: nodes}}},
		width:    100,
		height:   24,
		mode:     tuiDetail,
		selected: "alice",
		status:   "就绪",
	}
	for range 2 {
		model, _ := m.updateDetail(tea.KeyPressMsg(tea.Key{Text: "j", Code: 'j'}))
		m = model.(tuiModel)
	}
	if m.nodeCursor != 2 {
		t.Fatalf("two down commands selected node %d instead of 2", m.nodeCursor)
	}
	detail := m.render()
	if height := strings.Count(detail, "\n") + 1; height > m.height {
		t.Fatalf("detail exceeded terminal height: got %d, want <= %d\n%s", height, m.height, detail)
	}
	for _, want := range []string{"节点 3/3", "3.", "Relay B", "R 重置本月流量", "D 删除用户"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail viewport does not contain %q:\n%s", want, detail)
		}
	}
}

func TestDetailMonthlyTrafficResetShortcutIsVisibleAndConfirmed(t *testing.T) {
	m := tuiModel{
		state:    &State{Users: []User{{Name: "alice", Enabled: true}}},
		width:    100,
		height:   24,
		mode:     tuiDetail,
		selected: "alice",
	}
	if rendered := m.render(); !strings.Contains(rendered, "R 重置本月流量") {
		t.Fatalf("monthly traffic reset shortcut is not visible:\n%s", rendered)
	}
	model, _ := m.updateDetail(tea.KeyPressMsg(tea.Key{Text: "R", Code: 'R'}))
	updated := model.(tuiModel)
	if updated.mode != tuiConfirmMode || updated.confirm.action != confirmResetTraffic || updated.confirm.user != "alice" {
		t.Fatalf("monthly traffic reset shortcut did not open the expected confirmation: %#v", updated.confirm)
	}
	for _, want := range []string{"本月流量", "配额", "账期日", "立即应用"} {
		if !strings.Contains(updated.confirm.prompt, want) {
			t.Fatalf("reset confirmation does not explain %q: %q", want, updated.confirm.prompt)
		}
	}
}

func TestUserNftCounterBaselinesPreventPreResetTrafficFromReturning(t *testing.T) {
	s := &State{Counters: map[string]int64{"unrelated": 99}}
	u := &User{Name: "alice", Nodes: []Node{
		{Name: "Node A", Device: "phone", RateMark: 0x53420001},
		{Name: "Relay A", Device: "phone", RateMark: 0x53420002},
	}}
	counters := map[string]int64{
		"sbmgr:53420001 upload":                               100,
		"sbmgr:53420001 download":                             200,
		"sbmgr:53420002 upload":                               300,
		deviceNodeLabel("bob", "phone", "Node A") + " upload": 400,
	}
	applyUserNftCounterBaselines(s, u, counters)
	want := map[string]int64{
		"nft:53420001:upload": 100, "nft:53420001:download": 200,
		"nft:53420002:upload": 300, "unrelated": 99,
	}
	for key, value := range want {
		if s.Counters[key] != value {
			t.Fatalf("counter baseline %q = %d, want %d; all=%#v", key, s.Counters[key], value, s.Counters)
		}
	}
	if _, exists := s.Counters["nft:53420002:download"]; exists {
		t.Fatalf("missing live counter unexpectedly created a baseline: %#v", s.Counters)
	}
}

func TestUserListSupportsDirectDelete(t *testing.T) {
	m := tuiModel{state: &State{Users: []User{{Name: "alice", Enabled: true}}}, width: 100, height: 24}
	model, _ := m.updateList(tea.KeyPressMsg(tea.Key{Text: "D", Code: 'D'}))
	updated := model.(tuiModel)
	if updated.mode != tuiConfirmMode || updated.confirm.action != confirmDelete || updated.confirm.user != "alice" {
		t.Fatalf("list delete did not open the expected confirmation: %#v", updated.confirm)
	}
}

func TestRemoveNodeWithNoNodesDoesNotPanic(t *testing.T) {
	m := tuiModel{state: &State{Users: []User{{Name: "alice", Enabled: true}}}, mode: tuiDetail, selected: "alice"}
	model, _ := m.updateDetail(tea.KeyPressMsg(tea.Key{Text: "d", Code: 'd'}))
	updated := model.(tuiModel)
	if !updated.statusError || !strings.Contains(updated.status, "没有可移除") {
		t.Fatalf("empty node removal did not return a useful error: %q", updated.status)
	}
}
