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

func normalizedAccessPolicy(policy AccessPolicy) AccessPolicy {
	if policy.ConnectionAction == "" {
		policy.ConnectionAction = "alert"
	}
	return policy
}

func validateAccessPolicy(policy AccessPolicy) error {
	policy = normalizedAccessPolicy(policy)
	if policy.MaxConnections < 0 || policy.MaxConnections > 100000 {
		return errors.New("最大并发连接必须在 0–100000 之间；0 表示不限制")
	}
	if policy.ConnectionAction != "alert" && policy.ConnectionAction != "disable-device" && policy.ConnectionAction != "disable-user" {
		return errors.New("并发连接动作必须是 alert、disable-device 或 disable-user")
	}
	for _, domain := range append(append([]string(nil), policy.AllowedDomains...), policy.BlockedDomains...) {
		if domain == "" || strings.ContainsAny(domain, " /\\") {
			return fmt.Errorf("无效域名规则 %q", domain)
		}
	}
	for _, port := range policy.BlockedPorts {
		if port < 1 || port > 65535 {
			return fmt.Errorf("无效端口 %d", port)
		}
	}
	if policy.LastConnectionAlert != "" {
		if _, err := time.Parse(time.RFC3339Nano, policy.LastConnectionAlert); err != nil {
			return errors.New("无效并发连接告警时间")
		}
	}
	return nil
}

func parseDomainList(value string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, item := range strings.Split(value, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		item = strings.TrimPrefix(item, "*.")
		item = strings.TrimPrefix(item, ".")
		if item != "" && !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	sort.Strings(result)
	return result
}

func parsePortList(value string) ([]int, error) {
	seen := map[int]bool{}
	result := []int{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		port, err := strconv.Atoi(item)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("无效端口 %q", item)
		}
		if !seen[port] {
			seen[port] = true
			result = append(result, port)
		}
	}
	sort.Ints(result)
	return result, nil
}

func accessRestrictionRules(s *State, now time.Time) []any {
	var rules []any
	for _, u := range s.Users {
		if !u.Enabled || expired(u, now) || overQuota(u) || burstHardBlocked(u, now) {
			continue
		}
		for _, device := range u.Devices {
			if !device.Enabled {
				continue
			}
			authUsers := []any{}
			for _, node := range u.Nodes {
				if strings.EqualFold(node.Device, device.Name) {
					authUsers = append(authUsers, node.AuthUser)
				}
			}
			rules = appendAccessPolicyRules(rules, authUsers, device.Access)
		}
		authUsers := []any{}
		for _, node := range u.Nodes {
			if deviceEnabled(u, node.Device) {
				authUsers = append(authUsers, node.AuthUser)
			}
		}
		rules = appendAccessPolicyRules(rules, authUsers, u.Access)
	}
	return rules
}

func appendAccessPolicyRules(rules []any, authUsers []any, policy AccessPolicy) []any {
	if len(authUsers) == 0 {
		return rules
	}
	if len(policy.AllowedDomains) > 0 {
		rules = append(rules, map[string]any{
			"type": "logical", "mode": "and", "action": "reject", "method": "drop",
			"rules": []any{map[string]any{"auth_user": authUsers}, map[string]any{"domain_suffix": policy.AllowedDomains, "invert": true}},
		})
	}
	if len(policy.BlockedDomains) > 0 {
		rules = append(rules, map[string]any{"auth_user": authUsers, "domain_suffix": policy.BlockedDomains, "action": "reject", "method": "drop"})
	}
	if len(policy.BlockedPorts) > 0 {
		rules = append(rules, map[string]any{"auth_user": authUsers, "port": policy.BlockedPorts, "action": "reject", "method": "drop"})
	}
	return rules
}

func evaluateConnectionPolicies(s *State, now time.Time) (stateChanged, configChanged bool) {
	userCounts := map[string]int{}
	deviceCounts := map[string]int{}
	for _, connection := range s.ActiveConnections {
		if !connectionActiveAt(connection, now) {
			continue
		}
		userCounts[strings.ToLower(connection.User)]++
		deviceCounts[strings.ToLower(connection.User)+"\x00"+strings.ToLower(connection.Device)]++
	}
	for userIndex := range s.Users {
		u := &s.Users[userIndex]
		if evaluateConnectionPolicy(s, u, nil, &u.Access, userCounts[strings.ToLower(u.Name)], now) {
			stateChanged = true
			if !u.Enabled {
				configChanged = true
			}
		}
		for deviceIndex := range u.Devices {
			device := &u.Devices[deviceIndex]
			count := deviceCounts[strings.ToLower(u.Name)+"\x00"+strings.ToLower(device.Name)]
			wasEnabled := device.Enabled
			if evaluateConnectionPolicy(s, u, device, &device.Access, count, now) {
				stateChanged = true
				if wasEnabled != device.Enabled || !u.Enabled {
					configChanged = true
				}
			}
		}
	}
	return stateChanged, configChanged
}

func evaluateConnectionPolicy(s *State, u *User, device *Device, policy *AccessPolicy, count int, now time.Time) bool {
	normalized := normalizedAccessPolicy(*policy)
	if normalized.MaxConnections <= 0 || count <= normalized.MaxConnections {
		return false
	}
	last, _ := time.Parse(time.RFC3339Nano, normalized.LastConnectionAlert)
	if normalized.LastConnectionAlert != "" && now.Sub(last) < 10*time.Minute {
		return false
	}
	scope := u.Name
	if device != nil {
		scope += "/" + device.Name
	}
	actionText := "已告警"
	switch normalized.ConnectionAction {
	case "disable-user":
		u.Enabled = false
		u.DisabledReason = "connections"
		actionText = "已禁用用户"
	case "disable-device":
		if device != nil {
			device.Enabled = false
			actionText = "已禁用设备"
		}
	}
	normalized.LastConnectionAlert = now.Format(time.RFC3339Nano)
	*policy = normalized
	appendAlert(s, Alert{At: now.Format(time.RFC3339), User: u.Name, Kind: "connection_limit", Message: fmt.Sprintf("%s 当前识别到 %d 个活跃连接，超过上限 %d，%s", scope, count, normalized.MaxConnections, actionText)})
	return true
}

func (a *app) policyCmd(args []string) error {
	return a.withAuditedStateLock(auditAction("policy", args), args, func() error { return a.policyCmdLocked(args) })
}

func (a *app) policyCmdLocked(args []string) error {
	if len(args) < 2 {
		return errors.New("用法: sbmgr admin policy user USER | device USER DEVICE [规则参数]")
	}
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	u := findUser(s, args[1])
	if u == nil {
		return fmt.Errorf("用户 %q 不存在", args[1])
	}
	var policy *AccessPolicy
	flagStart := 2
	scope := u.Name
	switch args[0] {
	case "user":
		policy = &u.Access
	case "device":
		if len(args) < 3 {
			return errors.New("缺少设备名")
		}
		device := findDevice(u, args[2])
		if device == nil {
			return fmt.Errorf("设备 %q 不存在", args[2])
		}
		policy = &device.Access
		flagStart = 3
		scope += "/" + device.Name
	default:
		return fmt.Errorf("未知策略范围 %q", args[0])
	}
	fs := a.newFlagSet("policy " + args[0])
	allowed := fs.String("allow-domains", "", "允许域名后缀，逗号分隔；非空时其余域名拒绝")
	blocked := fs.String("block-domains", "", "拒绝域名后缀，逗号分隔")
	ports := fs.String("block-ports", "", "拒绝目标端口，逗号分隔")
	maxConnections := fs.Int("max-connections", 0, "最大识别活跃连接数；0 不限制")
	action := fs.String("connection-action", "alert", "alert/disable-device/disable-user")
	if err := fs.Parse(args[flagStart:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("访问策略不接受额外位置参数")
	}
	updated := *policy
	changed := false
	fs.Visit(func(current *flag.Flag) {
		changed = true
		switch current.Name {
		case "allow-domains":
			updated.AllowedDomains = parseDomainList(*allowed)
		case "block-domains":
			updated.BlockedDomains = parseDomainList(*blocked)
		case "block-ports":
			updated.BlockedPorts, err = parsePortList(*ports)
		case "max-connections":
			updated.MaxConnections = *maxConnections
		case "connection-action":
			updated.ConnectionAction = strings.ToLower(strings.TrimSpace(*action))
		}
	})
	if err != nil {
		return err
	}
	if !changed {
		return errors.New("没有指定要修改的访问策略")
	}
	if err := validateAccessPolicy(updated); err != nil {
		return err
	}
	*policy = updated
	if err := saveState(a.statePath, s); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "已更新 %s 的访问与并发策略（按 p 应用域名/端口规则）\n", scope)
	return nil
}
