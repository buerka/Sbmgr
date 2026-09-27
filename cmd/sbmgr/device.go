package main

import (
	"errors"
	"fmt"
	"sort"
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
		if u.DeviceLimit > 0 && len(u.Devices) >= u.DeviceLimit {
			return errors.New("设备名额已用完，请先增加用户的设备名额")
		}
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
		fmt.Fprintf(a.out, "设备 %s/%s 已%s（运行 admin apply 应用配置后生效）\n", u.Name, device.Name, map[bool]string{true: "启用", false: "禁用"}[device.Enabled])
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
		if err := requireDeviceCredentialRotationReady(s); err != nil {
			return err
		}
		count, err := rotateDeviceCredentials(s, u, args[2])
		if err != nil {
			return err
		}
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "已重置设备 %s/%s 的订阅及 %d 个节点连接凭据；旧链接立即失效，旧配置须待各入口自动应用后失效，请重新导入订阅\n", u.Name, args[2], count)
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
		fmt.Fprintf(a.out, "已删除设备 %s/%s 及其全部 UUID（运行 admin apply 应用配置后生效）\n", u.Name, name)
		return nil
	default:
		return fmt.Errorf("未知 device 子命令 %q", args[0])
	}
}
