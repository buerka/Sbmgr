package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type tuiMode int

const (
	tuiList tuiMode = iota
	tuiBatch
	tuiDetail
	tuiDevices
	tuiConnections
	tuiAccessHistory
	tuiHealth
	tuiSubscriptions
	tuiQRCode
	tuiAudit
	tuiFleet
	tuiAlerts
	tuiBackups
	tuiManage
	tuiUserMenu
	tuiProxyMenu
	tuiProxyReview
	tuiFormMode
	tuiConfirmMode
)

type tuiFormKind int

const (
	formAddUser tuiFormKind = iota
	formCloneUser
	formEditUser
	formBatchUser
	formBatchNode
	formBatchBurst
	formBatchIP
	formBatchAccess
	formEditBurst
	formEditIP
	formAddNode
	formEditNode
	formExport
	formAddDevice
	formEditDeviceIP
	formHealthSettings
	formSubscriptionSettings
	formMihomoTemplate
	formAccessPolicy
	formAddFleet
	formClientEndpoint
	formOutboundEndpoint
	formAddManagedProxy
	formManagedProxyImport
)

type tuiConfirmAction int

const (
	confirmDelete tuiConfirmAction = iota
	confirmToggle
	confirmResetTraffic
	confirmDeleteNode
	confirmApply
	confirmRestoreState
	confirmToggleDevice
	confirmRotateDevice
	confirmDeleteDevice
	confirmRotateSubscription
	confirmRemoveFleet
	confirmBatch
	confirmUnblock
	confirmClientEndpoint
	confirmOutboundEndpoint
	confirmManagedProxyJSON
	confirmDeleteManagedProxy
)

type tuiField struct {
	label       string
	value       string
	placeholder string
	options     []string
	cursor      int
	cursorSet   bool
	secret      bool
}

type tuiForm struct {
	kind         tuiFormKind
	title        string
	user         string
	users        []string
	device       string
	node         string
	endpointTag  string
	endpointType string
	proxyKind    ManagedProxyKind
	proxyTag     string
	proxyOp      string
	fields       []tuiField
	active       int
}

type tuiConfirm struct {
	action              tuiConfirmAction
	user                string
	device              string
	node                string
	backup              string
	server              string
	endpointServer      string
	endpointPort        int
	endpointCredentials OutboundCredentialUpdate
	proxyKind           ManagedProxyKind
	proxyOperation      string
	proxyTag            string
	proxyDraft          string
	prompt              string
	batch               *batchOperation
	returnMode          tuiMode
	returnSelected      string
}

type tuiModel struct {
	a                  *app
	state              *State
	width              int
	height             int
	cursor             int
	nodeCursor         int
	deviceCursor       int
	subscriptionCursor int
	connectionCursor   int
	detailOffset       int
	accessCursor       int
	accessOffset       int
	fleetCursor        int
	backupCursor       int
	menuCursor         int
	proxyMenuCursor    int
	healthCursor       int
	confirmOffset      int
	offset             int
	mode               tuiMode
	selected           string
	qrUser             string
	qrDevice           string
	qrReturnMode       tuiMode
	form               tuiForm
	confirm            tuiConfirm
	proxyKind          ManagedProxyKind
	proxyTag           string
	proxyOperation     string
	proxyDraft         string
	proxyIdentity      ManagedProxyIdentity
	proxyAddress       string
	formHelpExpanded   bool
	formHelpOffset     int
	filter             string
	searching          bool
	accessFilter       string
	accessSearching    bool
	status             string
	statusError        bool
	busy               bool
	pendingApply       bool
	stateGeneration    uint64
	refreshSequence    uint64
	appliedRefreshSeq  uint64
	checkedUsers       map[string]bool
}

type tuiManagedProxyEditorMsg struct {
	path string
	err  error
}

const maxManagedProxyDraftBytes = 1 << 20

type tuiActionMsg struct {
	state   *State
	output  string
	err     error
	pending bool
}

type tuiRefreshTickMsg time.Time

type tuiStateRefreshMsg struct {
	state      *State
	err        error
	generation uint64
	sequence   uint64
}

const tuiRefreshInterval = 3 * time.Second

var (
	tuiAccent       = lipgloss.Color("#7C6CFF")
	tuiAccentBright = lipgloss.Color("#A99CFF")
	tuiGreen        = lipgloss.Color("#55D187")
	tuiYellow       = lipgloss.Color("#F2C14E")
	tuiRed          = lipgloss.Color("#FF6B6B")
	tuiMuted        = lipgloss.Color("#8892A6")
	tuiPanel        = lipgloss.Color("#202637")
	tuiText         = lipgloss.Color("#E8ECF3")
	tuiSelectedText = lipgloss.Color("#111318")

	tuiTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(tuiAccentBright)
	tuiDimStyle   = lipgloss.NewStyle().Foreground(tuiMuted)
	tuiGoodStyle  = lipgloss.NewStyle().Foreground(tuiGreen).Bold(true)
	tuiWarnStyle  = lipgloss.NewStyle().Foreground(tuiYellow).Bold(true)
	tuiBadStyle   = lipgloss.NewStyle().Foreground(tuiRed).Bold(true)
)

func tuiSelectionStyle(width int) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(tuiSelectedText).Background(tuiAccentBright).Bold(true).Width(max(1, width))
}

func (a *app) menu() error {
	s, err := loadState(a.statePath)
	if err != nil {
		return fmt.Errorf("启动 TUI: %w", err)
	}
	status, statusError := "就绪", false
	if cleanupErr := cleanupStaleManagedProxyDrafts(s, time.Now(), 24*time.Hour); cleanupErr != nil {
		status = "旧 JSON 草稿清理已安全跳过：" + cleanupErr.Error()
		statusError = true
	}
	m := tuiModel{
		a:            a,
		state:        s,
		width:        100,
		height:       30,
		status:       status,
		statusError:  statusError,
		pendingApply: configurationPending(s),
		checkedUsers: map[string]bool{},
	}
	_, err = tea.NewProgram(m).Run()
	return err
}

func (m tuiModel) Init() tea.Cmd { return scheduleTUIRefresh() }

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.fixCursor()
		return m, nil
	case tuiRefreshTickMsg:
		next := scheduleTUIRefresh()
		if !m.canAutoRefreshState() || m.a == nil {
			return m, next
		}
		m.refreshSequence++
		return m, tea.Batch(next, loadTUIState(m.a.statePath, m.stateGeneration, m.refreshSequence))
	case tuiStateRefreshMsg:
		// A refresh can finish after the user has opened an editor or a
		// confirmation. Never replace that screen's state snapshot underneath
		// an in-progress decision.
		if msg.err != nil || msg.state == nil || !m.canAutoRefreshState() || msg.generation != m.stateGeneration {
			return m, nil
		}
		if msg.sequence != 0 && msg.sequence <= m.appliedRefreshSeq {
			return m, nil
		}
		m.state = msg.state
		if msg.sequence != 0 {
			m.appliedRefreshSeq = msg.sequence
		}
		m.pendingApply = configurationPending(msg.state)
		m.pruneCheckedUsers()
		if (m.mode == tuiDetail || m.mode == tuiDevices || m.mode == tuiConnections || m.mode == tuiAccessHistory) && findUser(m.state, m.selected) == nil {
			m.mode = tuiList
			m.selected = ""
		}
		m.fixCursor()
		return m, nil
	case tuiActionMsg:
		m.busy = false
		// The action result is a newer state generation than any asynchronous
		// read that was already in flight when the action began.
		m.stateGeneration++
		if msg.state != nil {
			m.state = msg.state
		}
		m.pruneCheckedUsers()
		m.pendingApply = msg.pending
		m.status = msg.output
		m.statusError = msg.err != nil
		if msg.err != nil {
			m.status = msg.err.Error()
		}
		if (m.mode == tuiDetail || m.mode == tuiDevices || m.mode == tuiConnections || m.mode == tuiAccessHistory) && findUser(m.state, m.selected) == nil {
			m.mode = tuiList
			m.selected = ""
		}
		m.fixCursor()
		return m, nil
	case tuiManagedProxyEditorMsg:
		m.busy = false
		if msg.path == "" || msg.path != m.proxyDraft {
			return m, nil
		}
		if msg.err != nil {
			m.status = "外部编辑器退出失败：" + msg.err.Error()
			m.statusError = true
			m.mode = tuiProxyReview
			return m, nil
		}
		if err := m.reviewManagedProxyDraft(); err != nil {
			m.status = err.Error()
			m.statusError = true
			m.mode = tuiProxyReview
			return m, nil
		}
		m.status = "JSON 基础检查通过；确认后还会执行 sing-box 完整校验"
		m.statusError = false
		return m, nil
	case tea.PasteMsg:
		if m.busy {
			return m, nil
		}
		if m.mode == tuiFormMode && m.form.active >= 0 && m.form.active < len(m.form.fields) && m.form.fields[m.form.active].secret && strings.ContainsAny(msg.Content, "\r\n\t") {
			m.status = "密钥或密码粘贴内容包含换行或制表符，已拒绝；请重新复制纯文本值"
			m.statusError = true
			return m, nil
		}
		text := singleLinePaste(msg.Content)
		if m.mode == tuiFormMode {
			return m.updateFormPaste(text)
		}
		if m.mode == tuiAccessHistory && m.accessSearching {
			m.accessFilter += text
			m.accessCursor, m.accessOffset = 0, 0
			return m, nil
		}
		if m.searching {
			m.filter += text
			m.cursor, m.offset = 0, 0
		}
		return m, nil
	}

	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if key.String() == "ctrl+c" {
		m.discardManagedProxyDraft()
		return m, tea.Quit
	}
	if m.busy {
		return m, nil
	}
	if m.mode == tuiAccessHistory && m.accessSearching {
		return m.updateAccessSearch(key)
	}
	if m.searching {
		return m.updateSearch(key)
	}
	switch m.mode {
	case tuiList:
		return m.updateList(key)
	case tuiBatch:
		return m.updateBatch(key)
	case tuiDetail:
		return m.updateDetail(key)
	case tuiDevices:
		return m.updateDevices(key)
	case tuiConnections:
		return m.updateConnections(key)
	case tuiAccessHistory:
		return m.updateAccessHistory(key)
	case tuiHealth:
		return m.updateHealth(key)
	case tuiSubscriptions:
		return m.updateSubscriptions(key)
	case tuiQRCode:
		if key.String() == "q" || key.String() == "esc" || key.String() == "backspace" {
			m.mode = m.qrReturnMode
			if m.mode != tuiSubscriptions && m.mode != tuiDevices {
				m.mode = tuiDevices
			}
		}
		return m, nil
	case tuiAudit:
		if key.String() == "q" || key.String() == "esc" || key.String() == "backspace" {
			m.mode = tuiList
		}
		return m, nil
	case tuiFleet:
		return m.updateFleet(key)
	case tuiAlerts:
		return m.updateAlerts(key)
	case tuiBackups:
		return m.updateBackups(key)
	case tuiManage:
		return m.updateManage(key)
	case tuiUserMenu:
		return m.updateUserMenu(key)
	case tuiProxyMenu:
		return m.updateProxyMenu(key)
	case tuiProxyReview:
		return m.updateProxyReview(key)
	case tuiFormMode:
		return m.updateForm(key)
	case tuiConfirmMode:
		return m.updateConfirm(key)
	default:
		return m, nil
	}
}

func scheduleTUIRefresh() tea.Cmd {
	return tea.Tick(tuiRefreshInterval, func(now time.Time) tea.Msg { return tuiRefreshTickMsg(now) })
}

func loadTUIState(path string, stamp ...uint64) tea.Cmd {
	generation, sequence := uint64(0), uint64(0)
	if len(stamp) > 0 {
		generation = stamp[0]
	}
	if len(stamp) > 1 {
		sequence = stamp[1]
	}
	return func() tea.Msg {
		state, err := loadState(path)
		return tuiStateRefreshMsg{state: state, err: err, generation: generation, sequence: sequence}
	}
}

func (m tuiModel) canAutoRefreshState() bool {
	return !m.busy && m.mode != tuiFormMode && m.mode != tuiConfirmMode
}

func (m tuiModel) updateList(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	users := m.filteredUsers()
	switch key.String() {
	case "q":
		m.discardManagedProxyDraft()
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(users)-1 {
			m.cursor++
		}
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		if len(users) > 0 {
			m.cursor = len(users) - 1
		}
	case "enter":
		if u := m.currentUser(); u != nil {
			m.selected = u.Name
			m.nodeCursor = 0
			m.detailOffset = 0
			m.mode = tuiDetail
		}
	case "space":
		if u := m.currentUser(); u != nil {
			m.toggleCheckedUser(u.Name)
			m.status = fmt.Sprintf("已选择 %d 个用户", len(m.batchSelectedNames()))
			m.statusError = false
		}
	case "A":
		if m.checkedUsers == nil {
			m.checkedUsers = map[string]bool{}
		}
		for _, u := range users {
			m.checkedUsers[strings.ToLower(u.Name)] = true
		}
		m.status = fmt.Sprintf("已选择 %d 个用户", len(m.batchSelectedNames()))
		m.statusError = false
	case "C":
		m.checkedUsers = map[string]bool{}
		m.status, m.statusError = "已清空批量选择", false
	case "B":
		if len(m.batchSelectedNames()) == 0 {
			m.status, m.statusError = "请先按空格勾选要批量管理的用户", true
			return m, nil
		}
		m.mode = tuiBatch
	case "D":
		if u := m.currentUser(); u != nil {
			m.openConfirm(tuiConfirm{action: confirmDelete, user: u.Name, prompt: "删除用户 " + u.Name + " 及其全部 UUID？"})
		}
	case "a":
		m.openAddUserForm()
	case "N":
		m.openCloneUserForm()
	case "U":
		blocked := make([]string, 0)
		for _, u := range m.state.Users {
			if strings.TrimSpace(u.BlockedUntil) != "" {
				blocked = append(blocked, u.Name)
			}
		}
		if len(blocked) == 0 {
			m.status, m.statusError = "当前没有临时封禁用户", false
			return m, nil
		}
		m.openConfirm(tuiConfirm{action: confirmUnblock, prompt: fmt.Sprintf("一键解除 %d 个用户的临时封禁？\n\n%s\n\n异常保护规则会保留，并立即应用配置。", len(blocked), strings.Join(blocked, "、"))})
	case "/":
		m.searching = true
	case "r":
		return m.startAction("正在刷新", func(_ *app) error { return nil })
	case "p":
		m.openConfirm(tuiConfirm{action: confirmApply, prompt: "校验并应用当前所有变更？"})
	case "s":
		return m.startAction("正在同步用量、访问与到期状态", func(a *app) error { return a.daemonCycle() })
	case "!":
		m.mode = tuiAlerts
	case "v":
		m.backupCursor = 0
		m.mode = tuiBackups
	case "h":
		m.healthCursor = 0
		m.mode = tuiHealth
	case "L":
		m.healthCursor = 0
		m.mode = tuiHealth
	case "M", "?":
		m.menuCursor = 0
		m.mode = tuiManage
	case "u":
		m.mode = tuiSubscriptions
	case "T":
		m.form = tuiForm{kind: formMihomoTemplate, title: "Mihomo 导出模板", fields: []tuiField{{label: "模板绝对路径", value: m.state.Client.MihomoTemplate, placeholder: "/root/sbmgr/mihomo.template.yaml；留空关闭"}}}
		m.mode = tuiFormMode
	case "o":
		m.mode = tuiAudit
	case "F":
		m.fleetCursor = 0
		m.mode = tuiFleet
	}
	m.fixCursor()
	return m, nil
}

func (m tuiModel) updateBatch(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	names := m.batchSelectedNames()
	if len(names) == 0 {
		m.mode = tuiList
		m.status, m.statusError = "批量选择已清空", false
		return m, nil
	}
	switch key.String() {
	case "q", "esc", "backspace":
		m.mode = tuiList
	case "e", "1":
		m.openBatchUserForm(names)
	case "l", "2":
		m.openBatchNodeForm(names)
	case "b", "3":
		m.openBatchBurstForm(names)
	case "i", "4":
		m.openBatchIPForm(names)
	case "f", "5":
		m.openBatchAccessForm(names)
	case "c", "C":
		m.checkedUsers = map[string]bool{}
		m.mode = tuiList
		m.status, m.statusError = "已清空批量选择", false
	case "p":
		m.openConfirm(tuiConfirm{action: confirmApply, prompt: "校验并应用当前所有变更？"})
	}
	return m, nil
}

type tuiMenuEntry struct {
	title       string
	description string
}

func manageMenuEntries() []tuiMenuEntry {
	return []tuiMenuEntry{
		{title: "线路与服务器", description: "修改中转入口、落地出口并查看线路健康"},
		{title: "告警中心", description: "查看异常流量、IP、出口和服务告警"},
		{title: "订阅交付", description: "管理每设备订阅服务与外部地址"},
		{title: "状态备份与恢复", description: "只备份 state.json，不管理程序版本"},
		{title: "操作审计", description: "查看最近的管理变更记录"},
		{title: "远端管理节点", description: "只读汇总其他 sbmgr 服务器"},
		{title: "Mihomo 导出母版", description: "设置每用户 YAML 的基础模板"},
		{title: "立即同步数据", description: "同步流量、访问、IP 和到期状态"},
		{title: "应用待处理配置", description: "校验并应用尚未生效的配置变更"},
	}
}

func userMenuEntries() []tuiMenuEntry {
	return []tuiMenuEntry{
		{title: "用户设置", description: "配额、到期、账期和阶梯限速"},
		{title: "设备与凭据", description: "管理设备、UUID、订阅与二维码"},
		{title: "当前连接", description: "查看来源 IP、节点和访问目标"},
		{title: "近期访问", description: "查看最近访问的网站与次数"},
		{title: "异常流量保护", description: "窗口阈值以及软封、硬封策略"},
		{title: "来源 IP 规则", description: "动态单活、自动绑定或仅告警"},
		{title: "访问与并发规则", description: "域名、端口和连接数限制"},
		{title: "重置本月流量", description: "清零统计并保留配额和账期设置"},
		{title: "立即解除临时封禁", description: "保留异常保护规则并恢复访问"},
		{title: "启用或禁用用户", description: "切换当前用户的连接权限"},
		{title: "删除用户", description: "危险：删除全部设备和 UUID"},
		{title: "应用待处理配置", description: "校验并应用尚未生效的配置变更"},
	}
}

func (m tuiModel) updateManage(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	entries := manageMenuEntries()
	switch key.String() {
	case "q", "esc", "backspace":
		m.mode = tuiList
	case "up", "k":
		if m.menuCursor > 0 {
			m.menuCursor--
		}
	case "down", "j":
		if m.menuCursor < len(entries)-1 {
			m.menuCursor++
		}
	case "home", "g":
		m.menuCursor = 0
	case "end", "G":
		m.menuCursor = len(entries) - 1
	case "enter":
		switch m.menuCursor {
		case 0:
			m.healthCursor, m.mode = 0, tuiHealth
		case 1:
			m.mode = tuiAlerts
		case 2:
			m.mode = tuiSubscriptions
		case 3:
			m.backupCursor, m.mode = 0, tuiBackups
		case 4:
			m.mode = tuiAudit
		case 5:
			m.fleetCursor, m.mode = 0, tuiFleet
		case 6:
			m.form = tuiForm{kind: formMihomoTemplate, title: "Mihomo 导出母版", fields: []tuiField{{label: "母版绝对路径", value: m.state.Client.MihomoTemplate, placeholder: "/root/sbmgr/mihomo.template.yaml；留空关闭"}}}
			m.mode = tuiFormMode
		case 7:
			return m.startAction("正在同步流量、访问与到期状态", func(a *app) error { return a.daemonCycle() })
		case 8:
			m.openConfirm(tuiConfirm{action: confirmApply, prompt: "校验并应用当前所有待处理配置？\n\n系统会先备份和校验；应用失败会自动恢复。"})
		}
	}
	return m, nil
}

func (m tuiModel) updateUserMenu(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	entries := userMenuEntries()
	u := findUser(m.state, m.selected)
	if u == nil {
		m.mode = tuiList
		return m, nil
	}
	switch key.String() {
	case "q", "esc", "backspace":
		m.mode = tuiDetail
	case "up", "k":
		if m.menuCursor > 0 {
			m.menuCursor--
		}
	case "down", "j":
		if m.menuCursor < len(entries)-1 {
			m.menuCursor++
		}
	case "home", "g":
		m.menuCursor = 0
	case "end", "G":
		m.menuCursor = len(entries) - 1
	case "enter":
		switch m.menuCursor {
		case 0:
			m.openEditUserForm(*u)
		case 1:
			m.deviceCursor, m.mode = 0, tuiDevices
		case 2:
			m.connectionCursor, m.mode = 0, tuiConnections
		case 3:
			m.accessCursor, m.accessOffset, m.mode = 0, 0, tuiAccessHistory
			m.accessFilter, m.accessSearching = "", false
		case 4:
			m.openBurstForm(*u)
		case 5:
			m.openIPPolicyForm(*u)
		case 6:
			m.openAccessPolicyForm(*u, nil)
		case 7:
			m.openConfirm(tuiConfirm{action: confirmResetTraffic, user: u.Name, prompt: "重置用户 " + u.Name + " 的本月流量？\n\n将清零用户与全部节点的上传/下载、实时速率、趋势和异常流量窗口；配额、附加包、账期日和到期日保持不变。此操作会立即应用配置。"})
		case 8:
			if strings.TrimSpace(u.BlockedUntil) == "" {
				m.status, m.statusError = "用户 "+u.Name+" 当前没有临时封禁", false
				return m, nil
			}
			m.openConfirm(tuiConfirm{action: confirmUnblock, user: u.Name, prompt: "立即解除用户 " + u.Name + " 的临时封禁？\n\n异常保护规则会保留，并立即应用配置。"})
		case 9:
			action := "禁用"
			if !u.Enabled {
				action = "启用"
			}
			m.openConfirm(tuiConfirm{action: confirmToggle, user: u.Name, prompt: action + "用户 " + u.Name + "？"})
		case 10:
			m.openConfirm(tuiConfirm{action: confirmDelete, user: u.Name, prompt: "删除用户 " + u.Name + " 及其全部设备和 UUID？\n\n此操作不会自动删除已导出的客户端文件。"})
		case 11:
			m.openConfirm(tuiConfirm{action: confirmApply, prompt: "校验并应用当前所有待处理配置？"})
		}
	}
	return m, nil
}

func (m tuiModel) updateFleet(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	servers := append([]FleetServer(nil), m.state.Fleet...)
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	switch key.String() {
	case "q", "esc", "backspace":
		m.mode = tuiList
	case "up", "k":
		if m.fleetCursor > 0 {
			m.fleetCursor--
		}
	case "down", "j":
		if m.fleetCursor < len(servers)-1 {
			m.fleetCursor++
		}
	case "r":
		return m.startAction("正在检查远端服务器", func(a *app) error { return a.fleetCmd([]string{"check"}) })
	case "a":
		m.form = tuiForm{kind: formAddFleet, title: "添加远端服务器", fields: []tuiField{
			{label: "显示名称", placeholder: "例如 HK-01"},
			{label: "SSH 主机", placeholder: "IP 或域名"},
			{label: "SSH 端口", value: "22"},
			{label: "SSH 用户", value: "root"},
			{label: "私钥绝对路径", placeholder: "/root/.ssh/id_ed25519"},
			{label: "远端应用目录", value: "/root/sbmgr"},
		}}
		m.mode = tuiFormMode
	case "D":
		if m.fleetCursor < len(servers) {
			server := servers[m.fleetCursor]
			m.openConfirm(tuiConfirm{action: confirmRemoveFleet, server: server.Name, prompt: "从汇总列表移除服务器 " + server.Name + "？不会删除远端任何数据。"})
		}
	}
	return m, nil
}

func (m tuiModel) updateSubscriptions(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	entries := subscriptionTUIEntries(m.state)
	if len(entries) == 0 {
		m.subscriptionCursor = 0
	} else {
		m.subscriptionCursor = min(max(0, m.subscriptionCursor), len(entries)-1)
	}
	switch key.String() {
	case "q", "esc", "backspace":
		m.mode = tuiList
	case "up", "k":
		if m.subscriptionCursor > 0 {
			m.subscriptionCursor--
		}
	case "down", "j":
		if m.subscriptionCursor < len(entries)-1 {
			m.subscriptionCursor++
		}
	case "home", "g":
		m.subscriptionCursor = 0
	case "end", "G":
		m.subscriptionCursor = max(0, len(entries)-1)
	case "enter", "z":
		if len(entries) == 0 {
			m.status, m.statusError = "还没有可展示二维码的用户设备", true
			return m, nil
		}
		entry := entries[m.subscriptionCursor]
		m.selected = entry.User.Name
		m.qrUser, m.qrDevice = entry.User.Name, entry.Device.Name
		m.qrReturnMode = tuiSubscriptions
		m.mode = tuiQRCode
	case "r", "u":
		if len(entries) == 0 {
			m.status, m.statusError = "还没有可轮换订阅凭据的用户设备", true
			return m, nil
		}
		entry := entries[m.subscriptionCursor]
		m.openConfirm(tuiConfirm{action: confirmRotateSubscription, user: entry.User.Name, device: entry.Device.Name, prompt: "轮换设备 " + entry.User.Name + " / " + entry.Device.Name + " 的订阅 token？旧订阅链接和二维码会立即失效，客户端需使用新的 URL。"})
	case "e":
		settings := normalizedSubscriptionSettings(m.state.Subscription)
		enabled := "关闭"
		if settings.Enabled {
			enabled = "开启"
		}
		m.form = tuiForm{kind: formSubscriptionSettings, title: "订阅交付设置", fields: []tuiField{
			{label: "订阅服务", value: enabled, options: []string{"关闭", "开启"}},
			{label: "监听地址", value: settings.Listen, placeholder: "127.0.0.1:18080"},
			{label: "外部基础 URL", value: settings.BaseURL, placeholder: "https://sub.example.com"},
			{label: "TLS 证书绝对路径", value: settings.TLSCertFile, placeholder: "/root/sbmgr/tls/fullchain.pem；留空关闭 HTTPS"},
			{label: "TLS 私钥绝对路径", value: settings.TLSKeyFile, placeholder: "/root/sbmgr/tls/privkey.pem；仅填写路径"},
		}}
		m.mode = tuiFormMode
	}
	return m, nil
}

func (m tuiModel) updateHealth(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	documents, documentErr := listManagedProxyDocumentsForTUI(m.state)
	itemCount := 1 + len(documents)
	if m.healthCursor >= itemCount {
		m.healthCursor = max(0, itemCount-1)
	}
	switch key.String() {
	case "q", "esc", "backspace":
		m.mode = tuiList
	case "up", "k":
		if m.healthCursor > 0 {
			m.healthCursor--
		}
	case "down", "j":
		if m.healthCursor < itemCount-1 {
			m.healthCursor++
		}
	case "home", "g":
		m.healthCursor = 0
	case "end", "G":
		m.healthCursor = max(0, itemCount-1)
	case "r":
		return m.startAction("正在探测出口", func(a *app) error { return a.healthCmd([]string{"check"}) })
	case "enter":
		if m.healthCursor == 0 {
			m.openClientEndpointForm()
			return m, nil
		}
		if documentErr != nil {
			m.status, m.statusError = documentErr.Error(), true
			return m, nil
		}
		if index := m.healthCursor - 1; index >= 0 && index < len(documents) {
			m.openManagedProxyMenu(documents[index])
		}
	case "e":
		if m.healthCursor == 0 {
			m.openClientEndpointForm()
			return m, nil
		}
		if documentErr != nil {
			m.status, m.statusError = documentErr.Error(), true
			return m, nil
		}
		if index := m.healthCursor - 1; index >= 0 && index < len(documents) {
			if err := m.openCommonManagedProxyForm(documents[index]); err != nil {
				m.openManagedProxyMenu(documents[index])
				m.status, m.statusError = "此对象没有常用字段表单，请使用完整 JSON", true
			}
		}
	case "a":
		m.openAddManagedProxyForm()
	case "s":
		settings := normalizedHealthSettings(m.state.Health)
		notifications := normalizedNotificationSettings(m.state.Notifications)
		mode := "自动探测"
		if settings.Mode == "off" {
			mode = "关闭"
		}
		m.form = tuiForm{kind: formHealthSettings, title: "出口健康与通知", fields: []tuiField{
			{label: "健康探测", value: mode, options: []string{"自动探测", "关闭"}},
			{label: "间隔（分钟）", value: strconv.Itoa(settings.IntervalMinutes)},
			{label: "超时（秒）", value: strconv.Itoa(settings.TimeoutSeconds)},
			{label: "失败告警次数", value: strconv.Itoa(settings.AlertAfterFailures)},
			{label: "自定义目标", value: formatHealthTargets(settings.Targets), placeholder: "tag=host:port,tag2=host:port"},
			{label: "Webhook URL", value: notifications.WebhookURL, placeholder: "留空关闭外部通知"},
			{label: "现有密钥", value: "保留", options: []string{"保留", "清除"}},
			{label: "Webhook 新密钥", placeholder: "留空不修改", secret: true},
			{label: "Webhook 超时", value: strconv.Itoa(notifications.TimeoutSeconds)},
		}}
		m.mode = tuiFormMode
	case "F":
		m.fleetCursor, m.mode = 0, tuiFleet
	}
	return m, nil
}

func (m *tuiModel) openClientEndpointForm() {
	port := m.state.Client.Port
	if port <= 0 {
		port = 443
	}
	m.form = tuiForm{kind: formClientEndpoint, title: "修改中转入口", fields: []tuiField{
		{label: "客户端连接地址", value: m.state.Client.Server, placeholder: "IP 或域名，不要带端口"},
		{label: "客户端连接端口", value: strconv.Itoa(port)},
	}}
	m.mode = tuiFormMode
}

func (m *tuiModel) openOutboundEndpointForm(endpoint OutboundEndpointSummary) error {
	fields := []tuiField{
		{label: "落地地址", value: endpoint.Server, placeholder: "IP 或域名，不要带端口"},
		{label: "落地端口", value: strconv.Itoa(endpoint.Port)},
	}
	if outboundSupportsUsername(endpoint.Type) {
		username, err := outboundEndpointUsername(m.state, endpoint.Tag)
		if err != nil {
			return err
		}
		fields = append(fields, tuiField{label: "用户名", value: username, placeholder: "可留空清除"})
	}
	if outboundSupportsPassword(endpoint.Type) {
		fields = append(fields, tuiField{label: "新密码", placeholder: "留空保留现有密码", secret: true})
	}
	m.form = tuiForm{kind: formOutboundEndpoint, title: "编辑落地出口 · " + endpoint.Tag, endpointTag: endpoint.Tag, endpointType: endpoint.Type, fields: fields}
	m.mode = tuiFormMode
	return nil
}

func (m *tuiModel) openAddManagedProxyForm() {
	m.form = tuiForm{kind: formAddManagedProxy, title: "新增 sing-box 对象", fields: []tuiField{
		{label: "对象类型", value: "出站", options: []string{"出站", "端点（底层接口）"}},
		{label: "tag", placeholder: "例如 to-new"},
		{label: "协议 type", placeholder: "例如 socks、vless、wireguard"},
		{label: "JSON 来源", value: "外部编辑器", options: []string{"外部编辑器", "从文件导入"}},
		{label: "JSON 文件", placeholder: "仅从文件导入时填写绝对路径"},
	}}
	m.mode = tuiFormMode
}

func (m *tuiModel) openManagedProxyMenu(document ManagedProxyDocument) {
	m.proxyKind = document.Kind
	m.proxyTag = document.Tag
	m.proxyOperation = "replace"
	m.proxyIdentity = managedProxyIdentity(document)
	m.proxyAddress = managedProxyDocumentAddress(document)
	m.proxyMenuCursor = 0
	m.status, m.statusError = "", false
	m.mode = tuiProxyMenu
}

func (m *tuiModel) openCommonManagedProxyForm(document ManagedProxyDocument) error {
	if document.Kind != ManagedProxyOutbound {
		return fmt.Errorf("%s仅支持完整 JSON 编辑", document.Kind.displayName())
	}
	endpoints, err := listOutboundEndpoints(m.state)
	if err != nil {
		return err
	}
	for _, endpoint := range endpoints {
		if endpoint.Tag == document.Tag {
			return m.openOutboundEndpointForm(endpoint)
		}
	}
	return fmt.Errorf("出站 %q 没有可引导编辑的 server/server_port", document.Tag)
}

func listManagedProxyDocumentsForTUI(state *State) ([]ManagedProxyDocument, error) {
	outbounds, err := listOutboundDocuments(state)
	if err != nil {
		return nil, err
	}
	endpoints, err := listEndpointDocuments(state)
	if err != nil {
		return nil, err
	}
	result := make([]ManagedProxyDocument, 0, len(outbounds)+len(endpoints))
	result = append(result, outbounds...)
	result = append(result, endpoints...)
	return result, nil
}

func managedProxyDocumentAddress(document ManagedProxyDocument) string {
	var object map[string]json.RawMessage
	if json.Unmarshal(document.RawJSON, &object) != nil {
		return "-"
	}
	server, err := optionalJSONString(object, "server")
	if err != nil || server == "" {
		return "-"
	}
	rawPort, ok := object["server_port"]
	if !ok {
		return server
	}
	port, err := decodeOutboundPort(rawPort)
	if err != nil || port <= 0 {
		return server
	}
	return net.JoinHostPort(server, strconv.Itoa(port))
}

func (m tuiModel) managedProxyDocument(kind ManagedProxyKind, tag string) (ManagedProxyDocument, error) {
	if kind == ManagedProxyEndpoint {
		return getEndpointDocument(m.state, tag)
	}
	return getOutboundDocument(m.state, tag)
}

func managedProxyMenuEntries() []tuiMenuEntry {
	return []tuiMenuEntry{
		{title: "常用字段", description: "地址、端口、用户名和新密码；不适用时使用完整 JSON"},
		{title: "完整 JSON", description: "用外部编辑器修改此对象的所有协议字段"},
		{title: "从文件导入", description: "读取一个 JSON 对象并替换此对象"},
		{title: "删除", description: "仅未被用户、路由或其他对象引用时允许"},
	}
}

func (m tuiModel) updateProxyMenu(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	entries := managedProxyMenuEntries()
	switch key.String() {
	case "q", "esc", "backspace":
		m.mode = tuiHealth
	case "up", "k":
		if m.proxyMenuCursor > 0 {
			m.proxyMenuCursor--
		}
	case "down", "j":
		if m.proxyMenuCursor < len(entries)-1 {
			m.proxyMenuCursor++
		}
	case "home", "g":
		m.proxyMenuCursor = 0
	case "end", "G":
		m.proxyMenuCursor = len(entries) - 1
	case "enter":
		document, err := m.managedProxyDocument(m.proxyKind, m.proxyTag)
		if err != nil {
			m.status, m.statusError = err.Error(), true
			return m, nil
		}
		switch m.proxyMenuCursor {
		case 0:
			if err := m.openCommonManagedProxyForm(document); err != nil {
				m.status = "此对象没有常用字段表单，请选择“完整 JSON”"
				m.statusError = true
			}
		case 1:
			cmd, err := m.openManagedProxyEditor(document.Kind, "replace", document.Tag, document.RawJSON)
			if err != nil {
				m.status, m.statusError = err.Error(), true
				return m, nil
			}
			return m, cmd
		case 2:
			m.form = tuiForm{kind: formManagedProxyImport, title: "从文件替换 · " + document.Tag, proxyKind: document.Kind, proxyTag: document.Tag, proxyOp: "replace", fields: []tuiField{{label: "JSON 文件", placeholder: "/root/sbmgr/imports/outbound.json"}}}
			m.mode = tuiFormMode
		case 3:
			users, nodes := managedProxyImpact(m.state, document.Tag)
			m.openConfirm(tuiConfirm{
				action: confirmDeleteManagedProxy, proxyKind: document.Kind, proxyTag: document.Tag,
				prompt: fmt.Sprintf("删除%s %s？\n\n协议：%s\n当前直接影响：%d 个用户、%d 个节点\n\n若仍被用户、路由、detour 或其他配置引用，系统会拒绝删除。确认后会先备份、完整校验并重启 sing-box；失败自动回滚。", document.Kind.displayName(), document.Tag, document.Type, users, nodes),
			})
		}
	}
	return m, nil
}

func (m tuiModel) updateProxyReview(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "q", "esc", "backspace":
		m.discardManagedProxyDraft()
		if m.proxyOperation == "replace" {
			m.mode = tuiProxyMenu
		} else {
			m.mode = tuiHealth
		}
	case "e":
		if m.proxyDraft == "" {
			m.status, m.statusError = "JSON 草稿不存在，请重新开始", true
			return m, nil
		}
		cmd, err := m.launchManagedProxyEditor(m.proxyDraft)
		if err != nil {
			m.status, m.statusError = err.Error(), true
			return m, nil
		}
		return m, cmd
	case "i":
		m.form = tuiForm{kind: formManagedProxyImport, title: "导入完整 JSON", proxyKind: m.proxyKind, proxyTag: m.proxyTag, proxyOp: m.proxyOperation, fields: []tuiField{{label: "JSON 文件", placeholder: "/root/sbmgr/imports/object.json"}}}
		m.mode = tuiFormMode
	case "enter":
		if m.proxyIdentity.Tag == "" || m.proxyDraft == "" {
			m.status, m.statusError = "草稿尚未通过基础检查；请按 e 修正或按 i 重新导入", true
			return m, nil
		}
		users, nodes := managedProxyImpact(m.state, m.proxyIdentity.Tag)
		action := "修改"
		if m.proxyOperation == "add" {
			action = "新增"
			users, nodes = 0, 0
		}
		m.openConfirm(tuiConfirm{
			action: confirmManagedProxyJSON, proxyKind: m.proxyKind, proxyOperation: m.proxyOperation, proxyTag: m.proxyTag, proxyDraft: m.proxyDraft,
			prompt: fmt.Sprintf("%s%s %s？\n\n协议：%s\n目标：%s\n当前直接影响：%d 个用户、%d 个节点\n\n完整 JSON 可能包含凭据，确认页和审计日志不会显示字段值。系统会先备份并执行 sing-box 完整校验，然后重启服务；失败自动回滚。", action, m.proxyKind.displayName(), m.proxyIdentity.Tag, m.proxyIdentity.Type, dash(m.proxyAddress), users, nodes),
		})
	}
	return m, nil
}

func managedProxyImpact(state *State, tag string) (int, int) {
	if state == nil || tag == "" {
		return 0, 0
	}
	used := map[string]bool{}
	nodes := 0
	for _, user := range state.Users {
		for _, node := range user.Nodes {
			if node.Outbound == tag {
				nodes++
				used[user.Name] = true
			}
		}
	}
	return len(used), nodes
}

func (m *tuiModel) openManagedProxyEditor(kind ManagedProxyKind, operation, tag string, rawJSON []byte) (tea.Cmd, error) {
	path, err := m.createManagedProxyDraft(rawJSON)
	if err != nil {
		return nil, err
	}
	m.discardManagedProxyDraft()
	m.proxyKind = kind
	m.proxyTag = tag
	m.proxyOperation = operation
	m.proxyDraft = path
	m.proxyIdentity = ManagedProxyIdentity{}
	m.proxyAddress = ""
	command, err := m.launchManagedProxyEditor(path)
	if err != nil {
		m.discardManagedProxyDraft()
		return nil, err
	}
	return command, nil
}

func (m *tuiModel) createManagedProxyDraft(rawJSON []byte) (string, error) {
	if m.state == nil || strings.TrimSpace(m.state.BaseConfig) == "" {
		return "", fmt.Errorf("状态缺少基础模板路径")
	}
	directory := filepath.Join(filepath.Dir(m.state.BaseConfig), ".drafts")
	directoryInfo, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(directory, 0700); err != nil {
			return "", fmt.Errorf("创建 JSON 草稿目录: %w", err)
		}
		directoryInfo, err = os.Lstat(directory)
	}
	if err != nil {
		return "", fmt.Errorf("检查 JSON 草稿目录: %w", err)
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return "", fmt.Errorf("JSON 草稿目录必须是真实目录，不能是符号链接")
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return "", fmt.Errorf("保护 JSON 草稿目录: %w", err)
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, rawJSON, "", "  "); err != nil {
		return "", fmt.Errorf("JSON 草稿无效: %w", err)
	}
	formatted.WriteByte('\n')
	file, err := os.CreateTemp(directory, "managed-proxy-*.json")
	if err != nil {
		return "", fmt.Errorf("创建 JSON 草稿: %w", err)
	}
	path := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(path)
	}
	if err := file.Chmod(0600); err != nil {
		cleanup()
		return "", fmt.Errorf("保护 JSON 草稿: %w", err)
	}
	if _, err := file.Write(formatted.Bytes()); err != nil {
		cleanup()
		return "", fmt.Errorf("写入 JSON 草稿: %w", err)
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return "", fmt.Errorf("同步 JSON 草稿: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("关闭 JSON 草稿: %w", err)
	}
	return path, nil
}

func managedProxyEditorCommand(path string) (*exec.Cmd, error) {
	candidates := []string{os.Getenv("VISUAL"), os.Getenv("EDITOR"), "nano", "vi"}
	for _, candidate := range candidates {
		parts := strings.Fields(strings.TrimSpace(candidate))
		if len(parts) == 0 {
			continue
		}
		program, err := exec.LookPath(parts[0])
		if err != nil {
			continue
		}
		arguments := append(append([]string(nil), parts[1:]...), path)
		command := exec.Command(program, arguments...)
		command.Dir = filepath.Dir(path)
		return command, nil
	}
	return nil, fmt.Errorf("找不到可用编辑器；请设置 VISUAL/EDITOR，或安装 nano/vi 后重试")
}

func (m *tuiModel) launchManagedProxyEditor(path string) (tea.Cmd, error) {
	command, err := managedProxyEditorCommand(path)
	if err != nil {
		return nil, err
	}
	m.busy = true
	m.mode = tuiProxyReview
	m.status = "已暂停 CUI，正在等待外部编辑器"
	m.statusError = false
	return tea.ExecProcess(command, func(err error) tea.Msg {
		return tuiManagedProxyEditorMsg{path: path, err: err}
	}), nil
}

func readManagedProxyJSONFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("读取 JSON 文件: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("JSON 路径必须是普通文件，不能是目录或符号链接")
	}
	if info.Size() > maxManagedProxyDraftBytes {
		return nil, fmt.Errorf("JSON 文件不能超过 1 MiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("读取 JSON 文件: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("检查 JSON 文件: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return nil, fmt.Errorf("JSON 文件在读取期间发生变化，已拒绝")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxManagedProxyDraftBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取 JSON 文件: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("JSON 文件不能为空")
	}
	if len(raw) > maxManagedProxyDraftBytes {
		return nil, fmt.Errorf("JSON 文件不能超过 1 MiB")
	}
	return raw, nil
}

func cleanupStaleManagedProxyDrafts(state *State, now time.Time, maxAge time.Duration) error {
	if state == nil || strings.TrimSpace(state.BaseConfig) == "" {
		return nil
	}
	directory := filepath.Join(filepath.Dir(state.BaseConfig), ".drafts")
	directoryInfo, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("检查草稿目录: %w", err)
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("草稿目录是符号链接，已拒绝清理")
	}
	if !directoryInfo.IsDir() {
		return fmt.Errorf("草稿路径不是目录，已拒绝清理")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("读取草稿目录: %w", err)
	}
	problems := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "managed-proxy-") || !strings.HasSuffix(name, ".json") || len(name) <= len("managed-proxy-.json") {
			continue
		}
		path := filepath.Join(directory, name)
		info, infoErr := os.Lstat(path)
		if infoErr != nil {
			problems = append(problems, name+" 无法检查")
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			problems = append(problems, name+" 是符号链接")
			continue
		}
		if !info.Mode().IsRegular() || now.Sub(info.ModTime()) < maxAge {
			continue
		}
		if removeErr := os.Remove(path); removeErr != nil {
			problems = append(problems, name+" 删除失败")
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "；"))
	}
	return nil
}

func (m *tuiModel) reviewManagedProxyDraft() error {
	m.proxyIdentity = ManagedProxyIdentity{}
	m.proxyAddress = ""
	raw, err := readManagedProxyJSONFile(m.proxyDraft)
	if err != nil {
		return err
	}
	identity, err := validateManagedProxyJSON(m.proxyKind, raw)
	if err != nil {
		return fmt.Errorf("JSON 基础检查失败: %w", err)
	}
	if m.proxyOperation == "replace" && identity.Tag != m.proxyTag {
		return fmt.Errorf("编辑时不能把 tag %q 改成 %q；请新增新对象后迁移引用", m.proxyTag, identity.Tag)
	}
	if m.proxyOperation == "add" {
		if m.proxyTag != "" && identity.Tag != m.proxyTag {
			return fmt.Errorf("新增草稿的 tag 必须保持为 %q；当前为 %q", m.proxyTag, identity.Tag)
		}
		documents, listErr := listManagedProxyDocumentsForTUI(m.state)
		if listErr != nil {
			return listErr
		}
		for _, document := range documents {
			if document.Tag == identity.Tag {
				return fmt.Errorf("tag %q 已被%s占用", identity.Tag, document.Kind.displayName())
			}
		}
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	m.proxyIdentity = identity
	m.proxyAddress = managedProxyDocumentAddress(ManagedProxyDocument{Kind: m.proxyKind, Tag: identity.Tag, Type: identity.Type, RawJSON: raw})
	m.mode = tuiProxyReview
	return nil
}

func (m *tuiModel) importManagedProxyDraft(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("JSON 文件必须使用绝对路径")
	}
	raw, err := readManagedProxyJSONFile(path)
	if err != nil {
		return err
	}
	draft, err := m.createManagedProxyDraft(raw)
	if err != nil {
		return err
	}
	m.discardManagedProxyDraft()
	m.proxyDraft = draft
	if err := m.reviewManagedProxyDraft(); err != nil {
		return err
	}
	return nil
}

func (m *tuiModel) discardManagedProxyDraft() {
	path := m.proxyDraft
	m.proxyDraft = ""
	m.proxyIdentity = ManagedProxyIdentity{}
	m.proxyAddress = ""
	if path == "" || m.state == nil || strings.TrimSpace(m.state.BaseConfig) == "" {
		return
	}
	directory, err := filepath.Abs(filepath.Join(filepath.Dir(m.state.BaseConfig), ".drafts"))
	if err != nil {
		return
	}
	target, err := filepath.Abs(path)
	if err != nil || filepath.Dir(target) != directory || !strings.HasPrefix(filepath.Base(target), "managed-proxy-") {
		return
	}
	_ = os.Remove(target)
}

func (m tuiModel) updateDetail(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	u := findUser(m.state, m.selected)
	if u == nil {
		m.mode = tuiList
		return m, nil
	}
	switch key.String() {
	case "q", "esc", "backspace":
		m.mode = tuiList
		m.selected = ""
	case "e":
		m.openEditUserForm(*u)
	case "b":
		m.openBurstForm(*u)
	case "i":
		m.openIPPolicyForm(*u)
	case "f":
		m.openAccessPolicyForm(*u, nil)
	case "up", "k":
		if m.nodeCursor > 0 {
			m.nodeCursor--
		}
		m.detailOffset = -1
	case "down", "j":
		if m.nodeCursor < len(u.Nodes)-1 {
			m.nodeCursor++
		}
		m.detailOffset = -1
	case "home", "g":
		m.nodeCursor = 0
		m.detailOffset = -1
	case "end", "G":
		if len(u.Nodes) > 0 {
			m.nodeCursor = len(u.Nodes) - 1
		}
		m.detailOffset = -1
	case "pgup":
		if m.detailOffset < 0 {
			m.detailOffset = 0
		} else {
			m.detailOffset = max(0, m.detailOffset-max(1, m.height-8))
		}
	case "pgdown":
		m.detailOffset = max(0, m.detailOffset) + max(1, m.height-8)
	case "n":
		options := nodeTemplateNames(m.state)
		devices := enabledDeviceNames(*u)
		if len(devices) == 0 {
			m.status, m.statusError = "请先启用或添加一台设备", true
			return m, nil
		}
		selectedDevice := devices[0]
		if len(u.Nodes) > 0 && m.nodeCursor < len(u.Nodes) && deviceEnabled(*u, u.Nodes[m.nodeCursor].Device) {
			selectedDevice = u.Nodes[m.nodeCursor].Device
		}
		m.form = tuiForm{kind: formAddNode, title: "分配节点 · " + u.Name, user: u.Name, fields: []tuiField{
			{label: "所属设备", value: selectedDevice, options: devices},
			{label: "节点线路", value: options[0], options: options},
			{label: "上传 Mbps", value: "0", placeholder: "0 不限"},
			{label: "下载 Mbps", value: "0", placeholder: "0 不限"},
		}}
		m.mode = tuiFormMode
	case "l":
		if len(u.Nodes) == 0 {
			return m, nil
		}
		n := u.Nodes[m.nodeCursor]
		upMbps, downMbps, _ := effectiveNodeRate(*u, n)
		m.form = tuiForm{kind: formEditNode, title: "节点限速 · " + n.Device + " / " + n.Name, user: u.Name, device: n.Device, node: n.Name, fields: []tuiField{
			{label: "上传 Mbps", value: strconv.FormatFloat(upMbps, 'f', -1, 64), placeholder: "0 不限"},
			{label: "下载 Mbps", value: strconv.FormatFloat(downMbps, 'f', -1, 64), placeholder: "0 不限"},
		}}
		m.mode = tuiFormMode
	case "x":
		devices := enabledDeviceNames(*u)
		if len(devices) == 0 {
			m.status, m.statusError = "没有可导出的已启用设备", true
			return m, nil
		}
		selectedDevice := devices[0]
		if len(u.Nodes) > 0 && m.nodeCursor < len(u.Nodes) && deviceEnabled(*u, u.Nodes[m.nodeCursor].Device) {
			selectedDevice = u.Nodes[m.nodeCursor].Device
		}
		path := defaultDeviceExportPath(m.a.statePath, u.Name, selectedDevice, time.Now())
		m.form = tuiForm{kind: formExport, title: "按设备导出 · " + u.Name, user: u.Name, fields: []tuiField{{label: "设备", value: selectedDevice, options: devices}, {label: "输出路径", value: path}}}
		m.mode = tuiFormMode
	case "m":
		m.deviceCursor = 0
		m.mode = tuiDevices
	case "c":
		m.connectionCursor = 0
		m.mode = tuiConnections
	case "v":
		m.accessCursor, m.accessOffset = 0, 0
		m.accessFilter, m.accessSearching = "", false
		m.mode = tuiAccessHistory
	case "?", "M":
		m.menuCursor = 0
		m.mode = tuiUserMenu
	case "space":
		action := "禁用"
		if !u.Enabled {
			action = "启用"
		}
		m.openConfirm(tuiConfirm{action: confirmToggle, user: u.Name, prompt: action + "用户 " + u.Name + "？"})
	case "R", "t":
		m.openConfirm(tuiConfirm{
			action: confirmResetTraffic,
			user:   u.Name,
			prompt: "重置用户 " + u.Name + " 的本月流量？\n\n将清零用户与全部节点的上传/下载、实时速率、趋势和异常流量窗口；配额、附加包、账期日和到期日保持不变。因配额停用的用户会恢复。此操作会立即应用配置。",
		})
	case "U":
		if strings.TrimSpace(u.BlockedUntil) == "" {
			m.status, m.statusError = "用户 "+u.Name+" 当前没有临时封禁", false
			return m, nil
		}
		m.openConfirm(tuiConfirm{action: confirmUnblock, user: u.Name, prompt: "立即解除用户 " + u.Name + " 的临时封禁？\n\n异常保护规则会保留，并立即应用配置。"})
	case "d":
		if len(u.Nodes) == 0 || m.nodeCursor < 0 || m.nodeCursor >= len(u.Nodes) {
			m.status, m.statusError = "当前没有可移除的节点", true
			return m, nil
		}
		if len(nodesForDevice(*u, u.Nodes[m.nodeCursor].Device)) <= 1 {
			m.status = "每台设备至少保留一个 UUID；如需移除请删除整台设备"
			m.statusError = true
			return m, nil
		}
		node := u.Nodes[m.nodeCursor]
		m.openConfirm(tuiConfirm{action: confirmDeleteNode, user: u.Name, device: node.Device, node: node.Name, prompt: "删除节点 " + u.Name + " / " + node.Device + " / " + node.Name + "？"})
	case "D":
		m.openConfirm(tuiConfirm{action: confirmDelete, user: u.Name, prompt: "删除用户 " + u.Name + " 及其全部 UUID？"})
	case "p":
		m.openConfirm(tuiConfirm{action: confirmApply, prompt: "校验并应用当前所有变更？"})
	}
	return m, nil
}

func (m tuiModel) updateConnections(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	connections := activeConnectionsForUser(m.state, m.selected)
	switch key.String() {
	case "q", "esc", "backspace":
		m.mode = tuiDetail
	case "up", "k":
		if m.connectionCursor > 0 {
			m.connectionCursor--
		}
	case "down", "j":
		if m.connectionCursor < len(connections)-1 {
			m.connectionCursor++
		}
	case "r":
		return m.startAction("正在刷新连接和用量", func(a *app) error { return a.daemonCycle() })
	}
	return m, nil
}

func (m tuiModel) updateAccessHistory(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	u := findUser(m.state, m.selected)
	if u == nil {
		m.mode = tuiList
		return m, nil
	}
	accesses := recentAccessesForUser(u, m.accessFilter)
	switch key.String() {
	case "q", "backspace":
		m.mode = tuiDetail
		m.accessFilter, m.accessSearching = "", false
	case "esc":
		if m.accessFilter != "" {
			m.accessFilter = ""
			m.accessCursor, m.accessOffset = 0, 0
		} else {
			m.mode = tuiDetail
		}
	case "up", "k":
		if m.accessCursor > 0 {
			m.accessCursor--
		}
	case "down", "j":
		if m.accessCursor < len(accesses)-1 {
			m.accessCursor++
		}
	case "home", "g":
		m.accessCursor = 0
	case "end", "G":
		if len(accesses) > 0 {
			m.accessCursor = len(accesses) - 1
		}
	case "/":
		m.accessSearching = true
	case "r":
		return m.startAction("正在刷新近期访问", func(a *app) error { return a.daemonCycle() })
	}
	m.fixAccessCursor(len(accesses))
	return m, nil
}

func (m tuiModel) updateAccessSearch(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.accessSearching = false
		m.accessFilter = ""
	case "enter":
		m.accessSearching = false
	case "backspace":
		m.accessFilter = trimLastRune(m.accessFilter)
	default:
		if text := key.Key().Text; text != "" {
			m.accessFilter += text
		}
	}
	m.accessCursor, m.accessOffset = 0, 0
	return m, nil
}

func (m tuiModel) updateDevices(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	u := findUser(m.state, m.selected)
	if u == nil {
		m.mode = tuiList
		return m, nil
	}
	if len(u.Devices) == 0 {
		normalizeDeviceModel(m.state)
	}
	if m.deviceCursor >= len(u.Devices) {
		m.deviceCursor = max(0, len(u.Devices)-1)
	}
	current := &u.Devices[m.deviceCursor]
	switch key.String() {
	case "q", "esc", "backspace":
		m.mode = tuiDetail
	case "up", "k":
		if m.deviceCursor > 0 {
			m.deviceCursor--
		}
	case "down", "j":
		if m.deviceCursor < len(u.Devices)-1 {
			m.deviceCursor++
		}
	case "a":
		from := u.Devices[m.deviceCursor].Name
		m.form = tuiForm{kind: formAddDevice, title: "添加设备 · " + u.Name, user: u.Name, fields: []tuiField{{label: "设备名称", placeholder: "例如 手机"}, {label: "复制节点自", value: from, options: deviceNames(*u)}}}
		m.mode = tuiFormMode
	case "space":
		action := "禁用"
		if !current.Enabled {
			action = "启用"
		}
		m.openConfirm(tuiConfirm{action: confirmToggleDevice, user: u.Name, device: current.Name, prompt: action + "设备 " + u.Name + " / " + current.Name + "？"})
	case "r":
		m.openConfirm(tuiConfirm{action: confirmRotateDevice, user: u.Name, device: current.Name, prompt: "轮换设备 " + u.Name + " / " + current.Name + " 的全部 UUID？旧配置将失效。"})
	case "u":
		m.openConfirm(tuiConfirm{action: confirmRotateSubscription, user: u.Name, device: current.Name, prompt: "轮换设备 " + u.Name + " / " + current.Name + " 的订阅 token？旧订阅链接和二维码会立即失效。"})
	case "z":
		m.qrUser, m.qrDevice = u.Name, current.Name
		m.qrReturnMode = tuiDevices
		m.mode = tuiQRCode
	case "D":
		if len(u.Devices) <= 1 {
			m.status, m.statusError = "每个用户至少保留一台设备", true
			return m, nil
		}
		m.openConfirm(tuiConfirm{action: confirmDeleteDevice, user: u.Name, device: current.Name, prompt: "删除设备 " + u.Name + " / " + current.Name + " 及其全部 UUID？"})
	case "i":
		m.openDeviceIPPolicyForm(*u, *current)
	case "f":
		m.openAccessPolicyForm(*u, current)
	case "x":
		if !current.Enabled {
			m.status, m.statusError = "已禁用设备不能导出配置", true
			return m, nil
		}
		path := defaultDeviceExportPath(m.a.statePath, u.Name, current.Name, time.Now())
		m.form = tuiForm{kind: formExport, title: "导出设备 · " + u.Name + " / " + current.Name, user: u.Name, device: current.Name, fields: []tuiField{{label: "输出路径", value: path}}}
		m.mode = tuiFormMode
	}
	return m, nil
}

func (m tuiModel) updateAlerts(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "q", "esc", "backspace":
		m.mode = tuiList
	case "c":
		return m.startAction("正在标记告警", func(a *app) error { return a.acknowledgeAlerts() })
	}
	return m, nil
}

func (m tuiModel) updateBackups(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	backups, _ := listStateBackups(m.a.statePath)
	switch key.String() {
	case "q", "esc", "backspace":
		m.mode = tuiList
	case "up", "k":
		if m.backupCursor > 0 {
			m.backupCursor--
		}
	case "down", "j":
		if m.backupCursor < len(backups)-1 {
			m.backupCursor++
		}
	case "c":
		return m.startAction("正在创建状态备份", func(a *app) error { return a.backupCmd([]string{"create"}) })
	case "enter":
		if m.backupCursor >= 0 && m.backupCursor < len(backups) {
			backup := backups[m.backupCursor]
			m.openConfirm(tuiConfirm{action: confirmRestoreState, backup: backup.Name, prompt: "恢复状态备份 " + backup.Name + " 并重启 sing-box？当前状态会先自动备份。"})
		}
	}
	return m, nil
}

func (m tuiModel) updateSearch(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.searching = false
		m.filter = ""
	case "enter":
		m.searching = false
	case "backspace":
		m.filter = trimLastRune(m.filter)
	default:
		if text := key.Key().Text; text != "" {
			m.filter += text
		}
	}
	m.cursor, m.offset = 0, 0
	return m, nil
}

func (m tuiModel) updateForm(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if len(m.form.fields) == 0 {
		return m, nil
	}
	if m.formHelpExpanded {
		return m.updateFormHelp(key)
	}
	if m.form.active < 0 {
		m.form.active = 0
	}
	if m.form.active >= len(m.form.fields) {
		m.form.active = len(m.form.fields) - 1
	}

	switch key.String() {
	case "?":
		if isIPPolicyForm(m.form.kind) {
			m.formHelpExpanded = true
			m.formHelpOffset = 0
		}
		return m, nil
	case "esc":
		m.clearFormSecrets()
		if len(m.form.users) > 0 {
			m.mode = tuiBatch
		} else if m.form.kind == formAddManagedProxy {
			m.mode = tuiHealth
		} else if m.form.kind == formManagedProxyImport {
			if m.proxyDraft != "" {
				m.mode = tuiProxyReview
			} else {
				m.mode = tuiProxyMenu
			}
		} else if m.form.kind == formAddFleet {
			m.mode = tuiFleet
		} else if m.form.kind == formHealthSettings || m.form.kind == formClientEndpoint || m.form.kind == formOutboundEndpoint {
			m.mode = tuiHealth
		} else if m.form.kind == formSubscriptionSettings {
			m.mode = tuiSubscriptions
		} else if m.form.device != "" || m.form.kind == formAddDevice || m.form.kind == formEditDeviceIP {
			m.mode = tuiDevices
		} else if m.form.user != "" {
			m.mode = tuiDetail
		} else {
			m.mode = tuiList
		}
		return m, nil
	case "up", "shift+tab":
		if m.form.active > 0 {
			m.form.active--
		}
	case "down", "tab":
		if m.form.active < len(m.form.fields)-1 {
			m.form.active++
		}
	case "left", "right":
		field := &m.form.fields[m.form.active]
		if len(field.options) > 0 {
			direction := 1
			if key.String() == "left" {
				direction = -1
			}
			index := 0
			for i, option := range field.options {
				if option == field.value {
					index = i
					break
				}
			}
			index = (index + direction + len(field.options)) % len(field.options)
			field.value = field.options[index]
		} else {
			field.ensureCursor()
			if key.String() == "left" && field.cursor > 0 {
				field.cursor--
			}
			if key.String() == "right" && field.cursor < len([]rune(field.value)) {
				field.cursor++
			}
		}
	case "home":
		field := &m.form.fields[m.form.active]
		if len(field.options) == 0 {
			field.cursor = 0
			field.cursorSet = true
		}
	case "end":
		field := &m.form.fields[m.form.active]
		if len(field.options) == 0 {
			field.cursor = len([]rune(field.value))
			field.cursorSet = true
		}
	case "enter":
		if m.form.active < len(m.form.fields)-1 {
			m.form.active++
			return m, nil
		}
		return m.submitForm()
	case "backspace":
		field := &m.form.fields[m.form.active]
		if len(field.options) == 0 {
			field.ensureCursor()
			runes := []rune(field.value)
			if field.cursor > 0 {
				runes = append(runes[:field.cursor-1], runes[field.cursor:]...)
				field.cursor--
				field.value = string(runes)
			}
		}
	case "delete":
		field := &m.form.fields[m.form.active]
		if len(field.options) == 0 {
			field.ensureCursor()
			runes := []rune(field.value)
			if field.cursor < len(runes) {
				runes = append(runes[:field.cursor], runes[field.cursor+1:]...)
				field.value = string(runes)
			}
		}
	case "ctrl+u":
		field := &m.form.fields[m.form.active]
		if len(field.options) == 0 {
			field.value = ""
			field.cursor = 0
			field.cursorSet = true
		}
	default:
		field := &m.form.fields[m.form.active]
		if text := key.Key().Text; text != "" && len(field.options) == 0 {
			field.insertText(text)
		}
	}
	return m, nil
}

func (m tuiModel) updateFormPaste(text string) (tea.Model, tea.Cmd) {
	if len(m.form.fields) == 0 || text == "" {
		return m, nil
	}
	m.form.active = min(max(0, m.form.active), len(m.form.fields)-1)
	field := &m.form.fields[m.form.active]
	if len(field.options) == 0 {
		field.insertText(text)
	}
	return m, nil
}

func (f *tuiField) insertText(text string) {
	if text == "" {
		return
	}
	f.ensureCursor()
	runes := []rune(f.value)
	inserted := []rune(text)
	runes = append(runes[:f.cursor], append(inserted, runes[f.cursor:]...)...)
	f.cursor += len(inserted)
	f.value = string(runes)
}

func singleLinePaste(text string) string {
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(text)
	return text
}

func (f *tuiField) ensureCursor() {
	length := len([]rune(f.value))
	if !f.cursorSet {
		f.cursor = length
		f.cursorSet = true
	}
	if f.cursor < 0 {
		f.cursor = 0
	}
	if f.cursor > length {
		f.cursor = length
	}
}

func (f tuiField) valueWithCursor() string {
	runes := []rune(f.value)
	cursor := f.cursor
	if !f.cursorSet {
		cursor = len(runes)
	}
	cursor = min(max(0, cursor), len(runes))
	return string(runes[:cursor]) + "▌" + string(runes[cursor:])
}

func (m *tuiModel) clearFormSecrets() {
	for index := range m.form.fields {
		if m.form.fields[index].secret {
			m.form.fields[index].value = ""
			m.form.fields[index].cursor = 0
			m.form.fields[index].cursorSet = false
		}
	}
}

// valueWindow renders a text field on one terminal line and keeps the logical
// cursor visible. The edge markers make horizontal scrolling explicit without
// changing the underlying value.
func (f tuiField) valueWindow(width int) string {
	width = max(1, width)
	runes := []rune(f.value)
	cursor := f.cursor
	if !f.cursorSet {
		cursor = len(runes)
	}
	cursor = min(max(0, cursor), len(runes))
	start, end := cursor, cursor
	render := func(left, right int) string {
		prefix, suffix := "", ""
		if left > 0 {
			prefix = "‹"
		}
		if right < len(runes) {
			suffix = "›"
		}
		return prefix + string(runes[left:cursor]) + "▌" + string(runes[cursor:right]) + suffix
	}
	for {
		changed := false
		if end < len(runes) && lipgloss.Width(render(start, end+1)) <= width {
			end++
			changed = true
		}
		if start > 0 && lipgloss.Width(render(start-1, end)) <= width {
			start--
			changed = true
		}
		if !changed {
			break
		}
	}
	return render(start, end)
}

func (m *tuiModel) openConfirm(confirm tuiConfirm) {
	confirm.returnMode = m.mode
	confirm.returnSelected = m.selected
	m.confirm = confirm
	m.confirmOffset = 0
	m.mode = tuiConfirmMode
}

func confirmNeedsExplicitYes(action tuiConfirmAction) bool {
	switch action {
	case confirmDelete, confirmResetTraffic, confirmDeleteNode, confirmRestoreState,
		confirmToggle, confirmToggleDevice, confirmRotateDevice, confirmDeleteDevice,
		confirmRotateSubscription, confirmRemoveFleet,
		confirmBatch, confirmUnblock, confirmClientEndpoint, confirmOutboundEndpoint,
		confirmManagedProxyJSON, confirmDeleteManagedProxy:
		return true
	default:
		return false
	}
}

func (m tuiModel) updateConfirm(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "up", "k":
		if m.confirmOffset > 0 {
			m.confirmOffset--
		}
		return m, nil
	case "down", "j":
		_, _, maxOffset := m.confirmPromptWindow()
		if m.confirmOffset < maxOffset {
			m.confirmOffset++
		}
		return m, nil
	case "n", "esc":
		confirm := m.confirm
		m.confirm = tuiConfirm{}
		m.confirmOffset = 0
		m.mode = confirm.returnMode
		m.selected = confirm.returnSelected
		return m, nil
	case "enter":
		if confirmNeedsExplicitYes(m.confirm.action) {
			m.status, m.statusError = "这是高影响操作，请按 y 明确确认", true
			return m, nil
		}
		fallthrough
	case "y":
		action := m.confirm
		m.confirm = tuiConfirm{}
		m.clearFormSecrets()
		m.form = tuiForm{}
		m.confirmOffset = 0
		m.mode = action.returnMode
		m.selected = action.returnSelected
		switch action.action {
		case confirmDelete:
			m.mode, m.selected = tuiList, ""
			return m.startAction("正在删除用户", func(a *app) error { return a.userCmd([]string{"delete", action.user}) })
		case confirmToggle:
			u := findUser(m.state, action.user)
			if u == nil {
				return m, nil
			}
			op := "disable"
			if !u.Enabled {
				op = "enable"
			}
			m.selected = action.user
			m.mode = tuiDetail
			return m.startAction("正在更新用户状态", func(a *app) error { return a.userCmd([]string{op, action.user}) })
		case confirmResetTraffic:
			m.selected = action.user
			m.mode = tuiDetail
			return m.startAction("正在重置本月流量", func(a *app) error {
				return a.trafficCmd([]string{"reset", action.user, "--apply"})
			})
		case confirmDeleteNode:
			m.selected = action.user
			m.mode = tuiDetail
			return m.startAction("正在删除节点", func(a *app) error {
				return a.nodeCmd([]string{"delete", action.user, action.node, "--device", action.device})
			})
		case confirmApply:
			return m.startAction("正在校验并应用配置", func(a *app) error { return a.applyCmd(nil) })
		case confirmRestoreState:
			return m.startAction("正在恢复状态并应用配置", func(a *app) error { return a.backupCmd([]string{"restore", action.backup, "--apply"}) })
		case confirmToggleDevice:
			u := findUser(m.state, action.user)
			if u == nil {
				return m, nil
			}
			device := findDevice(u, action.device)
			if device == nil {
				return m, nil
			}
			op := "disable"
			if !device.Enabled {
				op = "enable"
			}
			m.selected, m.mode = action.user, tuiDevices
			return m.startAction("正在更新设备状态", func(a *app) error { return a.deviceCmd([]string{op, action.user, action.device}) })
		case confirmRotateDevice:
			m.selected, m.mode = action.user, tuiDevices
			return m.startAction("正在轮换设备 UUID", func(a *app) error { return a.deviceCmd([]string{"rotate", action.user, action.device}) })
		case confirmDeleteDevice:
			m.selected, m.mode = action.user, tuiDevices
			return m.startAction("正在删除设备", func(a *app) error { return a.deviceCmd([]string{"delete", action.user, action.device}) })
		case confirmRotateSubscription:
			m.selected, m.mode = action.user, action.returnMode
			if m.mode != tuiSubscriptions && m.mode != tuiDevices {
				m.mode = tuiDevices
			}
			return m.startAction("正在轮换订阅 token", func(a *app) error { return a.deviceCmd([]string{"rotate-link", action.user, action.device}) })
		case confirmRemoveFleet:
			m.mode = tuiFleet
			return m.startAction("正在移除远端服务器", func(a *app) error { return a.fleetCmd([]string{"remove", action.server}) })
		case confirmBatch:
			if action.batch == nil {
				m.mode = tuiBatch
				m.status, m.statusError = "批量操作内容已丢失，请重新填写", true
				return m, nil
			}
			op := *action.batch
			m.mode = tuiBatch
			return m.startAction("正在原子保存批量修改", func(a *app) error { return a.batchUsers(op) })
		case confirmUnblock:
			args := []string{"unblock", "--all", "--apply"}
			m.mode = tuiList
			if action.user != "" {
				args = []string{"unblock", "--apply", action.user}
				m.selected, m.mode = action.user, tuiDetail
			}
			return m.startAction("正在解除临时封禁并应用配置", func(a *app) error { return a.userCmd(args) })
		case confirmClientEndpoint:
			m.mode = tuiHealth
			return m.startAction("正在更新中转入口", func(a *app) error {
				return a.setClientEndpoint(action.endpointServer, action.endpointPort)
			})
		case confirmOutboundEndpoint:
			m.mode = tuiHealth
			return m.startAction("正在备份、校验并更新落地出口", func(a *app) error {
				change, err := a.setOutboundEndpointWithCredentials(action.node, action.endpointServer, action.endpointPort, action.endpointCredentials, true)
				if err != nil {
					return err
				}
				if healthErr := a.healthCmd([]string{"check"}); healthErr != nil {
					fmt.Fprintln(a.err, "出口已更新，但立即健康探测失败:", healthErr)
				}
				if change.Changed {
					credentialText := "凭据保持不变"
					if change.UsernameChanged || change.PasswordChanged {
						credentialText = "凭据已安全更新"
					}
					fmt.Fprintf(a.out, "落地出口 %s 已更新为 %s；%s\n", action.node, net.JoinHostPort(action.endpointServer, strconv.Itoa(action.endpointPort)), credentialText)
				} else {
					fmt.Fprintf(a.out, "落地出口 %s 没有变化\n", action.node)
				}
				return nil
			})
		case confirmManagedProxyJSON:
			raw, err := readManagedProxyJSONFile(action.proxyDraft)
			if err != nil {
				m.mode = tuiProxyReview
				m.status, m.statusError = err.Error(), true
				return m, nil
			}
			kind, operation, tag := action.proxyKind, action.proxyOperation, action.proxyTag
			m.discardManagedProxyDraft()
			m.mode = tuiHealth
			return m.startAction("正在备份、校验并应用完整 JSON", func(a *app) error {
				if operation == "add" {
					if kind == ManagedProxyEndpoint {
						_, err = a.addEndpointJSON(raw, true)
					} else {
						_, err = a.addOutboundJSON(raw, true)
					}
				} else if kind == ManagedProxyEndpoint {
					_, err = a.replaceEndpointJSON(tag, raw, true)
				} else {
					_, err = a.replaceOutboundJSON(tag, raw, true)
				}
				if err == nil {
					fmt.Fprintf(a.out, "%s JSON 已安全应用\n", kind.displayName())
				}
				return err
			})
		case confirmDeleteManagedProxy:
			kind, tag := action.proxyKind, action.proxyTag
			m.mode = tuiHealth
			return m.startAction("正在检查引用并删除 sing-box 对象", func(a *app) error {
				var err error
				if kind == ManagedProxyEndpoint {
					_, err = a.deleteEndpoint(tag, true)
				} else {
					_, err = a.deleteOutbound(tag, true)
				}
				if err == nil {
					fmt.Fprintf(a.out, "%s %s 已删除并应用\n", kind.displayName(), tag)
				}
				return err
			})
		}
	}
	return m, nil
}

func (m *tuiModel) openAddUserForm() {
	options := nodeTemplateNames(m.state)
	m.form = tuiForm{kind: formAddUser, title: "添加用户", fields: []tuiField{
		{label: "用户名", placeholder: "例如 alice"},
		{label: "流量配额", value: "0", placeholder: "100G；0 不限"},
		{label: "配额计量", value: quotaModeOption(quotaModeTotal), options: quotaModeOptions()},
		{label: "到期日", placeholder: "YYYY-MM-DD；留空不限"},
		{label: "初始节点", value: options[0], options: options},
		{label: "上传 Mbps", value: "0", placeholder: "0 不限"},
		{label: "下载 Mbps", value: "0", placeholder: "0 不限"},
	}}
	m.mode = tuiFormMode
}

func (m *tuiModel) openCloneUserForm() {
	users := append([]User(nil), m.state.Users...)
	sort.Slice(users, func(i, j int) bool { return strings.ToLower(users[i].Name) < strings.ToLower(users[j].Name) })
	if len(users) == 0 {
		m.status, m.statusError = "还没有可套用的用户；请先创建一个普通用户", true
		return
	}
	options := make([]string, 0, len(users))
	for _, user := range users {
		options = append(options, user.Name)
	}
	m.form = tuiForm{kind: formCloneUser, title: "套用已有用户创建", fields: []tuiField{
		{label: "模板用户", value: options[0], options: options},
		{label: "新用户名", placeholder: "例如 alice-2"},
	}}
	m.mode = tuiFormMode
}

func (m *tuiModel) openEditUserForm(u User) {
	expires := u.Expires
	policy := normalizedThrottle(u.Throttle)
	tiered := "关闭"
	if u.Throttle.Enabled {
		tiered = "开启"
	}
	billing := normalizedBilling(u.Billing)
	billingEnabled := "关闭"
	if billing.Enabled {
		billingEnabled = "开启"
	}
	m.form = tuiForm{kind: formEditUser, title: "编辑用户 · " + u.Name, user: u.Name, fields: []tuiField{
		{label: "流量配额", value: formatQuotaForInput(u.QuotaBytes), placeholder: "100G；0 不限"},
		{label: "配额计量", value: quotaModeOption(u.QuotaMode), options: quotaModeOptions()},
		{label: "附加流量包", value: formatQuotaForInput(u.ExtraQuotaBytes), placeholder: "20G；0 清除"},
		{label: "到期日", value: expires, placeholder: "留空清除"},
		{label: "每月自动清零", value: billingEnabled, options: []string{"关闭", "开启"}},
		{label: "每月账期日", value: strconv.Itoa(billing.CycleDay), placeholder: "1-28"},
		{label: "阶梯限速", value: tiered, options: []string{"关闭", "开启"}},
		{label: "第一档用量 %", value: strconv.FormatFloat(policy.Tier1Usage, 'f', -1, 64)},
		{label: "第一档速度 %", value: strconv.FormatFloat(policy.Tier1Speed, 'f', -1, 64)},
		{label: "第二档用量 %", value: strconv.FormatFloat(policy.Tier2Usage, 'f', -1, 64)},
		{label: "第二档速度 %", value: strconv.FormatFloat(policy.Tier2Speed, 'f', -1, 64)},
	}}
	m.mode = tuiFormMode
}

func (m *tuiModel) openBatchUserForm(users []string) {
	m.form = tuiForm{kind: formBatchUser, title: fmt.Sprintf("批量用户设置 · %d 人", len(users)), users: append([]string(nil), users...), fields: []tuiField{
		{label: "用户状态", value: "保持不变", options: []string{"保持不变", "启用", "禁用"}},
		{label: "流量配额", placeholder: "留空不改；0 不限；例如 20G"},
		{label: "配额计量", value: "保持不变", options: append([]string{"保持不变"}, quotaModeOptions()...)},
		{label: "附加流量包", placeholder: "留空不改；0 清除；例如 5G"},
		{label: "到期日", placeholder: "留空不改；- 清除；YYYY-MM-DD"},
		{label: "每月自动清零", value: "保持不变", options: []string{"保持不变", "开启", "关闭"}},
		{label: "每月账期日", placeholder: "留空不改；1-28"},
		{label: "阶梯限速", value: "保持不变", options: []string{"保持不变", "开启", "关闭"}},
		{label: "第一档用量 %", placeholder: "留空不改；例如 50"},
		{label: "第一档速度 %", placeholder: "留空不改；例如 50"},
		{label: "第二档用量 %", placeholder: "留空不改；例如 80"},
		{label: "第二档速度 %", placeholder: "留空不改；例如 20"},
	}}
	m.mode = tuiFormMode
}

func (m *tuiModel) openBatchNodeForm(users []string) {
	seen := map[string]string{}
	for _, name := range users {
		if u := findUser(m.state, name); u != nil {
			for _, node := range u.Nodes {
				key := strings.ToLower(node.Name)
				if _, exists := seen[key]; !exists {
					seen[key] = node.Name
				}
			}
		}
	}
	nodeNames := make([]string, 0, len(seen))
	for _, name := range seen {
		nodeNames = append(nodeNames, name)
	}
	sort.Slice(nodeNames, func(i, j int) bool { return strings.ToLower(nodeNames[i]) < strings.ToLower(nodeNames[j]) })
	options := append([]string{"所有节点"}, nodeNames...)
	m.form = tuiForm{kind: formBatchNode, title: fmt.Sprintf("批量节点限速 · %d 人", len(users)), users: append([]string(nil), users...), fields: []tuiField{
		{label: "节点线路", value: options[0], options: options},
		{label: "上传 Mbps", placeholder: "留空不改；0 不限"},
		{label: "下载 Mbps", placeholder: "留空不改；0 不限"},
	}}
	m.mode = tuiFormMode
}

func (m *tuiModel) openBatchBurstForm(users []string) {
	m.form = tuiForm{kind: formBatchBurst, title: fmt.Sprintf("批量异常保护 · %d 人", len(users)), users: append([]string(nil), users...), fields: []tuiField{
		{label: "保护开关", value: "保持不变", options: []string{"保持不变", "开启", "关闭"}},
		{label: "处罚类型", value: "保持不变", options: []string{"保持不变", "软封禁（极低速）", "硬封禁（完全断连）"}},
		{label: "检测窗口（分钟）", placeholder: "留空不改；例如 30"},
		{label: "窗口流量阈值", placeholder: "留空不改；裸数字按 GiB"},
		{label: "封禁时长（分钟）", placeholder: "留空不改；例如 30"},
		{label: "软封上传 Kbps", placeholder: "留空不改；建议 16"},
		{label: "软封下载 Kbps", placeholder: "留空不改；建议 2"},
	}}
	m.mode = tuiFormMode
}

func (m *tuiModel) openBatchIPForm(users []string) {
	m.formHelpExpanded, m.formHelpOffset = false, 0
	m.form = tuiForm{kind: formBatchIP, title: fmt.Sprintf("批量来源 IP 规则 · %d 人", len(users)), users: append([]string(nil), users...), fields: []tuiField{
		{label: "规则开关", value: "保持不变", options: []string{"保持不变", "开启", "关闭"}},
		{label: "执行模式", value: "保持不变", options: []string{"保持不变", "强制限制", "仅记录告警"}},
		{label: "绑定方式", value: "保持不变", options: []string{"保持不变", "动态单活", "自动绑定", "手动指定"}},
		{label: "最多允许 IP", placeholder: "留空不改；动态单活固定为 1"},
		{label: "换网静默等待（秒）", placeholder: "留空不改；1–3600，建议 60"},
		{label: "固定 IP", placeholder: "留空不改；- 清除；逗号分隔"},
		{label: "临时替代 IP", placeholder: "留空不改；- 清除；逗号分隔"},
		{label: "临时分钟数", placeholder: "留空不改；替换临时 IP 时必填"},
	}}
	m.mode = tuiFormMode
}

func (m *tuiModel) openBatchAccessForm(users []string) {
	m.form = tuiForm{kind: formBatchAccess, title: fmt.Sprintf("批量访问与并发规则 · %d 人", len(users)), users: append([]string(nil), users...), fields: []tuiField{
		{label: "允许域名", placeholder: "留空不改；- 清除；逗号分隔"},
		{label: "拒绝域名", placeholder: "留空不改；- 清除；逗号分隔"},
		{label: "拒绝端口", placeholder: "留空不改；- 清除；例如 25,445"},
		{label: "最大活跃连接", placeholder: "留空不改；0 不限"},
		{label: "超限动作", value: "保持不变", options: []string{"保持不变", "仅告警", "禁用设备", "禁用用户"}},
	}}
	m.mode = tuiFormMode
}

func (m *tuiModel) openBurstForm(u User) {
	policy := normalizedBurst(u.Burst)
	enabled := "关闭"
	if u.Burst.Enabled {
		enabled = "开启"
	}
	m.form = tuiForm{kind: formEditBurst, title: "异常流量保护 · " + u.Name, user: u.Name, fields: []tuiField{
		{label: "保护开关", value: enabled, options: []string{"关闭", "开启"}},
		{label: "处罚类型", value: map[string]string{burstActionSoft: "软封禁（极低速）", burstActionHard: "硬封禁（完全断连）"}[policy.Action], options: []string{"软封禁（极低速）", "硬封禁（完全断连）"}},
		{label: "检测窗口（分钟）", value: strconv.Itoa(policy.WindowMinutes)},
		{label: "窗口流量阈值", value: formatQuotaForInput(policy.LimitBytes), placeholder: "裸数字按 GiB"},
		{label: "封禁时长（分钟）", value: strconv.Itoa(policy.BlockMinutes)},
		{label: "软封上传 Kbps", value: strconv.FormatFloat(policy.SoftUploadKbps, 'f', -1, 64)},
		{label: "软封下载 Kbps", value: strconv.FormatFloat(policy.SoftDownloadKbps, 'f', -1, 64)},
	}}
	m.mode = tuiFormMode
}

func (m *tuiModel) openIPPolicyForm(u User) {
	m.formHelpExpanded, m.formHelpOffset = false, 0
	policy := normalizedIPPolicy(u.IPPolicy)
	enabled := "关闭"
	if policy.Enabled {
		enabled = "开启"
	}
	mode := "强制限制"
	if policy.Mode == "monitor" {
		mode = "仅记录告警"
	}
	binding := "自动绑定"
	if policy.Binding == "manual" {
		binding = "手动指定"
	} else if policy.Binding == "dynamic" {
		binding = "动态单活"
	}
	tempMinutes := "0"
	temporaryIPs := policy.TemporaryIPs
	if until, err := time.Parse(time.RFC3339Nano, policy.TemporaryUntil); err == nil && until.After(time.Now()) {
		tempMinutes = strconv.Itoa(max(1, int(math.Ceil(time.Until(until).Minutes()))))
	} else {
		temporaryIPs = nil
	}
	m.form = tuiForm{kind: formEditIP, title: "来源 IP 规则 · " + u.Name, user: u.Name, fields: []tuiField{
		{label: "规则开关", value: enabled, options: []string{"关闭", "开启"}},
		{label: "执行模式", value: mode, options: []string{"强制限制", "仅记录告警"}},
		{label: "绑定方式", value: binding, options: []string{"动态单活", "自动绑定", "手动指定"}},
		{label: "最多允许 IP", value: strconv.Itoa(policy.MaxIPs)},
		{label: "换网静默等待（秒）", value: strconv.Itoa(policy.HandoverSeconds), placeholder: "1–3600，建议 60"},
		{label: "固定 IP", value: strings.Join(policy.BoundIPs, ","), placeholder: "自动绑定时可留空"},
		{label: "临时替代 IP", value: strings.Join(temporaryIPs, ","), placeholder: "留空表示不用临时 IP"},
		{label: "临时分钟数", value: tempMinutes, placeholder: "例如 60"},
	}}
	m.mode = tuiFormMode
}

func (m *tuiModel) openDeviceIPPolicyForm(u User, device Device) {
	m.formHelpExpanded, m.formHelpOffset = false, 0
	policy := normalizedIPPolicy(device.IPPolicy)
	enabled := "关闭"
	if policy.Enabled {
		enabled = "开启"
	}
	mode := "强制限制"
	if policy.Mode == "monitor" {
		mode = "仅记录告警"
	}
	binding := "自动绑定"
	if policy.Binding == "manual" {
		binding = "手动指定"
	} else if policy.Binding == "dynamic" {
		binding = "动态单活"
	}
	tempMinutes := "0"
	temporaryIPs := policy.TemporaryIPs
	if until, err := time.Parse(time.RFC3339Nano, policy.TemporaryUntil); err == nil && until.After(time.Now()) {
		tempMinutes = strconv.Itoa(max(1, int(math.Ceil(time.Until(until).Minutes()))))
	} else {
		temporaryIPs = nil
	}
	m.form = tuiForm{kind: formEditDeviceIP, title: "设备来源 IP · " + u.Name + " / " + device.Name, user: u.Name, device: device.Name, fields: []tuiField{
		{label: "规则开关", value: enabled, options: []string{"关闭", "开启"}},
		{label: "执行模式", value: mode, options: []string{"强制限制", "仅记录告警"}},
		{label: "绑定方式", value: binding, options: []string{"动态单活", "自动绑定", "手动指定"}},
		{label: "最多允许 IP", value: strconv.Itoa(policy.MaxIPs)},
		{label: "换网静默等待（秒）", value: strconv.Itoa(policy.HandoverSeconds), placeholder: "1–3600，建议 60"},
		{label: "固定 IP", value: strings.Join(policy.BoundIPs, ","), placeholder: "自动绑定时可留空"},
		{label: "临时替代 IP", value: strings.Join(temporaryIPs, ","), placeholder: "留空表示不用临时 IP"},
		{label: "临时分钟数", value: tempMinutes, placeholder: "例如 60"},
	}}
	m.mode = tuiFormMode
}

func (m *tuiModel) openAccessPolicyForm(u User, device *Device) {
	policy := u.Access
	title := "访问与并发策略 · " + u.Name
	deviceName := ""
	if device != nil {
		policy = device.Access
		deviceName = device.Name
		title += " / " + device.Name
	}
	policy = normalizedAccessPolicy(policy)
	action := "仅告警"
	if policy.ConnectionAction == "disable-device" {
		action = "禁用设备"
	} else if policy.ConnectionAction == "disable-user" {
		action = "禁用用户"
	}
	ports := make([]string, len(policy.BlockedPorts))
	for index, port := range policy.BlockedPorts {
		ports[index] = strconv.Itoa(port)
	}
	m.form = tuiForm{kind: formAccessPolicy, title: title, user: u.Name, device: deviceName, fields: []tuiField{
		{label: "只允许域名", value: strings.Join(policy.AllowedDomains, ","), placeholder: "留空不限；example.com"},
		{label: "拒绝域名", value: strings.Join(policy.BlockedDomains, ","), placeholder: "example.org,tracker.test"},
		{label: "拒绝端口", value: strings.Join(ports, ","), placeholder: "25,445,6881"},
		{label: "最大活跃连接", value: strconv.Itoa(policy.MaxConnections), placeholder: "0 不限"},
		{label: "超限动作", value: action, options: []string{"仅告警", "禁用设备", "禁用用户"}},
	}}
	m.mode = tuiFormMode
}

func (m tuiModel) submitForm() (tea.Model, tea.Cmd) {
	f := m.form
	value := func(i int) string { return strings.TrimSpace(f.fields[i].value) }
	rawValue := func(i int) string { return f.fields[i].value }
	formError := func(message string) (tea.Model, tea.Cmd) {
		m.status = message
		m.statusError = true
		return m, nil
	}
	optionalInt := func(i int) (*int, error) {
		if value(i) == "" {
			return nil, nil
		}
		parsed, err := strconv.Atoi(value(i))
		if err != nil {
			return nil, fmt.Errorf("%s必须是整数", f.fields[i].label)
		}
		return &parsed, nil
	}
	optionalFloat := func(i int) (*float64, error) {
		if value(i) == "" {
			return nil, nil
		}
		parsed, err := strconv.ParseFloat(value(i), 64)
		if err != nil {
			return nil, fmt.Errorf("%s必须是数值", f.fields[i].label)
		}
		return &parsed, nil
	}
	optionalSize := func(i int) (*int64, error) {
		if value(i) == "" {
			return nil, nil
		}
		parsed, err := parseSize(normalizeQuotaInput(value(i)))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.fields[i].label, err)
		}
		return &parsed, nil
	}
	optionBool := func(input string) *bool {
		if input != "开启" && input != "启用" && input != "关闭" && input != "禁用" {
			return nil
		}
		parsed := input == "开启" || input == "启用"
		return &parsed
	}
	stringPointer := func(input string) *string {
		return &input
	}
	switch f.kind {
	case formAddUser:
		if value(0) == "" {
			return formError("用户名不能为空")
		}
		template, ok := findNodeTemplate(m.state, value(4))
		if !ok {
			return formError("请选择有效的初始节点")
		}
		quota, upMbps, downMbps := normalizeQuotaInput(value(1)), value(5), value(6)
		if upMbps == "" {
			upMbps = "0"
		}
		if downMbps == "" {
			downMbps = "0"
		}
		args := []string{"add", value(0), "--quota", quota, "--quota-mode", quotaModeFromOption(value(2)), "--up-mbps", upMbps, "--down-mbps", downMbps, "--node-name", template.Name}
		if value(3) != "" {
			args = append(args, "--expire", value(3))
		}
		if template.Outbound != "" {
			args = append(args, "--outbound", template.Outbound)
		}
		m.mode = tuiList
		return m.startAction("正在添加用户并生成 UUID", func(a *app) error { return a.userCmd(args) })
	case formCloneUser:
		if value(0) == "" {
			return formError("请选择模板用户")
		}
		if value(1) == "" {
			return formError("新用户名不能为空")
		}
		newName, sourceName := value(1), value(0)
		m.selected, m.mode = newName, tuiDetail
		return m.startAction("正在套用用户模板并生成全新凭据", func(a *app) error {
			return a.userCmd([]string{"clone", newName, "--from", sourceName})
		})
	case formEditUser:
		quota := normalizeQuotaInput(value(0))
		extraQuota := normalizeQuotaInput(value(2))
		tiered := "false"
		if value(6) == "开启" {
			tiered = "true"
		}
		billingEnabled := "false"
		if value(4) == "开启" {
			billingEnabled = "true"
		}
		if value(5) == "" {
			return formError("每月账期日不能为空")
		}
		tierValues := []string{value(7), value(8), value(9), value(10)}
		defaults := []string{"80", "50", "95", "20"}
		for i := range tierValues {
			if tierValues[i] == "" {
				tierValues[i] = defaults[i]
			}
		}
		args := []string{"set", f.user, "--quota", quota, "--quota-mode", quotaModeFromOption(value(1)), "--extra-quota", extraQuota, "--billing-enabled", billingEnabled, "--billing-day", value(5), "--tiered", tiered,
			"--tier1-usage", tierValues[0], "--tier1-speed", tierValues[1], "--tier2-usage", tierValues[2], "--tier2-speed", tierValues[3]}
		if value(3) == "" {
			args = append(args, "--clear-expire")
		} else {
			args = append(args, "--expire", value(3))
		}
		m.selected, m.mode = f.user, tuiDetail
		return m.startAction("正在更新用户", func(a *app) error { return a.userCmd(args) })
	case formBatchUser:
		quota, err := optionalSize(1)
		if err != nil {
			return formError(err.Error())
		}
		extraQuota, err := optionalSize(3)
		if err != nil {
			return formError(err.Error())
		}
		billingDay, err := optionalInt(6)
		if err != nil {
			return formError(err.Error())
		}
		tier1Usage, err := optionalFloat(8)
		if err != nil {
			return formError(err.Error())
		}
		tier1Speed, err := optionalFloat(9)
		if err != nil {
			return formError(err.Error())
		}
		tier2Usage, err := optionalFloat(10)
		if err != nil {
			return formError(err.Error())
		}
		tier2Speed, err := optionalFloat(11)
		if err != nil {
			return formError(err.Error())
		}
		var expires *string
		if value(4) != "" {
			date := value(4)
			if date == "-" {
				date = ""
			}
			expires = &date
		}
		var quotaMode *string
		if value(2) != "保持不变" {
			mode := quotaModeFromOption(value(2))
			quotaMode = &mode
		}
		op := batchOperation{Kind: batchUserSettings, Users: append([]string(nil), f.users...), User: batchUserSettingsPatch{
			Enabled: optionBool(value(0)), QuotaBytes: quota, QuotaMode: quotaMode, ExtraQuotaBytes: extraQuota, Expires: expires,
			BillingEnabled: optionBool(value(5)), BillingDay: billingDay, ThrottleEnabled: optionBool(value(7)),
			Tier1Usage: tier1Usage, Tier1Speed: tier1Speed, Tier2Usage: tier2Usage, Tier2Speed: tier2Speed,
		}}
		return m.confirmBatchOperation(op)
	case formBatchNode:
		upload, err := optionalFloat(1)
		if err != nil {
			return formError(err.Error())
		}
		download, err := optionalFloat(2)
		if err != nil {
			return formError(err.Error())
		}
		nodeName := value(0)
		if nodeName == "所有节点" {
			nodeName = ""
		}
		op := batchOperation{Kind: batchNodeRates, Users: append([]string(nil), f.users...), Node: batchNodeRatesPatch{NodeName: nodeName, UploadMbps: upload, DownloadMbps: download}}
		return m.confirmBatchOperation(op)
	case formBatchBurst:
		window, err := optionalInt(2)
		if err != nil {
			return formError(err.Error())
		}
		limit, err := optionalSize(3)
		if err != nil {
			return formError(err.Error())
		}
		block, err := optionalInt(4)
		if err != nil {
			return formError(err.Error())
		}
		softUp, err := optionalFloat(5)
		if err != nil {
			return formError(err.Error())
		}
		softDown, err := optionalFloat(6)
		if err != nil {
			return formError(err.Error())
		}
		var action *string
		if value(1) == "软封禁（极低速）" {
			action = stringPointer(burstActionSoft)
		} else if value(1) == "硬封禁（完全断连）" {
			action = stringPointer(burstActionHard)
		}
		op := batchOperation{Kind: batchBurstPolicy, Users: append([]string(nil), f.users...), Burst: batchBurstPolicyPatch{Enabled: optionBool(value(0)), Action: action, WindowMinutes: window, LimitBytes: limit, BlockMinutes: block, SoftUploadKbps: softUp, SoftDownloadKbps: softDown}}
		return m.confirmBatchOperation(op)
	case formBatchIP:
		maxIPs, err := optionalInt(3)
		if err != nil {
			return formError(err.Error())
		}
		handoverSeconds, err := optionalInt(4)
		if err != nil {
			return formError(err.Error())
		}
		tempMinutes, err := optionalInt(7)
		if err != nil {
			return formError(err.Error())
		}
		patch := batchIPPolicyPatch{Enabled: optionBool(value(0)), MaxIPs: maxIPs, HandoverSeconds: handoverSeconds, TemporaryMinutes: tempMinutes}
		switch value(1) {
		case "强制限制":
			patch.Mode = stringPointer("enforce")
		case "仅记录告警":
			patch.Mode = stringPointer("monitor")
		}
		switch value(2) {
		case "动态单活":
			patch.Binding = stringPointer("dynamic")
		case "自动绑定":
			patch.Binding = stringPointer("auto")
		case "手动指定":
			patch.Binding = stringPointer("manual")
		}
		if value(5) != "" {
			var values []string
			if value(5) != "-" {
				values, err = parseIPList(value(5))
				if err != nil {
					return formError(err.Error())
				}
			}
			patch.BoundIPs = &values
		}
		if value(6) != "" {
			var values []string
			if value(6) != "-" {
				values, err = parseIPList(value(6))
				if err != nil {
					return formError(err.Error())
				}
			}
			patch.TemporaryIPs = &values
		}
		op := batchOperation{Kind: batchIPPolicy, Users: append([]string(nil), f.users...), IP: patch}
		return m.confirmBatchOperation(op)
	case formBatchAccess:
		maxConnections, err := optionalInt(3)
		if err != nil {
			return formError(err.Error())
		}
		patch := batchAccessPolicyPatch{MaxConnections: maxConnections}
		if value(0) != "" {
			values := []string{}
			if value(0) != "-" {
				values = parseDomainList(value(0))
			}
			patch.AllowedDomains = &values
		}
		if value(1) != "" {
			values := []string{}
			if value(1) != "-" {
				values = parseDomainList(value(1))
			}
			patch.BlockedDomains = &values
		}
		if value(2) != "" {
			values := []int{}
			if value(2) != "-" {
				values, err = parsePortList(value(2))
				if err != nil {
					return formError(err.Error())
				}
			}
			patch.BlockedPorts = &values
		}
		switch value(4) {
		case "仅告警":
			patch.ConnectionAction = stringPointer("alert")
		case "禁用设备":
			patch.ConnectionAction = stringPointer("disable-device")
		case "禁用用户":
			patch.ConnectionAction = stringPointer("disable-user")
		}
		op := batchOperation{Kind: batchAccessPolicy, Users: append([]string(nil), f.users...), Access: patch}
		return m.confirmBatchOperation(op)
	case formEditBurst:
		enabled := "false"
		if value(0) == "开启" {
			enabled = "true"
		}
		if value(2) == "" || value(3) == "" || value(4) == "" || value(5) == "" || value(6) == "" {
			return formError("检测窗口、流量阈值、封禁时长和软封速率都不能为空")
		}
		action := burstActionSoft
		if value(1) == "硬封禁（完全断连）" {
			action = burstActionHard
		}
		args := []string{"set", f.user, "--burst-enabled", enabled, "--burst-action", action, "--burst-window", value(2), "--burst-limit", normalizeQuotaInput(value(3)), "--burst-block", value(4), "--burst-soft-up-kbps", value(5), "--burst-soft-down-kbps", value(6)}
		m.selected, m.mode = f.user, tuiDetail
		return m.startAction("正在更新异常流量保护", func(a *app) error { return a.userCmd(args) })
	case formEditIP:
		enabled, mode, binding := "false", "enforce", "auto"
		if value(0) == "开启" {
			enabled = "true"
		}
		if value(1) == "仅记录告警" {
			mode = "monitor"
		}
		if value(2) == "手动指定" {
			binding = "manual"
		} else if value(2) == "动态单活" {
			binding = "dynamic"
		}
		if value(3) == "" || value(4) == "" {
			return formError("最多允许 IP 和换网静默等待都不能为空")
		}
		tempMinutes := value(7)
		if tempMinutes == "" {
			tempMinutes = "0"
		}
		maxIPs := value(3)
		if binding == "dynamic" {
			maxIPs = "1"
		}
		args := []string{"set", f.user, "--ip-enabled", enabled, "--ip-mode", mode, "--ip-binding", binding,
			"--ip-max", maxIPs, "--ip-handover-seconds", value(4), "--ip-allowed", value(5), "--ip-temp", value(6), "--ip-temp-minutes", tempMinutes}
		m.selected, m.mode = f.user, tuiDetail
		return m.startAction("正在更新来源 IP 规则", func(a *app) error { return a.userCmd(args) })
	case formAddNode:
		deviceName := value(0)
		template, ok := findNodeTemplate(m.state, value(1))
		if !ok {
			return formError("请选择有效的节点线路")
		}
		upMbps, downMbps := value(2), value(3)
		if upMbps == "" {
			upMbps = "0"
		}
		if downMbps == "" {
			downMbps = "0"
		}
		args := []string{"add", f.user, "--name", template.Name, "--device", deviceName, "--up-mbps", upMbps, "--down-mbps", downMbps}
		if template.Outbound != "" {
			args = append(args, "--outbound", template.Outbound)
		}
		m.selected, m.mode = f.user, tuiDetail
		return m.startAction("正在分配节点并生成独立 UUID", func(a *app) error { return a.nodeCmd(args) })
	case formEditNode:
		upMbps, downMbps := value(0), value(1)
		if upMbps == "" {
			upMbps = "0"
		}
		if downMbps == "" {
			downMbps = "0"
		}
		args := []string{"set", f.user, f.node, "--device", f.device, "--up-mbps", upMbps, "--down-mbps", downMbps}
		m.selected, m.mode = f.user, tuiDetail
		return m.startAction("正在更新节点限速", func(a *app) error { return a.nodeCmd(args) })
	case formExport:
		deviceName, path := f.device, value(0)
		if deviceName == "" {
			deviceName, path = value(0), value(1)
		}
		if path == "" {
			return formError("导出路径不能为空")
		}
		m.selected, m.mode = f.user, tuiDetail
		return m.startAction("正在导出配置", func(a *app) error { return a.exportCmd([]string{f.user, "--device", deviceName, "--output", path}) })
	case formAddDevice:
		if value(0) == "" {
			return formError("设备名称不能为空")
		}
		args := []string{"add", f.user, "--name", value(0), "--from", value(1)}
		m.selected, m.mode = f.user, tuiDevices
		return m.startAction("正在添加设备并生成独立 UUID", func(a *app) error { return a.deviceCmd(args) })
	case formEditDeviceIP:
		enabled, mode, binding := "false", "enforce", "auto"
		if value(0) == "开启" {
			enabled = "true"
		}
		if value(1) == "仅记录告警" {
			mode = "monitor"
		}
		if value(2) == "手动指定" {
			binding = "manual"
		} else if value(2) == "动态单活" {
			binding = "dynamic"
		}
		if value(3) == "" || value(4) == "" {
			return formError("最多允许 IP 和换网静默等待都不能为空")
		}
		tempMinutes := value(7)
		if tempMinutes == "" {
			tempMinutes = "0"
		}
		maxIPs := value(3)
		if binding == "dynamic" {
			maxIPs = "1"
		}
		args := []string{"set", f.user, f.device, "--ip-enabled", enabled, "--ip-mode", mode, "--ip-binding", binding,
			"--ip-max", maxIPs, "--ip-handover-seconds", value(4), "--ip-allowed", value(5), "--ip-temp", value(6), "--ip-temp-minutes", tempMinutes}
		m.selected, m.mode = f.user, tuiDevices
		return m.startAction("正在更新设备来源 IP 规则", func(a *app) error { return a.deviceCmd(args) })
	case formAddManagedProxy:
		kind := ManagedProxyOutbound
		if strings.HasPrefix(value(0), "端点") {
			kind = ManagedProxyEndpoint
		}
		tag, proxyType := value(1), value(2)
		if tag == "" || proxyType == "" {
			return formError("tag 和协议 type 都不能为空")
		}
		raw, err := json.MarshalIndent(map[string]string{"type": proxyType, "tag": tag}, "", "  ")
		if err != nil {
			return formError(err.Error())
		}
		if _, err := validateManagedProxyJSON(kind, raw); err != nil {
			return formError(err.Error())
		}
		if value(3) == "从文件导入" {
			path := value(4)
			if path == "" {
				return formError("从文件导入时必须填写 JSON 文件绝对路径")
			}
			imported, err := readManagedProxyJSONFile(path)
			if err != nil {
				return formError(err.Error())
			}
			identity, err := validateManagedProxyJSON(kind, imported)
			if err != nil {
				return formError(err.Error())
			}
			if identity.Tag != tag || identity.Type != proxyType {
				return formError(fmt.Sprintf("导入文件的 tag/type 为 %s/%s，与表单 %s/%s 不一致", identity.Tag, identity.Type, tag, proxyType))
			}
			m.proxyKind, m.proxyTag, m.proxyOperation = kind, tag, "add"
			if err := m.importManagedProxyDraft(path); err != nil {
				return formError(err.Error())
			}
			m.form = tuiForm{}
			m.status, m.statusError = "JSON 已安全导入；确认后还会执行 sing-box 完整校验", false
			return m, nil
		}
		cmd, err := m.openManagedProxyEditor(kind, "add", tag, raw)
		if err != nil {
			return formError(err.Error())
		}
		m.form = tuiForm{}
		return m, cmd
	case formManagedProxyImport:
		path := value(0)
		if path == "" {
			return formError("JSON 文件路径不能为空")
		}
		m.proxyKind = f.proxyKind
		m.proxyTag = f.proxyTag
		m.proxyOperation = f.proxyOp
		if err := m.importManagedProxyDraft(path); err != nil {
			return formError(err.Error())
		}
		m.form = tuiForm{}
		m.status, m.statusError = "JSON 已安全导入；确认后还会执行 sing-box 完整校验", false
		return m, nil
	case formClientEndpoint:
		port, err := strconv.Atoi(value(1))
		if err != nil {
			return formError("客户端连接端口必须是整数")
		}
		if err := validateOutboundServer(value(0)); err != nil {
			return formError("客户端连接地址: " + err.Error())
		}
		if err := validateOutboundPort(port); err != nil {
			return formError("客户端连接端口: " + err.Error())
		}
		before := net.JoinHostPort(m.state.Client.Server, strconv.Itoa(m.state.Client.Port))
		after := net.JoinHostPort(value(0), strconv.Itoa(port))
		m.openConfirm(tuiConfirm{
			action: confirmClientEndpoint, endpointServer: value(0), endpointPort: port,
			prompt: "更新中转入口？\n\n当前：" + before + "\n新的：" + after + "\n\n设备订阅会立即使用新地址；已经导出的 YAML 不会自动变化，需要重新导出。Reality 密钥、SNI 和用户 UUID 保持不变。",
		})
		return m, nil
	case formOutboundEndpoint:
		port, err := strconv.Atoi(value(1))
		if err != nil {
			return formError("落地端口必须是整数")
		}
		if err := validateOutboundServer(value(0)); err != nil {
			return formError("落地地址: " + err.Error())
		}
		if err := validateOutboundPort(port); err != nil {
			return formError("落地端口: " + err.Error())
		}
		endpoints, err := listOutboundEndpoints(m.state)
		if err != nil {
			return formError(err.Error())
		}
		var endpoint *OutboundEndpointSummary
		for index := range endpoints {
			if endpoints[index].Tag == f.endpointTag {
				endpoint = &endpoints[index]
				break
			}
		}
		if endpoint == nil {
			return formError("落地出口已不存在，请返回后刷新")
		}
		if endpoint.Type != f.endpointType {
			return formError("落地出口协议已被其他进程修改，请返回线路页后重新打开")
		}
		credentials := OutboundCredentialUpdate{}
		fieldIndex := 2
		usernameChange := "不支持"
		passwordChange := "不支持"
		if outboundSupportsUsername(endpoint.Type) {
			username := rawValue(fieldIndex)
			fieldIndex++
			usernameChange = "保持"
			currentUsername, usernameErr := outboundEndpointUsername(m.state, endpoint.Tag)
			if usernameErr != nil {
				return formError(usernameErr.Error())
			}
			if username != currentUsername {
				credentials.Username = &username
				if username == "" {
					usernameChange = "清除"
				} else {
					usernameChange = "更新"
				}
			}
		}
		if outboundSupportsPassword(endpoint.Type) {
			passwordChange = "保留"
			password := rawValue(fieldIndex)
			if password != "" {
				credentials.Password = &password
				passwordChange = "更新（不回显）"
			}
		}
		if err := validateOutboundCredentialUpdate(endpoint.Type, credentials); err != nil {
			return formError(err.Error())
		}
		before := net.JoinHostPort(endpoint.Server, strconv.Itoa(endpoint.Port))
		after := net.JoinHostPort(value(0), strconv.Itoa(port))
		credentialSummary := ""
		if outboundSupportsUsername(endpoint.Type) {
			credentialSummary += "\n用户名：" + usernameChange
		}
		if outboundSupportsPassword(endpoint.Type) {
			credentialSummary += "\n密码：" + passwordChange
		}
		m.openConfirm(tuiConfirm{
			action: confirmOutboundEndpoint, node: endpoint.Tag, endpointServer: value(0), endpointPort: port, endpointCredentials: credentials,
			prompt: fmt.Sprintf("更新落地出口 %s？\n\n当前：%s\n新的：%s%s\n影响：%d 个用户、%d 个节点\n\n密码不会显示或写入审计日志。系统会先备份并校验；地址变更安全重载，凭据变更会重启 sing-box；失败自动回滚。", endpoint.Tag, before, after, credentialSummary, endpoint.UserCount, endpoint.NodeCount),
		})
		return m, nil
	case formHealthSettings:
		mode := "auto"
		if value(0) == "关闭" {
			mode = "off"
		}
		for _, index := range []int{1, 2, 3, 8} {
			if value(index) == "" {
				return formError("间隔、超时和失败次数不能为空")
			}
		}
		args := []string{"set", "--mode", mode, "--interval", value(1), "--timeout", value(2), "--failures", value(3), "--targets", value(4), "--webhook", value(5), "--webhook-timeout", value(8)}
		if value(7) != "" {
			args = append(args, "--webhook-secret", value(7))
		} else if value(6) == "清除" {
			args = append(args, "--webhook-secret", "")
		}
		m.mode = tuiHealth
		return m.startAction("正在更新出口健康与通知", func(a *app) error { return a.healthCmd(args) })
	case formSubscriptionSettings:
		enabled := "false"
		if value(0) == "开启" {
			enabled = "true"
		}
		if value(1) == "" {
			return formError("监听地址不能为空")
		}
		args := []string{"set", "--enabled", enabled, "--listen", value(1), "--base-url", value(2), "--tls-cert", value(3), "--tls-key", value(4), "--restart"}
		m.mode = tuiSubscriptions
		return m.startAction("正在更新并重启订阅服务", func(a *app) error { return a.subscriptionCmd(args) })
	case formMihomoTemplate:
		m.mode = tuiList
		return m.startAction("正在校验并保存 Mihomo 模板", func(a *app) error { return a.templateCmd([]string{"set", "--path", value(0)}) })
	case formAccessPolicy:
		if value(3) == "" {
			return formError("最大活跃连接不能为空")
		}
		action := "alert"
		if value(4) == "禁用设备" {
			action = "disable-device"
		} else if value(4) == "禁用用户" {
			action = "disable-user"
		}
		args := []string{"user", f.user}
		if f.device != "" {
			args = []string{"device", f.user, f.device}
		}
		args = append(args, "--allow-domains", value(0), "--block-domains", value(1), "--block-ports", value(2), "--max-connections", value(3), "--connection-action", action)
		m.selected = f.user
		if f.device != "" {
			m.mode = tuiDevices
		} else {
			m.mode = tuiDetail
		}
		return m.startAction("正在更新访问与并发策略", func(a *app) error { return a.policyCmd(args) })
	case formAddFleet:
		for index := range f.fields {
			if value(index) == "" {
				return formError("远端服务器字段不能为空")
			}
		}
		args := []string{"add", "--name", value(0), "--host", value(1), "--port", value(2), "--user", value(3), "--key", value(4), "--app-dir", value(5)}
		m.mode = tuiFleet
		return m.startAction("正在保存远端服务器", func(a *app) error { return a.fleetCmd(args) })
	}
	return m, nil
}

func (m tuiModel) confirmBatchOperation(op batchOperation) (tea.Model, tea.Cmd) {
	_, result, err := applyBatchOperation(m.state, op, time.Now())
	if err != nil {
		m.status, m.statusError = err.Error(), true
		return m, nil
	}
	names := normalizedBatchUserNames(op.Users)
	displayNames := strings.Join(names, "、")
	if len(names) > 8 {
		displayNames = strings.Join(names[:8], "、") + fmt.Sprintf(" 等 %d 人", len(names))
	}
	impact := fmt.Sprintf("实际会改变 %d 个用户", result.Users)
	if op.Kind == batchNodeRates {
		impact += fmt.Sprintf("、%d 个节点", result.Nodes)
	}
	prompt := fmt.Sprintf("批量修改将一次性写入，任一用户校验失败会整批取消。\n\n用户：%s\n内容：%s\n影响：%s\n\n保存后按 p 应用到 sing-box。", displayNames, describeBatchOperation(op), impact)
	opCopy := op
	m.openConfirm(tuiConfirm{action: confirmBatch, prompt: prompt, batch: &opCopy})
	m.status, m.statusError = "请核对批量修改范围", false
	return m, nil
}

func describeBatchOperation(op batchOperation) string {
	parts := []string{}
	boolText := func(value bool) string {
		if value {
			return "开启"
		}
		return "关闭"
	}
	switch op.Kind {
	case batchUserSettings:
		p := op.User
		if p.Enabled != nil {
			parts = append(parts, "用户状态="+map[bool]string{true: "启用", false: "禁用"}[*p.Enabled])
		}
		if p.QuotaBytes != nil {
			parts = append(parts, "配额="+formatQuotaUI(*p.QuotaBytes))
		}
		if p.QuotaMode != nil {
			parts = append(parts, "配额计量="+quotaModeText(*p.QuotaMode))
		}
		if p.ExtraQuotaBytes != nil {
			parts = append(parts, "附加包="+formatQuotaUI(*p.ExtraQuotaBytes))
		}
		if p.Expires != nil {
			parts = append(parts, "到期="+dash(*p.Expires))
		}
		if p.BillingEnabled != nil {
			parts = append(parts, "自动清零="+boolText(*p.BillingEnabled))
		}
		if p.BillingDay != nil {
			parts = append(parts, fmt.Sprintf("账期日=%d", *p.BillingDay))
		}
		if p.ThrottleEnabled != nil {
			parts = append(parts, "阶梯限速="+boolText(*p.ThrottleEnabled))
		}
		for _, item := range []struct {
			name  string
			value *float64
		}{{"一档用量", p.Tier1Usage}, {"一档速度", p.Tier1Speed}, {"二档用量", p.Tier2Usage}, {"二档速度", p.Tier2Speed}} {
			if item.value != nil {
				parts = append(parts, item.name+"="+strconv.FormatFloat(*item.value, 'f', -1, 64)+"%")
			}
		}
	case batchNodeRates:
		name := op.Node.NodeName
		if name == "" {
			name = "所有节点"
		}
		parts = append(parts, "线路="+name)
		if op.Node.UploadMbps != nil {
			parts = append(parts, "上传="+formatMbpsUI(*op.Node.UploadMbps)+" Mbps")
		}
		if op.Node.DownloadMbps != nil {
			parts = append(parts, "下载="+formatMbpsUI(*op.Node.DownloadMbps)+" Mbps")
		}
	case batchBurstPolicy:
		p := op.Burst
		if p.Enabled != nil {
			parts = append(parts, "异常保护="+boolText(*p.Enabled))
		}
		if p.Action != nil {
			parts = append(parts, "处罚="+map[string]string{burstActionSoft: "软封禁", burstActionHard: "硬封禁"}[*p.Action])
		}
		if p.WindowMinutes != nil {
			parts = append(parts, fmt.Sprintf("窗口=%d 分钟", *p.WindowMinutes))
		}
		if p.LimitBytes != nil {
			parts = append(parts, "阈值="+formatSize(*p.LimitBytes))
		}
		if p.BlockMinutes != nil {
			parts = append(parts, fmt.Sprintf("封禁=%d 分钟", *p.BlockMinutes))
		}
		if p.SoftUploadKbps != nil {
			parts = append(parts, fmt.Sprintf("软封上传=%g Kbps", *p.SoftUploadKbps))
		}
		if p.SoftDownloadKbps != nil {
			parts = append(parts, fmt.Sprintf("软封下载=%g Kbps", *p.SoftDownloadKbps))
		}
	case batchIPPolicy:
		p := op.IP
		if p.Enabled != nil {
			parts = append(parts, "IP规则="+boolText(*p.Enabled))
		}
		if p.Mode != nil {
			parts = append(parts, "模式="+map[string]string{"enforce": "强制限制", "monitor": "仅告警"}[*p.Mode])
		}
		if p.Binding != nil {
			parts = append(parts, "绑定="+map[string]string{"dynamic": "动态单活", "auto": "自动绑定", "manual": "手动指定"}[*p.Binding])
		}
		if p.MaxIPs != nil {
			parts = append(parts, fmt.Sprintf("最多IP=%d", *p.MaxIPs))
		}
		if p.HandoverSeconds != nil {
			parts = append(parts, fmt.Sprintf("换网静默=%d 秒", *p.HandoverSeconds))
		}
		if p.BoundIPs != nil {
			parts = append(parts, "固定IP="+dash(strings.Join(*p.BoundIPs, ",")))
		}
		if p.TemporaryIPs != nil {
			parts = append(parts, "临时IP="+dash(strings.Join(*p.TemporaryIPs, ",")))
		}
		if p.TemporaryMinutes != nil {
			parts = append(parts, fmt.Sprintf("临时=%d 分钟", *p.TemporaryMinutes))
		}
	case batchAccessPolicy:
		p := op.Access
		if p.AllowedDomains != nil {
			parts = append(parts, "允许域名="+dash(strings.Join(*p.AllowedDomains, ",")))
		}
		if p.BlockedDomains != nil {
			parts = append(parts, "拒绝域名="+dash(strings.Join(*p.BlockedDomains, ",")))
		}
		if p.BlockedPorts != nil {
			values := make([]string, len(*p.BlockedPorts))
			for index, port := range *p.BlockedPorts {
				values[index] = strconv.Itoa(port)
			}
			parts = append(parts, "拒绝端口="+dash(strings.Join(values, ",")))
		}
		if p.MaxConnections != nil {
			parts = append(parts, fmt.Sprintf("最大连接=%d", *p.MaxConnections))
		}
		if p.ConnectionAction != nil {
			parts = append(parts, "超限动作="+map[string]string{"alert": "仅告警", "disable-device": "禁用设备", "disable-user": "禁用用户"}[*p.ConnectionAction])
		}
	}
	return strings.Join(parts, "；")
}

func (m tuiModel) startAction(label string, fn func(*app) error) (tea.Model, tea.Cmd) {
	m.busy = true
	m.status = label + "…"
	m.statusError = false
	return m, runTUIAction(m.a, fn)
}

func runTUIAction(a *app, fn func(*app) error) tea.Cmd {
	return func() tea.Msg {
		var stdout, stderr bytes.Buffer
		copyApp := *a
		copyApp.out, copyApp.err = &stdout, &stderr
		err := fn(&copyApp)
		s, loadErr := loadState(a.statePath)
		if err == nil && loadErr != nil {
			err = loadErr
		}
		output := lastOutputLine(stdout.String())
		if output == "" {
			output = lastOutputLine(stderr.String())
		}
		if output == "" && err == nil {
			output = "操作完成"
		}
		return tuiActionMsg{state: s, output: output, err: err, pending: s != nil && configurationPending(s)}
	}
}

func (m tuiModel) View() tea.View {
	content := m.render()
	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "sbmgr · sing-box 用户管理"
	return v
}

func (m tuiModel) render() string {
	if m.width < 64 || m.height < 18 {
		return tuiWarnStyle.Render("终端窗口太小") + "\n\n请调整到至少 64×18。\n\nctrl+c 退出"
	}
	var body string
	switch m.mode {
	case tuiList:
		body = m.renderList()
	case tuiBatch:
		body = m.renderBatch()
	case tuiDetail:
		body = m.renderDetail()
	case tuiDevices:
		body = m.renderDevices()
	case tuiConnections:
		body = m.renderConnections()
	case tuiAccessHistory:
		body = m.renderAccessHistory()
	case tuiHealth:
		body = m.renderHealth()
	case tuiSubscriptions:
		body = m.renderSubscriptions()
	case tuiQRCode:
		body = m.renderQRCode()
	case tuiAudit:
		body = m.renderAudit()
	case tuiFleet:
		body = m.renderFleet()
	case tuiAlerts:
		body = m.renderAlerts()
	case tuiBackups:
		body = m.renderBackups()
	case tuiManage:
		body = m.renderManage()
	case tuiUserMenu:
		body = m.renderUserMenu()
	case tuiProxyMenu:
		body = m.renderProxyMenu()
	case tuiProxyReview:
		body = m.renderProxyReview()
	case tuiFormMode:
		body = m.renderForm()
	case tuiConfirmMode:
		body = m.renderConfirm()
	}
	return body
}

func (m tuiModel) renderHeader(section string) string {
	title := tuiTitleStyle.Render("◆ sbmgr")
	subtitle := tuiDimStyle.Render("sing-box 多用户管理")
	left := title + "  " + subtitle
	if m.width >= 92 {
		user, network, operations := tuiDimStyle.Render("用户"), tuiDimStyle.Render("线路"), tuiDimStyle.Render("运维")
		switch m.mode {
		case tuiHealth, tuiProxyMenu, tuiProxyReview:
			network = tuiTitleStyle.Render("线路")
		case tuiManage, tuiSubscriptions, tuiAudit, tuiFleet, tuiAlerts, tuiBackups:
			operations = tuiTitleStyle.Render("运维")
		default:
			user = tuiTitleStyle.Render("用户")
		}
		left += "    " + user + tuiDimStyle.Render(" / ") + network + tuiDimStyle.Render(" / ") + operations
	}
	right := tuiDimStyle.Render(section)
	gap := max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right)-2)
	return " " + left + strings.Repeat(" ", gap) + right
}

func (m tuiModel) renderList() string {
	users := m.filteredUsers()
	enabled, limited, stopped, softBlocked, hardBlocked, ipLimited := 0, 0, 0, 0, 0, 0
	for _, u := range m.state.Users {
		if u.Enabled {
			enabled++
		}
		if userRateLimited(u) {
			limited++
		}
		if expired(u, time.Now()) || overQuota(u) || !u.Enabled {
			stopped++
		}
		if burstSoftBlocked(u, time.Now()) {
			softBlocked++
		}
		if burstHardBlocked(u, time.Now()) {
			hardBlocked++
		}
		if u.IPPolicy.Enabled {
			ipLimited++
		}
	}
	pending := tuiGoodStyle.Render("已同步")
	if m.pendingApply || runtimeApplyPending(m.state) {
		pending = tuiWarnStyle.Render("待应用")
	}
	selectedCount := len(m.batchSelectedNames())
	summary := fmt.Sprintf("  用户 %s   已选 %s   启用 %s   限速 %s   IP规则 %s   配置 %s",
		tuiTitleStyle.Render(strconv.Itoa(len(m.state.Users))), tuiAccentStyle().Render(strconv.Itoa(selectedCount)), tuiGoodStyle.Render(strconv.Itoa(enabled)),
		tuiWarnStyle.Render(strconv.Itoa(limited)), tuiWarnStyle.Render(strconv.Itoa(ipLimited)), pending)
	if m.width >= 100 {
		summary = fmt.Sprintf("  用户 %s   已选 %s   启用 %s   限速 %s   IP规则 %s   处罚 %s/%s   停用 %s   告警 %s   旧节点 %s   配置 %s",
			tuiTitleStyle.Render(strconv.Itoa(len(m.state.Users))), tuiAccentStyle().Render(strconv.Itoa(selectedCount)), tuiGoodStyle.Render(strconv.Itoa(enabled)),
			tuiWarnStyle.Render(strconv.Itoa(limited)), tuiWarnStyle.Render(strconv.Itoa(ipLimited)), tuiWarnStyle.Render("软"+strconv.Itoa(softBlocked)), tuiBadStyle.Render("硬"+strconv.Itoa(hardBlocked)),
			tuiBadStyle.Render(strconv.Itoa(stopped)), tuiBadStyle.Render(strconv.Itoa(unreadAlertCount(m.state))), tuiDimStyle.Render(strconv.Itoa(len(m.state.ReservedAuthUsers))), pending)
	}

	search := "  搜索：" + tuiDimStyle.Render("按 / 输入用户名")
	if m.searching || m.filter != "" {
		cursor := ""
		if m.searching {
			cursor = "▌"
		}
		search = "  搜索：" + tuiAccentStyle().Render(m.filter+cursor)
	}

	lines := []string{m.renderHeader("用户列表"), "", summary, "", search, ""}
	if len(users) == 0 {
		empty := "尚未添加受管用户"
		hint := "按 a 创建用户；系统会自动生成默认 UUID，随后即可导出配置"
		if m.filter != "" {
			empty, hint = "没有匹配的用户", "按 esc 清除搜索"
		}
		box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(tuiPanel).Padding(2, 4).Width(max(40, m.width-12)).Align(lipgloss.Center).Render(tuiTitleStyle.Render(empty) + "\n\n" + tuiDimStyle.Render(hint))
		lines = append(lines, "  "+box)
	} else {
		lines = append(lines, m.renderUserTable(users)...)
	}
	lines = append(lines, "", m.renderStatus(), m.footer("↑↓ 选择", "enter 详情", "space 勾选", "a 新建", "B 批量管理", "L 线路/服务器", "M 运维中心", "/ 搜索", "p 应用", "q 退出"))
	return strings.Join(lines, "\n")
}

func (m tuiModel) renderManage() string {
	return m.renderMenuPage(
		"运维中心",
		"服务器与系统管理",
		"高频用户操作留在用户页；线路、告警、订阅、备份和审计集中在这里。",
		manageMenuEntries(),
		m.footer("↑↓ 选择", "enter 打开", "Home/End 首尾", "esc 返回用户"),
	)
}

func (m tuiModel) renderUserMenu() string {
	u := findUser(m.state, m.selected)
	if u == nil {
		return m.renderList()
	}
	return m.renderMenuPage(
		"用户操作",
		u.Name+" · 全部管理操作",
		"危险操作集中在这里并要求明确确认；原有快捷键仍然可用。",
		userMenuEntries(),
		m.footer("↑↓ 选择", "enter 打开", "Home/End 首尾", "esc 返回详情"),
	)
}

func (m tuiModel) renderProxyMenu() string {
	entries := managedProxyMenuEntries()
	kind := m.proxyKind.displayName()
	description := "常用修改使用引导表单；任意 sing-box 协议和字段使用完整 JSON。"
	if m.proxyKind == ManagedProxyEndpoint {
		description = "端点仅管理底层网络接口；当前不能直接分配给用户节点，也不提供独立限速。"
	}
	content := []string{
		"",
		"  " + tuiTitleStyle.Render(m.proxyTag+" · "+kind+"操作"),
		"  " + tuiDimStyle.Render(singleLine(description, max(20, m.width-4))),
		"",
	}
	selectedStart, selectedEnd := -1, -1
	for index, entry := range entries {
		row := fmt.Sprintf("  %d. %s", index+1, entry.title)
		if index == m.proxyMenuCursor {
			selectedStart = len(content)
			row = tuiSelectionStyle(max(28, m.width-4)).Render(fmt.Sprintf("  › %d. %s", index+1, entry.title))
			content = append(content, row, "     "+tuiDimStyle.Render(singleLine(entry.description, max(20, m.width-7))))
			selectedEnd = len(content)
			continue
		}
		content = append(content, row)
	}
	return m.renderDetailViewport("线路对象操作", content, selectedStart, selectedEnd, m.footer("↑↓ 选择", "enter 打开", "Home/End 首尾", "esc 返回线路"))
}

func (m tuiModel) renderProxyReview() string {
	operation := "修改"
	if m.proxyOperation == "add" {
		operation = "新增"
	}
	description := "这里只显示安全摘要；JSON 字段值和凭据不会出现在 CUI、状态或审计日志中。"
	if m.proxyKind == ManagedProxyEndpoint {
		description = "安全摘要；端点仅管理底层接口，当前不能直接分配给用户节点或设置独立限速。"
	}
	content := []string{
		"",
		"  " + tuiTitleStyle.Render("完整 JSON · 脱敏预览"),
		"  " + tuiDimStyle.Render(singleLine(description, max(20, m.width-4))),
		"",
	}
	if m.proxyIdentity.Tag == "" {
		content = append(content,
			"  "+tuiWarnStyle.Render("JSON 尚未通过基础检查"),
			"  "+tuiDimStyle.Render("按 e 重新编辑，或按 i 从文件安全导入。"),
		)
	} else {
		users, nodes := managedProxyImpact(m.state, m.proxyIdentity.Tag)
		if m.proxyOperation == "add" {
			users, nodes = 0, 0
		}
		content = append(content,
			"  操作    "+operation+m.proxyKind.displayName(),
			"  tag     "+m.proxyIdentity.Tag,
			"  协议    "+m.proxyIdentity.Type,
			"  目标    "+dash(m.proxyAddress),
			fmt.Sprintf("  影响    %d 个用户 / %d 个节点", users, nodes),
			"",
			"  "+tuiGoodStyle.Render("✓ JSON 对象与 tag/type 基础检查通过"),
		)
	}
	footer := m.footer("enter 确认并完整校验", "e 外部编辑器", "i 从文件导入", "esc 丢弃草稿")
	return m.renderDetailViewport("完整 JSON 预览", content, -1, -1, footer)
}

func (m tuiModel) renderMenuPage(section, title, description string, entries []tuiMenuEntry, footer string) string {
	content := []string{"", "  " + tuiTitleStyle.Render(title), "  " + tuiDimStyle.Render(description), ""}
	selectedStart, selectedEnd := -1, -1
	for index, entry := range entries {
		row := fmt.Sprintf("  %2d. %-22s  %s", index+1, entry.title, tuiDimStyle.Render(entry.description))
		if index == m.menuCursor {
			selectedStart = len(content)
			row = tuiSelectionStyle(max(28, m.width-4)).Render(fmt.Sprintf("  › %2d. %s", index+1, entry.title))
			content = append(content, row, "      "+tuiDimStyle.Render(entry.description))
			selectedEnd = len(content)
			continue
		}
		content = append(content, row)
	}
	return m.renderDetailViewport(section, content, selectedStart, selectedEnd, footer)
}

func (m tuiModel) renderBatch() string {
	names := m.batchSelectedNames()
	nameText := strings.Join(names, "、")
	if nameText == "" {
		nameText = "无"
	}
	maxWidth := max(36, m.width-10)
	nameText = singleLine(nameText, max(20, maxWidth-6))
	selected := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(tuiPanel).Padding(1, 2).Width(maxWidth).Render(
		tuiTitleStyle.Render(fmt.Sprintf("已选择 %d 个用户", len(names))) + "\n" + tuiDimStyle.Render(nameText))
	lines := []string{
		m.renderHeader("批量管理"), "", "  " + selected, "",
		"  " + tuiAccentStyle().Bold(true).Render("1 / e  用户设置") + "  " + tuiDimStyle.Render("状态、配额、到期、账期、阶梯限速"),
		"  " + tuiAccentStyle().Bold(true).Render("2 / l  节点限速") + "  " + tuiDimStyle.Render("按线路统一上传/下载 Mbps"),
		"  " + tuiAccentStyle().Bold(true).Render("3 / b  异常保护") + "  " + tuiDimStyle.Render("滑动窗口、软封限速或硬封断连"),
		"  " + tuiAccentStyle().Bold(true).Render("4 / i  来源 IP") + "  " + tuiDimStyle.Render("开关、模式、绑定和临时 IP"),
		"  " + tuiAccentStyle().Bold(true).Render("5 / f  访问规则") + "  " + tuiDimStyle.Render("域名、端口、并发和超限动作"),
		"", m.renderStatus(), m.footer("1-5/e/l/b/i/f 编辑同类参数", "p 应用待处理配置", "c 清空选择", "esc 返回列表"),
	}
	return strings.Join(lines, "\n")
}

func (m tuiModel) renderUserTable(users []User) []string {
	compact := m.width < 96
	header := "      " + cell("用户", 20) + cell("状态", 10) + cell("计费用量 / 配额", 23)
	if !compact {
		header += cell("上传 / 下载 Mbps", 20) + cell("到期", 14)
	}
	header += "节点"
	lines := []string{tuiDimStyle.Render(header)}
	pageSize := max(3, m.height-13)
	start := min(m.offset, max(0, len(users)-1))
	end := min(len(users), start+pageSize)
	for i := start; i < end; i++ {
		u := users[i]
		status := userStatus(u)
		check := "[ ]"
		if m.checkedUsers[strings.ToLower(u.Name)] {
			check = "[✓]"
		}
		row := " " + check + " " + cell(u.Name, 20) + cell(status, 10) + cell(formatSize(measuredUsage(u))+" / "+formatQuotaUI(userQuota(u)), 23)
		if !compact {
			row += cell(userRateSummary(u), 20) + cell(dash(u.Expires), 14)
		}
		row += strconv.Itoa(len(u.Nodes))
		if i == m.cursor {
			row = tuiSelectionStyle(max(20, m.width-2)).Render(row)
		} else if !u.Enabled || status == "已到期" || status == "配额用尽" {
			row = tuiDimStyle.Render(row)
		}
		lines = append(lines, row)
	}
	if len(users) > pageSize {
		lines = append(lines, tuiDimStyle.Render(fmt.Sprintf("  %d–%d / %d", start+1, end, len(users))))
	}
	return lines
}

func (m tuiModel) renderDetail() string {
	normalizeDeviceModel(m.state)
	u := findUser(m.state, m.selected)
	if u == nil {
		return m.renderList()
	}
	status := userStatus(*u)
	statusStyle := tuiGoodStyle
	if status != "已启用" {
		statusStyle = tuiWarnStyle
	}
	now := time.Now()
	usage := formatSize(measuredUsage(*u)) + " / " + formatQuotaUI(userQuota(*u)) + "（" + quotaModeText(u.QuotaMode) + "）"
	detailWidth := max(24, m.width-2)
	unreadAlerts, lastAlert := userAlertAudit(m.state, u.Name)
	lastAlertText := "无"
	if lastAlert != nil {
		lastAlertText = formatDisplayTime(lastAlert.At) + " · " + lastAlert.Message
	}
	pendingText := "已同步"
	if m.pendingApply || configurationPending(m.state) || runtimeApplyPending(m.state) {
		pendingText = "待应用"
	}
	punishment := "无"
	if burstBlocked(*u, now) {
		kind := "硬封断连"
		if burstSoftBlocked(*u, now) {
			kind = "软封限速"
		}
		punishment = kind + " 至 " + formatDisplayTime(u.BlockedUntil)
	} else if !u.Enabled {
		punishment = userStatus(*u)
	}
	deviceIPRules := enabledDeviceIPRuleCount(*u)
	ipLayers := fmt.Sprintf("用户层 + %d 个设备层（取更严）", deviceIPRules)
	if !u.IPPolicy.Enabled {
		ipLayers = fmt.Sprintf("用户层关闭；%d 个设备层仍独立生效", deviceIPRules)
	}
	content := []string{
		"",
		"  " + tuiTitleStyle.Render(u.Name) + "   " + statusStyle.Render(status),
		"  " + tuiAccentStyle().Render("运行审计") + "   " + tuiDimStyle.Render("状态文件约每 3 秒只读刷新"),
		singleLine("  "+userTrafficAccountingAudit(*u), detailWidth),
		singleLine(fmt.Sprintf("  实时 ↑ %s / ↓ %s Mbps   活跃连接 %d   未读告警 %d", formatCurrentMbps(u.CurrentUploadMbps), formatCurrentMbps(u.CurrentDownloadMbps), len(activeConnectionsForUser(m.state, u.Name)), unreadAlerts), detailWidth),
		singleLine(fmt.Sprintf("  配额 %s   账期 %s   到期 %s", usage, billingSummary(*u), dash(u.Expires)), detailWidth),
		"  " + quotaProgressBar(*u, min(44, max(20, m.width-18))),
		singleLine(fmt.Sprintf("  处罚 %s   配置 %s", punishment, pendingText), detailWidth),
		singleLine("  有效 IP "+ipPolicySummaryText(u.IPPolicy, len(u.SourceIPs), now), detailWidth),
		singleLine("  IP 叠加 "+ipLayers, detailWidth),
		singleLine("  最后告警 "+lastAlertText, detailWidth),
		singleLine(fmt.Sprintf("  阶梯限速 %s   异常保护 %s", throttleSummary(*u), burstSummary(*u, now)), detailWidth),
		singleLine(fmt.Sprintf("  访问策略 %s", accessPolicySummary(u.Access)), detailWidth),
		singleLine(fmt.Sprintf("  设备 %d 台（%d 台启用）", len(u.Devices), len(enabledDeviceNames(*u))), detailWidth),
	}
	if m.width >= 92 {
		content = append(content, "  "+usageSparkline(*u, min(48, max(20, m.width-24))))
	}
	content = append(content, "", tuiDimStyle.Render(fmt.Sprintf("  节点（%d 个，↑↓ 选择；当前节点展开技术信息）", len(u.Nodes))))
	if len(u.Nodes) > 0 {
		if m.width >= 88 {
			content = append(content, tuiDimStyle.Render("     "+cell("线路", 22)+cell("设备", 16)+cell("节点用量", 16)+cell("当前 Mbps", 18)+"当前限速 Mbps"))
		} else {
			content = append(content, tuiDimStyle.Render("     "+cell("线路", 22)+cell("节点用量", 16)+"当前限速 Mbps"))
		}
	}
	selectedStart, selectedEnd := -1, -1
	for i, n := range u.Nodes {
		baseUp, baseDown, _ := baseNodeRate(*u, n)
		upMbps, downMbps, mark := effectiveNodeRate(*u, n)
		usageText := formatSize(n.Upload + n.Download)
		limitText := "↑" + formatMbpsUI(upMbps) + " / ↓" + formatMbpsUI(downMbps)
		var row string
		if m.width >= 88 {
			currentText := "↑" + formatCurrentMbps(n.CurrentUploadMbps) + " / ↓" + formatCurrentMbps(n.CurrentDownloadMbps)
			row = fmt.Sprintf("  %d. %s%s%s%s%s", i+1, cell(n.Name, 22), cell(n.Device, 16), cell(usageText, 16), cell(currentText, 18), limitText)
		} else {
			row = fmt.Sprintf("  %d. %s%s%s", i+1, cell(n.Name, 22), cell(usageText, 16), limitText)
		}
		if i == m.nodeCursor {
			selectedStart = len(content)
			row = tuiSelectionStyle(max(28, m.width-4)).Render(singleLine("  ›"+strings.TrimPrefix(row, "  "), max(24, m.width-5)))
		}
		content = append(content, row)
		if i == m.nodeCursor {
			content = append(content,
				tuiDimStyle.Render(singleLine(fmt.Sprintf("     出站 %s · UUID %s", dash(n.Outbound), n.UUID), max(24, m.width-6))),
				tuiDimStyle.Render(singleLine(fmt.Sprintf("     auth_user %s · mark %s · 更新 %s", n.AuthUser, rateMarkText(mark), formatDisplayTime(n.RateUpdatedAt)), max(24, m.width-6))))
			if (throttleStage(*u) > 0 || burstSoftBlocked(*u, time.Now())) && (baseUp != upMbps || baseDown != downMbps) {
				content = append(content, tuiDimStyle.Render(fmt.Sprintf("     基础限速 ↑ %s / ↓ %s Mbps", formatMbpsUI(baseUp), formatMbpsUI(baseDown))))
			}
			destinations := topDestinations(n, 5)
			if len(destinations) == 0 {
				content = append(content, tuiDimStyle.Render("     访问排行（按次数） 暂无统计"))
			} else {
				content = append(content, tuiDimStyle.Render(singleLine("     访问排行（按次数） "+strings.Join(destinations, " · "), max(24, m.width-6))))
			}
			selectedEnd = len(content)
		}
	}
	if len(u.Nodes) == 0 {
		content = append(content, tuiDimStyle.Render("  暂无节点，按 n 为设备分配节点"))
	}
	section := "用户详情"
	if len(u.Nodes) > 0 {
		section = fmt.Sprintf("用户详情 · 节点 %d/%d", min(m.nodeCursor+1, len(u.Nodes)), len(u.Nodes))
	}
	footer := m.footer("↑↓ 切换节点", "PgUp/PgDn 审计/节点", "e 用户设置", "m 设备", "n 分配节点", "l 节点限速", "x 导出", "R 重置本月流量", "D 删除用户", "? 全部操作", "esc 返回")
	return m.renderDetailViewportAtOffset(section, content, selectedStart, selectedEnd, footer, m.detailOffset)
}

func (m tuiModel) renderDetailViewport(section string, content []string, selectedStart, selectedEnd int, footer string) string {
	return m.renderDetailViewportAtOffset(section, content, selectedStart, selectedEnd, footer, -1)
}

func (m tuiModel) renderDetailViewportAtOffset(section string, content []string, selectedStart, selectedEnd int, footer string, requestedOffset int) string {
	top := []string{m.renderHeader(section)}
	tail := []string{}
	if status := m.renderStatus(); status != "" {
		tail = append(tail, status)
	}
	if footer != "" {
		tail = append(tail, strings.Split(footer, "\n")...)
	}
	available := max(1, m.height-len(top)-len(tail))
	offset := min(max(0, requestedOffset), max(0, len(content)-available))
	if requestedOffset < 0 && len(content) > available && selectedStart >= 0 && selectedEnd > selectedStart {
		selectedHeight := selectedEnd - selectedStart
		if selectedHeight <= available {
			offset = max(0, selectedEnd-available)
			offset = min(offset, selectedStart)
		} else {
			offset = selectedStart
		}
	}
	offset = min(offset, max(0, len(content)-available))
	end := min(len(content), offset+available)
	lines := append([]string{}, top...)
	lines = append(lines, content[offset:end]...)
	lines = append(lines, tail...)
	return strings.Join(lines, "\n")
}

func (m tuiModel) renderDevices() string {
	u := findUser(m.state, m.selected)
	if u == nil {
		return m.renderList()
	}
	lines := []string{m.renderHeader("设备管理"), "", "  " + tuiTitleStyle.Render(u.Name+" · 设备与独立凭据"), "", tuiDimStyle.Render("  每台设备拥有独立 UUID；禁用或轮换只影响这一台设备。"), ""}
	for i, device := range u.Devices {
		status := tuiGoodStyle.Render("已启用")
		if !device.Enabled {
			status = tuiBadStyle.Render("已禁用")
		}
		up, down := deviceTraffic(*u, device.Name)
		currentUp, currentDown := deviceCurrentRate(*u, device.Name)
		name := device.Name
		prefix := "  "
		if i == m.deviceCursor {
			prefix = "› "
			name = lipgloss.NewStyle().Foreground(tuiSelectedText).Background(tuiAccentBright).Bold(true).Padding(0, 1).Render(name)
		}
		lines = append(lines,
			fmt.Sprintf("  %s%d. %s   %s", prefix, i+1, name, status),
			fmt.Sprintf("     节点 %d   用量 ↑ %s / ↓ %s   实时 ↑ %s / ↓ %s Mbps   最后连接 %s", len(nodesForDevice(*u, device.Name)), formatSize(up), formatSize(down), formatCurrentMbps(currentUp), formatCurrentMbps(currentDown), formatDisplayTime(device.LastSeen)),
			fmt.Sprintf("     来源 IP   %s", deviceIPPolicySummary(*u, device, time.Now())))
		lines = append(lines, fmt.Sprintf("     访问策略  %s", accessPolicySummary(device.Access)))
		if i == m.deviceCursor {
			if sources := topDeviceSourceIPs(device, 4); len(sources) > 0 {
				lines = append(lines, tuiDimStyle.Render("     最近来源   "+strings.Join(sources, "  ·  ")))
			}
			for _, node := range nodesForDevice(*u, device.Name) {
				lines = append(lines, tuiDimStyle.Render(fmt.Sprintf("     · %s  %s  ↑%s/↓%s Mbps", node.Name, node.UUID, formatMbpsUI(node.UploadMbps), formatMbpsUI(node.DownloadMbps))))
			}
			if m.state.Subscription.Enabled {
				lines = append(lines, "     订阅链接   "+subscriptionURL(m.state, device))
			} else {
				lines = append(lines, tuiDimStyle.Render("     订阅链接   服务未开启（用户列表按 u 设置）"))
			}
		}
		lines = append(lines, "")
	}
	lines = append(lines, m.renderStatus(), m.footer("↑↓ 选择设备", "a 添加", "space 启停", "r 轮换UUID", "u 撤销订阅", "z 二维码", "i 设备IP", "f 访问规则", "x 导出", "D 删除", "esc 返回"))
	return strings.Join(lines, "\n")
}

type subscriptionTUIEntry struct {
	User   User
	Device Device
}

func subscriptionTUIEntries(state *State) []subscriptionTUIEntry {
	if state == nil {
		return nil
	}
	entries := []subscriptionTUIEntry{}
	for _, user := range state.Users {
		for _, device := range user.Devices {
			entries = append(entries, subscriptionTUIEntry{User: user, Device: device})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		left := strings.ToLower(entries[i].User.Name + "\x00" + entries[i].Device.Name)
		right := strings.ToLower(entries[j].User.Name + "\x00" + entries[j].Device.Name)
		return left < right
	})
	return entries
}

func subscriptionPublishedBase(settings SubscriptionSettings) string {
	settings = normalizedSubscriptionSettings(settings)
	if base := strings.TrimRight(settings.BaseURL, "/"); base != "" {
		return base
	}
	scheme := "http"
	if settings.TLSCertFile != "" && settings.TLSKeyFile != "" {
		scheme = "https"
	}
	return scheme + "://" + settings.Listen
}

func subscriptionCertificateRemaining(info subscriptionCertificateInfo, now time.Time) string {
	if !info.Enabled {
		return "-"
	}
	remaining := info.NotAfter.Sub(now)
	if remaining <= 0 {
		return "已过期"
	}
	if remaining >= 24*time.Hour {
		return fmt.Sprintf("%d 天", int(remaining/(24*time.Hour)))
	}
	if remaining >= time.Hour {
		return fmt.Sprintf("%d 小时", int(remaining/time.Hour))
	}
	return fmt.Sprintf("%d 分钟", max(1, int(remaining/time.Minute)))
}

func subscriptionTLSAuditLines(settings SubscriptionSettings, now time.Time, width int) []string {
	info, err := subscriptionTLSInfo(settings)
	if err != nil {
		return []string{tuiBadStyle.Render(singleLine("  原生 HTTPS 配置异常 · "+err.Error(), width))}
	}
	if !info.Enabled {
		return []string{tuiDimStyle.Render(singleLine("  原生 HTTPS 未启用 · 回环 HTTP 可用；公网监听必须配置证书和私钥", width))}
	}
	status := info.status(now)
	statusStyle := tuiGoodStyle
	if status != "有效" {
		statusStyle = tuiWarnStyle
	}
	sans := append(append([]string(nil), info.DNSNames...), info.IPAddresses...)
	if len(sans) == 0 {
		sans = []string{"未声明 SAN"}
	}
	statusLine := singleLine(fmt.Sprintf("  原生 HTTPS %s   证书到期 %s   剩余 %s", status, info.NotAfter.In(applicationLocation()).Format("2006-01-02 15:04"), subscriptionCertificateRemaining(info, now)), width)
	return []string{
		statusStyle.Render(statusLine),
		tuiDimStyle.Render(singleLine("  证书 SAN "+strings.Join(sans, "、")+"   私钥 已配置（内容永不显示）", width)),
	}
}

func (m tuiModel) renderSubscriptions() string {
	settings := normalizedSubscriptionSettings(m.state.Subscription)
	status := tuiBadStyle.Render("已关闭")
	if settings.Enabled {
		status = tuiGoodStyle.Render("已开启")
	}
	contentWidth := max(24, m.width-2)
	content := []string{
		"",
		"  " + tuiTitleStyle.Render("每设备订阅 · 选择后查看二维码或撤销旧链接"),
		"",
		singleLine(fmt.Sprintf("  服务 %s   监听 %s", status, settings.Listen), contentWidth),
		singleLine("  发布地址 "+subscriptionPublishedBase(settings), contentWidth),
	}
	content = append(content, subscriptionTLSAuditLines(settings, time.Now(), contentWidth)...)
	content = append(content,
		tuiDimStyle.Render(singleLine("  可直接启用原生 HTTPS；也可保持回环 HTTP 并由同机 HTTPS 反向代理转发。", contentWidth)),
		"",
		tuiDimStyle.Render("  设备订阅（配额和到期继承用户）"),
	)
	entries := subscriptionTUIEntries(m.state)
	cursor := 0
	if len(entries) > 0 {
		cursor = min(max(0, m.subscriptionCursor), len(entries)-1)
	}
	selectedStart, selectedEnd := -1, -1
	now := time.Now()
	for index, entry := range entries {
		availability := "可订阅"
		availabilityReason := ""
		if err := subscriptionDeviceAvailable(entry.User, entry.Device, now); err != nil {
			availability = "不可订阅"
			availabilityReason = err.Error()
		}
		up, down := deviceTraffic(entry.User, entry.Device.Name)
		deviceUsage := formatSize(max(int64(0), up) + max(int64(0), down))
		name := entry.User.Name + " / " + entry.Device.Name
		mainLine := "  " + name + "   " + availability
		if m.width >= 92 {
			mainLine += fmt.Sprintf("   设备流量 %s   用户计费 %s/%s（%s）   到期 %s", deviceUsage, formatSize(measuredUsage(entry.User)), formatQuotaUI(userQuota(entry.User)), quotaModeText(entry.User.QuotaMode), dash(entry.User.Expires))
		}
		mainLine = singleLine(mainLine, max(24, m.width-5))
		if index == cursor {
			selectedStart = len(content)
			mainLine = tuiSelectionStyle(max(28, m.width-4)).Render(singleLine("  › "+strings.TrimSpace(mainLine), max(24, m.width-5)))
		}
		content = append(content, mainLine)
		if m.width < 92 {
			content = append(content, tuiDimStyle.Render(singleLine(fmt.Sprintf("     设备流量 %s · 用户计费 %s/%s（%s） · 到期 %s", deviceUsage, formatSize(measuredUsage(entry.User)), formatQuotaUI(userQuota(entry.User)), quotaModeText(entry.User.QuotaMode), dash(entry.User.Expires)), max(20, m.width-5))))
		}
		if availabilityReason != "" {
			content = append(content, tuiBadStyle.Render(singleLine("     原因 "+availabilityReason, max(20, m.width-5))))
		}
		link := "服务关闭 · 开启后使用 " + subscriptionURL(m.state, entry.Device)
		if settings.Enabled {
			link = subscriptionURL(m.state, entry.Device)
		}
		content = append(content, tuiDimStyle.Render("     URL "+singleLine(link, max(14, m.width-10))))
		if index == cursor {
			selectedEnd = len(content)
		}
	}
	if len(entries) == 0 {
		content = append(content, tuiDimStyle.Render("  暂无用户设备"))
	}
	footer := m.footer("↑↓ 选择", "Home/End 首尾", "enter/z 二维码", "r/u 撤销旧链接", "e HTTPS/服务设置", "esc 返回")
	return m.renderDetailViewport("订阅交付", content, selectedStart, selectedEnd, footer)
}

func (m tuiModel) renderAudit() string {
	records, err := readAuditRecords(m.a.statePath, max(10, m.height-9))
	lines := []string{m.renderHeader("操作审计"), "", "  " + tuiTitleStyle.Render("最近成功的管理操作"), "", tuiDimStyle.Render("  密钥、指定 UUID 和 token 参数会自动脱敏；后台每分钟统计不会写入此日志。"), ""}
	if err != nil {
		lines = append(lines, tuiBadStyle.Render("  读取审计日志失败: "+err.Error()))
	} else if len(records) == 0 {
		lines = append(lines, tuiDimStyle.Render("  暂无审计记录"))
	} else {
		lines = append(lines, tuiDimStyle.Render("  "+cell("时间", 28)+cell("操作者", 14)+cell("操作", 25)+"参数"))
		for _, record := range records {
			arguments := strings.Join(record.Args, " ")
			lines = append(lines, "  "+cell(formatDisplayTime(record.At), 28)+cell(record.Actor, 14)+cell(record.Action, 25)+singleLine(arguments, max(20, m.width-69)))
		}
	}
	lines = append(lines, "", m.footer("esc 返回"))
	return strings.Join(lines, "\n")
}

func (m tuiModel) renderFleet() string {
	servers := append([]FleetServer(nil), m.state.Fleet...)
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	lines := []string{m.renderHeader("远端管理节点"), "", "  " + tuiTitleStyle.Render("其他 sbmgr 服务器 · 只读汇总"), "", tuiDimStyle.Render("  这里不是落地出口：SSH 使用 BatchMode 且严格校验 known_hosts，只读取远端快照，不修改用户或线路。"), ""}
	if len(servers) == 0 {
		lines = append(lines, tuiDimStyle.Render("  暂无远端服务器，按 a 添加"))
	} else {
		lines = append(lines, tuiDimStyle.Render("  "+cell("服务器", 16)+cell("状态", 10)+cell("版本", 12)+cell("用户/设备", 16)+cell("累计流量", 16)+cell("告警", 8)+"最近检查"))
		for index, server := range servers {
			status := m.state.FleetStatus[server.Name]
			label := tuiDimStyle.Render("未知")
			if status.Online {
				label = tuiGoodStyle.Render("在线")
			} else if status.CheckedAt != "" {
				label = tuiBadStyle.Render("离线")
			}
			row := "  " + cell(server.Name, 16) + cell(label, 10) + cell(dash(status.Snapshot.Version), 12) + cell(fmt.Sprintf("%d / %d", status.Snapshot.Users, status.Snapshot.Devices), 16) + cell(formatSize(status.Snapshot.UploadBytes+status.Snapshot.DownloadBytes), 16) + cell(strconv.Itoa(status.Snapshot.UnreadAlerts), 8) + formatDisplayTime(status.CheckedAt)
			if index == m.fleetCursor {
				row = tuiSelectionStyle(max(20, m.width-2)).Render(row)
			}
			lines = append(lines, row)
			if index == m.fleetCursor {
				lines = append(lines, tuiDimStyle.Render(fmt.Sprintf("     %s@%s:%d · %s", normalizedFleetServer(server).User, server.Host, normalizedFleetServer(server).Port, normalizedFleetServer(server).AppDir)))
				if status.Error != "" {
					lines = append(lines, tuiBadStyle.Render("     "+singleLine(status.Error, max(24, m.width-8))))
				}
			}
		}
	}
	lines = append(lines, "", m.renderStatus(), m.footer("↑↓ 选择", "r 刷新", "a 添加", "D 移除", "esc 返回"))
	return strings.Join(lines, "\n")
}

func (m tuiModel) renderQRCode() string {
	userName := m.qrUser
	if userName == "" {
		userName = m.selected
	}
	u := findUser(m.state, userName)
	if u == nil || len(u.Devices) == 0 {
		return m.renderList()
	}
	var device *Device
	if m.qrDevice != "" {
		device = findDevice(u, m.qrDevice)
	}
	if device == nil {
		index := min(m.deviceCursor, len(u.Devices)-1)
		device = &u.Devices[index]
	}
	settings := normalizedSubscriptionSettings(m.state.Subscription)
	link := subscriptionURL(m.state, *device)
	availability := "当前可订阅"
	availabilityStyle := tuiGoodStyle
	if err := subscriptionDeviceAvailable(*u, *device, time.Now()); err != nil {
		availability = "当前不可订阅：" + err.Error()
		availabilityStyle = tuiBadStyle
	} else if !settings.Enabled {
		availability = "设备规则允许，但订阅服务已关闭"
		availabilityStyle = tuiWarnStyle
	}
	availabilityLine := availabilityStyle.Render(singleLine("  "+availability, max(20, m.width-2)))
	footer := m.footer("esc 返回")
	footerLines := strings.Split(footer, "\n")
	linkLine := "  URL " + singleLine(link, max(12, m.width-8))
	qr := subscriptionQRText(link)
	qrLines := []string{}
	if qr != "" {
		for _, line := range strings.Split(qr, "\n") {
			qrLines = append(qrLines, "  "+line)
		}
	}
	full := []string{
		m.renderHeader("订阅二维码"),
		"",
		"  " + tuiTitleStyle.Render(singleLine(u.Name+" / "+device.Name, max(16, m.width-4))),
		availabilityLine,
		linkLine,
		"",
	}
	full = append(full, qrLines...)
	full = append(full, "")
	full = append(full, footerLines...)
	if len(qrLines) > 0 && tuiLinesFit(full, m.width, m.height) {
		return strings.Join(full, "\n")
	}

	requiredWidth := 0
	for _, line := range qrLines {
		requiredWidth = max(requiredWidth, lipgloss.Width(line))
	}
	requiredWidth = max(requiredWidth, 64)
	requiredHeight := len(full)
	available := max(1, m.height-len(footerLines))
	wrappedLink := wrapTUIText(link, max(12, m.width-6))
	// The URL is the useful fallback, so reserve its complete wrapped form
	// before adding explanatory lines. This avoids presenting a truncated link
	// with an instruction that tells the operator to copy it.
	compact := []string{m.renderHeader("订阅二维码")}
	optionalTop := []string{
		"",
		"  " + tuiTitleStyle.Render(singleLine(u.Name+" / "+device.Name, max(16, m.width-4))),
		availabilityLine,
	}
	linkBlock := []string{"  URL"}
	for _, line := range wrappedLink {
		linkBlock = append(linkBlock, "    "+line)
	}
	advice := []string{
		"",
		tuiWarnStyle.Render("  当前终端不足以完整显示二维码"),
		tuiDimStyle.Render(singleLine(fmt.Sprintf("  完整二维码至少需要 %d×%d 的终端", requiredWidth, requiredHeight), max(20, m.width-2))),
		tuiDimStyle.Render(singleLine("  复制上方订阅 URL 也可更新配置。", max(20, m.width-2))),
	}
	reserved := len(compact) + len(linkBlock)
	for _, line := range optionalTop {
		if reserved+1 > available {
			break
		}
		compact = append(compact, line)
		reserved++
	}
	compact = append(compact, linkBlock...)
	for _, line := range advice {
		if len(compact)+1 > available {
			break
		}
		compact = append(compact, line)
	}
	compact = append(compact, footerLines...)
	return strings.Join(compact, "\n")
}

func tuiLinesFit(lines []string, width, height int) bool {
	if len(lines) > height {
		return false
	}
	for _, line := range lines {
		if lipgloss.Width(line) > width {
			return false
		}
	}
	return true
}

func (m tuiModel) renderConnections() string {
	connections := activeConnectionsForUser(m.state, m.selected)
	lines := []string{m.renderHeader("当前连接"), "", "  " + tuiTitleStyle.Render(m.selected+" · 活跃连接"), "", tuiDimStyle.Render("  数据来自 sing-box journal；已关闭连接会自动移除，静默超过 5 分钟的记录会过期。"), ""}
	if len(connections) == 0 {
		lines = append(lines, tuiDimStyle.Render("  暂无已识别的活跃连接"))
	} else {
		lines = append(lines, tuiDimStyle.Render("  "+cell("设备 / 节点", 27)+cell("来源 IP", 20)+cell("目标", 34)+"最后活动"))
		limit := max(3, m.height-11)
		start := 0
		if m.connectionCursor >= limit {
			start = m.connectionCursor - limit + 1
		}
		end := min(len(connections), start+limit)
		for i := start; i < end; i++ {
			connection := connections[i]
			row := "  " + cell(connection.Device+" / "+connection.Node, 27) + cell(dash(connection.SourceIP), 20) + cell(dash(connection.Target), 34) + formatDisplayTime(connection.LastSeen)
			if i == m.connectionCursor {
				row = tuiSelectionStyle(max(20, m.width-2)).Render(row)
			}
			lines = append(lines, row)
		}
	}
	lines = append(lines, "", m.renderStatus(), m.footer("↑↓ 选择", "r 刷新", "esc 返回"))
	return strings.Join(lines, "\n")
}

func (m tuiModel) renderAccessHistory() string {
	u := findUser(m.state, m.selected)
	if u == nil {
		return m.renderList()
	}
	accesses := recentAccessesForUser(u, m.accessFilter)
	search := "  搜索：" + tuiDimStyle.Render("按 / 输入网站、设备或节点")
	if m.accessSearching || m.accessFilter != "" {
		cursor := ""
		if m.accessSearching {
			cursor = "▌"
		}
		search = "  搜索：" + tuiAccentStyle().Render(m.accessFilter+cursor)
	}
	lines := []string{
		m.renderHeader("近期访问"), "",
		"  " + tuiTitleStyle.Render(u.Name+" · 近期访问网站"),
		tuiDimStyle.Render(fmt.Sprintf("  最近 7 天 · 最多 %d 条 · 当前匹配 %d 条", recentAccessLimit, len(accesses))),
		search, "",
	}
	if len(accesses) == 0 {
		empty := "暂无近期访问记录"
		if m.accessFilter != "" {
			empty = "没有匹配的近期访问记录"
		}
		lines = append(lines, tuiDimStyle.Render("  "+empty))
	} else {
		compact := m.width < 100
		if compact {
			targetWidth := max(16, m.width-48)
			lines = append(lines, tuiDimStyle.Render("  "+cell("最后访问", 20)+cell("网站 / IP", targetWidth)+cell("设备 / 节点", 18)+"次数"))
		} else {
			targetWidth := max(24, m.width-64)
			lines = append(lines, tuiDimStyle.Render("  "+cell("最后访问", 20)+cell("网站 / IP", targetWidth)+cell("设备", 16)+cell("节点", 16)+"次数"))
		}
		pageSize := max(3, m.height-11)
		start := min(m.accessOffset, max(0, len(accesses)-1))
		end := min(len(accesses), start+pageSize)
		for index := start; index < end; index++ {
			item := accesses[index]
			var row string
			if compact {
				targetWidth := max(16, m.width-48)
				row = "  " + cell(formatDisplayTime(item.LastSeen), 20) + cell(item.Target, targetWidth) + cell(dash(item.Device)+" / "+dash(item.Node), 18) + strconv.FormatInt(item.Count, 10)
			} else {
				targetWidth := max(24, m.width-64)
				row = "  " + cell(formatDisplayTime(item.LastSeen), 20) + cell(item.Target, targetWidth) + cell(dash(item.Device), 16) + cell(dash(item.Node), 16) + strconv.FormatInt(item.Count, 10)
			}
			if index == m.accessCursor {
				row = tuiSelectionStyle(max(20, m.width-2)).Render(row)
			}
			lines = append(lines, row)
		}
		if len(accesses) > pageSize {
			lines = append(lines, tuiDimStyle.Render(fmt.Sprintf("  %d–%d / %d", start+1, end, len(accesses))))
		}
	}
	lines = append(lines, m.renderStatus(), m.footer("↑↓ 选择", "Home/End 首尾", "/ 搜索筛选", "esc 清除/返回", "r 刷新"))
	return strings.Join(lines, "\n")
}

func (m tuiModel) renderAlerts() string {
	lines := []string{m.renderHeader("告警中心"), "", "  " + tuiTitleStyle.Render("异常流量告警"), ""}
	if len(m.state.Alerts) == 0 {
		lines = append(lines, tuiDimStyle.Render("  暂无告警"))
	} else {
		limit := max(3, m.height-10)
		start := max(0, len(m.state.Alerts)-limit)
		for i := len(m.state.Alerts) - 1; i >= start; i-- {
			alert := m.state.Alerts[i]
			marker := tuiBadStyle.Render("● 未读")
			if alert.Acknowledged {
				marker = tuiDimStyle.Render("○ 已读")
			}
			lines = append(lines, fmt.Sprintf("  %s  %s  %s", marker, formatDisplayTime(alert.At), alert.Message))
		}
	}
	lines = append(lines, "", m.renderStatus(), m.footer("c 全部标记已读", "esc 返回"))
	return strings.Join(lines, "\n")
}

func (m tuiModel) renderHealth() string {
	settings := normalizedHealthSettings(m.state.Health)
	mode := "自动探测"
	if settings.Mode == "off" {
		mode = "已关闭"
	}
	webhook := "未配置"
	if m.state.Notifications.WebhookURL != "" {
		webhook = "已配置"
	}
	content := []string{
		"",
		"  " + tuiTitleStyle.Render("线路与服务器"),
		tuiDimStyle.Render(singleLine("  可编辑地址与线路凭据；密码始终遮罩且不回显，变更会先备份、校验、安全重载，失败自动回滚。", max(20, m.width-2))),
		"",
		"  " + tuiAccentStyle().Render("中转入口") + "  " + tuiDimStyle.Render("用户设备连接到本机"),
	}
	selectedStart, selectedEnd := -1, -1
	clientAddress := endpointDisplayAddress(m.state.Client.Server, m.state.Client.Port)
	clientRow := "  1. " + cell("客户端连接地址", 22) + cell(clientAddress, max(12, m.width-30))
	if m.healthCursor == 0 {
		selectedStart = len(content)
		clientText := singleLine("  › 1. 中转入口  "+clientAddress, max(24, m.width-5))
		clientRow = tuiSelectionStyle(max(28, m.width-4)).Render(clientText)
	}
	content = append(content, clientRow)
	content = append(content, tuiDimStyle.Render(singleLine("     修改后：订阅立即更新；已经导出的 YAML 需要重新导出，UUID / Reality 密钥 / SNI 不变。", max(20, m.width-2))))
	if m.healthCursor == 0 {
		selectedEnd = len(content)
	}

	content = append(content, "", "  "+tuiAccentStyle().Render("出站与端点")+"  "+tuiDimStyle.Render(singleLine("完整 JSON 支持全部协议；端点仅管理底层接口，不能直接分配用户节点", max(20, m.width-16))))
	documents, documentErr := listManagedProxyDocumentsForTUI(m.state)
	if documentErr != nil {
		content = append(content, tuiBadStyle.Render("  读取 sing-box 对象失败："+singleLine(documentErr.Error(), max(24, m.width-22))))
	} else if len(documents) == 0 {
		content = append(content, tuiDimStyle.Render("  基础模板里没有出站或端点；按 a 新增。"))
	} else {
		for index, document := range documents {
			address := managedProxyDocumentAddress(document)
			statusText, statusStyle, details := "不探测", tuiDimStyle, ""
			if document.Kind == ManagedProxyEndpoint {
				statusText, statusStyle = "端点", tuiAccentStyle()
			} else if address != "-" {
				statusText, statusStyle, details = outboundHealthDisplay(m.state.OutboundHealth[document.Tag])
			}
			row := singleLine(fmt.Sprintf("  %d. %s · %s · %s · %s", index+2, document.Tag, document.Type, address, statusText), max(24, m.width-2))
			if m.width >= 100 {
				addressWidth := min(32, max(18, m.width-64))
				row = "  " + cell(strconv.Itoa(index+2)+".", 4) + cell(document.Tag, 20) + cell(document.Kind.displayName(), 8) + cell(document.Type, 16) + cell(address, addressWidth) + statusStyle.Render(statusText)
			}
			if m.healthCursor == index+1 {
				selectedStart = len(content)
				selectedText := singleLine(fmt.Sprintf("  › %d. %s  %s  %s", index+2, document.Tag, document.Type, address), max(24, m.width-5))
				row = tuiSelectionStyle(max(28, m.width-4)).Render(selectedText)
			}
			content = append(content, row)
			users, nodes := managedProxyImpact(m.state, document.Tag)
			meta := fmt.Sprintf("     %s · %s · 影响 %d 个用户 / %d 个节点", document.Kind.displayName(), statusText, users, nodes)
			if document.Kind == ManagedProxyEndpoint {
				meta = "     端点 · 底层接口 · 不可直接分配用户节点或设置独立限速"
			}
			if details != "" {
				meta += " · " + details
			}
			content = append(content, tuiDimStyle.Render(singleLine(meta, max(24, m.width-4))))
			if status := m.state.OutboundHealth[document.Tag]; document.Kind == ManagedProxyOutbound && status.Error != "" && m.healthCursor == index+1 {
				content = append(content, tuiBadStyle.Render("     "+singleLine(status.Error, max(24, m.width-8))))
			}
			if m.healthCursor == index+1 {
				selectedEnd = len(content)
			}
		}
	}
	content = append(content,
		"",
		tuiDimStyle.Render(singleLine(fmt.Sprintf("  健康探测：%s · 每 %d 分钟 · 超时 %d 秒 · 连续失败 %d 次告警 · Webhook %s", mode, settings.IntervalMinutes, settings.TimeoutSeconds, settings.AlertAfterFailures, webhook), max(20, m.width-2))),
		tuiDimStyle.Render("  最近探测："+formatDisplayTime(m.state.LastHealthCheck)),
	)
	footer := m.footer("↑↓ 选择", "enter 对象操作", "e 常用字段", "a 新增", "r 立即探测", "s 健康/通知", "esc 返回")
	return m.renderDetailViewport("线路与服务器", content, selectedStart, selectedEnd, footer)
}

func endpointDisplayAddress(server string, port int) string {
	if strings.TrimSpace(server) == "" || port <= 0 {
		return "-"
	}
	return net.JoinHostPort(server, strconv.Itoa(port))
}

func outboundHealthDisplay(status OutboundHealth) (string, lipgloss.Style, string) {
	if strings.TrimSpace(status.CheckedAt) == "" {
		return "未探测", tuiDimStyle, ""
	}
	if !status.Healthy {
		return "失败", tuiBadStyle, "连续失败 " + strconv.Itoa(status.Failures) + " 次"
	}
	return "正常", tuiGoodStyle, fmt.Sprintf("%d ms · %s", status.LatencyMS, formatDisplayTime(status.CheckedAt))
}

func (m tuiModel) renderBackups() string {
	backups, err := listStateBackups(m.a.statePath)
	lines := []string{
		m.renderHeader("状态备份与恢复"),
		"",
		"  " + tuiTitleStyle.Render("state.json 状态备份"),
		"  " + tuiDimStyle.Render(singleLine("只包含 state.json，不包含程序版本；恢复后使用当前基础模板重新生成运行配置。", max(20, m.width-4))),
		"",
	}
	if err != nil {
		lines = append(lines, tuiBadStyle.Render("  读取备份失败: "+err.Error()))
	} else if len(backups) == 0 {
		lines = append(lines, tuiDimStyle.Render("  暂无状态备份，按 c 立即创建"))
	} else {
		lines = append(lines, tuiDimStyle.Render("  "+cell("文件", 42)+cell("修改时间", 27)+"大小"))
		limit := max(3, m.height-11)
		start := 0
		if m.backupCursor >= limit {
			start = m.backupCursor - limit + 1
		}
		end := min(len(backups), start+limit)
		for i := start; i < end; i++ {
			backup := backups[i]
			row := "  " + cell(backup.Name, 42) + cell(backup.Modified.In(applicationLocation()).Format("2006-01-02 15:04:05"), 27) + formatSize(backup.Size)
			if i == m.backupCursor {
				row = tuiSelectionStyle(max(20, m.width-2)).Render(row)
			}
			lines = append(lines, row)
		}
		if len(backups) > end {
			lines = append(lines, tuiDimStyle.Render(fmt.Sprintf("  显示 %d–%d / %d 个", start+1, end, len(backups))))
		}
	}
	lines = append(lines, "", m.renderStatus(), m.footer("↑↓ 选择", "enter 恢复", "c 创建备份", "esc 返回"))
	return strings.Join(lines, "\n")
}

func (m tuiModel) renderForm() string {
	if m.formHelpExpanded && isIPPolicyForm(m.form.kind) {
		return m.renderIPPolicyFormHelp()
	}
	fieldCount := len(m.form.fields)
	footerItems := []string{"↑↓/tab 切换字段", "←→ 光标/选项", "Home/End 首尾", "Del/Backspace 删除", "enter 下一项/提交", "ctrl+u 清空"}
	if isIPPolicyForm(m.form.kind) {
		footerItems = append(footerItems, "? 全部规则说明")
	}
	footerItems = append(footerItems, "esc 取消")
	footer := m.footer(footerItems...)
	tailHeight := len(strings.Split(footer, "\n"))
	if m.renderStatus() != "" {
		tailHeight++
	}
	helpWidth := max(20, m.width-8)
	helpLines := m.formHelpLines(helpWidth)
	// Four fixed heading lines, an optional help heading, and one possible
	// scroll-indicator line. Keep the active field, status, and complete footer
	// visible even at the supported 64×18.
	maxHelpLines := max(0, m.height-8-tailHeight)
	if len(helpLines) > maxHelpLines {
		helpLines = helpLines[:maxHelpLines]
		if len(helpLines) > 0 {
			helpLines[len(helpLines)-1] = singleLine(helpLines[len(helpLines)-1]+"…", helpWidth)
		}
	}
	helpHeight := len(helpLines)
	if helpHeight > 0 {
		helpHeight++
	}
	visibleCount := max(1, (m.height-5-tailHeight-helpHeight)/2)
	visibleCount = min(visibleCount, fieldCount)
	start := max(0, m.form.active-visibleCount/2)
	if start+visibleCount > fieldCount {
		start = max(0, fieldCount-visibleCount)
	}
	end := min(fieldCount, start+visibleCount)
	position := ""
	if fieldCount > 0 {
		position = tuiDimStyle.Render(fmt.Sprintf("  字段 %d/%d", m.form.active+1, fieldCount))
	}
	lines := []string{m.renderHeader(m.form.title), "", "  " + tuiTitleStyle.Render(m.form.title) + position, ""}
	if len(helpLines) > 0 {
		lines = append(lines, "  "+tuiAccentStyle().Render("当前选项会怎样工作"))
		for _, line := range helpLines {
			lines = append(lines, "    "+tuiDimStyle.Render(line))
		}
	}
	valueWidth := max(22, min(64, m.width-26))
	for i := start; i < end; i++ {
		field := m.form.fields[i]
		displayField := field
		if field.secret && field.value != "" {
			if i == m.form.active {
				displayField.value = strings.Repeat("•", len([]rune(field.value)))
			} else {
				displayField.value = "••••••••"
			}
		}
		value := singleLine(displayField.value, valueWidth)
		if len(field.options) > 0 {
			value = "◀  " + singleLine(field.value, max(1, valueWidth-8)) + "  ▶"
		}
		if value == "" && i != m.form.active {
			value = singleLine(field.placeholder, valueWidth)
		}
		labelStyle, valueStyle := tuiDimStyle, lipgloss.NewStyle().Foreground(tuiText)
		cursor := " "
		if i == m.form.active {
			labelStyle = lipgloss.NewStyle().Foreground(tuiAccentBright).Bold(true)
			valueStyle = lipgloss.NewStyle().Foreground(tuiText).Background(tuiPanel).Bold(true)
			cursor = "›"
			if len(field.options) == 0 {
				value = displayField.valueWindow(valueWidth)
				if field.value == "" && field.placeholder != "" {
					value += singleLine(field.placeholder, max(1, valueWidth-1))
				}
			}
		}
		line := fmt.Sprintf("  %s %s  %s", cursor, cell(labelStyle.Render(field.label), 18), valueStyle.Width(valueWidth).MaxWidth(valueWidth).Render(value))
		lines = append(lines, line, "")
	}
	if fieldCount > visibleCount {
		lines = append(lines, tuiDimStyle.Render(fmt.Sprintf("  显示字段 %d–%d / %d，移动光标自动滚动", start+1, end, fieldCount)))
	}
	lines = append(lines, m.renderStatus(), footer)
	return strings.Join(lines, "\n")
}

func (m tuiModel) formHelpLines(width int) []string {
	var paragraphs []string
	if isIPPolicyForm(m.form.kind) {
		paragraphs = ipPolicyFormHelp(m.form)
	} else {
		switch m.form.kind {
		case formSubscriptionSettings:
			paragraphs = []string{
				"证书和私钥路径同时填写会启用原生 HTTPS；都留空则关闭。私钥内容不会显示或写入审计日志。",
				"公网监听必须使用 TLS；也可只监听回环地址，再由同机 HTTPS 反向代理转发。",
			}
		case formAddUser:
			paragraphs = []string{"配额计量决定配额、阶梯限速和订阅剩余流量采用双向合计、仅上传或仅下载；原始上下行始终分别保留。"}
		case formEditUser:
			paragraphs = []string{"修改配额计量会立即按新口径重新判断配额与阶梯限速，不会改写原始上传/下载计数。"}
		case formBatchUser:
			paragraphs = []string{"配额计量选择“保持不变”时每个用户沿用原口径；其他选择会统一重新判断配额与阶梯限速。"}
		}
	}
	lines := make([]string, 0, len(paragraphs)*2)
	for _, paragraph := range paragraphs {
		wrapped := wrapTUIText(paragraph, width)
		lines = append(lines, wrapped...)
	}
	return lines
}

func isIPPolicyForm(kind tuiFormKind) bool {
	return kind == formEditIP || kind == formEditDeviceIP || kind == formBatchIP
}

func (m tuiModel) updateFormHelp(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	content := m.ipPolicyFormHelpContent(max(20, m.width-8))
	visible := m.ipPolicyFormHelpVisibleHeight()
	maxOffset := max(0, len(content)-visible)
	switch key.String() {
	case "?", "esc", "backspace":
		m.formHelpExpanded = false
		m.formHelpOffset = 0
	case "up", "k":
		m.formHelpOffset = max(0, m.formHelpOffset-1)
	case "down", "j":
		m.formHelpOffset = min(maxOffset, m.formHelpOffset+1)
	case "pgup":
		m.formHelpOffset = max(0, m.formHelpOffset-visible)
	case "pgdown":
		m.formHelpOffset = min(maxOffset, m.formHelpOffset+visible)
	case "home", "g":
		m.formHelpOffset = 0
	case "end", "G":
		m.formHelpOffset = maxOffset
	}
	return m, nil
}

func (m tuiModel) renderIPPolicyFormHelp() string {
	width := max(20, m.width-8)
	content := m.ipPolicyFormHelpContent(width)
	footer := m.ipPolicyFormHelpFooter()
	visible := m.ipPolicyFormHelpVisibleHeight()
	maxOffset := max(0, len(content)-visible)
	offset := min(max(0, m.formHelpOffset), maxOffset)
	end := min(len(content), offset+visible)
	position := ""
	if len(content) > visible {
		position = tuiDimStyle.Render(fmt.Sprintf("  内容 %d–%d/%d", offset+1, end, len(content)))
	}
	lines := []string{m.renderHeader("来源 IP 规则说明"), "", "  " + tuiTitleStyle.Render("来源 IP 规则说明") + position, ""}
	for _, line := range content[offset:end] {
		lines = append(lines, "    "+line)
	}
	lines = append(lines, footer)
	return strings.Join(lines, "\n")
}

func (m tuiModel) ipPolicyFormHelpFooter() string {
	return m.footer("↑↓/jk 滚动", "PgUp/PgDn 翻页", "Home/End 首尾", "? / esc 返回表单")
}

func (m tuiModel) ipPolicyFormHelpVisibleHeight() int {
	return max(1, m.height-4-len(strings.Split(m.ipPolicyFormHelpFooter(), "\n")))
}

func (m tuiModel) ipPolicyFormHelpContent(width int) []string {
	sections := []struct {
		title string
		body  []string
	}{
		{title: "当前选择", body: ipPolicyFormHelp(m.form)},
		{title: "关闭", body: []string{"不会因来源 IP 拒绝或告警，但服务器看到的公网 IP 仍会存档。它不会关闭配额、到期、异常保护、访问或并发规则。"}},
		{title: "执行模式", body: []string{"“强制限制”会下发 sing-box 拒绝规则；“仅记录告警”允许所有 IP，绑定列表只用于存档和报告不匹配。"}},
		{title: "动态单活", body: []string{"只保留 1 个公网 IP。同一 NAT/路由器后的多台设备显示为同一 IP，可同时使用。旧绑定 IP 在配置的静默秒数（1–3600，默认 60）内无新活动后，新 IP 重试即可接管。新 IP 首次尝试可能短暂失败；旧 IP 仍在静默窗口内时，新 IP 在强制模式下会被拒绝。"}},
		{title: "自动绑定", body: []string{"第一次观察到的 IP 会加入固定列表，直到达到“最多允许 IP”。列表满后不会自动换绑；强制模式下需手动改列表或使用临时替代 IP。"}},
		{title: "手动指定", body: []string{"只允许“固定 IP”列表，不会随地址变化自动更新。强制模式下列表为空会拒绝全部受管连接；仅告警模式下不会拒绝。"}},
		{title: "临时替代 IP", body: []string{"用于换地点后的临时换绑/解锁。有效期内临时列表会取代固定列表，不是两者同时允许；到期后自动恢复原绑定。"}},
		{title: "判定、等待与叠加", body: []string{"系统按服务器看到的公网出口 IP 判定，不识别内网地址。活跃状态来自 sing-box 日志，后台定时识别、更新和应用，换网后应在静默窗口结束后重试。用户级和设备级规则会同时检查，任一层不允许都会拒绝，即“取更严”。"}},
	}
	lines := []string{}
	for index, section := range sections {
		if index > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, tuiAccentStyle().Render(section.title))
		for _, paragraph := range section.body {
			for _, line := range wrapTUIText(paragraph, width) {
				lines = append(lines, tuiDimStyle.Render(line))
			}
		}
	}
	return lines
}

// ipPolicyFormHelp turns the current selections into an operational
// explanation. It deliberately describes the server-observed public IP and
// the delayed log/sync based handover so operators do not mistake the rule for
// a per-device or instant network switch.
func ipPolicyFormHelp(form tuiForm) []string {
	enabled := formFieldValue(form, "规则开关")
	mode := formFieldValue(form, "执行模式")
	binding := formFieldValue(form, "绑定方式")
	handoverSeconds := formFieldValue(form, "换网静默等待（秒）")
	temporaryIP := formFieldValue(form, "临时替代 IP")
	temporaryMinutes := formFieldValue(form, "临时分钟数")
	batch := form.kind == formBatchIP
	if handoverSeconds == "" {
		if batch {
			handoverSeconds = "各用户当前设置"
		} else {
			handoverSeconds = strconv.Itoa(defaultIPPolicyHandoverSeconds)
		}
	}
	paragraphs := []string{}

	if batch && enabled == "保持不变" {
		paragraphs = append(paragraphs, "批量操作：只修改不是“保持不变”的字段；每个用户的其他设置原样保留。")
	}
	if enabled == "关闭" {
		return append(paragraphs,
			"关闭：所有来源 IP 都可连接，不会因本规则被拒绝或产生 IP 告警；公网 IP 仍会存档。",
			"只关闭来源 IP 规则；配额、到期、异常流量保护、域名/端口和并发限制仍各自生效。",
		)
	}

	monitor := mode == "仅记录告警"
	if batch && mode == "保持不变" {
		monitor = false
	}
	if monitor {
		paragraphs = append(paragraphs, "仅记录告警：任何 IP 都可连接；绑定列表只用于判定并报告不匹配，不会下发拒绝规则。")
	} else if batch && mode == "保持不变" {
		paragraphs = append(paragraphs, "执行模式保持不变：强制用户会拒绝不允许的 IP；仅告警用户只存档和报告。")
	} else {
		paragraphs = append(paragraphs, "强制限制：不在当前允许列表中的公网 IP 会被拒绝；同一 NAT/路由器后的多台设备算同一个 IP，可同时使用。")
	}

	switch binding {
	case "动态单活":
		paragraphs = append(paragraphs, "动态单活：只保留 1 个公网 IP。同一 IP/NAT 后的多设备可同时用；旧绑定 IP 连续 "+handoverSeconds+" 秒无新活动后，新 IP 再次连接即可接管。")
		paragraphs = append(paragraphs, "新 IP 的首次尝试可能短暂失败；等待后台识别、应用后重试。旧 IP 仍在静默窗口内时，新 IP 在强制模式下会被拒绝。")
	case "自动绑定":
		paragraphs = append(paragraphs, "自动绑定：第一次观察到的 IP 会加入固定列表；数量满后不会因旧 IP 闲置而自动换绑，其他 IP 在强制模式下会被拒绝。")
	case "手动指定":
		paragraphs = append(paragraphs, "手动指定：只使用“固定 IP”列表，地址变化不会自动换绑；强制模式下列表为空会拒绝该用户/设备的全部连接。")
	default:
		paragraphs = append(paragraphs, "绑定方式保持不变：动态单活会换绑，自动绑定只填满固定列表，手动指定只信任填写的 IP。")
	}

	if temporaryIP != "" && temporaryIP != "-" {
		duration := temporaryMinutes
		if duration == "" || duration == "0" {
			duration = "未设置"
		} else {
			duration += " 分钟"
		}
		paragraphs = append(paragraphs, "临时换绑/解锁：有效期内只允许临时 IP（不是追加到固定 IP），当前时长 "+duration+"；到期后恢复原绑定。")
	} else {
		paragraphs = append(paragraphs, "换地点急用可填“临时替代 IP + 分钟数”：它会临时取代原绑定，到期后恢复；留空表示不启用。")
	}
	paragraphs = append(paragraphs, "来源 IP 仍会存档；后台通过 sing-box 日志判定并定时应用。用户级和设备级 IP 规则会叠加，任一层不允许都会拒绝；其他配额/到期/异常/访问/并发规则仍各自生效。")
	return paragraphs
}

func formFieldValue(form tuiForm, label string) string {
	for _, field := range form.fields {
		if field.label == label {
			return field.value
		}
	}
	return ""
}

func wrapTUIText(text string, width int) []string {
	text = strings.Join(strings.Fields(text), " ")
	width = max(1, width)
	if text == "" {
		return nil
	}
	lines := []string{}
	var current strings.Builder
	for _, r := range []rune(text) {
		candidate := current.String() + string(r)
		if current.Len() > 0 && lipgloss.Width(candidate) > width {
			lines = append(lines, current.String())
			current.Reset()
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		lines = append(lines, current.String())
	}
	return lines
}

func (m tuiModel) renderConfirm() string {
	window, total, maxOffset := m.confirmPromptWindow()
	offset := min(max(0, m.confirmOffset), maxOffset)
	position := ""
	if total > len(window) {
		position = fmt.Sprintf(" · 内容 %d–%d/%d", offset+1, min(offset+len(window), total), total)
	}
	hint := "↑↓ 滚动    y / enter 确认    n / esc 取消"
	if confirmNeedsExplicitYes(m.confirm.action) {
		hint = "↑↓ 滚动    y 明确确认    n / esc 取消"
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(tuiYellow).Padding(1, 2).Width(min(68, m.width-10)).Render(
		tuiWarnStyle.Render("确认操作"+position) + "\n\n" + strings.Join(window, "\n") + "\n\n" + tuiDimStyle.Render(hint))
	return strings.Join([]string{m.renderHeader("确认"), "", "  " + box, "", m.renderStatus()}, "\n")
}

func (m tuiModel) confirmPromptWindow() ([]string, int, int) {
	boxWidth := min(68, m.width-10)
	promptWidth := max(20, boxWidth-8)
	wrapped := lipgloss.NewStyle().Width(promptWidth).Render(m.confirm.prompt)
	lines := strings.Split(wrapped, "\n")
	visible := max(2, m.height-12)
	visible = min(visible, len(lines))
	maxOffset := max(0, len(lines)-visible)
	start := min(max(0, m.confirmOffset), maxOffset)
	return lines[start : start+visible], len(lines), maxOffset
}

func (m tuiModel) renderStatus() string {
	if m.status == "" {
		return ""
	}
	style := tuiGoodStyle
	prefix := "✓ "
	if m.busy {
		style, prefix = tuiWarnStyle, "◌ "
	} else if m.statusError {
		style, prefix = tuiBadStyle, "! "
	}
	return "  " + style.Render(prefix+singleLine(m.status, max(20, m.width-6)))
}

func (m tuiModel) footer(items ...string) string {
	indent := "  "
	limit := max(20, m.width-2)
	lines := []string{}
	current := indent
	for _, item := range items {
		rendered := tuiDimStyle.Render(item)
		separator := ""
		if lipgloss.Width(current) > lipgloss.Width(indent) {
			separator = tuiDimStyle.Render("  ·  ")
		}
		if lipgloss.Width(current)+lipgloss.Width(separator)+lipgloss.Width(rendered) > limit && lipgloss.Width(current) > lipgloss.Width(indent) {
			lines = append(lines, current)
			current = indent + rendered
			continue
		}
		current += separator + rendered
	}
	if lipgloss.Width(current) > lipgloss.Width(indent) {
		lines = append(lines, current)
	}
	return strings.Join(lines, "\n")
}

func (m tuiModel) filteredUsers() []User {
	users := append([]User(nil), m.state.Users...)
	sort.Slice(users, func(i, j int) bool { return strings.ToLower(users[i].Name) < strings.ToLower(users[j].Name) })
	if m.filter == "" {
		return users
	}
	needle := strings.ToLower(m.filter)
	filtered := users[:0]
	for _, u := range users {
		if strings.Contains(strings.ToLower(u.Name), needle) {
			filtered = append(filtered, u)
		}
	}
	return filtered
}

func (m tuiModel) currentUser() *User {
	users := m.filteredUsers()
	if m.cursor < 0 || m.cursor >= len(users) {
		return nil
	}
	u := users[m.cursor]
	return &u
}

func (m *tuiModel) toggleCheckedUser(name string) {
	if m.checkedUsers == nil {
		m.checkedUsers = map[string]bool{}
	}
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return
	}
	if m.checkedUsers[key] {
		delete(m.checkedUsers, key)
	} else {
		m.checkedUsers[key] = true
	}
}

func (m tuiModel) batchSelectedNames() []string {
	if m.state == nil || len(m.checkedUsers) == 0 {
		return nil
	}
	names := []string{}
	for _, u := range m.state.Users {
		if m.checkedUsers[strings.ToLower(u.Name)] {
			names = append(names, u.Name)
		}
	}
	return normalizedBatchUserNames(names)
}

func (m *tuiModel) pruneCheckedUsers() {
	if len(m.checkedUsers) == 0 || m.state == nil {
		return
	}
	existing := map[string]bool{}
	for _, u := range m.state.Users {
		existing[strings.ToLower(u.Name)] = true
	}
	for name := range m.checkedUsers {
		if !existing[name] {
			delete(m.checkedUsers, name)
		}
	}
}

func (m *tuiModel) fixCursor() {
	m.pruneCheckedUsers()
	entryCount := len(subscriptionTUIEntries(m.state))
	if entryCount == 0 {
		m.subscriptionCursor = 0
	} else {
		m.subscriptionCursor = min(max(0, m.subscriptionCursor), entryCount-1)
	}
	if u := findUser(m.state, m.selected); u != nil {
		if len(u.Nodes) == 0 {
			m.nodeCursor = 0
		} else {
			m.nodeCursor = min(max(0, m.nodeCursor), len(u.Nodes)-1)
		}
		if len(u.Devices) == 0 {
			m.deviceCursor = 0
		} else {
			m.deviceCursor = min(max(0, m.deviceCursor), len(u.Devices)-1)
		}
		if m.mode == tuiAccessHistory {
			m.fixAccessCursor(len(recentAccessesForUser(u, m.accessFilter)))
		}
	}
	count := len(m.filteredUsers())
	if count == 0 {
		m.cursor, m.offset = 0, 0
		return
	}
	if m.cursor >= count {
		m.cursor = count - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	pageSize := max(3, m.height-13)
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+pageSize {
		m.offset = m.cursor - pageSize + 1
	}
}

func (m *tuiModel) fixAccessCursor(count int) {
	if count <= 0 {
		m.accessCursor, m.accessOffset = 0, 0
		return
	}
	m.accessCursor = min(max(0, m.accessCursor), count-1)
	pageSize := max(3, m.height-11)
	if m.accessCursor < m.accessOffset {
		m.accessOffset = m.accessCursor
	}
	if m.accessCursor >= m.accessOffset+pageSize {
		m.accessOffset = m.accessCursor - pageSize + 1
	}
	m.accessOffset = min(max(0, m.accessOffset), max(0, count-1))
}

func configurationPending(s *State) bool {
	rendered, err := renderConfig(s)
	if err != nil {
		return true
	}
	current, err := os.ReadFile(s.ConfigPath)
	if err != nil {
		return true
	}
	var want, have any
	if json.Unmarshal(rendered, &want) != nil || json.Unmarshal(current, &have) != nil {
		return true
	}
	return !reflect.DeepEqual(want, have)
}

func runtimeApplyPending(s *State) bool {
	return s != nil && (s.IPApplyPending || s.BurstApplyPending || s.RateApplyPending || s.StatsApplyPending)
}

func userStatus(u User) string {
	if expired(u, time.Now()) {
		return "已到期"
	}
	if overQuota(u) {
		return "配额用尽"
	}
	if burstBlocked(u, time.Now()) {
		if burstSoftBlocked(u, time.Now()) {
			return "软封限速"
		}
		return "硬封断连"
	}
	if !u.Enabled {
		return "已禁用"
	}
	return "已启用"
}

func burstSummary(u User, now time.Time) string {
	if !u.Burst.Enabled {
		return "关闭"
	}
	policy := normalizedBurst(u.Burst)
	if burstBlocked(u, now) {
		if policy.Action == burstActionSoft {
			return fmt.Sprintf("软封限速至 %s（↑%g / ↓%g Kbps）", formatDisplayTime(u.BlockedUntil), policy.SoftUploadKbps, policy.SoftDownloadKbps)
		}
		return fmt.Sprintf("硬封断连至 %s", formatDisplayTime(u.BlockedUntil))
	}
	if policy.Action == burstActionSoft {
		return fmt.Sprintf("%d 分钟内 %s / %s，触发软封 %d 分钟（↑%g / ↓%g Kbps）", policy.WindowMinutes, formatSize(burstUsage(u, now)), formatSize(policy.LimitBytes), policy.BlockMinutes, policy.SoftUploadKbps, policy.SoftDownloadKbps)
	}
	return fmt.Sprintf("%d 分钟内 %s / %s，触发硬封断连 %d 分钟", policy.WindowMinutes, formatSize(burstUsage(u, now)), formatSize(policy.LimitBytes), policy.BlockMinutes)
}

func ipPolicySummary(u User, now time.Time) string {
	summary := ipPolicySummaryText(u.IPPolicy, len(u.SourceIPs), now)
	deviceRules := 0
	for _, device := range u.Devices {
		if device.IPPolicy.Enabled {
			deviceRules++
		}
	}
	if deviceRules > 0 {
		summary += fmt.Sprintf(" · 与 %d 个设备规则叠加（取更严）", deviceRules)
	}
	return summary
}

func userTrafficAccountingAudit(u User) string {
	return fmt.Sprintf("实际 ↑ %s / ↓ %s   计费 %s（%s）", formatSize(u.Upload), formatSize(u.Download), formatSize(measuredUsage(u)), quotaModeText(u.QuotaMode))
}

func quotaModeOptions() []string {
	return []string{"双向合计", "仅上传", "仅下载"}
}

func quotaModeOption(mode string) string {
	return quotaModeText(mode)
}

func quotaModeFromOption(option string) string {
	switch strings.TrimSpace(option) {
	case "仅上传":
		return quotaModeUpload
	case "仅下载":
		return quotaModeDownload
	default:
		return quotaModeTotal
	}
}

func enabledDeviceIPRuleCount(u User) int {
	count := 0
	for _, device := range u.Devices {
		if device.IPPolicy.Enabled {
			count++
		}
	}
	return count
}

func userAlertAudit(s *State, userName string) (int, *Alert) {
	unread := 0
	var last *Alert
	for index := range s.Alerts {
		alert := &s.Alerts[index]
		if !strings.EqualFold(alert.User, userName) {
			continue
		}
		if !alert.Acknowledged {
			unread++
		}
		last = alert
	}
	return unread, last
}

func deviceIPPolicySummary(user User, device Device, now time.Time) string {
	summary := ipPolicySummaryText(device.IPPolicy, len(device.SourceIPs), now)
	if user.IPPolicy.Enabled {
		summary += " · 与用户规则叠加（取更严）"
	}
	return summary
}

func ipPolicySummaryText(raw IPPolicy, archivedCount int, now time.Time) string {
	policy := normalizedIPPolicy(raw)
	if !policy.Enabled {
		return fmt.Sprintf("关闭 · 不拒绝/告警 · 仅存档（%d 个 IP）", archivedCount)
	}
	mode := "强制拒绝"
	if policy.Mode == "monitor" {
		mode = "仅告警（不拒绝）"
	}
	allowed := activePolicyIPs(policy, now)
	allowedText := strings.Join(allowed, ", ")
	if until, err := time.Parse(time.RFC3339Nano, policy.TemporaryUntil); len(policy.TemporaryIPs) > 0 && err == nil && now.Before(until) {
		return fmt.Sprintf("%s · 临时替代原绑定至 %s · 允许 %s", mode, formatDisplayTime(policy.TemporaryUntil), dash(allowedText))
	}
	switch policy.Binding {
	case "dynamic":
		if allowedText == "" {
			return fmt.Sprintf("%s · 动态单活 · 等待首个 IP（首次可短暂失败后重试） · 换网等待 %d 秒", mode, policy.HandoverSeconds)
		}
		return fmt.Sprintf("%s · 动态单活 %s · 同 NAT 可多设备 · 旧 IP 静默 %d 秒后换绑", mode, allowedText, policy.HandoverSeconds)
	case "manual":
		if allowedText == "" {
			if policy.Mode == "enforce" {
				return mode + " · 手动固定 · 无允许 IP（全部拒绝）"
			}
			return mode + " · 手动固定 · 列表为空"
		}
		return fmt.Sprintf("%s · 手动固定 %d/%d · %s", mode, len(allowed), policy.MaxIPs, allowedText)
	default:
		if allowedText == "" {
			return mode + " · 首次自动绑定 · 等待首个 IP（首次放行）"
		}
		return fmt.Sprintf("%s · 自动固定 %d/%d · %s（不自动换绑）", mode, len(allowed), policy.MaxIPs, allowedText)
	}
}

func accessPolicySummary(policy AccessPolicy) string {
	policy = normalizedAccessPolicy(policy)
	parts := []string{}
	if len(policy.AllowedDomains) > 0 {
		parts = append(parts, fmt.Sprintf("仅允许 %d 个域名后缀", len(policy.AllowedDomains)))
	}
	if len(policy.BlockedDomains) > 0 {
		parts = append(parts, fmt.Sprintf("拒绝 %d 个域名后缀", len(policy.BlockedDomains)))
	}
	if len(policy.BlockedPorts) > 0 {
		parts = append(parts, fmt.Sprintf("拒绝 %d 个端口", len(policy.BlockedPorts)))
	}
	if policy.MaxConnections > 0 {
		action := map[string]string{"alert": "告警", "disable-device": "禁用设备", "disable-user": "禁用用户"}[policy.ConnectionAction]
		parts = append(parts, fmt.Sprintf("连接 >%d 时%s", policy.MaxConnections, action))
	}
	if len(parts) == 0 {
		return "不限"
	}
	return strings.Join(parts, " · ")
}

func formatQuotaUI(n int64) string {
	if n == 0 {
		return "不限"
	}
	return formatSize(n)
}

func nodeTemplateNames(s *State) []string {
	templates := nodeTemplates(s)
	names := make([]string, 0, len(templates))
	for _, template := range templates {
		names = append(names, template.Name)
	}
	if len(names) == 0 {
		return []string{"默认线路"}
	}
	return names
}

func defaultExportPath(statePath, user string, now time.Time) string {
	chinaTime := now.In(time.FixedZone("Asia/Shanghai", 8*60*60))
	filename := fmt.Sprintf("%s-%s.yaml", slug(user), chinaTime.Format("20060102-150405"))
	return filepath.Join(filepath.Dir(statePath), "exports", filename)
}

func defaultDeviceExportPath(statePath, user, device string, now time.Time) string {
	chinaTime := now.In(time.FixedZone("Asia/Shanghai", 8*60*60))
	devicePart := slug(device)
	if devicePart == "" {
		devicePart = "device"
	}
	filename := fmt.Sprintf("%s-%s-%s.yaml", slug(user), devicePart, chinaTime.Format("20060102-150405"))
	return filepath.Join(filepath.Dir(statePath), "exports", filename)
}

func formatMbpsUI(value float64) string {
	if value == 0 {
		return "不限"
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func formatCurrentMbps(value float64) string {
	if value < 0.005 {
		return "0"
	}
	if value < 1 {
		return strconv.FormatFloat(value, 'f', 2, 64)
	}
	return strconv.FormatFloat(value, 'f', 1, 64)
}

func usageSparkline(u User, width int) string {
	if len(u.UsageHistory) == 0 {
		return tuiDimStyle.Render("近 24h 速率  暂无历史样本")
	}
	width = max(8, width)
	start := max(0, len(u.UsageHistory)-width)
	points := u.UsageHistory[start:]
	maximum := 0.0
	for _, point := range points {
		maximum = max(maximum, point.UploadMbps+point.DownloadMbps)
	}
	levels := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, point := range points {
		value := point.UploadMbps + point.DownloadMbps
		index := 0
		if maximum > 0 {
			index = min(len(levels)-1, int(math.Round(value/maximum*float64(len(levels)-1))))
		}
		b.WriteRune(levels[index])
	}
	return fmt.Sprintf("近 24h 速率  %s  峰值 %s Mbps", tuiAccentStyle().Render(b.String()), formatCurrentMbps(maximum))
}

func activeConnectionsForUser(s *State, userName string) []ActiveConnection {
	if s == nil {
		return nil
	}
	connections := make([]ActiveConnection, 0, len(s.ActiveConnections))
	now := time.Now()
	for _, connection := range s.ActiveConnections {
		if !connectionActiveAt(connection, now) {
			continue
		}
		if strings.EqualFold(connection.User, userName) {
			connections = append(connections, connection)
		}
	}
	sort.Slice(connections, func(i, j int) bool { return connections[i].LastSeen > connections[j].LastSeen })
	return connections
}

func userRateSummary(u User) string {
	if rateLimited(u) {
		return formatMbpsUI(u.UploadMbps) + " / " + formatMbpsUI(u.DownloadMbps)
	}
	limited := 0
	for _, n := range u.Nodes {
		if nodeRateLimited(n) {
			limited++
		}
	}
	if limited == 0 {
		return "不限"
	}
	return fmt.Sprintf("逐节点 (%d)", limited)
}

func quotaProgressBar(u User, width int) string {
	width = max(10, width)
	if userQuota(u) <= 0 {
		return tuiDimStyle.Render("[" + strings.Repeat("─", width) + "] 不限")
	}
	percent := usagePercent(u)
	clamped := min(100, max(0, percent))
	filled := int(math.Round(clamped * float64(width) / 100))
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	style := tuiGoodStyle
	if percent >= 95 {
		style = tuiBadStyle
	} else if percent >= 80 {
		style = tuiWarnStyle
	}
	return style.Render(fmt.Sprintf("[%s] %.1f%%", bar, percent))
}

func throttleSummary(u User) string {
	if !u.Throttle.Enabled {
		return "关闭"
	}
	policy := normalizedThrottle(u.Throttle)
	if userQuota(u) <= 0 {
		return "已开启，但无限配额时不触发"
	}
	return fmt.Sprintf("当前第 %d 档 · %.0f%%→%.0f%%速度 · %.0f%%→%.0f%%速度",
		throttleStage(u), policy.Tier1Usage, policy.Tier1Speed, policy.Tier2Usage, policy.Tier2Speed)
}

func billingSummary(u User) string {
	policy := normalizedBilling(u.Billing)
	parts := []string{}
	if u.ExtraQuotaBytes > 0 {
		parts = append(parts, "附加包 "+formatSize(u.ExtraQuotaBytes))
	}
	if policy.Enabled {
		parts = append(parts, fmt.Sprintf("每月 %d 日清零", policy.CycleDay), "下次 "+formatDisplayTime(policy.NextReset))
	} else {
		parts = append(parts, "不自动清零")
	}
	if len(u.BillingHistory) > 0 {
		last := u.BillingHistory[len(u.BillingHistory)-1]
		parts = append(parts, "上期 "+formatSize(last.UploadBytes+last.DownloadBytes))
	}
	return strings.Join(parts, " · ")
}

func rateMarkText(mark uint32) string {
	if mark == 0 {
		return "-"
	}
	return fmt.Sprintf("0x%08x", mark)
}

func formatQuotaForInput(n int64) string {
	if n == 0 {
		return "0"
	}
	return formatSize(n)
}

// In the CUI, a bare quota is interpreted as GiB. Treating "200" as 200 bytes
// is technically consistent with the low-level parser but surprising in a user
// form. Explicit suffixes such as M, G and T retain their original meaning.
func normalizeQuotaInput(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "0" {
		return "0"
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return value + "G"
	}
	return value
}

func cell(value string, width int) string {
	plainWidth := lipgloss.Width(value)
	if plainWidth > width-1 {
		var b strings.Builder
		for _, r := range value {
			if lipgloss.Width(b.String()+string(r)+"…") > width-1 {
				break
			}
			b.WriteRune(r)
		}
		value = b.String() + "…"
		plainWidth = lipgloss.Width(value)
	}
	return value + strings.Repeat(" ", max(1, width-plainWidth))
}

func trimLastRune(s string) string {
	if s == "" {
		return s
	}
	_, size := utf8.DecodeLastRuneInString(s)
	return s[:len(s)-size]
}

func lastOutputLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

func singleLine(s string, width int) string {
	s = strings.Join(strings.Fields(s), " ")
	if lipgloss.Width(s) <= width {
		return s
	}
	return cell(s, width)
}

func tuiAccentStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(tuiAccentBright).Bold(true)
}
