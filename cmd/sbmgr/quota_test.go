package main

import (
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLegacyQuotaModeDefaultsToTotal(t *testing.T) {
	var u User
	if err := json.Unmarshal([]byte(`{"name":"legacy","quota_bytes":100,"upload_bytes":40,"download_bytes":60}`), &u); err != nil {
		t.Fatal(err)
	}
	if got := normalizedQuotaMode(u.QuotaMode); got != quotaModeTotal {
		t.Fatalf("legacy mode = %q, want %q", got, quotaModeTotal)
	}
	if got := measuredUsage(u); got != 100 || !overQuota(u) {
		t.Fatalf("legacy accounting changed: usage=%d overQuota=%v", got, overQuota(u))
	}

	s := &State{Users: []User{u}}
	normalizeQuotaModes(s)
	if s.Users[0].QuotaMode != quotaModeTotal {
		t.Fatalf("legacy state was not canonicalized: %#v", s.Users[0])
	}
}

func TestQuotaModesUseSelectedTrafficDirection(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		upload      int64
		download    int64
		wantUsage   int64
		wantPercent float64
		wantOver    bool
	}{
		{name: "total below", mode: quotaModeTotal, upload: 49, download: 50, wantUsage: 99, wantPercent: 99, wantOver: false},
		{name: "total threshold", mode: quotaModeTotal, upload: 40, download: 60, wantUsage: 100, wantPercent: 100, wantOver: true},
		{name: "upload ignores download", mode: quotaModeUpload, upload: 99, download: 500, wantUsage: 99, wantPercent: 99, wantOver: false},
		{name: "upload threshold", mode: quotaModeUpload, upload: 100, download: 0, wantUsage: 100, wantPercent: 100, wantOver: true},
		{name: "download ignores upload", mode: quotaModeDownload, upload: 500, download: 99, wantUsage: 99, wantPercent: 99, wantOver: false},
		{name: "download threshold", mode: quotaModeDownload, upload: 0, download: 100, wantUsage: 100, wantPercent: 100, wantOver: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			u := User{QuotaBytes: 100, QuotaMode: test.mode, Upload: test.upload, Download: test.download}
			if got := measuredUsage(u); got != test.wantUsage {
				t.Fatalf("measuredUsage = %d, want %d", got, test.wantUsage)
			}
			if got := usagePercent(u); math.Abs(got-test.wantPercent) > 0.0001 {
				t.Fatalf("usagePercent = %v, want %v", got, test.wantPercent)
			}
			if got := overQuota(u); got != test.wantOver {
				t.Fatalf("overQuota = %v, want %v", got, test.wantOver)
			}
		})
	}
}

func TestQuotaModeDrivesTieredThrottle(t *testing.T) {
	u := User{
		QuotaBytes: 100, QuotaMode: quotaModeDownload, Upload: 500, Download: 79,
		Throttle: ThrottlePolicy{Enabled: true},
	}
	if got := throttleStage(u); got != 0 {
		t.Fatalf("upload incorrectly triggered download-only tier: %d", got)
	}
	u.Download = 80
	if got := throttleStage(u); got != 1 {
		t.Fatalf("download first threshold stage = %d", got)
	}
	u.Download = 95
	if got := throttleStage(u); got != 2 {
		t.Fatalf("download second threshold stage = %d", got)
	}
}

func TestUserCLIAddsSetsAndDisplaysQuotaMode(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(statePath, &State{Version: stateVersion}); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	a := &app{statePath: statePath, out: &output, err: io.Discard}
	if err := a.userCmd([]string{"add", "alice", "--quota", "100", "--quota-mode", "upload"}); err != nil {
		t.Fatal(err)
	}
	s, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := findUser(s, "alice").QuotaMode; got != quotaModeUpload {
		t.Fatalf("add mode = %q", got)
	}
	if err := a.userCmd([]string{"set", "alice", "--quota-mode", "DOWNLOAD"}); err != nil {
		t.Fatal(err)
	}
	s, err = loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := findUser(s, "alice").QuotaMode; got != quotaModeDownload {
		t.Fatalf("set mode = %q", got)
	}
	output.Reset()
	if err := a.userCmd([]string{"list"}); err != nil {
		t.Fatal(err)
	}
	if text := output.String(); !strings.Contains(text, "METERING") || !strings.Contains(text, "download") {
		t.Fatalf("quota mode missing from list output: %q", text)
	}
	if err := a.userCmd([]string{"set", "alice", "--quota-mode", "invalid"}); err == nil {
		t.Fatal("invalid quota mode was accepted")
	}
}

func TestBatchUserSettingsSupportsQuotaMode(t *testing.T) {
	s := batchTestState()
	mode := quotaModeDownload
	op := batchOperation{
		Kind:  batchUserSettings,
		Users: []string{"alice", "bob"},
		User:  batchUserSettingsPatch{QuotaMode: &mode},
	}
	updated, result, err := applyBatchOperation(s, op, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Users != 2 || findUser(updated, "alice").QuotaMode != mode || findUser(updated, "bob").QuotaMode != mode {
		t.Fatalf("batch quota mode not applied: result=%#v users=%#v", result, updated.Users)
	}

	invalid := "invalid"
	bad := batchOperation{Kind: batchUserSettings, Users: []string{"alice", "bob"}, User: batchUserSettingsPatch{QuotaMode: &invalid}}
	if _, _, err := applyBatchOperation(s, bad, time.Now()); err == nil {
		t.Fatal("invalid batch quota mode was accepted")
	}
	if findUser(s, "alice").QuotaMode != "" || findUser(s, "bob").QuotaMode != "" {
		t.Fatal("failed batch operation mutated source state")
	}
}

func TestLoadStateCanonicalizesMissingQuotaMode(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	s := batchTestState()
	for index := range s.Users {
		s.Users[index].QuotaMode = ""
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range loaded.Users {
		if u.QuotaMode != quotaModeTotal {
			t.Fatalf("loaded mode for %s = %q", u.Name, u.QuotaMode)
		}
	}
}
