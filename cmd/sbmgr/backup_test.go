package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManualBackupAndRestore(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	s := &State{Version: stateVersion, Users: []User{{Name: "alice", Enabled: true, Upload: 1}}}
	if err := saveState(statePath, s); err != nil {
		t.Fatal(err)
	}
	name, err := createManualStateBackup(statePath, time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	s.Users[0].Upload = 99
	if err := saveState(statePath, s); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.backupCmd([]string{"restore", name}); err != nil {
		t.Fatal(err)
	}
	restored, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Users[0].Upload != 1 {
		t.Fatalf("upload = %d", restored.Users[0].Upload)
	}
	backups, err := listStateBackups(statePath)
	if err != nil {
		t.Fatal(err)
	}
	foundSafetyBackup := false
	for _, backup := range backups {
		if len(backup.Name) >= len("state-manual-") && backup.Name[:len("state-manual-")] == "state-manual-" && backup.Name != name {
			foundSafetyBackup = true
		}
	}
	if !foundSafetyBackup {
		t.Fatal("restore did not create a pre-restore safety backup")
	}
}

func TestManualBackupRetentionKeepsNewestTwenty(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 25; i++ {
		name := filepath.Join(dir, time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC).Format("state-manual-20060102-150405.json"))
		if err := os.WriteFile(name, []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
		stamp := time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC)
		if err := os.Chtimes(name, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if err := pruneManualStateBackups(dir, 20); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "state-manual-*.json"))
	if err != nil || len(files) != 20 {
		t.Fatalf("manual backups=%d err=%v", len(files), err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state-manual-20260101-000000.json")); !os.IsNotExist(err) {
		t.Fatal("oldest manual backup was retained")
	}
	if _, err := os.Stat(filepath.Join(dir, "state-manual-20260101-000024.json")); err != nil {
		t.Fatal("newest manual backup was removed")
	}
}

func TestBackupNameRejectsTraversal(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if _, err := readStateBackup(statePath, "../state.json"); err == nil {
		t.Fatal("path traversal backup name was accepted")
	}
	if validStateBackupName("sing-box.previous.json") {
		t.Fatal("non-state backup was accepted")
	}
}
