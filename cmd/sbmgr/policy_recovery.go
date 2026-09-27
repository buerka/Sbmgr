package main

import (
	"fmt"
	"time"
)

const connectionBlockDuration = 10 * time.Minute

func migrateSecurityPolicies(s *State, now time.Time) {
	for i := range s.Users {
		u := &s.Users[i]
		if !u.Enabled && u.DisabledReason == "connections" {
			u.Access.ConnectionBlockedUntil = now.Add(connectionBlockDuration).Format(time.RFC3339Nano)
		}
	}
}

func recoverConnectionBlocks(s *State, now time.Time) bool {
	changed := false
	for i := range s.Users {
		u := &s.Users[i]
		if until, err := time.Parse(time.RFC3339Nano, u.Access.ConnectionBlockedUntil); err == nil && !now.Before(until) {
			if !u.Enabled && u.DisabledReason == "connections" {
				u.Enabled = true
				u.DisabledReason = ""
				changed = true
			}
			u.Access.ConnectionBlockedUntil = ""
			changed = true
			appendAlert(s, Alert{At: now.Format(time.RFC3339), User: u.Name, Kind: "connection_recovered", Message: "并发连接临时封禁到期，已重新检查用户资格；后台自动应用"})
		}
		for j := range u.Devices {
			d := &u.Devices[j]
			if until, err := time.Parse(time.RFC3339Nano, d.Access.ConnectionBlockedUntil); err == nil && !now.Before(until) {
				d.Enabled = true
				d.Access.ConnectionBlockedUntil = ""
				changed = true
				appendAlert(s, Alert{At: now.Format(time.RFC3339), User: u.Name, Kind: "connection_recovered", Message: fmt.Sprintf("设备 %s 的并发连接临时封禁到期，后台自动应用", d.Name)})
			}
		}
	}
	if changed {
		s.StatsApplyPending = true
	}
	return changed
}
