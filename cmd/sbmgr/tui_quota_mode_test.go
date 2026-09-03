package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func setTUIFieldByLabel(t *testing.T, form *tuiForm, label, value string) {
	t.Helper()
	for index := range form.fields {
		if form.fields[index].label == label {
			form.fields[index].value = value
			return
		}
	}
	t.Fatalf("form field %q not found", label)
}

func TestQuotaModeFormsAddEditAndBatch(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(statePath, &State{Version: stateVersion}); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	m := tuiModel{a: a, state: &State{Version: stateVersion}, checkedUsers: map[string]bool{}}
	m.openAddUserForm()
	setTUIFieldByLabel(t, &m.form, "用户名", "alice")
	setTUIFieldByLabel(t, &m.form, "流量配额", "20G")
	setTUIFieldByLabel(t, &m.form, "配额计量", "仅下载")
	_, cmd := m.submitForm()
	if cmd == nil {
		t.Fatal("add user form did not submit")
	}
	if msg := cmd().(tuiActionMsg); msg.err != nil {
		t.Fatal(msg.err)
	}
	state, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	user := findUser(state, "alice")
	if user == nil || user.QuotaMode != quotaModeDownload {
		t.Fatalf("add form quota mode = %#v", user)
	}

	m.state = state
	m.openEditUserForm(*user)
	if got := m.form.fields[1].value; got != "仅下载" {
		t.Fatalf("edit form quota mode = %q", got)
	}
	setTUIFieldByLabel(t, &m.form, "配额计量", "仅上传")
	_, cmd = m.submitForm()
	if msg := cmd().(tuiActionMsg); msg.err != nil {
		t.Fatal(msg.err)
	}
	state, err = loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := findUser(state, "alice").QuotaMode; got != quotaModeUpload {
		t.Fatalf("edit form quota mode = %q", got)
	}

	m.state = state
	m.openBatchUserForm([]string{"alice"})
	setTUIFieldByLabel(t, &m.form, "配额计量", "双向合计")
	model, _ := m.submitForm()
	m = model.(tuiModel)
	if m.mode != tuiConfirmMode || m.confirm.batch == nil || m.confirm.batch.User.QuotaMode == nil || *m.confirm.batch.User.QuotaMode != quotaModeTotal {
		t.Fatalf("batch quota mode patch = %#v", m.confirm.batch)
	}
	if !strings.Contains(m.confirm.prompt, "配额计量=双向合计") {
		t.Fatalf("batch confirmation hides quota mode: %q", m.confirm.prompt)
	}
}

func TestQuotaModeAuditUsesMeasuredUsageAndKeepsRawDirections(t *testing.T) {
	u := User{Upload: 7, Download: 11, QuotaMode: quotaModeDownload}
	got := userTrafficAccountingAudit(u)
	for _, want := range []string{"实际 ↑ 7B / ↓ 11B", "计费 11B", "仅下载"} {
		if !strings.Contains(got, want) {
			t.Fatalf("quota audit missing %q: %s", want, got)
		}
	}
}
