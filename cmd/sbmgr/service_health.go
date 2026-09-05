package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"time"
)

func runSystemctl(ctx context.Context, w io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	cmd.WaitDelay = time.Second
	cmd.Stdout, cmd.Stderr = w, w
	return cmd.Run()
}

func configuredInboundAddress(s *State) (string, error) {
	if s.ConfigPath == "" {
		return "", nil
	}
	raw, err := os.ReadFile(s.ConfigPath)
	if err != nil {
		return "", errors.New("无法读取已应用配置以检查入站健康")
	}
	var cfg map[string]any
	if json.Unmarshal(raw, &cfg) != nil {
		return "", errors.New("已应用配置无法解析")
	}
	inbounds, _ := cfg["inbounds"].([]any)
	for _, raw := range inbounds {
		in, _ := raw.(map[string]any)
		if stringValue(in["tag"]) != s.InboundTag {
			continue
		}
		port := numericPort(in["listen_port"])
		if port <= 0 {
			return "", errors.New("受管入站没有可检查的监听端口")
		}
		host := stringValue(in["listen"])
		if host == "" || host == "0.0.0.0" {
			host = "127.0.0.1"
		}
		if host == "::" {
			host = "::1"
		}
		return net.JoinHostPort(host, strconv.Itoa(port)), nil
	}
	return "", errors.New("已应用配置缺少受管入站")
}

func reloadAndCheckService(s *State, restart bool, w io.Writer) error {
	if err := validateRuntimeSettings(s); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args := []string{"kill", "-s", "HUP", "--", s.Service}
	if restart {
		args = []string{"restart", "--", s.Service}
	}
	if err := runSystemctl(ctx, w, args...); err != nil {
		return err
	}
	address, err := configuredInboundAddress(s)
	if err != nil {
		return err
	}
	// A successful signal delivery is not successful configuration loading.
	// Require a stable service and a reachable configured inbound before commit.
	stable := 0
	for attempt := 0; attempt < 10; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
		err := runSystemctl(ctx, io.Discard, "is-active", "--quiet", "--", s.Service)
		if err == nil && address != "" {
			dialCtx, done := context.WithTimeout(ctx, 500*time.Millisecond)
			conn, dialErr := (&net.Dialer{}).DialContext(dialCtx, "tcp", address)
			done()
			if conn != nil {
				conn.Close()
			}
			err = dialErr
		}
		if err != nil {
			stable = 0
			continue
		}
		stable++
		if stable == 3 {
			return nil
		}
	}
	return errors.New("sing-box 重载后服务或受管入站健康检查失败")
}
