package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestTUIStateRefreshPreservesNavigationState(t *testing.T) {
	oldState := &State{Users: []User{{Name: "alice", Nodes: []Node{{Name: "Node A"}, {Name: "Relay A"}}}}}
	newState := &State{Users: []User{{Name: "alice", Upload: 123, Nodes: []Node{{Name: "Node A"}, {Name: "Relay A"}}}}, IPApplyPending: true}
	m := tuiModel{
		state: oldState, mode: tuiDetail, selected: "alice", cursor: 4, nodeCursor: 1,
		detailOffset: 7, filter: "ali", checkedUsers: map[string]bool{"alice": true},
	}

	model, cmd := m.Update(tuiStateRefreshMsg{state: newState})
	if cmd != nil {
		t.Fatal("completed state refresh scheduled an unexpected command")
	}
	updated := model.(tuiModel)
	if updated.state != newState || updated.state.Users[0].Upload != 123 || !updated.pendingApply {
		t.Fatalf("fresh state was not installed: %#v", updated.state)
	}
	if updated.mode != tuiDetail || updated.selected != "alice" || updated.nodeCursor != 1 || updated.detailOffset != 7 || updated.filter != "ali" || !updated.checkedUsers["alice"] {
		t.Fatalf("refresh changed navigation state: %#v", updated)
	}
}

func TestTUIStateRefreshDoesNotReplaceFormConfirmOrBusySnapshot(t *testing.T) {
	oldState := &State{Users: []User{{Name: "old"}}}
	newState := &State{Users: []User{{Name: "new"}}}
	for _, tc := range []struct {
		name string
		mode tuiMode
		busy bool
	}{
		{name: "form", mode: tuiFormMode},
		{name: "confirm", mode: tuiConfirmMode},
		{name: "busy", mode: tuiDetail, busy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tuiModel{state: oldState, mode: tc.mode, busy: tc.busy}
			model, _ := m.Update(tuiStateRefreshMsg{state: newState})
			if got := model.(tuiModel).state; got != oldState {
				t.Fatalf("protected snapshot was replaced: %#v", got)
			}
		})
	}
}

func TestLoadTUIStateCommandReadsLatestSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	want := &State{Version: stateVersion, Users: []User{{Name: "alice", Upload: 99}}}
	if err := saveState(path, want); err != nil {
		t.Fatal(err)
	}
	msg := loadTUIState(path, 7, 11)().(tuiStateRefreshMsg)
	if msg.err != nil || msg.state == nil || len(msg.state.Users) != 1 || msg.state.Users[0].Upload != 99 {
		t.Fatalf("unexpected refresh result: %#v", msg)
	}
	if msg.generation != 7 || msg.sequence != 11 {
		t.Fatalf("refresh stamp = generation %d sequence %d", msg.generation, msg.sequence)
	}
}

func TestStaleTUIRefreshCannotOverwriteNewerActionResult(t *testing.T) {
	before := &State{Users: []User{{Name: "alice", Upload: 1}}}
	actionState := &State{Users: []User{{Name: "alice", Upload: 3}}}
	staleRead := &State{Users: []User{{Name: "alice", Upload: 2}}}
	m := tuiModel{state: before, mode: tuiList, busy: true, checkedUsers: map[string]bool{}}

	model, _ := m.Update(tuiActionMsg{state: actionState, output: "操作完成"})
	m = model.(tuiModel)
	if m.stateGeneration != 1 || m.state != actionState {
		t.Fatalf("action result did not advance generation: %#v", m)
	}
	model, _ = m.Update(tuiStateRefreshMsg{state: staleRead, generation: 0, sequence: 1})
	m = model.(tuiModel)
	if m.state != actionState || m.state.Users[0].Upload != 3 || m.status != "操作完成" {
		t.Fatalf("stale read overwrote action result: %#v", m)
	}
}

func TestTUIRefreshResponsesCannotApplyOutOfSequence(t *testing.T) {
	oldState := &State{Users: []User{{Name: "alice", Upload: 1}}}
	newState := &State{Users: []User{{Name: "alice", Upload: 2}}}
	m := tuiModel{state: oldState, mode: tuiList, stateGeneration: 4, checkedUsers: map[string]bool{}}

	model, _ := m.Update(tuiStateRefreshMsg{state: newState, generation: 4, sequence: 9})
	m = model.(tuiModel)
	if m.state != newState || m.appliedRefreshSeq != 9 {
		t.Fatalf("newest refresh was not applied: %#v", m)
	}
	model, _ = m.Update(tuiStateRefreshMsg{state: oldState, generation: 4, sequence: 8})
	m = model.(tuiModel)
	if m.state != newState || m.state.Users[0].Upload != 2 || m.appliedRefreshSeq != 9 {
		t.Fatalf("out-of-order refresh replaced newer state: %#v", m)
	}
}

func TestUserDetailShowsCompactOperationalAudit(t *testing.T) {
	now := time.Now()
	state := detailAuditTestState(now)
	m := tuiModel{
		a: &app{out: io.Discard, err: io.Discard}, state: state,
		width: 120, height: 36, mode: tuiDetail, selected: "alice", detailOffset: 0,
		pendingApply: true, checkedUsers: map[string]bool{},
	}
	rendered := m.renderDetail()
	for _, want := range []string{
		"运行审计", "约每 3 秒只读刷新", "实际 ↑", "双向合计", "活跃连接 1",
		"未读告警 1", "最后告警", "配置 待应用", "有效 IP", "IP 叠加", "取更严",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("detail audit does not contain %q:\n%s", want, rendered)
		}
	}
}

func TestUserDetailAuditStartsVisibleAndFooterStaysFixedAt64x18(t *testing.T) {
	m := tuiModel{
		a: &app{out: io.Discard, err: io.Discard}, state: detailAuditTestState(time.Now()),
		width: 64, height: 18, mode: tuiDetail, selected: "alice", detailOffset: 0,
		checkedUsers: map[string]bool{},
	}
	rendered := m.renderDetail()
	lines := strings.Split(rendered, "\n")
	if len(lines) > m.height {
		t.Fatalf("64x18 detail height = %d, want <= %d:\n%s", len(lines), m.height, rendered)
	}
	for index, line := range lines {
		if width := lipgloss.Width(line); width > m.width {
			t.Fatalf("detail line %d width = %d, want <= %d: %q", index+1, width, m.width, line)
		}
	}
	for _, want := range []string{"运行审计", "实际 ↑", "PgUp/PgDn 审计/节点", "esc 返回"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("64x18 detail does not contain %q:\n%s", want, rendered)
		}
	}

	model, _ := m.updateDetail(formSpecialKey(tea.KeyPgDown))
	updated := model.(tuiModel)
	if updated.detailOffset <= 0 {
		t.Fatalf("PgDown did not advance detail viewport: %d", updated.detailOffset)
	}
}

func detailAuditTestState(now time.Time) *State {
	return &State{
		IPApplyPending: true,
		ActiveConnections: map[string]ActiveConnection{
			"one": {ID: "one", User: "alice", Device: "phone", Node: "Node A", SourceIP: "203.0.113.10", LastSeen: now.Format(time.RFC3339)},
		},
		Alerts: []Alert{{At: now.Format(time.RFC3339), User: "alice", Kind: "ip_violation", Message: "新 IP 等待换绑", Acknowledged: false}},
		Users: []User{{
			Name: "alice", Enabled: true, Upload: 2 << 30, Download: 3 << 30, QuotaBytes: 20 << 30,
			CurrentUploadMbps: 1.25, CurrentDownloadMbps: 2.5,
			IPPolicy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "dynamic", MaxIPs: 1, HandoverSeconds: 60, BoundIPs: []string{"203.0.113.10"}},
			Devices:  []Device{{Name: "phone", Enabled: true, IPPolicy: IPPolicy{Enabled: true, Mode: "monitor", Binding: "dynamic", MaxIPs: 1, HandoverSeconds: 60}}},
			Nodes:    []Node{{Name: "Node A", Device: "phone", AuthUser: "alice:node-a", UUID: "11111111-1111-4111-8111-111111111111"}},
		}},
	}
}
