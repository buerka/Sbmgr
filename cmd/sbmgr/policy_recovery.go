package main

import (
	"fmt"
	"time"
)

const autoBindingTTL = 24 * time.Hour
const connectionBlockDuration = 10 * time.Minute

// Start the idle clock for explicitly prefilled automatic/dynamic bindings as
// well as learned bindings. Subsequent saves preserve their original clock.
func initializeBindingActivity(s *State, now time.Time) {
	initialize := func(p *IPPolicy) {
		if p.Binding == "manual" || len(p.BoundIPs) == 0 {
			return
		}
		if p.BoundLastSeen == nil {
			p.BoundLastSeen = map[string]string{}
		}
		for _, ip := range p.BoundIPs {
			if p.BoundLastSeen[ip] == "" {
				p.BoundLastSeen[ip] = now.Format(time.RFC3339Nano)
			}
		}
	}
	for i := range s.Users {
		initialize(&s.Users[i].IPPolicy)
		for j := range s.Users[i].Devices {
			initialize(&s.Users[i].Devices[j].IPPolicy)
		}
	}
}

func migrateSecurityPolicies(s *State, now time.Time) {
	seed := func(p *IPPolicy, sources map[string]SourceIPStat) {
		if len(p.BoundIPs) == 0 {
			return
		}
		p.BoundLastSeen = map[string]string{}
		for _, ip := range p.BoundIPs {
			last := sources[ip].LastSeen
			if _, err := time.Parse(time.RFC3339Nano, last); err != nil {
				last = now.Format(time.RFC3339Nano)
			}
			p.BoundLastSeen[ip] = last
		}
	}
	for i := range s.Users {
		u := &s.Users[i]
		seed(&u.IPPolicy, u.SourceIPs)
		if !u.Enabled && u.DisabledReason == "connections" {
			u.Access.ConnectionBlockedUntil = now.Add(connectionBlockDuration).Format(time.RFC3339Nano)
		}
		for j := range u.Devices {
			seed(&u.Devices[j].IPPolicy, u.Devices[j].SourceIPs)
		}
		// Old device state did not distinguish manual disablement from a
		// connection punishment. Never re-enable such devices by inference.
	}
}

func rememberBoundActivity(p *IPPolicy, ip string, now time.Time) {
	last := make(map[string]string, len(p.BoundIPs))
	for _, bound := range p.BoundIPs {
		if value := p.BoundLastSeen[bound]; value != "" {
			last[bound] = value
		}
		if bound == ip {
			last[bound] = now.Format(time.RFC3339Nano)
		}
	}
	p.BoundLastSeen = last
}

func releaseIdleAutoBindings(p *IPPolicy, now time.Time) bool {
	if !p.Enabled || p.Binding != "auto" {
		return false
	}
	kept := make([]string, 0, len(p.BoundIPs))
	changed := false
	for _, ip := range p.BoundIPs {
		last, err := time.Parse(time.RFC3339Nano, p.BoundLastSeen[ip])
		if err == nil && now.Sub(last) >= autoBindingTTL {
			changed = true
			continue
		}
		kept = append(kept, ip)
	}
	if changed {
		p.BoundIPs = kept
		rememberBoundActivity(p, "", now)
	}
	return changed
}

func releaseIdleBindings(s *State, now time.Time) bool {
	changed := false
	for i := range s.Users {
		u := &s.Users[i]
		released := releaseIdleAutoBindings(&u.IPPolicy, now)
		for j := range u.Devices {
			released = releaseIdleAutoBindings(&u.Devices[j].IPPolicy, now) || released
		}
		if released {
			appendAlert(s, Alert{At: now.Format(time.RFC3339), User: u.Name, Kind: "ip_binding_expired", Message: fmt.Sprintf("用户 %s 已释放连续 24 小时无活动的自动 IP 绑定；后台自动应用", u.Name)})
			changed = true
		}
	}
	if changed {
		s.IPApplyPending = true
	}
	return changed
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
