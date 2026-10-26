package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sbmgr/internal/mesh"
	"strings"
	"testing"
)

func TestStandaloneWireGuardChildKeepsOneEndpointAndSeparateUserMarks(t *testing.T) {
	tr, err := mesh.NewTransport("wireguard", "wg.example.com", 51820)
	if err != nil {
		t.Fatal(err)
	}
	topology := mesh.Topology{ID: "test", Master: "master", Revision: 1, Members: []mesh.Member{{ID: "master"}, {ID: "exit"}}, Routes: []mesh.Route{{ID: "wg", Hops: []string{"exit"}, Transports: []mesh.Transport{tr}}}}
	plan, _ := topology.Compile("master")
	cfg := map[string]any{"inbounds": []any{map[string]any{"type": "socks", "tag": "occupied", "listen": "127.0.0.1", "listen_port": 48000}}, "outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}}}
	if err := plan.Augment(cfg); err != nil {
		t.Fatal(err)
	}
	// Import just the protocol object into a standalone base configuration.
	// No membership, enrollment, or topology exists in the state below.
	s := &State{BaseConfig: filepath.Join(t.TempDir(), "base.json"), Users: []User{{Name: "local-test", Enabled: true, Nodes: []Node{
		{Name: "one", AuthUser: "one", Outbound: mesh.RouteTag("wg"), RateMark: rateMarkPrefix | 1},
		{Name: "two", AuthUser: "two", Outbound: mesh.RouteTag("wg"), RateMark: rateMarkPrefix | 2},
	}}}}
	raw, _ := json.Marshal(cfg)
	if err := os.WriteFile(s.BaseConfig, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := findNodeTemplate(s, mesh.RouteTag("wg")); !ok {
		t.Fatal("standalone WG cannot be assigned as a child node")
	}
	tags, err := addRateOutbounds(cfg, s, s.Users)
	if err != nil {
		t.Fatal(err)
	}
	if tags["one"] == tags["two"] {
		t.Fatal("users share a mark-bearing outbound")
	}
	if len(cfg["endpoints"].([]any)) != 1 {
		t.Fatal("WG peer identity was cloned")
	}
	inbounds := cfg["inbounds"].([]any)
	if len(inbounds) != 2 {
		t.Fatal("expected one shared local bridge")
	}
	bridge := inbounds[1].(map[string]any)
	if bridge["listen"] != "127.0.0.1" || bridge["listen_port"] != 48001 || len(bridge["users"].([]any)) != 1 {
		t.Fatal("bridge is public, unauthenticated, or overlaps a listener")
	}
	marks := map[uint32]bool{}
	for _, item := range cfg["outbounds"].([]any) {
		out := item.(map[string]any)
		if mark, ok := out["routing_mark"].(uint32); ok {
			marks[mark] = true
			if out["type"] != "socks" || out["server"] != "127.0.0.1" || out["udp_over_tcp"] != true {
				t.Fatal("WG traffic bypasses per-user accounting sockets")
			}
		}
	}
	if len(marks) != 2 {
		t.Fatal("per-user marks missing")
	}
	nft, err := renderNftables(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(nft, "ct direction reply ct mark") {
		t.Fatal("loopback upload would also count as download")
	}
}

func TestMeshEnrollmentAndProtocolChangesAreIndependent(t *testing.T) {
	installMeshTestRuntime(t)
	topology := meshFixture(t)
	a := meshFixtureApp(t, topology, "relay")
	plan, _ := topology.Compile("relay")
	r := meshRequest{Protocol: mesh.Protocol, Cluster: topology.ID, Member: "relay", Operation: "prepare", Transaction: "tx-first", Plan: &plan}
	if _, err := a.meshExecute(r); err != nil {
		t.Fatal(err)
	}
	r.Plan = nil
	for _, op := range []string{"commit", "finalize"} {
		r.Operation = op
		if _, err := a.meshExecute(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := setMeshRoute(topology, "near", []string{"relay"}, "wireguard", "data.example.com:25000", false); err != nil {
		t.Fatal(err)
	}
	topology.Revision++
	plan, _ = topology.Compile("relay")
	r.Operation, r.Transaction, r.Plan = "prepare", "tx-second", &plan
	if _, err := a.meshExecute(r); err != nil {
		t.Fatal("changing traffic protocol required reenrollment:", err)
	}
	s, _ := loadState(a.statePath)
	if s.MeshAgent.Member != "relay" || s.MeshAgent.Cluster != topology.ID || s.MeshAgent.Pending.Hops[1].Incoming.Type != "wireguard" {
		t.Fatal("protocol change altered management identity")
	}
}

func TestMeshInitAddAndEnrollmentDoNotContainWireGuardFields(t *testing.T) {
	a := meshFixtureApp(t, meshFixture(t), "master")
	s, _ := loadState(a.statePath)
	s.Mesh, s.MeshAgent = nil, MeshAgentState{}
	if err := saveState(a.statePath, s); err != nil {
		t.Fatal(err)
	}
	if err := a.meshCmd([]string{"init", "--id", "master"}); err != nil {
		t.Fatal(err)
	}
	if err := a.meshCmd([]string{"add", "--id", "relay", "--host", "management.example.com", "--key", filepath.Join(t.TempDir(), "id_rpc"), "--home", "/srv/sbmgr"}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "enrollment.json")
	if err := a.meshCmd([]string{"export", "--node", "relay", "--output", file}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file)
	for _, field := range []string{"private_key", "network", "address", "peers", "hops", "wireguard"} {
		if strings.Contains(string(raw), field) {
			t.Fatal("management enrollment includes traffic data")
		}
	}
}
