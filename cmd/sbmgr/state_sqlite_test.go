package main

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func sqliteFixtureState(sampleCount int) *State {
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	samples := make([]TrafficSample, sampleCount)
	for index := range samples {
		samples[index] = TrafficSample{At: now.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano), Bytes: int64(index + 1)}
	}
	return &State{
		Version:           stateVersion,
		BaseConfig:        "/srv/sbmgr/config.base.json",
		ConfigPath:        "/srv/sbmgr/sing-box.json",
		InboundTag:        "vless-in",
		SingBoxBin:        "sing-box",
		Service:           "sing-box",
		Counters:          map[string]int64{"user>>>alice:node-a>>>traffic>>>uplink": 101},
		JournalCursor:     "cursor-1",
		PendingSources:    map[string]PendingSource{"alice:node-a": {IP: "203.0.113.10", At: now.Format(time.RFC3339Nano)}},
		IPApplyPending:    true,
		ActiveConnections: map[string]ActiveConnection{"c1": {ID: "c1", User: "alice", Device: "phone", Node: "Node A", AuthUser: "alice:node-a", SourceIP: "203.0.113.10", Target: "example.com:443", StartedAt: now.Format(time.RFC3339), LastSeen: now.Format(time.RFC3339)}},
		OutboundHealth:    map[string]OutboundHealth{"direct": {Tag: "direct", Target: "127.0.0.1:443", Healthy: true, LatencyMS: 2, CheckedAt: now.Format(time.RFC3339)}},
		LastHealthCheck:   now.Format(time.RFC3339),
		Alerts:            []Alert{{At: now.Format(time.RFC3339Nano), User: "alice", Kind: "quota", Message: "test", Acknowledged: true}},
		Client:            ClientSettings{Server: "proxy.example", Port: 443},
		Users: []User{{
			Name:                "alice",
			Enabled:             true,
			QuotaBytes:          20 << 30,
			QuotaMode:           quotaModeTotal,
			Upload:              100,
			Download:            200,
			CurrentUploadMbps:   1.25,
			CurrentDownloadMbps: 2.5,
			SourceIPs: map[string]SourceIPStat{
				"203.0.113.10": {Count: 4, FirstSeen: now.Format(time.RFC3339), LastSeen: now.Format(time.RFC3339), LastNode: "Node A"},
			},
			TrafficSamples: samples,
			UsageHistory:   []UsagePoint{{At: now.Format(time.RFC3339Nano), UploadBytes: 100, DownloadBytes: 200, UploadMbps: 1.25, DownloadMbps: 2.5}},
			BillingHistory: []BillingRecord{{StartedAt: now.AddDate(0, -1, 0).Format(time.RFC3339), EndedAt: now.Format(time.RFC3339), UploadBytes: 90, DownloadBytes: 180, QuotaBytes: 20 << 30}},
			RecentAccesses: []RecentAccess{{Target: "example.com", Device: "phone", Node: "Node A", FirstSeen: now.Format(time.RFC3339), LastSeen: now.Format(time.RFC3339), Count: 3}},
			Devices: []Device{{Name: "phone", Enabled: true, CreatedAt: now.Format(time.RFC3339), LastSeen: now.Format(time.RFC3339), SubscriptionToken: strings.Repeat("a", 32), SourceIPs: map[string]SourceIPStat{
				"203.0.113.10": {Count: 4, FirstSeen: now.Format(time.RFC3339), LastSeen: now.Format(time.RFC3339), LastNode: "Node A"},
			}}},
			Nodes: []Node{{Name: "Node A", Device: "phone", AuthUser: "alice:node-a", UUID: "11111111-1111-4111-8111-111111111111", Outbound: "direct", RateMark: rateMarkPrefix | 1, Upload: 100, Download: 200, Destinations: map[string]AccessStat{
				"example.com": {Count: 3, LastSeen: now.Format(time.RFC3339)},
			}}},
		}},
	}
}

func TestSQLiteStateRoundTripUsesStructuredTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	state := sqliteFixtureState(3)
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, []byte("SQLite format 3\x00")) {
		t.Fatalf("state.db is not SQLite: %q", raw[:min(len(raw), 16)])
	}
	loaded, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	user := findUser(loaded, "alice")
	if user == nil || len(user.Devices) != 1 || len(user.Nodes) != 1 || len(user.TrafficSamples) != 3 {
		t.Fatalf("structured state did not round-trip: %#v", user)
	}
	if user.Nodes[0].Destinations["example.com"].Count != 3 || user.SourceIPs["203.0.113.10"].Count != 4 {
		t.Fatalf("structured statistics did not round-trip: %#v", user)
	}
	if loaded.ActiveConnections["c1"].Target != "example.com:443" || len(loaded.Alerts) != 1 {
		t.Fatalf("global runtime rows did not round-trip: %#v", loaded)
	}
	db := openSQLiteForTest(t, path)
	defer db.Close()
	for table, want := range map[string]int{
		"users": 1, "devices": 1, "nodes": 1, "traffic_samples": 3,
		"usage_history": 1, "recent_accesses": 1, "user_source_ips": 1,
		"device_source_ips": 1, "node_destinations": 1, "active_connections": 1,
		"billing_history": 1, "alerts": 1,
	} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Errorf("%s rows=%d want=%d", table, count, want)
		}
	}
	var document string
	if err := db.QueryRow(`SELECT document FROM settings WHERE id = 1`).Scan(&document); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"users"`, `"alerts"`, `"traffic_samples"`, `"active_connections"`} {
		if strings.Contains(document, forbidden) {
			t.Errorf("settings document contains structured field %s: %s", forbidden, document)
		}
	}
}

func TestLegacyJSONImportIsSerializedAndPreservesSource(t *testing.T) {
	directory := t.TempDir()
	legacyPath := filepath.Join(directory, "state.json")
	databasePath := filepath.Join(directory, "state.db")
	if err := saveJSONState(legacyPath, sqliteFixtureState(2)); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 12
	errorsFound := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			state, err := loadState(databasePath)
			if err == nil && (findUser(state, "alice") == nil || len(state.Users[0].TrafficSamples) != 2) {
				err = errors.New("imported state is incomplete")
			}
			errorsFound <- err
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatal("legacy state.json changed during import")
	}
	backups, err := filepath.Glob(filepath.Join(directory, "backups", "state-imported-json-*.json"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("legacy backups=%v err=%v", backups, err)
	}
}

func TestInvalidLegacyJSONDoesNotCreateDatabase(t *testing.T) {
	directory := t.TempDir()
	legacyPath := filepath.Join(directory, "state.json")
	databasePath := filepath.Join(directory, "state.db")
	raw := []byte(fmt.Sprintf(`{"version":%d,"users":[{"name":"Alice"},{"name":"alice"}]}`, stateVersion))
	if err := os.WriteFile(legacyPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadState(databasePath); err == nil {
		t.Fatal("invalid legacy state was imported")
	}
	if _, err := os.Stat(databasePath); !os.IsNotExist(err) {
		t.Fatalf("failed import left state.db: %v", err)
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil || !bytes.Equal(after, raw) {
		t.Fatalf("invalid legacy source changed: err=%v", err)
	}
}

func TestMigratedLegacyJSONIsNeverSilentlyReimported(t *testing.T) {
	directory := t.TempDir()
	legacyPath := filepath.Join(directory, "state.json")
	databasePath := filepath.Join(directory, "state.db")
	if err := saveJSONState(legacyPath, sqliteFixtureState(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := loadState(databasePath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sqliteMigrationMarkerPath(databasePath)); err != nil {
		t.Fatalf("migration marker missing: %v", err)
	}
	removeSQLiteFiles(databasePath)
	if _, err := loadState(databasePath); err == nil || !strings.Contains(err.Error(), "迁移标记") {
		t.Fatalf("stale JSON was not blocked after database loss: %v", err)
	}
}

func TestSQLiteMigrationPublishNeverReplacesExistingDatabase(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "candidate.db")
	destinationPath := filepath.Join(directory, "state.db")
	candidate := sqliteFixtureState(0)
	candidate.Users[0].Upload = 111
	live := sqliteFixtureState(0)
	live.Users[0].Upload = 999
	if err := saveState(sourcePath, candidate); err != nil {
		t.Fatal(err)
	}
	if err := finalizeStandaloneSQLite(sourcePath); err != nil {
		t.Fatal(err)
	}
	if err := saveState(destinationPath, live); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitSQLiteNoReplace(sourcePath, destinationPath); err == nil {
		t.Fatal("migration publish replaced an existing database")
	}
	after, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed no-replace publish changed live database bytes")
	}
	loaded, err := loadState(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Users[0].Upload; got != 999 {
		t.Fatalf("live database was overwritten: upload=%d", got)
	}
}

func TestSQLiteEmptyCollectionsHaveStableBusinessHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	state := sqliteFixtureState(0)
	state.Counters = map[string]int64{}
	state.PendingSources = map[string]PendingSource{}
	state.ActiveConnections = map[string]ActiveConnection{}
	state.OutboundHealth = map[string]OutboundHealth{}
	state.FleetStatus = map[string]FleetServerStatus{}
	state.Alerts = []Alert{}
	state.Users[0].SourceIPs = map[string]SourceIPStat{}
	state.Users[0].TrafficSamples = []TrafficSample{}
	state.Users[0].UsageHistory = []UsagePoint{}
	state.Users[0].BillingHistory = []BillingRecord{}
	state.Users[0].RecentAccesses = []RecentAccess{}
	state.Users[0].Devices[0].SourceIPs = map[string]SourceIPStat{}
	state.Users[0].Nodes[0].Destinations = map[string]AccessStat{}
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := sqliteStateHash(state)
	if err != nil {
		t.Fatal(err)
	}
	got, err := sqliteStateHash(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("empty collection canonical hash mismatch: got %s want %s", got, want)
	}
}

func TestSQLiteUnicodeNameKeysMatchValidationSemantics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	state := sqliteFixtureState(0)
	state.Users[0].Name = "ÄLICE"
	state.Users[0].Devices[0].Name = "手机Ä"
	state.Users[0].Nodes[0].Name = "节点Ä"
	state.Users[0].Nodes[0].Device = "手机ä"
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	state.Users[0].Name = "älice"
	state.Users[0].Devices[0].Name = "手机ä"
	state.Users[0].Nodes[0].Name = "节点ä"
	state.Users[0].Nodes[0].Device = "手机Ä"
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	db := openSQLiteForTest(t, path)
	defer db.Close()
	for _, table := range []string{"users", "devices", "nodes"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s contains %d rows after Unicode case-only rename", table, count)
		}
	}
}

func TestExistingUnrelatedSQLiteIsRejectedWithoutMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unrelated.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE private_data(value TEXT); INSERT INTO private_data VALUES('keep')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadState(path); err == nil || !strings.Contains(err.Error(), "非 sbmgr") {
		t.Fatalf("unrelated SQLite was accepted: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("opening an unrelated SQLite file changed its bytes")
	}
	for _, suffix := range sqliteFamilySuffixes[1:] {
		sidecar := path + suffix
		if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
			t.Fatalf("opening unrelated SQLite created %s: %v", sidecar, err)
		}
	}
}

func TestSQLiteManualBackupIncludesCommittedWAL(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "state.db")
	if err := saveState(path, sqliteFixtureState(1)); err != nil {
		t.Fatal(err)
	}
	reader := openSQLiteForTest(t, path)
	if _, err := reader.Exec(`PRAGMA wal_autocheckpoint = 0`); err != nil {
		t.Fatal(err)
	}
	readTx, err := reader.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var initial int64
	if err := readTx.QueryRow(`SELECT upload_bytes FROM users WHERE name_key = ?`, sqliteNameKey("alice")).Scan(&initial); err != nil {
		t.Fatal(err)
	}
	updated := sqliteFixtureState(1)
	updated.Users[0].Upload = 777
	if err := saveState(path, updated); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("test setup did not retain a committed WAL: info=%v err=%v", info, err)
	}
	name, err := createManualStateBackup(path, time.Date(2026, 9, 4, 2, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := readTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(directory, "backups", name)
	for _, suffix := range sqliteFamilySuffixes[1:] {
		sidecar := backupPath + suffix
		if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
			t.Fatalf("standalone backup retained sidecar %s: %v", sidecar, err)
		}
	}
	backup, err := readStateBackup(path, name)
	if err != nil {
		t.Fatal(err)
	}
	if got := findUser(backup, "alice").Upload; got != 777 {
		t.Fatalf("backup missed committed WAL data: upload=%d", got)
	}
}

func TestSQLiteBackupReadIsByteForByteImmutable(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "state.db")
	if err := saveState(path, sqliteFixtureState(2)); err != nil {
		t.Fatal(err)
	}
	name, err := createManualStateBackup(path, time.Date(2026, 9, 4, 3, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(directory, "backups", name)
	before, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readStateBackup(path, name); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("reading a finalized SQLite backup mutated its bytes")
	}
	for _, suffix := range sqliteFamilySuffixes[1:] {
		sidecar := backupPath + suffix
		if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
			t.Fatalf("reading backup created sidecar %s: %v", sidecar, err)
		}
	}
}

func TestSQLiteBackupRestoreRecoversMissingOrCorruptMainDB(t *testing.T) {
	for _, scenario := range []string{"missing", "corrupt"} {
		t.Run(scenario, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "state.db")
			state := sqliteFixtureState(1)
			state.Users[0].Upload = 321
			if err := saveState(path, state); err != nil {
				t.Fatal(err)
			}
			name, err := createManualStateBackup(path, time.Date(2026, 9, 4, 4, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			removeSQLiteFiles(path)
			if scenario == "corrupt" {
				if err := os.WriteFile(path, []byte("not a sqlite database"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			a := &app{statePath: path, out: io.Discard, err: io.Discard}
			if err := a.backupCmd([]string{"restore", name}); err != nil {
				t.Fatal(err)
			}
			restored, err := loadState(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := findUser(restored, "alice").Upload; got != 321 {
				t.Fatalf("restored upload=%d", got)
			}
			if scenario == "corrupt" {
				quarantined, err := filepath.Glob(filepath.Join(directory, "backups", "quarantine", "state-*", "state.db"))
				if err != nil || len(quarantined) != 1 {
					t.Fatalf("corrupt database quarantine=%v err=%v", quarantined, err)
				}
			}
		})
	}
}

func TestSQLiteRestoreFailureCanPutEntireOriginalFamilyBack(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "state.db")
	originals := map[string][]byte{
		"":         []byte("broken-main"),
		"-wal":     []byte("broken-wal"),
		"-shm":     []byte("broken-shm"),
		"-journal": []byte("broken-journal"),
	}
	for suffix, raw := range originals {
		if err := os.WriteFile(path+suffix, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	quarantine, err := quarantineSQLiteForRestore(path, time.Date(2026, 9, 4, 4, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	// Model a failed installation that left a partial new SQLite family.
	for _, suffix := range sqliteFamilySuffixes {
		if err := os.WriteFile(path+suffix, []byte("partial-new"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	removeSQLiteFiles(path)
	if err := quarantine.restore(); err != nil {
		t.Fatal(err)
	}
	for suffix, want := range originals {
		got, err := os.ReadFile(path + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("restored SQLite family %q=%q want %q", suffix, got, want)
		}
	}
}

func TestSQLiteReadUsesOneCommittedSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	first := sqliteFixtureState(1)
	first.Users[0].Upload = 111
	first.Users[0].Nodes[0].Upload = 111
	second := sqliteFixtureState(1)
	second.Users[0].Upload = 222
	second.Users[0].Nodes[0].Upload = 222
	if err := saveState(path, first); err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 2)
	go func() {
		for index := 0; index < 20; index++ {
			state := first
			if index%2 == 1 {
				state = second
			}
			if err := saveState(path, state); err != nil {
				errCh <- err
				return
			}
		}
		errCh <- nil
	}()
	go func() {
		for index := 0; index < 40; index++ {
			state, err := loadState(path)
			if err != nil {
				errCh <- err
				return
			}
			user := findUser(state, "alice")
			if user == nil || len(user.Nodes) != 1 || user.Upload != user.Nodes[0].Upload {
				errCh <- fmt.Errorf("mixed SQLite snapshot: %#v", user)
				return
			}
		}
		errCh <- nil
	}()
	for index := 0; index < 2; index++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
}

func TestSQLiteRecentAccessReorderingRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	state := sqliteFixtureState(0)
	older := state.Users[0].RecentAccesses[0]
	older.FirstSeen = "2026-09-04T00:30:00Z"
	older.LastSeen = "2026-09-04T01:00:00Z"
	state.Users[0].RecentAccesses = []RecentAccess{older}
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	newer := RecentAccess{Target: "new.example", Device: "phone", Node: "Node A", FirstSeen: "2026-09-04T02:00:00Z", LastSeen: "2026-09-04T02:00:00Z", Count: 1}
	state.Users[0].RecentAccesses = []RecentAccess{newer, older}
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Users[0].RecentAccesses; len(got) != 2 || got[0].Target != newer.Target || got[1].Target != older.Target {
		t.Fatalf("recent access order did not round-trip: %#v", got)
	}
}

func TestSQLiteRestoreAllowsReusedNodeCredentials(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "state.db")
	alice := sqliteFixtureState(0)
	if err := saveState(path, alice); err != nil {
		t.Fatal(err)
	}
	name, err := createManualStateBackup(path, time.Date(2026, 9, 4, 5, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	bob := sqliteFixtureState(0)
	bob.Users[0].Name = "bob"
	bob.Users[0].Nodes[0].AuthUser = "bob:node-a"
	// UUID is deliberately reused after alice has been removed.
	if err := saveState(path, &State{Version: stateVersion}); err != nil {
		t.Fatal(err)
	}
	if err := saveState(path, bob); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: path, out: io.Discard, err: io.Discard}
	if err := a.backupCmd([]string{"restore", name}); err != nil {
		t.Fatal(err)
	}
	restored, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if findUser(restored, "alice") == nil || findUser(restored, "bob") != nil {
		t.Fatalf("credential-reuse restore returned wrong users: %#v", restored.Users)
	}
}

func TestInitRestoresBaseWhenStateValidationFails(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "sing-box.json")
	basePath := filepath.Join(directory, "config.base.json")
	statePath := filepath.Join(directory, "state.db")
	config := `{"inbounds":[{"type":"vless","tag":"vless-in","listen_port":443,"users":[` +
		`{"name":"Alice","uuid":"11111111-1111-4111-8111-111111111111"},` +
		`{"name":"alice","uuid":"22222222-2222-4222-8222-222222222222"}]}]}`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	original := []byte("ORIGINAL BASE BYTES\n")
	if err := os.WriteFile(basePath, original, 0600); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.initCmd([]string{"--config", configPath, "--base", basePath, "--import-users"}); err == nil {
		t.Fatal("init unexpectedly accepted duplicate imported users")
	}
	after, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("failed init did not restore base: %q", after)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("failed init left state database: %v", err)
	}
}

func TestInitMarkerFailureDoesNotCommitBaseOrDatabase(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "sing-box.json")
	basePath := filepath.Join(directory, "config.base.json")
	statePath := filepath.Join(directory, "state.db")
	legacyPath := filepath.Join(directory, "state.json")
	config := `{"inbounds":[{"type":"vless","tag":"vless-in","listen_port":443,"users":[]}]}`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	original := []byte("UNCHANGED BASE\n")
	if err := os.WriteFile(basePath, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveJSONState(legacyPath, sqliteFixtureState(0)); err != nil {
		t.Fatal(err)
	}
	markerPath := sqliteMigrationMarkerPath(statePath)
	if err := os.Mkdir(markerPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerPath, "keep"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.initCmd([]string{"--config", configPath, "--base", basePath, "--force"}); err == nil {
		t.Fatal("init unexpectedly succeeded when migration marker was unwritable")
	}
	after, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("marker failure changed live base: %q", after)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("marker failure committed state database: %v", err)
	}
}

func TestSQLiteHistorySyncIsIncremental(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	state := sqliteFixtureState(500)
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	db := openSQLiteForTest(t, path)
	for _, statement := range []string{
		`CREATE TABLE write_probe(operation TEXT NOT NULL)`,
		`CREATE TRIGGER traffic_insert_probe AFTER INSERT ON traffic_samples BEGIN INSERT INTO write_probe VALUES('insert'); END`,
		`CREATE TRIGGER traffic_update_probe AFTER UPDATE ON traffic_samples BEGIN INSERT INTO write_probe VALUES('update'); END`,
		`CREATE TRIGGER traffic_delete_probe AFTER DELETE ON traffic_samples BEGIN INSERT INTO write_probe VALUES('delete'); END`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	state.Users[0].Upload++
	state.Users[0].TrafficSamples = append(state.Users[0].TrafficSamples, TrafficSample{At: "2026-09-04T02:00:00Z", Bytes: 999})
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	assertSQLiteProbeCounts(t, path, map[string]int{"insert": 1, "update": 0, "delete": 0})
	clearSQLiteProbe(t, path)
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	assertSQLiteProbeCounts(t, path, map[string]int{"insert": 0, "update": 0, "delete": 0})
	clearSQLiteProbe(t, path)
	state.Users[0].Upload++
	state.Users[0].TrafficSamples = append(state.Users[0].TrafficSamples[1:], TrafficSample{At: "2026-09-04T02:00:01Z", Bytes: 1000})
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	assertSQLiteProbeCounts(t, path, map[string]int{"insert": 1, "update": 0, "delete": 1})
}

func openSQLiteForTest(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db
}

func clearSQLiteProbe(t *testing.T, path string) {
	t.Helper()
	db := openSQLiteForTest(t, path)
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM write_probe`); err != nil {
		t.Fatal(err)
	}
}

func assertSQLiteProbeCounts(t *testing.T, path string, want map[string]int) {
	t.Helper()
	db := openSQLiteForTest(t, path)
	defer db.Close()
	for operation, expected := range want {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM write_probe WHERE operation = ?`, operation).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != expected {
			t.Errorf("%s count=%d want=%d", operation, count, expected)
		}
	}
}
