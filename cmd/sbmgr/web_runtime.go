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

const webIPCConcurrency = 8

type webRPCRequest struct {
	ID      uint64     `json:"id"`
	Request webRequest `json:"request"`
}
type webRPCReply struct {
	ID    uint64   `json:"id"`
	Reply webReply `json:"reply"`
}
type webRPC struct {
	conn    net.Conn
	once    sync.Once
	mu      sync.Mutex
	writeMu sync.Mutex
	next    uint64
	pending map[uint64]chan webReply
	closed  bool
}

func (rpc *webRPC) fail() {
	rpc.mu.Lock()
	defer rpc.mu.Unlock()
	rpc.closed = true
	for id, ch := range rpc.pending {
		ch <- webError(503, "管理通道已中断；已提交的修改请查询任务结果，不要重复提交")
		delete(rpc.pending, id)
	}
	_ = rpc.conn.Close()
}
func (rpc *webRPC) readReplies() {
	defer rpc.fail()
	for {
		data, err := readSubscriptionFrame(rpc.conn, webMaxReply)
		var frame webRPCReply
		if err != nil || json.Unmarshal(data, &frame) != nil || frame.ID == 0 || frame.Reply.Status < 200 || frame.Reply.Status > 599 {
			return
		}
		rpc.mu.Lock()
		ch := rpc.pending[frame.ID]
		delete(rpc.pending, frame.ID)
		if ch != nil {
			ch <- frame.Reply
		}
		rpc.mu.Unlock()
	}
}
func (rpc *webRPC) lookup(ctx context.Context, q webRequest) webReply {
	rpc.once.Do(func() { rpc.pending = make(map[uint64]chan webReply); go rpc.readReplies() })
	ctx, cancel := context.WithTimeout(ctx, webIPCTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return webError(503, "请求已取消；本次尚未发送")
	}
	rpc.mu.Lock()
	if rpc.closed {
		rpc.mu.Unlock()
		return webError(503, "管理服务正在恢复，请稍后查询任务结果")
	}
	if len(rpc.pending) >= webIPCConcurrency {
		rpc.mu.Unlock()
		return webError(429, "管理请求过多，请稍后重试；本次尚未发送")
	}
	rpc.next++
	id := rpc.next
	ch := make(chan webReply, 1)
	rpc.pending[id] = ch
	rpc.mu.Unlock()
	defer func() { rpc.mu.Lock(); delete(rpc.pending, id); rpc.mu.Unlock() }()
	data, err := json.Marshal(webRPCRequest{ID: id, Request: q})
	if err != nil {
		return webError(400, "请求格式不正确")
	}
	rpc.writeMu.Lock()
	if ctx.Err() != nil {
		rpc.writeMu.Unlock()
		return webError(503, "请求已取消；本次尚未发送")
	}
	deadline, _ := ctx.Deadline()
	_ = rpc.conn.SetWriteDeadline(deadline)
	_, err = rpc.conn.Write([]byte{0x57})
	if err == nil {
		err = writeSubscriptionFrame(rpc.conn, data, webMaxRequest*2)
	}
	rpc.writeMu.Unlock()
	if err != nil {
		rpc.fail()
		return webError(503, "管理通道已中断，请查询任务结果确认是否已受理")
	}
	select {
	case reply := <-ch:
		return reply
	case <-ctx.Done():
		// Keep reading the stream. A late reply belongs only to its request ID,
		// never to the next caller; one slow lookup cannot poison the IPC channel.
		return webError(504, "等待响应超时；修改可能已受理，请查询任务结果，不要重复提交")
	}
}

func serveWebBroker(ctx context.Context, conn net.Conn, lookup webLookup) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer conn.Close()
	_ = conn.SetDeadline(time.Time{})
	slots := make(chan struct{}, webIPCConcurrency)
	var writes sync.Mutex
	var activeMu sync.Mutex
	active := map[uint64]bool{}
	send := func(id uint64, reply webReply) error {
		data, err := json.Marshal(webRPCReply{ID: id, Reply: reply})
		if err != nil {
			return err
		}
		writes.Lock()
		defer writes.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(webIPCTimeout))
		return writeSubscriptionFrame(conn, data, webMaxReply)
	}
	for {
		_ = conn.SetReadDeadline(time.Time{})
		var lead [1]byte
		if _, err := io.ReadFull(conn, lead[:]); err != nil {
			return err
		}
		if lead[0] != 0x57 {
			return errors.New("invalid web frame")
		}
		_ = conn.SetReadDeadline(time.Now().Add(webIPCTimeout))
		data, err := readSubscriptionFrame(conn, webMaxRequest*2)
		if err != nil {
			return err
		}
		var frame webRPCRequest
		if webDecodeFrame(data, &frame) != nil || frame.ID == 0 {
			return errors.New("invalid web frame")
		}
		activeMu.Lock()
		duplicate := active[frame.ID]
		if !duplicate {
			active[frame.ID] = true
		}
		activeMu.Unlock()
		if duplicate {
			return errors.New("duplicate web frame")
		}
		select {
		case slots <- struct{}{}:
			go func(frame webRPCRequest) {
				defer func() { <-slots; activeMu.Lock(); delete(active, frame.ID); activeMu.Unlock() }()
				requestCtx, stop := context.WithTimeout(ctx, webIPCTimeout)
				defer stop()
				reply := lookup(requestCtx, frame.Request)
				if err := send(frame.ID, reply); err != nil {
					_ = conn.Close()
				}
			}(frame)
		default:
			activeMu.Lock()
			delete(active, frame.ID)
			activeMu.Unlock()
			if err := send(frame.ID, webError(429, "管理请求过多，请稍后重试；本次尚未执行")); err != nil {
				return err
			}
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
