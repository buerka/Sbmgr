package main

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestFleetValidationRejectsUnsafeRemoteCommands(t *testing.T) {
	valid := FleetServer{Name: "HK-01", Host: "203.0.113.10", Port: 22, User: "root", KeyPath: t.TempDir() + "/id.pem", AppDir: "/srv/sbmgr", Enabled: true}
	s := &State{Fleet: []FleetServer{valid}}
	if err := validateFleet(s); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*FleetServer){
		func(server *FleetServer) { server.Name = "bad name" },
		func(server *FleetServer) { server.Host = "host;reboot" },
		func(server *FleetServer) { server.User = "root$(id)" },
		func(server *FleetServer) { server.User = "-V" },
		func(server *FleetServer) { server.User = ".operator" },
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

func TestFleetValidationAllowsRemoteHomeDefault(t *testing.T) {
	server := FleetServer{Name: "HK-01", Host: "203.0.113.10", Port: 22, User: "operator", KeyPath: t.TempDir() + "/id.pem", Enabled: true}
	if err := validateFleet(&State{Fleet: []FleetServer{server}}); err != nil {
		t.Fatalf("remote-home default rejected: %v", err)
	}
	if got := fleetSnapshotCommand(server); got != `SBMGR_HOME="$HOME/sbmgr" "$HOME/sbmgr/sbmgr" admin snapshot` {
		t.Fatalf("remote-home command = %q", got)
	}
	server.AppDir = "/srv/sbmgr"
	if got := fleetSnapshotCommand(server); got != "SBMGR_HOME=/srv/sbmgr /srv/sbmgr/sbmgr admin snapshot" {
		t.Fatalf("explicit-directory command = %q", got)
	}
	server.AppDir = "/srv/sbmgr/"
	if got := fleetSnapshotCommand(server); got != "SBMGR_HOME=/srv/sbmgr /srv/sbmgr/sbmgr admin snapshot" {
		t.Fatalf("trailing-slash directory command = %q", got)
	}
}

func TestFleetValidationDoesNotCollapseRootDirectoryToHomeDefault(t *testing.T) {
	server := FleetServer{Name: "HK-01", Host: "203.0.113.10", Port: 22, User: "operator", KeyPath: t.TempDir() + "/id.pem", AppDir: "/", Enabled: true}
	if got := normalizedFleetServer(server).AppDir; got != "/" {
		t.Fatalf("root application directory collapsed to %q", got)
	}
	if err := validateFleet(&State{Fleet: []FleetServer{server}}); err == nil {
		t.Fatal("root application directory was accepted")
	}
	server.AppDir = "/srv/sbmgr..prod"
	if err := validateFleet(&State{Fleet: []FleetServer{server}}); err != nil {
		t.Fatalf("safe dotted directory rejected: %v", err)
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

func TestFleetSSHArgumentsDisableInteractiveAndForwardingFeatures(t *testing.T) {
	server := normalizedFleetServer(FleetServer{Host: "203.0.113.10", Port: 2202, User: "operator", KeyPath: "/keys/id.pem"})
	command := `SBMGR_HOME="$HOME/sbmgr" "$HOME/sbmgr/sbmgr" admin snapshot`
	args := fleetSSHArgs(server, command)
	options := map[string]bool{}
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "-o" {
			options[args[index+1]] = true
		}
	}
	for _, required := range []string{
		"BatchMode=yes",
		"ConnectTimeout=5",
		"StrictHostKeyChecking=yes",
		"IdentitiesOnly=yes",
		"PasswordAuthentication=no",
		"KbdInteractiveAuthentication=no",
		"ClearAllForwardings=yes",
		"RequestTTY=no",
	} {
		if !options[required] {
			t.Errorf("SSH option %q missing from %#v", required, args)
		}
	}
	if got := args[len(args)-2]; got != "operator@203.0.113.10" {
		t.Errorf("destination = %q", got)
	}
	if got := args[len(args)-1]; got != command {
		t.Errorf("remote command = %q", got)
	}
}

func validFleetSnapshot() FleetSnapshot {
	return FleetSnapshot{
		Hostname:        "edge-01",
		Version:         "v0.23.0",
		Users:           2,
		EnabledUsers:    1,
		Devices:         3,
		UploadBytes:     100,
		DownloadBytes:   200,
		UnreadAlerts:    4,
		UnhealthyRoutes: 1,
	}
}

func TestFleetSnapshotValidationRejectsNegativeOrInconsistentCounts(t *testing.T) {
	mutations := []func(*FleetSnapshot){
		func(snapshot *FleetSnapshot) { snapshot.Users = -1 },
		func(snapshot *FleetSnapshot) { snapshot.EnabledUsers = -1 },
		func(snapshot *FleetSnapshot) { snapshot.Devices = -1 },
		func(snapshot *FleetSnapshot) { snapshot.UploadBytes = -1 },
		func(snapshot *FleetSnapshot) { snapshot.DownloadBytes = -1 },
		func(snapshot *FleetSnapshot) { snapshot.UnreadAlerts = -1 },
		func(snapshot *FleetSnapshot) { snapshot.UnhealthyRoutes = -1 },
		func(snapshot *FleetSnapshot) { snapshot.EnabledUsers = snapshot.Users + 1 },
		func(snapshot *FleetSnapshot) { snapshot.UploadBytes, snapshot.DownloadBytes = math.MaxInt64, 1 },
	}
	for index, mutate := range mutations {
		snapshot := validFleetSnapshot()
		mutate(&snapshot)
		if err := validateFleetSnapshot(snapshot); err == nil {
			t.Errorf("mutation %d accepted: %#v", index, snapshot)
		}
	}
}

func TestDecodeFleetSnapshotRejectsOversizedOrUnsafeFields(t *testing.T) {
	snapshot := validFleetSnapshot()
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeFleetSnapshot(data)
	if err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
	if decoded != snapshot {
		t.Fatalf("decoded snapshot = %#v", decoded)
	}

	snapshot.Hostname = strings.Repeat("h", fleetMaxHostnameSize+1)
	data, _ = json.Marshal(snapshot)
	if _, err := decodeFleetSnapshot(data); err == nil {
		t.Fatal("oversized hostname accepted")
	}

	snapshot = validFleetSnapshot()
	snapshot.Version = "v1\x1b[31m"
	data, _ = json.Marshal(snapshot)
	if _, err := decodeFleetSnapshot(data); err == nil {
		t.Fatal("escaped terminal control accepted in snapshot field")
	}

	data, _ = json.Marshal(validFleetSnapshot())
	unsafeWire := append([]byte("\x1b[31m"), data...)
	if _, err := decodeFleetSnapshot(unsafeWire); err == nil {
		t.Fatal("raw ANSI control accepted")
	}
	if _, err := decodeFleetSnapshot(append(data, []byte(` {}`)...)); err == nil {
		t.Fatal("second JSON value accepted")
	}
	unknown := append(data[:len(data)-1], []byte(`,"unexpected":true}`)...)
	if _, err := decodeFleetSnapshot(unknown); err == nil {
		t.Fatal("unknown snapshot field accepted")
	}
}

func TestFleetRemoteOutputIsStrictlyBoundedAndSanitized(t *testing.T) {
	stdout := newFleetLimitedBuffer(8)
	if written, err := stdout.Write([]byte("0123456789")); err != nil || written != 10 {
		t.Fatalf("bounded write = %d, %v", written, err)
	}
	if !stdout.overflow || string(stdout.Bytes()) != "01234567" {
		t.Fatalf("bounded buffer = %q overflow=%v", stdout.Bytes(), stdout.overflow)
	}

	diagnostic := "\x1b[31mfailed\x1b[0m\n\x1b]8;;https://example.invalid\aCLICK\x1b]8;;\x1b\\\n\x00done\u202e"
	if got := sanitizeFleetDiagnostic(diagnostic); got != "failed CLICK done" {
		t.Fatalf("sanitized diagnostic = %q", got)
	}
	long := sanitizeFleetDiagnostic(strings.Repeat("界", fleetMaxErrorBytes))
	if len(long) > fleetMaxErrorBytes || !utf8.ValidString(long) {
		t.Fatalf("diagnostic limit invalid: bytes=%d valid=%v", len(long), utf8.ValidString(long))
	}
}
