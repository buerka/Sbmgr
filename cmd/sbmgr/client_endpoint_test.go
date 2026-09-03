package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetClientEndpointUpdatesOnlyPublicExportAddress(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	original := &State{
		Version: stateVersion,
		Client: ClientSettings{
			Server: "192.0.2.10", Port: 443, ServerName: "www.example.com",
			PublicKey: "keep-public-key", ShortID: "keep-short-id", MihomoTemplate: "/keep/template.yaml",
		},
		Users: []User{{Name: "alice", Enabled: true}},
	}
	if err := saveState(statePath, original); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	a := &app{statePath: statePath, out: &output, err: &output}
	if err := a.setClientEndpoint("relay.example.com", 8443); err != nil {
		t.Fatal(err)
	}
	updated, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Client.Server != "relay.example.com" || updated.Client.Port != 8443 {
		t.Fatalf("client endpoint not updated: %#v", updated.Client)
	}
	if updated.Client.ServerName != original.Client.ServerName || updated.Client.PublicKey != original.Client.PublicKey || updated.Client.ShortID != original.Client.ShortID || updated.Client.MihomoTemplate != original.Client.MihomoTemplate {
		t.Fatalf("credentials or template changed: %#v", updated.Client)
	}
	if !strings.Contains(output.String(), "旧文件需要重新导出") {
		t.Fatalf("missing export warning: %q", output.String())
	}
	records, err := readAuditRecords(statePath, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Action != "client.endpoint.update" || !strings.Contains(strings.Join(records[0].Args, " "), "192.0.2.10:443") {
		t.Fatalf("client endpoint audit missing: %#v", records)
	}
}

func TestSetClientEndpointRejectsInvalidAddressWithoutMutation(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(statePath, &State{Version: stateVersion, Client: ClientSettings{Server: "192.0.2.10", Port: 443}}); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: &bytes.Buffer{}, err: &bytes.Buffer{}}
	if err := a.setClientEndpoint("https://bad.example/path", 0); err == nil {
		t.Fatal("invalid client endpoint was accepted")
	}
	state, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Client.Server != "192.0.2.10" || state.Client.Port != 443 {
		t.Fatalf("invalid update mutated state: %#v", state.Client)
	}
}
