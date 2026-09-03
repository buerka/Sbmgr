package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	crand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestSubscriptionKeyPair(t *testing.T, commonName string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{commonName},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	certificateDER, err := x509.CreateCertificate(crand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(dir, "certificate.pem")
	keyPath := filepath.Join(dir, "private-key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER}), 0600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func TestSubscriptionTLSSettingsValidation(t *testing.T) {
	if err := validateSubscriptionRuntime(SubscriptionSettings{Enabled: true, Listen: "0.0.0.0:18443"}); err == nil || !strings.Contains(err.Error(), "必须配置 TLS") {
		t.Fatalf("public listener without TLS error = %v", err)
	}
	if err := validateSubscriptionRuntime(SubscriptionSettings{Enabled: true, Listen: "127.0.0.1:18080"}); err != nil {
		t.Fatal("loopback listener without TLS was rejected:", err)
	}
	if err := validateSubscriptionRuntime(SubscriptionSettings{Listen: "127.0.0.1:18080", TLSCertFile: "certificate.pem"}); err == nil || !strings.Contains(err.Error(), "同时配置") {
		t.Fatalf("unpaired TLS settings error = %v", err)
	}
	if err := validateSubscriptionRuntime(SubscriptionSettings{Listen: "127.0.0.1:18080", TLSCertFile: "certificate.pem", TLSKeyFile: "private-key.pem"}); err == nil || !strings.Contains(err.Error(), "绝对路径") {
		t.Fatalf("relative TLS paths error = %v", err)
	}
	missing := filepath.Join(t.TempDir(), "missing.pem")
	if err := validateSubscriptionRuntime(SubscriptionSettings{Listen: "127.0.0.1:18080", TLSCertFile: missing, TLSKeyFile: missing}); err == nil || !strings.Contains(err.Error(), "无法读取") {
		t.Fatalf("missing TLS files error = %v", err)
	}
	certPath, keyPath := writeTestSubscriptionKeyPair(t, "first")
	if err := validateSubscriptionRuntime(SubscriptionSettings{Enabled: true, Listen: "0.0.0.0:18443", TLSCertFile: certPath, TLSKeyFile: keyPath}); err != nil {
		t.Fatal("valid public HTTPS settings were rejected:", err)
	}
	_, otherKeyPath := writeTestSubscriptionKeyPair(t, "second")
	if err := validateSubscriptionRuntime(SubscriptionSettings{Listen: "127.0.0.1:18080", TLSCertFile: certPath, TLSKeyFile: otherKeyPath}); err == nil || !strings.Contains(err.Error(), "无法配对") {
		t.Fatalf("mismatched TLS pair error = %v", err)
	}
}

func TestSubscriptionTLSInfoReportsValidityAndSANs(t *testing.T) {
	certPath, keyPath := writeTestSubscriptionKeyPair(t, "subscription.example")
	info, err := subscriptionTLSInfo(SubscriptionSettings{TLSCertFile: certPath, TLSKeyFile: keyPath})
	if err != nil {
		t.Fatal(err)
	}
	if !info.Enabled || info.NotAfter.IsZero() || info.NotBefore.IsZero() {
		t.Fatalf("incomplete TLS info: %#v", info)
	}
	if len(info.DNSNames) != 1 || info.DNSNames[0] != "subscription.example" {
		t.Fatalf("DNS SANs = %#v", info.DNSNames)
	}
	if len(info.IPAddresses) != 1 || info.IPAddresses[0] != "127.0.0.1" {
		t.Fatalf("IP SANs = %#v", info.IPAddresses)
	}
	if got := info.status(time.Now()); got != "即将过期" {
		t.Fatalf("one-hour certificate status = %q", got)
	}
	disabled, err := subscriptionTLSInfo(SubscriptionSettings{})
	if err != nil || disabled.Enabled || disabled.status(time.Now()) != "未启用" {
		t.Fatalf("disabled TLS info = %#v, %v", disabled, err)
	}
}

func TestSubscriptionServerServesNativeHTTPS(t *testing.T) {
	certPath, keyPath := writeTestSubscriptionKeyPair(t, "subscription")
	statePath := filepath.Join(t.TempDir(), "state.json")
	state := &State{Subscription: SubscriptionSettings{
		Enabled:     true,
		Listen:      "127.0.0.1:0",
		TLSCertFile: certPath,
		TLSKeyFile:  keyPath,
	}}
	if err := saveState(statePath, state); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	a := &app{statePath: statePath, out: &output, err: io.Discard}
	server, err := a.startSubscriptionServer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec -- test certificate
	response, err := client.Get("https://" + server.Addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("HTTPS health status = %d", response.StatusCode)
	}
	if response.Header.Get("Strict-Transport-Security") == "" || response.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("HTTPS security headers = %#v", response.Header)
	}
	if !strings.Contains(output.String(), "(HTTPS)") {
		t.Fatalf("startup output did not identify HTTPS: %q", output.String())
	}
	if got := subscriptionURL(state, Device{SubscriptionToken: "token"}); !strings.HasPrefix(got, "https://") {
		t.Fatalf("default TLS subscription URL = %q", got)
	}
}

func TestSubscriptionCommandSetsAndReportsTLSPaths(t *testing.T) {
	certPath, keyPath := writeTestSubscriptionKeyPair(t, "command")
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(statePath, &State{}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	a := &app{statePath: statePath, out: &output, err: io.Discard}
	if err := a.subscriptionCmdLocked([]string{"set", "--tls-cert", certPath, "--tls-key", keyPath}); err != nil {
		t.Fatal(err)
	}
	stored, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Subscription.TLSCertFile != certPath || stored.Subscription.TLSKeyFile != keyPath {
		t.Fatalf("stored TLS settings = %#v", stored.Subscription)
	}
	output.Reset()
	if err := a.subscriptionCmdLocked([]string{"status"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "tls_cert="+certPath) || !strings.Contains(output.String(), "tls_key="+keyPath) {
		t.Fatalf("TLS status output = %q", output.String())
	}
	if strings.Contains(output.String(), "BEGIN PRIVATE KEY") {
		t.Fatal("TLS status leaked private key contents")
	}
}

func TestSubscriptionStateWithMissingCertificateCanLoadAndBeDisabled(t *testing.T) {
	directory := t.TempDir()
	statePath := filepath.Join(directory, "state.json")
	missingCert := filepath.Join(directory, "missing-fullchain.pem")
	missingKey := filepath.Join(directory, "missing-privkey.pem")
	state := &State{Version: stateVersion, Subscription: SubscriptionSettings{
		Enabled: true, Listen: "0.0.0.0:18443", BaseURL: "https://192.0.2.10:18443",
		TLSCertFile: missingCert, TLSKeyFile: missingKey,
	}}
	if err := saveState(statePath, state); err != nil {
		t.Fatal("external TLS files incorrectly made state unsaveable:", err)
	}
	loaded, err := loadState(statePath)
	if err != nil {
		t.Fatal("missing TLS files locked the CLI out of state:", err)
	}
	if err := validateSubscriptionRuntime(loaded.Subscription); err == nil || !strings.Contains(err.Error(), "无法读取") {
		t.Fatalf("runtime accepted missing certificate files: %v", err)
	}

	var output bytes.Buffer
	a := &app{statePath: statePath, out: &output, err: io.Discard}
	if err := a.subscriptionCmdLocked([]string{"set", "--enabled", "false", "--tls-cert", "", "--tls-key", ""}); err != nil {
		t.Fatal("administrator could not disable broken HTTPS settings:", err)
	}
	loaded, err = loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Subscription.Enabled || loaded.Subscription.TLSCertFile != "" || loaded.Subscription.TLSKeyFile != "" {
		t.Fatalf("broken HTTPS settings were not disabled: %#v", loaded.Subscription)
	}
}

func TestLegacyPublicHTTPSubscriptionStateLoadsButCannotStart(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	legacy := &State{Version: stateVersion, Subscription: SubscriptionSettings{
		Enabled: true, Listen: "0.0.0.0:18080", BaseURL: "http://192.0.2.10:18080",
	}}
	if err := saveState(statePath, legacy); err != nil {
		t.Fatal("legacy public HTTP state could not be preserved for repair:", err)
	}
	loaded, err := loadState(statePath)
	if err != nil {
		t.Fatal("legacy public HTTP state locked out the CLI:", err)
	}
	if err := validateSubscriptionRuntime(loaded.Subscription); err == nil || !strings.Contains(err.Error(), "必须配置 TLS") {
		t.Fatalf("legacy public HTTP listener was not rejected at runtime: %v", err)
	}
}
