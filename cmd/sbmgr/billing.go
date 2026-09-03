package main

import (
	"errors"
	"fmt"
	"time"
)

func normalizedBilling(policy BillingPolicy) BillingPolicy {
	if policy.CycleDay == 0 {
		policy.CycleDay = 1
	}
	if policy.TimeZone == "" {
		policy.TimeZone = "Asia/Shanghai"
	}
	return policy
}

func validateBilling(policy BillingPolicy) error {
	policy = normalizedBilling(policy)
	if policy.CycleDay < 1 || policy.CycleDay > 28 {
		return errors.New("账期日必须在 1–28 之间")
	}
	if _, err := billingLocation(policy.TimeZone); err != nil {
		return fmt.Errorf("无效账期时区 %q: %w", policy.TimeZone, err)
	}
	for _, value := range []string{policy.LastReset, policy.NextReset} {
		if value != "" {
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				return fmt.Errorf("无效账期时间 %q", value)
			}
		}
	}
	return nil
}

func billingLocation(name string) (*time.Location, error) {
	if name == "Asia/Shanghai" {
		if location, err := time.LoadLocation(name); err == nil {
			return location, nil
		}
		return time.FixedZone(name, 8*60*60), nil
	}
	return time.LoadLocation(name)
}

func applicationLocation() *time.Location {
	location, _ := billingLocation("Asia/Shanghai")
	return location
}

func nextBillingReset(now time.Time, policy BillingPolicy) time.Time {
	policy = normalizedBilling(policy)
	location, _ := billingLocation(policy.TimeZone)
	local := now.In(location)
	candidate := time.Date(local.Year(), local.Month(), policy.CycleDay, 0, 0, 0, 0, location)
	if !candidate.After(local) {
		candidate = time.Date(local.Year(), local.Month()+1, policy.CycleDay, 0, 0, 0, 0, location)
	}
	return candidate
}

func userQuota(u User) int64 {
	if u.QuotaBytes <= 0 {
		return 0
	}
	if u.ExtraQuotaBytes > 0 && u.ExtraQuotaBytes <= int64(^uint64(0)>>1)-u.QuotaBytes {
		return u.QuotaBytes + u.ExtraQuotaBytes
	}
	return u.QuotaBytes
}

func evaluateBillingCycles(s *State, now time.Time) bool {
	changed := false
	for index := range s.Users {
		u := &s.Users[index]
		policy := normalizedBilling(u.Billing)
		if !policy.Enabled {
			continue
		}
		location, _ := billingLocation(policy.TimeZone)
		localNow := now.In(location)
		next, err := time.Parse(time.RFC3339, policy.NextReset)
		if err != nil {
			policy.LastReset = localNow.Format(time.RFC3339)
			policy.NextReset = nextBillingReset(now, policy).Format(time.RFC3339)
			u.Billing = policy
			changed = true
			continue
		}
		if now.Before(next) {
			continue
		}
		started := policy.LastReset
		if started == "" {
			started = localNow.Format(time.RFC3339)
		}
		u.BillingHistory = append(u.BillingHistory, BillingRecord{StartedAt: started, EndedAt: next.Format(time.RFC3339), UploadBytes: u.Upload, DownloadBytes: u.Download, QuotaBytes: userQuota(*u)})
		if len(u.BillingHistory) > 24 {
			u.BillingHistory = append([]BillingRecord(nil), u.BillingHistory[len(u.BillingHistory)-24:]...)
		}
		resetUserTraffic(u)
		if u.DisabledReason == "quota" && !expired(*u, now) {
			u.Enabled = true
			u.DisabledReason = ""
		}
		policy.LastReset = next.Format(time.RFC3339)
		policy.NextReset = nextBillingReset(now, policy).Format(time.RFC3339)
		u.Billing = policy
		appendAlert(s, Alert{At: now.Format(time.RFC3339), User: u.Name, Kind: "billing_reset", Message: fmt.Sprintf("用户 %s 的账期已重置；上期用量 ↑%s / ↓%s，下次重置 %s", u.Name, formatSize(u.BillingHistory[len(u.BillingHistory)-1].UploadBytes), formatSize(u.BillingHistory[len(u.BillingHistory)-1].DownloadBytes), policy.NextReset)})
		changed = true
	}
	return changed
}

func resetUserTraffic(u *User) {
	u.Upload, u.Download = 0, 0
	u.CurrentUploadMbps, u.CurrentDownloadMbps = 0, 0
	u.TrafficSamples = nil
	u.UsageHistory = nil
	u.BlockedUntil, u.BlockReason = "", ""
	u.QuotaAlertStage = 0
	for index := range u.Nodes {
		u.Nodes[index].Upload, u.Nodes[index].Download = 0, 0
		u.Nodes[index].CurrentUploadMbps, u.Nodes[index].CurrentDownloadMbps = 0, 0
	}
}

func manualResetUserMonthlyTraffic(s *State, u *User, now time.Time) {
	oldUpload, oldDownload := u.Upload, u.Download
	policy := normalizedBilling(u.Billing)
	if policy.Enabled {
		location, _ := billingLocation(policy.TimeZone)
		localNow := now.In(location)
		if oldUpload > 0 || oldDownload > 0 {
			started := policy.LastReset
			if started == "" {
				started = localNow.Format(time.RFC3339)
			}
			u.BillingHistory = append(u.BillingHistory, BillingRecord{
				StartedAt: started, EndedAt: localNow.Format(time.RFC3339),
				UploadBytes: oldUpload, DownloadBytes: oldDownload, QuotaBytes: userQuota(*u),
			})
			if len(u.BillingHistory) > 24 {
				u.BillingHistory = append([]BillingRecord(nil), u.BillingHistory[len(u.BillingHistory)-24:]...)
			}
		}
		policy.LastReset = localNow.Format(time.RFC3339)
		if next, err := time.Parse(time.RFC3339, policy.NextReset); err != nil || !next.After(now) {
			policy.NextReset = nextBillingReset(now, policy).Format(time.RFC3339)
		}
		u.Billing = policy
	}
	resetUserTraffic(u)
	if u.DisabledReason == "quota" && !expired(*u, now) {
		u.Enabled = true
		u.DisabledReason = ""
	}
	appendAlert(s, Alert{
		At: now.Format(time.RFC3339), User: u.Name, Kind: "traffic_reset_manual",
		Message: fmt.Sprintf("管理员已手动重置用户 %s 的本月流量；重置前 ↑%s / ↓%s", u.Name, formatSize(oldUpload), formatSize(oldDownload)),
	})
}

func evaluateLifecycleAlerts(s *State, now time.Time) bool {
	changed := false
	location := applicationLocation()
	localNow := now.In(location)
	for index := range s.Users {
		u := &s.Users[index]
		quotaStage := 0
		percent := usagePercent(*u)
		if userQuota(*u) > 0 {
			if percent >= 95 {
				quotaStage = 2
			} else if percent >= 80 {
				quotaStage = 1
			}
		}
		if quotaStage > u.QuotaAlertStage {
			threshold := 80
			if quotaStage == 2 {
				threshold = 95
			}
			appendAlert(s, Alert{At: now.Format(time.RFC3339), User: u.Name, Kind: "quota_warning", Message: fmt.Sprintf("用户 %s 已使用 %.1f%% 流量，达到 %d%% 预警线", u.Name, percent, threshold)})
			u.QuotaAlertStage = quotaStage
			changed = true
		} else if quotaStage < u.QuotaAlertStage && percent < 80 {
			u.QuotaAlertStage = quotaStage
			changed = true
		}

		expiryStage := 0
		days := 0
		if u.Expires != "" {
			date, err := time.ParseInLocation("2006-01-02", u.Expires, location)
			if err == nil {
				end := date.AddDate(0, 0, 1)
				days = int(end.Sub(localNow).Hours()/24 + 0.999)
				if days <= 1 {
					expiryStage = 3
				} else if days <= 3 {
					expiryStage = 2
				} else if days <= 7 {
					expiryStage = 1
				}
			}
		}
		if expiryStage > u.ExpiryAlertStage {
			appendAlert(s, Alert{At: now.Format(time.RFC3339), User: u.Name, Kind: "expiry_warning", Message: fmt.Sprintf("用户 %s 将在 %d 天内到期（%s）", u.Name, max(0, days), u.Expires)})
			u.ExpiryAlertStage = expiryStage
			changed = true
		} else if expiryStage == 0 && u.ExpiryAlertStage != 0 {
			u.ExpiryAlertStage = 0
			changed = true
		}
	}
	return changed
}
