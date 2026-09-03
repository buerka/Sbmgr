package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

type batchOperationKind int

const (
	batchUserSettings batchOperationKind = iota
	batchNodeRates
	batchBurstPolicy
	batchIPPolicy
	batchAccessPolicy
)

type batchUserSettingsPatch struct {
	Enabled         *bool
	QuotaBytes      *int64
	QuotaMode       *string
	ExtraQuotaBytes *int64
	Expires         *string
	BillingEnabled  *bool
	BillingDay      *int
	ThrottleEnabled *bool
	Tier1Usage      *float64
	Tier1Speed      *float64
	Tier2Usage      *float64
	Tier2Speed      *float64
}

type batchNodeRatesPatch struct {
	NodeName     string
	UploadMbps   *float64
	DownloadMbps *float64
}

type batchBurstPolicyPatch struct {
	Enabled          *bool
	Action           *string
	WindowMinutes    *int
	LimitBytes       *int64
	BlockMinutes     *int
	SoftUploadKbps   *float64
	SoftDownloadKbps *float64
}

type batchIPPolicyPatch struct {
	Enabled          *bool
	Mode             *string
	Binding          *string
	MaxIPs           *int
	HandoverSeconds  *int
	BoundIPs         *[]string
	TemporaryIPs     *[]string
	TemporaryMinutes *int
}

type batchAccessPolicyPatch struct {
	AllowedDomains   *[]string
	BlockedDomains   *[]string
	BlockedPorts     *[]int
	MaxConnections   *int
	ConnectionAction *string
}

type batchOperation struct {
	Kind   batchOperationKind
	Users  []string
	User   batchUserSettingsPatch
	Node   batchNodeRatesPatch
	Burst  batchBurstPolicyPatch
	IP     batchIPPolicyPatch
	Access batchAccessPolicyPatch
}

type batchResult struct {
	Users int
	Nodes int
}

func (a *app) batchUsers(op batchOperation) error {
	names := normalizedBatchUserNames(op.Users)
	auditArgs := []string{batchOperationName(op.Kind), "--users", strings.Join(names, ",")}
	return a.withAuditedStateLock("user.batch", auditArgs, func() error {
		s, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		updated, result, err := applyBatchOperation(s, op, time.Now())
		if err != nil {
			return err
		}
		if err := saveState(a.statePath, updated); err != nil {
			return err
		}
		if op.Kind == batchNodeRates {
			fmt.Fprintf(a.out, "已批量更新 %d 个用户的 %d 个节点（按 p 应用配置后生效）\n", result.Users, result.Nodes)
		} else {
			fmt.Fprintf(a.out, "已批量更新 %d 个用户（按 p 应用配置后生效）\n", result.Users)
		}
		return nil
	})
}

// applyBatchOperation validates and applies the complete operation to a deep
// copy. The caller's state is never changed when one selected user fails.
func applyBatchOperation(source *State, op batchOperation, now time.Time) (*State, batchResult, error) {
	if source == nil {
		return nil, batchResult{}, errors.New("状态为空")
	}
	names := normalizedBatchUserNames(op.Users)
	if len(names) == 0 {
		return nil, batchResult{}, errors.New("请至少选择一个用户")
	}
	if !batchOperationHasChanges(op) {
		return nil, batchResult{}, errors.New("没有指定要批量修改的字段")
	}
	updated, err := cloneStateForBatch(source)
	if err != nil {
		return nil, batchResult{}, err
	}
	result := batchResult{}
	for _, name := range names {
		u := findUser(updated, name)
		if u == nil {
			return nil, batchResult{}, fmt.Errorf("用户 %q 不存在，批量修改已取消", name)
		}
		before := *u
		beforeJSON, _ := json.Marshal(before)
		nodes, err := applyBatchToUser(updated, u, op, now)
		if err != nil {
			return nil, batchResult{}, fmt.Errorf("用户 %s: %w", u.Name, err)
		}
		afterJSON, _ := json.Marshal(*u)
		if !reflect.DeepEqual(beforeJSON, afterJSON) {
			result.Users++
		}
		result.Nodes += nodes
	}
	if result.Users == 0 && result.Nodes == 0 {
		return nil, batchResult{}, errors.New("所选参数与当前配置相同")
	}
	if err := validateState(updated); err != nil {
		return nil, batchResult{}, fmt.Errorf("批量修改后的状态无效: %w", err)
	}
	return updated, result, nil
}

func applyBatchToUser(s *State, u *User, op batchOperation, now time.Time) (int, error) {
	switch op.Kind {
	case batchUserSettings:
		return 0, applyBatchUserSettings(u, op.User, now)
	case batchNodeRates:
		return applyBatchNodeRates(s, u, op.Node)
	case batchBurstPolicy:
		return 0, applyBatchBurst(u, op.Burst)
	case batchIPPolicy:
		return 0, applyBatchIP(s, u, op.IP, now)
	case batchAccessPolicy:
		return 0, applyBatchAccess(u, op.Access)
	default:
		return 0, errors.New("未知批量操作")
	}
}

func applyBatchUserSettings(u *User, patch batchUserSettingsPatch, now time.Time) error {
	if patch.Enabled != nil {
		u.Enabled = *patch.Enabled
		if u.Enabled {
			u.DisabledReason = ""
		} else {
			u.DisabledReason = "manual"
		}
	}
	if patch.QuotaBytes != nil {
		if *patch.QuotaBytes < 0 {
			return errors.New("流量配额不能为负数")
		}
		u.QuotaBytes = *patch.QuotaBytes
		u.QuotaAlertStage = 0
	}
	if patch.QuotaMode != nil {
		if err := validateQuotaMode(*patch.QuotaMode); err != nil {
			return err
		}
		u.QuotaMode = normalizedQuotaMode(*patch.QuotaMode)
		u.QuotaAlertStage = 0
	}
	if patch.ExtraQuotaBytes != nil {
		if *patch.ExtraQuotaBytes < 0 {
			return errors.New("附加流量包不能为负数")
		}
		u.ExtraQuotaBytes = *patch.ExtraQuotaBytes
		u.QuotaAlertStage = 0
	}
	if patch.Expires != nil {
		if err := validateDate(*patch.Expires); err != nil {
			return err
		}
		u.Expires = *patch.Expires
		u.ExpiryAlertStage = 0
	}
	if patch.BillingEnabled != nil || patch.BillingDay != nil {
		policy := normalizedBilling(u.Billing)
		if patch.BillingEnabled != nil {
			if *patch.BillingEnabled && !policy.Enabled {
				location, _ := billingLocation(policy.TimeZone)
				policy.LastReset = now.In(location).Format(time.RFC3339)
				policy.NextReset = nextBillingReset(now, policy).Format(time.RFC3339)
			}
			policy.Enabled = *patch.BillingEnabled
		}
		if patch.BillingDay != nil {
			policy.CycleDay = *patch.BillingDay
			if policy.Enabled {
				policy.NextReset = nextBillingReset(now, policy).Format(time.RFC3339)
			}
		}
		if err := validateBilling(policy); err != nil {
			return err
		}
		u.Billing = policy
	}
	if patch.ThrottleEnabled != nil || patch.Tier1Usage != nil || patch.Tier1Speed != nil || patch.Tier2Usage != nil || patch.Tier2Speed != nil {
		policy := normalizedThrottle(u.Throttle)
		if patch.ThrottleEnabled != nil {
			policy.Enabled = *patch.ThrottleEnabled
		}
		if patch.Tier1Usage != nil {
			policy.Tier1Usage = *patch.Tier1Usage
		}
		if patch.Tier1Speed != nil {
			policy.Tier1Speed = *patch.Tier1Speed
		}
		if patch.Tier2Usage != nil {
			policy.Tier2Usage = *patch.Tier2Usage
		}
		if patch.Tier2Speed != nil {
			policy.Tier2Speed = *patch.Tier2Speed
		}
		if err := validateThrottle(policy); err != nil {
			return err
		}
		u.Throttle = policy
	}
	return nil
}

func applyBatchNodeRates(s *State, u *User, patch batchNodeRatesPatch) (int, error) {
	changed := map[int]bool{}
	if rateLimited(*u) {
		// Preserve the effective limits of untouched legacy nodes before clearing
		// the old user-level fallback. This also lets an explicit 0/0 mean
		// unlimited for just the selected line.
		for index := range u.Nodes {
			node := &u.Nodes[index]
			if nodeRateLimited(*node) {
				continue
			}
			node.UploadMbps, node.DownloadMbps = u.UploadMbps, u.DownloadMbps
			if nodeRateLimited(*node) && !validRateMark(node.RateMark) {
				node.RateMark = allocateRateMark(s)
			}
			changed[index] = true
		}
		u.UploadMbps, u.DownloadMbps, u.RateMark = 0, 0, 0
	}
	matched := 0
	for index := range u.Nodes {
		node := &u.Nodes[index]
		if patch.NodeName != "" && !strings.EqualFold(node.Name, patch.NodeName) {
			continue
		}
		matched++
		up, down := node.UploadMbps, node.DownloadMbps
		if patch.UploadMbps != nil {
			up = *patch.UploadMbps
		}
		if patch.DownloadMbps != nil {
			down = *patch.DownloadMbps
		}
		if err := validateMbps(up, down); err != nil {
			return 0, fmt.Errorf("节点 %s/%s: %w", node.Device, node.Name, err)
		}
		if node.UploadMbps == up && node.DownloadMbps == down {
			continue
		}
		node.UploadMbps, node.DownloadMbps = up, down
		if nodeRateLimited(*node) && !validRateMark(node.RateMark) {
			node.RateMark = allocateRateMark(s)
		}
		changed[index] = true
	}
	if matched == 0 {
		name := patch.NodeName
		if name == "" {
			name = "所有节点"
		}
		return 0, fmt.Errorf("没有匹配的节点 %q，批量修改已取消", name)
	}
	return len(changed), nil
}

func applyBatchBurst(u *User, patch batchBurstPolicyPatch) error {
	policy := normalizedBurst(u.Burst)
	if patch.Enabled != nil {
		policy.Enabled = *patch.Enabled
	}
	if patch.Action != nil {
		policy.Action = *patch.Action
	}
	if patch.WindowMinutes != nil {
		policy.WindowMinutes = *patch.WindowMinutes
	}
	if patch.LimitBytes != nil {
		policy.LimitBytes = *patch.LimitBytes
	}
	if patch.BlockMinutes != nil {
		policy.BlockMinutes = *patch.BlockMinutes
	}
	if patch.SoftUploadKbps != nil {
		policy.SoftUploadKbps = *patch.SoftUploadKbps
	}
	if patch.SoftDownloadKbps != nil {
		policy.SoftDownloadKbps = *patch.SoftDownloadKbps
	}
	if err := validateBurst(policy); err != nil {
		return err
	}
	u.Burst = policy
	if !policy.Enabled {
		u.TrafficSamples = nil
		u.BlockedUntil, u.BlockReason = "", ""
	}
	return nil
}

func applyBatchIP(s *State, u *User, patch batchIPPolicyPatch, now time.Time) error {
	oldPolicy := normalizedIPPolicy(u.IPPolicy)
	policy := oldPolicy
	if patch.Enabled != nil {
		policy.Enabled = *patch.Enabled
	}
	if patch.Mode != nil {
		policy.Mode = *patch.Mode
	}
	if patch.Binding != nil {
		policy.Binding = *patch.Binding
		if policy.Binding == "dynamic" && patch.MaxIPs == nil {
			policy.MaxIPs = 1
		}
	}
	if patch.MaxIPs != nil {
		policy.MaxIPs = *patch.MaxIPs
	}
	if patch.HandoverSeconds != nil {
		policy.HandoverSeconds = *patch.HandoverSeconds
	}
	if patch.BoundIPs != nil {
		policy.BoundIPs = append([]string(nil), (*patch.BoundIPs)...)
	}
	if patch.TemporaryIPs != nil {
		policy.TemporaryIPs = append([]string(nil), (*patch.TemporaryIPs)...)
		if len(policy.TemporaryIPs) == 0 {
			policy.TemporaryUntil = ""
		} else {
			if patch.TemporaryMinutes == nil || *patch.TemporaryMinutes <= 0 {
				return errors.New("替换临时 IP 时必须同时填写大于 0 的有效分钟数")
			}
			policy.TemporaryUntil = now.Add(time.Duration(*patch.TemporaryMinutes) * time.Minute).Format(time.RFC3339Nano)
		}
	} else if patch.TemporaryMinutes != nil {
		if len(policy.TemporaryIPs) == 0 || *patch.TemporaryMinutes <= 0 {
			return errors.New("延长临时 IP 时必须已有临时 IP，且分钟数大于 0")
		}
		policy.TemporaryUntil = now.Add(time.Duration(*patch.TemporaryMinutes) * time.Minute).Format(time.RFC3339Nano)
	}
	if policy.Enabled && policy.Binding == "dynamic" && len(policy.BoundIPs) == 0 && len(policy.TemporaryIPs) == 0 {
		if active := activeSourceIPs(s, u.Name, ""); len(active) == 1 {
			policy.BoundIPs = active
		}
	}
	if err := validateIPPolicy(policy); err != nil {
		return err
	}
	u.IPPolicy = policy
	if ipPolicyRuleSignature(oldPolicy, now) != ipPolicyRuleSignature(policy, now) {
		s.IPApplyPending = true
	}
	return nil
}

func applyBatchAccess(u *User, patch batchAccessPolicyPatch) error {
	policy := normalizedAccessPolicy(u.Access)
	if patch.AllowedDomains != nil {
		policy.AllowedDomains = append([]string(nil), (*patch.AllowedDomains)...)
	}
	if patch.BlockedDomains != nil {
		policy.BlockedDomains = append([]string(nil), (*patch.BlockedDomains)...)
	}
	if patch.BlockedPorts != nil {
		policy.BlockedPorts = append([]int(nil), (*patch.BlockedPorts)...)
	}
	if patch.MaxConnections != nil {
		policy.MaxConnections = *patch.MaxConnections
	}
	if patch.ConnectionAction != nil {
		policy.ConnectionAction = *patch.ConnectionAction
	}
	if err := validateAccessPolicy(policy); err != nil {
		return err
	}
	u.Access = policy
	return nil
}

func batchOperationHasChanges(op batchOperation) bool {
	switch op.Kind {
	case batchUserSettings:
		p := op.User
		return p.Enabled != nil || p.QuotaBytes != nil || p.QuotaMode != nil || p.ExtraQuotaBytes != nil || p.Expires != nil || p.BillingEnabled != nil || p.BillingDay != nil || p.ThrottleEnabled != nil || p.Tier1Usage != nil || p.Tier1Speed != nil || p.Tier2Usage != nil || p.Tier2Speed != nil
	case batchNodeRates:
		return op.Node.UploadMbps != nil || op.Node.DownloadMbps != nil
	case batchBurstPolicy:
		p := op.Burst
		return p.Enabled != nil || p.Action != nil || p.WindowMinutes != nil || p.LimitBytes != nil || p.BlockMinutes != nil || p.SoftUploadKbps != nil || p.SoftDownloadKbps != nil
	case batchIPPolicy:
		p := op.IP
		return p.Enabled != nil || p.Mode != nil || p.Binding != nil || p.MaxIPs != nil || p.HandoverSeconds != nil || p.BoundIPs != nil || p.TemporaryIPs != nil || p.TemporaryMinutes != nil
	case batchAccessPolicy:
		p := op.Access
		return p.AllowedDomains != nil || p.BlockedDomains != nil || p.BlockedPorts != nil || p.MaxConnections != nil || p.ConnectionAction != nil
	default:
		return false
	}
}

func batchOperationName(kind batchOperationKind) string {
	switch kind {
	case batchUserSettings:
		return "settings"
	case batchNodeRates:
		return "node-rates"
	case batchBurstPolicy:
		return "burst"
	case batchIPPolicy:
		return "ip-policy"
	case batchAccessPolicy:
		return "access-policy"
	default:
		return "unknown"
	}
}

func normalizedBatchUserNames(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value != "" && !seen[key] {
			seen[key] = true
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i]) < strings.ToLower(result[j]) })
	return result
}

func cloneStateForBatch(source *State) (*State, error) {
	data, err := json.Marshal(source)
	if err != nil {
		return nil, fmt.Errorf("复制状态: %w", err)
	}
	var cloned State
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, fmt.Errorf("复制状态: %w", err)
	}
	return &cloned, nil
}
