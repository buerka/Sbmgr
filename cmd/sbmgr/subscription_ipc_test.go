package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubscriptionBrokerRejectsMalformedAndOversizedRequests(t *testing.T) {
	for _, body := range [][]byte{
		append([]byte{99}, []byte(strings.Repeat("a", 32))...),
		append([]byte{subscriptionGET}, []byte("../../state.db")...),
		append([]byte{subscriptionGET}, []byte(strings.Repeat("a", 129))...),
		[]byte(`{"command":"admin snapshot"}`),
	} {
		parent, child := net.Pipe()
		var calls atomic.Int32
		done := make(chan error, 1)
		go func() {
			done <- serveSubscriptionBroker(context.Background(), parent, func(context.Context, byte, string) subscriptionResult {
				calls.Add(1)
				return subscriptionFailure(http.StatusOK)
			}, &subscriptionBrokerBudget{})
		}()
		_ = child.SetDeadline(time.Now().Add(time.Second))
		_ = writeSubscriptionFrame(child, body, 1024)
		_ = child.Close()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("malformed request accepted")
			}
		case <-time.After(time.Second):
			t.Fatal("broker did not stop")
		}
		if calls.Load() != 0 {
			t.Fatal("malformed request reached private state")
		}
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], ^uint32(0))
	if _, err := readSubscriptionFrame(bytes.NewReader(header[:]), 129); err == nil {
		t.Fatal("oversized allocation accepted")
	}
}

func TestSubscriptionBrokerBudgetSurvivesNewChannel(t *testing.T) {
	budget := &subscriptionBrokerBudget{window: time.Now(), count: 599}
	var calls atomic.Int32
	for index, want := range []int{http.StatusOK, http.StatusTooManyRequests} {
		parent, child := net.Pipe()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- serveSubscriptionBroker(ctx, parent, func(context.Context, byte, string) subscriptionResult {
				calls.Add(1)
				return subscriptionFailure(http.StatusOK)
			}, budget)
		}()
		requestCtx, requestCancel := context.WithTimeout(ctx, time.Second)
		result := newSubscriptionRPC(child).lookup(requestCtx, subscriptionHEAD, strings.Repeat("a", 32))
		requestCancel()
		cancel()
		_ = child.Close()
		<-done
		if result.Status != want {
			t.Fatalf("channel %d status = %d", index, result.Status)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("privileged budget bypassed")
	}
}

func TestSubscriptionRPCCancellationClosesChannel(t *testing.T) {
	parent, child := net.Pipe()
	defer parent.Close()
	defer child.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if got := newSubscriptionRPC(child).lookup(ctx, subscriptionHEAD, strings.Repeat("a", 32)); got.Status != http.StatusServiceUnavailable {
		t.Fatal("hung broker accepted")
	}
	if _, err := parent.Read(make([]byte, 1)); err != io.EOF {
		t.Fatal("cancelled exchange was reusable")
	}
}

func TestSubscriptionFormExplainsPrivilegeIsolation(t *testing.T) {
	m := tuiModel{form: tuiForm{kind: formSubscriptionSettings}}
	help := strings.Join(m.formHelpLines(100), "")
	for _, want := range []string{"低权限", "后台维护", "应用配置"} {
		if !strings.Contains(help, want) {
			t.Fatalf("missing subscription guidance: %s", want)
		}
	}
}
