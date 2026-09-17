package main

import (
	"io"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNodeRenamePreservesIdentityUsageAndRates(t *testing.T) {
	state := sqliteFixtureState(2)
	state.Users[0].Nodes[0].UploadMbps = 7
	path := filepath.Join(t.TempDir(), "state.db")
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	before := state.Users[0].Nodes[0]
	a := &app{statePath: path, out: io.Discard, err: io.Discard}
	if err := a.nodeCmd([]string{"set", "alice", "Node A", "--name", "GHA via Relay"}); err != nil {
		t.Fatal(err)
	}
	after, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	got := after.Users[0].Nodes[0]
	if got.Name != "GHA via Relay" {
		t.Fatal("name was not updated")
	}
	got.Name = before.Name
	if !reflect.DeepEqual(got, before) {
		t.Fatal("rename changed identity, history or rates")
	}
	if err := a.nodeCmd([]string{"set", "alice", "GHA via Relay", "--name", "invalid/name"}); err == nil {
		t.Fatal("invalid name accepted")
	}
	loaded, err := loadState(path)
	if err != nil || loaded.Users[0].Nodes[0].Name != "GHA via Relay" {
		t.Fatal("failed rename changed persisted state")
	}
}
