package main

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func tuiRegressionKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code})
}

func TestMinimumTerminalLongFormFitsAndKeepsCursorVisible(t *testing.T) {
	fields := make([]tuiField, 8)
	for index := range fields {
		fields[index] = tuiField{
			label: fmt.Sprintf("长字段 %d", index+1),
			value: "https://example.com/" + strings.Repeat(fmt.Sprintf("路径%d/", index+1), 18),
		}
	}
	active := 4
	fields[active].cursor = len([]rune(fields[active].value)) / 2
	fields[active].cursorSet = true
	m := tuiModel{
		width:  64,
		height: 18,
		mode:   tuiFormMode,
		status: "就绪",
		form:   tuiForm{title: "编辑", active: active, fields: fields},
	}

	rendered := m.renderForm()
	lines := strings.Split(rendered, "\n")
	if len(lines) > m.height {
		t.Errorf("64x18 long form height = %d, want <= %d:\n%s", len(lines), m.height, rendered)
	}
	for index, line := range lines {
		if width := lipgloss.Width(line); width > m.width {
			t.Errorf("line %d width = %d, want <= %d: %q", index+1, width, m.width, line)
		}
	}
	if !strings.Contains(rendered, "‹") || !strings.Contains(rendered, "▌") || !strings.Contains(rendered, "›") {
		t.Errorf("long active field must show both scroll markers and cursor:\n%s", rendered)
	}
}

func TestFormPasteInsertsAtCursorAndFlattensNewlines(t *testing.T) {
	m := tuiModel{
		mode: tuiFormMode,
		form: tuiForm{fields: []tuiField{{
			label: "名称", value: "甲乙", cursor: 1, cursorSet: true,
		}}},
	}

	model, _ := m.Update(tea.PasteMsg{Content: "新\n值\r\nX"})
	updated := model.(tuiModel)
	field := updated.form.fields[0]
	if field.value != "甲新 值 X乙" {
		t.Fatalf("pasted value = %q, want %q", field.value, "甲新 值 X乙")
	}
	if field.cursor != 6 || !field.cursorSet {
		t.Fatalf("cursor after paste = %d (set=%v), want 6", field.cursor, field.cursorSet)
	}
}

func TestDangerousUserMenuConfirmationCancelReturnsToMenu(t *testing.T) {
	entries := userMenuEntries()
	deleteIndex := -1
	for index, entry := range entries {
		if entry.title == "删除用户" {
			deleteIndex = index
			break
		}
	}
	if deleteIndex < 0 {
		t.Fatal("user action menu has no delete entry")
	}
	m := tuiModel{
		state:      &State{Users: []User{{Name: "alice", Enabled: true}}},
		mode:       tuiUserMenu,
		selected:   "alice",
		menuCursor: deleteIndex,
	}

	model, cmd := m.updateUserMenu(tuiRegressionKey(tea.KeyEnter))
	if cmd != nil {
		t.Fatal("opening a confirmation unexpectedly returned a command")
	}
	confirming := model.(tuiModel)
	if confirming.mode != tuiConfirmMode || confirming.confirm.action != confirmDelete {
		t.Fatalf("delete menu did not open delete confirmation: mode=%v confirm=%#v", confirming.mode, confirming.confirm)
	}
	if confirming.confirm.returnMode != tuiUserMenu {
		t.Errorf("confirmation return mode = %v, want tuiUserMenu", confirming.confirm.returnMode)
	}

	model, cmd = confirming.updateConfirm(tuiRegressionKey(tea.KeyEscape))
	if cmd != nil {
		t.Fatal("canceling a confirmation unexpectedly returned a command")
	}
	cancelled := model.(tuiModel)
	if cancelled.mode != tuiUserMenu || cancelled.selected != "alice" || cancelled.menuCursor != deleteIndex {
		t.Fatalf("cancel returned to mode=%v selected=%q cursor=%d; want user menu/alice/%d", cancelled.mode, cancelled.selected, cancelled.menuCursor, deleteIndex)
	}
}

func TestDangerousConfirmationRequiresYBeforeStartingAction(t *testing.T) {
	m := tuiModel{
		state:    &State{Users: []User{{Name: "alice", Enabled: true}}},
		mode:     tuiDetail,
		selected: "alice",
	}
	m.openConfirm(tuiConfirm{action: confirmDelete, user: "alice", prompt: "删除用户 alice？"})

	model, cmd := m.updateConfirm(tuiRegressionKey(tea.KeyEnter))
	blocked := model.(tuiModel)
	if cmd != nil || blocked.mode != tuiConfirmMode || blocked.busy {
		t.Fatalf("Enter started dangerous action: cmd=%v mode=%v busy=%v", cmd != nil, blocked.mode, blocked.busy)
	}
	if !blocked.statusError || !strings.Contains(blocked.status, "按 y") {
		t.Fatalf("Enter did not explain explicit confirmation requirement: %q", blocked.status)
	}

	model, cmd = blocked.updateConfirm(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
	started := model.(tuiModel)
	if cmd == nil || !started.busy || started.mode != tuiList {
		t.Fatalf("y did not start dangerous action: cmd=%v mode=%v busy=%v", cmd != nil, started.mode, started.busy)
	}
}

func TestEndpointPageShowsRowsAndOpensSelectedEditor(t *testing.T) {
	_, state := writeOutboundEndpointFixture(t, outboundEndpointFixture)
	state.Client = ClientSettings{Server: "198.51.100.10", Port: 443}
	state.Users = []User{{Name: "alice", Nodes: []Node{{Outbound: "to-relay-a"}}}}
	m := tuiModel{state: state, width: 100, height: 24, mode: tuiHealth, healthCursor: 0}

	rendered := m.renderHealth()
	for _, want := range []string{
		"中转入口", "198.51.100.10:443", "出站与端点", "to-relay-a", "192.0.2.10:443",
		"↑↓ 选择", "enter 对象操作", "e 常用字段", "a 新增", "s 健康/通知",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("endpoint page missing %q:\n%s", want, rendered)
		}
	}

	model, cmd := m.updateHealth(tuiRegressionKey(tea.KeyEnter))
	if cmd != nil {
		t.Fatal("opening client endpoint form unexpectedly returned a command")
	}
	clientForm := model.(tuiModel)
	if clientForm.mode != tuiFormMode || clientForm.form.kind != formClientEndpoint {
		t.Fatalf("client row opened mode=%v form=%v, want client endpoint form", clientForm.mode, clientForm.form.kind)
	}
	if clientForm.form.fields[0].value != "198.51.100.10" || clientForm.form.fields[1].value != "443" {
		t.Fatalf("client endpoint form values = %#v", clientForm.form.fields)
	}

	documents, err := listManagedProxyDocumentsForTUI(state)
	if err != nil {
		t.Fatal(err)
	}
	indexFor := func(tag string) int {
		for index, document := range documents {
			if document.Tag == tag {
				return index + 1
			}
		}
		return -1
	}
	selectedOutbound := m
	selectedOutbound.healthCursor = indexFor("to-relay-a")
	model, cmd = selectedOutbound.updateHealth(tuiRegressionKey(tea.KeyEnter))
	if cmd != nil {
		t.Fatal("opening outbound action menu unexpectedly returned a command")
	}
	menu := model.(tuiModel)
	if menu.mode != tuiProxyMenu || menu.proxyTag != "to-relay-a" || menu.proxyKind != ManagedProxyOutbound {
		t.Fatalf("outbound row opened mode=%v kind=%v tag=%q", menu.mode, menu.proxyKind, menu.proxyTag)
	}
	model, cmd = selectedOutbound.updateHealth(tuiRegressionKey('e'))
	if cmd != nil {
		t.Fatal("opening common outbound form unexpectedly returned a command")
	}
	outboundForm := model.(tuiModel)
	if outboundForm.mode != tuiFormMode || outboundForm.form.kind != formOutboundEndpoint || outboundForm.form.endpointTag != "to-relay-a" {
		t.Fatalf("outbound row opened mode=%v form=%v tag=%q", outboundForm.mode, outboundForm.form.kind, outboundForm.form.endpointTag)
	}
	if len(outboundForm.form.fields) != 3 || outboundForm.form.fields[0].value != "192.0.2.10" || outboundForm.form.fields[1].value != "443" || outboundForm.form.fields[2].label != "新密码" || !outboundForm.form.fields[2].secret || outboundForm.form.fields[2].value != "" {
		t.Fatalf("outbound endpoint form values = %#v", outboundForm.form.fields)
	}

	relayB := m
	relayB.healthCursor = indexFor("to-relay-b")
	model, cmd = relayB.updateHealth(tuiRegressionKey('e'))
	if cmd != nil {
		t.Fatal("opening SOCKS endpoint form unexpectedly returned a command")
	}
	socksForm := model.(tuiModel)
	if len(socksForm.form.fields) != 4 || socksForm.form.fields[2].label != "用户名" || socksForm.form.fields[2].value != "unchanged" || socksForm.form.fields[3].label != "新密码" || !socksForm.form.fields[3].secret || socksForm.form.fields[3].value != "" {
		t.Fatalf("SOCKS editor fields = %#v", socksForm.form.fields)
	}
}

func TestSOCKSEndpointCredentialsStayMaskedThroughConfirmation(t *testing.T) {
	_, state := writeOutboundEndpointFixture(t, outboundEndpointFixture)
	endpoints, err := listOutboundEndpoints(state)
	if err != nil {
		t.Fatal(err)
	}
	var endpoint OutboundEndpointSummary
	for _, candidate := range endpoints {
		if candidate.Tag == "to-relay-b" {
			endpoint = candidate
			break
		}
	}
	if endpoint.Tag == "" {
		t.Fatal("SOCKS fixture endpoint missing")
	}
	m := tuiModel{state: state, width: 90, height: 24, mode: tuiHealth}
	if err := m.openOutboundEndpointForm(endpoint); err != nil {
		t.Fatal(err)
	}
	username := "new-account"
	password := " leading:@\\密🔑 trailing "
	m.form.fields[2].value = username
	m.form.fields[3].value = password
	m.form.fields[3].cursor = len([]rune(password))
	m.form.fields[3].cursorSet = true
	m.form.active = 3
	if rendered := m.renderForm(); strings.Contains(rendered, password) || strings.Contains(rendered, "leading:@") || strings.Contains(rendered, "🔑") {
		t.Fatalf("credential form leaked password:\n%s", rendered)
	}

	model, cmd := m.submitForm()
	if cmd != nil {
		t.Fatal("credential form bypassed confirmation")
	}
	confirmed := model.(tuiModel)
	if confirmed.mode != tuiConfirmMode || confirmed.confirm.action != confirmOutboundEndpoint {
		t.Fatalf("credential form opened mode=%v action=%v", confirmed.mode, confirmed.confirm.action)
	}
	credentials := confirmed.confirm.endpointCredentials
	if credentials.Username == nil || *credentials.Username != username || credentials.Password == nil || *credentials.Password != password {
		t.Fatalf("credential values were changed before apply: username=%v password=%v", credentials.Username, credentials.Password)
	}
	if strings.Contains(confirmed.confirm.prompt, username) || strings.Contains(confirmed.confirm.prompt, password) || strings.Contains(confirmed.renderConfirm(), password) || strings.Contains(confirmed.renderConfirm(), "leading:@") {
		t.Fatalf("confirmation leaked credentials:\n%s", confirmed.renderConfirm())
	}
	if !strings.Contains(confirmed.confirm.prompt, "用户名：更新") || !strings.Contains(confirmed.confirm.prompt, "密码：更新（不回显）") {
		t.Fatalf("confirmation omitted safe change summary: %q", confirmed.confirm.prompt)
	}
	canceledModel, _ := confirmed.updateConfirm(tuiRegressionKey('n'))
	canceled := canceledModel.(tuiModel)
	if canceled.mode != tuiFormMode || canceled.confirm.endpointCredentials.Password != nil || canceled.form.fields[3].value != password {
		t.Fatalf("cancel did not safely return to the editable form: mode=%v confirm=%#v", canceled.mode, canceled.confirm)
	}
	canceledModel, _ = canceled.updateForm(tuiRegressionKey(tea.KeyEscape))
	leftForm := canceledModel.(tuiModel)
	if leftForm.form.fields[3].value != "" {
		t.Fatal("leaving the form retained the password value")
	}
}

func TestWideEndpointRowsNeverWrapStatusToRightEdge(t *testing.T) {
	_, state := writeOutboundEndpointFixture(t, outboundEndpointFixture)
	state.Client = ClientSettings{Server: "198.51.100.10", Port: 443}
	state.OutboundHealth = map[string]OutboundHealth{
		"to-relay-a": {
			Tag: "to-relay-a", Target: "192.0.2.10:443", Healthy: true,
			LatencyMS: 8, CheckedAt: "2026-08-25T22:33:16+08:00",
		},
		"to-relay-b": {
			Tag: "to-relay-b", Target: "relay-b.example:8443", Healthy: false,
			Failures: 522, CheckedAt: "2026-08-25T22:33:16+08:00", Error: "connection refused",
		},
	}

	for _, width := range []int{64, 73, 74, 80, 81, 88, 89, 99, 100, 101, 120, 156, 200} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			m := tuiModel{state: state, width: width, height: 30, mode: tuiHealth, healthCursor: 2}
			rendered := m.renderHealth()
			var healthyRow string
			for index, line := range strings.Split(rendered, "\n") {
				if got := lipgloss.Width(line); got > width {
					t.Errorf("line %d width = %d, want <= %d: %q", index+1, got, width, line)
				}
				if strings.Contains(line, "to-relay-a") && strings.Contains(line, "192.0.2.10:443") {
					healthyRow = line
				}
			}
			if healthyRow == "" {
				t.Fatalf("healthy endpoint row missing at width %d:\n%s", width, rendered)
			}
			if width >= 100 && !strings.Contains(healthyRow, "正常") {
				t.Fatalf("healthy status was not kept on the endpoint row at width %d:\n%s", width, rendered)
			}
		})
	}
}
