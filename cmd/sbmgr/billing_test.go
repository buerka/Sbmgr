package main

import (
	"io"
	"path/filepath"
	"testing"
	"time"
)

func TestBillingCycleArchivesAndResetsQuotaDisabledUser(t *testing.T) {
	now := time.Date(2026, 8, 3, 0, 0, 1, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	s := &State{Users: []User{{
		Name: "alice", Enabled: false, DisabledReason: "quota", QuotaBytes: 100, ExtraQuotaBytes: 20, Upload: 80, Download: 40,
		Billing: BillingPolicy{Enabled: true, CycleDay: 3, TimeZone: "Asia/Shanghai", LastReset: "2026-07-03T00:00:00+08:00", NextReset: "2026-08-03T00:00:00+08:00"},
		Nodes:   []Node{{Name: "Relay A", Upload: 80, Download: 40, CurrentUploadMbps: 2}}, UsageHistory: []UsagePoint{{At: "2026-08-02T23:55:00+08:00"}}, QuotaAlertStage: 2,
	}}}
	if !evaluateBillingCycles(s, now) {
		t.Fatal("due billing cycle was not evaluated")
	}
	u := s.Users[0]
	if !u.Enabled || u.DisabledReason != "" || u.Upload != 0 || u.Download != 0 || u.Nodes[0].Upload != 0 || len(u.UsageHistory) != 0 {
		t.Fatalf("usage was not safely reset: %#v", u)
	}
	if len(u.BillingHistory) != 1 || u.BillingHistory[0].UploadBytes != 80 || u.BillingHistory[0].DownloadBytes != 40 || u.BillingHistory[0].QuotaBytes != 120 {
		t.Fatalf("bad billing archive: %#v", u.BillingHistory)
	}
	if u.Billing.NextReset != "2026-09-03T00:00:00+08:00" || len(s.Alerts) != 1 || s.Alerts[0].Kind != "billing_reset" {
		t.Fatalf("next reset or alert missing: billing=%#v alerts=%#v", u.Billing, s.Alerts)
	}
}

func TestBillingInitializationDoesNotEraseExistingUsage(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	s := &State{Users: []User{{Name: "alice", Upload: 42, Billing: BillingPolicy{Enabled: true, CycleDay: 5}}}}
	if !evaluateBillingCycles(s, now) {
		t.Fatal("billing schedule was not initialized")
	}
	if s.Users[0].Upload != 42 || s.Users[0].Billing.NextReset == "" || len(s.Users[0].BillingHistory) != 0 {
		t.Fatalf("initialization unexpectedly reset usage: %#v", s.Users[0])
	}
	if s.Users[0].Billing.LastReset != "2026-08-03T20:00:00+08:00" {
		t.Fatalf("billing initialization did not use policy timezone: %s", s.Users[0].Billing.LastReset)
	}
}

func TestManualMonthlyTrafficResetArchivesUsageAndPreservesPlan(t *testing.T) {
	now := time.Date(2026, 8, 10, 20, 30, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	nextReset := "2026-09-03T00:00:00+08:00"
	s := &State{Users: []User{{
		Name: "alice", Enabled: false, DisabledReason: "quota",
		QuotaBytes: 100, ExtraQuotaBytes: 20, Expires: "2026-12-31",
		Upload: 80, Download: 40, CurrentUploadMbps: 3, CurrentDownloadMbps: 4,
		Billing:        BillingPolicy{Enabled: true, CycleDay: 3, TimeZone: "Asia/Shanghai", LastReset: "2026-08-03T00:00:00+08:00", NextReset: nextReset},
		TrafficSamples: []TrafficSample{{At: now.Add(-time.Minute).Format(time.RFC3339), Bytes: 120}},
		UsageHistory:   []UsagePoint{{At: now.Add(-time.Minute).Format(time.RFC3339), UploadBytes: 80, DownloadBytes: 40}},
		BlockedUntil:   now.Add(time.Hour).Format(time.RFC3339), BlockReason: "test", QuotaAlertStage: 2,
		Nodes: []Node{{Name: "Node A", Upload: 80, Download: 40, CurrentUploadMbps: 3, CurrentDownloadMbps: 4}},
	}}}

	manualResetUserMonthlyTraffic(s, &s.Users[0], now)
	u := s.Users[0]
	if !u.Enabled || u.DisabledReason != "" || u.Upload != 0 || u.Download != 0 || u.CurrentUploadMbps != 0 || u.CurrentDownloadMbps != 0 {
		t.Fatalf("manual reset did not clear usage or restore quota-disabled user: %#v", u)
	}
	if len(u.TrafficSamples) != 0 || len(u.UsageHistory) != 0 || u.BlockedUntil != "" || u.BlockReason != "" || u.QuotaAlertStage != 0 {
		t.Fatalf("manual reset left derived traffic state behind: %#v", u)
	}
	if u.Nodes[0].Upload != 0 || u.Nodes[0].Download != 0 || u.Nodes[0].CurrentUploadMbps != 0 || u.Nodes[0].CurrentDownloadMbps != 0 {
		t.Fatalf("manual reset did not clear node traffic: %#v", u.Nodes[0])
	}
	if u.QuotaBytes != 100 || u.ExtraQuotaBytes != 20 || u.Expires != "2026-12-31" || u.Billing.CycleDay != 3 || u.Billing.NextReset != nextReset {
		t.Fatalf("manual reset changed the user's plan: %#v", u)
	}
	if len(u.BillingHistory) != 1 || u.BillingHistory[0].UploadBytes != 80 || u.BillingHistory[0].DownloadBytes != 40 || u.BillingHistory[0].QuotaBytes != 120 {
		t.Fatalf("manual reset did not archive the partial billing period: %#v", u.BillingHistory)
	}
	if u.Billing.LastReset != now.Format(time.RFC3339) {
		t.Fatalf("manual reset start time = %q, want %q", u.Billing.LastReset, now.Format(time.RFC3339))
	}
	if len(s.Alerts) != 1 || s.Alerts[0].Kind != "traffic_reset_manual" {
		t.Fatalf("manual reset alert missing: %#v", s.Alerts)
	}
}

func TestLifecycleWarningsAreMilestonesNotSpam(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	s := &State{Users: []User{{Name: "alice", QuotaBytes: 100, Upload: 96, Expires: "2026-08-05"}}}
	if !evaluateLifecycleAlerts(s, now) {
		t.Fatal("lifecycle warnings were not created")
	}
	if len(s.Alerts) != 2 || s.Users[0].QuotaAlertStage != 2 || s.Users[0].ExpiryAlertStage == 0 {
		t.Fatalf("missing quota/expiry warnings: %#v %#v", s.Users[0], s.Alerts)
	}
	if evaluateLifecycleAlerts(s, now.Add(time.Hour)) || len(s.Alerts) != 2 {
		t.Fatal("same lifecycle milestone generated duplicate alerts")
	}
}

func TestExpiryUsesApplicationTimeZone(t *testing.T) {
	u := User{Expires: "2026-08-03"}
	if expired(u, time.Date(2026, 8, 3, 15, 59, 59, 0, time.UTC)) {
		t.Fatal("user expired before the end of the Shanghai expiry date")
	}
	if !expired(u, time.Date(2026, 8, 3, 16, 0, 0, 0, time.UTC)) {
		t.Fatal("user did not expire at the start of the next Shanghai date")
	}
}

func TestStateRejectsQuotaOverflow(t *testing.T) {
	maximum := int64(^uint64(0) >> 1)
	s := &State{Users: []User{{Name: "alice", QuotaBytes: maximum, ExtraQuotaBytes: 1}}}
	if err := validateState(s); err == nil {
		t.Fatal("overflowing base and extra quota was accepted")
	}
}

func TestUserSetInitializesBillingInPolicyTimeZone(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(statePath, &State{Users: []User{{Name: "alice", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.userCmd([]string{"set", "alice", "--billing-enabled", "true", "--billing-day", "3"}); err != nil {
		t.Fatal(err)
	}
	s, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	stamp, err := time.Parse(time.RFC3339, s.Users[0].Billing.LastReset)
	if err != nil {
		t.Fatal(err)
	}
	_, offset := stamp.Zone()
	if offset != 8*60*60 {
		t.Fatalf("billing was initialized with offset %d instead of Asia/Shanghai", offset)
	}
}
