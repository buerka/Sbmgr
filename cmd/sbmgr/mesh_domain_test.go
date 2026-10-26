package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sbmgr/internal/mesh"
	"testing"
)

func TestMeshSingBoxDomainConfiguration(t *testing.T) {
	bin := os.Getenv("SBMGR_TEST_SING_BOX")
	if bin == "" {
		t.Skip("set SBMGR_TEST_SING_BOX for local configuration checks")
	}
	for _, kind := range []string{"socks", "hysteria2", "wireguard"} {
		t.Run(kind, func(t *testing.T) {
			tr, err := mesh.NewTransport(kind, "relay.example.com", 21000)
			if err != nil {
				t.Fatal(err)
			}
			plan := mesh.Plan{Protocol: mesh.Protocol, Cluster: "test", Member: "master", Master: true, Revision: 1, Hops: []mesh.Hop{{ID: "test", Outgoing: tr.Connection(false)}}}
			cfg := map[string]any{}
			if err := plan.Augment(cfg); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(cfg)
			path := filepath.Join(t.TempDir(), "candidate.json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bin, "check", "-c", path)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal(string(redactSingBoxDiagnostics(out, raw)))
			}
		})
	}
}
