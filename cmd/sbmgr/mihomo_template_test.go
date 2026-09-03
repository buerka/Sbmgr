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

const representativeMihomoTemplate = `# 用户提供的完整母版注释
mixed-port: 7893
mode: rule
dns:
  enable: true
  nameserver:
    - tls://1.1.1.1
rule-providers:
  google:
    type: http
    url: https://example.com/google.yaml
proxies:
  - name: LAX
    type: vless
    server: old.example.com
    port: 443
    uuid: old-lax-uuid
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
  - name: ATT via LAX
    type: vless
    server: old.example.com
    port: 443
    uuid: old-att-uuid
    network: tcp
    tls: true
    udp: true
    servername: old-sni.example.com
    client-fingerprint: chrome
    reality-opts:
      public-key: old-public-key
      short-id: old-short-id
    packet-encoding: xudp
  - name: Frontier via LAX
    type: vless
    server: old.example.com
    port: 443
    uuid: old-frontier-uuid
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
  - name: 节点选择
    type: select
    proxies: [Auto⚡, Frontier via LAX, ATT via LAX, LAX, DIRECT]
  - name: Auto⚡
    type: url-test
    proxies: [LAX, Frontier via LAX, ATT via LAX]
  - name: LLM
    type: select
    proxies: [Frontier via LAX, ATT via LAX]
  - name: 通讯
    type: select
    proxies: [节点选择, LAX, Frontier via LAX, ATT via LAX]
  - name: 漏网之🐟
    type: select
    proxies: [节点选择, LAX, Frontier via LAX, ATT via LAX, DIRECT]
rules:
  - AND,((NETWORK,UDP),(DST-PORT,443)),REJECT
  - DOMAIN-SUFFIX,openai.com,LLM
  - RULE-SET,google,节点选择
  - MATCH,漏网之🐟
`

func TestMihomoTemplatePreservesRulesAndFiltersNodes(t *testing.T) {
	templatePath := filepath.Join(t.TempDir(), "v.yaml")
	if err := os.WriteFile(templatePath, []byte(representativeMihomoTemplate), 0600); err != nil {
		t.Fatal(err)
	}
	s := &State{Client: ClientSettings{
		Server: "managed.example.com", Port: 8443, ServerName: "www.cloudflare.com",
		PublicKey: "managed-public-key", ShortID: "managed-short-id", MihomoTemplate: templatePath,
	}}
	u := User{
		Name: "alice", Enabled: true, Devices: []Device{{Name: "电脑", Enabled: true}},
		Nodes: []Node{
			{Name: "LAX", Device: "电脑", UUID: "11111111-1111-4111-8111-111111111111"},
			{Name: "Frontier via LAX", Device: "电脑", UUID: "22222222-2222-4222-8222-222222222222"},
			{Name: "ATT via LAX", Device: "手机", UUID: "33333333-3333-4333-8333-333333333333"},
		},
	}

	exported, err := renderMihomoDevice(s, u, "电脑")
	if err != nil {
		t.Fatal(err)
	}
	text := string(exported)
	for _, forbidden := range []string{"old-lax-uuid", "old-att-uuid", "old-frontier-uuid", "33333333-3333-4333-8333-333333333333"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("unavailable credential %q leaked into export:\n%s", forbidden, text)
		}
	}
	for _, preserved := range []string{"用户提供的完整母版注释", "tls://1.1.1.1", "https://example.com/google.yaml", "DOMAIN-SUFFIX,openai.com,LLM"} {
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
	if got := rules.Content[len(rules.Content)-1].Value; got != "MATCH,漏网之🐟" {
		t.Fatalf("final rule = %q, want MATCH rule from template", got)
	}
	proxies, _ := yamlMapValue(root, "proxies")
	if len(proxies.Content) != 2 {
		t.Fatalf("exported %d proxies, want 2", len(proxies.Content))
	}
	wantUUIDs := map[string]string{
		"LAX":              "11111111-1111-4111-8111-111111111111",
		"Frontier via LAX": "22222222-2222-4222-8222-222222222222",
	}
	for _, proxy := range proxies.Content {
		name := yamlStringMapValue(proxy, "name")
		if got, want := yamlStringMapValue(proxy, "uuid"), wantUUIDs[name]; got != want {
			t.Fatalf("proxy %q UUID = %q want %q", name, got, want)
		}
		if got := yamlStringMapValue(proxy, "server"); got != "managed.example.com" {
			t.Fatalf("proxy %q server = %q", name, got)
		}
		if got := yamlStringMapValue(proxy, "port"); got != "8443" {
			t.Fatalf("proxy %q port = %q", name, got)
		}
	}
	if lax := proxies.Content[0]; yamlStringMapValue(lax, "skip-cert-verify") != "true" {
		t.Fatal("template-only proxy options were not retained")
	}

	groups, _ := yamlMapValue(root, "proxy-groups")
	wantGroups := map[string][]string{
		"节点选择":  {"Auto⚡", "LAX", "Frontier via LAX", "DIRECT"},
		"Auto⚡": {"LAX", "Frontier via LAX"},
		"LLM":   {"Frontier via LAX"},
		"通讯":    {"节点选择", "LAX", "Frontier via LAX"},
		"漏网之🐟":  {"节点选择", "LAX", "Frontier via LAX", "DIRECT"},
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
	u := User{Name: "alice", Enabled: true, Devices: []Device{{Name: "默认设备", Enabled: true}}, Nodes: []Node{{Name: "LAX", Device: "默认设备", UUID: "11111111-1111-4111-8111-111111111111"}}}
	exported, err := renderMihomoDevice(s, u, "默认设备")
	if err != nil {
		t.Fatal(err)
	}
	document, _ := parseMihomoTemplate(exported)
	root, _ := yamlRootMapping(document)
	groups, _ := yamlMapValue(root, "proxy-groups")
	if got, want := mihomoGroupProxyValues(groups, "LLM"), []string{"节点选择"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("LLM proxies = %#v want %#v", got, want)
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
