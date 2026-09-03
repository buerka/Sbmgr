package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOutboundHealthDetectsFailureAndRecovery(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	base := filepath.Join(t.TempDir(), "base.json")
	raw := map[string]any{"outbounds": []any{map[string]any{"type": "socks", "tag": "test-out", "server": host, "server_port": portText}}}
	data, _ := json.Marshal(raw)
	if err := os.WriteFile(base, data, 0600); err != nil {
		t.Fatal(err)
	}
	s := &State{BaseConfig: base, Health: HealthSettings{Mode: "auto", IntervalMinutes: 1, TimeoutSeconds: 1, AlertAfterFailures: 1}}
	now := time.Now()
	if changed, err := checkOutboundHealth(s, now, true); err != nil || !changed {
		t.Fatalf("healthy check changed=%v err=%v", changed, err)
	}
	if !s.OutboundHealth["test-out"].Healthy {
		t.Fatalf("reachable outbound marked failed: %#v", s.OutboundHealth)
	}
	_ = listener.Close()
	if _, err := checkOutboundHealth(s, now.Add(time.Minute), true); err != nil {
		t.Fatal(err)
	}
	if s.OutboundHealth["test-out"].Healthy || s.OutboundHealth["test-out"].Failures != 1 {
		t.Fatalf("closed outbound not marked failed: %#v", s.OutboundHealth["test-out"])
	}
	if len(s.Alerts) != 1 || s.Alerts[0].Kind != "outbound_unhealthy" {
		t.Fatalf("failure alert missing: %#v", s.Alerts)
	}
}

func TestWebhookDeliveryMarksAlertAndUsesBearerSecret(t *testing.T) {
	received := make(chan bool, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer secret-value" || request.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	s := &State{Notifications: NotificationSettings{WebhookURL: server.URL, WebhookSecret: "secret-value", TimeoutSeconds: 2}, Alerts: []Alert{{At: time.Now().Format(time.RFC3339), Kind: "quota", User: "alice", Message: "test"}}}
	changed, err := deliverPendingAlerts(s, time.Now())
	if err != nil || !changed {
		t.Fatalf("delivery changed=%v err=%v", changed, err)
	}
	select {
	case <-received:
	default:
		t.Fatal("webhook request was not received")
	}
	alert := s.Alerts[0]
	if alert.NotifiedAt == "" || alert.NotifyAttempts != 1 || alert.NotifyError != "" {
		t.Fatalf("bad notification state: %#v", alert)
	}
	if strings.Contains(string(mustJSON(t, s)), "secret-value") == false {
		t.Fatal("notification secret unexpectedly omitted from persisted state")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
