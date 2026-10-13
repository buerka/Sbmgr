package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const analyticsApplicationID = 0x5342414e // SBAN
const analyticsSchemaVersion = 1

// Generations live in the business database and survive ordinary edits. SQLite
// may reuse INTEGER PRIMARY KEY values after deletion; these random generations
// prevent a newly created account or device from inheriting the old history.
var sqliteAnalyticsIdentitySchema = []string{
	`CREATE TABLE IF NOT EXISTS analytics_user_identity (user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, generation TEXT NOT NULL UNIQUE CHECK(length(generation)=32)) STRICT`,
	`CREATE TABLE IF NOT EXISTS analytics_device_identity (device_id INTEGER PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE, generation TEXT NOT NULL UNIQUE CHECK(length(generation)=32)) STRICT`,
	`INSERT OR IGNORE INTO analytics_user_identity SELECT id,lower(hex(randomblob(16))) FROM users`,
	`INSERT OR IGNORE INTO analytics_device_identity SELECT id,lower(hex(randomblob(16))) FROM devices`,
	`CREATE TRIGGER IF NOT EXISTS analytics_user_identity_insert AFTER INSERT ON users BEGIN INSERT INTO analytics_user_identity VALUES(new.id,lower(hex(randomblob(16)))); END`,
	`CREATE TRIGGER IF NOT EXISTS analytics_device_identity_insert AFTER INSERT ON devices BEGIN INSERT INTO analytics_device_identity VALUES(new.id,lower(hex(randomblob(16)))); END`,
}

var analyticsMirrorSchema = []string{
	`CREATE TABLE metadata (key TEXT PRIMARY KEY,value TEXT NOT NULL) STRICT`,
	`CREATE TABLE users (id INTEGER PRIMARY KEY,generation TEXT NOT NULL UNIQUE,name TEXT NOT NULL,name_key TEXT NOT NULL UNIQUE) STRICT`,
	`CREATE TABLE devices (id INTEGER PRIMARY KEY,user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,generation TEXT NOT NULL UNIQUE,name TEXT NOT NULL,name_key TEXT NOT NULL,ordinal INTEGER NOT NULL,UNIQUE(user_id,name_key)) STRICT`,
	`CREATE TABLE nodes (id INTEGER PRIMARY KEY,user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,identity TEXT NOT NULL UNIQUE,auth_user TEXT NOT NULL UNIQUE) STRICT`,
}

var analyticsHistoryTables = []string{"analytics_connections", "analytics_daily", "analytics_coverage", "analytics_gap_events", "analytics_events", "analytics_remote_cursor"}

func analyticsDatabasePath(statePath string) string {
	return strings.TrimSuffix(statePath, filepath.Ext(statePath)) + ".analytics.db"
}

func analyticsLockPath(statePath string) string { return analyticsDatabasePath(statePath) + ".lock" }

type analyticsDatabase struct {
	*sql.DB
	lock        *stateFileLock
	path        string
	controlHash string
}

func (db *analyticsDatabase) Close() error {
	return errors.Join(db.DB.Close(), chmodSQLiteFiles(db.path), db.lock.release())
}

func openAnalyticsFile(path string, create bool) (*sql.DB, error) {
	if create {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err = f.Close(); err != nil {
			return nil, err
		}
	}
	dsn, err := analyticsSQLiteDSN(path, "rw")
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*sql.DB, error) { db.Close(); return nil, err }
	for _, q := range []string{"PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON", "PRAGMA trusted_schema=OFF"} {
		if _, err = db.Exec(q); err != nil {
			return fail(err)
		}
	}
	if create {
		tx, err := db.Begin()
		if err != nil {
			return fail(err)
		}
		defer tx.Rollback()
		for _, q := range append(append([]string{}, analyticsMirrorSchema...), sqliteAnalyticsSchema...) {
			if _, err = tx.Exec(q); err != nil {
				return fail(err)
			}
		}
		for _, q := range []string{fmt.Sprintf("PRAGMA application_id=%d", analyticsApplicationID), fmt.Sprintf("PRAGMA user_version=%d", analyticsSchemaVersion), `INSERT INTO metadata VALUES('format','sbmgr-analytics-v1')`} {
			if _, err = tx.Exec(q); err != nil {
				return fail(err)
			}
		}
		if err = tx.Commit(); err != nil {
			return fail(err)
		}
	} else {
		var appID, version int
		var format string
		if err = db.QueryRow("PRAGMA application_id").Scan(&appID); err != nil {
			return fail(err)
		}
		if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
			return fail(err)
		}
		if appID != analyticsApplicationID || version != analyticsSchemaVersion {
			return fail(errors.New("分析数据库格式或版本不匹配"))
		}
		if err = db.QueryRow(`SELECT value FROM metadata WHERE key='format'`).Scan(&format); err != nil {
			return fail(err)
		}
		if format != "sbmgr-analytics-v1" {
			return fail(errors.New("分析数据库格式标记不匹配"))
		}
	}
	if err = ensureSQLiteWAL(db); err != nil {
		return fail(err)
	}
	if _, err = db.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		return fail(err)
	}
	if err = chmodSQLiteFiles(path); err != nil {
		return fail(err)
	}
	return db, nil
}

type analyticsUserIdentity struct {
	id                    int64
	generation, name, key string
}
type analyticsDeviceIdentity struct {
	id, user              int64
	generation, name, key string
	ordinal               int
}
type analyticsNodeIdentity struct {
	id, user, device int64
	identity, auth   string
}
type analyticsIdentities struct {
	controlHash string
	migrated    string
	users       []analyticsUserIdentity
	devices     []analyticsDeviceIdentity
	nodes       []analyticsNodeIdentity
}

func readAnalyticsIdentities(db *sql.DB) (analyticsIdentities, error) {
	var result analyticsIdentities
	tx, err := db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	err = tx.QueryRow(`SELECT value FROM metadata WHERE key='control_hash'`).Scan(&result.controlHash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	err = tx.QueryRow(`SELECT value FROM metadata WHERE key='analytics_migrated'`).Scan(&result.migrated)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}

	rows, err := tx.Query(`SELECT u.id,k.generation,u.name,u.name_key FROM users u JOIN analytics_user_identity k ON k.user_id=u.id`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var v analyticsUserIdentity
		if err = rows.Scan(&v.id, &v.generation, &v.name, &v.key); err != nil {
			rows.Close()
			return result, err
		}
		result.users = append(result.users, v)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return result, err
	}
	rows, err = tx.Query(`SELECT d.id,d.user_id,k.generation,d.name,d.name_key,d.ordinal FROM devices d JOIN analytics_device_identity k ON k.device_id=d.id`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var v analyticsDeviceIdentity
		if err = rows.Scan(&v.id, &v.user, &v.generation, &v.name, &v.key, &v.ordinal); err != nil {
			rows.Close()
			return result, err
		}
		result.devices = append(result.devices, v)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return result, err
	}
	rows, err = tx.Query(`SELECT id,user_id,device_id,uuid,auth_user FROM nodes`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var v analyticsNodeIdentity
		var uuid string
		if err = rows.Scan(&v.id, &v.user, &v.device, &uuid, &v.auth); err != nil {
			rows.Close()
			return result, err
		}
		v.identity = meshNodeIdentity(Node{UUID: uuid})
		result.nodes = append(result.nodes, v)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func syncAnalyticsIdentities(db *sql.DB, identities analyticsIdentities) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Temporary desired sets make deletion and rename safe even when ordinals or
	// row IDs are reused. They never contain credentials or authorization state.
	for _, q := range []string{
		`CREATE TEMP TABLE wanted_users(id INTEGER PRIMARY KEY,generation TEXT,name TEXT,name_key TEXT)`,
		`CREATE TEMP TABLE wanted_devices(id INTEGER PRIMARY KEY,user_id INTEGER,generation TEXT,name TEXT,name_key TEXT,ordinal INTEGER)`,
		`CREATE TEMP TABLE wanted_nodes(id INTEGER PRIMARY KEY,user_id INTEGER,device_id INTEGER,identity TEXT UNIQUE,auth_user TEXT)`,
	} {
		if _, err = tx.Exec(q); err != nil {
			return err
		}
	}
	for _, v := range identities.users {
		if _, err = tx.Exec(`INSERT INTO wanted_users VALUES(?,?,?,?)`, v.id, v.generation, v.name, v.key); err != nil {
			return err
		}
	}
	for _, v := range identities.devices {
		if _, err = tx.Exec(`INSERT INTO wanted_devices VALUES(?,?,?,?,?,?)`, v.id, v.user, v.generation, v.name, v.key, v.ordinal); err != nil {
			return err
		}
	}
	for _, v := range identities.nodes {
		if _, err = tx.Exec(`INSERT INTO wanted_nodes VALUES(?,?,?,?,?)`, v.id, v.user, v.device, v.identity, v.auth); err != nil {
			return err
		}
	}
	for _, q := range []string{
		`DELETE FROM users WHERE NOT EXISTS(SELECT 1 FROM wanted_users w WHERE w.id=users.id AND w.generation=users.generation)`,
		`DELETE FROM devices WHERE NOT EXISTS(SELECT 1 FROM wanted_devices w WHERE w.id=devices.id AND w.user_id=devices.user_id AND w.generation=devices.generation)`,
		`DELETE FROM nodes WHERE NOT EXISTS(SELECT 1 FROM wanted_nodes w WHERE w.id=nodes.id AND w.device_id=nodes.device_id AND w.identity=nodes.identity)`,
		`DELETE FROM analytics_events WHERE NOT EXISTS(SELECT 1 FROM wanted_nodes w WHERE w.identity=analytics_events.node_identity)`,
		`INSERT INTO users SELECT * FROM wanted_users WHERE true ON CONFLICT(id) DO UPDATE SET name=excluded.name,name_key=excluded.name_key WHERE users.name<>excluded.name OR users.name_key<>excluded.name_key`,
		`INSERT INTO devices SELECT * FROM wanted_devices WHERE true ON CONFLICT(id) DO UPDATE SET name=excluded.name,name_key=excluded.name_key,ordinal=excluded.ordinal WHERE devices.name<>excluded.name OR devices.name_key<>excluded.name_key OR devices.ordinal<>excluded.ordinal`,
		`INSERT INTO nodes SELECT * FROM wanted_nodes WHERE true ON CONFLICT(id) DO UPDATE SET auth_user=excluded.auth_user WHERE nodes.auth_user<>excluded.auth_user`,
		`DROP TABLE wanted_nodes`, `DROP TABLE wanted_devices`, `DROP TABLE wanted_users`,
	} {
		if _, err = tx.Exec(q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Every analytics operation holds a distinct cross-process lock until Close.
// Business writes never wait for an analytics query; a subsequent operation
// reconciles a fresh identity snapshot before accessing retained history.
func openSQLiteAnalytics(statePath string) (*analyticsDatabase, error) {
	state, _, err := openSQLiteState(statePath)
	if err != nil {
		return nil, err
	}
	if err = state.Close(); err != nil {
		return nil, err
	}
	lock, err := acquireStateFileLock(analyticsLockPath(statePath))
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*analyticsDatabase, error) { lock.release(); return nil, err }
	identities, err := readCurrentAnalyticsIdentities(statePath)
	if err != nil {
		return fail(err)
	}
	path := analyticsDatabasePath(statePath)
	_, err = os.Stat(path)
	create := errors.Is(err, os.ErrNotExist)
	if err != nil && !create {
		return fail(err)
	}
	if create {
		if identities.migrated != "" {
			return fail(errors.New("迁移后的分析数据库缺失，请恢复配套快照"))
		}
	}
	db, err := openAnalyticsFile(path, create)
	if err != nil {
		if create {
			removeSQLiteFiles(path)
		}
		return fail(err)
	}
	if err = syncAnalyticsIdentities(db, identities); err != nil {
		db.Close()
		if create {
			removeSQLiteFiles(path)
		}
		return fail(err)
	}
	return &analyticsDatabase{DB: db, lock: lock, path: path, controlHash: identities.controlHash}, nil
}

// Migration is restartable. The source history remains intact until both a
// complete source snapshot and the new independent store have passed integrity
// checks. A dedicated lock serializes migration even for legacy read callers
// that have not acquired the application's state lock.
func migrateSQLiteAnalyticsStorage(statePath string, db *sql.DB) error {
	var legacy int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='analytics_connections'`).Scan(&legacy); err != nil {
		return err
	}
	pending, err := sqliteMetadata(db, "analytics_vacuum_pending")
	if err != nil {
		return err
	}
	if legacy == 0 && pending != "1" {
		return nil
	}
	lock, err := acquireStateFileLock(analyticsLockPath(statePath))
	if err != nil {
		return err
	}
	defer lock.release()
	if err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='analytics_connections'`).Scan(&legacy); err != nil {
		return err
	}
	pending, err = sqliteMetadata(db, "analytics_vacuum_pending")
	if err != nil {
		return err
	}
	if legacy == 0 && pending != "1" {
		return nil
	}
	if legacy > 0 {
		var migrationID string
		if err = db.QueryRow(`SELECT value FROM metadata WHERE key='analytics_migration_id'`).Scan(&migrationID); err != nil {
			return err
		}
		if len(migrationID) != 32 || strings.Trim(migrationID, "0123456789abcdef") != "" {
			return errors.New("分析迁移标记无效")
		}
		backupDir := stateBackupDir(statePath)
		if err = os.MkdirAll(backupDir, 0700); err != nil {
			return err
		}
		backupPath := filepath.Join(backupDir, "state-before-analytics-"+migrationID+".db")
		sourceVerified := false
		if _, err = os.Stat(backupPath); errors.Is(err, os.ErrNotExist) {
			candidate, err := unusedSQLitePath(backupDir, ".analytics-source-*.db")
			if err != nil {
				return err
			}
			defer removeSQLiteFiles(candidate)
			if err = createPrivateAnalyticsSnapshot(db, candidate); err != nil {
				return err
			}
			if err = finalizeStandaloneSQLite(candidate); err != nil {
				return err
			}
			if err = replaceStandaloneSQLiteFile(candidate, backupPath); err != nil {
				return err
			}
			sourceVerified = true
		} else if err != nil {
			return err
		}
		if err := verifyAnalyticsMigrationSource(backupPath, migrationID, !sourceVerified); err != nil {
			return fmt.Errorf("分析迁移源备份校验失败，原历史保留: %w", err)
		}
		analyticsPath := analyticsDatabasePath(statePath)
		destinationVerified := false
		if _, err = os.Stat(analyticsPath); errors.Is(err, os.ErrNotExist) {
			if err = importLegacyAnalytics(backupPath, analyticsPath, migrationID); err != nil {
				return fmt.Errorf("迁移连接分析失败，原历史保留: %w", err)
			}
			destinationVerified = true
		} else if err != nil {
			return err
		}
		history, err := openAnalyticsFile(analyticsPath, false)
		if err != nil {
			return err
		}
		var imported string
		readErr := history.QueryRow(`SELECT value FROM metadata WHERE key='legacy_migration_id'`).Scan(&imported)
		if readErr == nil && imported != migrationID {
			readErr = errors.New("现有分析数据库不属于本次迁移，拒绝覆盖")
		}
		if readErr == nil && !destinationVerified {
			readErr = verifySQLiteIntegrity(history)
		}
		closeErr := history.Close()
		if err = errors.Join(readErr, closeErr); err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, table := range analyticsHistoryTables {
			if _, err = tx.Exec(`DROP TABLE ` + table); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(`DELETE FROM metadata WHERE key='analytics_pruned_at_ns'`); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO metadata(key,value) VALUES('analytics_vacuum_pending','1') ON CONFLICT(key) DO UPDATE SET value='1'`); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO metadata(key,value) VALUES('analytics_migrated',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, migrationID); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	// This is a one-time space reclamation, not a periodic whole-database rewrite.
	// If interrupted, the marker retries it before any later business backup.
	if _, err = db.Exec(`VACUUM`); err != nil {
		return fmt.Errorf("分析历史已迁出，业务库空间回收待重试: %w", err)
	}
	_, err = db.Exec(`DELETE FROM metadata WHERE key='analytics_vacuum_pending'`)
	return err
}

func importLegacyAnalytics(sourcePath, destinationPath, migrationID string) error {
	candidate, err := unusedSQLitePath(filepath.Dir(destinationPath), ".analytics-import-*.db")
	if err != nil {
		return err
	}
	defer removeSQLiteFiles(candidate)
	sourceDSN, err := analyticsSQLiteDSN(sourcePath, "ro")
	if err != nil {
		return err
	}
	source, err := sql.Open("sqlite", sourceDSN)
	if err != nil {
		return err
	}
	identities, err := readAnalyticsIdentities(source)
	closeErr := source.Close()
	if err = errors.Join(err, closeErr); err != nil {
		return err
	}
	db, err := openAnalyticsFile(candidate, true)
	if err != nil {
		return err
	}
	defer db.Close()
	if err = syncAnalyticsIdentities(db, identities); err != nil {
		return err
	}
	if _, err = db.Exec(`ATTACH DATABASE ? AS legacy`, sourcePath); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range analyticsHistoryTables {
		if _, err = tx.Exec(`INSERT INTO ` + table + ` SELECT * FROM legacy.` + table); err != nil {
			return err
		}
	}
	for _, q := range []string{
		`INSERT INTO metadata SELECT key,value FROM legacy.metadata WHERE key='analytics_pruned_at_ns'`,
	} {
		if _, err = tx.Exec(q); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO metadata VALUES('legacy_migration_id',?)`, migrationID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if _, err = db.Exec(`DETACH DATABASE legacy`); err != nil {
		return err
	}
	if err = db.Close(); err != nil {
		return err
	}
	if err = finalizeStandaloneSQLite(candidate); err != nil {
		return err
	}
	if err = replaceStandaloneSQLiteFile(candidate, destinationPath); err != nil {
		return err
	}
	return chmodSQLiteFiles(destinationPath)
}

// An explicit full backup may snapshot analysis separately. Routine business
// pre-write backups deliberately do not call this function.
func sqliteAnalyticsBackupTo(statePath, destinationPath string) error {
	if filepath.Clean(destinationPath) == filepath.Clean(statePath) || filepath.Clean(destinationPath) == filepath.Clean(analyticsDatabasePath(statePath)) {
		return errors.New("分析备份目标不能覆盖活动数据库")
	}
	if _, err := os.Stat(analyticsDatabasePath(statePath)); errors.Is(err, os.ErrNotExist) {
		_, err := checkSQLiteAnalytics(statePath)
		return err
	} else if err != nil {
		return err
	}
	db, err := openSQLiteAnalytics(statePath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err = os.MkdirAll(filepath.Dir(destinationPath), 0700); err != nil {
		return err
	}
	candidate, err := unusedSQLitePath(filepath.Dir(destinationPath), ".analytics-backup-*.db")
	if err != nil {
		return err
	}
	defer removeSQLiteFiles(candidate)
	if err = createPrivateAnalyticsSnapshot(db.DB, candidate); err != nil {
		return err
	}
	if err = finalizeStandaloneSQLite(candidate); err != nil {
		return err
	}
	if err = replaceStandaloneSQLiteFile(candidate, destinationPath); err != nil {
		return err
	}
	return chmodSQLiteFiles(destinationPath)
}

// Disaster recovery restores a matched business database first, then its
// analytics snapshot. Ordinary business restores deliberately create fresh
// generations and therefore cannot attach history from another incarnation.
func analyticsRestoreSnapshot(statePath, sourcePath string) error {
	state, _, err := openSQLiteState(statePath)
	if err != nil {
		return err
	}
	if err = state.Close(); err != nil {
		return err
	}
	lock, err := acquireStateFileLock(analyticsLockPath(statePath))
	if err != nil {
		return err
	}
	defer lock.release()
	identities, err := readCurrentAnalyticsIdentities(statePath)
	if err != nil {
		return err
	}
	source, err := openAnalyticsReadOnly(sourcePath)
	if err != nil {
		return err
	}
	if err = verifySQLiteIntegrity(source); err != nil {
		source.Close()
		return err
	}
	// Full disaster recovery requires the exact identity generations from its
	// paired business snapshot. A merely similar account list is insufficient.
	validUsers := map[int64]string{}
	for _, v := range identities.users {
		validUsers[v.id] = v.generation
	}
	validDevices := map[int64]string{}
	for _, v := range identities.devices {
		validDevices[v.id] = v.generation
	}
	validNodes := map[int64]string{}
	for _, v := range identities.nodes {
		validNodes[v.id] = v.identity
	}
	for _, kind := range []struct {
		table      string
		column     string
		identities map[int64]string
	}{{"users", "generation", validUsers}, {"devices", "generation", validDevices}, {"nodes", "identity", validNodes}} {
		rows, queryErr := source.Query(`SELECT id,` + kind.column + ` FROM ` + kind.table)
		if queryErr != nil {
			source.Close()
			return queryErr
		}
		seen := 0
		for rows.Next() {
			var id int64
			var generation string
			if err = rows.Scan(&id, &generation); err != nil {
				rows.Close()
				source.Close()
				return err
			}
			if wanted, ok := kind.identities[id]; !ok || wanted != generation {
				rows.Close()
				source.Close()
				return errors.New("分析快照与业务身份代次不匹配，拒绝恢复")
			}
			seen++
		}
		if seen != len(kind.identities) {
			rows.Close()
			source.Close()
			return errors.New("分析快照与业务身份集合不匹配，拒绝恢复")
		}
		if err = errors.Join(rows.Err(), rows.Close()); err != nil {
			source.Close()
			return err
		}
	}
	path := analyticsDatabasePath(statePath)
	candidate, err := unusedSQLitePath(filepath.Dir(path), ".analytics-restore-*.db")
	if err != nil {
		source.Close()
		return err
	}
	defer removeSQLiteFiles(candidate)
	err = createPrivateAnalyticsSnapshot(source, candidate)
	if err = errors.Join(err, source.Close()); err != nil {
		return err
	}
	// Reconcile only the staged image. A rejected or failed restore never
	// modifies either the caller's source backup or the active history.
	staged, err := openAnalyticsFile(candidate, false)
	if err != nil {
		return err
	}
	err = syncAnalyticsIdentities(staged, identities)
	if err = errors.Join(err, staged.Close()); err != nil {
		return err
	}
	if err = finalizeStandaloneSQLite(candidate); err != nil {
		return err
	}
	// All active analytics handles honor the same lock and close before this
	// point. Sidecars are absent after successful close/checkpoint.
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if info, statErr := os.Stat(path + suffix); statErr == nil && info.Size() > 0 {
			return errors.New("现有分析数据库仍有未合并日志，拒绝覆盖")
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
	}
	if err = replaceStandaloneSQLiteFile(candidate, path); err != nil {
		return err
	}
	return chmodSQLiteFiles(path)
}

// Read-only deployment check. It must not acquire state.lock: deployment holds
// that lock across both the candidate's migration and this verification.
func checkSQLiteAnalytics(statePath string) (bool, error) {
	path := analyticsDatabasePath(statePath)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat(statePath); errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		dsn, err := analyticsSQLiteDSN(statePath, "ro")
		if err != nil {
			return false, err
		}
		state, err := sql.Open("sqlite", dsn)
		if err != nil {
			return false, err
		}
		migrated, readErr := sqliteMetadata(state, "analytics_migrated")
		closeErr := state.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return false, err
		}
		if migrated != "" {
			return false, errors.New("迁移后的分析数据库缺失，请恢复配套快照")
		}
		return false, nil
	} else if err != nil {
		return false, err
	}
	lock, err := acquireStateFileLock(analyticsLockPath(statePath))
	if err != nil {
		return true, err
	}
	defer lock.release()
	db, err := openAnalyticsReadOnly(path)
	if err != nil {
		return true, err
	}
	defer db.Close()
	if err = verifySQLiteIntegrity(db); err != nil {
		return true, err
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return true, err
	}
	violated := rows.Next()
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return true, err
	}
	if violated {
		return true, errors.New("分析数据库身份关联校验失败")
	}
	var invalid int
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM users WHERE length(generation)<>32)+(SELECT count(*) FROM devices WHERE length(generation)<>32)+(SELECT count(*) FROM nodes n JOIN devices d ON d.id=n.device_id WHERE n.user_id<>d.user_id)`).Scan(&invalid); err != nil {
		return true, err
	}
	if invalid != 0 {
		return true, errors.New("分析数据库身份镜像校验失败")
	}
	return true, nil
}

func openAnalyticsReadOnly(path string) (*sql.DB, error) {
	dsn, err := analyticsSQLiteDSN(path, "ro")
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*sql.DB, error) { db.Close(); return nil, err }
	if _, err = db.Exec(`PRAGMA query_only=ON`); err != nil {
		return fail(err)
	}
	var appID, version int
	var format string
	if err = db.QueryRow(`PRAGMA application_id`).Scan(&appID); err != nil {
		return fail(err)
	}
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fail(err)
	}
	if appID != analyticsApplicationID || version != analyticsSchemaVersion {
		return fail(errors.New("分析数据库格式或版本不匹配"))
	}
	if err = db.QueryRow(`SELECT value FROM metadata WHERE key='format'`).Scan(&format); err != nil {
		return fail(err)
	}
	if format != "sbmgr-analytics-v1" {
		return fail(errors.New("分析数据库格式标记不匹配"))
	}
	for _, table := range append([]string{"metadata", "users", "devices", "nodes"}, analyticsHistoryTables...) {
		var count int
		if err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			return fail(err)
		}
		if count != 1 {
			return fail(errors.New("分析数据库结构缺失"))
		}
	}
	return db, nil
}

// Read this version while holding state.lock alongside the validated State.
// The later analytics operation compares it with its own identity snapshot,
// rejecting stale authorization without holding the business lock for queries.
func analyticsControlVersion(statePath string) (string, error) {
	if !isSQLiteStatePath(statePath) {
		return "", nil
	}
	db, _, err := openSQLiteState(statePath)
	if err != nil {
		return "", err
	}
	defer db.Close()
	return sqliteMetadata(db, "control_hash")
}

func analyticsSQLiteDSN(path, mode string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	uriPath := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	return (&url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=" + mode}).String(), nil
}

func createPrivateAnalyticsSnapshot(source *sql.DB, path string) error {
	// SQLite's native backup API creates its output using the process umask.
	// Reserve the destination with owner-only permissions before it opens it.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return createSQLiteOnlineBackup(source, path)
}

func verifyAnalyticsMigrationSource(path, migrationID string, fullCheck bool) error {
	dsn, err := analyticsSQLiteDSN(path, "ro")
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err = validateSQLiteIdentity(db); err != nil {
		return err
	}
	var found string
	if err = db.QueryRow(`SELECT value FROM metadata WHERE key='analytics_migration_id'`).Scan(&found); err != nil {
		return err
	}
	if found != migrationID {
		return errors.New("分析迁移源备份代次不匹配")
	}
	if fullCheck {
		return verifySQLiteIntegrity(db)
	}
	return nil
}

// Legacy readers may open a database before acquiring the application lock.
// Serialize forward schema changes separately, and re-read the version only
// after acquiring this lock. It is never held while acquiring state.lock.
func ensureSQLiteSchemaSerialized(path string, db *sql.DB, created bool) error {
	// The exclusive file creation already owns initialization. Temporary
	// restore/backup images must not leave persistent migration lock files.
	if created {
		return ensureSQLiteSchema(db, true)
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version >= sqliteSchemaVersion {
		return ensureSQLiteSchema(db, created)
	}
	lock, err := acquireStateFileLock(path + ".migration.lock")
	if err != nil {
		return err
	}
	defer lock.release()
	return ensureSQLiteSchema(db, created)
}

func maintainAnalyticsStorage(statePath string) error {
	db, err := openSQLiteAnalytics(statePath)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = analyticsPrune(tx, time.Now()); err != nil {
		return err
	}
	return tx.Commit()
}

// Called only after taking analytics.lock. Reopening here is essential: a
// restore may have atomically replaced state.db while we waited for that lock.
// Reusing the pre-lock handle would read the old inode and reconcile stale
// generations. Schema work happens before this lock; this read fails closed
// when a concurrent rollback requires another migration attempt.
func readCurrentAnalyticsIdentities(statePath string) (analyticsIdentities, error) {
	var empty analyticsIdentities
	dsn, err := analyticsSQLiteDSN(statePath, "ro")
	if err != nil {
		return empty, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return empty, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, statement := range []string{`PRAGMA query_only=ON`, `PRAGMA trusted_schema=OFF`, `PRAGMA busy_timeout=5000`} {
		if _, err = db.Exec(statement); err != nil {
			return empty, err
		}
	}
	if err = validateSQLiteIdentity(db); err != nil {
		return empty, err
	}
	var version int
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return empty, err
	}
	if version != sqliteSchemaVersion {
		return empty, errors.New("分析等待期间业务数据库已替换，请重试")
	}
	var legacy int
	if err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='analytics_connections'`).Scan(&legacy); err != nil {
		return empty, err
	}
	if legacy > 0 {
		return empty, errors.New("分析等待期间业务数据库已替换，请重试")
	}

	return readAnalyticsIdentities(db)
}
