package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestIPPolicyFormHelpExplainsEveryOperatingMode(t *testing.T) {
	tests := []struct {
		name   string
		policy IPPolicy
		edit   func(*tuiForm)
		want   []string
	}{
		{
			name:   "disabled still archives and leaves other policies alone",
			policy: IPPolicy{Enabled: false},
			want:   []string{"所有来源 IP 都可连接", "公网 IP 仍会存档", "配额", "并发限制仍各自生效"},
		},
		{
			name:   "dynamic enforce explains handover NAT and delay",
			policy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "dynamic", MaxIPs: 1},
			want:   []string{"同一 NAT", "可同时使用", "旧绑定 IP 连续 60 秒无新活动", "新 IP 再次连接即可接管", "首次尝试可能短暂失败"},
		},
		{
			name:   "monitor never rejects",
			policy: IPPolicy{Enabled: true, Mode: "monitor", Binding: "dynamic", MaxIPs: 1},
			want:   []string{"任何 IP 都可连接", "不会下发拒绝规则", "动态单活"},
		},
		{
			name:   "automatic binding does not rotate",
			policy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "auto", MaxIPs: 1},
			want:   []string{"第一次观察到", "加入固定列表", "不会因旧 IP 闲置而自动换绑"},
		},
		{
			name:   "manual empty list warns about reject all",
			policy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "manual", MaxIPs: 1},
			want:   []string{"地址变化不会自动换绑", "列表为空会拒绝", "全部连接"},
		},
		{
			name: "temporary IP replaces instead of appends",
			policy: IPPolicy{
				Enabled: true, Mode: "enforce", Binding: "manual", MaxIPs: 1,
				BoundIPs: []string{"203.0.113.10"}, TemporaryIPs: []string{"198.51.100.20"}, TemporaryUntil: time.Now().Add(time.Hour).Format(time.RFC3339Nano),
			},
			want: []string{"临时换绑/解锁", "不是追加到固定 IP", "到期后恢复原绑定"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tuiModel{}
			m.openIPPolicyForm(User{Name: "alice", IPPolicy: tt.policy})
			if tt.edit != nil {
				tt.edit(&m.form)
			}
			got := strings.Join(ipPolicyFormHelp(m.form), "\n")
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("help does not contain %q:\n%s", want, got)
				}
			}
		})
	}
}

func TestDeviceIPPolicyFormUsesDetailedHelp(t *testing.T) {
	m := tuiModel{}
	m.openDeviceIPPolicyForm(User{Name: "alice"}, Device{Name: "phone", IPPolicy: IPPolicy{
		Enabled: true, Mode: "monitor", Binding: "auto", MaxIPs: 2,
	}})
	help := strings.Join(ipPolicyFormHelp(m.form), "\n")
	for _, want := range []string{"任何 IP 都可连接", "自动绑定", "其他 IP 在强制模式下会被拒绝"} {
		if !strings.Contains(help, want) {
			t.Fatalf("device help does not contain %q:\n%s", want, help)
		}
	}
}

func TestIPPolicyFormHelpTracksCurrentSelections(t *testing.T) {
	m := tuiModel{}
	m.openIPPolicyForm(User{Name: "alice", IPPolicy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "dynamic", MaxIPs: 1}})

	m.form.fields[1].value = "仅记录告警"
	if got := strings.Join(ipPolicyFormHelp(m.form), "\n"); !strings.Contains(got, "不会下发拒绝规则") {
		t.Fatalf("mode change was not reflected:\n%s", got)
	}

	m.form.fields[2].value = "手动指定"
	if got := strings.Join(ipPolicyFormHelp(m.form), "\n"); !strings.Contains(got, "地址变化不会自动换绑") {
		t.Fatalf("binding change was not reflected:\n%s", got)
	}

	m.form.fields[6].value = "198.51.100.20"
	m.form.fields[7].value = "30"
	if got := strings.Join(ipPolicyFormHelp(m.form), "\n"); !strings.Contains(got, "当前时长 30 分钟") {
		t.Fatalf("temporary override change was not reflected:\n%s", got)
	}
}

func TestIPPolicyFormFits64x18WithHelpAndActiveField(t *testing.T) {
	m := tuiModel{width: 64, height: 18, mode: tuiFormMode, status: "就绪"}
	m.openIPPolicyForm(User{Name: "alice", IPPolicy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "dynamic", MaxIPs: 1}})
	m.form.active = len(m.form.fields) - 1
	rendered := m.renderForm()
	lines := strings.Split(rendered, "\n")
	if len(lines) > m.height {
		t.Fatalf("IP policy form height = %d, want <= %d:\n%s", len(lines), m.height, rendered)
	}
	for index, line := range lines {
		if width := lipgloss.Width(line); width > m.width {
			t.Fatalf("line %d width = %d, want <= %d: %q", index+1, width, m.width, line)
		}
	}
	for _, want := range []string{"当前选项会怎样工作", "临时分钟数", "字段 8/8", "←→ 光标/选项", "esc 取消"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("64x18 form does not contain %q:\n%s", want, rendered)
		}
	}
}

func TestIPPolicyFullHelpIsScrollableAndFits64x18(t *testing.T) {
	m := tuiModel{width: 64, height: 18, mode: tuiFormMode}
	m.openIPPolicyForm(User{Name: "alice", IPPolicy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "dynamic", MaxIPs: 1}})

	updated := updateFormModel(t, m, formTextKey("?"))
	if !updated.formHelpExpanded {
		t.Fatal("? did not open the complete IP rule explanation")
	}
	allHelp := strings.Join(updated.ipPolicyFormHelpContent(56), "\n")
	for _, want := range []string{"关闭", "执行模式", "动态单活", "自动绑定", "手动指定", "临时替代 IP", "判定、等待与叠加", "默认 60", "任一层不允许都会拒绝"} {
		if !strings.Contains(allHelp, want) {
			t.Fatalf("complete help does not contain %q:\n%s", want, allHelp)
		}
	}

	rendered := updated.renderForm()
	lines := strings.Split(rendered, "\n")
	if len(lines) > updated.height {
		t.Fatalf("complete help height = %d, want <= %d:\n%s", len(lines), updated.height, rendered)
	}
	for index, line := range lines {
		if width := lipgloss.Width(line); width > updated.width {
			t.Fatalf("help line %d width = %d, want <= %d: %q", index+1, width, updated.width, line)
		}
	}

	startOffset := updated.formHelpOffset
	updated = updateFormModel(t, updated, formSpecialKey(tea.KeyDown))
	if updated.formHelpOffset <= startOffset {
		t.Fatal("down did not scroll the complete help")
	}
	updated = updateFormModel(t, updated, formTextKey("?"))
	if updated.formHelpExpanded {
		t.Fatal("? did not return from complete help to the form")
	}
}

func TestBatchIPPolicyHelpMakesKeepSemanticsExplicit(t *testing.T) {
	m := tuiModel{}
	m.openBatchIPForm([]string{"alice", "bob"})
	help := strings.Join(ipPolicyFormHelp(m.form), "\n")
	for _, want := range []string{"只修改不是“保持不变”的字段", "来源 IP 仍会存档", "访问/并发规则仍各自生效"} {
		if !strings.Contains(help, want) {
			t.Fatalf("batch help does not contain %q:\n%s", want, help)
		}
	}
}

func TestIPPolicyDetailSummaryStatesRealEffect(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		policy IPPolicy
		want   []string
	}{
		{name: "off", policy: IPPolicy{}, want: []string{"关闭", "不拒绝/告警", "仅存档"}},
		{name: "dynamic", policy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "dynamic", MaxIPs: 1, HandoverSeconds: 45, BoundIPs: []string{"203.0.113.10"}}, want: []string{"动态单活", "同 NAT 可多设备", "静默 45 秒后换绑"}},
		{name: "auto", policy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "auto", MaxIPs: 2, BoundIPs: []string{"203.0.113.10"}}, want: []string{"自动固定 1/2", "不自动换绑"}},
		{name: "manual reject all", policy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "manual", MaxIPs: 1}, want: []string{"手动固定", "无允许 IP（全部拒绝）"}},
		{name: "monitor", policy: IPPolicy{Enabled: true, Mode: "monitor", Binding: "dynamic", MaxIPs: 1}, want: []string{"仅告警（不拒绝）"}},
		{name: "temporary", policy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "manual", MaxIPs: 1, BoundIPs: []string{"203.0.113.10"}, TemporaryIPs: []string{"198.51.100.20"}, TemporaryUntil: now.Add(time.Hour).Format(time.RFC3339Nano)}, want: []string{"临时替代原绑定", "允许 198.51.100.20"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ipPolicySummaryText(tt.policy, 3, now)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("summary does not contain %q: %s", want, got)
				}
			}
		})
	}
}

func TestIPPolicySummariesWarnWhenUserAndDeviceRulesStack(t *testing.T) {
	now := time.Now()
	user := User{
		IPPolicy: IPPolicy{Enabled: true, Mode: "monitor", Binding: "dynamic", MaxIPs: 1, HandoverSeconds: 60},
		Devices:  []Device{{Name: "phone", IPPolicy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "manual", MaxIPs: 1, BoundIPs: []string{"203.0.113.10"}}}},
	}
	if got := ipPolicySummary(user, now); !strings.Contains(got, "与 1 个设备规则叠加（取更严）") {
		t.Fatalf("user summary omitted stacked device rule: %s", got)
	}
	if got := deviceIPPolicySummary(user, user.Devices[0], now); !strings.Contains(got, "与用户规则叠加（取更严）") {
		t.Fatalf("device summary omitted stacked user rule: %s", got)
	}
}
