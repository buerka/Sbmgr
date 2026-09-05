package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// cloneUserFromTemplate copies user-facing configuration while deliberately
// replacing every identity and clearing all runtime/accounting history.
func cloneUserFromTemplate(s *State, sourceName, newName string, now time.Time) (User, error) {
	if s == nil {
		return User{}, errors.New("状态为空")
	}
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return User{}, errors.New("新用户名不能为空")
	}
	if findUser(s, newName) != nil {
		return User{}, fmt.Errorf("用户 %q 已存在", newName)
	}
	normalizeDeviceModel(s)
	source := findUser(s, sourceName)
	if source == nil {
		return User{}, fmt.Errorf("模板用户 %q 不存在", sourceName)
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return User{}, fmt.Errorf("复制模板用户: %w", err)
	}
	var cloned User
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return User{}, fmt.Errorf("复制模板用户: %w", err)
	}

	cloned.Name = newName
	cloned.Enabled = true
	cloned.Upload, cloned.Download = 0, 0
	cloned.RateMark = 0
	cloned.SourceIPs = nil
	cloned.TrafficSamples = nil
	cloned.UsageHistory = nil
	cloned.RecentAccesses = nil
	cloned.CurrentUploadMbps, cloned.CurrentDownloadMbps = 0, 0
	cloned.BlockedUntil, cloned.BlockReason, cloned.DisabledReason = "", "", ""
	cloned.BillingHistory = nil
	cloned.Billing.LastReset, cloned.Billing.NextReset = "", ""
	cloned.QuotaAlertStage, cloned.ExpiryAlertStage = 0, 0
	cloned.Access.LastConnectionAlert = ""
	cloned.Access.ConnectionBlockedUntil = ""
	resetTemplateIPRuntime(&cloned.IPPolicy)

	usedAuth := map[string]bool{}
	for _, auth := range s.ReservedAuthUsers {
		usedAuth[auth] = true
	}
	for _, user := range s.Users {
		for _, node := range user.Nodes {
			usedAuth[node.AuthUser] = true
		}
	}
	allocateAuth := func(base string) string {
		if !usedAuth[base] {
			usedAuth[base] = true
			return base
		}
		for suffix := 2; ; suffix++ {
			candidate := fmt.Sprintf("%s-%d", base, suffix)
			if !usedAuth[candidate] {
				usedAuth[candidate] = true
				return candidate
			}
		}
	}

	createdAt := now.Format(time.RFC3339)
	for index := range cloned.Devices {
		device := &cloned.Devices[index]
		device.CreatedAt = createdAt
		device.LastSeen = ""
		device.SourceIPs = nil
		device.SubscriptionToken = newSubscriptionToken()
		device.Access.LastConnectionAlert = ""
		device.Access.ConnectionBlockedUntil = ""
		resetTemplateIPRuntime(&device.IPPolicy)
	}
	for index := range cloned.Nodes {
		node := &cloned.Nodes[index]
		base := newName + "-" + slug(node.Device) + "-" + slug(node.Name)
		node.AuthUser = allocateAuth(base)
		node.UUID = newUUID()
		node.RateMark = 0
		node.Upload, node.Download = 0, 0
		node.Destinations = nil
		node.CurrentUploadMbps, node.CurrentDownloadMbps = 0, 0
		node.RateUpdatedAt = ""
	}
	return cloned, nil
}

func resetTemplateIPRuntime(policy *IPPolicy) {
	if policy == nil {
		return
	}
	policy.TemporaryIPs, policy.TemporaryUntil = nil, ""
	policy.BoundLastSeen = nil
	if normalizedIPPolicy(*policy).Binding != "manual" {
		policy.BoundIPs = nil
	}
}
