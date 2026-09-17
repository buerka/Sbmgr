//go:build !linux

package main

import (
	"context"
	"errors"
	"net"
)

// Non-Linux is a loopback-only development host. Production management and
// sing-box/nftables operations remain Linux-only.
func launchWebProcess(ctx context.Context, c webConfig, lookup webLookup) (*subscriptionProcess, error) {
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil || !localSubscriptionHost(host) {
		return nil, errors.New("非 Linux Web 预览仅允许回环监听")
	}
	ln, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return nil, err
	}
	server := newWebHTTPServer(ln.Addr().String(), c.Origin, lookup)
	workerCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { <-workerCtx.Done(); _ = server.Close() }()
	go func() {
		defer stop()
		var err error
		if c.TLSCert != "" {
			err = server.ServeTLS(ln, c.TLSCert, c.TLSKey)
		} else {
			err = server.Serve(ln)
		}
		done <- err
		close(done)
	}()
	return &subscriptionProcess{Addr: ln.Addr().String(), Done: done, stop: stop}, nil
}
func runWebWorker() error { return errors.New("Web 工作进程仅用于 Linux") }
