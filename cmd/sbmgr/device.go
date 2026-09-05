package main

import (
	"errors"
	"flag"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultDeviceName = "默认设备"

func normalizeDeviceModel(s *State) {
	for i := range s.Users {
		u := &s.Users[i]
		if len(u.Devices) == 0 {
			u.Devices = []Device{{Name: defaultDeviceName, Enabled: true}}
		}
		known := map[string]bool{}
		for j := range u.Devices {
			if strings.TrimSpace(u.Devices[j].Name) == "" {
				u.Devices[j].Name = defaultDeviceName
			}
			known[strings.ToLower(u.Devices[j].Name)] = true
			if u.Devices[j].SubscriptionToken == "" {
				u.Devices[j].SubscriptionToken = newSubscriptionToken()
			}
		}
		fallback := u.Devices[0].Name
		for j := range u.Nodes {
			if strings.TrimSpace(u.Nodes[j].Device) == "" || !known[strings.ToLower(u.Nodes[j].Device)] {
				u.Nodes[j].Device = fallback
			}
		}
	}
}

func findDevice(u *User, name string) *Device {
	if u == nil {
		return nil
	}
	for i := range u.Devices {
		if strings.EqualFold(u.Devices[i].Name, strings.TrimSpace(name)) {
			return &u.Devices[i]
		}
	}
	return nil
}

func deviceEnabled(u User, deviceName string) bool {
	for _, device := range u.Devices {
		if strings.EqualFold(device.Name, deviceName) {
			return device.Enabled
		}
	}
	return false
}

func enabledDeviceNames(u User) []string {
	var result []string
	for _, device := range u.Devices {
		if device.Enabled {
			result = append(result, device.Name)
		}
	}
	sort.Strings(result)
	return result
}

func deviceNames(u User) []string {
	result := make([]string, 0, len(u.Devices))
	for _, device := range u.Devices {
		result = append(result, device.Name)
	}
	return result
}

func deviceTraffic(u User, deviceName string) (upload, download int64) {
	for _, node := range u.Nodes {
		if strings.EqualFold(node.Device, deviceName) {
			upload += node.Upload
			download += node.Download
		}
	}
	return upload, download
}

func deviceCurrentRate(u User, deviceName string) (upload, download float64) {
	for _, node := range u.Nodes {
		if strings.EqualFold(node.Device, deviceName) {
			upload += node.CurrentUploadMbps
			download += node.CurrentDownloadMbps
		}
	}
	return upload, download
}

func nodesForDevice(u User, deviceName string) []Node {
	var result []Node
	for _, node := range u.Nodes {
		if strings.EqualFold(node.Device, deviceName) {
			result = append(result, node)
		}
	}
	return result
}

func findUserNode(u *User, deviceName, nodeName string) (*Node, error) {
	if u == nil {
		return nil, errors.New("用户不存在")
	}
	var matches []*Node
	for i := range u.Nodes {
		node := &u.Nodes[i]
		if !strings.EqualFold(node.Name, strings.TrimSpace(nodeName)) {
			continue
		}
		if strings.TrimSpace(deviceName) != "" && !strings.EqualFold(node.Device, strings.TrimSpace(deviceName)) {
			continue
		}
		matches = append(matches, node)
	}
	if len(matches) == 0 {
		if deviceName == "" {
			return nil, fmt.Errorf("节点 %q 不存在", nodeName)
		}
		return nil, fmt.Errorf("设备 %q 中的节点 %q 不存在", deviceName, nodeName)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("节点 %q 存在于多台设备，请用 --device 指定", nodeName)
	}
	return matches[0], nil
}

func activeUserDevices(u User) User {
	copyUser := u
	copyUser.Nodes = nil
	for _, node := range u.Nodes {
		if deviceEnabled(u, node.Device) {
			copyUser.Nodes = append(copyUser.Nodes, node)
		}
	}
	return copyUser
}

func deviceNodeLabel(userName, deviceName, nodeName string) string {
	if strings.EqualFold(deviceName, defaultDeviceName) || strings.TrimSpace(deviceName) == "" {
		return userName + "/" + nodeName
	}
	return userName + "/" + deviceName + "/" + nodeName
}

func (a *app) deviceCmd(args []string) error {
	return a.withAuditedStateLock(auditAction("device", args), args, func() error { return a.deviceCmdLocked(args) })
}

func (a *app) deviceCmdLocked(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: sbmgr admin device add|list|set|enable|disable|rotate|delete USER")
	}
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	if len(args) < 2 {
		return errors.New("缺少用户名")
	}
	u := findUser(s, args[1])
	if u == nil {
		return fmt.Errorf("用户 %q 不存在", args[1])
	}
	switch args[0] {
	case "list":
		if len(args) != 2 {
			return errors.New("用法: sbmgr admin device list USER")
		}
		fmt.Fprintln(a.out, "NAME\tSTATUS\tNODES\tLAST_SEEN")
		for _, device := range u.Devices {
			status := "enabled"
			if !device.Enabled {
				status = "disabled"
			}
			fmt.Fprintf(a.out, "%s\t%s\t%d\t%s\n", device.Name, status, len(nodesForDevice(*u, device.Name)), dash(device.LastSeen))
		}
		return nil
	case "add":
		fs := a.newFlagSet("device add")
		name := fs.String("name", "", "设备名称")
		from := fs.String("from", "", "复制哪个现有设备的节点；留空复制第一个设备")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 0 || strings.TrimSpace(*name) == "" {
			return errors.New("用法: sbmgr admin device add USER --name NAME [--from DEVICE]")
		}
		if findDevice(u, *name) != nil {
			return fmt.Errorf("设备 %q 已存在", *name)
		}
		sourceName := *from
		if sourceName == "" {
			sourceName = u.Devices[0].Name
		}
		if findDevice(u, sourceName) == nil {
			return fmt.Errorf("模板设备 %q 不存在", sourceName)
		}
		u.Devices = append(u.Devices, Device{Name: strings.TrimSpace(*name), Enabled: true, CreatedAt: time.Now().Format(time.RFC3339), SubscriptionToken: newSubscriptionToken()})
		for _, source := range nodesForDevice(*u, sourceName) {
			mark, err := allocateRateMark(s)
			if err != nil {
				return err
			}
			node := Node{
				Name: source.Name, Device: strings.TrimSpace(*name), AuthUser: uniqueAuthUser(s, u.Name+"-"+slug(*name)+"-"+slug(source.Name)), UUID: newUUID(),
				Outbound: source.Outbound, UploadMbps: source.UploadMbps, DownloadMbps: source.DownloadMbps, RateMark: mark,
			}
			u.Nodes = append(u.Nodes, node)
		}
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "已为用户 %s 添加设备 %s，并生成 %d 个独立 UUID\n", u.Name, *name, len(nodesForDevice(*u, *name)))
		return nil
	case "set":
		fs := a.newFlagSet("device set")
		ipEnabled := fs.String("ip-enabled", "", "来源 IP 规则开关: true/false")
		ipMode := fs.String("ip-mode", "", "来源 IP 模式: enforce/monitor")
		ipBinding := fs.String("ip-binding", "", "绑定方式: dynamic/auto/manual")
		ipMax := fs.Int("ip-max", 0, "最多允许的来源 IP 数量")
		ipHandoverSeconds := fs.Int("ip-handover-seconds", 0, "动态单活换绑宽限秒数")
		ipAllowed := fs.String("ip-allowed", "", "固定允许 IP，逗号分隔")
		ipTemp := fs.String("ip-temp", "", "临时替代 IP，逗号分隔；留空清除")
		ipTempMinutes := fs.Int("ip-temp-minutes", 0, "临时 IP 有效分钟数")
		if len(args) < 3 {
			return errors.New("用法: sbmgr admin device set USER DEVICE [IP 规则参数]")
		}
		if err := fs.Parse(args[3:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("用法: sbmgr admin device set USER DEVICE [IP 规则参数]")
		}
		device := findDevice(u, args[2])
		if device == nil {
			return fmt.Errorf("设备 %q 不存在", args[2])
		}
		oldPolicy := normalizedIPPolicy(device.IPPolicy)
		policy := oldPolicy
		changed := false
		tempSet, tempMinutesSet := false, false
		var parseErr error
		fs.Visit(func(f *flag.Flag) {
			changed = true
			switch f.Name {
			case "ip-enabled":
				policy.Enabled, parseErr = strconv.ParseBool(*ipEnabled)
			case "ip-mode":
				policy.Mode = strings.ToLower(strings.TrimSpace(*ipMode))
			case "ip-binding":
				policy.Binding = strings.ToLower(strings.TrimSpace(*ipBinding))
			case "ip-max":
				policy.MaxIPs = *ipMax
			case "ip-handover-seconds":
				policy.HandoverSeconds = *ipHandoverSeconds
			case "ip-allowed":
				policy.BoundIPs, parseErr = parseIPList(*ipAllowed)
			case "ip-temp":
				tempSet = true
				policy.TemporaryIPs, parseErr = parseIPList(*ipTemp)
			case "ip-temp-minutes":
				tempMinutesSet = true
			}
		})
		if parseErr != nil {
			return parseErr
		}
		if !changed {
			return errors.New("没有指定要修改的设备规则")
		}
		if tempSet {
			if len(policy.TemporaryIPs) == 0 {
				policy.TemporaryUntil = ""
			} else {
				if *ipTempMinutes <= 0 {
					return errors.New("设置临时 IP 时，临时分钟数必须大于 0")
				}
				policy.TemporaryUntil = time.Now().Add(time.Duration(*ipTempMinutes) * time.Minute).Format(time.RFC3339Nano)
			}
		} else if tempMinutesSet {
			if len(policy.TemporaryIPs) == 0 || *ipTempMinutes <= 0 {
				return errors.New("延长临时 IP 时必须已有临时 IP，且分钟数大于 0")
			}
			policy.TemporaryUntil = time.Now().Add(time.Duration(*ipTempMinutes) * time.Minute).Format(time.RFC3339Nano)
		}
		if policy.Enabled && policy.Binding == "dynamic" && len(policy.BoundIPs) == 0 && len(policy.TemporaryIPs) == 0 {
			if active := activeSourceIPs(s, u.Name, device.Name); len(active) == 1 {
				policy.BoundIPs = active
			}
		}
		if err := validateIPPolicy(policy); err != nil {
			return err
		}
		device.IPPolicy = policy
		if ipPolicyRuleSignature(oldPolicy, time.Now()) != ipPolicyRuleSignature(policy, time.Now()) {
			s.IPApplyPending = true
		}
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "已更新设备 %s/%s 的来源 IP 规则，后台下个维护周期自动应用\n", u.Name, device.Name)
		return nil
	case "enable", "disable":
		if len(args) != 3 {
			return fmt.Errorf("用法: sbmgr admin device %s USER DEVICE", args[0])
		}
		device := findDevice(u, args[2])
		if device == nil {
			return fmt.Errorf("设备 %q 不存在", args[2])
		}
		device.Enabled = args[0] == "enable"
		device.Access.ConnectionBlockedUntil = ""
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "设备 %s/%s 已%s（按 p 应用配置后生效）\n", u.Name, device.Name, map[bool]string{true: "启用", false: "禁用"}[device.Enabled])
		return nil
	case "rotate":
		if len(args) != 3 {
			return errors.New("用法: sbmgr admin device rotate USER DEVICE")
		}
		device := findDevice(u, args[2])
		if device == nil {
			return fmt.Errorf("设备 %q 不存在", args[2])
		}
		count := 0
		for i := range u.Nodes {
			if strings.EqualFold(u.Nodes[i].Device, device.Name) {
				u.Nodes[i].UUID = newUUID()
				count++
			}
		}
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "已轮换设备 %s/%s 的 %d 个 UUID（重新导出并应用配置）\n", u.Name, device.Name, count)
		return nil
	case "rotate-link":
		if len(args) != 3 {
			return errors.New("用法: sbmgr admin device rotate-link USER DEVICE")
		}
		device := findDevice(u, args[2])
		if device == nil {
			return fmt.Errorf("设备 %q 不存在", args[2])
		}
		device.SubscriptionToken = newSubscriptionToken()
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "已轮换设备 %s/%s 的订阅 token，旧链接立即失效\n", u.Name, device.Name)
		return nil
	case "delete":
		if len(args) != 3 {
			return errors.New("用法: sbmgr admin device delete USER DEVICE")
		}
		if len(u.Devices) <= 1 {
			return errors.New("每个用户至少保留一个设备")
		}
		index := -1
		for i := range u.Devices {
			if strings.EqualFold(u.Devices[i].Name, args[2]) {
				index = i
				break
			}
		}
		if index < 0 {
			return fmt.Errorf("设备 %q 不存在", args[2])
		}
		name := u.Devices[index].Name
		u.Devices = append(u.Devices[:index], u.Devices[index+1:]...)
		kept := u.Nodes[:0]
		for _, node := range u.Nodes {
			if !strings.EqualFold(node.Device, name) {
				kept = append(kept, node)
			}
		}
		u.Nodes = kept
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "已删除设备 %s/%s 及其全部 UUID（按 p 应用配置后生效）\n", u.Name, name)
		return nil
	default:
		return fmt.Errorf("未知 device 子命令 %q", args[0])
	}
}
