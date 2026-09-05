package main

import (
	"context"
	"net/http"
	"time"
)

const (
	subscriptionGET byte = iota + 1
	subscriptionHEAD
	subscriptionQR
)

// This is the entire privileged RPC surface: one bearer token and one of
// three read-only operations. No paths, commands, SQL or state mutations can
// be selected by the HTTP process. Only the requested device leaves it.
type subscriptionResult struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"body,omitempty"`
	QRURL   string            `json:"qr_url,omitempty"`
}

type subscriptionLookup func(context.Context, byte, string) subscriptionResult

func subscriptionFailure(status int) subscriptionResult {
	return subscriptionResult{Status: status}
}

func (a *app) lookupSubscription(ctx context.Context, operation byte, token string) subscriptionResult {
	if operation < subscriptionGET || operation > subscriptionQR || !subscriptionTokenPattern.MatchString(token) {
		return subscriptionFailure(http.StatusNotFound)
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	exists, err := subscriptionTokenExists(lookupCtx, a.statePath, token)
	cancel()
	if err != nil {
		return subscriptionFailure(http.StatusServiceUnavailable)
	}
	if !exists {
		return subscriptionFailure(http.StatusNotFound)
	}
	// loadState is read-only; canonicalization is committed once at startup.
	state, err := loadState(a.statePath)
	if err != nil || ctx.Err() != nil {
		return subscriptionFailure(http.StatusServiceUnavailable)
	}
	if !normalizedSubscriptionSettings(state.Subscription).Enabled {
		return subscriptionFailure(http.StatusServiceUnavailable)
	}
	u, device := findSubscriptionDevice(state, token)
	if u == nil || device == nil {
		return subscriptionFailure(http.StatusNotFound)
	}
	if subscriptionDeviceAvailable(*u, *device, time.Now()) != nil {
		return subscriptionFailure(http.StatusForbidden)
	}
	if operation == subscriptionQR {
		return subscriptionResult{Status: http.StatusOK, QRURL: subscriptionURL(state, *device)}
	}
	headers, err := subscriptionProfileHeadersAt(*u, *device, time.Now())
	if err != nil {
		return subscriptionFailure(http.StatusInternalServerError)
	}
	result := subscriptionResult{Status: http.StatusOK, Headers: make(map[string]string)}
	for name := range headers {
		result.Headers[name] = headers.Get(name)
	}
	if operation == subscriptionGET {
		result.Body, err = renderMihomoDevice(state, *u, device.Name)
		if err != nil || len(result.Body) > subscriptionMaxBody {
			return subscriptionFailure(http.StatusInternalServerError)
		}
	}
	return result
}
