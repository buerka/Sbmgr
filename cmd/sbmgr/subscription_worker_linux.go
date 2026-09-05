//go:build linux

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const subscriptionAccount = "sbmgr-subscription"

type subscriptionBootstrap struct {
	UID  int    `json:"uid"`
	GID  int    `json:"gid"`
	Cert []byte `json:"cert,omitempty"`
	Key  []byte `json:"key,omitempty"`
}

func subscriptionIdentity() (int, int, error) {
	identity, err := user.Lookup(subscriptionAccount)
	if err != nil {
		return 0, 0, errors.New("缺少专用订阅账号；请先运行 deploy/install-systemd.sh --component core")
	}
	uid, e1 := strconv.Atoi(identity.Uid)
	gid, e2 := strconv.Atoi(identity.Gid)
	if e1 != nil || e2 != nil || uid <= 0 || gid <= 0 {
		return 0, 0, errors.New("订阅账号必须使用非零 UID/GID")
	}
	return uid, gid, nil
}

func readSubscriptionCredential(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("无法读取订阅 TLS 材料")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 128<<10+1))
	if err != nil || len(data) == 0 || len(data) > 128<<10 {
		return nil, errors.New("订阅 TLS 材料过大或不可读")
	}
	return data, nil
}

func launchSubscriptionProcess(ctx context.Context, settings SubscriptionSettings, lookup subscriptionLookup, budget *subscriptionBrokerBudget) (*subscriptionProcess, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("后台必须由 root 启动并降权到专用订阅账号")
	}
	uid, gid, err := subscriptionIdentity()
	if err != nil {
		return nil, err
	}
	boot := subscriptionBootstrap{UID: uid, GID: gid}
	if settings.TLSCertFile != "" {
		boot.Cert, err = readSubscriptionCredential(settings.TLSCertFile)
		if err != nil {
			return nil, err
		}
		boot.Key, err = readSubscriptionCredential(settings.TLSKeyFile)
		if err != nil {
			return nil, err
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, errors.New("无法定位订阅工作进程")
	}
	return spawnSubscriptionProcess(ctx, executable, settings.Listen, boot, lookup, budget)
}

func spawnSubscriptionProcess(ctx context.Context, executable, address string, boot subscriptionBootstrap, lookup subscriptionLookup, budget *subscriptionBrokerBudget) (*subscriptionProcess, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	defer listener.Close()
	listenerFile, err := listener.(*net.TCPListener).File()
	if err != nil {
		return nil, err
	}
	defer listenerFile.Close()
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parentFile := os.NewFile(uintptr(pair[0]), "subscription-parent")
	childFile := os.NewFile(uintptr(pair[1]), "subscription-child")
	defer childFile.Close()
	conn, err := net.FileConn(parentFile)
	_ = parentFile.Close()
	if err != nil {
		return nil, err
	}
	workerCtx, stop := context.WithCancel(ctx)
	cmd := exec.CommandContext(workerCtx, executable, subscriptionWorkerArg)
	cmd.Dir = "/"
	cmd.Env = []string{"TZ=Asia/Shanghai", "GOMEMLIMIT=128MiB"}
	cmd.ExtraFiles = []*os.File{listenerFile, childFile}
	// No database, directory or audit descriptors; no inherited environment or
	// stdout/stderr channels through which HTTP input can enter the root log.
	if err := cmd.Start(); err != nil {
		stop()
		_ = conn.Close()
		return nil, err
	}
	_ = childFile.Close()
	done := make(chan error, 1)
	go func() { err := cmd.Wait(); _ = conn.Close(); done <- err; close(done) }()
	failed := true
	defer func() {
		if failed {
			stop()
			_ = conn.Close()
			<-done
		}
	}()
	_ = conn.SetDeadline(time.Now().Add(subscriptionIPCTimeout))
	encoded, err := json.Marshal(boot)
	if err != nil || writeSubscriptionFrame(conn, encoded, subscriptionMaxBootstrap) != nil {
		return nil, errors.New("订阅进程初始化失败")
	}
	ready, err := readSubscriptionFrame(conn, 1)
	if err != nil || len(ready) != 1 || ready[0] != 1 {
		return nil, errors.New("订阅进程降权或初始化失败；拒绝以 root 提供 HTTP")
	}
	_ = conn.SetDeadline(time.Time{})
	go func() {
		_ = serveSubscriptionBroker(workerCtx, conn, lookup, budget)
		stop() // A corrupt/closed channel terminates the worker, never a fallback.
	}()
	failed = false
	return &subscriptionProcess{Addr: listener.Addr().String(), PID: cmd.Process.Pid, Done: done, stop: stop}, nil
}

func subscriptionPrctlAll(option, value uintptr) error {
	_, _, errno := syscall.AllThreadsSyscall6(unix.SYS_PRCTL, option, value, 0, 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func dropSubscriptionPrivileges(uid, gid int) error {
	if uid <= 0 || gid <= 0 || os.Geteuid() != 0 {
		return errors.New("invalid subscription identity")
	}
	// All Go threads must lose privileges. CGO builds that cannot guarantee
	// this fail closed; production Linux builds already use CGO_ENABLED=0.
	if err := subscriptionPrctlAll(unix.PR_SET_NO_NEW_PRIVS, 1); err != nil {
		return fmt.Errorf("set no-new-privileges: %w", err)
	}
	if err := subscriptionPrctlAll(unix.PR_SET_KEEPCAPS, 0); err != nil {
		return fmt.Errorf("disable keep-caps: %w", err)
	}
	if err := syscall.Setgroups([]int{}); err != nil {
		return fmt.Errorf("clear supplementary groups: %w", err)
	}
	if err := syscall.Setresgid(gid, gid, gid); err != nil {
		return fmt.Errorf("drop group identity: %w", err)
	}
	if err := syscall.Setresuid(uid, uid, uid); err != nil {
		return fmt.Errorf("drop user identity: %w", err)
	}
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	data := [2]unix.CapUserData{}
	if _, _, errno := syscall.AllThreadsSyscall(unix.SYS_CAPSET, uintptr(unsafe.Pointer(&header)), uintptr(unsafe.Pointer(&data[0])), 0); errno != 0 {
		return fmt.Errorf("clear capabilities: %w", errno)
	}
	if err := subscriptionPrctlAll(unix.PR_SET_DUMPABLE, 0); err != nil {
		return fmt.Errorf("disable dumpability: %w", err)
	}
	if os.Getuid() != uid || os.Geteuid() != uid || os.Getgid() != gid || os.Getegid() != gid {
		return errors.New("subscription identity mismatch")
	}
	groups, err := os.Getgroups()
	if err != nil || len(groups) != 0 {
		return errors.New("subscription supplementary groups remain")
	}
	if err := unix.Capget(&header, &data[0]); err != nil {
		return err
	}
	if data != [2]unix.CapUserData{} {
		return errors.New("subscription capabilities remain")
	}
	return nil
}

func runSubscriptionWorker() error {
	// Only inherited descriptors are accepted; command-line paths or account
	// overrides would turn a root bootstrap into a second administration API.
	listenerFile := os.NewFile(3, "subscription-listener")
	ipcFile := os.NewFile(4, "subscription-ipc")
	defer listenerFile.Close()
	defer ipcFile.Close()
	conn, err := net.FileConn(ipcFile)
	if err != nil {
		return errSubscriptionIPC
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(subscriptionIPCTimeout))
	encoded, err := readSubscriptionFrame(conn, subscriptionMaxBootstrap)
	if err != nil {
		return err
	}
	var boot subscriptionBootstrap
	if json.Unmarshal(encoded, &boot) != nil {
		return errSubscriptionIPC
	}
	// No accept, TLS handshake, HTTP parsing, or public input before this point.
	if err := dropSubscriptionPrivileges(boot.UID, boot.GID); err != nil {
		return errors.New("subscription privilege drop failed")
	}
	listener, err := net.FileListener(listenerFile)
	if err != nil {
		return errSubscriptionIPC
	}
	_ = listenerFile.Close()
	defer listener.Close()
	rpc := newSubscriptionRPC(conn)
	server := newSubscriptionHTTPServer(listener.Addr().String(), rpc.lookup)
	go func() {
		<-rpc.failed
		_ = server.Close()
	}()
	if len(boot.Cert) != 0 || len(boot.Key) != 0 {
		pair, err := tls.X509KeyPair(boot.Cert, boot.Key)
		if err != nil {
			return errors.New("subscription TLS unavailable")
		}
		server.TLSConfig.Certificates = []tls.Certificate{pair}
		listener = tls.NewListener(listener, server.TLSConfig)
	}
	clear(boot.Key)
	clear(encoded)
	if err := writeSubscriptionFrame(conn, []byte{1}, 1); err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Time{})
	// Poll only hangup, never consume RPC bytes. Parent death must close an
	// idle listener too, without relying on a client's next subscription query.
	go func() {
		poll := []unix.PollFd{{Fd: int32(ipcFile.Fd()), Events: unix.POLLHUP | unix.POLLRDHUP | unix.POLLERR}}
		for {
			_, err := unix.Poll(poll, 1000)
			if err == unix.EINTR {
				continue
			}
			if err != nil || poll[0].Revents != 0 {
				_ = server.Close()
				return
			}
		}
	}()
	return server.Serve(listener)
}
