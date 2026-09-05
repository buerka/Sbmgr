package main

import (
	"context"
	"fmt"
	"time"
)

type subscriptionProcess struct {
	Addr string
	PID  int
	Done <-chan error
	stop context.CancelFunc
}

func (a *app) startIsolatedSubscription(ctx context.Context, budget *subscriptionBrokerBudget) (*subscriptionProcess, error) {
	s, err := loadState(a.statePath)
	if err != nil {
		return nil, err
	}
	settings := normalizedSubscriptionSettings(s.Subscription)
	if !settings.Enabled {
		return nil, nil
	}
	if err := validateSubscriptionRuntime(settings); err != nil {
		return nil, err
	}
	return launchSubscriptionProcess(ctx, settings, a.lookupSubscription, budget)
}

func (a *app) startDaemonSubscription(ctx context.Context) {
	budget := &subscriptionBrokerBudget{}
	worker, err := a.startIsolatedSubscription(ctx, budget)
	if err != nil {
		fmt.Fprintf(a.err, "订阅服务未启动: %v\n", err)
		return
	}
	if worker == nil {
		return
	}
	fmt.Fprintln(a.out, "订阅 HTTP 已在专用低权限进程启动")
	go superviseSubscription(ctx, worker, 5*time.Second,
		func() (*subscriptionProcess, error) { return a.startIsolatedSubscription(ctx, budget) },
		func(message string) { fmt.Fprintln(a.err, message) })
}

func superviseSubscription(ctx context.Context, worker *subscriptionProcess, retry time.Duration, start func() (*subscriptionProcess, error), report func(string)) {
	defer func() {
		if worker != nil {
			worker.stop()
		}
	}()
	for {
		if worker != nil {
			select {
			case <-ctx.Done():
				return
			case <-worker.Done:
			}
			worker.stop()
			if ctx.Err() != nil {
				return
			}
			report("订阅工作进程已退出；稍后重试，后台维护继续运行")
		}
		timer := time.NewTimer(retry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		var err error
		worker, err = start()
		if err != nil {
			report("订阅工作进程重启失败；保持关闭并继续重试")
			continue
		}
		if worker == nil {
			return
		}
	}
}
