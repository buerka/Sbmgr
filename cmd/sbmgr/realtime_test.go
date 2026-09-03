package main

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestRecordRealtimeUsageCalculatesRatesAndHistory(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 1, 0, 0, time.UTC)
	s := &State{Users: []User{{Name: "alice", Nodes: []Node{{
		Name: "ATT", Device: "phone", AuthUser: "alice-phone", RateUpdatedAt: now.Add(-time.Minute).Format(time.RFC3339Nano),
	}}}}}
	changed := recordRealtimeUsage(s, map[string]trafficDelta{"alice-phone": {upload: 7_500_000, download: 15_000_000}}, now)
	if !changed {
		t.Fatal("rate update was not reported as a state change")
	}
	u := &s.Users[0]
	if math.Abs(u.CurrentUploadMbps-1) > 0.0001 || math.Abs(u.CurrentDownloadMbps-2) > 0.0001 {
		t.Fatalf("user rate = %.4f/%.4f", u.CurrentUploadMbps, u.CurrentDownloadMbps)
	}
	if len(u.UsageHistory) != 1 || u.UsageHistory[0].UploadBytes != 7_500_000 || u.UsageHistory[0].DownloadBytes != 15_000_000 {
		t.Fatalf("bad usage history: %#v", u.UsageHistory)
	}

	if !recordRealtimeUsage(s, map[string]trafficDelta{}, now.Add(time.Minute)) {
		t.Fatal("nonzero-to-zero rate transition was not reported as a change")
	}
	if u.CurrentUploadMbps != 0 || u.CurrentDownloadMbps != 0 || len(u.UsageHistory) != 1 {
		t.Fatalf("idle rate did not decay in the current five-minute bucket: %#v", u)
	}
	idleUpdatedAt := u.Nodes[0].RateUpdatedAt
	for minute := 2; minute < 5; minute++ {
		if recordRealtimeUsage(s, map[string]trafficDelta{}, now.Add(time.Duration(minute)*time.Minute)) {
			t.Fatalf("continuous zero sample at minute %d changed state", minute)
		}
		if u.Nodes[0].RateUpdatedAt != idleUpdatedAt {
			t.Fatalf("continuous zero sample moved rate timestamp: %s -> %s", idleUpdatedAt, u.Nodes[0].RateUpdatedAt)
		}
	}
	recordRealtimeUsage(s, map[string]trafficDelta{"alice-phone": {download: 30_000_000}}, now.Add(5*time.Minute))
	if len(u.UsageHistory) != 2 {
		t.Fatalf("continuous minute samples prevented a new five-minute bucket: %#v", u.UsageHistory)
	}
}

func TestRecordRealtimeUsageDoesNotCreateContinuousZeroHistory(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	s := &State{Users: []User{{
		Name: "alice",
		Nodes: []Node{{
			Name: "LAX", AuthUser: "alice:lax",
			RateUpdatedAt: now.Add(-time.Minute).Format(time.RFC3339Nano),
		}},
		UsageHistory: []UsagePoint{{At: now.Add(-time.Minute).Format(time.RFC3339)}},
	}}}
	beforeUpdatedAt := s.Users[0].Nodes[0].RateUpdatedAt
	if recordRealtimeUsage(s, map[string]trafficDelta{}, now) {
		t.Fatal("an already-idle sample was reported as a state change")
	}
	if got := s.Users[0].Nodes[0].RateUpdatedAt; got != beforeUpdatedAt {
		t.Fatalf("idle timestamp changed from %s to %s", beforeUpdatedAt, got)
	}
	if got := len(s.Users[0].UsageHistory); got != 1 {
		t.Fatalf("continuous zero created history, len=%d", got)
	}
}

func TestRecordRealtimeUsageUsesInMemorySampleWindowAfterIdle(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	fresh := &State{Users: []User{{Name: "fresh", Nodes: []Node{{Name: "LAX", AuthUser: "fresh:lax"}}}}}
	freshDelta := map[string]trafficDelta{"fresh:lax": {download: 6_250_000}}
	if !recordRealtimeUsageAt(fresh, freshDelta, now, 5*time.Second) {
		t.Fatal("first in-memory sample was not recorded")
	}
	if got := fresh.Users[0].Nodes[0].CurrentDownloadMbps; math.Abs(got-10) > 0.0001 {
		t.Fatalf("first sample rate = %.4f Mbps, want 10 Mbps", got)
	}

	s := &State{Users: []User{{Name: "alice", Nodes: []Node{{
		Name: "LAX", AuthUser: "alice:lax", RateUpdatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano),
	}}}}}
	delta := map[string]trafficDelta{"alice:lax": {download: 6_250_000}}
	if !recordRealtimeUsageAt(s, delta, now, 5*time.Second) {
		t.Fatal("resumed traffic was not recorded")
	}
	if got := s.Users[0].Nodes[0].CurrentDownloadMbps; math.Abs(got-10) > 0.0001 {
		t.Fatalf("resumed rate = %.4f Mbps, want 10 Mbps from the five-second sample", got)
	}
	if !recordRealtimeUsageAt(s, map[string]trafficDelta{}, now.Add(5*time.Second), 5*time.Second) {
		t.Fatal("moving-to-idle transition was not persisted")
	}
	idleTimestamp := s.Users[0].Nodes[0].RateUpdatedAt
	if recordRealtimeUsageAt(s, map[string]trafficDelta{}, now.Add(time.Hour), 5*time.Second) {
		t.Fatal("continuous idle traffic caused another state write")
	}
	if s.Users[0].Nodes[0].RateUpdatedAt != idleTimestamp {
		t.Fatal("continuous idle traffic moved the persisted timestamp")
	}
	if !recordRealtimeUsageAt(s, delta, now.Add(time.Hour+5*time.Second), 5*time.Second) {
		t.Fatal("traffic after a long idle was not recorded")
	}
	if got := s.Users[0].Nodes[0].CurrentDownloadMbps; math.Abs(got-10) > 0.0001 {
		t.Fatalf("post-idle rate = %.4f Mbps, want 10 Mbps", got)
	}
}

func TestThrottleTransitionQueuesDurableRateApply(t *testing.T) {
	s := &State{Users: []User{{
		Name: "alice", QuotaBytes: 1000, Download: 799,
		Throttle: ThrottlePolicy{Enabled: true},
	}}}
	before := currentThrottleStages(s)
	s.Users[0].Download = 800
	if !queueThrottleStageApply(before, s) || !s.RateApplyPending {
		t.Fatalf("threshold transition did not queue rate apply: %#v", s)
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var restored State
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if !restored.RateApplyPending {
		t.Fatal("pending rate apply was not durable across state serialization")
	}
	// A failed immediate apply is retried by the minute cycle from this flag;
	// it must survive even though there is no second threshold transition.
	afterFailure := currentThrottleStages(&restored)
	if queueThrottleStageApply(afterFailure, &restored) {
		t.Fatal("unchanged stage was reported as a second transition")
	}
	if !restored.RateApplyPending {
		t.Fatal("unchanged stage cleared the failed apply retry flag")
	}
}

func TestDeviceCounterLabelsStayDistinct(t *testing.T) {
	if got := deviceNodeLabel("alice", defaultDeviceName, "ATT"); got != "alice/ATT" {
		t.Fatalf("legacy counter label changed: %q", got)
	}
	if got := deviceNodeLabel("alice", "phone", "ATT"); got != "alice/phone/ATT" {
		t.Fatalf("device counter label is not isolated: %q", got)
	}
}

func TestConnectionClosedLogDetection(t *testing.T) {
	closed := `INFO [1781284324 1m2s] inbound/vless[vless-in]: connection closed: EOF`
	if !connectionClosed(closed) {
		t.Fatal("closed connection was not detected")
	}
	if connectionClosed(`INFO [1781284324 1ms] inbound/vless[vless-in]: inbound connection to example.com:443`) {
		t.Fatal("active connection was mistaken for a close")
	}
	if !strings.Contains(usageSparkline(User{UsageHistory: []UsagePoint{{UploadMbps: 1}}}, 8), "峰值") {
		t.Fatal("history sparkline was not rendered")
	}
}
