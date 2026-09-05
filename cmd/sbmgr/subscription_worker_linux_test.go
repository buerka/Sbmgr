//go:build linux

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func init() {
	// The test binary execs the real worker entrypoint, not a mocked server.
	if len(os.Args) == 2 && os.Args[1] == subscriptionWorkerArg {
		if runSubscriptionWorker() != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
}

func requireSubscriptionPrivilegeTest(t *testing.T) {
	t.Helper()
	if os.Getenv("SBMGR_RUN_PRIVILEGE_TEST") != "1" {
		t.Skip("requires explicit local privilege-test opt-in")
	}
	if os.Geteuid() != 0 {
		t.Fatal("privilege test must start as root")
	}
}

func TestSubscriptionPrivilegeDropProbe(t *testing.T) {
	if os.Getenv("SBMGR_PRIVILEGE_PROBE") != "1" {
		t.Skip("subprocess probe")
	}
	path := os.Getenv("SBMGR_PROBE_PRIVATE_FILE")
	if err := dropSubscriptionPrivileges(65534, 65534); err != nil {
		t.Fatal("could not drop privileges:", err)
	}
	if _, err := os.ReadFile(path); !os.IsPermission(err) {
		t.Fatal("worker can read private state")
	}
	if err := os.WriteFile(path, nil, 0600); !os.IsPermission(err) {
		t.Fatal("worker can write private state")
	}
	if syscall.Setresuid(0, 0, 0) == nil || syscall.Setresgid(0, 0, 0) == nil {
		t.Fatal("worker regained root")
	}
	if fd, err := unix.Socket(unix.AF_INET, unix.SOCK_RAW, unix.IPPROTO_ICMP); err == nil {
		unix.Close(fd)
		t.Fatal("worker retained raw network capability")
	}
	var group sync.WaitGroup
	problems := make(chan bool, 16)
	for range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			caps := [2]unix.CapUserData{}
			hdr := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
			err := unix.Capget(&hdr, &caps[0])
			nnp, nnpErr := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
			problems <- err != nil || caps != [2]unix.CapUserData{} || os.Geteuid() != 65534 || os.Getegid() != 65534 || nnpErr != nil || nnp != 1
		}()
	}
	group.Wait()
	close(problems)
	for failed := range problems {
		if failed {
			t.Fatal("a worker thread retained privileges")
		}
	}
}

func TestSubscriptionPrivilegeIsolation(t *testing.T) {
	requireSubscriptionPrivilegeTest(t)
	privateFile := filepath.Join(t.TempDir(), "private-state")
	if err := os.WriteFile(privateFile, []byte("private fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	executable, _ := os.Executable()
	command := exec.Command(executable, "-test.run=^TestSubscriptionPrivilegeDropProbe$")
	command.Env = []string{"SBMGR_PRIVILEGE_PROBE=1", "SBMGR_PROBE_PRIVATE_FILE=" + privateFile}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("privilege probe failed: %v\n%s", err, output)
	}
}

func TestSubscriptionPrivilegeWorkerHTTPSAndRevocation(t *testing.T) {
	requireSubscriptionPrivilegeTest(t)
	statePath := filepath.Join(t.TempDir(), "state.db")
	s := &State{Subscription: SubscriptionSettings{Enabled: true, Listen: "127.0.0.1:0", BaseURL: "https://sub.example"}, Client: ClientSettings{Server: "proxy.example", Port: 443, PublicKey: "test-public-key"}, Users: []User{
		{Name: "alice", Enabled: true, Nodes: []Node{{Name: "node", AuthUser: "alice-node", UUID: newUUID()}}},
		{Name: "bob", Enabled: true, Nodes: []Node{{Name: "node", AuthUser: "bob-node", UUID: newUUID()}}},
	}}
	if err := saveState(statePath, s); err != nil {
		t.Fatal(err)
	}
	a := &app{statePath: statePath, out: io.Discard, err: io.Discard}
	certFile, keyFile := writeTestSubscriptionKeyPair(t, "isolated")
	cert, _ := os.ReadFile(certFile)
	key, _ := os.ReadFile(keyFile)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executable, _ := os.Executable()
	worker, err := spawnSubscriptionProcess(ctx, executable, "127.0.0.1:0", subscriptionBootstrap{UID: 65534, GID: 65534, Cert: cert, Key: key}, a.lookupSubscription, &subscriptionBrokerBudget{})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.stop()
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", worker.PID))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Uid:\t65534\t65534\t65534\t65534", "Gid:\t65534\t65534\t65534\t65534", "CapEff:\t0000000000000000", "CapPrm:\t0000000000000000", "CapInh:\t0000000000000000", "CapAmb:\t0000000000000000", "NoNewPrivs:\t1"} {
		if !strings.Contains(string(status), want) {
			t.Fatalf("worker isolation property missing: %s", strings.Split(want, ":")[0])
		}
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // test-only self-signed certificate
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	request := func(method, path string, want int) []byte {
		t.Helper()
		r, _ := http.NewRequest(method, "https://"+worker.Addr+path, nil)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal("isolated HTTP request failed")
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != want {
			t.Fatalf("isolated response status %d, want %d", response.StatusCode, want)
		}
		return body
	}
	token := s.Users[0].Devices[0].SubscriptionToken
	request("GET", "/healthz", 204)
	body := request("GET", "/sub/"+token, 200)
	if !strings.Contains(string(body), s.Users[0].Nodes[0].UUID) || strings.Contains(string(body), s.Users[1].Nodes[0].UUID) {
		t.Fatal("worker returned another device or omitted own device")
	}
	if len(request("HEAD", "/sub/"+token, 200)) != 0 {
		t.Fatal("HEAD returned body")
	}
	if len(request("GET", "/qr/"+token+".png", 200)) < 100 {
		t.Fatal("missing QR")
	}
	request("GET", "/sub/"+newSubscriptionToken(), 404)
	s.Users[0].Enabled = false
	if err := saveState(statePath, s); err != nil {
		t.Fatal(err)
	}
	request("GET", "/sub/"+token, 403)
	request("GET", "/qr/"+token, 403)
	s.Users[0].Enabled = true
	s.Users[0].Devices[0].SubscriptionToken = newSubscriptionToken()
	if err := saveState(statePath, s); err != nil {
		t.Fatal(err)
	}
	request("GET", "/sub/"+token, 404)
	worker.stop()
	select {
	case <-worker.Done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker survived shutdown")
	}
	if conn, err := net.DialTimeout("tcp", worker.Addr, time.Second); err == nil {
		conn.Close()
		t.Fatal("worker listener survived shutdown")
	}
}

func TestSubscriptionPrivilegeWorkerCrashRestarts(t *testing.T) {
	requireSubscriptionPrivilegeTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executable, _ := os.Executable()
	budget := &subscriptionBrokerBudget{}
	start := func() (*subscriptionProcess, error) {
		return spawnSubscriptionProcess(ctx, executable, "127.0.0.1:0", subscriptionBootstrap{UID: 65534, GID: 65534}, func(context.Context, byte, string) subscriptionResult { return subscriptionFailure(404) }, budget)
	}
	worker, err := start()
	if err != nil {
		t.Fatal(err)
	}
	defer worker.stop()
	restarted := make(chan *subscriptionProcess, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		superviseSubscription(ctx, worker, 20*time.Millisecond, func() (*subscriptionProcess, error) {
			next, err := start()
			if err == nil {
				restarted <- next
			}
			return next, err
		}, func(string) {})
	}()
	if err := unix.Kill(worker.PID, unix.SIGKILL); err != nil {
		t.Fatal(err)
	}
	select {
	case next := <-restarted:
		defer next.stop()
		if next.PID == worker.PID {
			t.Fatal("worker was not replaced")
		}
		client := &http.Client{Timeout: time.Second}
		response, err := client.Get("http://" + next.Addr + "/healthz")
		if err != nil {
			t.Fatal("replacement worker did not accept HTTP")
		}
		response.Body.Close()
		if response.StatusCode != 204 {
			t.Fatal("replacement health failed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker was not restarted")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not stop")
	}
}

func TestSubscriptionPrivilegeParentExitProbe(t *testing.T) {
	if os.Getenv("SBMGR_PARENT_EXIT_PROBE") != "1" {
		t.Skip("subprocess probe")
	}
	executable, _ := os.Executable()
	worker, err := spawnSubscriptionProcess(context.Background(), executable, "127.0.0.1:0", subscriptionBootstrap{UID: 65534, GID: 65534}, func(context.Context, byte, string) subscriptionResult { return subscriptionFailure(404) }, &subscriptionBrokerBudget{})
	if err != nil {
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(worker.Addr)
	os.Exit(0) // Intentional parent death without child cleanup.
}

func TestSubscriptionPrivilegeParentDeathClosesListener(t *testing.T) {
	requireSubscriptionPrivilegeTest(t)
	executable, _ := os.Executable()
	command := exec.Command(executable, "-test.run=^TestSubscriptionPrivilegeParentExitProbe$")
	command.Env = []string{"SBMGR_PARENT_EXIT_PROBE=1"}
	output, err := command.Output()
	if err != nil {
		t.Fatal("parent probe failed")
	}
	var address string
	if json.Unmarshal(output, &address) != nil {
		t.Fatal("invalid parent probe response")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err != nil {
			return
		}
		conn.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("orphaned subscription listener remained open")
}
