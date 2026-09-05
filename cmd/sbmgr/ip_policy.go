package main

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

const (
	sourceIPArchiveLimit           = 100
	defaultIPPolicyHandoverSeconds = 60
	maxIPPolicyHandoverSeconds     = 3600
)

func normalizedIPPolicy(policy IPPolicy) IPPolicy {
	if policy.Mode == "" {
		policy.Mode = "enforce"
	}
	if policy.Binding == "" {
		policy.Binding = "auto"
	}
	if policy.MaxIPs == 0 {
		policy.MaxIPs = 1
	}
	if policy.HandoverSeconds == 0 {
		policy.HandoverSeconds = defaultIPPolicyHandoverSeconds
	}
	return policy
}

func parseIPList(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	seen := map[string]bool{}
	var result []string
	for _, item := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' }) {
		ip := net.ParseIP(strings.TrimSpace(item))
		if ip == nil {
			return nil, fmt.Errorf("无效 IP 地址 %q", item)
		}
		normalized := ip.String()
		if !seen[normalized] {
			seen[normalized] = true
			result = append(result, normalized)
		}
	}
	sort.Strings(result)
	return result, nil
}

func validateIPPolicy(policy IPPolicy) error {
	policy = normalizedIPPolicy(policy)
	if policy.Mode != "enforce" && policy.Mode != "monitor" {
		return errors.New("IP 策略模式必须是 enforce 或 monitor")
	}
	if policy.Binding != "auto" && policy.Binding != "manual" && policy.Binding != "dynamic" {
		return errors.New("IP 绑定方式必须是 dynamic、auto 或 manual")
	}
	if policy.MaxIPs <= 0 {
		return errors.New("允许 IP 数量必须大于 0")
	}
	if policy.Binding == "dynamic" && policy.MaxIPs != 1 {
		return errors.New("动态单活模式的允许 IP 数量必须是 1")
	}
	if policy.HandoverSeconds < 1 || policy.HandoverSeconds > maxIPPolicyHandoverSeconds {
		return fmt.Errorf("动态单活换绑宽限必须在 1–%d 秒之间", maxIPPolicyHandoverSeconds)
	}
	if len(policy.BoundIPs) > policy.MaxIPs {
		return fmt.Errorf("固定 IP 有 %d 个，超过允许数量 %d", len(policy.BoundIPs), policy.MaxIPs)
	}
	if len(policy.TemporaryIPs) > policy.MaxIPs {
		return fmt.Errorf("临时 IP 有 %d 个，超过允许数量 %d", len(policy.TemporaryIPs), policy.MaxIPs)
	}
	for _, values := range [][]string{policy.BoundIPs, policy.TemporaryIPs} {
		for _, value := range values {
			if net.ParseIP(value) == nil {
				return fmt.Errorf("无效 IP 地址 %q", value)
			}
		}
	}
	if len(policy.TemporaryIPs) > 0 {
		if _, err := time.Parse(time.RFC3339Nano, policy.TemporaryUntil); err != nil {
			return errors.New("临时 IP 缺少有效的到期时间")
		}
	}
	for ip, last := range policy.BoundLastSeen {
		if net.ParseIP(ip) == nil {
			return errors.New("绑定活动记录包含无效 IP")
		}
		if _, err := time.Parse(time.RFC3339Nano, last); err != nil {
			return errors.New("绑定活动记录包含无效时间")
		}
	}
	return nil
}

func ipPolicyRuleSignature(policy IPPolicy, now time.Time) string {
	policy = normalizedIPPolicy(policy)
	if !policy.Enabled || policy.Mode != "enforce" {
		return ""
	}
	allowed := activePolicyIPs(policy, now)
	sort.Strings(allowed)
	if len(allowed) == 0 {
		if policy.Binding == "auto" || policy.Binding == "dynamic" {
			return ""
		}
		return "reject-all"
	}
	return "allow:" + strings.Join(allowed, ",")
}

func activePolicyIPs(policy IPPolicy, now time.Time) []string {
	if len(policy.TemporaryIPs) > 0 {
		until, err := time.Parse(time.RFC3339Nano, policy.TemporaryUntil)
		if err == nil && now.Before(until) {
			return append([]string(nil), policy.TemporaryIPs...)
		}
	}
	return append([]string(nil), policy.BoundIPs...)
}

func containsIP(values []string, ip string) bool {
	normalized := net.ParseIP(ip)
	if normalized == nil {
		return false
	}
	for _, value := range values {
		if parsed := net.ParseIP(value); parsed != nil && parsed.Equal(normalized) {
			return true
		}
	}
	return false
}

func activeSourceIPs(s *State, userName, deviceName string) []string {
	return activeSourceIPsAt(s, userName, deviceName, time.Now())
}

func activeSourceIPsAt(s *State, userName, deviceName string, now time.Time) []string {
	seen := map[string]bool{}
	for _, connection := range s.ActiveConnections {
		if !connectionActiveAt(connection, now) {
			continue
		}
		if !strings.EqualFold(connection.User, userName) {
			continue
		}
		if deviceName != "" && !strings.EqualFold(connection.Device, deviceName) {
			continue
		}
		if parsed := net.ParseIP(connection.SourceIP); parsed != nil {
			seen[parsed.String()] = true
		}
	}
	result := make([]string, 0, len(seen))
	for ip := range seen {
		result = append(result, ip)
	}
	sort.Strings(result)
	return result
}

// hasCompetingActiveBoundIP reports whether the currently bound address still
// has recent activity.  Connections from other, unbound addresses must not
// participate in the decision: those connections may have been rejected by
// the generated source-IP rule, and counting them lets two rejected candidates
// keep each other from ever taking over after the incumbent goes away.
func hasCompetingActiveBoundIP(s *State, userName, deviceName, candidateIP string, policy IPPolicy, now time.Time) bool {
	if net.ParseIP(candidateIP) == nil {
		return false
	}
	policy = normalizedIPPolicy(policy)
	grace := time.Duration(policy.HandoverSeconds) * time.Second
	for _, ip := range policy.BoundIPs {
		if containsIP([]string{candidateIP}, ip) {
			continue
		}
		if last, err := time.Parse(time.RFC3339Nano, policy.BoundLastSeen[ip]); err == nil && now.Sub(last) <= grace {
			return true
		}
	}
	for _, connection := range s.ActiveConnections {
		if !connectionActiveWithinAt(connection, now, grace) {
			continue
		}
		if !strings.EqualFold(connection.User, userName) {
			continue
		}
		if deviceName != "" && !strings.EqualFold(connection.Device, deviceName) {
			continue
		}
		ip := connection.SourceIP
		if !containsIP([]string{candidateIP}, ip) && containsIP(policy.BoundIPs, ip) {
			return true
		}
	}
	return false
}

func hasEnforcedDynamicIPPolicy(s *State, now time.Time) bool {
	if s == nil {
		return false
	}
	for _, u := range s.Users {
		if !u.Enabled || expired(u, now) || overQuota(u) || burstHardBlocked(u, now) {
			continue
		}
		policy := normalizedIPPolicy(u.IPPolicy)
		if policy.Enabled && policy.Mode == "enforce" && policy.Binding == "dynamic" {
			return true
		}
		for _, device := range u.Devices {
			policy = normalizedIPPolicy(device.IPPolicy)
			if device.Enabled && policy.Enabled && policy.Mode == "enforce" && policy.Binding == "dynamic" {
				return true
			}
		}
	}
	return false
}

func recordUserSourceIP(s *State, u *User, nodeName, ip string, now time.Time) bool {
	if u == nil || net.ParseIP(ip) == nil {
		return false
	}
	ip = net.ParseIP(ip).String()
	if u.SourceIPs == nil {
		u.SourceIPs = map[string]SourceIPStat{}
	}
	stat := u.SourceIPs[ip]
	if stat.FirstSeen == "" {
		stat.FirstSeen = now.Format(time.RFC3339)
	}
	stat.Count++
	stat.LastSeen = now.Format(time.RFC3339)
	stat.LastNode = nodeName

	policy := normalizedIPPolicy(u.IPPolicy)
	previousRuleSignature := ipPolicyRuleSignature(policy, now)
	if policy.Enabled {
		allowed := activePolicyIPs(policy, now)
		temporary := len(policy.TemporaryIPs) > 0 && len(allowed) > 0
		if !containsIP(allowed, ip) && policy.Binding == "auto" && !temporary && len(policy.BoundIPs) < policy.MaxIPs {
			policy.BoundIPs = append(policy.BoundIPs, ip)
			sort.Strings(policy.BoundIPs)
			u.IPPolicy = policy
			appendAlert(s, Alert{At: now.Format(time.RFC3339), User: u.Name, Kind: "ip_bound", Message: fmt.Sprintf("用户 %s 已自动绑定来源 IP %s（%s）", u.Name, ip, nodeName)})
			allowed = policy.BoundIPs
		}
		if !containsIP(allowed, ip) && policy.Binding == "dynamic" && !temporary && (len(policy.BoundIPs) == 0 || !hasCompetingActiveBoundIP(s, u.Name, "", ip, policy, now)) {
			previous := append([]string(nil), policy.BoundIPs...)
			policy.BoundIPs = []string{ip}
			u.IPPolicy = policy
			kind := "ip_bound"
			message := fmt.Sprintf("用户 %s 已启用动态单活 IP %s（%s）", u.Name, ip, nodeName)
			if len(previous) > 0 {
				kind = "ip_switched"
				message = fmt.Sprintf("用户 %s 的动态单活 IP 已从 %s 切换为 %s（%s）", u.Name, strings.Join(previous, ","), ip, nodeName)
			}
			appendAlert(s, Alert{At: now.Format(time.RFC3339), User: u.Name, Kind: kind, Message: message})
			allowed = policy.BoundIPs
		}
		if !containsIP(allowed, ip) {
			stat.Violations++
			lastAlert, _ := time.Parse(time.RFC3339Nano, stat.LastAlert)
			if stat.LastAlert == "" || now.Sub(lastAlert) >= 10*time.Minute {
				action := "已记录告警"
				if policy.Mode == "enforce" {
					action = "已触发 sing-box 强制拒绝"
				}
				appendAlert(s, Alert{At: now.Format(time.RFC3339), User: u.Name, Kind: "ip_violation", Message: fmt.Sprintf("来源 IP 告警：用户 %s 从未授权 IP %s 使用节点 %s，%s", u.Name, ip, nodeName, action)})
				stat.LastAlert = now.Format(time.RFC3339Nano)
			}
		}
		if previousRuleSignature != ipPolicyRuleSignature(policy, now) {
			s.IPApplyPending = true
		}
	}
	if policy.Enabled && containsIP(policy.BoundIPs, ip) {
		rememberBoundActivity(&policy, ip, now)
		u.IPPolicy = policy
	}
	u.SourceIPs[ip] = stat
	trimSourceIPs(u.SourceIPs, sourceIPArchiveLimit)
	return true
}

func recordDeviceSourceIP(s *State, u *User, device *Device, nodeName, ip string, now time.Time) bool {
	if u == nil || device == nil || net.ParseIP(ip) == nil {
		return false
	}
	ip = net.ParseIP(ip).String()
	if device.SourceIPs == nil {
		device.SourceIPs = map[string]SourceIPStat{}
	}
	device.LastSeen = now.Format(time.RFC3339)
	stat := device.SourceIPs[ip]
	if stat.FirstSeen == "" {
		stat.FirstSeen = now.Format(time.RFC3339)
	}
	stat.Count++
	stat.LastSeen = now.Format(time.RFC3339)
	stat.LastNode = nodeName

	policy := normalizedIPPolicy(device.IPPolicy)
	previousRuleSignature := ipPolicyRuleSignature(policy, now)
	if policy.Enabled {
		allowed := activePolicyIPs(policy, now)
		temporary := len(policy.TemporaryIPs) > 0 && len(allowed) > 0
		if !containsIP(allowed, ip) && policy.Binding == "auto" && !temporary && len(policy.BoundIPs) < policy.MaxIPs {
			policy.BoundIPs = append(policy.BoundIPs, ip)
			sort.Strings(policy.BoundIPs)
			device.IPPolicy = policy
			appendAlert(s, Alert{At: now.Format(time.RFC3339), User: u.Name, Kind: "device_ip_bound", Message: fmt.Sprintf("设备 %s/%s 已自动绑定来源 IP %s（%s）", u.Name, device.Name, ip, nodeName)})
			allowed = policy.BoundIPs
		}
		if !containsIP(allowed, ip) && policy.Binding == "dynamic" && !temporary && (len(policy.BoundIPs) == 0 || !hasCompetingActiveBoundIP(s, u.Name, device.Name, ip, policy, now)) {
			previous := append([]string(nil), policy.BoundIPs...)
			policy.BoundIPs = []string{ip}
			device.IPPolicy = policy
			kind := "device_ip_bound"
			message := fmt.Sprintf("设备 %s/%s 已启用动态单活 IP %s（%s）", u.Name, device.Name, ip, nodeName)
			if len(previous) > 0 {
				kind = "device_ip_switched"
				message = fmt.Sprintf("设备 %s/%s 的动态单活 IP 已从 %s 切换为 %s（%s）", u.Name, device.Name, strings.Join(previous, ","), ip, nodeName)
			}
			appendAlert(s, Alert{At: now.Format(time.RFC3339), User: u.Name, Kind: kind, Message: message})
			allowed = policy.BoundIPs
		}
		if !containsIP(allowed, ip) {
			stat.Violations++
			lastAlert, _ := time.Parse(time.RFC3339Nano, stat.LastAlert)
			if stat.LastAlert == "" || now.Sub(lastAlert) >= 10*time.Minute {
				action := "已记录告警"
				if policy.Mode == "enforce" {
					action = "已触发 sing-box 强制拒绝"
				}
				appendAlert(s, Alert{At: now.Format(time.RFC3339), User: u.Name, Kind: "device_ip_violation", Message: fmt.Sprintf("来源 IP 告警：设备 %s/%s 从未授权 IP %s 使用节点 %s，%s", u.Name, device.Name, ip, nodeName, action)})
				stat.LastAlert = now.Format(time.RFC3339Nano)
			}
		}
		if previousRuleSignature != ipPolicyRuleSignature(policy, now) {
			s.IPApplyPending = true
		}
	}
	if policy.Enabled && containsIP(policy.BoundIPs, ip) {
		rememberBoundActivity(&policy, ip, now)
		device.IPPolicy = policy
	}
	device.SourceIPs[ip] = stat
	trimSourceIPs(device.SourceIPs, sourceIPArchiveLimit)
	return true
}

func trimSourceIPs(values map[string]SourceIPStat, limit int) {
	if len(values) <= limit {
		return
	}
	type item struct {
		ip   string
		stat SourceIPStat
	}
	items := make([]item, 0, len(values))
	for ip, stat := range values {
		items = append(items, item{ip: ip, stat: stat})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].stat.LastSeen > items[j].stat.LastSeen })
	keep := map[string]bool{}
	for _, item := range items[:limit] {
		keep[item.ip] = true
	}
	for ip := range values {
		if !keep[ip] {
			delete(values, ip)
		}
	}
}

func expireTemporaryIPPolicies(s *State, now time.Time) bool {
	changed := releaseIdleBindings(s, now)
	for i := range s.Users {
		u := &s.Users[i]
		if len(u.IPPolicy.TemporaryIPs) > 0 {
			until, err := time.Parse(time.RFC3339Nano, u.IPPolicy.TemporaryUntil)
			if err != nil || !now.Before(until) {
				u.IPPolicy.TemporaryIPs = nil
				u.IPPolicy.TemporaryUntil = ""
				s.IPApplyPending = true
				appendAlert(s, Alert{At: now.Format(time.RFC3339), User: u.Name, Kind: "ip_override_expired", Message: fmt.Sprintf("用户 %s 的临时来源 IP 已到期，已恢复固定绑定", u.Name)})
				changed = true
			}
		}
		for j := range u.Devices {
			device := &u.Devices[j]
			if len(device.IPPolicy.TemporaryIPs) == 0 {
				continue
			}
			until, err := time.Parse(time.RFC3339Nano, device.IPPolicy.TemporaryUntil)
			if err == nil && now.Before(until) {
				continue
			}
			device.IPPolicy.TemporaryIPs = nil
			device.IPPolicy.TemporaryUntil = ""
			s.IPApplyPending = true
			appendAlert(s, Alert{At: now.Format(time.RFC3339), User: u.Name, Kind: "device_ip_override_expired", Message: fmt.Sprintf("设备 %s/%s 的临时来源 IP 已到期，已恢复固定绑定", u.Name, device.Name)})
			changed = true
		}
	}
	return changed
}

func ipRestrictionRules(s *State, now time.Time) []any {
	normalizeDeviceModel(s)
	var rules []any
	for _, u := range s.Users {
		if !u.Enabled || expired(u, now) || overQuota(u) || burstHardBlocked(u, now) {
			continue
		}
		for _, device := range u.Devices {
			policy := normalizedIPPolicy(device.IPPolicy)
			if !device.Enabled || !policy.Enabled || policy.Mode != "enforce" {
				continue
			}
			authUsers := make([]any, 0, len(u.Nodes))
			for _, node := range u.Nodes {
				if strings.EqualFold(node.Device, device.Name) {
					authUsers = append(authUsers, node.AuthUser)
				}
			}
			rules = appendIPRestrictionRule(rules, authUsers, policy, now)
		}
		policy := normalizedIPPolicy(u.IPPolicy)
		if policy.Enabled && policy.Mode == "enforce" {
			authUsers := make([]any, 0, len(u.Nodes))
			for _, node := range u.Nodes {
				if deviceEnabled(u, node.Device) {
					authUsers = append(authUsers, node.AuthUser)
				}
			}
			rules = appendIPRestrictionRule(rules, authUsers, policy, now)
		}
	}
	return rules
}

func appendIPRestrictionRule(rules []any, authUsers []any, policy IPPolicy, now time.Time) []any {
	if len(authUsers) == 0 {
		return rules
	}
	allowed := activePolicyIPs(policy, now)
	if len(allowed) == 0 && (policy.Binding == "auto" || policy.Binding == "dynamic") {
		return rules // permit the first observed connection so it can be learned.
	}
	if len(allowed) == 0 {
		return append(rules, map[string]any{"auth_user": authUsers, "action": "reject", "method": "drop"})
	}
	return append(rules, map[string]any{
		"type": "logical", "mode": "and", "action": "reject", "method": "drop",
		"rules": []any{
			map[string]any{"auth_user": authUsers},
			map[string]any{"source_ip_cidr": allowed, "invert": true},
		},
	})
}

func topSourceIPs(u User, limit int) []string {
	type item struct {
		ip   string
		stat SourceIPStat
	}
	items := make([]item, 0, len(u.SourceIPs))
	for ip, stat := range u.SourceIPs {
		items = append(items, item{ip: ip, stat: stat})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].stat.LastSeen > items[j].stat.LastSeen })
	if len(items) > limit {
		items = items[:limit]
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		marker := ""
		if containsIP(activePolicyIPs(normalizedIPPolicy(u.IPPolicy), time.Now()), item.ip) {
			marker = " ✓"
		}
		result = append(result, fmt.Sprintf("%s%s · %d 次 · %s · %s", item.ip, marker, item.stat.Count, item.stat.LastNode, formatDisplayTime(item.stat.LastSeen)))
	}
	return result
}

func topDeviceSourceIPs(device Device, limit int) []string {
	type item struct {
		ip   string
		stat SourceIPStat
	}
	items := make([]item, 0, len(device.SourceIPs))
	for ip, stat := range device.SourceIPs {
		items = append(items, item{ip: ip, stat: stat})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].stat.LastSeen > items[j].stat.LastSeen })
	if len(items) > limit {
		items = items[:limit]
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		marker := ""
		if containsIP(activePolicyIPs(normalizedIPPolicy(device.IPPolicy), time.Now()), item.ip) {
			marker = " ✓"
		}
		result = append(result, fmt.Sprintf("%s%s · %d 次 · %s", item.ip, marker, item.stat.Count, formatDisplayTime(item.stat.LastSeen)))
	}
	return result
}
