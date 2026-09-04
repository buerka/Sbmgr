package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
)

type staticStatsService interface {
	QueryStats(context.Context, *queryStatsRequest) (*queryStatsResponse, error)
}

type staticStatsServer struct {
	stats []*stat
}

func (s staticStatsServer) QueryStats(_ context.Context, _ *queryStatsRequest) (*queryStatsResponse, error) {
	return &queryStatsResponse{Stat: s.stats}, nil
}

func startStaticStatsServer(t *testing.T, stats ...*stat) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: "v2ray.core.app.stats.command.StatsService",
		HandlerType: (*staticStatsService)(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: "QueryStats",
			Handler: func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
				req := new(queryStatsRequest)
				if err := dec(req); err != nil {
					return nil, err
				}
				return srv.(staticStatsService).QueryStats(ctx, req)
			},
		}},
	}, staticStatsServer{stats: stats})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	return listener.Addr().String()
}

func statsTransactionState(api string, quota int64) *State {
	return &State{
		Version:  stateVersion,
		StatsAPI: api,
		Users: []User{{
			Name:       "alice",
			Enabled:    true,
			QuotaBytes: quota,
			Nodes: []Node{{
				Name:     "Relay A",
				AuthUser: "alice:relay-a",
				UUID:     "11111111-1111-4111-8111-111111111111",
			}},
		}},
	}
}

func TestMergeUserStatsUsesCallerStateAndCounterBaseline(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	s := statsTransactionState("", 1_000)
	s.Counters = map[string]int64{"user>>>alice:relay-a>>>traffic>>>uplink": 30}
	item := &stat{Name: "user>>>alice:relay-a>>>traffic>>>uplink", Value: 42}

	result := mergeUserStats(s, []*stat{item}, now, 5*time.Second)
	if result.Added != 12 || !result.Changed || result.EligibilityChanged {
		t.Fatalf("first merge result = %#v", result)
	}
	if got := s.Users[0].Upload; got != 12 {
		t.Fatalf("caller state upload = %d, want 12", got)
	}
	if got := s.Users[0].Nodes[0].Upload; got != 12 {
		t.Fatalf("caller node upload = %d, want 12", got)
	}

	result = mergeUserStats(s, []*stat{item}, now.Add(5*time.Second), 5*time.Second)
	if result.Added != 0 {
		t.Fatalf("unchanged counter was charged twice: %#v", result)
	}
	if got := s.Users[0].Upload; got != 12 {
		t.Fatalf("unchanged counter changed total to %d", got)
	}
}

func TestMergeUserStatsReportsOnlyEligibilityTransition(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	s := statsTransactionState("", 100)
	s.Users[0].Upload = 90
	s.Users[0].Nodes[0].Upload = 90
	s.Counters = map[string]int64{"user>>>alice:relay-a>>>traffic>>>uplink": 90}

	result := mergeUserStats(s, []*stat{{Name: "user>>>alice:relay-a>>>traffic>>>uplink", Value: 100}}, now, time.Minute)
	if result.Added != 10 || !result.Changed || !result.EligibilityChanged {
		t.Fatalf("quota transition result = %#v", result)
	}
	if s.Users[0].Enabled || s.Users[0].DisabledReason == "" {
		t.Fatalf("quota transition did not disable caller state: %#v", s.Users[0])
	}
	if len(result.DisabledUsers) != 1 || result.DisabledUsers[0] != "alice" {
		t.Fatalf("disabled users = %#v", result.DisabledUsers)
	}
}

func TestSyncApplyDoesNotReloadForOrdinaryStats(t *testing.T) {
	api := startStaticStatsServer(t, &stat{Name: "user>>>alice:relay-a>>>traffic>>>uplink", Value: 42})
	statePath := filepath.Join(t.TempDir(), "state.json")
	// BaseConfig and ConfigPath are intentionally empty. Any accidental apply
	// for this ordinary traffic sample would fail while rendering the config.
	if err := saveState(statePath, statsTransactionState(api, 1_000)); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.syncCmd([]string{"--apply"}); err != nil {
		t.Fatalf("ordinary stats unexpectedly entered apply path: %v", err)
	}
	s, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if s.Users[0].Upload != 42 || s.StatsApplyPending || s.RateApplyPending {
		t.Fatalf("ordinary stats state = %#v", s)
	}
}

func TestSyncApplyFailureKeepsEligibilityPendingAndUsage(t *testing.T) {
	api := startStaticStatsServer(t, &stat{Name: "user>>>alice:relay-a>>>traffic>>>uplink", Value: 42})
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(statePath, statsTransactionState(api, 40)); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.syncCmd([]string{"--apply"}); err == nil {
		t.Fatal("eligibility apply unexpectedly succeeded without a base config")
	}
	s, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if s.Users[0].Upload != 42 || s.Users[0].Enabled {
		t.Fatalf("failed apply lost synchronized eligibility state: %#v", s.Users[0])
	}
	if !s.StatsApplyPending {
		t.Fatal("failed eligibility apply lost its durable retry flag")
	}
}

func TestRealtimeCycleStatsAPIPersistsWithoutDoubleChargingOrOrdinaryApply(t *testing.T) {
	api := startStaticStatsServer(t, &stat{Name: "user>>>alice:relay-a>>>traffic>>>uplink", Value: 42})
	statePath := filepath.Join(t.TempDir(), "state.json")
	// An empty base config makes any accidental full apply fail. Ordinary
	// five-second counter samples must only persist their new baseline.
	if err := saveState(statePath, statsTransactionState(api, 1_000)); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard, lastStatsSample: time.Now().Add(-5 * time.Second)}
	if err := a.realtimeCycle(); err != nil {
		t.Fatalf("ordinary realtime stats unexpectedly entered apply path: %v", err)
	}
	first, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if first.Users[0].Upload != 42 || first.StatsApplyPending || first.RateApplyPending {
		t.Fatalf("first realtime stats state = %#v", first)
	}
	if err := a.realtimeCycle(); err != nil {
		t.Fatal(err)
	}
	second, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if second.Users[0].Upload != 42 || second.Users[0].Nodes[0].Upload != 42 {
		t.Fatalf("unchanged API counter was charged twice: %#v", second.Users[0])
	}
}

func TestRealtimeStatsApplyFailureKeepsEligibilityPendingAndCounterBaseline(t *testing.T) {
	api := startStaticStatsServer(t, &stat{Name: "user>>>alice:relay-a>>>traffic>>>uplink", Value: 42})
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(statePath, statsTransactionState(api, 40)); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.realtimeCycle(); err == nil {
		t.Fatal("eligibility apply unexpectedly succeeded without a base config")
	}
	afterFailure, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if afterFailure.Users[0].Enabled || afterFailure.Users[0].Upload != 42 || !afterFailure.StatsApplyPending {
		t.Fatalf("failed realtime apply lost durable state: %#v", afterFailure)
	}
	// The next five-second query sees the persisted API baseline. It must not
	// charge 42 bytes again or perform another full-apply storm.
	if err := a.realtimeCycle(); err != nil {
		t.Fatalf("unchanged realtime sample should wait for normal retry: %v", err)
	}
	afterRetryWindow, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if afterRetryWindow.Users[0].Upload != 42 || !afterRetryWindow.StatsApplyPending {
		t.Fatalf("retry window changed durable accounting: %#v", afterRetryWindow)
	}
}

func TestEnforceApplyFailureKeepsAndRetriesDurableEligibilityPending(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	s := statsTransactionState("", 40)
	s.Users[0].Upload = 42
	s.Users[0].Nodes[0].Upload = 42
	if err := saveState(statePath, s); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	if err := a.enforceCmd([]string{"--apply"}); err == nil {
		t.Fatal("quota apply unexpectedly succeeded without a base config")
	}
	first, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if first.Users[0].Enabled || first.Users[0].DisabledReason != "quota" || !first.StatsApplyPending {
		t.Fatalf("first failed enforcement was not durable: %#v", first)
	}
	// No eligibility transition occurs on this call. The persisted flag alone
	// must still take the apply path and remain set when that retry fails.
	if err := a.enforceCmd([]string{"--apply"}); err == nil {
		t.Fatal("pending eligibility retry did not attempt an apply")
	}
	second, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if second.Users[0].Upload != 42 || second.Users[0].Enabled || !second.StatsApplyPending {
		t.Fatalf("failed eligibility retry corrupted durable state: %#v", second)
	}
}

func TestDaemonSubscriptionStartupFailureIsNonFatal(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(statePath, &State{Subscription: SubscriptionSettings{Enabled: true, Listen: "0.0.0.0:18443"}}); err != nil {
		t.Fatal(err)
	}
	var errorOutput bytes.Buffer
	a := &app{statePath: statePath, out: io.Discard, err: &errorOutput}
	a.startDaemonSubscription(context.Background())
	if !bytes.Contains(errorOutput.Bytes(), []byte("订阅服务未启动")) {
		t.Fatalf("daemon did not report isolated subscription failure: %q", errorOutput.String())
	}
	// Maintenance remains callable after the auxiliary listener failed. A
	// canonical state load is a side-effect-free stand-in for the next cycle.
	if _, err := loadState(statePath); err != nil {
		t.Fatalf("subscription failure poisoned maintenance state: %v", err)
	}
}
