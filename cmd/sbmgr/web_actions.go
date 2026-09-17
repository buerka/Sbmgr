package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sbmgr/internal/mesh"
	"strconv"
	"strings"
)

type webField struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"`
	Source   string   `json:"source,omitempty"`
}
type webAction struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Scope     string     `json:"scope"`
	Effect    string     `json:"effect"`
	Danger    bool       `json:"danger"`
	Fields    []webField `json:"fields"`
	command   []string
	positions []string
}
type webActionInput struct {
	Action  string            `json:"action"`
	Fields  map[string]string `json:"fields"`
	Confirm bool              `json:"confirm"`
}

func webActions() []webAction {
	f := func(key, label, kind string, required bool) webField {
		return webField{Key: key, Label: label, Type: kind, Required: required}
	}
	selectField := func(key, label string, values ...string) webField {
		v := f(key, label, "select", key == "kind")
		v.Options = values
		return v
	}
	source := func(key, label, from string, required bool) webField {
		v := f(key, label, "select", required)
		v.Source = from
		return v
	}
	user := source("user", "用户", "users", true)
	device := source("device", "设备", "devices", true)
	node := source("node", "节点", "nodes", true)
	quota := f("quota", "配额（如 100G；0 不限）", "text", false)
	up := f("up-mbps", "上传上限 Mbps（0 不限）", "number", false)
	down := f("down-mbps", "下载上限 Mbps（0 不限）", "number", false)
	saved := "已保存；点击「应用配置」使运行中的连接策略生效。"
	meshSaved := "已保存拓扑；点击「应用拓扑」校验并同步各机器，再分配节点。"
	var specs []webAction
	add := func(id, title, scope, command, effect string, danger bool, positions []string, fields ...webField) {
		if fields == nil {
			fields = []webField{}
		}
		specs = append(specs, webAction{ID: id, Title: title, Scope: scope, Effect: effect, Danger: danger, Fields: fields, command: strings.Fields(command), positions: positions})
	}
	add("user.add", "新建用户", "users", "user add", saved, false, []string{"user"}, f("user", "用户名", "text", true), quota, selectField("quota-mode", "计费方向", "total", "upload", "download"), f("expire", "到期日（留空不限）", "date", false), f("node-name", "初始节点名称", "text", true), source("outbound", "初始线路", "outbounds", false), up, down)
	add("user.clone", "复制用户策略", "users", "user clone", saved, false, []string{"user"}, f("user", "新用户名", "text", true), source("from", "复制自", "users", true))
	add("user.set", "配额与限速", "user", "user set", saved, false, []string{"user"}, user, quota, selectField("quota-mode", "计费方向", "total", "upload", "download"), f("extra-quota", "本期附加流量（0 清除）", "text", false), f("expire", "到期日", "date", false), f("clear-expire", "清除到期日", "checkbox", false), up, down, selectField("billing-enabled", "自动账期", "true", "false"), f("billing-day", "账期日（1–28）", "number", false))
	add("user.ip", "来源 IP 规则", "user", "user set", saved, false, []string{"user"}, user, selectField("ip-enabled", "启用规则", "true", "false"), selectField("ip-mode", "执行方式", "enforce", "monitor"), selectField("ip-binding", "绑定方式", "dynamic", "auto", "manual"), f("ip-max", "最多来源 IP 数", "number", false), f("ip-handover-seconds", "换绑宽限秒数", "number", false), f("ip-allowed", "固定 IP（逗号分隔）", "text", false), f("ip-temp", "临时替代 IP（逗号分隔）", "text", false), f("ip-temp-minutes", "临时 IP 有效分钟数", "number", false))
	add("user.burst", "异常流量保护", "user", "user set", saved, false, []string{"user"}, user, selectField("burst-enabled", "启用保护", "true", "false"), f("burst-window", "滑动窗口（分钟）", "number", false), f("burst-limit", "窗口流量阈值（如 2G）", "text", false), f("burst-block", "封禁分钟数", "number", false), selectField("burst-action", "处理方式", "soft", "hard"), f("burst-soft-up-kbps", "软封上传 Kbps", "number", false), f("burst-soft-down-kbps", "软封下载 Kbps", "number", false))
	add("user.throttle", "阶梯限速", "user", "user set", saved, false, []string{"user"}, user, selectField("tiered", "启用阶梯限速", "true", "false"), f("tier1-usage", "第一档用量百分比", "number", false), f("tier1-speed", "第一档保留速度百分比", "number", false), f("tier2-usage", "第二档用量百分比", "number", false), f("tier2-speed", "第二档保留速度百分比", "number", false))
	for _, op := range []struct {
		id, title string
		danger    bool
	}{{"enable", "启用用户", false}, {"disable", "禁用用户", true}, {"unblock", "解除临时封禁", false}, {"delete", "删除用户", true}} {
		add("user."+op.id, op.title, "user", "user "+op.id, saved, op.danger, []string{"user"}, user)
	}
	add("node.add", "分配节点", "user", "node add", saved, false, []string{"user"}, user, device, f("name", "节点显示名称", "text", true), source("outbound", "线路", "outbounds", false), up, down)
	add("node.set", "名称与节点限速", "node", "node set", saved, false, []string{"user", "node"}, user, node, device, f("name", "新的显示名称（留空保留）", "text", false), up, down)
	add("node.delete", "撤销节点", "node", "node delete", saved, true, []string{"user", "node"}, user, node, device)
	add("device.add", "新增设备", "user", "device add", saved, false, []string{"user"}, user, f("name", "设备名称", "text", true), source("from", "复制节点自设备", "devices", false))
	for _, op := range []struct {
		id, title string
		danger    bool
	}{{"enable", "启用设备", false}, {"disable", "禁用设备", true}, {"rotate", "重建设备身份", true}, {"rotate-link", "轮换订阅链接", true}, {"delete", "删除设备", true}} {
		effect := saved
		if op.id == "rotate-link" {
			effect = "订阅已轮换，旧链接立即失效；请重新交付。"
		}
		add("device."+op.id, op.title, "device", "device "+op.id, effect, op.danger, []string{"user", "device"}, user, device)
	}
	add("device.ip", "设备来源规则", "device", "device set", saved, false, []string{"user", "device"}, user, device, selectField("ip-enabled", "启用规则", "true", "false"), selectField("ip-mode", "执行方式", "enforce", "monitor"), selectField("ip-binding", "绑定方式", "dynamic", "auto", "manual"), f("ip-max", "最多来源 IP 数", "number", false), f("ip-allowed", "固定 IP（逗号分隔）", "text", false))
	add("config.apply", "应用配置", "ops", "apply --restart", "配置已校验并应用。失败时保留或恢复原运行配置。", true, nil)
	add("config.check", "校验配置", "ops", "check", "配置校验通过。", false, nil)
	add("backup.create", "创建备份", "ops", "backup create", "已创建一致性状态备份。", false, nil)
	add("backup.restore", "恢复备份", "backup", "backup restore", "状态已恢复；检查后应用配置。", true, []string{"name"}, source("name", "备份", "backups", true))
	add("health.check", "检查出站健康", "ops", "health check", "出站健康检查完成。", false, nil)
	add("health.set", "健康检查设置", "ops", "health set", "健康设置已保存，后台下一轮生效。", false, nil, selectField("mode", "自动探测", "auto", "off"), f("interval", "间隔分钟", "number", false), f("timeout", "超时秒数", "number", false), f("failures", "告警失败次数", "number", false), f("targets", "探测目标 tag=host:port（逗号分隔）", "text", false))
	for _, op := range []struct {
		id, title, effect string
		danger            bool
	}{{"check", "检查主从通信", "主从通信检查通过。", false}, {"sync", "同步入口授权", "授权与用量同步完成。", false}, {"apply", "应用拓扑", "拓扑已校验并应用到各机器。", true}, {"recover", "恢复未完成事务", "主从事务恢复完成。", true}} {
		add("mesh."+op.id, op.title, "routes", "mesh "+op.id, op.effect, op.danger, nil)
	}
	add("mesh.init", "初始化主从管理", "routes", "mesh init", meshSaved, false, nil, f("id", "本机标识", "text", true), f("cluster", "集群标识", "text", true))
	add("mesh.add", "登记从机", "routes", "mesh add", meshSaved, false, nil, f("id", "从机标识", "text", true), f("host", "SSH 地址", "text", true), f("port", "SSH 端口（默认 22）", "number", false), f("user", "SSH 用户（默认 root）", "text", false), f("key", "主机上的专用私钥路径", "text", true), f("home", "从机应用目录", "text", true))
	add("mesh.route", "编排线路", "routes", "mesh route", meshSaved, false, nil, f("id", "线路标识（相同标识更新）", "text", true), source("entry", "客户端入口", "members", true), f("hops", "经过的成员标识（逗号分隔；本机落地填入口标识）", "text", true), f("exit", "末跳出站 tag（留空直接出站）", "text", false), selectField("protocols", "跨机传输协议", "hysteria2", "socks", "wireguard"))
	add("mesh.remove-route", "删除线路", "route", "mesh remove-route", meshSaved, true, nil, f("id", "线路标识", "text", true))
	add("mesh.remove", "移除从机", "member", "mesh remove", meshSaved, true, nil, f("id", "从机标识", "text", true))
	add("subscription.set", "订阅服务设置", "subscriptions", "subscription set", "设置已保存；监听与证书变化需要重启 sbmgr 服务。", false, nil, selectField("enabled", "启用订阅", "true", "false"), f("listen", "监听 host:port", "text", false), f("base-url", "公开 HTTPS 基础地址", "text", false), f("tls-cert", "证书绝对路径", "text", false), f("tls-key", "私钥绝对路径", "text", false))
	add("template.set", "设置订阅模板", "subscriptions", "template set", "模板设置已保存，下次获取订阅即生效。", false, nil, f("path", "服务器上的模板绝对路径", "text", true))
	add("proxy.add", "添加出站或端点", "routes", "", "基础模板已校验并保存；应用配置后生效。", false, nil, selectField("kind", "类型", "outbound", "endpoint"), f("json", "单个 sing-box 出站或端点 JSON（提交后不回显）", "secret-text", true))
	add("proxy.delete", "删除出站或端点", "proxy", "proxy delete", "基础模板已更新；应用配置后生效。", true, []string{"kind", "tag"}, selectField("kind", "类型", "outbound", "endpoint"), f("tag", "出站或端点 tag", "text", true))
	policyFields := []webField{f("allow-domains", "允许域名（逗号分隔；填 - 清除）", "text", false), f("block-domains", "拒绝域名（逗号分隔；填 - 清除）", "text", false), f("block-ports", "拒绝端口（逗号分隔；填 - 清除）", "text", false), f("max-connections", "最大活跃连接数（0 不限）", "number", false), selectField("connection-action", "超过连接数时", "alert", "disable-device", "disable-user")}
	add("user.access", "访问与并发规则", "user", "policy user", saved, false, []string{"user"}, append([]webField{user}, policyFields...)...)
	add("device.access", "设备访问规则", "device", "policy device", saved, false, []string{"user", "device"}, append([]webField{user, device}, policyFields...)...)
	add("user.reset", "重置本期用量", "user", "traffic reset", saved, true, []string{"user"}, user)
	add("user.batch", "批量修改用户", "users", "batch", "所选用户已原子更新；任一校验失败则全部取消。应用配置后生效。", true, nil, source("users", "选择用户（可多选）", "users", true), selectField("enabled", "启用状态", "true", "false"), quota, selectField("quota-mode", "计费方向", "total", "upload", "download"), f("expire", "到期日（填 - 清除）", "text", false))
	specs[len(specs)-1].Fields[0].Type = "multi-select"
	add("client.set", "本机客户端地址", "subscriptions", "client set", "订阅立即使用新地址；监听和 Reality 密钥保持原配置。", false, nil, f("server", "客户端连接地址", "text", true), f("port", "客户端连接端口", "number", true))
	add("proxy.replace", "替换出站配置", "proxy", "", "基础模板已校验并保存；应用配置后生效。", true, nil, selectField("kind", "类型", "outbound", "endpoint"), f("tag", "原出站或端点 tag", "text", true), f("json", "完整的新 JSON（凭据不会回显）", "secret-text", true))
	add("fleet.add", "登记监控服务器", "ops", "fleet add", "服务器已登记，可运行远端状态检查。", false, nil, f("name", "名称", "text", true), f("host", "SSH 地址", "text", true), f("port", "SSH 端口", "number", false), f("user", "SSH 用户", "text", false), f("key", "私钥绝对路径", "text", true), f("app-dir", "远端应用目录", "text", false))
	add("fleet.check", "检查远端状态", "ops", "fleet check", "远端状态检查完成。", false, nil)
	add("fleet.remove", "移除监控服务器", "fleet", "fleet remove", "已移除监控服务器。", true, []string{"name"}, f("name", "名称", "text", true))
	add("mesh.entry", "设置客户端入口", "member", "", meshSaved, false, nil, source("id", "成员", "members", true), f("server", "客户端连接地址", "text", true), f("port", "端口", "number", true), f("server_name", "Reality servername", "text", true), f("reality_public_key", "Reality 公钥", "text", true), f("short_id", "Reality short-id", "text", false))
	return specs
}

func compileWebAction(input webActionInput) ([]string, webAction, error) {
	var spec webAction
	found := false
	for _, s := range webActions() {
		if s.ID == input.Action {
			spec = s
			found = true
			break
		}
	}
	if !found {
		return nil, spec, errors.New("未知管理操作")
	}
	if spec.Danger && !input.Confirm {
		return nil, spec, errors.New("请先确认这项修改的影响")
	}
	known := map[string]webField{}
	for _, f := range spec.Fields {
		known[f.Key] = f
	}
	for key, v := range input.Fields {
		f, ok := known[key]
		if !ok {
			return nil, spec, fmt.Errorf("不接受字段 %q", key)
		}
		if len(v) > 131072 || strings.ContainsRune(v, 0) {
			return nil, spec, errors.New("字段内容过长或无效")
		}
		if len(f.Options) > 0 && v != "" {
			match := false
			for _, o := range f.Options {
				match = match || v == o
			}
			if !match {
				return nil, spec, fmt.Errorf("%s选项无效", f.Label)
			}
		}
		if f.Type == "checkbox" && v != "true" && v != "false" {
			return nil, spec, errors.New("开关字段必须是 true 或 false")
		}
	}
	for _, f := range spec.Fields {
		if f.Required && strings.TrimSpace(input.Fields[f.Key]) == "" {
			return nil, spec, fmt.Errorf("请填写%s", f.Label)
		}
	}
	args := append([]string{}, spec.command...)
	positional := map[string]bool{}
	for _, key := range spec.positions {
		value := input.Fields[key]
		if strings.TrimSpace(value) == "" || strings.HasPrefix(value, "-") {
			return nil, spec, errors.New("操作对象不能为空或以减号开头")
		}
		args = append(args, value)
		positional[key] = true
	}
	for _, f := range spec.Fields {
		if positional[f.Key] {
			continue
		}
		value, ok := input.Fields[f.Key]
		if ok {
			if f.Key == "quota" || f.Key == "extra-quota" || f.Key == "burst-limit" {
				value = normalizeQuotaInput(value)
			}
			if value == "-" && (f.Key == "allow-domains" || f.Key == "block-domains" || f.Key == "block-ports" || f.Key == "ip-allowed" || f.Key == "ip-temp" || (input.Action == "user.batch" && f.Key == "expire")) {
				value = ""
			}
			args = append(args, "--"+f.Key+"="+value)
		}
	}
	return args, spec, nil
}

func safeWebActionError(err error, input webActionInput) string {
	if strings.HasPrefix(input.Action, "proxy.") {
		return "出站修改失败：请检查 JSON、字段类型和线路引用；原模板保持不变。"
	}
	message := err.Error()
	for key, value := range input.Fields {
		if value != "" && (strings.Contains(key, "password") || strings.Contains(key, "secret") || key == "json") {
			message = strings.ReplaceAll(message, value, "[已隐藏]")
		}
	}
	if len(message) > 1000 {
		message = message[:1000]
	}
	return safeTerminalText(message)
}

func (a *app) executeWebAction(input webActionInput, args []string) error {
	if strings.HasPrefix(input.Action, "user.") || strings.HasPrefix(input.Action, "device.") || strings.HasPrefix(input.Action, "node.") {
		return a.withStateLock(func() error { return a.executeWebActionLocked(input, args) })
	}
	return a.executeWebActionLocked(input, args)
}

func (a *app) executeWebActionLocked(input webActionInput, args []string) error {
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	if webSlaveMutation(s, input.Action) {
		return errors.New("从机用户授权由主机统一管理")
	}
	if input.Action == "proxy.add" || input.Action == "proxy.replace" {
		kind, err := parseProxyAdminKind(input.Fields["kind"])
		if err != nil {
			return err
		}
		if input.Action == "proxy.replace" {
			_, err = a.replaceManagedProxyJSON(kind, input.Fields["tag"], []byte(input.Fields["json"]), false)
			return err
		}
		_, err = a.addManagedProxyJSON(kind, []byte(input.Fields["json"]), false)
		return err
	}
	if input.Action == "mesh.entry" {
		port, err := strconv.Atoi(input.Fields["port"])
		if err != nil {
			return errors.New("端口必须为整数")
		}
		client := mesh.Client{Server: input.Fields["server"], Port: port, ServerName: input.Fields["server_name"], PublicKey: input.Fields["reality_public_key"], ShortID: input.Fields["short_id"]}
		if err := client.Validate(); err != nil {
			return err
		}
		data, _ := json.Marshal(client)
		file, err := os.CreateTemp(filepath.Dir(a.statePath), ".web-entry-*.json")
		if err != nil {
			return err
		}
		defer os.Remove(file.Name())
		_, err = file.Write(data)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		return a.meshCmd([]string{"entry", "--id=" + input.Fields["id"], "--file=" + file.Name()})
	}
	return a.adminCmd(args)
}
