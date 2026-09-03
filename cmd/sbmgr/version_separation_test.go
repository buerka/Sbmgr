package main

import (
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestRuntimeInterfacesDoNotExposeBinaryVersionManagement(t *testing.T) {
	var help strings.Builder
	usage(&help)
	for _, forbidden := range []string{"admin upgrade", "版本与回滚", "安全回滚", "二进制备份"} {
		if strings.Contains(help.String(), forbidden) {
			t.Errorf("public usage still exposes %q:\n%s", forbidden, help.String())
		}
	}

	a := &app{out: io.Discard, err: io.Discard}
	if err := a.adminCmd([]string{"upgrade", "list"}); err == nil || !strings.Contains(err.Error(), "未知维护操作") {
		t.Fatalf("removed admin upgrade command remained callable: %v", err)
	}

	for _, entry := range manageMenuEntries() {
		combined := entry.title + " " + entry.description
		if strings.Contains(combined, "版本") || strings.Contains(combined, "回滚") {
			if entry.title != "状态备份与恢复" {
				t.Errorf("operations menu still exposes binary version management: %q", combined)
			}
		}
	}
	m := tuiModel{state: &State{Version: stateVersion}, width: 100, height: 24, mode: tuiList}
	if rendered := m.renderManage(); strings.Contains(rendered, "版本与回滚") || strings.Contains(rendered, "安全回滚") {
		t.Fatalf("operations CUI still renders binary version management:\n%s", rendered)
	}
	model, cmd := m.updateList(tea.KeyPressMsg(tea.Key{Code: 'w', Text: "w"}))
	updated := model.(tuiModel)
	if cmd != nil || updated.mode != tuiList {
		t.Fatalf("removed version-management hotkey still changed state: mode=%v cmd=%v", updated.mode, cmd != nil)
	}

	backupPage := m
	backupPage.a = &app{statePath: t.TempDir() + "/state.json"}
	backupPage.mode = tuiBackups
	rendered := backupPage.renderBackups()
	for _, expected := range []string{"状态备份与恢复", "只包含 state.json", "当前基础模板", "不包含程序版本"} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("state backup page is missing %q:\n%s", expected, rendered)
		}
	}
}

func TestAppVersionCanBeInjectedForDisplay(t *testing.T) {
	originalVersion, originalCommit := appVersion, gitCommit
	t.Cleanup(func() { appVersion, gitCommit = originalVersion, originalCommit })
	appVersion = "git-test-build"
	gitCommit = "0123456789abcdef"

	var out strings.Builder
	a := &app{out: &out, err: io.Discard}
	if err := a.run([]string{"version"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "sbmgr git-test-build" {
		t.Fatalf("version output = %q", got)
	}
	out.Reset()
	if err := a.run([]string{"version", "--verbose"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "sbmgr git-test-build\ncommit 0123456789abcdef" {
		t.Fatalf("verbose version output = %q", got)
	}
	if err := a.run([]string{"version", "unexpected"}); err == nil || !strings.Contains(err.Error(), "version [--verbose]") {
		t.Fatalf("unexpected version argument was accepted: %v", err)
	}
}
