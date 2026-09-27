package main

import (
	"fmt"
	"net"
	"sort"
	"time"
)

const sourceIPArchiveLimit = 100

// Source addresses remain observable for usage diagnostics. They never change
// authorization, trigger alerts, or request a configuration apply.
func recordUserSourceIP(_ *State, u *User, nodeName, ip string, now time.Time) bool {
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
	u.SourceIPs[ip] = stat
	trimSourceIPs(u.SourceIPs, sourceIPArchiveLimit)
	return true
}

func recordDeviceSourceIP(_ *State, _ *User, device *Device, nodeName, ip string, now time.Time) bool {
	if device == nil || net.ParseIP(ip) == nil {
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
		result = append(result, fmt.Sprintf("%s · %d 次 · %s · %s", item.ip, item.stat.Count, item.stat.LastNode, formatDisplayTime(item.stat.LastSeen)))
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
		result = append(result, fmt.Sprintf("%s · %d 次 · %s", item.ip, item.stat.Count, formatDisplayTime(item.stat.LastSeen)))
	}
	return result
}
