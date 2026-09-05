//go:build !linux

package main

import (
	"context"
	"errors"
)

func launchSubscriptionProcess(context.Context, SubscriptionSettings, subscriptionLookup, *subscriptionBrokerBudget) (*subscriptionProcess, error) {
	return nil, errors.New("隔离订阅服务仅支持 Linux；不会回退到特权 HTTP 服务")
}

func runSubscriptionWorker() error {
	return errors.New("隔离订阅服务仅支持 Linux")
}
