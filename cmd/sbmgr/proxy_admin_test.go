package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeProxyAdminFixture(t *testing.T, baseJSON string) (string, string) {
	t.Helper()
	directory := t.TempDir()
	basePath := filepath.Join(directory, "config.base.json")
	if err := os.WriteFile(basePath, []byte(baseJSON), 0600); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(directory, "state.json")
	state := &State{
		Version:    stateVersion,
		BaseConfig: basePath,
		ConfigPath: filepath.Join(directory, "sing-box.json"),
		InboundTag: "vless-in",
	}
	if err := saveState(statePath, state); err != nil {
		t.Fatal(err)
	}
	return statePath, basePath
}

func writeProxyAdminInput(t *testing.T, directory, name, content string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProxyAdminListOutputsOnlySafeIdentity(t *testing.T) {
	statePath, _ := writeProxyAdminFixture(t, managedProxyFixture)
	var output, stderr bytes.Buffer
	application := &app{statePath: statePath, out: &output, err: &stderr}
	if err := application.adminCmd([]string{"proxy", "list"}); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("list lines=%d: %q", len(lines), output.String())
	}
	for _, line := range lines {
		if fields := strings.Split(line, "\t"); len(fields) != 3 {
			t.Fatalf("list row contains fields other than kind/tag/type: %q", line)
		}
	}
	for _, forbidden := range []string{
		"private-account", "private-password", "private-key-value",
		"proxy.example", "wg.example", "server_port", "RawJSON",
	} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("list leaked %q: %s", forbidden, output.String())
		}
	}
	if !strings.Contains(output.String(), "outbound\tremote\tsocks") ||
		!strings.Contains(output.String(), "endpoint\twg-home\twireguard") {
		t.Fatalf("safe identities missing: %s", output.String())
	}
	if records, err := readAuditRecords(statePath, 10); err != nil || len(records) != 0 {
		t.Fatalf("read-only list wrote audit records: %#v err=%v", records, err)
	}
}

func TestProxyAdminMutationsReadFilesWithoutLeakingContents(t *testing.T) {
	statePath, basePath := writeProxyAdminFixture(t, `{
  "inbounds":[{"type":"vless","tag":"vless-in","users":[]}],
  "outbounds":[{"type":"direct","tag":"direct"}],
  "route":{"final":"direct","rules":[]}
}`)
	directory := filepath.Dir(statePath)
	addSecret := "add-password-must-remain-private"
	replaceSecret := "replacement-password-must-remain-private"
	addPath := writeProxyAdminInput(t, directory, "add.json", `{"type":"socks","tag":"automation-proxy","server":"private.example","server_port":1080,"username":"private-user","password":"`+addSecret+`"}`)
	replacePath := writeProxyAdminInput(t, directory, "replace.json", `{"type":"http","tag":"automation-proxy","server":"new-private.example","server_port":8080,"username":"new-private-user","password":"`+replaceSecret+`"}`)
	deletePath := writeProxyAdminInput(t, directory, "delete.json", `{"type":"http","tag":"automation-proxy"}`)

	var output, stderr bytes.Buffer
	application := &app{statePath: statePath, out: &output, err: &stderr}
	if err := application.proxyAdminCmd([]string{"add", "outbound", "--file", addPath}); err != nil {
		t.Fatal(err)
	}
	if err := application.proxyAdminCmd([]string{"replace", "outbound", "automation-proxy", "--file", replacePath}); err != nil {
		t.Fatal(err)
	}
	if err := application.proxyAdminCmd([]string{"delete", "outbound", "--file", deletePath}); err != nil {
		t.Fatal(err)
	}
	base, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(base, []byte("automation-proxy")) {
		t.Fatalf("delete did not remove object: %s", base)
	}
	records, err := readAuditRecords(statePath, 10)
	if err != nil || len(records) != 3 {
		t.Fatalf("audit records=%#v err=%v", records, err)
	}
	auditJSON, _ := json.Marshal(records)
	visible := output.String() + stderr.String() + string(auditJSON)
	for _, forbidden := range []string{
		addSecret, replaceSecret, "private-user", "new-private-user",
		"private.example", "new-private.example", "server_port",
		addPath, replacePath, deletePath,
	} {
		if strings.Contains(visible, forbidden) {
			t.Fatalf("admin mutation leaked %q: %s", forbidden, visible)
		}
	}
	if !strings.Contains(visible, "automation-proxy") || !strings.Contains(visible, "proxy.outbound.add") {
		t.Fatalf("safe mutation identity/audit metadata missing: %s", visible)
	}
}

func TestProxyAdminApplyFlagReachesTransactionalRollback(t *testing.T) {
	const fixture = `{
  "inbounds":[{"type":"vless","tag":"vless-in","users":[]}],
  "outbounds":[{"type":"direct","tag":"direct"}],
  "route":{"final":"direct","rules":[]}
}`
	statePath, basePath := writeProxyAdminFixture(t, fixture)
	secret := "apply-failure-secret-must-not-leak"
	inputPath := writeProxyAdminInput(t, filepath.Dir(statePath), "apply.json", `{"type":"socks","tag":"apply-test","server":"apply-private.example","server_port":1080,"password":"`+secret+`"}`)
	var output, stderr bytes.Buffer
	application := &app{statePath: statePath, out: &output, err: &stderr}
	err := application.proxyAdminCmd([]string{"add", "outbound", "--file", inputPath, "--apply"})
	if err == nil {
		t.Fatal("--apply unexpectedly succeeded without a configured sing-box executable")
	}
	base, readErr := os.ReadFile(basePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !jsonEqualForProxyAdminTest(base, []byte(fixture)) {
		t.Fatalf("failed apply did not restore base config: %s", base)
	}
	visible := output.String() + stderr.String() + err.Error()
	for _, forbidden := range []string{secret, "apply-private.example", "server_port", inputPath} {
		if strings.Contains(visible, forbidden) {
			t.Fatalf("failed apply leaked %q: %s", forbidden, visible)
		}
	}
	if records, auditErr := readAuditRecords(statePath, 10); auditErr != nil || len(records) != 0 {
		t.Fatalf("failed transaction was audited as success: %#v err=%v", records, auditErr)
	}
}

func TestProxyAdminCredentialsPreserveAddressAndRedactValues(t *testing.T) {
	const fixture = `{
  "inbounds":[{"type":"vless","tag":"vless-in","users":[]}],
  "outbounds":[
    {"type":"direct","tag":"direct"},
    {"type":"socks","tag":"to-frontier","server":"existing.example","server_port":41080,"username":"old-user","password":"old-password"}
  ],
  "route":{"final":"direct","rules":[]}
}`
	statePath, basePath := writeProxyAdminFixture(t, fixture)
	directory := filepath.Dir(statePath)
	username := "new-account-must-remain-private"
	password := "new-password-must-remain-private"
	patchPath := writeProxyAdminInput(t, directory, "credentials.json", `{"username":"`+username+`","password":"`+password+`"}`)
	var output, stderr bytes.Buffer
	application := &app{statePath: statePath, out: &output, err: &stderr}
	if err := application.proxyAdminCmd([]string{"credentials", "to-frontier", "--file", patchPath}); err != nil {
		t.Fatal(err)
	}
	base, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{`"server": "existing.example"`, `"server_port": 41080`, username, password} {
		if !bytes.Contains(base, []byte(required)) {
			t.Fatalf("updated base missing %q: %s", required, base)
		}
	}
	records, err := readAuditRecords(statePath, 10)
	if err != nil || len(records) != 1 {
		t.Fatalf("credential audit records=%#v err=%v", records, err)
	}
	auditJSON, _ := json.Marshal(records)
	visible := output.String() + stderr.String() + string(auditJSON)
	for _, forbidden := range []string{username, password, "old-user", "old-password", patchPath} {
		if strings.Contains(visible, forbidden) {
			t.Fatalf("credential operation leaked %q: %s", forbidden, visible)
		}
	}
	for _, safe := range []string{"to-frontier", "username-change", "password-change", "replace"} {
		if !strings.Contains(visible, safe) {
			t.Fatalf("safe credential metadata %q missing: %s", safe, visible)
		}
	}
}

func TestProxyAdminCredentialsOmittedKeepsAndEmptyClears(t *testing.T) {
	const fixture = `{
  "inbounds":[{"type":"vless","tag":"vless-in","users":[]}],
  "outbounds":[{"type":"socks","tag":"remote","server":"proxy.example","server_port":1080,"username":"keep-this-user","password":"remove-this-password"}],
  "route":{"final":"remote","rules":[]}
}`
	statePath, basePath := writeProxyAdminFixture(t, fixture)
	patchPath := writeProxyAdminInput(t, filepath.Dir(statePath), "clear-password.json", `{"password":""}`)
	application := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := application.proxyAdminCmd([]string{"credentials", "remote", "--file", patchPath}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	var outbounds []map[string]json.RawMessage
	if err := json.Unmarshal(root["outbounds"], &outbounds); err != nil || len(outbounds) != 1 {
		t.Fatalf("outbounds=%#v err=%v", outbounds, err)
	}
	username, err := optionalJSONString(outbounds[0], "username")
	if err != nil || username != "keep-this-user" {
		t.Fatalf("omitted username was not preserved: %q err=%v", username, err)
	}
	if _, exists := outbounds[0]["password"]; exists {
		t.Fatalf("empty password did not clear field: %s", raw)
	}
}

func TestProxyAdminCredentialsStrictSchemaAndRedactedErrors(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: `{}`},
		{name: "array", raw: `[]`},
		{name: "unknown", raw: `{"token":"unknown-secret-value"}`},
		{name: "duplicate", raw: `{"password":"first-secret","password":"second-secret"}`},
		{name: "null", raw: `{"username":null}`},
		{name: "number", raw: `{"password":123456789}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeProxyAdminCredentials([]byte(test.raw))
			if err == nil {
				t.Fatalf("invalid credentials accepted: %s", test.raw)
			}
			for _, secret := range []string{"unknown-secret-value", "first-secret", "second-secret", "123456789"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("schema error leaked %q: %v", secret, err)
				}
			}
		})
	}

	redacted := redactProxyCredentialError(
		errors.New("upstream echoed account-secret and password-secret"),
		OutboundCredentialUpdate{Username: pointerForProxyAdminTest("account-secret"), Password: pointerForProxyAdminTest("password-secret")},
	)
	if redacted == nil || strings.Contains(redacted.Error(), "account-secret") || strings.Contains(redacted.Error(), "password-secret") {
		t.Fatalf("returned error was not redacted: %v", redacted)
	}
}

func TestReadProxyAdminFileEnforcesLimitAndRegularFile(t *testing.T) {
	directory := t.TempDir()
	tooLarge := filepath.Join(directory, "large.json")
	if err := os.WriteFile(tooLarge, bytes.Repeat([]byte("x"), 17), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readProxyAdminFile(tooLarge, 16); err == nil || !strings.Contains(err.Error(), "16") {
		t.Fatalf("oversized file accepted: %v", err)
	}
	if _, err := readProxyAdminFile(directory, 16); err == nil || !strings.Contains(err.Error(), "普通文件") {
		t.Fatalf("directory accepted as input: %v", err)
	}
	valid := writeProxyAdminInput(t, directory, "valid.json", `{}`)
	raw, err := readProxyAdminFile(valid, 16)
	if err != nil || string(raw) != `{}` {
		t.Fatalf("valid file raw=%q err=%v", raw, err)
	}
}

func pointerForProxyAdminTest(value string) *string { return &value }

func jsonEqualForProxyAdminTest(first, second []byte) bool {
	var left, right any
	return json.Unmarshal(first, &left) == nil && json.Unmarshal(second, &right) == nil &&
		reflect.DeepEqual(left, right)
}
