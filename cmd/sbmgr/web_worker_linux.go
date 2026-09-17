//go:build linux

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

type webBootstrap struct {
	UID      int
	GID      int
	Origin   string
	BasePath string
	Cert     []byte
	Key      []byte
}

func launchWebProcess(ctx context.Context, c webConfig, lookup webLookup) (*subscriptionProcess, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("Linux 管理服务须由 root 启动；HTTP 会在接收请求前降权")
	}
	uid, gid, err := subscriptionIdentity()
	if err != nil {
		return nil, errors.New("请先运行 sbmgr service install，创建 HTTP 服务账号")
	}
	boot := webBootstrap{UID: uid, GID: gid, Origin: c.Origin, BasePath: c.BasePath}
	if c.TLSCert != "" {
		boot.Cert, err = readSubscriptionCredential(c.TLSCert)
		if err != nil {
			return nil, err
		}
		boot.Key, err = readSubscriptionCredential(c.TLSKey)
		if err != nil {
			return nil, err
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return spawnWebProcess(ctx, executable, c.Listen, boot, lookup)
}

func spawnWebProcess(ctx context.Context, executable, address string, boot webBootstrap, lookup webLookup) (*subscriptionProcess, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	defer listener.Close()
	lf, err := listener.(*net.TCPListener).File()
	if err != nil {
		return nil, err
	}
	defer lf.Close()
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	pf, cf := os.NewFile(uintptr(pair[0]), "web-parent"), os.NewFile(uintptr(pair[1]), "web-child")
	defer cf.Close()
	conn, err := net.FileConn(pf)
	_ = pf.Close()
	if err != nil {
		return nil, err
	}
	workerCtx, stop := context.WithCancel(ctx)
	cmd := exec.CommandContext(workerCtx, executable, webWorkerArg)
	cmd.Dir = "/"
	cmd.Env = []string{"GOMEMLIMIT=128MiB"}
	cmd.ExtraFiles = []*os.File{lf, cf}
	if err := cmd.Start(); err != nil {
		stop()
		_ = conn.Close()
		return nil, err
	}
	_ = cf.Close()
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
	data, _ := json.Marshal(boot)
	if writeSubscriptionFrame(conn, data, subscriptionMaxBootstrap) != nil {
		return nil, errors.New("Web 初始化失败")
	}
	ready, err := readSubscriptionFrame(conn, 1)
	if err != nil || len(ready) != 1 || ready[0] != 1 {
		return nil, errors.New("Web 降权或 TLS 初始化失败；未开放 HTTP")
	}
	go func() { _ = serveWebBroker(workerCtx, conn, lookup); stop() }()
	failed = false
	return &subscriptionProcess{Addr: listener.Addr().String(), PID: cmd.Process.Pid, Done: done, stop: stop}, nil
}

func runWebWorker() error {
	lf, cf := os.NewFile(3, "web-listener"), os.NewFile(4, "web-ipc")
	defer lf.Close()
	defer cf.Close()
	conn, err := net.FileConn(cf)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(subscriptionIPCTimeout))
	data, err := readSubscriptionFrame(conn, subscriptionMaxBootstrap)
	if err != nil {
		return err
	}
	var boot webBootstrap
	if json.Unmarshal(data, &boot) != nil {
		return errors.New("invalid web bootstrap")
	}
	if err := dropSubscriptionPrivileges(boot.UID, boot.GID); err != nil {
		return err
	}
	listener, err := net.FileListener(lf)
	if err != nil {
		return err
	}
	_ = lf.Close()
	defer listener.Close()
	rpc := &webRPC{conn: conn}
	server := newWebHTTPServer(listener.Addr().String(), boot.Origin, boot.BasePath, rpc.lookup)
	if len(boot.Cert) != 0 || len(boot.Key) != 0 {
		pair, err := tls.X509KeyPair(boot.Cert, boot.Key)
		if err != nil {
			return errors.New("web TLS unavailable")
		}
		server.TLSConfig.Certificates = []tls.Certificate{pair}
		listener = tls.NewListener(listener, server.TLSConfig)
	}
	clear(boot.Key)
	clear(data)
	if err := writeSubscriptionFrame(conn, []byte{1}, 1); err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Time{})
	go func() {
		poll := []unix.PollFd{{Fd: int32(cf.Fd()), Events: unix.POLLHUP | unix.POLLRDHUP | unix.POLLERR}}
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
