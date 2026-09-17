package main

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sbmgr/internal/mesh"
	"strings"
	"testing"
)

func TestWebAccountChangesRevokeSessionsAndPreserveDeployment(t *testing.T) {
	for _, password := range []string{"", "new-fixture-password-only"} {
		t.Run(map[bool]string{true: "rename", false: "rename-and-password"}[password == ""], func(t *testing.T) {
			a, b := webFixture(t)
			c := b.config
			c.BasePath = "/private-entry"
			writeWebFixtureConfig(t, a, c)
			b.config = c
			login := webRequest{Method: "POST", Path: c.BasePath + "/api/login", Host: "127.0.0.1:9090", Origin: c.Origin, Remote: "127.0.0.1"}
			login.Body, _ = json.Marshal(map[string]string{"username": c.Username, "password": webTestPassword})
			response := b.lookup(context.Background(), login)
			var session map[string]string
			_ = json.Unmarshal(response.Body, &session)
			q := login
			q.Path = c.BasePath + "/api/account"
			q.Session, q.CSRF = response.SetSession, session["csrf"]
			q.Body, _ = json.Marshal(map[string]string{"username": "new-admin", "current_password": webTestPassword, "new_password": password})
			before, _ := loadState(a.statePath)
			for _, alter := range []func(*webRequest){func(q *webRequest) { q.CSRF = "" }, func(q *webRequest) { q.Origin = "https://other.example" }, func(q *webRequest) { q.Session = "" }} {
				bad := q
				alter(&bad)
				if r := b.lookup(context.Background(), bad); r.Status != 401 && r.Status != 403 {
					t.Fatal("account boundary was bypassed")
				}
			}
			r := b.lookup(context.Background(), q)
			if r.Status != 200 || !r.Logout {
				t.Fatalf("account change status %d", r.Status)
			}
			next, err := readWebConfig(a.statePath)
			if err != nil {
				t.Fatal(err)
			}
			if next.Username != "new-admin" || next.Listen != c.Listen || next.Origin != c.Origin || next.BasePath != c.BasePath || next.TLSCert != c.TLSCert || next.TLSKey != c.TLSKey {
				t.Fatal("account edit changed deployment settings")
			}
			if password == "" && (next.PasswordHash != c.PasswordHash || next.Salt != c.Salt) {
				t.Fatal("rename changed password")
			}
			q.Method, q.Path, q.Body = "GET", c.BasePath+"/api/session", nil
			if b.lookup(context.Background(), q).Status != 401 {
				t.Fatal("old session survived")
			}
			if password == "" {
				password = webTestPassword
			}
			login.Body, _ = json.Marshal(map[string]string{"username": "new-admin", "password": password})
			if b.lookup(context.Background(), login).Status != 200 {
				t.Fatal("new login failed")
			}
			after, _ := loadState(a.statePath)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("account edit changed business state")
			}
			audit, _ := os.ReadFile(auditPath(a.statePath))
			for _, secret := range []string{webTestPassword, password, next.Salt, next.PasswordHash} {
				if strings.Contains(string(audit), secret) {
					t.Fatal("credential leaked to audit")
				}
			}
			if !strings.Contains(string(audit), "web.account") {
				t.Fatal("missing account audit")
			}
		})
	}
}

func TestWebAccountFailureAndRateLimit(t *testing.T) {
	a, b := webFixture(t)
	q := loginWebFixture(t, b)
	c := b.config
	q.Path = "/api/account"
	for _, body := range []string{`{"username":"other","current_password":"x","new_password":"short"}`, `{"username":"other","current_password":"x","listen":"0.0.0.0:80"}`} {
		q.Body = []byte(body)
		if b.lookup(context.Background(), q).Status != 400 {
			t.Fatal("invalid account accepted")
		}
	}
	q.Body = []byte(`{"username":"other","current_password":"wrong","new_password":""}`)
	for i := 0; i < 5; i++ {
		if b.lookup(context.Background(), q).Status != 403 {
			t.Fatal("wrong password accepted")
		}
	}
	if b.lookup(context.Background(), q).Status != 429 {
		t.Fatal("password verification unbounded")
	}
	next, _ := readWebConfig(a.statePath)
	if next != c {
		t.Fatal("failed change modified config")
	}
}

func assignmentInput(t *testing.T, a *app, routes ...string) webActionInput {
	t.Helper()
	s, err := loadState(a.statePath)
	if err != nil {
		t.Fatal(err)
	}
	var selected []map[string]string
	for _, r := range routes {
		selected = append(selected, map[string]string{"outbound": r, "name": "Node A"})
	}
	raw, _ := json.Marshal(selected)
	return webActionInput{Action: "node.assign", Fields: map[string]string{"user": "alice", "device": "phone", "expected": webAssignmentVersion(&s.Users[0], "phone"), "selection": string(raw)}}
}

func TestWebAssignmentPreservesKeptIdentitiesUsageAndOtherDevices(t *testing.T) {
	a, b := webFixture(t)
	q := loginWebFixture(t, b)
	if err := a.deviceCmd([]string{"add", "alice", "--name=laptop", "--from=phone"}); err != nil {
		t.Fatal(err)
	}
	before, _ := loadState(a.statePath)
	input := assignmentInput(t, a, "direct", "to-relay-a")
	job := performWebAction(t, b, q, input)
	if job.Status != "success" {
		t.Fatal(job.Message)
	}
	after, _ := loadState(a.statePath)
	old := before.Users[0]
	next := after.Users[0]
	for _, n := range old.Nodes {
		found, err := findUserNode(&next, n.Device, n.Name)
		if err != nil || !reflect.DeepEqual(n, *found) {
			t.Fatal("kept node changed")
		}
	}
	if !reflect.DeepEqual(old.Devices, next.Devices) || old.Upload != next.Upload || old.Download != next.Download || old.QuotaBytes != next.QuotaBytes || len(next.Nodes) != len(old.Nodes)+1 {
		t.Fatal("grant changed unrelated state")
	}
	added := next.Nodes[len(next.Nodes)-1]
	if added.UUID == "" || added.UUID == old.Nodes[0].UUID || added.Name == old.Nodes[0].Name {
		t.Fatal("new node identity/name not unique")
	}
	// A stale form cannot revoke a concurrently created node.
	job = performWebAction(t, b, q, input)
	if job.Status != "failed" {
		t.Fatal("stale assignment accepted")
	}
	for _, routes := range [][]string{nil, {"direct", "missing"}, {"direct", "direct"}} {
		job = performWebAction(t, b, q, assignmentInput(t, a, routes...))
		if job.Status != "failed" {
			t.Fatal("invalid selection accepted")
		}
		state, _ := loadState(a.statePath)
		if !reflect.DeepEqual(state.Users, after.Users) {
			t.Fatal("failure partially changed users")
		}
	}
	job = performWebAction(t, b, q, assignmentInput(t, a, "to-relay-a"))
	if job.Status != "success" {
		t.Fatal(job.Message)
	}
	last, _ := loadState(a.statePath)
	if len(nodesForDevice(last.Users[0], "phone")) != 1 || len(nodesForDevice(last.Users[0], "laptop")) != 1 || last.Users[0].Upload != old.Upload {
		t.Fatal("revoke affected another device or user totals")
	}
	// The original link follows the new grants without changing device tokens.
	phone := findDevice(&last.Users[0], "phone")
	reply := a.lookupSubscription(context.Background(), subscriptionGET, phone.SubscriptionToken)
	if reply.Status != 200 || !strings.Contains(string(reply.Body), added.UUID) || strings.Contains(string(reply.Body), old.Nodes[0].UUID) {
		t.Fatalf("subscription did not follow atomic assignment, status=%d", reply.Status)
	}
	laptop := findDevice(&last.Users[0], "laptop")
	reply = a.lookupSubscription(context.Background(), subscriptionGET, laptop.SubscriptionToken)
	if reply.Status != 200 || strings.Contains(string(reply.Body), added.UUID) {
		t.Fatal("assignment leaked to another device subscription")
	}
}

func TestWebWireInventoryScopePendingAssignmentAndConflict(t *testing.T) {
	a, b := webFixture(t)
	q := loginWebFixture(t, b)
	if err := a.meshCmd([]string{"init", "--id=master", "--cluster=fixture"}); err != nil {
		t.Fatal(err)
	}
	q.Method, q.Path, q.Body = "GET", "/api/route-inventory/master", nil
	r := b.lookup(context.Background(), q)
	if r.Status != 200 || strings.Contains(string(r.Body), "uuid") || strings.Contains(string(r.Body), "password") {
		t.Fatal("invalid inventory projection")
	}
	input := webActionInput{Action: "mesh.wire", Fields: map[string]string{"id": "residential", "entry": "master", "exit": "missing-remote-exit", "revision": "1"}}
	job := performWebAction(t, b, q, input)
	if job.Status != "failed" {
		t.Fatal("foreign exit connected")
	}
	input.Fields["exit"] = "to-relay-a"
	job = performWebAction(t, b, q, input)
	if job.Status != "success" {
		t.Fatal(job.Message)
	}
	s, _ := loadState(a.statePath)
	if len(s.Mesh.Routes) != 1 || s.Mesh.Routes[0].Hops[0] != "master" {
		t.Fatal("wire created extra relay")
	}
	job = performWebAction(t, b, q, input)
	if job.Status != "failed" {
		t.Fatal("stale topology accepted")
	}
	input.Fields["revision"] = "2"
	job = performWebAction(t, b, q, input)
	if job.Status != "failed" {
		t.Fatal("new wire overwrote existing ID")
	}
	job = performWebAction(t, b, q, assignmentInput(t, a, "direct", mesh.RouteTag("residential")))
	if job.Status != "failed" {
		t.Fatal("unapplied route assigned")
	}
	plan, err := s.Mesh.Compile("master")
	if err != nil {
		t.Fatal(err)
	}
	s.MeshAgent.Active = &plan
	if err := saveState(a.statePath, s); err != nil {
		t.Fatal(err)
	}
	job = performWebAction(t, b, q, assignmentInput(t, a, "direct", mesh.RouteTag("residential")))
	if job.Status != "success" {
		t.Fatal(job.Message)
	}
}

func TestMeshInventoryIsReadOnlyAndDoesNotChangeStatusProtocol(t *testing.T) {
	a := meshFixtureApp(t, meshFixture(t), "relay")
	before, _ := loadState(a.statePath)
	request := meshRequest{Protocol: mesh.Protocol, Cluster: before.MeshAgent.Cluster, Member: before.MeshAgent.Member, Operation: "inventory"}
	r, err := a.meshExecute(request)
	if err != nil || len(r.Exits) == 0 {
		t.Fatal("inventory unavailable")
	}
	after, _ := loadState(a.statePath)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("inventory mutated state")
	}
	request.Operation = "status"
	r, err = a.meshExecute(request)
	if err != nil || r.Exits != nil {
		t.Fatal("status changed for older masters")
	}
}
