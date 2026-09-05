package main

import (
	"context"
	"crypto/tls"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
)

// Only the unprivileged worker calls this in production. Unit tests provide a
// direct lookup to exercise the same HTTP contract without requiring root.
func newSubscriptionHTTPServer(addr string, lookup subscriptionLookup) *http.Server {
	limiter := &subscriptionLimiter{}
	requests := make(chan struct{}, 4)
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Referrer-Policy", "no-referrer")
		if request.TLS != nil {
			response.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if request.URL.Path == "/healthz" {
			response.WriteHeader(http.StatusNoContent)
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			response.Header().Set("Allow", "GET, HEAD")
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !limiter.allow(subscriptionClientIP(request), time.Now()) {
			http.Error(response, "too many requests", http.StatusTooManyRequests)
			return
		}
		parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
		if len(parts) != 2 || (parts[0] != "sub" && parts[0] != "qr") {
			http.NotFound(response, request)
			return
		}
		token := strings.TrimSuffix(parts[1], ".png")
		if !subscriptionTokenPattern.MatchString(token) {
			http.NotFound(response, request)
			return
		}
		operation := subscriptionGET
		if parts[0] == "qr" {
			operation = subscriptionQR
		} else if request.Method == http.MethodHead {
			operation = subscriptionHEAD
		}
		select {
		case requests <- struct{}{}:
			defer func() { <-requests }()
		default:
			http.Error(response, "busy", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
		defer cancel()
		result := lookup(ctx, operation, token)
		if result.Status != http.StatusOK {
			http.Error(response, http.StatusText(result.Status), result.Status)
			return
		}
		if operation == subscriptionQR {
			if request.Method != http.MethodGet {
				response.Header().Set("Allow", "GET")
				http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			png, err := qrcode.Encode(result.QRURL, qrcode.Medium, 384)
			if err != nil {
				http.Error(response, "qr unavailable", http.StatusInternalServerError)
				return
			}
			response.Header().Set("Content-Type", "image/png")
			_, _ = response.Write(png)
			return
		}
		for name, value := range result.Headers {
			response.Header().Set(name, value)
		}
		response.WriteHeader(http.StatusOK)
		if request.Method == http.MethodGet {
			_, _ = response.Write(result.Body)
		}
	})
	return &http.Server{
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		Addr:      addr, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 16 * 1024,
		// HTTP/TLS errors and panic stacks can include untrusted request data.
		// The supervisor reports only worker lifecycle errors to the root log.
		ErrorLog: log.New(io.Discard, "", 0),
	}
}
