package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const realtimeSyncInterval = 5 * time.Second

// daemonCmd is the single systemd-hosted maintenance loop. The interactive UI
// remains a normal foreground program; this process only restores rate rules,
// enforces expiry/quota state and collects traffic statistics when configured.
func (a *app) daemonCmd(args []string) error {
	fs := a.newFlagSet("daemon")
	interval := fs.Duration("interval", time.Minute, "后台维护间隔")
	once := fs.Bool("once", false, "只运行一次（安装检查用）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("daemon 不接受位置参数")
	}
	if *interval <= 0 {
		return errors.New("维护间隔必须大于 0")
	}
	if _, err := a.loadCanonicalState(); err != nil {
		return fmt.Errorf("准备后台状态: %w", err)
	}

	runCycle := func() error {
		started := time.Now()
		if err := a.daemonCycle(); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "%s 后台维护完成（%s）\n", started.Format(time.RFC3339), time.Since(started).Round(time.Millisecond))
		return nil
	}
	if *once {
		return runCycle()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a.startDaemonSubscription(ctx)
	fmt.Fprintf(a.out, "sbmgr %s 后台服务已启动，维护间隔 %s\n", appVersion, interval.String())
	if err := runCycle(); err != nil {
		fmt.Fprintf(a.err, "%s 后台维护失败: %v\n", time.Now().Format(time.RFC3339), err)
	}
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	var realtimeTicker *time.Ticker
	var realtimeTick <-chan time.Time
	if *interval > realtimeSyncInterval {
		realtimeTicker = time.NewTicker(realtimeSyncInterval)
		realtimeTick = realtimeTicker.C
		defer realtimeTicker.Stop()
	}
	lastRealtimeError := ""
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(a.out, "sbmgr 后台服务已停止")
			return nil
		case <-ticker.C:
			if err := runCycle(); err != nil {
				fmt.Fprintf(a.err, "%s 后台维护失败: %v\n", time.Now().Format(time.RFC3339), err)
			}
			if realtimeTicker != nil {
				// Do not immediately run the same nft/journal sample twice when the
				// minute and five-second tickers become ready together.
				select {
				case <-realtimeTicker.C:
				default:
				}
				realtimeTicker.Reset(realtimeSyncInterval)
			}
		case <-realtimeTick:
			if err := a.realtimeCycle(); err != nil {
				// A persistent journal or apply failure must not flood journald every
				// five seconds.  The normal maintenance cycle still reports it once
				// per interval and retries all pending state.
				if message := err.Error(); message != lastRealtimeError {
					fmt.Fprintf(a.err, "%s 实时状态同步失败: %v\n", time.Now().Format(time.RFC3339), err)
					lastRealtimeError = message
				}
			} else if lastRealtimeError != "" {
				fmt.Fprintln(a.out, "实时状态同步已恢复")
				lastRealtimeError = ""
			}
		}
	}
}

func (a *app) startDaemonSubscription(ctx context.Context) {
	if _, err := a.startSubscriptionServer(ctx); err != nil {
		// Subscription delivery is an auxiliary surface. A missing/expired TLS
		// certificate must not stop traffic accounting, quota enforcement, IP
		// handover, or the pending-apply retry loop.
		fmt.Fprintf(a.err, "订阅服务未启动: %v\n", err)
	}
}

// realtimeCycle refreshes nft byte counters, current Mbps, access records and
// dynamic source-IP handovers between normal maintenance runs. Both paths use
// the same state lock, so a sample cannot race an interactive edit or the
// minute maintenance cycle.
func (a *app) realtimeCycle() error {
	return a.withStateLock(a.realtimeCycleLocked)
}

func (a *app) realtimeCycleLocked() error {
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	now := time.Now()
	useNftCounters := s.StatsAPI == ""
	useStatsAPI := !useNftCounters
	hasDynamicIP := hasEnforcedDynamicIPPolicy(s, now)
	before, err := ipRestrictionSetSignature(s, now)
	if err != nil {
		return err
	}
	stages := currentThrottleStages(s)
	changed := false
	nftSampled := false
	statsSampled := false
	statsSampleAt := time.Time{}
	eligibilityChanged := false
	var cycleErrors []error
	if useStatsAPI {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		stats, queryErr := queryUserStats(ctx, s.StatsAPI)
		cancel()
		if queryErr != nil {
			cycleErrors = append(cycleErrors, fmt.Errorf("同步实时 V2Ray 流量统计: %w", queryErr))
		} else {
			statsSampleAt = time.Now()
			result := mergeUserStats(s, stats, statsSampleAt, usageSampleInterval(a.lastStatsSample, statsSampleAt))
			statsSampled = true
			changed = changed || result.Changed
			if result.EligibilityChanged {
				// StatsApplyPending is the historical on-disk name for a full
				// eligibility/config apply. Keep using it for compatibility.
				s.StatsApplyPending = true
				eligibilityChanged = true
				changed = true
			}
			for _, name := range result.DisabledUsers {
				if user := findUser(s, name); user != nil {
					fmt.Fprintf(a.out, "已禁用 %s：%s\n", user.Name, disableReason(*user, statsSampleAt))
				}
			}
			if !result.Changed {
				// No counter baseline needs persistence before advancing this
				// process-local sample clock.
				a.lastStatsSample = statsSampleAt
			}
			if result.Added > 0 {
				fmt.Fprintf(a.out, "已同步实时 V2Ray 流量 %s\n", formatSize(result.Added))
			}
		}
	} else {
		sampleInterval := usageSampleInterval(a.lastNftUsageSample, now)
		if _, nftChanged, err := syncNftUsageAt(s, now, sampleInterval); err != nil {
			cycleErrors = append(cycleErrors, fmt.Errorf("同步实时流量: %w", err))
		} else {
			nftSampled = true
			changed = changed || nftChanged
		}
	}
	if useNftCounters || hasDynamicIP {
		if _, accessChanged, err := syncAccessStats(s); err != nil {
			cycleErrors = append(cycleErrors, fmt.Errorf("同步实时访问: %w", err))
		} else {
			changed = changed || accessChanged
		}
	}
	after, err := ipRestrictionSetSignature(s, now)
	if err != nil {
		return err
	}
	ruleChanged := before != after
	tierChanged := queueThrottleStageApply(stages, s)
	if ruleChanged {
		s.IPApplyPending = true
		changed = true
	}
	if tierChanged {
		changed = true
	}
	if changed {
		if err := saveState(a.statePath, s); err != nil {
			return errors.Join(errors.Join(cycleErrors...), fmt.Errorf("保存实时状态: %w", err))
		}
	}
	if nftSampled {
		// Advance this in-memory clock only after any changed counter baseline is
		// durable. If saving failed, the next sample must span the whole unsaved
		// interval so its Mbps denominator still matches its byte delta.
		a.lastNftUsageSample = now
	}
	if statsSampled && changed {
		// changed state was saved above; the persisted API baseline now owns
		// this sample and prevents the minute cycle from charging it again.
		a.lastStatsSample = statsSampleAt
	}
	if ruleChanged || eligibilityChanged {
		// The learned binding is already durable. If applying fails, pending
		// flags remain set and the normal maintenance cycle retries them.
		if err := applyState(s, false, true, a.out); err != nil {
			return errors.Join(errors.Join(cycleErrors...), fmt.Errorf("应用实时资格或动态 IP 变更: %w", err))
		}
		s.IPApplyPending = false
		s.BurstApplyPending = false
		s.RateApplyPending = false  // applyState also installed current nft rates.
		s.StatsApplyPending = false // the full configuration includes eligibility changes too.
		// A successful restart terminates every old transport.  Keeping their
		// journal snapshots would fabricate a competing IP during the next switch.
		s.ActiveConnections = nil
		s.PendingSources = nil
		if err := saveState(a.statePath, s); err != nil {
			return errors.Join(errors.Join(cycleErrors...), fmt.Errorf("确认动态 IP 换绑: %w", err))
		}
		return errors.Join(cycleErrors...)
	}
	if !tierChanged {
		return errors.Join(cycleErrors...)
	}
	if s.StatsApplyPending || s.IPApplyPending || s.BurstApplyPending {
		// A failed full apply takes precedence over a rate-only refresh. Removing
		// the disabled user's nft rules while its old inbound remains live would
		// weaken enforcement. The normal cycle retries the full transaction.
		return errors.Join(cycleErrors...)
	}
	if err := applyRateLimits(s, a.out); err != nil {
		// The pending flag was saved above. Avoid a five-second retry storm; the
		// normal maintenance cycle retries it once per configured interval.
		return errors.Join(errors.Join(cycleErrors...), fmt.Errorf("应用实时阶梯限速: %w", err))
	}
	s.RateApplyPending = false
	if err := saveState(a.statePath, s); err != nil {
		return errors.Join(errors.Join(cycleErrors...), fmt.Errorf("确认实时阶梯限速: %w", err))
	}
	return errors.Join(cycleErrors...)
}

func usageSampleInterval(previous, now time.Time) time.Duration {
	if previous.IsZero() || !now.After(previous) {
		return 0
	}
	return now.Sub(previous)
}

func currentThrottleStages(s *State) map[string]int {
	stages := make(map[string]int, len(s.Users))
	for _, user := range s.Users {
		stages[user.Name] = throttleStage(user)
	}
	return stages
}

func throttleStagesChanged(before map[string]int, s *State) bool {
	for _, user := range s.Users {
		if before[user.Name] != throttleStage(user) {
			return true
		}
	}
	return false
}

func queueThrottleStageApply(before map[string]int, s *State) bool {
	if !throttleStagesChanged(before, s) {
		return false
	}
	s.RateApplyPending = true
	return true
}

func ipRestrictionSetSignature(s *State, now time.Time) (string, error) {
	rules, err := json.Marshal(ipRestrictionRules(s, now))
	if err != nil {
		return "", fmt.Errorf("计算来源 IP 规则签名: %w", err)
	}
	return string(rules), nil
}

func (a *app) daemonCycle() error {
	return a.withStateLock(a.daemonCycleLocked)
}

func (a *app) daemonCycleLocked() error {
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	var cycleErrors []error
	if ensureNodeMarks(s) {
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		if err := applyState(s, false, false, a.out); err != nil {
			cycleErrors = append(cycleErrors, fmt.Errorf("迁移节点统计 mark: %w", err))
		}
	} else if err := applyRateLimitsIfMissing(s, a.out); err != nil {
		cycleErrors = append(cycleErrors, fmt.Errorf("恢复实时限速与用量计数: %w", err))
	}
	billingChanged := evaluateBillingCycles(s, time.Now())
	if billingChanged {
		if err := saveState(a.statePath, s); err != nil {
			return fmt.Errorf("保存账期重置: %w", err)
		}
	}

	stages := currentThrottleStages(s)
	stateChanged := false
	nftSampled := false
	nftSampleAt := time.Time{}
	statsSampled := false
	statsSampleAt := time.Time{}
	if s.StatsAPI != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		stats, err := queryUserStats(ctx, s.StatsAPI)
		cancel()
		if err != nil {
			cycleErrors = append(cycleErrors, fmt.Errorf("同步 V2Ray 流量统计: %w", err))
		} else {
			statsSampleAt = time.Now()
			result := mergeUserStats(s, stats, statsSampleAt, usageSampleInterval(a.lastStatsSample, statsSampleAt))
			statsSampled = true
			stateChanged = stateChanged || result.Changed
			if result.EligibilityChanged {
				s.StatsApplyPending = true
				stateChanged = true
			}
			for _, name := range result.DisabledUsers {
				if user := findUser(s, name); user != nil {
					fmt.Fprintf(a.out, "已禁用 %s：%s\n", user.Name, disableReason(*user, statsSampleAt))
				}
			}
			if !result.Changed {
				a.lastStatsSample = statsSampleAt
			}
			if result.Added > 0 {
				fmt.Fprintf(a.out, "已同步 V2Ray 流量 %s\n", formatSize(result.Added))
			}
		}
	} else {
		nftSampleAt = time.Now()
		sampleInterval := usageSampleInterval(a.lastNftUsageSample, nftSampleAt)
		if added, changed, err := syncNftUsageAt(s, nftSampleAt, sampleInterval); err != nil {
			cycleErrors = append(cycleErrors, err)
		} else {
			nftSampled = true
			stateChanged = stateChanged || changed
			if !changed {
				// No counter baseline changed, so there is nothing that must be
				// persisted before advancing the in-memory sampling clock.
				a.lastNftUsageSample = nftSampleAt
			}
			if added > 0 {
				fmt.Fprintf(a.out, "已同步 nftables 流量 %s\n", formatSize(added))
			}
		}
	}
	if accesses, changed, err := syncAccessStats(s); err != nil {
		cycleErrors = append(cycleErrors, err)
	} else {
		stateChanged = stateChanged || changed
		if accesses > 0 {
			fmt.Fprintf(a.out, "已记录 %d 次受管节点访问\n", accesses)
		}
	}
	connectionStateChanged, connectionConfigChanged := evaluateConnectionPolicies(s, time.Now())
	stateChanged = stateChanged || connectionStateChanged
	if expireTemporaryIPPolicies(s, time.Now()) {
		stateChanged = true
	}
	if changed, err := checkOutboundHealth(s, time.Now(), false); err != nil {
		cycleErrors = append(cycleErrors, fmt.Errorf("探测出口健康: %w", err))
	} else {
		stateChanged = stateChanged || changed
	}
	if fleetCheckDue(s, time.Now()) && refreshFleet(s, time.Now()) {
		stateChanged = true
	}
	burstStateChanged, burstConfigChanged, burstHardDisconnect, burstAlerts := evaluateBurstPolicies(s, time.Now())
	stateChanged = stateChanged || burstStateChanged
	if burstConfigChanged {
		s.BurstApplyPending = true
		stateChanged = true
	}
	for _, alert := range burstAlerts {
		fmt.Fprintln(a.out, "告警:", alert.Message)
	}
	if evaluateLifecycleAlerts(s, time.Now()) {
		stateChanged = true
	}
	if changed, err := deliverPendingAlerts(s, time.Now()); err != nil {
		stateChanged = stateChanged || changed
		cycleErrors = append(cycleErrors, fmt.Errorf("发送 Webhook 告警: %w", err))
	} else {
		stateChanged = stateChanged || changed
	}
	tierChanged := queueThrottleStageApply(stages, s)
	if tierChanged {
		stateChanged = true
		for _, u := range s.Users {
			if stages[u.Name] != throttleStage(u) {
				fmt.Fprintf(a.out, "用户 %s 已进入阶梯 %d\n", u.Name, throttleStage(u))
			}
		}
	}
	stateSaveFailed := false
	if stateChanged {
		if err := saveState(a.statePath, s); err != nil {
			cycleErrors = append(cycleErrors, fmt.Errorf("保存后台统计: %w", err))
			stateSaveFailed = true
		} else if nftSampled {
			a.lastNftUsageSample = nftSampleAt
			if statsSampled {
				a.lastStatsSample = statsSampleAt
			}
		} else if statsSampled {
			a.lastStatsSample = statsSampleAt
		}
	}
	if stateSaveFailed {
		// A failed state save must prevent applying an in-memory policy that is
		// not yet durable. The next minute cycle will recompute the same policy
		// transition from the last successfully stored counter baseline.
		return errors.Join(cycleErrors...)
	}
	burstPending := s.BurstApplyPending
	if billingChanged || connectionConfigChanged || burstConfigChanged || burstConfigurationPending(s) || burstPending || s.IPApplyPending || s.StatsApplyPending {
		restart := billingChanged || connectionConfigChanged || burstHardDisconnect || s.IPApplyPending || s.StatsApplyPending || (burstPending && hasHardBurstBlock(s, time.Now()))
		if err := applyState(s, false, restart, a.out); err != nil {
			cycleErrors = append(cycleErrors, fmt.Errorf("应用自动保护状态: %w", err))
		} else {
			pendingChanged := false
			if restart {
				// Every tracked transport was terminated by the service restart.
				// Retaining these snapshots would create phantom concurrency and
				// dynamic-IP conflicts until their normal display TTL elapsed.
				s.ActiveConnections = nil
				s.PendingSources = nil
				pendingChanged = true
			}
			if s.IPApplyPending {
				s.IPApplyPending = false
				pendingChanged = true
			}
			if s.BurstApplyPending {
				s.BurstApplyPending = false
				pendingChanged = true
			}
			if s.RateApplyPending {
				s.RateApplyPending = false
				pendingChanged = true
			}
			if s.StatsApplyPending {
				s.StatsApplyPending = false
				pendingChanged = true
			}
			if pendingChanged {
				if err := saveState(a.statePath, s); err != nil {
					cycleErrors = append(cycleErrors, fmt.Errorf("保存自动保护应用状态: %w", err))
				}
			}
			if burstConfigChanged || burstPending {
				if burstHardDisconnect {
					fmt.Fprintln(a.out, "异常流量硬封禁已应用，现有连接已断开")
				} else {
					fmt.Fprintln(a.out, "异常流量软封禁限速状态已应用")
				}
			}
		}
	}

	if s.RateApplyPending {
		if err := applyRateLimits(s, a.out); err != nil {
			cycleErrors = append(cycleErrors, fmt.Errorf("更新阶梯限速: %w", err))
		} else {
			s.RateApplyPending = false
			if err := saveState(a.statePath, s); err != nil {
				cycleErrors = append(cycleErrors, fmt.Errorf("保存阶梯限速应用状态: %w", err))
			}
		}
	}
	if !s.StatsApplyPending {
		if err := a.enforceCmd([]string{"--apply"}); err != nil {
			cycleErrors = append(cycleErrors, fmt.Errorf("检查配额和到期时间: %w", err))
		}
	}
	return errors.Join(cycleErrors...)
}
