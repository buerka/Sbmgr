package main

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const representativeMihomoTemplate = `# Representative complete template fixture
mixed-port: 7893
mode: rule
dns:
  enable: true
  nameserver:
    - tls://192.0.2.53
rule-providers:
  sample-rules:
    type: http
    url: https://rules.example/sample.yaml
proxies:
  - name: Node A
    type: vless
    server: old.example.com
    port: 443
    uuid: old-node-a-uuid
    network: tcp
    tls: true
    udp: true
    servername: old-sni.example.com
    client-fingerprint: chrome
    skip-cert-verify: true
    alpn: [h2, http/1.1]
    reality-opts:
      public-key: old-public-key
      short-id: old-short-id
    packet-encoding: xudp
  - name: Relay A via Node A
    type: vless
    server: old.example.com
    port: 443
    uuid: old-relay-a-uuid
    network: tcp
    tls: true
    udp: true
    servername: old-sni.example.com
    client-fingerprint: chrome
    reality-opts:
      public-key: old-public-key
      short-id: old-short-id
    packet-encoding: xudp
  - name: Relay B via Node A
    type: vless
    server: old.example.com
    port: 443
    uuid: old-relay-b-uuid
    network: tcp
    tls: true
    udp: true
    servername: old-sni.example.com
    client-fingerprint: chrome
    reality-opts:
      public-key: old-public-key
      short-id: old-short-id
    packet-encoding: xudp
proxy-groups:
  - name: Primary
    type: select
    proxies: [Auto, Relay B via Node A, Relay A via Node A, Node A, DIRECT]
  - name: Auto
    type: url-test
    proxies: [Node A, Relay B via Node A, Relay A via Node A]
  - name: Service
    type: select
    proxies: [Relay B via Node A, Relay A via Node A]
  - name: Secondary
    type: select
    proxies: [Primary, Node A, Relay B via Node A, Relay A via Node A]
  - name: Fallback
    type: select
    proxies: [Primary, Node A, Relay B via Node A, Relay A via Node A, DIRECT]
rules:
  - AND,((NETWORK,UDP),(DST-PORT,443)),REJECT
  - DOMAIN-SUFFIX,service.example,Service
  - RULE-SET,sample-rules,Primary
  - MATCH,Fallback
`

func TestMihomoTemplatePreservesRulesAndFiltersNodes(t *testing.T) {
	templatePath := filepath.Join(t.TempDir(), "v.yaml")
	if err := os.WriteFile(templatePath, []byte(representativeMihomoTemplate), 0600); err != nil {
		t.Fatal(err)
	}
	s := &State{Client: ClientSettings{
		Server: "managed.example", Port: 8443, ServerName: "tls.example",
		PublicKey: "managed-public-key", ShortID: "managed-short-id", MihomoTemplate: templatePath,
	}}
	u := User{
		Name: "alice", Enabled: true, Devices: []Device{{Name: "电脑", Enabled: true}},
		Nodes: []Node{
			{Name: "Node A", Device: "电脑", UUID: "11111111-1111-4111-8111-111111111111"},
			{Name: "Relay B via Node A", Device: "电脑", UUID: "22222222-2222-4222-8222-222222222222"},
			{Name: "Relay A via Node A", Device: "手机", UUID: "33333333-3333-4333-8333-333333333333"},
		},
	}

	exported, err := renderMihomoDevice(s, u, "电脑")
	if err != nil {
		t.Fatal(err)
	}
	text := string(exported)
	for _, forbidden := range []string{"old-node-a-uuid", "old-relay-a-uuid", "old-relay-b-uuid", "33333333-3333-4333-8333-333333333333"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("unavailable credential %q leaked into export:\n%s", forbidden, text)
		}
	}
	for _, preserved := range []string{"Representative complete template fixture", "tls://192.0.2.53", "https://rules.example/sample.yaml", "DOMAIN-SUFFIX,service.example,Service", "client-fingerprint: chrome", "packet-encoding: xudp"} {
		if !strings.Contains(text, preserved) {
			t.Fatalf("template content %q was not preserved:\n%s", preserved, text)
		}
	}

	document, err := parseMihomoTemplate(exported)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := yamlRootMapping(document)
	rules, _ := yamlMapValue(root, "rules")
	if got := rules.Content[len(rules.Content)-1].Value; got != "MATCH,Fallback" {
		t.Fatalf("final rule = %q, want MATCH rule from template", got)
	}
	proxies, _ := yamlMapValue(root, "proxies")
	if len(proxies.Content) != 2 {
		t.Fatalf("exported %d proxies, want 2", len(proxies.Content))
	}
	wantUUIDs := map[string]string{
		"Node A":             "11111111-1111-4111-8111-111111111111",
		"Relay B via Node A": "22222222-2222-4222-8222-222222222222",
	}
	for _, proxy := range proxies.Content {
		name := yamlStringMapValue(proxy, "name")
		if got, want := yamlStringMapValue(proxy, "uuid"), wantUUIDs[name]; got != want {
			t.Fatalf("proxy %q UUID = %q want %q", name, got, want)
		}
		if got := yamlStringMapValue(proxy, "server"); got != "managed.example" {
			t.Fatalf("proxy %q server = %q", name, got)
		}
		if got := yamlStringMapValue(proxy, "port"); got != "8443" {
			t.Fatalf("proxy %q port = %q", name, got)
		}
	}
	if nodeA := proxies.Content[0]; yamlStringMapValue(nodeA, "skip-cert-verify") != "true" {
		t.Fatal("template-only proxy options were not retained")
	}

	groups, _ := yamlMapValue(root, "proxy-groups")
	wantGroups := map[string][]string{
		"Primary":   {"Auto", "Node A", "Relay B via Node A", "DIRECT"},
		"Auto":      {"Node A", "Relay B via Node A"},
		"Service":   {"Relay B via Node A"},
		"Secondary": {"Primary", "Node A", "Relay B via Node A"},
		"Fallback":  {"Primary", "Node A", "Relay B via Node A", "DIRECT"},
	}
	for name, want := range wantGroups {
		if got := mihomoGroupProxyValues(groups, name); !reflect.DeepEqual(got, want) {
			t.Fatalf("group %q proxies = %#v want %#v", name, got, want)
		}
	}
}

func TestMihomoTemplateEmptyPartialGroupFallsBackToPrimary(t *testing.T) {
	templatePath := filepath.Join(t.TempDir(), "v.yaml")
	if err := os.WriteFile(templatePath, []byte(representativeMihomoTemplate), 0600); err != nil {
		t.Fatal(err)
	}
	s := &State{Client: ClientSettings{Server: "example.com", Port: 443, ServerName: "example.com", PublicKey: "pub", ShortID: "id", MihomoTemplate: templatePath}}
	u := User{Name: "alice", Enabled: true, Devices: []Device{{Name: "默认设备", Enabled: true}}, Nodes: []Node{{Name: "Node A", Device: "默认设备", UUID: "11111111-1111-4111-8111-111111111111"}}}
	exported, err := renderMihomoDevice(s, u, "默认设备")
	if err != nil {
		t.Fatal(err)
	}
	document, _ := parseMihomoTemplate(exported)
	root, _ := yamlRootMapping(document)
	groups, _ := yamlMapValue(root, "proxy-groups")
	if got, want := mihomoGroupProxyValues(groups, "Service"), []string{"Primary"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Service proxies = %#v want %#v", got, want)
	}
}

func TestTemplateCommandRejectsInvalidFileWithoutChangingState(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	if err := saveState(statePath, &State{Version: stateVersion}); err != nil {
		t.Fatal(err)
	}
	invalidPath := filepath.Join(dir, "invalid.yaml")
	if err := os.WriteFile(invalidPath, []byte("rules: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.templateCmd([]string{"set", "--path", invalidPath}); err == nil {
		t.Fatal("invalid template was accepted")
	}
	s, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if s.Client.MihomoTemplate != "" {
		t.Fatalf("invalid template changed state to %q", s.Client.MihomoTemplate)
	}
}

func mihomoGroupProxyValues(groups *yaml.Node, name string) []string {
	for _, group := range groups.Content {
		if yamlStringMapValue(group, "name") != name {
			continue
		}
		proxies, ok := yamlMapValue(group, "proxies")
		if !ok {
			return nil
		}
		result := make([]string, 0, len(proxies.Content))
		for _, proxy := range proxies.Content {
			result = append(result, strings.TrimSpace(proxy.Value))
		}
		return result
	}
	return nil
}
