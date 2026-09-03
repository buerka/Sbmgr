package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentStateUpdatesAreSerialized(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(statePath, &State{Version: stateVersion, Users: []User{{Name: "alice", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	const workers = 32
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
			errs <- a.trafficCmd([]string{"add", "alice", "--upload", "1"})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	s, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Users[0].Upload; got != workers {
		t.Fatalf("lost concurrent updates: got %d want %d", got, workers)
	}
}

func TestStateVersionOneMigratesToCurrent(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(statePath, []byte(`{"version":1,"users":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if s.Version != stateVersion {
		t.Fatalf("version = %d want %d", s.Version, stateVersion)
	}
	if err := saveState(statePath, s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(statePath), "backups", "state.previous.json")); err != nil {
		t.Fatal("migration did not preserve previous state:", err)
	}
}

func TestInvalidDuplicateStateIsRejected(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	raw := fmt.Sprintf(`{"version":%d,"users":[{"name":"Alice"},{"name":"alice"}]}`, stateVersion)
	if err := os.WriteFile(statePath, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadState(statePath); err == nil {
		t.Fatal("duplicate usernames were accepted")
	}
}

func TestSaveCreatesPreviousAndDailyBackups(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	s := &State{Version: stateVersion, Users: []User{{Name: "alice", Enabled: true}}}
	if err := saveState(statePath, s); err != nil {
		t.Fatal(err)
	}
	initial, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	s.Users[0].Upload = 42
	if err := saveState(statePath, s); err != nil {
		t.Fatal(err)
	}
	previous, err := os.ReadFile(filepath.Join(dir, "backups", "state.previous.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(previous) != string(initial) {
		t.Fatal("state.previous.json does not contain the pre-write state")
	}
	daily, err := filepath.Glob(filepath.Join(dir, "backups", "state-????????.json"))
	if err != nil || len(daily) != 1 {
		t.Fatalf("daily backups = %#v err=%v", daily, err)
	}
}

func TestDailyBackupRotationKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	for day := 1; day <= 16; day++ {
		name := filepath.Join(dir, fmt.Sprintf("state-202601%02d.json", day))
		if err := os.WriteFile(name, []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := pruneDailyStateBackups(dir, 14); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "state-????????.json"))
	if len(files) != 14 {
		t.Fatalf("kept %d backups", len(files))
	}
	if _, err := os.Stat(filepath.Join(dir, "state-20260101.json")); !os.IsNotExist(err) {
		t.Fatal("oldest backup was not pruned")
	}
	if _, err := os.Stat(filepath.Join(dir, "state-20260116.json")); err != nil {
		t.Fatal("newest backup was pruned")
	}
}
