package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	oldproto "github.com/golang/protobuf/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const queryStatsMethod = "/v2ray.core.app.stats.command.StatsService/QueryStats"

// These small message definitions intentionally implement the legacy protobuf
// interface. gRPC adapts them to protobuf v2, avoiding a dependency on the
// complete V2Ray/Xray source tree for four fields.
type queryStatsRequest struct {
	Pattern string `protobuf:"bytes,1,opt,name=pattern,proto3" json:"pattern,omitempty"`
	Reset_  bool   `protobuf:"varint,2,opt,name=reset,proto3" json:"reset,omitempty"`
}

func (m *queryStatsRequest) Reset()         { *m = queryStatsRequest{} }
func (m *queryStatsRequest) String() string { return oldproto.CompactTextString(m) }
func (*queryStatsRequest) ProtoMessage()    {}

type queryStatsResponse struct {
	Stat []*stat `protobuf:"bytes,1,rep,name=stat,proto3" json:"stat,omitempty"`
}

func (m *queryStatsResponse) Reset()         { *m = queryStatsResponse{} }
func (m *queryStatsResponse) String() string { return oldproto.CompactTextString(m) }
func (*queryStatsResponse) ProtoMessage()    {}

type stat struct {
	Name  string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Value int64  `protobuf:"varint,2,opt,name=value,proto3" json:"value,omitempty"`
}

func (m *stat) Reset()         { *m = stat{} }
func (m *stat) String() string { return oldproto.CompactTextString(m) }
func (*stat) ProtoMessage()    {}

func queryUserStats(ctx context.Context, address string) ([]*stat, error) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	response := &queryStatsResponse{}
	if err := conn.Invoke(ctx, queryStatsMethod, &queryStatsRequest{Pattern: "user>>>"}, response); err != nil {
		return nil, err
	}
	return response.Stat, nil
}

type statsSyncResult struct {
	Added              int64
	Changed            bool
	EligibilityChanged bool
	DisabledUsers      []string
}

// mergeUserStats mutates exactly the State owned by its caller. It deliberately
// does not save or apply anything, allowing the daemon to combine statistics,
// access logs and policy evaluation into one locked transaction.
func mergeUserStats(s *State, stats []*stat, now time.Time, sampleInterval time.Duration) statsSyncResult {
	result := statsSyncResult{}
	if s.Counters == nil {
		s.Counters = map[string]int64{}
	}
	type statOwner struct {
		user *User
		node *Node
	}
	authOwners := map[string]statOwner{}
	for i := range s.Users {
		for j := range s.Users[i].Nodes {
			node := &s.Users[i].Nodes[j]
			authOwners[node.AuthUser] = statOwner{user: &s.Users[i], node: node}
		}
	}
	userAdded := map[*User]int64{}
	nodeDeltas := map[string]trafficDelta{}
	for _, item := range stats {
		if item == nil {
			continue
		}
		auth, direction, ok := parseUserStatName(item.Name)
		if !ok {
			continue
		}
		owner, exists := authOwners[auth]
		if !exists {
			continue
		}
		previous, hadPrevious := s.Counters[item.Name]
		delta := item.Value - previous
		if delta < 0 { // sing-box restarted and its in-memory counter reset.
			delta = item.Value
		}
		if delta < 0 {
			delta = 0
		}
		if direction == "uplink" {
			owner.user.Upload += delta
			owner.node.Upload += delta
			nodeDelta := nodeDeltas[owner.node.AuthUser]
			nodeDelta.upload += delta
			nodeDeltas[owner.node.AuthUser] = nodeDelta
		} else {
			owner.user.Download += delta
			owner.node.Download += delta
			nodeDelta := nodeDeltas[owner.node.AuthUser]
			nodeDelta.download += delta
			nodeDeltas[owner.node.AuthUser] = nodeDelta
		}
		result.Added += delta
		userAdded[owner.user] += delta
		if !hadPrevious || previous != item.Value {
			result.Changed = true
		}
		s.Counters[item.Name] = item.Value
	}
	if recordRealtimeUsageAt(s, nodeDeltas, now, sampleInterval) {
		result.Changed = true
	}
	for user, delta := range userAdded {
		if appendTrafficSample(user, delta, now) {
			result.Changed = true
		}
	}
	for i := range s.Users {
		user := &s.Users[i]
		if user.Enabled && (expired(*user, now) || overQuota(*user)) {
			user.Enabled = false
			user.DisabledReason = automaticDisableReason(*user, now)
			result.Changed = true
			result.EligibilityChanged = true
			result.DisabledUsers = append(result.DisabledUsers, user.Name)
		}
	}
	return result
}

func (a *app) syncCmd(args []string) error {
	return a.withStateLock(func() error { return a.syncCmdLocked(args) })
}

func (a *app) syncCmdLocked(args []string) error {
	fs := a.newFlagSet("sync")
	doApply := fs.Bool("apply", false, "同步后若触发配额/到期则立即应用配置")
	ifEnabled := fs.Bool("if-enabled", false, "未配置统计 API 时安静跳过")
	timeout := fs.Duration("timeout", 5*time.Second, "API 查询超时")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	if s.StatsAPI == "" {
		if *ifEnabled {
			fmt.Fprintln(a.out, "未启用 V2Ray 统计 API，已跳过同步")
			return nil
		}
		return errors.New("未启用统计；初始化时请提供 --stats-api 127.0.0.1:8080")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	stats, err := queryUserStats(ctx, s.StatsAPI)
	if err != nil {
		return fmt.Errorf("查询 V2Ray API %s: %w", s.StatsAPI, err)
	}
	now := time.Now()
	stages := currentThrottleStages(s)
	result := mergeUserStats(s, stats, now, usageSampleInterval(a.lastStatsSample, now))
	if result.EligibilityChanged {
		s.StatsApplyPending = true
	}
	queueThrottleStageApply(stages, s)
	for _, name := range result.DisabledUsers {
		if user := findUser(s, name); user != nil {
			fmt.Fprintf(a.out, "已禁用 %s：%s\n", user.Name, disableReason(*user, now))
		}
	}
	if err := saveState(a.statePath, s); err != nil {
		return err
	}
	a.lastStatsSample = now
	fmt.Fprintf(a.out, "统计同步完成，本次新增 %s\n", formatSize(result.Added))
	if *doApply && s.StatsApplyPending {
		if err := applyState(s, false, true, a.out); err != nil {
			return err
		}
		s.StatsApplyPending = false
		s.RateApplyPending = false
		return saveState(a.statePath, s)
	}
	if *doApply && s.RateApplyPending {
		if err := applyRateLimits(s, a.out); err != nil {
			return err
		}
		s.RateApplyPending = false
		return saveState(a.statePath, s)
	}
	if s.StatsApplyPending || s.RateApplyPending {
		fmt.Fprintln(a.out, "有运行策略变化；运行 sbmgr apply 使其生效")
	}
	return nil
}

func parseUserStatName(name string) (authUser, direction string, ok bool) {
	parts := strings.Split(name, ">>>")
	if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" {
		return "", "", false
	}
	if parts[3] != "uplink" && parts[3] != "downlink" {
		return "", "", false
	}
	return parts[1], parts[3], parts[1] != ""
}
