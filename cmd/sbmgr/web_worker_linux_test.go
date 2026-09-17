//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func init() {
	if len(os.Args) == 2 && os.Args[1] == webWorkerArg {
		if runWebWorker() != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
}

func TestWebPrivilegeWorkerTLSAuthenticationAndShutdown(t *testing.T) {
	requireSubscriptionPrivilegeTest(t)
	a, b := webFixture(t)
	c := b.config
	c.Origin = "https://admin.example"
	c.BasePath = "/worker-fixture"
	writeWebFixtureConfig(t, a, c)
	certPath, keyPath := writeTestSubscriptionKeyPair(t, "isolated")
	cert, _ := os.ReadFile(certPath)
	key, _ := os.ReadFile(keyPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executable, _ := os.Executable()
	p, err := spawnWebProcess(ctx, executable, "127.0.0.1:0", webBootstrap{UID: 65534, GID: 65534, Origin: c.Origin, BasePath: c.BasePath, Cert: cert, Key: key}, b.lookup)
	if err != nil {
		t.Fatal(err)
	}
	defer p.stop()
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", p.PID))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "Uid:", "Gid:":
			for _, v := range fields[1:] {
				if v != "65534" {
					t.Fatal("HTTP worker retained privileged identity")
				}
			}
		case "CapInh:", "CapPrm:", "CapEff:", "CapAmb:":
			if strings.Trim(fields[1], "0") != "" {
				t.Fatal("HTTP worker retained capabilities")
			}
		case "NoNewPrivs:":
			if fields[1] != "1" {
				t.Fatal("HTTP worker can gain privileges")
			}
		}
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // isolated test certificate
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	call := func(path string, data []byte, cookie *http.Cookie) (*http.Response, []byte) {
		t.Helper()
		method := "GET"
		if data != nil {
			method = "POST"
		}
		req, _ := http.NewRequest(method, "https://"+p.Addr+path, bytes.NewReader(data))
		req.Host = "admin.example"
		req.Header.Set("Origin", c.Origin)
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		r, err := client.Do(req)
		if err != nil {
			t.Fatal("isolated Web HTTPS request failed")
		}
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		return r, body
	}
	if r, _ := call("/api/state", nil, nil); r.StatusCode != 404 {
		t.Fatal("worker exposed an unprefixed API")
	}
	if r, _ := call(c.BasePath+"/api/state", nil, nil); r.StatusCode != 401 {
		t.Fatal("unauthenticated worker access")
	}
	data, _ := json.Marshal(map[string]string{"username": "admin", "password": webTestPassword})
	r, _ := call(c.BasePath+"/api/login", data, nil)
	cookies := r.Cookies()
	if r.StatusCode != 200 || len(cookies) != 1 || !cookies[0].Secure || cookies[0].Path != c.BasePath+"/" {
		t.Fatal("worker login failed")
	}
	r, body := call(c.BasePath+"/api/state", nil, cookies[0])
	if r.StatusCode != 200 || !bytes.Contains(body, []byte("alice")) {
		t.Fatal("privileged broker inventory unavailable")
	}
	if r, _ := call("/state.db", nil, cookies[0]); r.StatusCode != 404 {
		t.Fatal("worker exposed state")
	}
	p.stop()
	select {
	case <-p.Done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker survived shutdown")
	}
	if conn, err := net.DialTimeout("tcp", p.Addr, time.Second); err == nil {
		conn.Close()
		t.Fatal("listener survived shutdown")
	}
}
