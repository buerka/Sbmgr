package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	moderncsqlite "modernc.org/sqlite"
)

const (
	sqliteSchemaVersion = 2
	sqliteApplicationID = 0x53424d47 // "SBMG"
	sqliteFormatMarker  = "sbmgr-state-v1"
)

var sqliteSchema = []string{
	`CREATE TABLE IF NOT EXISTS metadata (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	) STRICT`,
	`CREATE TABLE IF NOT EXISTS settings (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		document TEXT NOT NULL CHECK (json_valid(document) AND json_type(document) = 'object'),
		updated_at TEXT NOT NULL
	) STRICT`,
	`CREATE TABLE IF NOT EXISTS runtime_state (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		journal_cursor TEXT NOT NULL,
		ip_apply_pending INTEGER NOT NULL CHECK (ip_apply_pending IN (0, 1)),
		burst_apply_pending INTEGER NOT NULL CHECK (burst_apply_pending IN (0, 1)),
		rate_apply_pending INTEGER NOT NULL CHECK (rate_apply_pending IN (0, 1)),
		stats_apply_pending INTEGER NOT NULL CHECK (stats_apply_pending IN (0, 1)),
		last_health_check TEXT NOT NULL
	) STRICT`,
	`CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY,
		ordinal INTEGER NOT NULL,
		name TEXT NOT NULL,
		name_key TEXT NOT NULL UNIQUE,
		enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
		quota_bytes INTEGER NOT NULL,
		quota_mode TEXT NOT NULL,
		extra_quota_bytes INTEGER NOT NULL,
		expires TEXT NOT NULL,
		upload_bytes INTEGER NOT NULL,
		download_bytes INTEGER NOT NULL,
		upload_mbps REAL NOT NULL,
		download_mbps REAL NOT NULL,
		rate_mark INTEGER NOT NULL,
		throttle_json TEXT NOT NULL CHECK (json_valid(throttle_json) AND json_type(throttle_json) = 'object'),
		burst_json TEXT NOT NULL CHECK (json_valid(burst_json) AND json_type(burst_json) = 'object'),
		ip_policy_json TEXT NOT NULL CHECK (json_valid(ip_policy_json) AND json_type(ip_policy_json) = 'object'),
		current_upload_mbps REAL NOT NULL,
		current_download_mbps REAL NOT NULL,
		blocked_until TEXT NOT NULL,
		block_reason TEXT NOT NULL,
		disabled_reason TEXT NOT NULL,
		billing_json TEXT NOT NULL CHECK (json_valid(billing_json) AND json_type(billing_json) = 'object'),
		quota_alert_stage INTEGER NOT NULL,
		expiry_alert_stage INTEGER NOT NULL,
		access_json TEXT NOT NULL CHECK (json_valid(access_json) AND json_type(access_json) = 'object')
	) STRICT`,
	`CREATE INDEX IF NOT EXISTS users_ordinal_idx ON users(ordinal)`,
	`CREATE TABLE IF NOT EXISTS devices (
		id INTEGER PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		ordinal INTEGER NOT NULL,
		name TEXT NOT NULL,
		name_key TEXT NOT NULL,
		enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
		created_at TEXT NOT NULL,
		last_seen TEXT NOT NULL,
		ip_policy_json TEXT NOT NULL CHECK (json_valid(ip_policy_json) AND json_type(ip_policy_json) = 'object'),
		subscription_token TEXT NOT NULL,
		access_json TEXT NOT NULL CHECK (json_valid(access_json) AND json_type(access_json) = 'object'),
		UNIQUE(user_id, name_key)
	) STRICT`,
	`CREATE INDEX IF NOT EXISTS devices_user_ordinal_idx ON devices(user_id, ordinal)`,
	`CREATE INDEX IF NOT EXISTS devices_subscription_token_idx ON devices(subscription_token)`,
	`CREATE TABLE IF NOT EXISTS nodes (
		id INTEGER PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
		ordinal INTEGER NOT NULL,
		name TEXT NOT NULL,
		name_key TEXT NOT NULL,
		auth_user TEXT NOT NULL UNIQUE,
		uuid TEXT NOT NULL COLLATE NOCASE UNIQUE,
		outbound TEXT NOT NULL,
		upload_mbps REAL NOT NULL,
		download_mbps REAL NOT NULL,
		rate_mark INTEGER NOT NULL,
		upload_bytes INTEGER NOT NULL,
		download_bytes INTEGER NOT NULL,
		current_upload_mbps REAL NOT NULL,
		current_download_mbps REAL NOT NULL,
		rate_updated_at TEXT NOT NULL,
		UNIQUE(device_id, name_key)
	) STRICT`,
	`CREATE INDEX IF NOT EXISTS nodes_user_ordinal_idx ON nodes(user_id, ordinal)`,
	`CREATE TABLE IF NOT EXISTS stats_counters (
		name TEXT PRIMARY KEY,
		value INTEGER NOT NULL
	) STRICT`,
	`CREATE TABLE IF NOT EXISTS pending_sources (
		auth_user TEXT PRIMARY KEY,
		ip TEXT NOT NULL,
		observed_at TEXT NOT NULL
	) STRICT`,
	`CREATE TABLE IF NOT EXISTS active_connections (
		id TEXT PRIMARY KEY,
		user_name TEXT NOT NULL,
		device_name TEXT NOT NULL,
		node_name TEXT NOT NULL,
		auth_user TEXT NOT NULL,
		source_ip TEXT NOT NULL,
		target TEXT NOT NULL,
		started_at TEXT NOT NULL,
		last_seen TEXT NOT NULL
	) STRICT`,
	`CREATE INDEX IF NOT EXISTS active_connections_user_idx ON active_connections(user_name, last_seen)`,
	`CREATE TABLE IF NOT EXISTS outbound_health (
		tag TEXT PRIMARY KEY,
		target TEXT NOT NULL,
		healthy INTEGER NOT NULL CHECK (healthy IN (0, 1)),
		latency_ms INTEGER NOT NULL,
		failures INTEGER NOT NULL,
		checked_at TEXT NOT NULL,
		error TEXT NOT NULL
	) STRICT`,
	`CREATE TABLE IF NOT EXISTS fleet_status (
		name TEXT PRIMARY KEY COLLATE NOCASE,
		checked_at TEXT NOT NULL,
		online INTEGER NOT NULL CHECK (online IN (0, 1)),
		latency_ms INTEGER NOT NULL,
		hostname TEXT NOT NULL,
		version TEXT NOT NULL,
		users INTEGER NOT NULL,
		enabled_users INTEGER NOT NULL,
		devices INTEGER NOT NULL,
		upload_bytes INTEGER NOT NULL,
		download_bytes INTEGER NOT NULL,
		unread_alerts INTEGER NOT NULL,
		unhealthy_routes INTEGER NOT NULL,
		error TEXT NOT NULL
	) STRICT`,
	`CREATE TABLE IF NOT EXISTS alerts (
		id INTEGER PRIMARY KEY,
		stable_key TEXT NOT NULL UNIQUE,
		ordinal INTEGER NOT NULL,
		at TEXT NOT NULL,
		user_name TEXT NOT NULL,
		kind TEXT NOT NULL,
		message TEXT NOT NULL,
		acknowledged INTEGER NOT NULL CHECK (acknowledged IN (0, 1)),
		notified_at TEXT NOT NULL,
		notify_attempts INTEGER NOT NULL,
		last_notify_attempt TEXT NOT NULL,
		notify_error TEXT NOT NULL
	) STRICT`,
	`CREATE INDEX IF NOT EXISTS alerts_at_idx ON alerts(at DESC)`,
	`CREATE INDEX IF NOT EXISTS alerts_user_idx ON alerts(user_name, at DESC)`,
	`CREATE TABLE IF NOT EXISTS user_source_ips (
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		ip TEXT NOT NULL,
		count INTEGER NOT NULL,
		violations INTEGER NOT NULL,
		first_seen TEXT NOT NULL,
		last_seen TEXT NOT NULL,
		last_node TEXT NOT NULL,
		last_alert TEXT NOT NULL,
		PRIMARY KEY(user_id, ip)
	) STRICT`,
	`CREATE INDEX IF NOT EXISTS user_source_ips_last_seen_idx ON user_source_ips(user_id, last_seen DESC)`,
	`CREATE TABLE IF NOT EXISTS device_source_ips (
		device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
		ip TEXT NOT NULL,
		count INTEGER NOT NULL,
		violations INTEGER NOT NULL,
		first_seen TEXT NOT NULL,
		last_seen TEXT NOT NULL,
		last_node TEXT NOT NULL,
		last_alert TEXT NOT NULL,
		PRIMARY KEY(device_id, ip)
	) STRICT`,
	`CREATE INDEX IF NOT EXISTS device_source_ips_last_seen_idx ON device_source_ips(device_id, last_seen DESC)`,
	`CREATE TABLE IF NOT EXISTS traffic_samples (
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		stable_key TEXT NOT NULL,
		ordinal INTEGER NOT NULL,
		at TEXT NOT NULL,
		bytes INTEGER NOT NULL,
		PRIMARY KEY(user_id, stable_key)
	) STRICT`,
	`CREATE INDEX IF NOT EXISTS traffic_samples_time_idx ON traffic_samples(user_id, at)`,
	`CREATE TABLE IF NOT EXISTS usage_history (
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		stable_key TEXT NOT NULL,
		ordinal INTEGER NOT NULL,
		at TEXT NOT NULL,
		upload_bytes INTEGER NOT NULL,
		download_bytes INTEGER NOT NULL,
		upload_mbps REAL NOT NULL,
		download_mbps REAL NOT NULL,
		PRIMARY KEY(user_id, stable_key)
	) STRICT`,
	`CREATE INDEX IF NOT EXISTS usage_history_time_idx ON usage_history(user_id, at)`,
	`CREATE TABLE IF NOT EXISTS billing_history (
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		stable_key TEXT NOT NULL,
		ordinal INTEGER NOT NULL,
		started_at TEXT NOT NULL,
		ended_at TEXT NOT NULL,
		upload_bytes INTEGER NOT NULL,
		download_bytes INTEGER NOT NULL,
		quota_bytes INTEGER NOT NULL,
		PRIMARY KEY(user_id, stable_key)
	) STRICT`,
	`CREATE INDEX IF NOT EXISTS billing_history_end_idx ON billing_history(user_id, ended_at DESC)`,
	`CREATE TABLE IF NOT EXISTS recent_accesses (
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		ordinal INTEGER NOT NULL,
		target TEXT NOT NULL,
		device_name TEXT NOT NULL,
		node_name TEXT NOT NULL,
		first_seen TEXT NOT NULL,
		last_seen TEXT NOT NULL,
		count INTEGER NOT NULL,
		PRIMARY KEY(user_id, target, device_name, node_name)
	) STRICT`,
	`CREATE INDEX IF NOT EXISTS recent_accesses_target_idx ON recent_accesses(user_id, target)`,
	`CREATE INDEX IF NOT EXISTS recent_accesses_time_idx ON recent_accesses(user_id, last_seen DESC)`,
	`CREATE TABLE IF NOT EXISTS node_destinations (
		node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
		target TEXT NOT NULL,
		count INTEGER NOT NULL,
		last_seen TEXT NOT NULL,
		PRIMARY KEY(node_id, target)
	) STRICT`,
	`CREATE INDEX IF NOT EXISTS node_destinations_count_idx ON node_destinations(node_id, count DESC)`,
}

func isSQLiteStatePath(path string) bool {
	return !strings.EqualFold(filepath.Ext(strings.TrimSpace(path)), ".json")
}

func loadSQLiteStateWithCanonicalChange(path string) (*State, bool, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := importLegacyJSONState(path); err != nil {
			return nil, false, err
		}
	} else if err != nil {
		return nil, false, fmt.Errorf("读取状态数据库 %s: %w", path, err)
	}
	s, err := readSQLiteState(path)
	if err != nil {
		return nil, false, err
	}
	before, err := json.Marshal(s)
	if err != nil {
		return nil, false, err
	}
	if err := migrateState(s); err != nil {
		return nil, false, err
	}
	normalizeQuotaModes(s)
	normalizeDeviceModel(s)
	if _, err := ensureNodeMarks(s); err != nil {
		return nil, false, err
	}
	normalizeLegacyNodeNames(s)
	if err := validateState(s); err != nil {
		return nil, false, fmt.Errorf("状态数据库校验失败: %w", err)
	}
	after, err := json.Marshal(s)
	if err != nil {
		return nil, false, err
	}
	return s, !strings.EqualFold(hex.EncodeToString(hashBytes(before)), hex.EncodeToString(hashBytes(after))), nil
}

func loadSQLiteBackupState(path string) (*State, error) {
	s, err := readSQLiteStateReadOnly(path)
	if err != nil {
		return nil, err
	}
	if err := migrateState(s); err != nil {
		return nil, err
	}
	normalizeQuotaModes(s)
	normalizeDeviceModel(s)
	if _, err := ensureNodeMarks(s); err != nil {
		return nil, err
	}
	normalizeLegacyNodeNames(s)
	if err := validateState(s); err != nil {
		return nil, fmt.Errorf("SQLite 备份校验失败: %w", err)
	}
	return s, nil
}

func saveSQLiteState(path string, state *State) error {
	existed := true
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		existed = false
	} else if err != nil {
		return err
	}
	db, created, err := openSQLiteState(path)
	if err != nil {
		return err
	}
	if created {
		existed = false
	}
	complete := false
	defer func() {
		if !existed && !complete {
			removeSQLiteFiles(path)
		}
	}()
	stateHash, err := sqliteStateHash(state)
	if err != nil {
		db.Close()
		return err
	}
	controlHash, err := sqliteControlHash(state)
	if err != nil {
		db.Close()
		return err
	}
	oldStateHash, err := sqliteMetadata(db, "state_hash")
	if err != nil {
		db.Close()
		return err
	}
	oldControlHash, err := sqliteMetadata(db, "control_hash")
	if err != nil {
		db.Close()
		return err
	}
	if existed && oldStateHash == stateHash {
		if err := db.Close(); err != nil {
			return err
		}
		complete = true
		return chmodSQLiteFiles(path)
	}
	if err := db.Close(); err != nil {
		return err
	}
	if existed {
		if err := backupSQLiteBeforeWrite(path, oldControlHash != controlHash, time.Now()); err != nil {
			return fmt.Errorf("创建状态数据库备份: %w", err)
		}
	}
	db, _, err = openSQLiteState(path)
	if err != nil {
		return err
	}
	defer func() {
		_ = db.Close()
		_ = chmodSQLiteFiles(path)
	}()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := writeSQLiteState(tx, state, stateHash, controlHash); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	complete = true
	return nil
}

func openSQLiteState(path string) (*sql.DB, bool, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, false, err
	}
	created := false
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err == nil {
		created = true
		if closeErr := file.Close(); closeErr != nil {
			_ = os.Remove(path)
			return nil, false, closeErr
		}
	} else if !errors.Is(err, os.ErrExist) {
		return nil, false, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		if created {
			_ = os.Remove(path)
		}
		return nil, false, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, statement := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
		"PRAGMA trusted_schema = OFF",
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			if created {
				removeSQLiteFiles(path)
			}
			return nil, false, fmt.Errorf("配置 SQLite: %w", err)
		}
	}
	// Identify an existing file before executing persistent PRAGMAs or schema
	// statements. This prevents a mistyped --state path from converting an
	// unrelated SQLite database to WAL or adding sbmgr tables to it.
	if !created {
		if err := validateSQLiteHeader(db); err != nil {
			db.Close()
			return nil, false, fmt.Errorf("拒绝打开非 sbmgr 状态数据库 %s: %w", path, err)
		}
	}
	if err := ensureSQLiteSchema(db, created); err != nil {
		db.Close()
		if created {
			removeSQLiteFiles(path)
		}
		return nil, false, err
	}
	if err := ensureSQLiteWAL(db); err != nil {
		db.Close()
		if created {
			removeSQLiteFiles(path)
		}
		return nil, false, err
	}
	if _, err := db.Exec("PRAGMA synchronous = NORMAL"); err != nil {
		db.Close()
		if created {
			removeSQLiteFiles(path)
		}
		return nil, false, fmt.Errorf("配置 SQLite synchronous: %w", err)
	}
	if err := chmodSQLiteFiles(path); err != nil {
		db.Close()
		return nil, false, err
	}
	return db, created, nil
}

func ensureSQLiteWAL(db *sql.DB) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		var journalMode string
		if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
			return fmt.Errorf("读取 SQLite journal_mode: %w", err)
		}
		if strings.EqualFold(journalMode, "wal") {
			return nil
		}
		err := db.QueryRow("PRAGMA journal_mode = WAL").Scan(&journalMode)
		if err == nil && strings.EqualFold(journalMode, "wal") {
			return nil
		}
		if err == nil {
			err = fmt.Errorf("SQLite 返回 journal_mode %q", journalMode)
		}
		message := strings.ToLower(err.Error())
		if (strings.Contains(message, "locked") || strings.Contains(message, "busy")) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		return fmt.Errorf("配置 SQLite WAL: %w", err)
	}
}

func validateSQLiteHeader(db sqliteQuerier) error {
	var applicationID, version int
	if err := db.QueryRow("PRAGMA application_id").Scan(&applicationID); err != nil {
		return fmt.Errorf("读取 SQLite application_id: %w", err)
	}
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("读取 SQLite schema 版本: %w", err)
	}
	if applicationID != sqliteApplicationID {
		return fmt.Errorf("application_id 0x%08x 不匹配（需要 0x%08x）", uint32(applicationID), uint32(sqliteApplicationID))
	}
	if version <= 0 {
		return errors.New("缺少有效的 sbmgr schema 版本")
	}
	return nil
}

func validateSQLiteIdentity(db sqliteQuerier) error {
	if err := validateSQLiteHeader(db); err != nil {
		return err
	}
	var format string
	if err := db.QueryRow(`SELECT value FROM metadata WHERE key = 'format'`).Scan(&format); err != nil {
		return fmt.Errorf("读取状态数据库格式标记: %w", err)
	}
	if format != sqliteFormatMarker {
		return fmt.Errorf("状态数据库格式标记 %q 不匹配", format)
	}
	return nil
}

func ensureSQLiteSchema(db *sql.DB, created bool) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("读取 SQLite schema 版本: %w", err)
	}
	if version > sqliteSchemaVersion {
		return fmt.Errorf("状态数据库 schema 版本 %d 高于本程序支持的 %d", version, sqliteSchemaVersion)
	}
	for version < sqliteSchemaVersion {
		switch version {
		case 0:
			if !created {
				return errors.New("拒绝在既有、无标识 SQLite 文件中初始化 sbmgr schema")
			}
			tx, err := db.Begin()
			if err != nil {
				return err
			}
			ok := false
			defer func() {
				if !ok {
					_ = tx.Rollback()
				}
			}()
			for _, statement := range sqliteSchema {
				if _, err := tx.Exec(statement); err != nil {
					return fmt.Errorf("创建 SQLite schema: %w", err)
				}
			}
			if _, err := tx.Exec(`INSERT INTO metadata(key, value) VALUES('format', ?)`, sqliteFormatMarker); err != nil {
				return fmt.Errorf("写入 SQLite 格式标记: %w", err)
			}
			if _, err := tx.Exec(`INSERT INTO metadata(key, value) VALUES('schema_version', ?)`, fmt.Sprint(sqliteSchemaVersion)); err != nil {
				return fmt.Errorf("写入 SQLite schema 元数据: %w", err)
			}
			if _, err := tx.Exec(fmt.Sprintf("PRAGMA application_id = %d", sqliteApplicationID)); err != nil {
				return fmt.Errorf("写入 SQLite application_id: %w", err)
			}
			if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", sqliteSchemaVersion)); err != nil {
				return err
			}
			if err := tx.Commit(); err != nil {
				return err
			}
			ok = true
			version = sqliteSchemaVersion
		case 1:
			tx, err := db.Begin()
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if _, err = tx.Exec(`CREATE INDEX IF NOT EXISTS devices_subscription_token_idx ON devices(subscription_token)`); err != nil {
				return err
			}
			if _, err = tx.Exec(`UPDATE metadata SET value = '2' WHERE key = 'schema_version'`); err != nil {
				return err
			}
			if _, err = tx.Exec(`PRAGMA user_version = 2`); err != nil {
				return err
			}
			if err = tx.Commit(); err != nil {
				return err
			}
			version = 2
		default:
			return fmt.Errorf("缺少从 SQLite schema 版本 %d 开始的迁移程序", version)
		}
	}
	if err := validateSQLiteIdentity(db); err != nil {
		return fmt.Errorf("校验 SQLite 数据库身份: %w", err)
	}
	return nil
}

func verifySQLiteIntegrity(db *sql.DB) error {
	var result string
	if err := db.QueryRow("PRAGMA integrity_check(1)").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("PRAGMA integrity_check: %s", result)
	}
	return nil
}

func sqliteMetadata(db *sql.DB, key string) (string, error) {
	var value string
	err := db.QueryRow("SELECT value FROM metadata WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func sqliteStateHash(state *State) (string, error) {
	raw, err := sqliteCanonicalStateJSON(state)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hashBytes(raw)), nil
}

func sqliteControlHash(state *State) (string, error) {
	raw, err := sqliteCanonicalStateJSON(state)
	if err != nil {
		return "", err
	}
	var control State
	if err := json.Unmarshal(raw, &control); err != nil {
		return "", err
	}
	stripSQLiteRuntime(&control)
	raw, err = sqliteCanonicalStateJSON(&control)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hashBytes(raw)), nil
}

func sqliteCanonicalStateJSON(state *State) ([]byte, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	var canonical State
	if err := json.Unmarshal(raw, &canonical); err != nil {
		return nil, err
	}
	canonicalizeEmptySQLiteCollections(&canonical)
	return json.Marshal(canonical)
}

func canonicalizeEmptySQLiteCollections(state *State) {
	for key, connection := range state.ActiveConnections {
		connection.ID = key
		state.ActiveConnections[key] = connection
	}
	for key, health := range state.OutboundHealth {
		health.Tag = key
		state.OutboundHealth[key] = health
	}
	if len(state.Counters) == 0 {
		state.Counters = nil
	}
	if len(state.PendingSources) == 0 {
		state.PendingSources = nil
	}
	if len(state.ActiveConnections) == 0 {
		state.ActiveConnections = nil
	}
	if len(state.OutboundHealth) == 0 {
		state.OutboundHealth = nil
	}
	if len(state.FleetStatus) == 0 {
		state.FleetStatus = nil
	}
	if len(state.Alerts) == 0 {
		state.Alerts = nil
	}
	if len(state.Users) == 0 {
		state.Users = nil
	}
	for userIndex := range state.Users {
		user := &state.Users[userIndex]
		canonicalDevices := map[string]string{}
		for deviceIndex := range user.Devices {
			device := &user.Devices[deviceIndex]
			canonicalDevices[strings.ToLower(strings.TrimSpace(device.Name))] = device.Name
		}
		for nodeIndex := range user.Nodes {
			node := &user.Nodes[nodeIndex]
			if canonical, ok := canonicalDevices[strings.ToLower(strings.TrimSpace(node.Device))]; ok {
				node.Device = canonical
			}
		}
		if len(user.SourceIPs) == 0 {
			user.SourceIPs = nil
		}
		if len(user.Devices) == 0 {
			user.Devices = nil
		}
		if len(user.TrafficSamples) == 0 {
			user.TrafficSamples = nil
		}
		if len(user.UsageHistory) == 0 {
			user.UsageHistory = nil
		}
		if len(user.BillingHistory) == 0 {
			user.BillingHistory = nil
		}
		if len(user.RecentAccesses) == 0 {
			user.RecentAccesses = nil
		}
		if len(user.Nodes) == 0 {
			user.Nodes = nil
		}
		for deviceIndex := range user.Devices {
			if len(user.Devices[deviceIndex].SourceIPs) == 0 {
				user.Devices[deviceIndex].SourceIPs = nil
			}
		}
		for nodeIndex := range user.Nodes {
			if len(user.Nodes[nodeIndex].Destinations) == 0 {
				user.Nodes[nodeIndex].Destinations = nil
			}
		}
	}
}

func hashBytes(raw []byte) []byte {
	sum := sha256.Sum256(raw)
	return sum[:]
}

func stripSQLiteRuntime(state *State) {
	state.Counters = nil
	state.JournalCursor = ""
	state.PendingSources = nil
	state.IPApplyPending = false
	state.BurstApplyPending = false
	state.RateApplyPending = false
	state.StatsApplyPending = false
	state.ActiveConnections = nil
	state.OutboundHealth = nil
	state.LastHealthCheck = ""
	state.FleetStatus = nil
	state.Alerts = nil
	for userIndex := range state.Users {
		user := &state.Users[userIndex]
		user.Upload = 0
		user.Download = 0
		user.CurrentUploadMbps = 0
		user.CurrentDownloadMbps = 0
		user.BlockedUntil = ""
		user.BlockReason = ""
		user.DisabledReason = ""
		user.QuotaAlertStage = 0
		user.ExpiryAlertStage = 0
		user.Access.LastConnectionAlert = ""
		user.IPPolicy.BoundLastSeen = nil
		user.Access.ConnectionBlockedUntil = ""
		user.SourceIPs = nil
		user.TrafficSamples = nil
		user.UsageHistory = nil
		user.BillingHistory = nil
		user.RecentAccesses = nil
		for deviceIndex := range user.Devices {
			device := &user.Devices[deviceIndex]
			device.LastSeen = ""
			device.Access.LastConnectionAlert = ""
			device.IPPolicy.BoundLastSeen = nil
			device.Access.ConnectionBlockedUntil = ""
			device.SourceIPs = nil
		}
		for nodeIndex := range user.Nodes {
			node := &user.Nodes[nodeIndex]
			node.Upload = 0
			node.Download = 0
			node.Destinations = nil
			node.CurrentUploadMbps = 0
			node.CurrentDownloadMbps = 0
			node.RateUpdatedAt = ""
		}
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func intBool(value int) bool { return value != 0 }

func marshalSQLiteJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	return string(raw), err
}

func unmarshalSQLiteJSON(raw string, target any) error {
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("解析 SQLite JSON 列: %w", err)
	}
	return nil
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func chmodSQLiteFiles(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	for _, suffix := range sqliteFamilySuffixes {
		candidate := path + suffix
		if err := os.Chmod(candidate, 0600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func removeSQLiteFiles(path string) {
	for _, suffix := range sqliteFamilySuffixes {
		_ = os.Remove(path + suffix)
	}
}

var sqliteFamilySuffixes = []string{"", "-wal", "-shm", "-journal"}

func sqliteGlobalDocument(state *State) (string, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return "", err
	}
	for _, field := range []string{
		"stats_counters", "journal_cursor", "pending_sources", "ip_apply_pending", "burst_apply_pending",
		"rate_apply_pending", "stats_apply_pending", "active_connections", "outbound_health",
		"last_health_check", "fleet_status", "alerts", "users",
	} {
		delete(document, field)
	}
	raw, err = json.Marshal(document)
	return string(raw), err
}

func writeSQLiteState(tx *sql.Tx, state *State, stateHash, controlHash string) error {
	if err := prepareSQLiteKeepTables(tx); err != nil {
		return err
	}
	document, err := sqliteGlobalDocument(state)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`INSERT INTO settings(id, document, updated_at) VALUES(1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET document = excluded.document, updated_at = excluded.updated_at
		WHERE settings.document IS NOT excluded.document`, document, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO runtime_state(
		id, journal_cursor, ip_apply_pending, burst_apply_pending, rate_apply_pending, stats_apply_pending, last_health_check
	) VALUES(1, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET journal_cursor = excluded.journal_cursor,
		ip_apply_pending = excluded.ip_apply_pending, burst_apply_pending = excluded.burst_apply_pending,
		rate_apply_pending = excluded.rate_apply_pending, stats_apply_pending = excluded.stats_apply_pending,
		last_health_check = excluded.last_health_check
		WHERE runtime_state.journal_cursor IS NOT excluded.journal_cursor
			OR runtime_state.ip_apply_pending IS NOT excluded.ip_apply_pending
			OR runtime_state.burst_apply_pending IS NOT excluded.burst_apply_pending
			OR runtime_state.rate_apply_pending IS NOT excluded.rate_apply_pending
			OR runtime_state.stats_apply_pending IS NOT excluded.stats_apply_pending
			OR runtime_state.last_health_check IS NOT excluded.last_health_check`, state.JournalCursor, boolInt(state.IPApplyPending), boolInt(state.BurstApplyPending),
		boolInt(state.RateApplyPending), boolInt(state.StatsApplyPending), state.LastHealthCheck); err != nil {
		return err
	}
	for key, value := range map[string]string{
		"schema_version": fmt.Sprint(sqliteSchemaVersion),
		"state_hash":     stateHash,
		"control_hash":   controlHash,
		"updated_at":     now,
	} {
		if _, err := tx.Exec(`INSERT INTO metadata(key, value) VALUES(?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
			return err
		}
	}

	userIDs := make(map[string]int64, len(state.Users))
	deviceIDs := map[string]int64{}
	nodeIDs := map[string]int64{}
	for userOrdinal := range state.Users {
		user := &state.Users[userOrdinal]
		userNameKey := sqliteNameKey(user.Name)
		if _, err := tx.Exec(`INSERT INTO keep_users(name_key) VALUES(?)`, userNameKey); err != nil {
			return err
		}
		throttleJSON, err := marshalSQLiteJSON(user.Throttle)
		if err != nil {
			return err
		}
		burstJSON, err := marshalSQLiteJSON(user.Burst)
		if err != nil {
			return err
		}
		ipPolicyJSON, err := marshalSQLiteJSON(user.IPPolicy)
		if err != nil {
			return err
		}
		billingJSON, err := marshalSQLiteJSON(user.Billing)
		if err != nil {
			return err
		}
		accessJSON, err := marshalSQLiteJSON(user.Access)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO users(
			ordinal, name, name_key, enabled, quota_bytes, quota_mode, extra_quota_bytes, expires,
			upload_bytes, download_bytes, upload_mbps, download_mbps, rate_mark,
			throttle_json, burst_json, ip_policy_json, current_upload_mbps, current_download_mbps,
			blocked_until, block_reason, disabled_reason, billing_json, quota_alert_stage,
			expiry_alert_stage, access_json
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name_key) DO UPDATE SET name = excluded.name, ordinal = excluded.ordinal, enabled = excluded.enabled,
			quota_bytes = excluded.quota_bytes, quota_mode = excluded.quota_mode,
			extra_quota_bytes = excluded.extra_quota_bytes, expires = excluded.expires,
			upload_bytes = excluded.upload_bytes, download_bytes = excluded.download_bytes,
			upload_mbps = excluded.upload_mbps, download_mbps = excluded.download_mbps,
			rate_mark = excluded.rate_mark, throttle_json = excluded.throttle_json,
			burst_json = excluded.burst_json, ip_policy_json = excluded.ip_policy_json,
			current_upload_mbps = excluded.current_upload_mbps,
			current_download_mbps = excluded.current_download_mbps,
			blocked_until = excluded.blocked_until, block_reason = excluded.block_reason,
			disabled_reason = excluded.disabled_reason, billing_json = excluded.billing_json,
			quota_alert_stage = excluded.quota_alert_stage, expiry_alert_stage = excluded.expiry_alert_stage,
			access_json = excluded.access_json
		WHERE users.name IS NOT excluded.name OR users.ordinal IS NOT excluded.ordinal OR users.enabled IS NOT excluded.enabled
			OR users.quota_bytes IS NOT excluded.quota_bytes OR users.quota_mode IS NOT excluded.quota_mode
			OR users.extra_quota_bytes IS NOT excluded.extra_quota_bytes OR users.expires IS NOT excluded.expires
			OR users.upload_bytes IS NOT excluded.upload_bytes OR users.download_bytes IS NOT excluded.download_bytes
			OR users.upload_mbps IS NOT excluded.upload_mbps OR users.download_mbps IS NOT excluded.download_mbps
			OR users.rate_mark IS NOT excluded.rate_mark OR users.throttle_json IS NOT excluded.throttle_json
			OR users.burst_json IS NOT excluded.burst_json OR users.ip_policy_json IS NOT excluded.ip_policy_json
			OR users.current_upload_mbps IS NOT excluded.current_upload_mbps
			OR users.current_download_mbps IS NOT excluded.current_download_mbps
			OR users.blocked_until IS NOT excluded.blocked_until OR users.block_reason IS NOT excluded.block_reason
			OR users.disabled_reason IS NOT excluded.disabled_reason OR users.billing_json IS NOT excluded.billing_json
			OR users.quota_alert_stage IS NOT excluded.quota_alert_stage
			OR users.expiry_alert_stage IS NOT excluded.expiry_alert_stage
			OR users.access_json IS NOT excluded.access_json`,
			userOrdinal, user.Name, userNameKey, boolInt(user.Enabled), user.QuotaBytes, user.QuotaMode, user.ExtraQuotaBytes, user.Expires,
			user.Upload, user.Download, user.UploadMbps, user.DownloadMbps, user.RateMark,
			throttleJSON, burstJSON, ipPolicyJSON, user.CurrentUploadMbps, user.CurrentDownloadMbps,
			user.BlockedUntil, user.BlockReason, user.DisabledReason, billingJSON, user.QuotaAlertStage,
			user.ExpiryAlertStage, accessJSON)
		if err != nil {
			return fmt.Errorf("保存用户 %s: %w", user.Name, err)
		}
		var userID int64
		if err := tx.QueryRow(`SELECT id FROM users WHERE name_key = ?`, userNameKey).Scan(&userID); err != nil {
			return err
		}
		userIDs[user.Name] = userID

		for deviceOrdinal := range user.Devices {
			device := &user.Devices[deviceOrdinal]
			deviceNameKey := sqliteNameKey(device.Name)
			if _, err := tx.Exec(`INSERT INTO keep_devices(user_id, name_key) VALUES(?, ?)`, userID, deviceNameKey); err != nil {
				return err
			}
			ipPolicyJSON, err := marshalSQLiteJSON(device.IPPolicy)
			if err != nil {
				return err
			}
			accessJSON, err := marshalSQLiteJSON(device.Access)
			if err != nil {
				return err
			}
			_, err = tx.Exec(`INSERT INTO devices(
				user_id, ordinal, name, name_key, enabled, created_at, last_seen, ip_policy_json, subscription_token, access_json
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(user_id, name_key) DO UPDATE SET name = excluded.name, ordinal = excluded.ordinal, enabled = excluded.enabled,
				created_at = excluded.created_at, last_seen = excluded.last_seen,
				ip_policy_json = excluded.ip_policy_json, subscription_token = excluded.subscription_token,
				access_json = excluded.access_json
			WHERE devices.name IS NOT excluded.name OR devices.ordinal IS NOT excluded.ordinal OR devices.enabled IS NOT excluded.enabled
				OR devices.created_at IS NOT excluded.created_at OR devices.last_seen IS NOT excluded.last_seen
				OR devices.ip_policy_json IS NOT excluded.ip_policy_json
				OR devices.subscription_token IS NOT excluded.subscription_token
				OR devices.access_json IS NOT excluded.access_json`, userID, deviceOrdinal, device.Name, deviceNameKey, boolInt(device.Enabled),
				device.CreatedAt, device.LastSeen, ipPolicyJSON, device.SubscriptionToken, accessJSON)
			if err != nil {
				return fmt.Errorf("保存设备 %s/%s: %w", user.Name, device.Name, err)
			}
			var deviceID int64
			if err := tx.QueryRow(`SELECT id FROM devices WHERE user_id = ? AND name_key = ?`, userID, deviceNameKey).Scan(&deviceID); err != nil {
				return err
			}
			deviceIDs[sqliteEntityKey(user.Name, device.Name)] = deviceID
		}

		for nodeOrdinal := range user.Nodes {
			node := &user.Nodes[nodeOrdinal]
			nodeNameKey := sqliteNameKey(node.Name)
			deviceID, ok := deviceIDs[sqliteEntityKey(user.Name, node.Device)]
			if !ok {
				return fmt.Errorf("保存节点 %s/%s: 找不到设备 %q", user.Name, node.Name, node.Device)
			}
			if _, err := tx.Exec(`INSERT INTO keep_nodes(device_id, name_key) VALUES(?, ?)`, deviceID, nodeNameKey); err != nil {
				return err
			}
			_, err := tx.Exec(`INSERT INTO nodes(
				user_id, device_id, ordinal, name, name_key, auth_user, uuid, outbound, upload_mbps, download_mbps,
				rate_mark, upload_bytes, download_bytes, current_upload_mbps, current_download_mbps, rate_updated_at
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(device_id, name_key) DO UPDATE SET name = excluded.name, user_id = excluded.user_id, ordinal = excluded.ordinal,
				auth_user = excluded.auth_user, uuid = excluded.uuid, outbound = excluded.outbound,
				upload_mbps = excluded.upload_mbps, download_mbps = excluded.download_mbps,
				rate_mark = excluded.rate_mark, upload_bytes = excluded.upload_bytes,
				download_bytes = excluded.download_bytes, current_upload_mbps = excluded.current_upload_mbps,
				current_download_mbps = excluded.current_download_mbps,
				rate_updated_at = excluded.rate_updated_at
			WHERE nodes.name IS NOT excluded.name OR nodes.user_id IS NOT excluded.user_id OR nodes.ordinal IS NOT excluded.ordinal
				OR nodes.auth_user IS NOT excluded.auth_user OR nodes.uuid IS NOT excluded.uuid
				OR nodes.outbound IS NOT excluded.outbound OR nodes.upload_mbps IS NOT excluded.upload_mbps
				OR nodes.download_mbps IS NOT excluded.download_mbps OR nodes.rate_mark IS NOT excluded.rate_mark
				OR nodes.upload_bytes IS NOT excluded.upload_bytes OR nodes.download_bytes IS NOT excluded.download_bytes
				OR nodes.current_upload_mbps IS NOT excluded.current_upload_mbps
				OR nodes.current_download_mbps IS NOT excluded.current_download_mbps
				OR nodes.rate_updated_at IS NOT excluded.rate_updated_at`, userID, deviceID, nodeOrdinal,
				node.Name, nodeNameKey, node.AuthUser, node.UUID, node.Outbound, node.UploadMbps, node.DownloadMbps,
				node.RateMark, node.Upload, node.Download, node.CurrentUploadMbps, node.CurrentDownloadMbps, node.RateUpdatedAt)
			if err != nil {
				return fmt.Errorf("保存节点 %s/%s/%s: %w", user.Name, node.Device, node.Name, err)
			}
			var nodeID int64
			if err := tx.QueryRow(`SELECT id FROM nodes WHERE device_id = ? AND name_key = ?`, deviceID, nodeNameKey).Scan(&nodeID); err != nil {
				return err
			}
			nodeIDs[sqliteEntityKey(user.Name, node.Device, node.Name)] = nodeID
		}
	}
	if _, err := tx.Exec(`DELETE FROM nodes WHERE NOT EXISTS (
		SELECT 1 FROM keep_nodes WHERE keep_nodes.device_id = nodes.device_id AND keep_nodes.name_key = nodes.name_key
	)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM devices WHERE NOT EXISTS (
		SELECT 1 FROM keep_devices WHERE keep_devices.user_id = devices.user_id AND keep_devices.name_key = devices.name_key
	)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE name_key NOT IN (SELECT name_key FROM keep_users)`); err != nil {
		return err
	}

	for _, name := range sortedMapKeys(state.Counters) {
		if _, err := tx.Exec(`INSERT INTO keep_counters(name) VALUES(?)`, name); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO stats_counters(name, value) VALUES(?, ?)
			ON CONFLICT(name) DO UPDATE SET value = excluded.value
			WHERE stats_counters.value IS NOT excluded.value`, name, state.Counters[name]); err != nil {
			return err
		}
	}
	for _, authUser := range sortedMapKeys(state.PendingSources) {
		pending := state.PendingSources[authUser]
		if _, err := tx.Exec(`INSERT INTO keep_pending_sources(auth_user) VALUES(?)`, authUser); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO pending_sources(auth_user, ip, observed_at) VALUES(?, ?, ?)
			ON CONFLICT(auth_user) DO UPDATE SET ip = excluded.ip, observed_at = excluded.observed_at
			WHERE pending_sources.ip IS NOT excluded.ip OR pending_sources.observed_at IS NOT excluded.observed_at`, authUser, pending.IP, pending.At); err != nil {
			return err
		}
	}
	for _, id := range sortedMapKeys(state.ActiveConnections) {
		connection := state.ActiveConnections[id]
		if _, err := tx.Exec(`INSERT INTO keep_active_connections(id) VALUES(?)`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO active_connections(
			id, user_name, device_name, node_name, auth_user, source_ip, target, started_at, last_seen
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET user_name = excluded.user_name, device_name = excluded.device_name,
			node_name = excluded.node_name, auth_user = excluded.auth_user, source_ip = excluded.source_ip,
			target = excluded.target, started_at = excluded.started_at, last_seen = excluded.last_seen
		WHERE active_connections.user_name IS NOT excluded.user_name
			OR active_connections.device_name IS NOT excluded.device_name
			OR active_connections.node_name IS NOT excluded.node_name
			OR active_connections.auth_user IS NOT excluded.auth_user
			OR active_connections.source_ip IS NOT excluded.source_ip
			OR active_connections.target IS NOT excluded.target
			OR active_connections.started_at IS NOT excluded.started_at
			OR active_connections.last_seen IS NOT excluded.last_seen`, id, connection.User, connection.Device, connection.Node,
			connection.AuthUser, connection.SourceIP, connection.Target, connection.StartedAt, connection.LastSeen); err != nil {
			return err
		}
	}
	for _, tag := range sortedMapKeys(state.OutboundHealth) {
		health := state.OutboundHealth[tag]
		if _, err := tx.Exec(`INSERT INTO keep_outbound_health(tag) VALUES(?)`, tag); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO outbound_health(
			tag, target, healthy, latency_ms, failures, checked_at, error
		) VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tag) DO UPDATE SET target = excluded.target, healthy = excluded.healthy,
			latency_ms = excluded.latency_ms, failures = excluded.failures,
			checked_at = excluded.checked_at, error = excluded.error
		WHERE outbound_health.target IS NOT excluded.target OR outbound_health.healthy IS NOT excluded.healthy
			OR outbound_health.latency_ms IS NOT excluded.latency_ms OR outbound_health.failures IS NOT excluded.failures
			OR outbound_health.checked_at IS NOT excluded.checked_at OR outbound_health.error IS NOT excluded.error`, tag, health.Target, boolInt(health.Healthy), health.LatencyMS,
			health.Failures, health.CheckedAt, health.Error); err != nil {
			return err
		}
	}
	for _, name := range sortedMapKeys(state.FleetStatus) {
		status := state.FleetStatus[name]
		snapshot := status.Snapshot
		if _, err := tx.Exec(`INSERT INTO keep_fleet_status(name) VALUES(?)`, name); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO fleet_status(
			name, checked_at, online, latency_ms, hostname, version, users, enabled_users, devices,
			upload_bytes, download_bytes, unread_alerts, unhealthy_routes, error
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET checked_at = excluded.checked_at, online = excluded.online,
			latency_ms = excluded.latency_ms, hostname = excluded.hostname, version = excluded.version,
			users = excluded.users, enabled_users = excluded.enabled_users, devices = excluded.devices,
			upload_bytes = excluded.upload_bytes, download_bytes = excluded.download_bytes,
			unread_alerts = excluded.unread_alerts, unhealthy_routes = excluded.unhealthy_routes,
			error = excluded.error
		WHERE fleet_status.checked_at IS NOT excluded.checked_at OR fleet_status.online IS NOT excluded.online
			OR fleet_status.latency_ms IS NOT excluded.latency_ms OR fleet_status.hostname IS NOT excluded.hostname
			OR fleet_status.version IS NOT excluded.version OR fleet_status.users IS NOT excluded.users
			OR fleet_status.enabled_users IS NOT excluded.enabled_users OR fleet_status.devices IS NOT excluded.devices
			OR fleet_status.upload_bytes IS NOT excluded.upload_bytes OR fleet_status.download_bytes IS NOT excluded.download_bytes
			OR fleet_status.unread_alerts IS NOT excluded.unread_alerts
			OR fleet_status.unhealthy_routes IS NOT excluded.unhealthy_routes OR fleet_status.error IS NOT excluded.error`, name, status.CheckedAt, boolInt(status.Online),
			status.LatencyMS, snapshot.Hostname, snapshot.Version, snapshot.Users, snapshot.EnabledUsers, snapshot.Devices,
			snapshot.UploadBytes, snapshot.DownloadBytes, snapshot.UnreadAlerts, snapshot.UnhealthyRoutes, status.Error); err != nil {
			return err
		}
	}
	alertOccurrences := map[string]int{}
	for _, alert := range state.Alerts {
		identity, err := sqliteAlertIdentity(alert)
		if err != nil {
			return err
		}
		occurrence := alertOccurrences[identity]
		alertOccurrences[identity] = occurrence + 1
		stableKey := fmt.Sprintf("%s-%d", identity, occurrence)
		if _, err := tx.Exec(`INSERT INTO keep_alerts(stable_key) VALUES(?)`, stableKey); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO alerts(
			stable_key, ordinal, at, user_name, kind, message, acknowledged, notified_at, notify_attempts,
			last_notify_attempt, notify_error
		) VALUES(?, COALESCE((SELECT MAX(ordinal) + 1 FROM alerts), 0), ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(stable_key) DO UPDATE SET acknowledged = excluded.acknowledged, notified_at = excluded.notified_at,
			notify_attempts = excluded.notify_attempts,
			last_notify_attempt = excluded.last_notify_attempt, notify_error = excluded.notify_error
		WHERE alerts.acknowledged IS NOT excluded.acknowledged
			OR alerts.notified_at IS NOT excluded.notified_at OR alerts.notify_attempts IS NOT excluded.notify_attempts
			OR alerts.last_notify_attempt IS NOT excluded.last_notify_attempt OR alerts.notify_error IS NOT excluded.notify_error`,
			stableKey, alert.At, alert.User, alert.Kind, alert.Message,
			boolInt(alert.Acknowledged), alert.NotifiedAt, alert.NotifyAttempts, alert.LastNotifyAttempt, alert.NotifyError); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`DELETE FROM stats_counters WHERE name NOT IN (SELECT name FROM keep_counters)`,
		`DELETE FROM pending_sources WHERE auth_user NOT IN (SELECT auth_user FROM keep_pending_sources)`,
		`DELETE FROM active_connections WHERE id NOT IN (SELECT id FROM keep_active_connections)`,
		`DELETE FROM outbound_health WHERE tag NOT IN (SELECT tag FROM keep_outbound_health)`,
		`DELETE FROM fleet_status WHERE name NOT IN (SELECT name FROM keep_fleet_status)`,
		`DELETE FROM alerts WHERE stable_key NOT IN (SELECT stable_key FROM keep_alerts)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}

	for userIndex := range state.Users {
		user := &state.Users[userIndex]
		userID := userIDs[user.Name]
		for _, ip := range sortedMapKeys(user.SourceIPs) {
			stat := user.SourceIPs[ip]
			if _, err := tx.Exec(`INSERT INTO keep_user_source_ips(user_id, ip) VALUES(?, ?)`, userID, ip); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO user_source_ips(
				user_id, ip, count, violations, first_seen, last_seen, last_node, last_alert
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(user_id, ip) DO UPDATE SET count = excluded.count,
				violations = excluded.violations, first_seen = excluded.first_seen,
				last_seen = excluded.last_seen, last_node = excluded.last_node,
				last_alert = excluded.last_alert
			WHERE user_source_ips.count IS NOT excluded.count
				OR user_source_ips.violations IS NOT excluded.violations
				OR user_source_ips.first_seen IS NOT excluded.first_seen
				OR user_source_ips.last_seen IS NOT excluded.last_seen
				OR user_source_ips.last_node IS NOT excluded.last_node
				OR user_source_ips.last_alert IS NOT excluded.last_alert`, userID, ip, stat.Count, stat.Violations, stat.FirstSeen,
				stat.LastSeen, stat.LastNode, stat.LastAlert); err != nil {
				return err
			}
		}
		trafficOccurrences := map[string]int{}
		for _, sample := range user.TrafficSamples {
			stableKey := sqliteSequenceKey(sample.At, trafficOccurrences)
			if _, err := tx.Exec(`INSERT INTO keep_traffic_samples(user_id, stable_key) VALUES(?, ?)`, userID, stableKey); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO traffic_samples(user_id, stable_key, ordinal, at, bytes)
				VALUES(?, ?, COALESCE((SELECT MAX(ordinal) + 1 FROM traffic_samples WHERE user_id = ?), 0), ?, ?)
				ON CONFLICT(user_id, stable_key) DO UPDATE SET at = excluded.at, bytes = excluded.bytes
				WHERE traffic_samples.at IS NOT excluded.at OR traffic_samples.bytes IS NOT excluded.bytes`,
				userID, stableKey, userID, sample.At, sample.Bytes); err != nil {
				return err
			}
		}
		usageOccurrences := map[string]int{}
		for _, point := range user.UsageHistory {
			stableKey := sqliteSequenceKey(point.At, usageOccurrences)
			if _, err := tx.Exec(`INSERT INTO keep_usage_history(user_id, stable_key) VALUES(?, ?)`, userID, stableKey); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO usage_history(
				user_id, stable_key, ordinal, at, upload_bytes, download_bytes, upload_mbps, download_mbps
			) VALUES(?, ?, COALESCE((SELECT MAX(ordinal) + 1 FROM usage_history WHERE user_id = ?), 0), ?, ?, ?, ?, ?)
			ON CONFLICT(user_id, stable_key) DO UPDATE SET at = excluded.at,
				upload_bytes = excluded.upload_bytes, download_bytes = excluded.download_bytes,
				upload_mbps = excluded.upload_mbps, download_mbps = excluded.download_mbps
			WHERE usage_history.at IS NOT excluded.at
				OR usage_history.upload_bytes IS NOT excluded.upload_bytes
				OR usage_history.download_bytes IS NOT excluded.download_bytes
				OR usage_history.upload_mbps IS NOT excluded.upload_mbps
				OR usage_history.download_mbps IS NOT excluded.download_mbps`, userID, stableKey, userID, point.At, point.UploadBytes, point.DownloadBytes,
				point.UploadMbps, point.DownloadMbps); err != nil {
				return err
			}
		}
		billingOccurrences := map[string]int{}
		for _, record := range user.BillingHistory {
			identity := record.StartedAt + "\x00" + record.EndedAt
			stableKey := sqliteSequenceKey(identity, billingOccurrences)
			if _, err := tx.Exec(`INSERT INTO keep_billing_history(user_id, stable_key) VALUES(?, ?)`, userID, stableKey); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO billing_history(
				user_id, stable_key, ordinal, started_at, ended_at, upload_bytes, download_bytes, quota_bytes
			) VALUES(?, ?, COALESCE((SELECT MAX(ordinal) + 1 FROM billing_history WHERE user_id = ?), 0), ?, ?, ?, ?, ?)
			ON CONFLICT(user_id, stable_key) DO UPDATE SET started_at = excluded.started_at, ended_at = excluded.ended_at,
				upload_bytes = excluded.upload_bytes, download_bytes = excluded.download_bytes,
				quota_bytes = excluded.quota_bytes
			WHERE billing_history.started_at IS NOT excluded.started_at
				OR billing_history.ended_at IS NOT excluded.ended_at
				OR billing_history.upload_bytes IS NOT excluded.upload_bytes
				OR billing_history.download_bytes IS NOT excluded.download_bytes
				OR billing_history.quota_bytes IS NOT excluded.quota_bytes`, userID, stableKey, userID, record.StartedAt, record.EndedAt,
				record.UploadBytes, record.DownloadBytes, record.QuotaBytes); err != nil {
				return err
			}
		}
		for accessOrdinal, access := range user.RecentAccesses {
			if _, err := tx.Exec(`INSERT INTO keep_recent_accesses(user_id, target, device_name, node_name) VALUES(?, ?, ?, ?)`,
				userID, access.Target, access.Device, access.Node); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO recent_accesses(
				user_id, ordinal, target, device_name, node_name, first_seen, last_seen, count
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(user_id, target, device_name, node_name) DO UPDATE SET
				ordinal = excluded.ordinal, first_seen = excluded.first_seen,
				last_seen = excluded.last_seen, count = excluded.count
			WHERE recent_accesses.ordinal IS NOT excluded.ordinal
				OR recent_accesses.first_seen IS NOT excluded.first_seen
				OR recent_accesses.last_seen IS NOT excluded.last_seen
				OR recent_accesses.count IS NOT excluded.count`, userID, accessOrdinal, access.Target, access.Device, access.Node,
				access.FirstSeen, access.LastSeen, access.Count); err != nil {
				return err
			}
		}
		for deviceIndex := range user.Devices {
			device := &user.Devices[deviceIndex]
			deviceID := deviceIDs[sqliteEntityKey(user.Name, device.Name)]
			for _, ip := range sortedMapKeys(device.SourceIPs) {
				stat := device.SourceIPs[ip]
				if _, err := tx.Exec(`INSERT INTO keep_device_source_ips(device_id, ip) VALUES(?, ?)`, deviceID, ip); err != nil {
					return err
				}
				if _, err := tx.Exec(`INSERT INTO device_source_ips(
					device_id, ip, count, violations, first_seen, last_seen, last_node, last_alert
				) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(device_id, ip) DO UPDATE SET count = excluded.count,
					violations = excluded.violations, first_seen = excluded.first_seen,
					last_seen = excluded.last_seen, last_node = excluded.last_node,
					last_alert = excluded.last_alert
				WHERE device_source_ips.count IS NOT excluded.count
					OR device_source_ips.violations IS NOT excluded.violations
					OR device_source_ips.first_seen IS NOT excluded.first_seen
					OR device_source_ips.last_seen IS NOT excluded.last_seen
					OR device_source_ips.last_node IS NOT excluded.last_node
					OR device_source_ips.last_alert IS NOT excluded.last_alert`, deviceID, ip, stat.Count, stat.Violations, stat.FirstSeen,
					stat.LastSeen, stat.LastNode, stat.LastAlert); err != nil {
					return err
				}
			}
		}
		for nodeIndex := range user.Nodes {
			node := &user.Nodes[nodeIndex]
			nodeID := nodeIDs[sqliteEntityKey(user.Name, node.Device, node.Name)]
			for _, target := range sortedMapKeys(node.Destinations) {
				stat := node.Destinations[target]
				if _, err := tx.Exec(`INSERT INTO keep_node_destinations(node_id, target) VALUES(?, ?)`, nodeID, target); err != nil {
					return err
				}
				if _, err := tx.Exec(`INSERT INTO node_destinations(node_id, target, count, last_seen) VALUES(?, ?, ?, ?)
					ON CONFLICT(node_id, target) DO UPDATE SET count = excluded.count, last_seen = excluded.last_seen
					WHERE node_destinations.count IS NOT excluded.count OR node_destinations.last_seen IS NOT excluded.last_seen`,
					nodeID, target, stat.Count, stat.LastSeen); err != nil {
					return err
				}
			}
		}
	}
	for _, statement := range []string{
		`DELETE FROM user_source_ips WHERE NOT EXISTS (SELECT 1 FROM keep_user_source_ips k WHERE k.user_id = user_source_ips.user_id AND k.ip = user_source_ips.ip)`,
		`DELETE FROM device_source_ips WHERE NOT EXISTS (SELECT 1 FROM keep_device_source_ips k WHERE k.device_id = device_source_ips.device_id AND k.ip = device_source_ips.ip)`,
		`DELETE FROM traffic_samples WHERE NOT EXISTS (SELECT 1 FROM keep_traffic_samples k WHERE k.user_id = traffic_samples.user_id AND k.stable_key = traffic_samples.stable_key)`,
		`DELETE FROM usage_history WHERE NOT EXISTS (SELECT 1 FROM keep_usage_history k WHERE k.user_id = usage_history.user_id AND k.stable_key = usage_history.stable_key)`,
		`DELETE FROM billing_history WHERE NOT EXISTS (SELECT 1 FROM keep_billing_history k WHERE k.user_id = billing_history.user_id AND k.stable_key = billing_history.stable_key)`,
		`DELETE FROM recent_accesses WHERE NOT EXISTS (SELECT 1 FROM keep_recent_accesses k WHERE k.user_id = recent_accesses.user_id AND k.target = recent_accesses.target AND k.device_name = recent_accesses.device_name AND k.node_name = recent_accesses.node_name)`,
		`DELETE FROM node_destinations WHERE NOT EXISTS (SELECT 1 FROM keep_node_destinations k WHERE k.node_id = node_destinations.node_id AND k.target = node_destinations.target)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func sqliteEntityKey(parts ...string) string {
	for index := range parts {
		parts[index] = sqliteNameKey(parts[index])
	}
	return strings.Join(parts, "\x00")
}

func sqliteNameKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func prepareSQLiteKeepTables(tx *sql.Tx) error {
	statements := []string{
		`CREATE TEMP TABLE IF NOT EXISTS keep_users(name_key TEXT PRIMARY KEY) WITHOUT ROWID`,
		`CREATE TEMP TABLE IF NOT EXISTS keep_devices(user_id INTEGER NOT NULL, name_key TEXT NOT NULL, PRIMARY KEY(user_id, name_key)) WITHOUT ROWID`,
		`CREATE TEMP TABLE IF NOT EXISTS keep_nodes(device_id INTEGER NOT NULL, name_key TEXT NOT NULL, PRIMARY KEY(device_id, name_key)) WITHOUT ROWID`,
		`CREATE TEMP TABLE IF NOT EXISTS keep_counters(name TEXT PRIMARY KEY) WITHOUT ROWID`,
		`CREATE TEMP TABLE IF NOT EXISTS keep_pending_sources(auth_user TEXT PRIMARY KEY) WITHOUT ROWID`,
		`CREATE TEMP TABLE IF NOT EXISTS keep_active_connections(id TEXT PRIMARY KEY) WITHOUT ROWID`,
		`CREATE TEMP TABLE IF NOT EXISTS keep_outbound_health(tag TEXT PRIMARY KEY) WITHOUT ROWID`,
		`CREATE TEMP TABLE IF NOT EXISTS keep_fleet_status(name TEXT PRIMARY KEY COLLATE NOCASE) WITHOUT ROWID`,
		`CREATE TEMP TABLE IF NOT EXISTS keep_alerts(stable_key TEXT PRIMARY KEY) WITHOUT ROWID`,
		`CREATE TEMP TABLE IF NOT EXISTS keep_user_source_ips(user_id INTEGER NOT NULL, ip TEXT NOT NULL, PRIMARY KEY(user_id, ip)) WITHOUT ROWID`,
		`CREATE TEMP TABLE IF NOT EXISTS keep_device_source_ips(device_id INTEGER NOT NULL, ip TEXT NOT NULL, PRIMARY KEY(device_id, ip)) WITHOUT ROWID`,
		`CREATE TEMP TABLE IF NOT EXISTS keep_traffic_samples(user_id INTEGER NOT NULL, stable_key TEXT NOT NULL, PRIMARY KEY(user_id, stable_key)) WITHOUT ROWID`,
		`CREATE TEMP TABLE IF NOT EXISTS keep_usage_history(user_id INTEGER NOT NULL, stable_key TEXT NOT NULL, PRIMARY KEY(user_id, stable_key)) WITHOUT ROWID`,
		`CREATE TEMP TABLE IF NOT EXISTS keep_billing_history(user_id INTEGER NOT NULL, stable_key TEXT NOT NULL, PRIMARY KEY(user_id, stable_key)) WITHOUT ROWID`,
		`CREATE TEMP TABLE IF NOT EXISTS keep_recent_accesses(user_id INTEGER NOT NULL, target TEXT NOT NULL, device_name TEXT NOT NULL, node_name TEXT NOT NULL, PRIMARY KEY(user_id, target, device_name, node_name)) WITHOUT ROWID`,
		`CREATE TEMP TABLE IF NOT EXISTS keep_node_destinations(node_id INTEGER NOT NULL, target TEXT NOT NULL, PRIMARY KEY(node_id, target)) WITHOUT ROWID`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("创建 SQLite 增量同步表: %w", err)
		}
	}
	for _, table := range []string{
		"keep_users", "keep_devices", "keep_nodes", "keep_counters", "keep_pending_sources",
		"keep_active_connections", "keep_outbound_health", "keep_fleet_status", "keep_alerts",
		"keep_user_source_ips", "keep_device_source_ips", "keep_traffic_samples", "keep_usage_history",
		"keep_billing_history", "keep_recent_accesses", "keep_node_destinations",
	} {
		if _, err := tx.Exec("DELETE FROM " + table); err != nil {
			return err
		}
	}
	return nil
}

func sqliteSequenceKey(identity string, occurrences map[string]int) string {
	occurrence := occurrences[identity]
	occurrences[identity] = occurrence + 1
	sum := sha256.Sum256([]byte(identity + "\x00" + fmt.Sprint(occurrence)))
	return hex.EncodeToString(sum[:])
}

func sqliteAlertIdentity(alert Alert) (string, error) {
	raw, err := json.Marshal([]string{alert.At, alert.User, alert.Kind, alert.Message})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

type sqliteDeviceLocation struct {
	userIndex   int
	deviceIndex int
}

type sqliteNodeLocation struct {
	userIndex int
	nodeIndex int
}

type sqliteQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func readSQLiteState(path string) (*State, error) {
	db, _, err := openSQLiteState(path)
	if err != nil {
		return nil, fmt.Errorf("打开状态数据库 %s: %w", path, err)
	}
	defer func() {
		_ = db.Close()
		_ = chmodSQLiteFiles(path)
	}()
	return readSQLiteStateFromOpenDB(path, db)
}

// readSQLiteStateFromOpenDB reconstructs one state from a single read
// transaction. Keeping every table read in the same transaction is important:
// the daemon may commit counters and history while the CUI or subscription
// server is reading.
func readSQLiteStateFromOpenDB(path string, db *sql.DB) (*State, error) {
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := validateSQLiteIdentity(tx); err != nil {
		return nil, fmt.Errorf("校验状态数据库 %s: %w", path, err)
	}
	var document string
	if err := tx.QueryRow(`SELECT document FROM settings WHERE id = 1`).Scan(&document); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("状态数据库尚未初始化；请运行初始化或从 state.json 迁移")
		}
		return nil, err
	}
	state := &State{}
	if err := json.Unmarshal([]byte(document), state); err != nil {
		return nil, fmt.Errorf("解析 SQLite 全局设置: %w", err)
	}
	var ipPending, burstPending, ratePending, statsPending int
	if err := tx.QueryRow(`SELECT journal_cursor, ip_apply_pending, burst_apply_pending,
		rate_apply_pending, stats_apply_pending, last_health_check FROM runtime_state WHERE id = 1`).Scan(
		&state.JournalCursor, &ipPending, &burstPending, &ratePending, &statsPending, &state.LastHealthCheck,
	); err != nil {
		return nil, fmt.Errorf("读取 SQLite 运行状态: %w", err)
	}
	state.IPApplyPending = intBool(ipPending)
	state.BurstApplyPending = intBool(burstPending)
	state.RateApplyPending = intBool(ratePending)
	state.StatsApplyPending = intBool(statsPending)

	userIDs := map[int64]int{}
	rows, err := tx.Query(`SELECT id, name, enabled, quota_bytes, quota_mode, extra_quota_bytes, expires,
		upload_bytes, download_bytes, upload_mbps, download_mbps, rate_mark, throttle_json, burst_json,
		ip_policy_json, current_upload_mbps, current_download_mbps, blocked_until, block_reason,
		disabled_reason, billing_json, quota_alert_stage, expiry_alert_stage, access_json
		FROM users ORDER BY ordinal, id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var enabled int
		var throttleJSON, burstJSON, ipPolicyJSON, billingJSON, accessJSON string
		var user User
		if err := rows.Scan(&id, &user.Name, &enabled, &user.QuotaBytes, &user.QuotaMode, &user.ExtraQuotaBytes,
			&user.Expires, &user.Upload, &user.Download, &user.UploadMbps, &user.DownloadMbps, &user.RateMark,
			&throttleJSON, &burstJSON, &ipPolicyJSON, &user.CurrentUploadMbps, &user.CurrentDownloadMbps,
			&user.BlockedUntil, &user.BlockReason, &user.DisabledReason, &billingJSON, &user.QuotaAlertStage,
			&user.ExpiryAlertStage, &accessJSON); err != nil {
			rows.Close()
			return nil, err
		}
		user.Enabled = intBool(enabled)
		if err := unmarshalSQLiteJSON(throttleJSON, &user.Throttle); err != nil {
			rows.Close()
			return nil, err
		}
		if err := unmarshalSQLiteJSON(burstJSON, &user.Burst); err != nil {
			rows.Close()
			return nil, err
		}
		if err := unmarshalSQLiteJSON(ipPolicyJSON, &user.IPPolicy); err != nil {
			rows.Close()
			return nil, err
		}
		if err := unmarshalSQLiteJSON(billingJSON, &user.Billing); err != nil {
			rows.Close()
			return nil, err
		}
		if err := unmarshalSQLiteJSON(accessJSON, &user.Access); err != nil {
			rows.Close()
			return nil, err
		}
		userIDs[id] = len(state.Users)
		state.Users = append(state.Users, user)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	deviceIDs := map[int64]sqliteDeviceLocation{}
	rows, err = tx.Query(`SELECT id, user_id, name, enabled, created_at, last_seen, ip_policy_json,
		subscription_token, access_json FROM devices ORDER BY user_id, ordinal, id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, userID int64
		var enabled int
		var ipPolicyJSON, accessJSON string
		var device Device
		if err := rows.Scan(&id, &userID, &device.Name, &enabled, &device.CreatedAt, &device.LastSeen,
			&ipPolicyJSON, &device.SubscriptionToken, &accessJSON); err != nil {
			rows.Close()
			return nil, err
		}
		userIndex, ok := userIDs[userID]
		if !ok {
			rows.Close()
			return nil, fmt.Errorf("设备 %s 引用了不存在的用户 id %d", device.Name, userID)
		}
		device.Enabled = intBool(enabled)
		if err := unmarshalSQLiteJSON(ipPolicyJSON, &device.IPPolicy); err != nil {
			rows.Close()
			return nil, err
		}
		if err := unmarshalSQLiteJSON(accessJSON, &device.Access); err != nil {
			rows.Close()
			return nil, err
		}
		location := sqliteDeviceLocation{userIndex: userIndex, deviceIndex: len(state.Users[userIndex].Devices)}
		state.Users[userIndex].Devices = append(state.Users[userIndex].Devices, device)
		deviceIDs[id] = location
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	nodeIDs := map[int64]sqliteNodeLocation{}
	rows, err = tx.Query(`SELECT id, user_id, device_id, name, auth_user, uuid, outbound, upload_mbps,
		download_mbps, rate_mark, upload_bytes, download_bytes, current_upload_mbps,
		current_download_mbps, rate_updated_at FROM nodes ORDER BY user_id, ordinal, id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, userID, deviceID int64
		var node Node
		if err := rows.Scan(&id, &userID, &deviceID, &node.Name, &node.AuthUser, &node.UUID, &node.Outbound,
			&node.UploadMbps, &node.DownloadMbps, &node.RateMark, &node.Upload, &node.Download,
			&node.CurrentUploadMbps, &node.CurrentDownloadMbps, &node.RateUpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		userIndex, ok := userIDs[userID]
		if !ok {
			rows.Close()
			return nil, fmt.Errorf("节点 %s 引用了不存在的用户 id %d", node.Name, userID)
		}
		deviceLocation, ok := deviceIDs[deviceID]
		if !ok || deviceLocation.userIndex != userIndex {
			rows.Close()
			return nil, fmt.Errorf("节点 %s 引用了无效设备 id %d", node.Name, deviceID)
		}
		node.Device = state.Users[userIndex].Devices[deviceLocation.deviceIndex].Name
		location := sqliteNodeLocation{userIndex: userIndex, nodeIndex: len(state.Users[userIndex].Nodes)}
		state.Users[userIndex].Nodes = append(state.Users[userIndex].Nodes, node)
		nodeIDs[id] = location
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := readSQLiteGlobalRuntime(tx, state); err != nil {
		return nil, err
	}
	if err := readSQLiteUserRuntime(tx, state, userIDs, deviceIDs, nodeIDs); err != nil {
		return nil, err
	}
	var expectedHash string
	if err := tx.QueryRow(`SELECT value FROM metadata WHERE key = 'state_hash'`).Scan(&expectedHash); err != nil {
		return nil, fmt.Errorf("读取状态数据库业务哈希: %w", err)
	}
	actualHash, err := sqliteStateHash(state)
	if err != nil {
		return nil, err
	}
	if actualHash != expectedHash {
		return nil, errors.New("状态数据库业务哈希校验失败；数据表可能被不完整地外部修改")
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return state, nil
}

func readSQLiteStateReadOnly(path string) (*State, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	dsn := (&url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro&immutable=1"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err := db.Exec(`PRAGMA query_only = ON`); err != nil {
		return nil, err
	}
	return readSQLiteStateFromOpenDB(path, db)
}

func readSQLiteGlobalRuntime(db sqliteQuerier, state *State) error {
	rows, err := db.Query(`SELECT name, value FROM stats_counters ORDER BY name`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		var value int64
		if err := rows.Scan(&name, &value); err != nil {
			rows.Close()
			return err
		}
		if state.Counters == nil {
			state.Counters = map[string]int64{}
		}
		state.Counters[name] = value
	}
	if err := closeSQLiteRows(rows); err != nil {
		return err
	}

	rows, err = db.Query(`SELECT auth_user, ip, observed_at FROM pending_sources ORDER BY auth_user`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var authUser string
		var pending PendingSource
		if err := rows.Scan(&authUser, &pending.IP, &pending.At); err != nil {
			rows.Close()
			return err
		}
		if state.PendingSources == nil {
			state.PendingSources = map[string]PendingSource{}
		}
		state.PendingSources[authUser] = pending
	}
	if err := closeSQLiteRows(rows); err != nil {
		return err
	}

	rows, err = db.Query(`SELECT id, user_name, device_name, node_name, auth_user, source_ip, target,
		started_at, last_seen FROM active_connections ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var connection ActiveConnection
		if err := rows.Scan(&connection.ID, &connection.User, &connection.Device, &connection.Node,
			&connection.AuthUser, &connection.SourceIP, &connection.Target, &connection.StartedAt,
			&connection.LastSeen); err != nil {
			rows.Close()
			return err
		}
		if state.ActiveConnections == nil {
			state.ActiveConnections = map[string]ActiveConnection{}
		}
		state.ActiveConnections[connection.ID] = connection
	}
	if err := closeSQLiteRows(rows); err != nil {
		return err
	}

	rows, err = db.Query(`SELECT tag, target, healthy, latency_ms, failures, checked_at, error
		FROM outbound_health ORDER BY tag`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var tag string
		var healthy int
		var health OutboundHealth
		if err := rows.Scan(&tag, &health.Target, &healthy, &health.LatencyMS, &health.Failures,
			&health.CheckedAt, &health.Error); err != nil {
			rows.Close()
			return err
		}
		health.Tag = tag
		health.Healthy = intBool(healthy)
		if state.OutboundHealth == nil {
			state.OutboundHealth = map[string]OutboundHealth{}
		}
		state.OutboundHealth[tag] = health
	}
	if err := closeSQLiteRows(rows); err != nil {
		return err
	}

	rows, err = db.Query(`SELECT name, checked_at, online, latency_ms, hostname, version, users,
		enabled_users, devices, upload_bytes, download_bytes, unread_alerts, unhealthy_routes, error
		FROM fleet_status ORDER BY name`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		var online int
		var status FleetServerStatus
		if err := rows.Scan(&name, &status.CheckedAt, &online, &status.LatencyMS, &status.Snapshot.Hostname,
			&status.Snapshot.Version, &status.Snapshot.Users, &status.Snapshot.EnabledUsers,
			&status.Snapshot.Devices, &status.Snapshot.UploadBytes, &status.Snapshot.DownloadBytes,
			&status.Snapshot.UnreadAlerts, &status.Snapshot.UnhealthyRoutes, &status.Error); err != nil {
			rows.Close()
			return err
		}
		status.Online = intBool(online)
		if state.FleetStatus == nil {
			state.FleetStatus = map[string]FleetServerStatus{}
		}
		state.FleetStatus[name] = status
	}
	if err := closeSQLiteRows(rows); err != nil {
		return err
	}

	rows, err = db.Query(`SELECT at, user_name, kind, message, acknowledged, notified_at,
		notify_attempts, last_notify_attempt, notify_error FROM alerts ORDER BY ordinal, id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var acknowledged int
		var alert Alert
		if err := rows.Scan(&alert.At, &alert.User, &alert.Kind, &alert.Message, &acknowledged,
			&alert.NotifiedAt, &alert.NotifyAttempts, &alert.LastNotifyAttempt, &alert.NotifyError); err != nil {
			rows.Close()
			return err
		}
		alert.Acknowledged = intBool(acknowledged)
		state.Alerts = append(state.Alerts, alert)
	}
	return closeSQLiteRows(rows)
}

func readSQLiteUserRuntime(db sqliteQuerier, state *State, userIDs map[int64]int, deviceIDs map[int64]sqliteDeviceLocation, nodeIDs map[int64]sqliteNodeLocation) error {
	rows, err := db.Query(`SELECT user_id, ip, count, violations, first_seen, last_seen, last_node, last_alert
		FROM user_source_ips ORDER BY user_id, ip`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var userID int64
		var ip string
		var stat SourceIPStat
		if err := rows.Scan(&userID, &ip, &stat.Count, &stat.Violations, &stat.FirstSeen, &stat.LastSeen,
			&stat.LastNode, &stat.LastAlert); err != nil {
			rows.Close()
			return err
		}
		userIndex, ok := userIDs[userID]
		if !ok {
			rows.Close()
			return fmt.Errorf("来源 IP 引用了不存在的用户 id %d", userID)
		}
		if state.Users[userIndex].SourceIPs == nil {
			state.Users[userIndex].SourceIPs = map[string]SourceIPStat{}
		}
		state.Users[userIndex].SourceIPs[ip] = stat
	}
	if err := closeSQLiteRows(rows); err != nil {
		return err
	}

	rows, err = db.Query(`SELECT device_id, ip, count, violations, first_seen, last_seen, last_node, last_alert
		FROM device_source_ips ORDER BY device_id, ip`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var deviceID int64
		var ip string
		var stat SourceIPStat
		if err := rows.Scan(&deviceID, &ip, &stat.Count, &stat.Violations, &stat.FirstSeen, &stat.LastSeen,
			&stat.LastNode, &stat.LastAlert); err != nil {
			rows.Close()
			return err
		}
		location, ok := deviceIDs[deviceID]
		if !ok {
			rows.Close()
			return fmt.Errorf("设备来源 IP 引用了不存在的设备 id %d", deviceID)
		}
		device := &state.Users[location.userIndex].Devices[location.deviceIndex]
		if device.SourceIPs == nil {
			device.SourceIPs = map[string]SourceIPStat{}
		}
		device.SourceIPs[ip] = stat
	}
	if err := closeSQLiteRows(rows); err != nil {
		return err
	}

	rows, err = db.Query(`SELECT user_id, at, bytes FROM traffic_samples ORDER BY user_id, ordinal`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var userID int64
		var sample TrafficSample
		if err := rows.Scan(&userID, &sample.At, &sample.Bytes); err != nil {
			rows.Close()
			return err
		}
		userIndex, ok := userIDs[userID]
		if !ok {
			rows.Close()
			return fmt.Errorf("流量样本引用了不存在的用户 id %d", userID)
		}
		state.Users[userIndex].TrafficSamples = append(state.Users[userIndex].TrafficSamples, sample)
	}
	if err := closeSQLiteRows(rows); err != nil {
		return err
	}

	rows, err = db.Query(`SELECT user_id, at, upload_bytes, download_bytes, upload_mbps, download_mbps
		FROM usage_history ORDER BY user_id, ordinal`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var userID int64
		var point UsagePoint
		if err := rows.Scan(&userID, &point.At, &point.UploadBytes, &point.DownloadBytes,
			&point.UploadMbps, &point.DownloadMbps); err != nil {
			rows.Close()
			return err
		}
		userIndex, ok := userIDs[userID]
		if !ok {
			rows.Close()
			return fmt.Errorf("用量历史引用了不存在的用户 id %d", userID)
		}
		state.Users[userIndex].UsageHistory = append(state.Users[userIndex].UsageHistory, point)
	}
	if err := closeSQLiteRows(rows); err != nil {
		return err
	}

	rows, err = db.Query(`SELECT user_id, started_at, ended_at, upload_bytes, download_bytes, quota_bytes
		FROM billing_history ORDER BY user_id, ordinal`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var userID int64
		var record BillingRecord
		if err := rows.Scan(&userID, &record.StartedAt, &record.EndedAt, &record.UploadBytes,
			&record.DownloadBytes, &record.QuotaBytes); err != nil {
			rows.Close()
			return err
		}
		userIndex, ok := userIDs[userID]
		if !ok {
			rows.Close()
			return fmt.Errorf("账单历史引用了不存在的用户 id %d", userID)
		}
		state.Users[userIndex].BillingHistory = append(state.Users[userIndex].BillingHistory, record)
	}
	if err := closeSQLiteRows(rows); err != nil {
		return err
	}

	rows, err = db.Query(`SELECT user_id, target, device_name, node_name, first_seen, last_seen, count
		FROM recent_accesses ORDER BY user_id, ordinal`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var userID int64
		var access RecentAccess
		if err := rows.Scan(&userID, &access.Target, &access.Device, &access.Node, &access.FirstSeen,
			&access.LastSeen, &access.Count); err != nil {
			rows.Close()
			return err
		}
		userIndex, ok := userIDs[userID]
		if !ok {
			rows.Close()
			return fmt.Errorf("近期访问引用了不存在的用户 id %d", userID)
		}
		state.Users[userIndex].RecentAccesses = append(state.Users[userIndex].RecentAccesses, access)
	}
	if err := closeSQLiteRows(rows); err != nil {
		return err
	}

	rows, err = db.Query(`SELECT node_id, target, count, last_seen FROM node_destinations ORDER BY node_id, target`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var nodeID int64
		var target string
		var stat AccessStat
		if err := rows.Scan(&nodeID, &target, &stat.Count, &stat.LastSeen); err != nil {
			rows.Close()
			return err
		}
		location, ok := nodeIDs[nodeID]
		if !ok {
			rows.Close()
			return fmt.Errorf("访问目标引用了不存在的节点 id %d", nodeID)
		}
		node := &state.Users[location.userIndex].Nodes[location.nodeIndex]
		if node.Destinations == nil {
			node.Destinations = map[string]AccessStat{}
		}
		node.Destinations[target] = stat
	}
	return closeSQLiteRows(rows)
}

func closeSQLiteRows(rows *sql.Rows) error {
	err := rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func importLegacyJSONState(databasePath string) (err error) {
	legacyPath := filepath.Join(filepath.Dir(databasePath), "state.json")
	legacyLock, err := acquireStateFileLock(legacyPath + ".lock")
	if err != nil {
		return fmt.Errorf("锁定旧状态文件以便迁移: %w", err)
	}
	defer func() {
		if releaseErr := legacyLock.release(); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("释放旧状态迁移锁: %w", releaseErr))
		}
	}()
	if _, statErr := os.Stat(databasePath); statErr == nil {
		return nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if _, markerErr := os.Stat(sqliteMigrationMarkerPath(databasePath)); markerErr == nil {
		return fmt.Errorf("状态数据库 %s 缺失，但检测到旧 JSON 已迁移标记；为避免回灌陈旧数据，请从 SQLite 备份恢复或人工核对后删除 %s",
			databasePath, sqliteMigrationMarkerPath(databasePath))
	} else if !errors.Is(markerErr, os.ErrNotExist) {
		return markerErr
	}
	raw, err := os.ReadFile(legacyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("状态数据库 %s 不存在，且未找到可迁移的 %s", databasePath, legacyPath)
		}
		return fmt.Errorf("读取旧状态文件 %s: %w", legacyPath, err)
	}
	state, _, err := loadJSONStateWithCanonicalChange(legacyPath)
	if err != nil {
		return fmt.Errorf("旧 state.json 校验失败，未创建 state.db: %w", err)
	}
	backupName, err := backupLegacyJSONBytes(databasePath, raw, "state-imported-json-", time.Now())
	if err != nil {
		return fmt.Errorf("备份旧 state.json: %w", err)
	}
	temporaryPath, err := unusedSQLitePath(filepath.Dir(databasePath), ".state-import-*.db")
	if err != nil {
		return err
	}
	defer removeSQLiteFiles(temporaryPath)
	if err := saveSQLiteState(temporaryPath, state); err != nil {
		return fmt.Errorf("创建迁移数据库: %w", err)
	}
	migrated, err := readSQLiteState(temporaryPath)
	if err != nil {
		return fmt.Errorf("复核迁移数据库: %w", err)
	}
	wantHash, err := sqliteStateHash(state)
	if err != nil {
		return err
	}
	gotHash, err := sqliteStateHash(migrated)
	if err != nil {
		return err
	}
	if gotHash != wantHash {
		return errors.New("迁移数据库业务内容校验不一致")
	}
	if err := finalizeStandaloneSQLite(temporaryPath); err != nil {
		return fmt.Errorf("合并迁移数据库 WAL: %w", err)
	}
	finalized, err := loadSQLiteBackupState(temporaryPath)
	if err != nil {
		return fmt.Errorf("重新打开迁移数据库复核: %w", err)
	}
	finalizedHash, err := sqliteStateHash(finalized)
	if err != nil {
		return err
	}
	if finalizedHash != wantHash {
		return errors.New("合并 WAL 后迁移数据库业务内容校验不一致")
	}
	if _, statErr := os.Stat(databasePath); statErr == nil {
		return nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	// Persist the anti-rollback marker before publishing the database. After
	// the no-replace link succeeds there are no fallible migration steps that
	// could tempt cleanup code to delete a concurrently replaced live DB.
	if err := writeSQLiteMigrationMarker(databasePath, legacyPath, backupName); err != nil {
		return fmt.Errorf("写入旧状态归档标记: %w", err)
	}
	if err := commitSQLiteNoReplace(temporaryPath, databasePath); err != nil {
		return fmt.Errorf("提交 state.db 迁移: %w", err)
	}
	return nil
}

// commitSQLiteNoReplace publishes a finalized single-file database without
// ever replacing an existing path. This closes the narrow race between the
// legacy state.json lock domain and a concurrent new-version init holding
// state.lock: whichever creates state.db first wins, and the other fails safely.
func commitSQLiteNoReplace(sourcePath, destinationPath string) error {
	if err := os.Link(sourcePath, destinationPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("状态数据库已被另一个进程创建，迁移未覆盖其内容")
		}
		return err
	}
	// The caller's deferred cleanup removes sourcePath. Both hard links refer
	// to the already finalized 0600 inode, so publication itself is the only
	// commit point and has no post-commit rollback race.
	return nil
}

func sqliteMigrationMarkerPath(databasePath string) string {
	return filepath.Join(filepath.Dir(databasePath), "state.json.migrated")
}

func writeSQLiteMigrationMarker(databasePath, legacyPath, backupName string) error {
	record := map[string]string{
		"migrated_at": time.Now().UTC().Format(time.RFC3339Nano),
		"database":    filepath.Base(databasePath),
		"legacy":      filepath.Base(legacyPath),
		"backup":      backupName,
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return atomicWrite(sqliteMigrationMarkerPath(databasePath), raw, 0600)
}

func backupLegacyJSONFile(databasePath, legacyPath, prefix string, now time.Time) (string, error) {
	raw, err := os.ReadFile(legacyPath)
	if err != nil {
		return "", err
	}
	return backupLegacyJSONBytes(databasePath, raw, prefix, now)
}

func backupLegacyJSONBytes(databasePath string, raw []byte, prefix string, now time.Time) (string, error) {
	directory := stateBackupDir(databasePath)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	name := uniqueLegacyJSONBackupName(directory, prefix, now)
	if err := atomicWrite(filepath.Join(directory, name), raw, 0600); err != nil {
		return "", err
	}
	return name, nil
}

func uniqueLegacyJSONBackupName(directory, prefix string, now time.Time) string {
	base := prefix + now.Format("20060102-150405")
	name := base + ".json"
	for suffix := 1; ; suffix++ {
		if _, err := os.Stat(filepath.Join(directory, name)); errors.Is(err, os.ErrNotExist) {
			return name
		}
		name = fmt.Sprintf("%s-%d.json", base, suffix)
	}
}

func unusedSQLitePath(directory, pattern string) (string, error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

func backupSQLiteBeforeWrite(path string, controlChanged bool, now time.Time) error {
	directory := stateBackupDir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	previous := filepath.Join(directory, "state.previous.db")
	if controlChanged {
		if err := sqliteBackupTo(path, previous); err != nil {
			return err
		}
	}
	daily := filepath.Join(directory, "state-"+now.Format("20060102")+".db")
	if _, err := os.Stat(daily); errors.Is(err, os.ErrNotExist) {
		if controlChanged {
			if err := copyInactiveFileAtomic(previous, daily, 0600); err != nil {
				return err
			}
		} else if err := sqliteBackupTo(path, daily); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return pruneDailySQLiteBackups(directory, 14)
}

// installSQLiteStateForRestore builds and verifies a complete replacement
// database before touching the live SQLite family. The old family is moved as
// one rollback unit; any installation or verification failure restores the
// exact original bytes instead of attempting a row-level merge.
func installSQLiteStateForRestore(path string, state *State, now time.Time) (*sqliteRestoreQuarantine, error) {
	temporaryPath, err := unusedSQLitePath(filepath.Dir(path), ".state-restore-*.db")
	if err != nil {
		return nil, err
	}
	defer removeSQLiteFiles(temporaryPath)
	if err := saveState(temporaryPath, state); err != nil {
		return nil, fmt.Errorf("构建恢复数据库: %w", err)
	}
	wantHash, err := sqliteStateHash(state)
	if err != nil {
		return nil, err
	}
	if err := finalizeStandaloneSQLite(temporaryPath); err != nil {
		return nil, fmt.Errorf("合并恢复数据库 WAL: %w", err)
	}
	verified, err := loadSQLiteBackupState(temporaryPath)
	if err != nil {
		return nil, fmt.Errorf("校验恢复数据库: %w", err)
	}
	gotHash, err := sqliteStateHash(verified)
	if err != nil {
		return nil, err
	}
	if gotHash != wantHash {
		return nil, errors.New("恢复数据库业务内容校验不一致")
	}
	quarantine, err := quarantineSQLiteForRestore(path, now)
	if err != nil {
		return nil, fmt.Errorf("隔离恢复前 SQLite 文件: %w", err)
	}
	rollback := func(installErr error) (*sqliteRestoreQuarantine, error) {
		removeSQLiteFiles(path)
		if restoreErr := quarantine.restore(); restoreErr != nil {
			return nil, errors.Join(installErr, fmt.Errorf("恢复原 SQLite 文件: %w", restoreErr))
		}
		return nil, installErr
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return rollback(fmt.Errorf("原子安装恢复数据库: %w", err))
	}
	if err := chmodSQLiteFiles(path); err != nil {
		return rollback(fmt.Errorf("设置恢复数据库权限: %w", err))
	}
	live, err := loadState(path)
	if err != nil {
		return rollback(fmt.Errorf("重新打开恢复数据库: %w", err))
	}
	liveHash, err := sqliteStateHash(live)
	if err != nil {
		return rollback(err)
	}
	if liveHash != wantHash {
		return rollback(errors.New("安装后的恢复数据库业务内容校验不一致"))
	}
	return quarantine, nil
}

func sqliteBackupTo(sourcePath, destinationPath string) error {
	if filepath.Clean(sourcePath) == filepath.Clean(destinationPath) {
		return errors.New("SQLite 备份源和目标不能相同")
	}
	if _, err := os.Stat(sourcePath); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0700); err != nil {
		return err
	}
	temporaryPath, err := unusedSQLitePath(filepath.Dir(destinationPath), ".state-backup-*.db")
	if err != nil {
		return err
	}
	defer removeSQLiteFiles(temporaryPath)
	source, _, err := openSQLiteState(sourcePath)
	if err != nil {
		return err
	}
	sourceHash, err := sqliteMetadata(source, "state_hash")
	if err != nil {
		source.Close()
		return fmt.Errorf("读取 SQLite 快照源业务哈希: %w", err)
	}
	backupErr := createSQLiteOnlineBackup(source, temporaryPath)
	closeErr := source.Close()
	if backupErr != nil {
		return fmt.Errorf("创建 SQLite 一致性快照: %w", backupErr)
	}
	if closeErr != nil {
		return closeErr
	}
	if err := finalizeStandaloneSQLite(temporaryPath); err != nil {
		return fmt.Errorf("校验并合并 SQLite 快照 WAL: %w", err)
	}
	if _, err := loadSQLiteBackupState(temporaryPath); err != nil {
		return fmt.Errorf("重新打开 SQLite 快照复核业务内容: %w", err)
	}
	backupHash, err := sqliteFileMetadata(temporaryPath, "state_hash")
	if err != nil {
		return err
	}
	if sourceHash == "" || backupHash != sourceHash {
		return errors.New("SQLite 快照业务哈希与源数据库不一致")
	}
	if err := finalizeStandaloneSQLite(temporaryPath); err != nil {
		return err
	}
	if err := replaceStandaloneSQLiteFile(temporaryPath, destinationPath); err != nil {
		return err
	}
	return chmodSQLiteFiles(destinationPath)
}

type sqliteBackupFactory interface {
	NewBackup(string) (*moderncsqlite.Backup, error)
}

func createSQLiteOnlineBackup(source *sql.DB, destinationPath string) error {
	connection, err := source.Conn(context.Background())
	if err != nil {
		return err
	}
	defer connection.Close()
	return connection.Raw(func(driverConnection any) (resultErr error) {
		factory, ok := driverConnection.(sqliteBackupFactory)
		if !ok {
			return errors.New("SQLite 驱动不支持在线备份 API")
		}
		backup, err := factory.NewBackup(destinationPath)
		if err != nil {
			return err
		}
		finished := false
		defer func() {
			if !finished {
				resultErr = errors.Join(resultErr, backup.Finish())
			}
		}()
		deadline := time.Now().Add(5 * time.Second)
		for {
			more, stepErr := backup.Step(128)
			if stepErr != nil {
				message := strings.ToLower(stepErr.Error())
				if (strings.Contains(message, "locked") || strings.Contains(message, "busy")) && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
					continue
				}
				return stepErr
			}
			if !more {
				finished = true
				return backup.Finish()
			}
		}
	})
}

// replaceStandaloneSQLiteFile preserves the previous destination until the
// replacement has been committed. Windows cannot rename over an existing
// file, so deleting the old backup first would create an avoidable loss window.
func replaceStandaloneSQLiteFile(sourcePath, destinationPath string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(sourcePath, destinationPath)
	}
	if _, err := os.Stat(destinationPath); errors.Is(err, os.ErrNotExist) {
		return os.Rename(sourcePath, destinationPath)
	} else if err != nil {
		return err
	}
	rollbackPath, err := unusedSQLitePath(filepath.Dir(destinationPath), ".state-replaced-*.db")
	if err != nil {
		return err
	}
	if err := os.Rename(destinationPath, rollbackPath); err != nil {
		return fmt.Errorf("暂存旧 SQLite 备份: %w", err)
	}
	if err := os.Rename(sourcePath, destinationPath); err != nil {
		if restoreErr := os.Rename(rollbackPath, destinationPath); restoreErr != nil {
			return errors.Join(fmt.Errorf("替换 SQLite 备份: %w", err), fmt.Errorf("恢复旧 SQLite 备份: %w", restoreErr))
		}
		return fmt.Errorf("替换 SQLite 备份（旧备份已恢复）: %w", err)
	}
	if err := os.Remove(rollbackPath); err != nil {
		return fmt.Errorf("新 SQLite 备份已生效，但清理旧备份失败: %w", err)
	}
	return nil
}

func copyInactiveFileAtomic(sourcePath, destinationPath string, mode os.FileMode) error {
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	return atomicWrite(destinationPath, raw, mode)
}

func pruneDailySQLiteBackups(directory string, keep int) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	names := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && len(name) == len("state-20060102.db") && strings.HasPrefix(name, "state-") && strings.HasSuffix(name, ".db") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for len(names) > keep {
		if err := os.Remove(filepath.Join(directory, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}

func finalizeStandaloneSQLite(path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		db.Close()
		return err
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		db.Close()
		return err
	}
	var journalMode string
	if err := db.QueryRow(`PRAGMA journal_mode = DELETE`).Scan(&journalMode); err != nil {
		db.Close()
		return err
	}
	if !strings.EqualFold(journalMode, "delete") {
		db.Close()
		return fmt.Errorf("无法把独立 SQLite 文件切换到 DELETE journal: %s", journalMode)
	}
	if err := verifySQLiteIntegrity(db); err != nil {
		db.Close()
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	for _, suffix := range sqliteFamilySuffixes[1:] {
		sidecar := path + suffix
		if info, err := os.Stat(sidecar); err == nil && info.Size() != 0 {
			return fmt.Errorf("独立 SQLite 文件仍有未合并 sidecar: %s (%d bytes)", sidecar, info.Size())
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		_ = os.Remove(sidecar)
	}
	return chmodSQLiteFiles(path)
}

func sqliteFileMetadata(path, key string) (string, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return "", err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	return sqliteMetadata(db, key)
}
