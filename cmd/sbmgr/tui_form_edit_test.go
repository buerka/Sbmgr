package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func formSpecialKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code})
}

func formTextKey(text string) tea.KeyPressMsg {
	code := rune(0)
	if runes := []rune(text); len(runes) > 0 {
		code = runes[0]
	}
	return tea.KeyPressMsg(tea.Key{Code: code, Text: text})
}

func updateFormModel(t *testing.T, m tuiModel, key tea.KeyPressMsg) tuiModel {
	t.Helper()
	model, _ := m.updateForm(key)
	updated, ok := model.(tuiModel)
	if !ok {
		t.Fatalf("updateForm returned %T, want tuiModel", model)
	}
	return updated
}

func TestFormTextFieldInsertsUnicodeAtCursor(t *testing.T) {
	m := tuiModel{form: tuiForm{fields: []tuiField{{label: "名称", value: "北京ab"}}}}

	// An untouched populated field starts with its cursor at the end, matching
	// the previous append-only input behavior.
	m = updateFormModel(t, m, formSpecialKey(tea.KeyLeft))
	m = updateFormModel(t, m, formSpecialKey(tea.KeyLeft))
	m = updateFormModel(t, m, formTextKey("新节点"))

	field := m.form.fields[0]
	if field.value != "北京新节点ab" {
		t.Fatalf("inserted value = %q, want %q", field.value, "北京新节点ab")
	}
	if field.cursor != 5 || !field.cursorSet {
		t.Fatalf("cursor after Unicode insert = %d (set=%v), want 5", field.cursor, field.cursorSet)
	}
}

func TestFormTextFieldBackspaceDeleteAndBoundaries(t *testing.T) {
	m := tuiModel{form: tuiForm{fields: []tuiField{{label: "名称", value: "甲乙丙"}}}}

	m = updateFormModel(t, m, formSpecialKey(tea.KeyHome))
	m = updateFormModel(t, m, formSpecialKey(tea.KeyBackspace))
	if got := m.form.fields[0].value; got != "甲乙丙" {
		t.Fatalf("backspace at start changed value to %q", got)
	}

	m = updateFormModel(t, m, formSpecialKey(tea.KeyRight))
	m = updateFormModel(t, m, formSpecialKey(tea.KeyBackspace))
	if got := m.form.fields[0].value; got != "乙丙" {
		t.Fatalf("backspace did not remove rune before cursor: %q", got)
	}
	if got := m.form.fields[0].cursor; got != 0 {
		t.Fatalf("cursor after backspace = %d, want 0", got)
	}

	m = updateFormModel(t, m, formSpecialKey(tea.KeyDelete))
	if got := m.form.fields[0].value; got != "丙" {
		t.Fatalf("delete did not remove rune at cursor: %q", got)
	}
	if got := m.form.fields[0].cursor; got != 0 {
		t.Fatalf("cursor after delete = %d, want 0", got)
	}

	m = updateFormModel(t, m, formSpecialKey(tea.KeyEnd))
	m = updateFormModel(t, m, formSpecialKey(tea.KeyDelete))
	if got := m.form.fields[0].value; got != "丙" {
		t.Fatalf("delete at end changed value to %q", got)
	}
	m = updateFormModel(t, m, formSpecialKey(tea.KeyBackspace))
	m = updateFormModel(t, m, formSpecialKey(tea.KeyLeft))
	m = updateFormModel(t, m, formSpecialKey(tea.KeyRight))
	if field := m.form.fields[0]; field.value != "" || field.cursor != 0 {
		t.Fatalf("empty-field boundary state = value %q cursor %d", field.value, field.cursor)
	}
}

func TestFormOptionFieldKeepsArrowSelectionBehavior(t *testing.T) {
	m := tuiModel{form: tuiForm{fields: []tuiField{{label: "状态", value: "关闭", options: []string{"关闭", "开启"}}}}}

	m = updateFormModel(t, m, formSpecialKey(tea.KeyLeft))
	if got := m.form.fields[0].value; got != "开启" {
		t.Fatalf("left did not wrap option selection: %q", got)
	}
	m = updateFormModel(t, m, formSpecialKey(tea.KeyRight))
	m = updateFormModel(t, m, formSpecialKey(tea.KeyHome))
	m = updateFormModel(t, m, formSpecialKey(tea.KeyDelete))
	m = updateFormModel(t, m, formSpecialKey(tea.KeyBackspace))
	m = updateFormModel(t, m, formTextKey("不应插入"))
	if field := m.form.fields[0]; field.value != "关闭" || field.cursorSet {
		t.Fatalf("option field was treated as text: value %q cursorSet=%v", field.value, field.cursorSet)
	}
}

func TestRenderFormShowsCursorAtRunePosition(t *testing.T) {
	m := tuiModel{
		width:  90,
		height: 24,
		form: tuiForm{fields: []tuiField{
			{label: "节点名称", value: "甲乙", cursor: 1, cursorSet: true},
			{label: "备注", placeholder: "请输入备注"},
		}},
	}

	rendered := m.renderForm()
	if !strings.Contains(rendered, "甲▌乙") {
		t.Fatalf("rendered form does not show the cursor in the value:\n%s", rendered)
	}
	for _, want := range []string{"←→ 光标/选项", "Home/End 首尾", "Del/Backspace 删除"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered form does not advertise %q:\n%s", want, rendered)
		}
	}

	m.form.active = 1
	rendered = m.renderForm()
	if !strings.Contains(rendered, "▌") || !strings.Contains(rendered, "请输入备注") {
		t.Fatalf("empty active field does not show cursor and placeholder:\n%s", rendered)
	}
}

func TestFormCursorClampsAfterExternalValueChange(t *testing.T) {
	m := tuiModel{form: tuiForm{fields: []tuiField{{value: "长文本", cursor: 99, cursorSet: true}}}}
	m = updateFormModel(t, m, formSpecialKey(tea.KeyLeft))
	field := m.form.fields[0]
	if field.cursor != 2 {
		t.Fatalf("clamped cursor after left = %d, want 2", field.cursor)
	}
	m = updateFormModel(t, m, formTextKey("新"))
	if field = m.form.fields[0]; field.value != "长文新本" || field.cursor != 3 {
		t.Fatalf("insert after clamping = value %q cursor %d", field.value, field.cursor)
	}
}

func TestSecretFieldIsMaskedWhileCursorEditing(t *testing.T) {
	secret := "P@ss密🔑"
	m := tuiModel{
		width:  72,
		height: 20,
		form: tuiForm{title: "凭据", fields: []tuiField{{
			label: "新密码", value: secret, cursor: 3, cursorSet: true, secret: true,
		}}},
	}
	rendered := m.renderForm()
	if strings.Contains(rendered, secret) || strings.Contains(rendered, "P@ss") || strings.Contains(rendered, "🔑") {
		t.Fatalf("secret field leaked plaintext:\n%s", rendered)
	}
	if !strings.Contains(rendered, "•••▌•••") {
		t.Fatalf("masked cursor position missing:\n%s", rendered)
	}

	m = updateFormModel(t, m, formSpecialKey(tea.KeyLeft))
	m = updateFormModel(t, m, formTextKey("新"))
	if m.form.fields[0].value != "P@新ss密🔑" || m.form.fields[0].cursor != 3 {
		t.Fatalf("secret cursor edit changed data incorrectly: %#v", m.form.fields[0])
	}
	rendered = m.renderForm()
	if strings.Contains(rendered, "P@") || strings.Contains(rendered, "🔑") {
		t.Fatalf("edited secret leaked plaintext:\n%s", rendered)
	}
}

func TestSecretFieldRejectsMultilinePasteWithoutChangingValue(t *testing.T) {
	m := tuiModel{
		width: 80, height: 20, mode: tuiFormMode,
		form: tuiForm{fields: []tuiField{{label: "新密码", value: "safe", secret: true}}},
	}
	model, _ := m.Update(tea.PasteMsg{Content: "copied-secret\r\n"})
	updated := model.(tuiModel)
	if updated.form.fields[0].value != "safe" || !updated.statusError || !strings.Contains(updated.status, "已拒绝") {
		t.Fatalf("multiline secret paste was not rejected: value=%q status=%q error=%v", updated.form.fields[0].value, updated.status, updated.statusError)
	}

	model, _ = updated.Update(tea.PasteMsg{Content: "-clean-value-"})
	accepted := model.(tuiModel)
	if accepted.form.fields[0].value != "safe-clean-value-" {
		t.Fatalf("single-line secret paste was changed: %q", accepted.form.fields[0].value)
	}
}
