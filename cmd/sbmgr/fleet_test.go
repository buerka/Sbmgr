package main

import (
	"strings"
	"testing"
	"time"
)

func TestFleetValidationRejectsUnsafeRemoteCommands(t *testing.T) {
	valid := FleetServer{Name: "HK-01", Host: "203.0.113.10", Port: 22, User: "root", KeyPath: t.TempDir() + "/id.pem", AppDir: "/root/sbmgr", Enabled: true}
	s := &State{Fleet: []FleetServer{valid}}
	if err := validateFleet(s); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*FleetServer){
		func(server *FleetServer) { server.Name = "bad name" },
		func(server *FleetServer) { server.Host = "host;reboot" },
		func(server *FleetServer) { server.User = "root$(id)" },
		func(server *FleetServer) { server.AppDir = "/root/../etc" },
		func(server *FleetServer) { server.KeyPath = "relative.pem" },
	} {
		candidate := valid
		mutate(&candidate)
		if err := validateFleet(&State{Fleet: []FleetServer{candidate}}); err == nil {
			t.Fatalf("unsafe fleet profile accepted: %#v", candidate)
		}
	}
}

func TestFleetSnapshotAggregatesWithoutCredentials(t *testing.T) {
	s := &State{Users: []User{
		{Name: "alice", Enabled: true, Upload: 10, Download: 20, Devices: []Device{{Name: "phone"}}},
		{Name: "bob", Enabled: false, Upload: 30, Download: 40, Devices: []Device{{Name: "pc"}, {Name: "tablet"}}},
	}, Alerts: []Alert{{Acknowledged: false}}, OutboundHealth: map[string]OutboundHealth{"bad": {CheckedAt: time.Now().Format(time.RFC3339), Healthy: false}}}
	snapshot := localFleetSnapshot(s)
	if snapshot.Users != 2 || snapshot.EnabledUsers != 1 || snapshot.Devices != 3 || snapshot.UploadBytes != 40 || snapshot.DownloadBytes != 60 || snapshot.UnreadAlerts != 1 || snapshot.UnhealthyRoutes != 1 {
		t.Fatalf("bad fleet snapshot: %#v", snapshot)
	}
	data, _ := strings.CutPrefix(snapshot.Version, "v")
	if data == "" {
		t.Fatal("snapshot version missing")
	}
}

func TestFleetCheckScheduleUsesFiveMinuteWindow(t *testing.T) {
	now := time.Now()
	s := &State{Fleet: []FleetServer{{Name: "remote", Enabled: true}}, FleetStatus: map[string]FleetServerStatus{"remote": {CheckedAt: now.Format(time.RFC3339)}}}
	if fleetCheckDue(s, now.Add(time.Minute)) {
		t.Fatal("fleet check scheduled too frequently")
	}
	if !fleetCheckDue(s, now.Add(6*time.Minute)) {
		t.Fatal("fleet check was not due")
	}
}
