package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sbmgr/internal/mesh"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func meshFixture(t *testing.T) *mesh.Topology {
	t.Helper()
	topology := &mesh.Topology{ID: "test", Master: "master", Revision: 1}
	for _, id := range []string{"master", "relay", "exit"} {
		topology.Members = append(topology.Members, mesh.Member{ID: id, SSHHost: id + ".example.com", SSHPort: 22, SSHUser: "root", SSHKeyPath: filepath.Join(t.TempDir(), "id_test"), AppDir: "/srv/sbmgr"})
	}
	if err := setMeshRoute(topology, "chain", []string{"relay", "exit"}, "socks,wireguard", "", false); err != nil {
		t.Fatal(err)
	}
	if err := setMeshRoute(topology, "near", []string{"relay"}, "hysteria2", "", false); err != nil {
		t.Fatal(err)
	}
	return topology
}

func meshFixtureApp(t *testing.T, topology *mesh.Topology, id string) *app {
	t.Helper()
	dir := t.TempDir()
	s := &State{Version: stateVersion, BaseConfig: filepath.Join(dir, "base.json"), ConfigPath: filepath.Join(dir, "running.json"), InboundTag: "vless-in", Service: "sing-box", SingBoxBin: "sing-box", MeshAgent: MeshAgentState{Cluster: topology.ID, Member: id}}
	if id == topology.Master {
		s.Mesh = topology
	}
	if err := os.WriteFile(s.BaseConfig, []byte(`{"inbounds":[{"type":"vless","tag":"vless-in","listen":"127.0.0.1","listen_port":1443,"users":[]}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: filepath.Join(dir, "state.db"), out: io.Discard, err: io.Discard}
	if err := saveState(a.statePath, s); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestMeshSQLiteStructuredRoundTripAndVersionTenMigration(t *testing.T) {
	topology := meshFixture(t)
	a := meshFixtureApp(t, topology, topology.Master)
	s, err := loadState(a.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Mesh, topology) {
		t.Fatal("topology round-trip mismatch")
	}
	db, _, err := openSQLiteState(a.statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var document string
	if err := db.QueryRow(`SELECT document FROM settings`).Scan(&document); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(document, topology.Routes[0].Transports[1].ServerKey) || strings.Contains(document, topology.Routes[0].Transports[0].Credential) {
		t.Fatal("topology records stored in global settings instead of structured tables")
	}
	var members, routes int
	if err := db.QueryRow(`SELECT count(*) FROM mesh_members`).Scan(&members); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM mesh_routes`).Scan(&routes); err != nil {
		t.Fatal(err)
	}
	if members != 3 || routes != 2 {
		t.Fatal("missing structured topology rows")
	}
	db.Close()
	legacy := sqliteFixtureState(2)
	legacy.Version = 10
	legacyPath := filepath.Join(t.TempDir(), "state.json")
	raw, _ := json.Marshal(legacy)
	if err := os.WriteFile(legacyPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != stateVersion || loaded.Mesh != nil || loaded.MeshAgent.Cluster != "" || loaded.Users[0].Upload != legacy.Users[0].Upload {
		t.Fatal("old state changed identities, counters, or enabled mesh")
	}
}

func TestMeshSQLiteSchemaTwoMigrationRetainsBusinessState(t *testing.T) {
	a := meshFixtureApp(t, meshFixture(t), "master")
	s, err := loadState(a.statePath)
	if err != nil {
		t.Fatal(err)
	}
	s.Mesh = nil
	s.MeshAgent = MeshAgentState{}
	if err := saveState(a.statePath, s); err != nil {
		t.Fatal(err)
	}
	db, _, err := openSQLiteState(a.statePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`DROP TABLE mesh_members`, `DROP TABLE mesh_routes`, `PRAGMA user_version=2`, `UPDATE metadata SET value='2' WHERE key='schema_version'`} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	loaded, err := loadState(a.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Mesh != nil || loaded.InboundTag != s.InboundTag {
		t.Fatal("schema migration changed business state")
	}
}

func installMeshTestRuntime(t *testing.T) *int {
	t.Helper()
	oldCheck, oldApply := meshCheckCandidate, meshApplyCandidate
	count := new(int)
	meshCheckCandidate = func(s *State) error { _, err := renderConfig(s); return err }
	meshApplyCandidate = func(a *app, s *State, target, previous *mesh.Plan) error {
		*count++
		_, err := renderConfig(s)
		return err
	}
	t.Cleanup(func() { meshCheckCandidate, meshApplyCandidate = oldCheck, oldApply })
	return count
}

func TestMeshAgentPreparationIdempotencyIdentityAndRollback(t *testing.T) {
	count := installMeshTestRuntime(t)
	topology := meshFixture(t)
	a := meshFixtureApp(t, topology, "relay")
	plan, err := topology.Compile("relay")
	if err != nil {
		t.Fatal(err)
	}
	r := meshRequest{Protocol: mesh.Protocol, Cluster: topology.ID, Member: "relay", Transaction: "tx-test", Operation: "prepare", Plan: &plan}
	if _, err := a.meshExecute(r); err != nil {
		t.Fatal(err)
	}
	if _, err := a.meshExecute(r); err != nil {
		t.Fatal("prepare retry failed")
	}
	s, _ := loadState(a.statePath)
	if s.MeshAgent.Active != nil || s.MeshAgent.Phase != "prepared" || *count != 0 {
		t.Fatal("prepare changed active runtime")
	}
	wrong := r
	wrong.Member = "exit"
	if _, err := a.meshExecute(wrong); err == nil {
		t.Fatal("wrong member accepted")
	}
	wrong = r
	wrong.Transaction = "tx-conflict"
	if _, err := a.meshExecute(wrong); err == nil {
		t.Fatal("concurrent transaction accepted")
	}
	r.Operation, r.Plan = "commit", nil
	if _, err := a.meshExecute(r); err != nil {
		t.Fatal(err)
	}
	if _, err := a.meshExecute(r); err != nil || *count != 1 {
		t.Fatal("commit not idempotent")
	}
	r.Operation = "rollback"
	if _, err := a.meshExecute(r); err != nil {
		t.Fatal(err)
	}
	if _, err := a.meshExecute(r); err != nil || *count != 2 {
		t.Fatal("rollback not idempotent")
	}
	s, _ = loadState(a.statePath)
	if s.MeshAgent.Active != nil || s.MeshAgent.Transaction != "" {
		t.Fatal("rollback did not restore previous state")
	}
}

func TestMeshCoordinatorRollsBackUncertainCommitAndCanRecover(t *testing.T) {
	installMeshTestRuntime(t)
	topology := meshFixture(t)
	apps := map[string]*app{}
	for _, member := range topology.Members {
		apps[member.ID] = meshFixtureApp(t, topology, member.ID)
	}
	oldExchange := meshExchange
	t.Cleanup(func() { meshExchange = oldExchange })
	uncertain, unreachable := true, true
	meshExchange = func(_ *app, m mesh.Member, r meshRequest, _ bool) (meshResponse, error) {
		if r.Operation == "rollback" && m.ID == "exit" && unreachable {
			return meshResponse{}, errors.New("test transport unavailable")
		}
		response, err := apps[m.ID].meshExecute(r)
		if r.Operation == "commit" && m.ID == "exit" && uncertain {
			return meshResponse{}, errors.New("test response lost after commit")
		}
		return response, err
	}
	if err := apps["master"].meshCoordinate("apply"); err == nil {
		t.Fatal("uncertain commit reported success")
	}
	s, _ := loadState(apps["master"].statePath)
	if s.MeshRollout == nil || s.MeshRollout.Decision != "rollback" {
		t.Fatal("recovery decision not retained")
	}
	uncertain, unreachable = false, false
	if err := apps["master"].meshCoordinate("recover"); err != nil {
		t.Fatal(err)
	}
	for _, a := range apps {
		s, _ := loadState(a.statePath)
		if s.MeshAgent.Active != nil || s.MeshAgent.Transaction != "" {
			t.Fatal("participant left partially committed")
		}
	}
	if err := apps["master"].meshCoordinate("apply"); err != nil {
		t.Fatal(err)
	}
	for _, a := range apps {
		s, _ := loadState(a.statePath)
		if s.MeshAgent.Active == nil || s.MeshAgent.Active.Revision != 1 || s.MeshAgent.Transaction != "" {
			t.Fatal("successful rollout not finalized")
		}
	}
}

func TestMeshProtocolBoundsRejectTrailingUnknownAndSecretDiagnostics(t *testing.T) {
	for _, raw := range []string{`{"unknown":"confidential"}`, `{} {}`, strings.Repeat("x", mesh.MaxMessage+1)} {
		var r meshRequest
		err := decodeMeshJSON(strings.NewReader(raw), &r)
		if err == nil || strings.Contains(err.Error(), "confidential") {
			t.Fatal("unsafe protocol error")
		}
	}
}

func TestMeshMenuAndSubscriptionDeliveryFitAndHideSecrets(t *testing.T) {
	topology := meshFixture(t)
	state := qrTUITestState()
	state.Mesh = topology
	state.MeshAgent = MeshAgentState{Cluster: topology.ID, Member: topology.Master}
	for _, size := range [][2]int{{64, 18}, {42, 12}, {100, 24}} {
		m := tuiModel{state: state, width: size[0], height: size[1], mode: tuiMesh}
		for _, render := range []string{m.renderMesh(), m.renderMeshTopology(), m.renderSubscriptionActions(), m.renderSubscriptions()} {
			assertTUIRenderBounds(t, render, m.width, m.height)
			if strings.Contains(render, topology.Routes[0].Transports[1].ServerKey) || strings.Contains(render, state.Users[0].Devices[0].SubscriptionToken) {
				t.Fatal("default TUI disclosed credentials")
			}
		}
	}
	m := tuiModel{state: state, width: 80, height: 24, mode: tuiSubscriptions}
	model, cmd := m.updateSubscriptions(tea.KeyPressMsg(tea.Key{Text: "c", Code: 'c'}))
	if cmd == nil || model.(tuiModel).statusError {
		t.Fatal("copy shortcut unavailable outside QR view")
	}
	if got := fmtClipboardMessage(cmd()); got != subscriptionURL(state, state.Users[0].Devices[0]) {
		t.Fatal("clipboard command did not carry complete URL")
	}
}

func fmtClipboardMessage(v any) string {
	value := reflect.ValueOf(v)
	if value.Kind() == reflect.String {
		return value.String()
	}
	return ""
}

func TestSubscriptionCopyReloadsTokenAndSaveDoesNotEchoIt(t *testing.T) {
	a := meshFixtureApp(t, meshFixture(t), "master")
	s, _ := loadState(a.statePath)
	s.Users = qrTUITestState().Users
	s.Subscription = SubscriptionSettings{Enabled: true, Listen: "127.0.0.1:18080"}
	s.Users[0].Nodes[0].UUID = newUUID()
	s.Users[0].Nodes[0].AuthUser = "alice-phone"
	if err := saveState(a.statePath, s); err != nil {
		t.Fatal(err)
	}
	m := tuiModel{a: a, state: s, mode: tuiSubscriptions}
	fresh, _ := loadState(a.statePath)
	fresh.Users[0].Devices[0].SubscriptionToken = newSubscriptionToken()
	if err := saveState(a.statePath, fresh); err != nil {
		t.Fatal(err)
	}
	_, cmd := m.deliverSubscription("alice", "phone", false)
	if cmd == nil || fmtClipboardMessage(cmd()) != subscriptionURL(fresh, fresh.Users[0].Devices[0]) {
		t.Fatal("copy used stale credentials")
	}
	_, cmd = m.deliverSubscription("alice", "phone", true)
	message := cmd().(tuiActionMsg)
	if message.err != nil {
		t.Fatal(message.err)
	}
	if strings.Contains(message.output, fresh.Users[0].Devices[0].SubscriptionToken) {
		t.Fatal("save output disclosed token")
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(a.statePath), "exports", "subscription-link-*.txt"))
	if len(files) != 1 {
		t.Fatal("link export missing")
	}
	raw, _ := os.ReadFile(files[0])
	if !bytes.Equal(raw, []byte(subscriptionURL(fresh, fresh.Users[0].Devices[0])+"\n")) {
		t.Fatal("link export incomplete")
	}
}

func TestMeshEnrollmentBootstrapsEmptySlaveAndPinsIdentity(t *testing.T) {
	topology := meshFixture(t)
	master := meshFixtureApp(t, topology, "master")
	joinFile := filepath.Join(t.TempDir(), "join.json")
	if err := master.meshCmd([]string{"export", "--node", "relay", "--output", joinFile}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	slave := &app{statePath: filepath.Join(dir, "state.db"), out: io.Discard, err: io.Discard}
	if err := slave.meshCmd([]string{"join", "--file", joinFile}); err != nil {
		t.Fatal(err)
	}
	s, err := loadState(slave.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if s.MeshAgent.Identity == nil || s.MeshAgent.Member != "relay" || s.MeshAgent.Active != nil || len(s.Users) != 0 {
		t.Fatal("join activated traffic or omitted pinned identity")
	}
	if _, err := renderConfig(s); err != nil {
		t.Fatal(err)
	}
	if err := slave.meshCmd([]string{"join", "--file", joinFile}); err == nil {
		t.Fatal("join replaced an existing identity")
	}
	installMeshTestRuntime(t)
	plan, _ := topology.Compile("relay")
	plan.Member = "foreign"
	_, err = slave.meshExecute(meshRequest{Protocol: mesh.Protocol, Cluster: topology.ID, Member: "relay", Transaction: "tx-pinned", Operation: "prepare", Plan: &plan})
	if err == nil {
		t.Fatal("RPC replaced locally approved management identity")
	}
	dir = t.TempDir()
	existing := filepath.Join(dir, "config.base.json")
	if err := os.WriteFile(existing, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	slave = &app{statePath: filepath.Join(dir, "state.db"), out: io.Discard, err: io.Discard}
	if err := slave.meshCmd([]string{"join", "--file", joinFile}); err == nil {
		t.Fatal("join overwrote a base configuration")
	}
	raw, _ := os.ReadFile(existing)
	if string(raw) != "preserve" {
		t.Fatal("existing base was not preserved")
	}
}

func TestMeshRoutesBecomeNodeTemplatesOnlyAfterApplyAndKeepMarks(t *testing.T) {
	topology := meshFixture(t)
	a := meshFixtureApp(t, topology, "master")
	s, _ := loadState(a.statePath)
	for _, template := range nodeTemplates(s) {
		if strings.HasPrefix(template.Outbound, "sbmgr-mesh-") {
			t.Fatal("unapplied route became assignable")
		}
	}
	plan, _ := topology.Compile("master")
	s.MeshAgent.Active = &plan
	template, ok := findNodeTemplate(s, "主从 · chain")
	if !ok || template.Outbound != mesh.RouteTag("chain") {
		t.Fatal("applied route missing from user templates")
	}
	s.Users = qrTUITestState().Users
	s.Users[0].Nodes[0].UUID = newUUID()
	s.Users[0].Nodes[0].AuthUser = "alice-phone"
	s.Users[0].Nodes[0].Outbound = template.Outbound
	raw, err := renderConfig(s)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range cfg["outbounds"].([]any) {
		out := item.(map[string]any)
		if strings.HasPrefix(stringValue(out["tag"]), "sbmgr-rate-") && out["type"] == "socks" && out["routing_mark"] != nil {
			found = true
		}
	}
	if !found {
		t.Fatal("mesh route lost per-user routing mark or chosen protocol")
	}
	if err := saveState(a.statePath, s); err != nil {
		t.Fatal(err)
	}
	if err := a.meshCmd([]string{"remove-route", "--id", "chain"}); err == nil {
		t.Fatal("referenced route removed")
	}
}

func TestMeshCoordinatorFinalDecisionSurvivesLostFinalizeResponse(t *testing.T) {
	installMeshTestRuntime(t)
	topology := meshFixture(t)
	apps := map[string]*app{}
	for _, member := range topology.Members {
		apps[member.ID] = meshFixtureApp(t, topology, member.ID)
	}
	oldExchange := meshExchange
	t.Cleanup(func() { meshExchange = oldExchange })
	loseResponse := true
	meshExchange = func(_ *app, m mesh.Member, r meshRequest, _ bool) (meshResponse, error) {
		response, err := apps[m.ID].meshExecute(r)
		if r.Operation == "finalize" && m.ID == "relay" && loseResponse {
			return meshResponse{}, errors.New("test finalize response lost")
		}
		return response, err
	}
	if err := apps["master"].meshCoordinate("apply"); err == nil {
		t.Fatal("lost acknowledgement reported as final success")
	}
	s, _ := loadState(apps["master"].statePath)
	if s.MeshRollout == nil || s.MeshRollout.Decision != "finalize" {
		t.Fatal("durable commit decision missing")
	}
	loseResponse = false
	if err := apps["master"].meshCoordinate("recover"); err != nil {
		t.Fatal(err)
	}
	for _, a := range apps {
		s, _ := loadState(a.statePath)
		if s.MeshAgent.Active == nil || s.MeshAgent.Active.Revision != 1 {
			t.Fatal("recovery undid committed topology")
		}
	}
}

func TestMeshFailedApplyRetainsJournalAndRestoresPreviousRevision(t *testing.T) {
	installMeshTestRuntime(t)
	topology := meshFixture(t)
	a := meshFixtureApp(t, topology, "relay")
	first, _ := topology.Compile("relay")
	s, _ := loadState(a.statePath)
	s.MeshAgent.Active = &first
	if err := saveState(a.statePath, s); err != nil {
		t.Fatal(err)
	}
	topology.Revision++
	topology.Routes[0].Hops = []string{"exit", "relay"}
	next, _ := topology.Compile("relay")
	r := meshRequest{Protocol: mesh.Protocol, Cluster: topology.ID, Member: "relay", Transaction: "tx-failure", Operation: "prepare", Plan: &next}
	if _, err := a.meshExecute(r); err != nil {
		t.Fatal(err)
	}
	apply := meshApplyCandidate
	meshApplyCandidate = func(_ *app, _ *State, _, _ *mesh.Plan) error { return errors.New("test apply interrupted") }
	r.Operation, r.Plan = "commit", nil
	if _, err := a.meshExecute(r); err == nil {
		t.Fatal("interrupted apply succeeded")
	}
	s, _ = loadState(a.statePath)
	if s.MeshAgent.Phase != "committing" || s.MeshAgent.Active.Revision != 1 || s.MeshAgent.Pending.Revision != 2 {
		t.Fatal("recovery journal lost previous or pending revision")
	}
	meshApplyCandidate = apply
	r.Operation = "rollback"
	if _, err := a.meshExecute(r); err != nil {
		t.Fatal(err)
	}
	s, _ = loadState(a.statePath)
	if s.MeshAgent.Active.Revision != 1 || s.MeshAgent.Transaction != "" {
		t.Fatal("previous revision was not restored")
	}
}
