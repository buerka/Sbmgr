package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// In-process HTTP exists only in test binaries. Production always uses the
// Linux privilege-separated worker, with no fallback to root HTTP handling.
func (a *app) startSubscriptionServer(ctx context.Context) (*http.Server, error) {
	s, err := a.loadCanonicalState()
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
	listener, err := net.Listen("tcp", settings.Listen)
	if err != nil {
		return nil, err
	}
	server := newSubscriptionHTTPServer(listener.Addr().String(), a.lookupSubscription)
	go func() {
		if settings.TLSCertFile != "" {
			_ = server.ServeTLS(listener, settings.TLSCertFile, settings.TLSKeyFile)
		} else {
			_ = server.Serve(listener)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	scheme := "HTTP"
	if settings.TLSCertFile != "" {
		scheme = "HTTPS"
	}
	fmt.Fprintf(a.out, "订阅服务已监听 %s (%s)\n", settings.Listen, scheme)
	return server, nil
}
