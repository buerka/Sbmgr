package mesh

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func testTopology(t *testing.T) Topology {
	t.Helper()
	topology := Topology{ID: "test", Master: "master", Revision: 1, Members: []Member{{ID: "master"}, {ID: "relay"}, {ID: "exit"}}}
	transport := func(kind, server string, port int) Transport {
		tr, err := NewTransport(kind, server, port)
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	topology.Routes = []Route{
		{ID: "chain", Hops: []string{"relay", "exit"}, Transports: []Transport{transport("socks", "relay.example.com", 20000), transport("wireguard", "exit.example.com", 20000)}},
		{ID: "near", Hops: []string{"relay"}, Transports: []Transport{transport("hysteria2", "relay.example.com", 20001)}},
		{ID: "home", Hops: []string{"master"}},
	}
	return topology
}

func TestMembershipDoesNotRequireTrafficProtocol(t *testing.T) {
	topology := Topology{ID: "test", Master: "master", Revision: 1, Members: []Member{{ID: "master"}, {ID: "slave"}}}
	if err := topology.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"master", "slave"} {
		p, err := topology.Compile(id)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(p)
		for _, field := range []string{"private_key", "address", "peers", "wireguard"} {
			if strings.Contains(string(raw), field) {
				t.Fatal("membership contains traffic parameters")
			}
		}
		if len(p.Hops) != 0 {
			t.Fatal("enrollment activated traffic")
		}
	}
}

func TestAnyMemberCanRelayOrExitWithMixedProtocols(t *testing.T) {
	topology := testTopology(t)
	relay, err := topology.Compile("relay")
	if err != nil {
		t.Fatal(err)
	}
	if relay.Hops[0].Incoming.Type != "socks" || relay.Hops[0].Outgoing.Type != "wireguard" {
		t.Fatal("mixed protocol relay not compiled")
	}
	if relay.Hops[1].Incoming.Type != "hysteria2" || relay.Hops[1].Outgoing != nil {
		t.Fatal("same member cannot exit locally")
	}
	master, _ := topology.Compile("master")
	if master.Hops[2].Outgoing != nil || master.Hops[2].Incoming != nil {
		t.Fatal("local master exit requires a transport")
	}
	raw, _ := json.Marshal(relay)
	if strings.Contains(string(raw), topology.Routes[0].Transports[1].ServerKey) {
		t.Fatal("relay received remote WG private key")
	}
	exit, _ := topology.Compile("exit")
	raw, _ = json.Marshal(exit)
	if strings.Contains(string(raw), topology.Routes[0].Transports[1].ClientKey) {
		t.Fatal("exit received remote WG private key")
	}
	original := append([]Member(nil), topology.Members...)
	changed, err := NewTransport("wg", "alternate.example.com", 21000)
	if err != nil {
		t.Fatal(err)
	}
	topology.Routes[1].Transports[0] = changed
	if err := topology.Validate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(topology.Members, original) {
		t.Fatal("protocol switch changed membership")
	}
}

func TestTopologyRejectsCyclesMissingProtocolsAndPortConflicts(t *testing.T) {
	cases := map[string]func(*Topology){
		"cycle":            func(p *Topology) { p.Routes[0].Hops[1] = "relay" },
		"missing":          func(p *Topology) { p.Routes[0].Hops[0] = "unknown" },
		"master-loop":      func(p *Topology) { p.Routes[0].Hops[0] = "master" },
		"missing-protocol": func(p *Topology) { p.Routes[0].Transports = nil },
		"port-conflict":    func(p *Topology) { p.Routes[1].Transports[0].Port = 20000 },
		"injection":        func(p *Topology) { p.Routes[0].Transports[0].Server = "bad\nserver" },
		"key":              func(p *Topology) { p.Routes[0].Transports[1].ClientKey = "invalid" },
		"unknown-protocol": func(p *Topology) { p.Routes[0].Transports[0].Type = "unknown" },
		"duplicate-member": func(p *Topology) { p.Members[1].ID = "master" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := testTopology(t)
			mutate(&p)
			if p.Validate() == nil {
				t.Fatal("invalid topology accepted")
			}
		})
	}
}

func TestProtocolObjectsAndRemoteKeyIsolation(t *testing.T) {
	for _, kind := range []string{"socks", "hysteria2", "wireguard"} {
		t.Run(kind, func(t *testing.T) {
			tr, err := NewTransport(kind, "relay.example.com", 20000)
			if err != nil {
				t.Fatal(err)
			}
			topology := Topology{ID: "test", Master: "master", Revision: 1, Members: []Member{{ID: "master"}, {ID: "relay"}}, Routes: []Route{{ID: "one", Hops: []string{"relay"}, Transports: []Transport{tr}}}}
			for _, id := range []string{"master", "relay"} {
				p, err := topology.Compile(id)
				if err != nil {
					t.Fatal(err)
				}
				cfg := map[string]any{"outbounds": []any{map[string]any{"type": "direct", "tag": "base"}}}
				if err := p.Augment(cfg); err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(cfg)
				if strings.Contains(string(raw), "bind_interface") || strings.Contains(string(raw), "sbm-wg0") {
					t.Fatal("protocol changed host networking")
				}
				if kind == "wireguard" {
					endpoints, _ := cfg["endpoints"].([]any)
					if len(endpoints) != 1 || endpoints[0].(map[string]any)["system"] != false {
						t.Fatal("WG is not a userspace protocol endpoint")
					}
				} else if _, ok := cfg["endpoints"]; ok {
					t.Fatal("non-WG protocol depends on WG")
				}
				if kind == "hysteria2" && id == "master" {
					if strings.Contains(string(raw), "PRIVATE KEY") || strings.Contains(string(raw), "\"insecure\"") {
						t.Fatal("TLS peer pinning was bypassed")
					}
				}
				if err := p.Augment(cfg); err == nil {
					t.Fatal("generated tag collision accepted")
				}
			}
		})
	}
}
