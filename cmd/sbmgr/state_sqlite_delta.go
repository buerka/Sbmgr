package main

import (
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
)

// sqliteHistorySnapshot is an immutable, process-local optimization. It is
// trusted only while both the file path and authenticated state hash still
// match. A copied/imported state or intervening write always takes the complete
// reconciliation path. No cache is a substitute for the persisted state hash.
type sqliteHistorySnapshot struct {
	path      string
	stateHash string
	users     map[string]map[string][sha256.Size]byte
}

var sqliteHistoryTables = []string{
	"user_source_ips", "traffic_samples", "usage_history", "billing_history",
	"recent_accesses", "device_source_ips", "node_destinations",
}

func snapshotSQLiteHistory(path, stateHash string, state *State) (*sqliteHistorySnapshot, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	snapshot := &sqliteHistorySnapshot{path: absolute, stateHash: stateHash, users: make(map[string]map[string][sha256.Size]byte, len(state.Users))}
	for _, user := range state.Users {
		devices := make([]struct {
			Name string
			IPs  map[string]SourceIPStat
		}, len(user.Devices))
		for index, device := range user.Devices {
			devices[index].Name, devices[index].IPs = device.Name, device.SourceIPs
		}
		nodes := make([]struct {
			Name, Device string
			Destinations map[string]AccessStat
		}, len(user.Nodes))
		for index, node := range user.Nodes {
			nodes[index].Name, nodes[index].Device, nodes[index].Destinations = node.Name, node.Device, node.Destinations
		}
		values := []any{user.SourceIPs, user.TrafficSamples, user.UsageHistory, user.BillingHistory, user.RecentAccesses, devices, nodes}
		hashes := make(map[string][sha256.Size]byte, len(values))
		for index, value := range values {
			raw, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			hashes[sqliteHistoryTables[index]] = sha256.Sum256(raw)
		}
		snapshot.users[sqliteNameKey(user.Name)] = hashes
	}
	return snapshot, nil
}

type sqliteHistoryChanges map[string]map[string]bool

func changedSQLiteHistory(previous, next *sqliteHistorySnapshot, oldStateHash string) sqliteHistoryChanges {
	if previous == nil || previous.path != next.path || previous.stateHash != oldStateHash {
		return nil
	}
	changes := make(sqliteHistoryChanges, len(next.users))
	for user, hashes := range next.users {
		changed := make(map[string]bool, len(sqliteHistoryTables))
		before := previous.users[user]
		for table, digest := range hashes {
			previousDigest, exists := before[table]
			changed[table] = !exists || previousDigest != digest
		}
		changes[user] = changed
	}
	return changes
}

func (changes sqliteHistoryChanges) includes(user, table string) bool {
	if changes == nil {
		return true
	}
	changed, exists := changes[sqliteNameKey(user)]
	return !exists || changed[table]
}
