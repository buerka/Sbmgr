package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const webWorkerArg = "--web-worker"
const webIPCTimeout = 20 * time.Second

type webRPC struct {
	conn net.Conn
	mu   sync.Mutex
}

func (rpc *webRPC) lookup(ctx context.Context, q webRequest) webReply {
	rpc.mu.Lock()
	defer rpc.mu.Unlock()
	if ctx.Err() != nil {
		return webError(503, "请求已取消")
	}
	_ = rpc.conn.SetDeadline(time.Now().Add(webIPCTimeout))
	data, err := json.Marshal(q)
	if err == nil {
		_, err = rpc.conn.Write([]byte{0x57})
	}
	if err == nil {
		err = writeSubscriptionFrame(rpc.conn, data, webMaxRequest*2)
	}
	if err != nil {
		_ = rpc.conn.Close()
		return webError(503, "管理通道不可用")
	}
	data, err = readSubscriptionFrame(rpc.conn, webMaxReply)
	var r webReply
	if err != nil || json.Unmarshal(data, &r) != nil || r.Status < 200 || r.Status > 599 {
		_ = rpc.conn.Close()
		return webError(503, "管理通道不可用")
	}
	return r
}

func serveWebBroker(ctx context.Context, conn net.Conn, lookup webLookup) error {
	for {
		_ = conn.SetDeadline(time.Time{})
		var lead [1]byte
		if _, err := io.ReadFull(conn, lead[:]); err != nil {
			return err
		}
		if lead[0] != 0x57 {
			return errors.New("invalid web frame")
		}
		_ = conn.SetDeadline(time.Now().Add(webIPCTimeout))
		data, err := readSubscriptionFrame(conn, webMaxRequest*2)
		if err != nil {
			return err
		}
		var q webRequest
		if err := webDecodeFrame(data, &q); err != nil {
			return err
		}
		r := lookup(ctx, q)
		data, err = json.Marshal(r)
		if err != nil {
			return err
		}
		if err := writeSubscriptionFrame(conn, data, webMaxReply); err != nil {
			return err
		}
	}
}
func webDecodeFrame(data []byte, target any) error { return json.Unmarshal(data, target) }

func (a *app) startDaemonWeb(ctx context.Context) error {
	c, err := readWebConfig(a.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	backend := newWebBackend(a, c)
	process, err := launchWebProcess(ctx, c, backend.lookup)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "Web 管理面板：%s\n", c.Origin)
	go func() {
		for {
			select {
			case <-ctx.Done():
				process.stop()
				return
			case <-process.Done:
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			for {
				p, err := launchWebProcess(ctx, c, backend.lookup)
				if err == nil {
					process = p
					break
				}
				fmt.Fprintln(a.err, "Web 工作进程重启失败，将重试")
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
			}
		}
	}()
	return nil
}

func (a *app) serveCmd(args []string) error {
	fs := a.newFlagSet("serve")
	webOnly := fs.Bool("web-only", false, "只启动 Web，适合本机开发；生产使用默认模式同时运行维护")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("serve 不接受位置参数")
	}
	if _, err := readWebConfig(a.statePath); err != nil {
		return errors.New("尚未配置 Web：先执行 sbmgr web configure --password-file <私有密码文件>，再运行 sbmgr serve")
	}
	if !*webOnly {
		return a.daemonCmd(nil)
	}
	if _, err := a.loadCanonicalState(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := a.startDaemonWeb(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}
