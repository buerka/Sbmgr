package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	subscriptionWorkerArg    = "--subscription-worker"
	subscriptionMaxBody      = 4 << 20
	subscriptionMaxResponse  = 6 << 20 // JSON base64 overhead included.
	subscriptionMaxBootstrap = 512 << 10
	subscriptionIPCTimeout   = 5 * time.Second
)

var errSubscriptionIPC = errors.New("subscription IPC unavailable")

// Framing bounds every allocation. Incoming requests are one operation byte
// and at most 128 token bytes; no user-controlled JSON is parsed by root.
func readSubscriptionFrame(reader io.Reader, limit uint32) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, errSubscriptionIPC
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > limit {
		return nil, errSubscriptionIPC
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, errSubscriptionIPC
	}
	return body, nil
}

func writeSubscriptionFrame(writer io.Writer, body []byte, limit int) error {
	if len(body) == 0 || len(body) > limit {
		return errSubscriptionIPC
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)))
	if _, err := io.Copy(writer, bytes.NewReader(header[:])); err != nil {
		return errSubscriptionIPC
	}
	if _, err := io.Copy(writer, bytes.NewReader(body)); err != nil {
		return errSubscriptionIPC
	}
	return nil
}

type subscriptionBrokerBudget struct {
	mu     sync.Mutex
	window time.Time
	count  int
}

func (b *subscriptionBrokerBudget) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.window.IsZero() || now.Sub(b.window) >= time.Minute {
		b.window, b.count = now, 0
	}
	if b.count >= 600 {
		return false
	}
	b.count++
	return true
}

// The budget lives across worker restarts and is enforced by the privileged
// side, even if a compromised worker bypasses HTTP rate/concurrency checks.
func serveSubscriptionBroker(ctx context.Context, conn net.Conn, lookup subscriptionLookup, budget *subscriptionBrokerBudget) error {
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	for {
		// An idle worker is fine; a partial frame must finish within five seconds.
		_ = conn.SetReadDeadline(time.Time{})
		var first [1]byte
		if _, err := io.ReadFull(conn, first[:]); err != nil {
			return errSubscriptionIPC
		}
		_ = conn.SetReadDeadline(time.Now().Add(subscriptionIPCTimeout))
		request, err := readSubscriptionFrame(io.MultiReader(bytes.NewReader(first[:]), conn), 129)
		if err != nil {
			return err
		}
		if len(request) < 21 || request[0] < subscriptionGET || request[0] > subscriptionQR || !subscriptionTokenPattern.Match(request[1:]) {
			return errSubscriptionIPC
		}
		result := subscriptionFailure(http.StatusTooManyRequests)
		if budget.allow(time.Now()) {
			requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			result = lookup(requestCtx, request[0], string(request[1:]))
			cancel()
		}
		body, err := json.Marshal(result)
		if err != nil || len(body) > subscriptionMaxResponse {
			body = []byte(`{"status":503}`)
		}
		_ = conn.SetWriteDeadline(time.Now().Add(subscriptionIPCTimeout))
		if err := writeSubscriptionFrame(conn, body, subscriptionMaxResponse); err != nil {
			return err
		}
	}
}

type subscriptionRPC struct {
	conn   net.Conn
	gate   chan struct{}
	failed chan struct{}
	once   sync.Once
}

func newSubscriptionRPC(conn net.Conn) *subscriptionRPC {
	return &subscriptionRPC{conn: conn, gate: make(chan struct{}, 1), failed: make(chan struct{})}
}

func (rpc *subscriptionRPC) close() {
	rpc.once.Do(func() { _ = rpc.conn.Close(); close(rpc.failed) })
}

func (rpc *subscriptionRPC) lookup(ctx context.Context, operation byte, token string) subscriptionResult {
	failure := subscriptionFailure(http.StatusServiceUnavailable)
	select {
	case rpc.gate <- struct{}{}:
		defer func() { <-rpc.gate }()
	case <-ctx.Done():
		return failure
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(10 * time.Second)
	}
	_ = rpc.conn.SetDeadline(deadline)
	// A cancelled or incomplete exchange cannot be reused for another token.
	stop := context.AfterFunc(ctx, rpc.close)
	defer stop()
	request := append([]byte{operation}, []byte(token)...)
	if err := writeSubscriptionFrame(rpc.conn, request, 129); err != nil {
		rpc.close()
		return failure
	}
	body, err := readSubscriptionFrame(rpc.conn, subscriptionMaxResponse)
	if err != nil {
		rpc.close()
		return failure
	}
	var result subscriptionResult
	if json.Unmarshal(body, &result) != nil {
		rpc.close()
		return failure
	}
	switch result.Status {
	case http.StatusOK, http.StatusNotFound, http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusInternalServerError:
		return result
	default:
		rpc.close()
		return failure
	}
}
