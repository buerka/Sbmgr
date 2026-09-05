package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

const (
	burstActionSoft = "soft"
	burstActionHard = "hard"
)

func normalizedBurst(policy BurstPolicy) BurstPolicy {
	if policy.WindowMinutes == 0 {
		policy.WindowMinutes = 30
	}
	if policy.LimitBytes == 0 {
		policy.LimitBytes = 2 * 1024 * 1024 * 1024
	}
	if policy.BlockMinutes == 0 {
		policy.BlockMinutes = 30
	}
	if policy.Action == "" {
		policy.Action = burstActionSoft
	}
	if policy.SoftUploadKbps == 0 {
		policy.SoftUploadKbps = 16
	}
	if policy.SoftDownloadKbps == 0 {
		policy.SoftDownloadKbps = 2
	}
	return policy
}

func validateBurst(policy BurstPolicy) error {
	if !policy.Enabled {
		return nil
	}
	if policy.WindowMinutes <= 0 {
		return errors.New("异常流量检测窗口必须大于 0 分钟")
	}
	if policy.LimitBytes <= 0 {
		return errors.New("异常流量阈值必须大于 0")
	}
	if policy.BlockMinutes <= 0 {
		return errors.New("临时封禁时长必须大于 0 分钟")
	}
	if policy.Action != burstActionSoft && policy.Action != burstActionHard {
		return errors.New("封禁类型必须是 soft 或 hard")
	}
	if policy.Action == burstActionSoft {
		if policy.SoftUploadKbps <= 0 || policy.SoftDownloadKbps <= 0 {
			return errors.New("软封禁上传和下载 Kbps 必须大于 0")
		}
		if err := validateMbps(policy.SoftUploadKbps/1000, policy.SoftDownloadKbps/1000); err != nil {
			return fmt.Errorf("软封禁速率: %w", err)
		}
	}
	return nil
}

func burstBlocked(u User, now time.Time) bool {
	if u.BlockedUntil == "" {
		return false
	}
	until, err := time.Parse(time.RFC3339Nano, u.BlockedUntil)
	return err != nil || now.Before(until)
}

func burstSoftBlocked(u User, now time.Time) bool {
	return burstBlocked(u, now) && normalizedBurst(u.Burst).Action == burstActionSoft
}

func burstHardBlocked(u User, now time.Time) bool {
	return burstBlocked(u, now) && normalizedBurst(u.Burst).Action == burstActionHard
}

func burstActionText(policy BurstPolicy) string {
	if normalizedBurst(policy).Action == burstActionHard {
		return "硬封禁（完全断连）"
	}
	return "软封禁（极低速限流）"
}

func hasHardBurstBlock(s *State, now time.Time) bool {
	for _, user := range s.Users {
		if burstHardBlocked(user, now) {
			return true
		}
	}
	return false
}

func burstUsage(u User, now time.Time) int64 {
	policy := normalizedBurst(u.Burst)
	cutoff := now.Add(-time.Duration(policy.WindowMinutes) * time.Minute)
	var total int64
	for _, sample := range u.TrafficSamples {
		at, err := time.Parse(time.RFC3339Nano, sample.At)
		if err == nil && !at.Before(cutoff) && !at.After(now) && sample.Bytes > 0 {
			total += sample.Bytes
		}
	}
	return total
}

func appendTrafficSample(u *User, bytes int64, now time.Time) bool {
	if u == nil || !u.Burst.Enabled || bytes <= 0 {
		return false
	}
	u.TrafficSamples = append(u.TrafficSamples, TrafficSample{At: now.Format(time.RFC3339Nano), Bytes: bytes})
	return true
}

// evaluateBurstPolicies prunes rolling samples, blocks threshold violators and
// automatically releases expired temporary blocks. configChanged means the
// active sing-box users changed and applyState must restart existing sessions.
func evaluateBurstPolicies(s *State, now time.Time) (stateChanged, configChanged, hardDisconnect bool, alerts []Alert) {
	for i := range s.Users {
		u := &s.Users[i]
		policy := normalizedBurst(u.Burst)
		if u.BlockedUntil != "" && !burstBlocked(*u, now) {
			wasHard := policy.Action == burstActionHard
			u.BlockedUntil, u.BlockReason = "", ""
			u.TrafficSamples = nil
			alert := Alert{At: now.Format(time.RFC3339), User: u.Name, Kind: "burst_unblocked", Message: fmt.Sprintf("用户 %s 的%s已到期，已自动恢复", u.Name, burstActionText(policy))}
			appendAlert(s, alert)
			alerts = append(alerts, alert)
			stateChanged, configChanged = true, true
			hardDisconnect = hardDisconnect || wasHard
		}
		if !policy.Enabled {
			if len(u.TrafficSamples) > 0 {
				u.TrafficSamples = nil
				stateChanged = true
			}
			continue
		}
		if !u.Enabled || expired(*u, now) || overQuota(*u) {
			if len(u.TrafficSamples) > 0 {
				u.TrafficSamples = nil
				stateChanged = true
			}
			continue
		}
		cutoff := now.Add(-time.Duration(policy.WindowMinutes) * time.Minute)
		kept := u.TrafficSamples[:0]
		for _, sample := range u.TrafficSamples {
			at, err := time.Parse(time.RFC3339Nano, sample.At)
			if err == nil && !at.Before(cutoff) {
				kept = append(kept, sample)
			} else {
				stateChanged = true
			}
		}
		u.TrafficSamples = kept
		if burstBlocked(*u, now) || burstUsage(*u, now) < policy.LimitBytes {
			continue
		}
		used := burstUsage(*u, now)
		u.BlockedUntil = now.Add(time.Duration(policy.BlockMinutes) * time.Minute).Format(time.RFC3339Nano)
		u.BlockReason = fmt.Sprintf("%d 分钟内使用 %s，达到阈值 %s", policy.WindowMinutes, formatSize(used), formatSize(policy.LimitBytes))
		u.TrafficSamples = nil
		alert := Alert{At: now.Format(time.RFC3339), User: u.Name, Kind: "burst_blocked", Message: fmt.Sprintf("异常流量：用户 %s %s；已执行%s %d 分钟", u.Name, u.BlockReason, burstActionText(policy), policy.BlockMinutes)}
		appendAlert(s, alert)
		alerts = append(alerts, alert)
		stateChanged, configChanged = true, true
		hardDisconnect = hardDisconnect || policy.Action == burstActionHard
	}
	return
}

func appendAlert(s *State, alert Alert) {
	s.Alerts = append(s.Alerts, alert)
	if len(s.Alerts) > 100 {
		s.Alerts = append([]Alert(nil), s.Alerts[len(s.Alerts)-100:]...)
	}
}

func unreadAlertCount(s *State) int {
	count := 0
	for _, alert := range s.Alerts {
		if !alert.Acknowledged {
			count++
		}
	}
	return count
}

func (a *app) acknowledgeAlerts() error {
	return a.withAuditedStateLock("alerts.acknowledge", nil, a.acknowledgeAlertsLocked)
}

func (a *app) acknowledgeAlertsLocked() error {
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	count := 0
	for i := range s.Alerts {
		if !s.Alerts[i].Acknowledged {
			s.Alerts[i].Acknowledged = true
			count++
		}
	}
	if err := saveState(a.statePath, s); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "已将 %d 条告警标记为已读\n", count)
	return nil
}

// burstConfigurationPending is deliberately limited to users with protection
// enabled, so the daemon retries a failed automatic block/unblock without
// silently applying unrelated interactive edits.
func burstConfigurationPending(s *State) bool {
	raw, err := os.ReadFile(s.ConfigPath)
	if err != nil {
		return true
	}
	var cfg map[string]any
	if json.Unmarshal(raw, &cfg) != nil {
		return true
	}
	active := map[string]bool{}
	if inbounds, ok := cfg["inbounds"].([]any); ok {
		for _, item := range inbounds {
			inbound, _ := item.(map[string]any)
			if stringValue(inbound["tag"]) != s.InboundTag {
				continue
			}
			if users, ok := inbound["users"].([]any); ok {
				for _, value := range users {
					user, _ := value.(map[string]any)
					active[stringValue(user["name"])] = true
				}
			}
		}
	}
	now := time.Now()
	for _, user := range s.Users {
		if !user.Burst.Enabled {
			continue
		}
		want := user.Enabled && !expired(user, now) && !overQuota(user) && !burstHardBlocked(user, now)
		for _, node := range user.Nodes {
			nodeWant := want && deviceEnabled(user, node.Device)
			if active[node.AuthUser] != nodeWant {
				return true
			}
		}
	}
	return false
}
