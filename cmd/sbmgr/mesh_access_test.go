package main

import (
	tea "charm.land/bubbletea/v2"
	"encoding/json"
	"os"
	"reflect"
	"sbmgr/internal/mesh"
	"strings"
	"testing"
	"time"
)

func TestMeshRouteFormExposesEntryAndTerminalExit(t *testing.T) {
	master, _, _ := independentEntryFixture(t)
	m := tuiModel{state: master, mode: tuiMesh, menuCursor: 3, width: 100, height: 36}
	model, _ := m.updateMesh(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	got := model.(tuiModel)
	if got.form.kind != formMeshRoute || len(got.form.fields) != 7 || got.form.fields[5].label != "客户端入口" || got.form.fields[6].label != "末跳出站" {
		t.Fatal("route editor does not expose entry/exit selection")
	}
	for _, size := range [][2]int{{100, 36}, {36, 16}} {
		got.width, got.height = size[0], size[1]
		assertTUIRenderBounds(t, got.View().Content, size[0], size[1])
	}
}

func independentEntryFixture(t *testing.T) (*State, *State, *app) {
	t.Helper()
	topology := meshFixture(t)
	client := &mesh.Client{Server: "relay.example.com", Port: 443, ServerName: "example.com", PublicKey: strings.Repeat("A", 43), ShortID: "abcd"}
	topology.Members[1].Client = client
	if err := setMeshRouteOptions(topology, "independent", []string{"relay"}, "", "", false, "relay", "direct"); err != nil {
		t.Fatal(err)
	}
	masterApp := meshFixtureApp(t, topology, "master")
	agentApp := meshFixtureApp(t, topology, "relay")
	master, _ := loadState(masterApp.statePath)
	agent, _ := loadState(agentApp.statePath)
	masterPlan, err := topology.Compile("master")
	if err != nil {
		t.Fatal(err)
	}
	agentPlan, err := topology.Compile("relay")
	if err != nil {
		t.Fatal(err)
	}
	master.MeshAgent.Active = &masterPlan
	agent.MeshAgent.Active = &agentPlan
	agent.MeshAgent.Identity = &MeshIdentity{Master: false}
	master.Client = ClientSettings{Server: "master.example.com", Port: 443, ServerName: "example.com", PublicKey: strings.Repeat("B", 43)}
	master.Users = []User{{Name: "alice", Enabled: true, QuotaBytes: 100000, QuotaMode: quotaModeTotal, Devices: []Device{{Name: "phone", Enabled: true, SubscriptionToken: newSubscriptionToken()}}, Nodes: []Node{
		{Name: "local", Device: "phone", AuthUser: "alice-local", UUID: newUUID(), Outbound: "direct"},
		{Name: "remote", Device: "phone", AuthUser: "alice-remote", UUID: newUUID(), Outbound: mesh.RouteTag("independent")},
	}}}
	if _, err := ensureNodeMarks(master); err != nil {
		t.Fatal(err)
	}
	return master, agent, agentApp
}

func TestMeshIndependentEntryIdentitySubscriptionAndTerminalOutbound(t *testing.T) {
	master, agent, _ := independentEntryFixture(t)
	if meshNodeLocal(master, master.Users[0].Nodes[1]) {
		t.Fatal("remote identity accepted at master")
	}
	raw, err := renderConfig(master)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), master.Users[0].Nodes[1].UUID) {
		t.Fatal("master rendered remote identity")
	}
	yaml, err := renderMihomoDevice(master, master.Users[0], "phone")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(yaml), "relay.example.com") || !strings.Contains(string(yaml), "master.example.com") {
		t.Fatal("subscription lost entry selection")
	}
	if !meshNodeLocal(agent, master.Users[0].Nodes[1]) {
		t.Fatal("agent did not own its entry")
	}
	raw, err = renderConfig(agent)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range cfg["outbounds"].([]any) {
		obj := item.(map[string]any)
		if obj["tag"] == mesh.RouteTag("independent") {
			found = obj["type"] == "direct"
		}
	}
	if !found {
		t.Fatal("independent terminal exit missing")
	}
}

func TestMeshUsageReplayIdentityIsolationAndSQLiteBaselines(t *testing.T) {
	master, _, app := independentEntryFixture(t)
	n := master.Users[0].Nodes[1]
	usage := []meshUsage{{Auth: n.AuthUser, Identity: meshNodeIdentity(n), Upload: 11, Download: 22}}
	for range 2 {
		if err := mergeMeshUsage(master, "relay", usage); err != nil {
			t.Fatal(err)
		}
	}
	if master.Users[0].Upload != 11 || master.Users[0].Download != 22 {
		t.Fatal("duplicate usage charged")
	}
	usage[0].Upload = 20
	if err := mergeMeshUsage(master, "wrong-member", usage); err != nil {
		t.Fatal(err)
	}
	if master.Users[0].Upload != 11 {
		t.Fatal("wrong member charged")
	}
	usage[0].Identity = strings.Repeat("0", 64)
	if err := mergeMeshUsage(master, "relay", usage); err != nil {
		t.Fatal(err)
	}
	if master.Users[0].Upload != 11 {
		t.Fatal("wrong identity charged")
	}
	if err := saveState(app.statePath, master); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(app.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(master.Counters, loaded.Counters) || !reflect.DeepEqual(master.Mesh, loaded.Mesh) {
		t.Fatal("structured entry or usage persistence changed")
	}
}

func TestMeshAccessLeaseRevocationAndReplayRejection(t *testing.T) {
	master, agent, app := independentEntryFixture(t)
	oldCheck, oldApply := meshCheckCandidate, meshApplyAccess
	meshCheckCandidate = func(s *State) error { _, err := renderConfig(s); return err }
	meshApplyAccess = func(s *State) error { _, err := renderConfig(s); return err }
	t.Cleanup(func() { meshCheckCandidate, meshApplyAccess = oldCheck, oldApply })
	access := buildMeshAccess(master, "relay", 1, time.Now())
	if len(access.Users) != 1 || len(access.Users[0].Nodes) != 1 || access.Users[0].Devices[0].SubscriptionToken != "" {
		t.Fatal("grant includes unrelated identities or subscription token")
	}
	if err := app.installMeshAccess(agent, &access); err != nil {
		t.Fatal(err)
	}
	if err := app.installMeshAccess(agent, &access); err == nil {
		t.Fatal("accepted replay")
	}
	raw, err := renderConfig(agent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), master.Users[0].Nodes[1].UUID) {
		t.Fatal("grant not rendered")
	}
	agent.MeshLease.Until = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
	raw, err = renderConfig(agent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), master.Users[0].Nodes[1].UUID) {
		t.Fatal("expired lease still authenticates")
	}
	master.Users[0].Enabled = false
	access = buildMeshAccess(master, "relay", 2, time.Now())
	if err := app.installMeshAccess(agent, &access); err != nil {
		t.Fatal(err)
	}
	raw, err = renderConfig(agent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), master.Users[0].Nodes[1].UUID) {
		t.Fatal("revoked identity still authenticates")
	}
	if _, err := os.Stat(app.statePath); err != nil {
		t.Fatal(err)
	}
}

func TestMeshLeaseRenewalPreservesLocalLearningAndBillingBudget(t *testing.T) {
	master, agent, app := independentEntryFixture(t)
	oldCheck, oldApply := meshCheckCandidate, meshApplyAccess
	meshCheckCandidate = func(s *State) error { _, err := renderConfig(s); return err }
	meshApplyAccess = func(s *State) error { _, err := renderConfig(s); return err }
	t.Cleanup(func() { meshCheckCandidate, meshApplyAccess = oldCheck, oldApply })
	master.Users[0].IPPolicy = IPPolicy{Enabled: true, Binding: "auto", MaxIPs: 1}
	grant := buildMeshAccess(master, "relay", 1, time.Now())
	if err := app.installMeshAccess(agent, &grant); err != nil {
		t.Fatal(err)
	}
	agent.Users[0].IPPolicy.BoundIPs = []string{"192.0.2.10"}
	agent.Users[0].Upload = master.Users[0].QuotaBytes * 2
	grant = buildMeshAccess(master, "relay", 2, time.Now())
	if err := app.installMeshAccess(agent, &grant); err != nil {
		t.Fatal(err)
	}
	if len(agent.Users[0].IPPolicy.BoundIPs) != 1 || overQuota(agent.Users[0]) || !meshLocalView(agent).Users[0].Enabled {
		t.Fatal("renewal lost local binding or applied a cycle quota to cumulative usage")
	}
	agent.Users[0].Upload = agent.MeshLease.Quota["alice"]
	if meshLocalView(agent).Users[0].Enabled {
		t.Fatal("lease budget was not enforced")
	}
	master.Users[0].IPPolicy = IPPolicy{Enabled: true, Binding: "manual", MaxIPs: 1, BoundIPs: []string{"192.0.2.20"}}
	grant = buildMeshAccess(master, "relay", 3, time.Now())
	if err := app.installMeshAccess(agent, &grant); err != nil {
		t.Fatal(err)
	}
	if agent.Users[0].IPPolicy.BoundIPs[0] != "192.0.2.20" {
		t.Fatal("central policy update did not replace the learned binding")
	}
}
