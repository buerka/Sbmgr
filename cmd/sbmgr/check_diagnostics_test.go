package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRedactSingBoxDiagnosticsHidesCredentialValues(t *testing.T) {
	config := []byte(`{
  "outbounds": [{
    "type": "socks",
    "tag": "to-test",
    "server": "proxy.example.com",
    "username": "account-123",
    "password": "password-456",
    "tls": {"client_secret": "nested-secret"}
  }],
  "inbounds": [{"users": [{"name": "managed-auth-user", "uuid": "12345678-1234-1234-1234-123456789abc"}]}]
}`)
	diagnostic := []byte(`failed password password-456 for "account-123" client_secret nested-secret uuid 12345678-1234-1234-1234-123456789abc user managed-auth-user at proxy.example.com`)
	redacted := string(redactSingBoxDiagnostics(diagnostic, config))
	for _, secret := range []string{"account-123", "password-456", "nested-secret", "12345678-1234-1234-1234-123456789abc", "managed-auth-user"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("diagnostic leaked %q: %s", secret, redacted)
		}
	}
	if !strings.Contains(redacted, "proxy.example.com") || !strings.Contains(redacted, "<已隐藏>") {
		t.Fatalf("useful non-secret diagnostics were lost: %s", redacted)
	}
}

func TestCappedDiagnosticBufferDrainsAndTruncates(t *testing.T) {
	var diagnostic cappedDiagnosticBuffer
	input := bytes.Repeat([]byte("x"), maxSingBoxCheckDiagnosticBytes+100)
	if written, err := diagnostic.Write(input); err != nil || written != len(input) {
		t.Fatalf("Write() = %d, %v", written, err)
	}
	if diagnostic.buffer.Len() != maxSingBoxCheckDiagnosticBytes || !diagnostic.truncated {
		t.Fatalf("buffer len=%d truncated=%v", diagnostic.buffer.Len(), diagnostic.truncated)
	}
}

func TestRedactSingBoxDiagnosticsHidesAuthKeyAndShortCredentialOutput(t *testing.T) {
	diagnostic := []byte("bad auth key tail-auth-secret and password xy")
	config := []byte(`{"endpoints":[{"type":"tailscale","tag":"tail","auth_key":"tail-auth-secret","password":"xy"}]}`)
	redacted := string(redactSingBoxDiagnostics(diagnostic, config))
	if strings.Contains(redacted, "tail-auth-secret") || strings.Contains(redacted, "xy") {
		t.Fatalf("short or endpoint credential leaked: %s", redacted)
	}
	if !strings.Contains(redacted, "详细内容已隐藏") {
		t.Fatalf("short credential did not select safe generic diagnostic: %s", redacted)
	}
}

func TestRedactSingBoxDiagnosticsCoversProtocolSpecificSecrets(t *testing.T) {
	config := []byte(`{
  "outbounds": [{"type":"hysteria","tag":"hy","auth":"hy-auth-value","auth_str":"hy-auth-string"}],
  "tls": {"key":"inline-private-key", "acme":{"external_account":{"mac_key":"acme-mac-value"}}},
  "services": [{"type":"derp","tag":"derp","mesh_psk":"derp-mesh-value"}]
}`)
	diagnostic := []byte(`auth hy-auth-value quoted "hy-auth-string" key inline-private-key mac acme-mac-value psk derp-mesh-value`)
	redacted := string(redactSingBoxDiagnostics(diagnostic, config))
	for _, secret := range []string{"hy-auth-value", "hy-auth-string", "inline-private-key", "acme-mac-value", "derp-mesh-value"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("protocol-specific diagnostic leaked %q: %s", secret, redacted)
		}
	}
}
