package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

func snapshotUserTrafficCounterBaselines(s *State, u *User) error {
	if s.Counters == nil {
		s.Counters = map[string]int64{}
	}
	authUsers := make(map[string]bool, len(u.Nodes))
	for _, node := range u.Nodes {
		authUsers[node.AuthUser] = true
	}
	if strings.TrimSpace(s.StatsAPI) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stats, err := queryUserStats(ctx, s.StatsAPI)
		if err != nil {
			return fmt.Errorf("查询 V2Ray 统计 API: %w", err)
		}
		for _, item := range stats {
			authUser, _, ok := parseUserStatName(item.Name)
			if ok && authUsers[authUser] {
				s.Counters[item.Name] = item.Value
			}
		}
		return nil
	}
	if runtime.GOOS != "linux" || len(u.Nodes) == 0 {
		return nil
	}
	counters, err := readNftRateCounters()
	if err != nil {
		return err
	}
	applyUserNftCounterBaselines(s, u, counters)
	return nil
}

func applyUserNftCounterBaselines(s *State, u *User, counters map[string]int64) {
	if s.Counters == nil {
		s.Counters = map[string]int64{}
	}
	for _, node := range u.Nodes {
		for _, direction := range []string{"upload", "download"} {
			comment := deviceNodeLabel(u.Name, node.Device, node.Name) + " " + direction
			if current, ok := counters[comment]; ok {
				s.Counters[fmt.Sprintf("nft:%08x:%s", node.RateMark, direction)] = current
			}
		}
	}
}

type nftJSONDocument struct {
	Nftables []struct {
		Rule *struct {
			Comment string            `json:"comment"`
			Expr    []json.RawMessage `json:"expr"`
		} `json:"rule,omitempty"`
	} `json:"nftables"`
}

type trafficDelta struct {
	upload   int64
	download int64
}

const (
	// sing-box does not always emit a close line for every multiplexed stream.
	// Treat only recently observed streams as active so historical log entries do
	// not trigger concurrency enforcement or block dynamic IP handover.
	activeConnectionTTL = 5 * time.Minute
	pendingSourceTTL    = 2 * time.Minute
	pendingSourceLimit  = 4096
	recentAccessLimit   = 1000
	recentAccessWindow  = 7 * 24 * time.Hour
)

func syncNftUsage(s *State) (int64, bool, error) {
	return syncNftUsageAt(s, time.Now(), 0)
}

// syncNftUsageAt accepts the elapsed time since the preceding successful nft
// sample. The daemon keeps that clock in memory so an idle node can avoid
// persistent timestamp writes while its first resumed rate still uses the real
// polling window. A zero interval retains the persisted-timestamp fallback for
// one-shot/admin callers and the daemon's first sample after a restart.
func syncNftUsageAt(s *State, now time.Time, sampleInterval time.Duration) (int64, bool, error) {
	if runtime.GOOS != "linux" || !hasManagedNodes(s) {
		return 0, false, nil
	}
	counters, err := readNftRateCounters()
	if err != nil {
		return 0, false, err
	}
	if s.Counters == nil {
		s.Counters = map[string]int64{}
	}
	var added int64
	changed := false
	nodeDeltas := map[string]trafficDelta{}
	for userIndex := range s.Users {
		u := &s.Users[userIndex]
		var userAdded int64
		for nodeIndex := range u.Nodes {
			n := &u.Nodes[nodeIndex]
			for _, direction := range []string{"upload", "download"} {
				comment := deviceNodeLabel(u.Name, n.Device, n.Name) + " " + direction
				current, ok := counters[comment]
				if !ok {
					continue
				}
				key := fmt.Sprintf("nft:%08x:%s", n.RateMark, direction)
				previous := s.Counters[key]
				delta := current - previous
				if delta < 0 {
					delta = current // The nft table was atomically replaced.
				}
				if delta > 0 {
					nodeDelta := nodeDeltas[n.AuthUser]
					if direction == "upload" {
						n.Upload += delta
						u.Upload += delta
						nodeDelta.upload += delta
					} else {
						n.Download += delta
						u.Download += delta
						nodeDelta.download += delta
					}
					nodeDeltas[n.AuthUser] = nodeDelta
					added += delta
					userAdded += delta
				}
				if previous != current {
					s.Counters[key] = current
					changed = true
				}
			}
		}
		if appendTrafficSample(u, userAdded, now) {
			changed = true
		}
	}
	if recordRealtimeUsageAt(s, nodeDeltas, now, sampleInterval) {
		changed = true
	}
	return added, changed, nil
}

func recordRealtimeUsage(s *State, deltas map[string]trafficDelta, now time.Time) bool {
	return recordRealtimeUsageAt(s, deltas, now, 0)
}

func recordRealtimeUsageAt(s *State, deltas map[string]trafficDelta, now time.Time, sampleInterval time.Duration) bool {
	changed := false
	nowNano := now.Format(time.RFC3339Nano)
	nowSecond := now.Format(time.RFC3339)
	for userIndex := range s.Users {
		u := &s.Users[userIndex]
		var userDelta trafficDelta
		var currentUpload, currentDownload float64
		for nodeIndex := range u.Nodes {
			node := &u.Nodes[nodeIndex]
			delta := deltas[node.AuthUser]
			userDelta.upload += delta.upload
			userDelta.download += delta.download
			hasDelta := delta.upload > 0 || delta.download > 0
			wasMoving := node.CurrentUploadMbps != 0 || node.CurrentDownloadMbps != 0
			if hasDelta || wasMoving {
				previous, err := time.Parse(time.RFC3339Nano, node.RateUpdatedAt)
				seconds := now.Sub(previous).Seconds()
				if hasDelta && sampleInterval > 0 {
					seconds = sampleInterval.Seconds()
				}
				uploadMbps, downloadMbps := 0.0, 0.0
				validSampleWindow := sampleInterval > 0 || err == nil
				if hasDelta && validSampleWindow && seconds > 0 && seconds <= 10*time.Minute.Seconds() {
					uploadMbps = float64(delta.upload) * 8 / seconds / 1_000_000
					downloadMbps = float64(delta.download) * 8 / seconds / 1_000_000
				}
				if node.CurrentUploadMbps != uploadMbps || node.CurrentDownloadMbps != downloadMbps || node.RateUpdatedAt != nowNano {
					changed = true
				}
				node.CurrentUploadMbps = uploadMbps
				node.CurrentDownloadMbps = downloadMbps
				node.RateUpdatedAt = nowNano
			}
			currentUpload += node.CurrentUploadMbps
			currentDownload += node.CurrentDownloadMbps
		}
		wasMoving := u.CurrentUploadMbps != 0 || u.CurrentDownloadMbps != 0
		if u.CurrentUploadMbps != currentUpload || u.CurrentDownloadMbps != currentDownload {
			changed = true
		}
		u.CurrentUploadMbps = currentUpload
		u.CurrentDownloadMbps = currentDownload
		hasDelta := userDelta.upload > 0 || userDelta.download > 0
		stopped := wasMoving && currentUpload == 0 && currentDownload == 0
		if !hasDelta && !stopped {
			continue
		}
		point := UsagePoint{At: nowSecond, UploadBytes: userDelta.upload, DownloadBytes: userDelta.download, UploadMbps: currentUpload, DownloadMbps: currentDownload}
		if len(u.UsageHistory) == 0 {
			u.UsageHistory = append(u.UsageHistory, point)
			changed = true
		} else {
			last := &u.UsageHistory[len(u.UsageHistory)-1]
			at, err := time.Parse(time.RFC3339, last.At)
			if err != nil || now.Sub(at) >= 5*time.Minute {
				u.UsageHistory = append(u.UsageHistory, point)
			} else {
				last.UploadBytes += point.UploadBytes
				last.DownloadBytes += point.DownloadBytes
				last.UploadMbps = point.UploadMbps
				last.DownloadMbps = point.DownloadMbps
			}
			changed = true
		}
		if len(u.UsageHistory) > 288 {
			u.UsageHistory = append([]UsagePoint(nil), u.UsageHistory[len(u.UsageHistory)-288:]...)
		}
	}
	return changed
}

func parseNftCounterJSON(out []byte) (map[string]int64, error) {
	var document nftJSONDocument
	if err := json.Unmarshal(out, &document); err != nil {
		return nil, fmt.Errorf("解析 nftables 用量计数: %w", err)
	}
	counters := map[string]int64{}
	for _, item := range document.Nftables {
		if item.Rule == nil || item.Rule.Comment == "" {
			continue
		}
		for _, raw := range item.Rule.Expr {
			var expression struct {
				Counter *struct {
					Bytes int64 `json:"bytes"`
				} `json:"counter,omitempty"`
			}
			if json.Unmarshal(raw, &expression) == nil && expression.Counter != nil {
				counters[item.Rule.Comment] = expression.Counter.Bytes
			}
		}
	}
	return counters, nil
}

type journalRecord struct {
	Message   json.RawMessage `json:"MESSAGE"`
	Cursor    string          `json:"__CURSOR"`
	Timestamp string          `json:"__REALTIME_TIMESTAMP"`
}

var (
	ansiEscapePattern   = regexp.MustCompile("\\x1b\\[[0-9;]*m")
	connectionIDPattern = regexp.MustCompile(`\[(\d+)\s+[^\]]+\]`)
)

func syncAccessStats(s *State) (int, bool, error) {
	if runtime.GOOS != "linux" {
		return 0, false, nil
	}
	args := []string{"-u", s.Service, "-o", "json", "--no-pager"}
	baseline := s.JournalCursor == ""
	if baseline {
		args = append(args, "-n", "1")
	} else {
		args = append(args, "--after-cursor", s.JournalCursor)
	}
	out, err := exec.Command("journalctl", args...).Output()
	if err != nil && !baseline {
		// A vacuumed journal can invalidate a saved cursor. Re-baseline at the
		// newest record instead of failing every future maintenance cycle.
		baseline = true
		out, err = exec.Command("journalctl", "-u", s.Service, "-o", "json", "--no-pager", "-n", "1").Output()
	}
	if err != nil {
		return 0, false, fmt.Errorf("读取 sing-box 访问日志: %w", err)
	}
	type authOwner struct {
		user   *User
		device *Device
		node   *Node
	}
	authOwners := map[string]authOwner{}
	for i := range s.Users {
		for j := range s.Users[i].Nodes {
			node := &s.Users[i].Nodes[j]
			authOwners[node.AuthUser] = authOwner{user: &s.Users[i], device: findDevice(&s.Users[i], node.Device), node: node}
		}
	}
	if s.PendingSources == nil {
		s.PendingSources = map[string]PendingSource{}
	}
	if s.ActiveConnections == nil {
		s.ActiveConnections = map[string]ActiveConnection{}
	}
	count, changed := 0, false
	for index := range s.Users {
		if pruneRecentAccesses(&s.Users[index], time.Now()) {
			changed = true
		}
	}
	if pruneConnectionTracking(s, time.Now()) {
		changed = true
	}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		var record journalRecord
		if json.Unmarshal(scanner.Bytes(), &record) != nil {
			continue
		}
		if record.Cursor != "" && record.Cursor != s.JournalCursor {
			s.JournalCursor = record.Cursor
			changed = true
		}
		if baseline {
			continue
		}
		message := decodeJournalMessage(record.Message)
		observedAt := journalTime(record.Timestamp)
		connectionID := parseConnectionID(message)
		if connectionID != "" && connectionClosed(message) {
			if _, exists := s.ActiveConnections[connectionID]; exists {
				delete(s.ActiveConnections, connectionID)
				changed = true
			}
			if _, exists := s.PendingSources[connectionID]; exists {
				delete(s.PendingSources, connectionID)
				changed = true
			}
			continue
		}
		if sourceIP, ok := parseSourceLog(message); ok && connectionID != "" {
			s.PendingSources[connectionID] = PendingSource{IP: sourceIP, At: observedAt.Format(time.RFC3339Nano)}
			if attachSourceToKnownConnection(s, connectionID, sourceIP, observedAt) {
				delete(s.PendingSources, connectionID)
			}
			trimPendingSources(s.PendingSources, pendingSourceLimit)
			changed = true
		}
		auth, target, ok := parseAccessLog(message)
		if !ok {
			continue
		}
		pending, hasPendingSource := freshPendingSource(s.PendingSources[connectionID], observedAt)
		if hasPendingSource {
			delete(s.PendingSources, connectionID)
			changed = true
		}
		owner, exists := authOwners[auth]
		if !exists {
			continue
		}
		node := owner.node
		connection := s.ActiveConnections[connectionID]
		if connection.StartedAt == "" {
			connection.StartedAt = observedAt.Format(time.RFC3339)
			if hasPendingSource {
				if started, err := time.Parse(time.RFC3339Nano, pending.At); err == nil {
					connection.StartedAt = started.Format(time.RFC3339)
				}
			}
		}
		connection.ID = connectionID
		connection.User = owner.user.Name
		connection.Node = node.Name
		connection.AuthUser = auth
		connection.Target = target
		connection.LastSeen = observedAt.Format(time.RFC3339)
		if owner.device != nil {
			connection.Device = owner.device.Name
		}
		if hasPendingSource {
			connection.SourceIP = pending.IP
		}
		if connectionID != "" {
			s.ActiveConnections[connectionID] = connection
		}
		if node.Destinations == nil {
			node.Destinations = map[string]AccessStat{}
		}
		stat := node.Destinations[target]
		stat.Count++
		stat.LastSeen = journalTime(record.Timestamp).Format(time.RFC3339)
		node.Destinations[target] = stat
		trimDestinations(node.Destinations, 50)
		recordRecentAccess(owner.user, owner.device, node.Name, target, observedAt)
		if hasPendingSource {
			if recordUserSourceIP(s, owner.user, node.Name, pending.IP, observedAt) {
				changed = true
			}
			if recordDeviceSourceIP(s, owner.user, owner.device, node.Name, pending.IP, observedAt) {
				changed = true
			}
		}
		count++
		changed = true
	}
	if err := scanner.Err(); err != nil {
		return count, changed, err
	}
	if pruneConnectionTracking(s, time.Now()) {
		changed = true
	}
	return count, changed, nil
}

func recordRecentAccess(u *User, device *Device, nodeName, target string, observedAt time.Time) {
	if u == nil || strings.TrimSpace(target) == "" {
		return
	}
	deviceName := ""
	if device != nil {
		deviceName = device.Name
	}
	pruneRecentAccesses(u, observedAt)
	for index := range u.RecentAccesses {
		item := &u.RecentAccesses[index]
		if strings.EqualFold(item.Target, target) && strings.EqualFold(item.Device, deviceName) && strings.EqualFold(item.Node, nodeName) {
			item.Count++
			item.LastSeen = observedAt.Format(time.RFC3339)
			sortRecentAccesses(u.RecentAccesses)
			return
		}
	}
	u.RecentAccesses = append(u.RecentAccesses, RecentAccess{
		Target: target, Device: deviceName, Node: nodeName,
		FirstSeen: observedAt.Format(time.RFC3339), LastSeen: observedAt.Format(time.RFC3339), Count: 1,
	})
	sortRecentAccesses(u.RecentAccesses)
	if len(u.RecentAccesses) > recentAccessLimit {
		u.RecentAccesses = append([]RecentAccess(nil), u.RecentAccesses[:recentAccessLimit]...)
	}
}

func pruneRecentAccesses(u *User, now time.Time) bool {
	if u == nil || len(u.RecentAccesses) == 0 {
		return false
	}
	cutoff := now.Add(-recentAccessWindow)
	kept := u.RecentAccesses[:0]
	for _, item := range u.RecentAccesses {
		lastSeen, err := time.Parse(time.RFC3339, item.LastSeen)
		if err == nil && !lastSeen.Before(cutoff) && !lastSeen.After(now.Add(30*time.Second)) && strings.TrimSpace(item.Target) != "" {
			kept = append(kept, item)
		}
	}
	changed := len(kept) != len(u.RecentAccesses)
	u.RecentAccesses = kept
	sortRecentAccesses(u.RecentAccesses)
	if len(u.RecentAccesses) > recentAccessLimit {
		u.RecentAccesses = append([]RecentAccess(nil), u.RecentAccesses[:recentAccessLimit]...)
		changed = true
	}
	return changed
}

func sortRecentAccesses(values []RecentAccess) {
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].LastSeen != values[j].LastSeen {
			return values[i].LastSeen > values[j].LastSeen
		}
		if !strings.EqualFold(values[i].Target, values[j].Target) {
			return strings.ToLower(values[i].Target) < strings.ToLower(values[j].Target)
		}
		if !strings.EqualFold(values[i].Device, values[j].Device) {
			return strings.ToLower(values[i].Device) < strings.ToLower(values[j].Device)
		}
		return strings.ToLower(values[i].Node) < strings.ToLower(values[j].Node)
	})
}

func recentAccessesForUser(u *User, filter string) []RecentAccess {
	if u == nil {
		return nil
	}
	values := append([]RecentAccess(nil), u.RecentAccesses...)
	sortRecentAccesses(values)
	needle := strings.ToLower(strings.TrimSpace(filter))
	if needle == "" {
		return values
	}
	filtered := values[:0]
	for _, item := range values {
		haystack := strings.ToLower(strings.Join([]string{item.Target, item.Device, item.Node}, "\x00"))
		if strings.Contains(haystack, needle) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func seedRecentAccessesFromNodeStats(s *State, now time.Time) {
	if s == nil {
		return
	}
	for userIndex := range s.Users {
		u := &s.Users[userIndex]
		if len(u.RecentAccesses) > 0 {
			pruneRecentAccesses(u, now)
			continue
		}
		for _, node := range u.Nodes {
			for target, stat := range node.Destinations {
				seen, err := time.Parse(time.RFC3339, stat.LastSeen)
				if err != nil || seen.Before(now.Add(-recentAccessWindow)) || seen.After(now.Add(30*time.Second)) {
					continue
				}
				count := stat.Count
				if count <= 0 {
					count = 1
				}
				u.RecentAccesses = append(u.RecentAccesses, RecentAccess{
					Target: target, Device: node.Device, Node: node.Name,
					FirstSeen: stat.LastSeen, LastSeen: stat.LastSeen, Count: count,
				})
			}
		}
		sortRecentAccesses(u.RecentAccesses)
		if len(u.RecentAccesses) > recentAccessLimit {
			u.RecentAccesses = append([]RecentAccess(nil), u.RecentAccesses[:recentAccessLimit]...)
		}
	}
}

func validateRecentAccesses(u User) error {
	if len(u.RecentAccesses) > recentAccessLimit {
		return fmt.Errorf("近期访问记录有 %d 条，超过上限 %d", len(u.RecentAccesses), recentAccessLimit)
	}
	for _, item := range u.RecentAccesses {
		if strings.TrimSpace(item.Target) == "" || item.Count <= 0 {
			return errors.New("近期访问记录存在空目标或非正访问次数")
		}
		first, firstErr := time.Parse(time.RFC3339, item.FirstSeen)
		last, lastErr := time.Parse(time.RFC3339, item.LastSeen)
		if firstErr != nil || lastErr != nil || first.After(last) {
			return fmt.Errorf("目标 %s 的近期访问时间无效", item.Target)
		}
	}
	return nil
}

func freshPendingSource(pending PendingSource, observedAt time.Time) (PendingSource, bool) {
	at, err := time.Parse(time.RFC3339Nano, pending.At)
	if err != nil || pending.IP == "" {
		return PendingSource{}, false
	}
	age := observedAt.Sub(at)
	return pending, age >= -5*time.Second && age <= pendingSourceTTL
}

func connectionActiveAt(connection ActiveConnection, now time.Time) bool {
	return connectionActiveWithinAt(connection, now, activeConnectionTTL)
}

func connectionActiveWithinAt(connection ActiveConnection, now time.Time, ttl time.Duration) bool {
	lastSeen, err := time.Parse(time.RFC3339, connection.LastSeen)
	return ttl >= 0 && err == nil && !lastSeen.After(now.Add(30*time.Second)) && now.Sub(lastSeen) <= ttl
}

func pruneConnectionTracking(s *State, now time.Time) bool {
	changed := false
	for id, pending := range s.PendingSources {
		at, err := time.Parse(time.RFC3339Nano, pending.At)
		if err != nil || now.Sub(at) > pendingSourceTTL || at.After(now.Add(30*time.Second)) {
			delete(s.PendingSources, id)
			changed = true
		}
	}
	for id, connection := range s.ActiveConnections {
		if !connectionActiveAt(connection, now) {
			delete(s.ActiveConnections, id)
			changed = true
		}
	}
	return changed
}

// attachSourceToKnownConnection handles the journal ordering where the access
// line arrives before the inbound source line. Previously that order updated
// only the transient connection record and silently skipped IP archiving and
// enforcement.
func attachSourceToKnownConnection(s *State, connectionID, sourceIP string, observedAt time.Time) bool {
	connection, exists := s.ActiveConnections[connectionID]
	if !exists || net.ParseIP(sourceIP) == nil {
		return false
	}
	previousIP := connection.SourceIP
	connection.SourceIP = net.ParseIP(sourceIP).String()
	connection.LastSeen = observedAt.Format(time.RFC3339)
	s.ActiveConnections[connectionID] = connection
	if previousIP == connection.SourceIP {
		return true
	}
	u := findUser(s, connection.User)
	if u == nil {
		return true
	}
	recordUserSourceIP(s, u, connection.Node, connection.SourceIP, observedAt)
	recordDeviceSourceIP(s, u, findDevice(u, connection.Device), connection.Node, connection.SourceIP, observedAt)
	return true
}

func connectionClosed(message string) bool {
	message = strings.ToLower(cleanLogMessage(message))
	return strings.Contains(message, "connection closed") || strings.Contains(message, "connection close:") || strings.Contains(message, "closed connection")
}

func trimPendingSources(values map[string]PendingSource, limit int) {
	if len(values) <= limit {
		return
	}
	type item struct {
		id string
		at string
	}
	items := make([]item, 0, len(values))
	for id, pending := range values {
		items = append(items, item{id: id, at: pending.At})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].at > items[j].at })
	keep := map[string]bool{}
	for _, item := range items[:limit] {
		keep[item.id] = true
	}
	for id := range values {
		if !keep[id] {
			delete(values, id)
		}
	}
}

func decodeJournalMessage(raw json.RawMessage) string {
	var data []byte
	if json.Unmarshal(raw, &data) == nil {
		return string(data)
	}
	var message string
	if json.Unmarshal(raw, &message) == nil {
		return message
	}
	return ""
}

func parseAccessLog(message string) (auth, target string, ok bool) {
	message = cleanLogMessage(message)
	marker := "] inbound connection to "
	if strings.Contains(message, "] inbound packet connection to ") {
		marker = "] inbound packet connection to "
	}
	position := strings.Index(message, marker)
	if position < 0 {
		return "", "", false
	}
	left := message[:position]
	open := strings.LastIndex(left, "[")
	if open < 0 || open+1 >= len(left) {
		return "", "", false
	}
	auth = strings.TrimSpace(left[open+1:])
	fields := strings.Fields(message[position+len(marker):])
	if len(fields) == 0 {
		return "", "", false
	}
	target = fields[0]
	if host, _, err := net.SplitHostPort(target); err == nil {
		target = host
	} else if colon := strings.LastIndex(target, ":"); colon > 0 {
		if _, err := strconv.Atoi(target[colon+1:]); err == nil {
			target = target[:colon]
		}
	}
	return auth, strings.Trim(target, "[]"), auth != "" && target != ""
}

func cleanLogMessage(message string) string {
	return ansiEscapePattern.ReplaceAllString(message, "")
}

func parseConnectionID(message string) string {
	match := connectionIDPattern.FindStringSubmatch(cleanLogMessage(message))
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func parseSourceLog(message string) (string, bool) {
	message = cleanLogMessage(message)
	marker := "inbound connection from "
	position := strings.Index(message, marker)
	if position < 0 {
		return "", false
	}
	fields := strings.Fields(message[position+len(marker):])
	if len(fields) == 0 {
		return "", false
	}
	address := strings.TrimSpace(fields[0])
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return "", false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return "", false
	}
	return ip.String(), true
}

func journalTime(raw string) time.Time {
	microseconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Now()
	}
	return time.UnixMicro(microseconds)
}

func trimDestinations(destinations map[string]AccessStat, limit int) {
	if len(destinations) <= limit {
		return
	}
	type item struct {
		name string
		stat AccessStat
	}
	items := make([]item, 0, len(destinations))
	for name, stat := range destinations {
		items = append(items, item{name: name, stat: stat})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].stat.Count != items[j].stat.Count {
			return items[i].stat.Count > items[j].stat.Count
		}
		return items[i].stat.LastSeen > items[j].stat.LastSeen
	})
	keep := map[string]bool{}
	for _, item := range items[:limit] {
		keep[item.name] = true
	}
	for name := range destinations {
		if !keep[name] {
			delete(destinations, name)
		}
	}
}

func topDestinations(node Node, limit int) []string {
	if limit <= 0 || len(node.Destinations) == 0 {
		return nil
	}
	type item struct {
		name string
		stat AccessStat
	}
	items := make([]item, 0, len(node.Destinations))
	for name, stat := range node.Destinations {
		items = append(items, item{name: name, stat: stat})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].stat.Count != items[j].stat.Count {
			return items[i].stat.Count > items[j].stat.Count
		}
		return items[i].stat.LastSeen > items[j].stat.LastSeen
	})
	if len(items) > limit {
		items = items[:limit]
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, fmt.Sprintf("%s (%d)", item.name, item.stat.Count))
	}
	return result
}
