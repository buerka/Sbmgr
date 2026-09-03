package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func managedProxyTUIFixture(t *testing.T) *State {
	t.Helper()
	directory := t.TempDir()
	base := filepath.Join(directory, "config.base.json")
	config := map[string]any{
		"outbounds": []any{
			map[string]any{"type": "direct", "tag": "direct"},
			map[string]any{"type": "socks", "tag": "to-socks", "server": "proxy.example", "server_port": 1080, "username": "account", "password": "do-not-render"},
		},
		"endpoints": []any{
			map[string]any{"type": "wireguard", "tag": "wg-exit", "address": []string{"10.0.0.2/32"}, "private_key": "endpoint-secret"},
		},
		"route": map[string]any{"final": "direct"},
	}
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	return &State{BaseConfig: base, Users: []User{{Name: "alice", Nodes: []Node{{Name: "proxy", Outbound: "to-socks"}}}}}
}

func assertTUIFits(t *testing.T, rendered string, width, height int) {
	t.Helper()
	lines := strings.Split(rendered, "\n")
	if len(lines) > height {
		t.Fatalf("render height = %d, want <= %d:\n%s", len(lines), height, rendered)
	}
	for index, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", index+1, got, width, line)
		}
	}
}

func TestManagedProxyMenuAndReviewFitMinimumTerminal(t *testing.T) {
	state := managedProxyTUIFixture(t)
	document, err := getEndpointDocument(state, "wg-exit")
	if err != nil {
		t.Fatal(err)
	}
	m := tuiModel{state: state, width: 64, height: 18, mode: tuiHealth}
	m.openManagedProxyMenu(document)

	rendered := m.renderProxyMenu()
	assertTUIFits(t, rendered, 64, 18)
	for _, want := range []string{"wg-exit", "端点仅管理底层网络接口", "常用字段", "完整 JSON", "从文件导入", "删除"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("proxy menu missing %q:\n%s", want, rendered)
		}
	}

	model, cmd := m.updateProxyMenu(tuiRegressionKey(tea.KeyEnter))
	if cmd != nil {
		t.Fatal("unsupported common editor started a command")
	}
	unsupported := model.(tuiModel)
	if unsupported.mode != tuiProxyMenu || !unsupported.statusError || !strings.Contains(unsupported.status, "完整 JSON") {
		t.Fatalf("endpoint common edit state = mode %v status %q", unsupported.mode, unsupported.status)
	}
	assertTUIFits(t, unsupported.renderProxyMenu(), 64, 18)

	m.mode = tuiProxyReview
	m.proxyOperation = "replace"
	m.proxyIdentity = managedProxyIdentity(document)
	m.proxyAddress = "-"
	rendered = m.renderProxyReview()
	assertTUIFits(t, rendered, 64, 18)
	if strings.Contains(rendered, "endpoint-secret") || strings.Contains(rendered, "private_key") {
		t.Fatalf("redacted review leaked endpoint JSON:\n%s", rendered)
	}
}

func TestManagedProxyDraftIsProtectedRedactedAndRemovedOnCancel(t *testing.T) {
	state := managedProxyTUIFixture(t)
	document, err := getOutboundDocument(state, "to-socks")
	if err != nil {
		t.Fatal(err)
	}
	m := tuiModel{
		state: state, width: 80, height: 20, mode: tuiProxyReview,
		proxyKind: ManagedProxyOutbound, proxyTag: document.Tag, proxyOperation: "replace",
	}
	path, err := m.createManagedProxyDraft(document.RawJSON)
	if err != nil {
		t.Fatal(err)
	}
	m.proxyDraft = path
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("draft permissions = %o, want 600", info.Mode().Perm())
	}
	if filepath.Dir(path) != filepath.Join(filepath.Dir(state.BaseConfig), ".drafts") {
		t.Fatalf("draft path %q is outside the managed state directory", path)
	}
	if err := m.reviewManagedProxyDraft(); err != nil {
		t.Fatal(err)
	}
	for _, rendered := range []string{m.renderProxyReview()} {
		if strings.Contains(rendered, "do-not-render") || strings.Contains(rendered, "account") {
			t.Fatalf("review leaked credentials:\n%s", rendered)
		}
	}

	model, cmd := m.updateProxyReview(tuiRegressionKey(tea.KeyEnter))
	if cmd != nil {
		t.Fatal("review bypassed explicit confirmation")
	}
	confirming := model.(tuiModel)
	if confirming.mode != tuiConfirmMode || confirming.confirm.action != confirmManagedProxyJSON {
		t.Fatalf("review opened mode=%v action=%v", confirming.mode, confirming.confirm.action)
	}
	if rendered := confirming.renderConfirm(); strings.Contains(rendered, "do-not-render") || strings.Contains(rendered, "account") {
		t.Fatalf("confirmation leaked credentials:\n%s", rendered)
	}
	confirming.width, confirming.height = 64, 18
	assertTUIFits(t, confirming.renderConfirm(), 64, 18)

	model, _ = confirming.updateConfirm(tuiRegressionKey('n'))
	cancelled := model.(tuiModel)
	model, _ = cancelled.updateProxyReview(tuiRegressionKey(tea.KeyEscape))
	left := model.(tuiModel)
	if left.mode != tuiProxyMenu {
		t.Fatalf("cancel returned to mode %v, want proxy menu", left.mode)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("discarded secret draft still exists: %v", err)
	}
}

func TestManagedProxyDraftRejectsTagRename(t *testing.T) {
	state := managedProxyTUIFixture(t)
	m := tuiModel{state: state, proxyKind: ManagedProxyOutbound, proxyTag: "to-socks", proxyOperation: "replace"}
	path, err := m.createManagedProxyDraft([]byte(`{"type":"socks","tag":"renamed","server":"example.com","server_port":1080}`))
	if err != nil {
		t.Fatal(err)
	}
	m.proxyDraft = path
	defer m.discardManagedProxyDraft()
	if err := m.reviewManagedProxyDraft(); err == nil || !strings.Contains(err.Error(), "不能把 tag") {
		t.Fatalf("tag rename error = %v", err)
	}
}

func TestManagedProxyImportCopiesFileIntoProtectedDraft(t *testing.T) {
	state := managedProxyTUIFixture(t)
	source := filepath.Join(t.TempDir(), "endpoint.json")
	secret := "imported-secret-must-stay-hidden"
	raw := []byte(`{"type":"wireguard","tag":"wg-exit","private_key":"` + secret + `"}`)
	if err := os.WriteFile(source, raw, 0644); err != nil {
		t.Fatal(err)
	}
	m := tuiModel{
		state: state, width: 72, height: 18,
		proxyKind: ManagedProxyEndpoint, proxyTag: "wg-exit", proxyOperation: "replace",
	}
	if err := m.importManagedProxyDraft(source); err != nil {
		t.Fatal(err)
	}
	defer m.discardManagedProxyDraft()
	if m.proxyDraft == source || filepath.Dir(m.proxyDraft) != filepath.Join(filepath.Dir(state.BaseConfig), ".drafts") {
		t.Fatalf("import was not copied to a protected draft: source=%q draft=%q", source, m.proxyDraft)
	}
	if rendered := m.renderProxyReview(); strings.Contains(rendered, secret) || strings.Contains(rendered, "private_key") {
		t.Fatalf("import review leaked JSON values:\n%s", rendered)
	}
}

func TestAddManagedProxyFormCanStartEndpointJSONEditor(t *testing.T) {
	state := managedProxyTUIFixture(t)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", os.Args[0])
	m := tuiModel{state: state, mode: tuiFormMode, form: tuiForm{kind: formAddManagedProxy, fields: []tuiField{
		{label: "对象类型", value: "端点（底层接口）", options: []string{"出站", "端点（底层接口）"}},
		{label: "tag", value: "new-endpoint"},
		{label: "协议 type", value: "tailscale"},
		{label: "JSON 来源", value: "外部编辑器", options: []string{"外部编辑器", "从文件导入"}},
		{label: "JSON 文件"},
	}}}

	model, cmd := m.submitForm()
	started := model.(tuiModel)
	if cmd == nil || !started.busy || started.mode != tuiProxyReview {
		t.Fatalf("endpoint editor did not start: cmd=%v busy=%v mode=%v status=%q", cmd != nil, started.busy, started.mode, started.status)
	}
	if started.proxyKind != ManagedProxyEndpoint || started.proxyOperation != "add" || started.proxyDraft == "" {
		t.Fatalf("endpoint draft context = kind %v op %q path %q", started.proxyKind, started.proxyOperation, started.proxyDraft)
	}
	defer started.discardManagedProxyDraft()
	raw, err := readManagedProxyJSONFile(started.proxyDraft)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "password") || !strings.Contains(string(raw), `"type": "tailscale"`) || !strings.Contains(string(raw), `"tag": "new-endpoint"`) {
		t.Fatalf("unexpected endpoint skeleton: %s", raw)
	}
}

func TestAddManagedProxyCanImportWithoutExternalEditor(t *testing.T) {
	state := managedProxyTUIFixture(t)
	source := filepath.Join(t.TempDir(), "new-outbound.json")
	if err := os.WriteFile(source, []byte(`{"type":"hysteria2","tag":"to-hy2","server":"hy2.example","server_port":443,"password":"hidden"}`), 0600); err != nil {
		t.Fatal(err)
	}
	m := tuiModel{state: state, width: 64, height: 18, mode: tuiFormMode, form: tuiForm{kind: formAddManagedProxy, fields: []tuiField{
		{label: "对象类型", value: "出站", options: []string{"出站", "端点"}},
		{label: "tag", value: "to-hy2"},
		{label: "协议 type", value: "hysteria2"},
		{label: "JSON 来源", value: "从文件导入", options: []string{"外部编辑器", "从文件导入"}},
		{label: "JSON 文件", value: source},
	}}}

	model, cmd := m.submitForm()
	imported := model.(tuiModel)
	if cmd != nil || imported.mode != tuiProxyReview || imported.proxyIdentity.Tag != "to-hy2" || imported.proxyOperation != "add" {
		t.Fatalf("file add state = cmd %v mode %v identity %#v op %q status %q", cmd != nil, imported.mode, imported.proxyIdentity, imported.proxyOperation, imported.status)
	}
	defer imported.discardManagedProxyDraft()
	if rendered := imported.renderProxyReview(); strings.Contains(rendered, "hidden") || strings.Contains(rendered, "password") {
		t.Fatalf("imported add review leaked secret:\n%s", rendered)
	}
	assertTUIFits(t, imported.renderProxyReview(), 64, 18)
}

func TestHealthPageListsNonServerOutboundsAndEndpointsAtMinimumSize(t *testing.T) {
	state := managedProxyTUIFixture(t)
	documents, err := listManagedProxyDocumentsForTUI(state)
	if err != nil {
		t.Fatal(err)
	}
	endpointCursor := 0
	for index, document := range documents {
		if document.Tag == "wg-exit" {
			endpointCursor = index + 1
		}
	}
	m := tuiModel{state: state, width: 64, height: 18, mode: tuiHealth, healthCursor: endpointCursor}
	rendered := m.renderHealth()
	assertTUIFits(t, rendered, 64, 18)
	if !strings.Contains(rendered, "wg-exit") || !strings.Contains(rendered, "wireguard") {
		t.Fatalf("selected endpoint is not visible:\n%s", rendered)
	}
	if !strings.Contains(rendered, "不可直接分配用户节点") {
		t.Fatalf("endpoint limitations are not visible:\n%s", rendered)
	}
}

func TestStaleManagedProxyDraftCleanupIsStrictAndDoesNotFollowLinks(t *testing.T) {
	state := managedProxyTUIFixture(t)
	directory := filepath.Join(filepath.Dir(state.BaseConfig), ".drafts")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	old := now.Add(-25 * time.Hour)
	write := func(name string) string {
		t.Helper()
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte(`{"type":"direct","tag":"x"}`), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	stale := write("managed-proxy-stale.json")
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	fresh := write("managed-proxy-fresh.json")
	if err := os.Chtimes(fresh, now, now); err != nil {
		t.Fatal(err)
	}
	userImport := write("user-import.json")
	if err := os.Chtimes(userImport, old, old); err != nil {
		t.Fatal(err)
	}
	matchingDirectory := filepath.Join(directory, "managed-proxy-directory.json")
	if err := os.Mkdir(matchingDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external-secret.json")
	if err := os.WriteFile(external, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "managed-proxy-link.json")
	linkCreated := os.Symlink(external, link) == nil

	err := cleanupStaleManagedProxyDrafts(state, now, 24*time.Hour)
	if linkCreated && (err == nil || !strings.Contains(err.Error(), "符号链接")) {
		t.Fatalf("matching symlink was not safely reported: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale managed draft still exists: %v", err)
	}
	for _, path := range []string{fresh, userImport, matchingDirectory, external} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("cleanup touched protected path %q: %v", path, err)
		}
	}
	if linkCreated {
		if _, err := os.Lstat(link); err != nil {
			t.Fatalf("cleanup removed a symlink instead of rejecting it: %v", err)
		}
	}
}

func TestStaleDraftCleanupRejectsDraftDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	base := filepath.Join(root, "config.base.json")
	if err := os.WriteFile(base, []byte(`{"outbounds":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(external, "managed-proxy-stale.json")
	if err := os.WriteFile(secret, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, ".drafts")); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	externalInfo, err := os.Stat(external)
	if err != nil {
		t.Fatal(err)
	}
	err = cleanupStaleManagedProxyDrafts(&State{BaseConfig: base}, time.Now(), 0)
	if err == nil || !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("draft directory symlink was accepted: %v", err)
	}
	m := tuiModel{state: &State{BaseConfig: base}}
	if _, err := m.createManagedProxyDraft([]byte(`{"type":"direct","tag":"must-not-write"}`)); err == nil || !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("draft creation followed a directory symlink: %v", err)
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("external file was touched: %v", err)
	}
	afterInfo, err := os.Stat(external)
	if err != nil {
		t.Fatal(err)
	}
	if afterInfo.Mode().Perm() != externalInfo.Mode().Perm() {
		t.Fatalf("draft creation changed external directory permissions from %o to %o", externalInfo.Mode().Perm(), afterInfo.Mode().Perm())
	}
	entries, err := os.ReadDir(external)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(secret) {
		t.Fatalf("draft creation wrote into external symlink target: %#v", entries)
	}
}

func TestNormalQuitRemovesCurrentManagedProxyDraft(t *testing.T) {
	state := managedProxyTUIFixture(t)
	m := tuiModel{state: state, mode: tuiList, checkedUsers: map[string]bool{}}
	path, err := m.createManagedProxyDraft([]byte(`{"type":"direct","tag":"temporary"}`))
	if err != nil {
		t.Fatal(err)
	}
	m.proxyDraft = path
	model, cmd := m.updateList(tuiRegressionKey('q'))
	quit := model.(tuiModel)
	if cmd == nil || quit.proxyDraft != "" {
		t.Fatalf("normal quit did not clear draft state: cmd=%v path=%q", cmd != nil, quit.proxyDraft)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("normal quit retained managed draft: %v", err)
	}
}
