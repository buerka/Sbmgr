package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

func isMeshForm(kind tuiFormKind) bool { return kind >= formMeshInit && kind <= formMeshRemoveRoute }

func (m tuiModel) meshMenuEntries() []tuiMenuEntry {
	if m.state.Mesh == nil {
		if m.state.MeshAgent.Cluster != "" {
			return []tuiMenuEntry{{title: "查看本机状态", description: "管理身份、执行线路与当前修订"}}
		}
		return []tuiMenuEntry{{title: "初始化主机", description: "创建管理集合；线路协议单独选择"}, {title: "作为从机加入", description: "导入主机生成的管理身份文件"}}
	}
	return []tuiMenuEntry{
		{title: "查看节点与线路", description: "查看拓扑、当前修订和待应用状态"},
		{title: "接入新从机", description: "登记专用 SSH 管理连接；不要求 WG"},
		{title: "导出从机接入文件", description: "导出管理身份；不包含线路凭据"},
		{title: "新增或修改线路", description: "逐跳选择协议；最后一跳就地落地"},
		{title: "测试节点通信", description: "测试管理连接，核对已应用修订与事务"},
		{title: "应用主从拓扑", description: "逐机校验、备份并应用；失败回滚"},
		{title: "恢复未完成事务", description: "按持久日志完成回滚或确认提交"},
		{title: "删除线路", description: "仅允许删除未被用户引用的线路"},
		{title: "移除从机", description: "移除管理登记；需先停用该机线路"},
		{title: "配置客户端入口", description: "导入该机器的公开入口参数；需应用拓扑"},
		{title: "同步入口授权", description: "汇总用量并下发权限；后台也会自动执行"},
	}
}

func (m tuiModel) renderMesh() string {
	return m.renderMenuPage("主从管理", "节点与线路", "同一从机可中转或落地；线路编辑后需应用主从拓扑。", m.meshMenuEntries(), m.footer("↑↓ 选择", "enter 打开", "esc 返回"))
}

func (m tuiModel) renderMeshTopology() string {
	content := []string{""}
	if t := m.state.Mesh; t != nil {
		applied := uint64(0)
		if m.state.MeshAgent.Active != nil {
			applied = m.state.MeshAgent.Active.Revision
		}
		content = append(content, fmt.Sprintf("  主机 %s · 管理集合 %s", t.Master, t.ID), fmt.Sprintf("  待应用修订 %d · 已应用修订 %d", t.Revision, applied), "", "  节点（管理连接）")
		for _, member := range t.Members {
			content = append(content, "  "+member.ID+" · "+dash(member.SSHHost))
		}
		content = append(content, "", "  线路（客户端 → 指定入口 → 所列节点）")
		for _, route := range t.Routes {
			exit := route.Exit
			if exit == "" {
				exit = "就地落地"
			}
			content = append(content, "  "+route.ID+"：入口 "+t.Entry(route)+" → "+strings.Join(route.Hops, " → ")+" → "+exit)
			for i, tr := range route.Transports {
				content = append(content, fmt.Sprintf("    → %s · %s · %s:%d", route.Hops[i], tr.Type, tr.Server, tr.Port))
			}
		}
		if len(t.Routes) == 0 {
			content = append(content, "  暂无线路；可添加主机本地落地或从机中转链。")
		}
	} else {
		j := m.state.MeshAgent
		content = append(content, "  集合 "+dash(j.Cluster)+" · 从机 "+dash(j.Member), "  应用事务 "+dash(j.Phase))
		if j.Active != nil {
			content = append(content, fmt.Sprintf("  已应用修订 %d · 执行线路 %d", j.Active.Revision, len(j.Active.Hops)))
		}
	}
	if m.state.MeshRollout != nil {
		content = append(content, "", "  有未完成事务，请从主从菜单执行恢复。")
	}
	content = append(content, "", "  密钥与线路凭据已隐藏。订阅分配仅提供已应用线路。")
	for i := range content {
		content[i] = singleLine(content[i], max(24, m.width-2))
	}
	return m.renderDetailViewportAtOffset("节点与线路", content, -1, -1, m.footer("↑↓ 滚动", "esc 返回"), m.detailOffset)
}

func (m tuiModel) updateMesh(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	entries := m.meshMenuEntries()
	m.menuCursor = min(max(0, m.menuCursor), len(entries)-1)
	switch key.String() {
	case "esc", "q", "backspace":
		m.menuCursor, m.mode = 0, tuiManage
	case "up", "k":
		m.menuCursor = max(0, m.menuCursor-1)
	case "down", "j":
		m.menuCursor = min(len(entries)-1, m.menuCursor+1)
	case "home":
		m.menuCursor = 0
	case "end":
		m.menuCursor = len(entries) - 1
	case "enter":
		if m.state.Mesh == nil {
			if m.state.MeshAgent.Cluster != "" {
				m.mode, m.detailOffset = tuiMeshTopology, 0
				return m, nil
			}
			if m.menuCursor == 0 {
				m.form = tuiForm{kind: formMeshInit, title: "初始化主机", fields: []tuiField{{label: "主从集合", value: "default"}, {label: "主机标识", value: "master"}}}
			} else {
				m.form = tuiForm{kind: formMeshJoin, title: "加入主从集合", fields: []tuiField{{label: "接入文件绝对路径", placeholder: m.appDataPath("node-join.json")}}}
			}
			m.mode = tuiFormMode
			return m, nil
		}
		ids := []string{}
		for _, member := range m.state.Mesh.Members {
			if member.ID != m.state.Mesh.Master {
				ids = append(ids, member.ID)
			}
		}
		routes := []string{}
		for _, route := range m.state.Mesh.Routes {
			routes = append(routes, route.ID)
		}
		first := func(values []string) string {
			if len(values) > 0 {
				return values[0]
			}
			return ""
		}
		switch m.menuCursor {
		case 0:
			m.mode, m.detailOffset = tuiMeshTopology, 0
			return m, nil
		case 1:
			m.form = tuiForm{kind: formMeshAdd, title: "接入新从机", fields: []tuiField{
				{label: "节点标识", placeholder: "relay-a（小写英文、数字、连字符）"}, {label: "SSH 管理主机", placeholder: "relay.example.com"}, {label: "SSH 端口", value: "22"}, {label: "SSH 用户", value: "root"}, {label: "专用 SSH 私钥路径", placeholder: "绝对路径；使用固定 RPC 命令密钥"}, {label: "从机应用目录", value: "/srv/sbmgr"},
			}}
		case 2:
			if len(ids) == 0 {
				m.status, m.statusError = "请先登记从机", true
				return m, nil
			}
			m.form = tuiForm{kind: formMeshExport, title: "导出从机接入配置", fields: []tuiField{{label: "从机", value: first(ids), options: ids}, {label: "输出绝对路径", value: filepath.Join(m.appDataPath("exports"), "mesh-join-"+time.Now().Format("20060102-150405")+".json")}}}
		case 3:
			m.form = tuiForm{kind: formMeshRoute, title: "编排中转与落地线路", fields: []tuiField{
				{label: "线路标识", placeholder: "新名称；填现有标识可修改线路"},
				{label: "中转链", placeholder: "relay-a,exit-b；或仅填主机标识就地落地"},
				{label: "每跳协议", placeholder: "hysteria2、socks、wireguard；混用时以逗号分隔"},
				{label: "每跳数据地址", placeholder: "host:port 逗号分隔；留空自动分配"},
				{label: "轮换线路凭据", value: "否", options: []string{"否", "是"}},
				{label: "客户端入口", value: m.state.Mesh.Master, placeholder: "主机或已配置入口的从机标识"},
				{label: "末跳出站", placeholder: "留空直接出站；或末跳机器上的出站 tag"},
			}}
		case 4:
			return m.startAction("正在测试主从通信", func(a *app) error { return a.meshCoordinate("check") })
		case 5:
			m.openConfirm(tuiConfirm{action: confirmMeshApply, prompt: fmt.Sprintf("将修订 %d 应用到 %d 台机器？\n\n先逐机校验并备份，主机入口最后切换。应用失败会回滚；通信中断时保留日志，可从菜单恢复。连接可能在服务重启时断开。", m.state.Mesh.Revision, len(m.state.Mesh.Members))})
			return m, nil
		case 6:
			return m.startAction("正在恢复主从事务", func(a *app) error { return a.meshCoordinate("recover") })
		case 7:
			if len(routes) == 0 {
				m.status, m.statusError = "暂无可删除线路", true
				return m, nil
			}
			m.form = tuiForm{kind: formMeshRemoveRoute, title: "删除线路", fields: []tuiField{{label: "线路", value: first(routes), options: routes}}}
		case 8:
			if len(ids) == 0 {
				m.status, m.statusError = "暂无可移除从机", true
				return m, nil
			}
			m.form = tuiForm{kind: formMeshRemove, title: "移除从机", fields: []tuiField{{label: "从机", value: first(ids), options: ids}}}
		case 9:
			allIDs := append([]string{m.state.Mesh.Master}, ids...)
			m.form = tuiForm{kind: formMeshEntry, title: "配置客户端入口", fields: []tuiField{{label: "节点", value: first(allIDs), options: allIDs}, {label: "入口公开参数 JSON", placeholder: "绝对路径；只含地址、端口、SNI、公钥与 short-id"}}}
		case 10:
			return m.startAction("正在同步入口权限与用量", func(a *app) error { return a.meshSyncAccess() })
		}
		m.mode = tuiFormMode
	}
	return m, nil
}

func (m tuiModel) submitMeshForm() (tea.Model, tea.Cmd) {
	v := func(i int) string { return strings.TrimSpace(m.form.fields[i].value) }
	var args []string
	switch m.form.kind {
	case formMeshInit:
		args = []string{"init", "--cluster", v(0), "--id", v(1)}
	case formMeshJoin:
		args = []string{"join", "--file", v(0)}
	case formMeshAdd:
		args = []string{"add", "--id", v(0), "--host", v(1), "--port", v(2), "--user", v(3), "--key", v(4), "--home", v(5)}
	case formMeshRoute:
		args = []string{"route", "--id", v(0), "--hops", v(1), "--protocols", v(2), "--endpoints", v(3)}
		if len(m.form.fields) > 6 {
			args = append(args, "--entry", v(5), "--exit", v(6))
		}
		if v(4) == "是" {
			args = append(args, "--rotate")
		}
	case formMeshExport:
		args = []string{"export", "--node", v(0), "--output", v(1)}
	case formMeshEntry:
		args = []string{"entry", "--id", v(0), "--file", v(1)}
	case formMeshRemove, formMeshRemoveRoute:
		action := confirmMeshRemove
		if m.form.kind == formMeshRemoveRoute {
			action = confirmMeshRemoveRoute
		}
		m.mode = tuiMesh
		m.openConfirm(tuiConfirm{action: action, server: v(0), prompt: "移除 " + v(0) + "？仍被引用时会拒绝。保存后需应用主从拓扑，原有从机数据保留。"})
		return m, nil
	}
	m.mode = tuiMesh
	return m.startAction("正在保存主从设置", func(a *app) error { return a.meshCmd(args) })
}
