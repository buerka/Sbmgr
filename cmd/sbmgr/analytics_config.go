package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

const analyticsServiceTag = "sbmgr-analytics"

type ConnectionAnalyticsSettings struct {
	Enabled bool   `json:"enabled"`
	Listen  string `json:"listen,omitempty"`
	Secret  string `json:"secret,omitempty"`
}

func validateConnectionAnalyticsSettings(settings *ConnectionAnalyticsSettings) error {
	if settings == nil {
		return nil
	}
	if !settings.Enabled && settings.Listen == "" && settings.Secret == "" {
		return nil
	}
	host, portText, err := net.SplitHostPort(settings.Listen)
	if err != nil {
		return errors.New("分析 API 监听必须是本机 IP:端口")
	}
	ip := net.ParseIP(host)
	port, err := strconv.Atoi(portText)
	if ip == nil || !ip.IsLoopback() || err != nil || port < 1 || port > 65535 {
		return errors.New("分析 API 只能监听回环 IP 的有效端口")
	}
	if settings.Enabled && (len(settings.Secret) != 64 || strings.Trim(settings.Secret, "0123456789abcdef") != "") {
		return errors.New("分析 API 密钥无效")
	}
	return nil
}

func (a *app) analyticsCmd(args []string) error {
	if len(args) == 1 && args[0] == "status" {
		s, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		if s.Analytics == nil || !s.Analytics.Enabled {
			fmt.Fprintln(a.out, "连接分析未启用")
			return nil
		}
		fmt.Fprintf(a.out, "连接分析已启用；监听 %s；密钥已配置\n", s.Analytics.Listen)
		return nil
	}
	if len(args) == 0 || args[0] != "configure" {
		return errors.New("用法: admin analytics configure --enabled true|false --listen 127.0.0.1:9191 | status")
	}
	fs := a.newFlagSet("analytics configure")
	enabled := fs.String("enabled", "", "true 或 false")
	listen := fs.String("listen", "", "回环 IP:端口")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || *enabled == "" {
		return errors.New("必须指定 --enabled true|false")
	}
	value, err := strconv.ParseBool(*enabled)
	if err != nil {
		return errors.New("--enabled 必须是 true 或 false")
	}
	return a.withAuditedStateLock("analytics.configure", nil, func() error {
		s, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		if s.Analytics == nil {
			s.Analytics = &ConnectionAnalyticsSettings{}
		}
		p := *s.Analytics
		p.Enabled = value
		if *listen != "" {
			p.Listen = *listen
		}
		if p.Listen == "" {
			p.Listen = "127.0.0.1:9191"
		}
		if p.Secret == "" {
			var raw [32]byte
			if _, err := rand.Read(raw[:]); err != nil {
				return err
			}
			p.Secret = hex.EncodeToString(raw[:])
		}
		if err := validateConnectionAnalyticsSettings(&p); err != nil {
			return err
		}
		s.Analytics = &p
		s.StatsApplyPending = true
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "连接分析设置已保存；应用配置后生效")
		return nil
	})
}

// The base template owns all of its services. Only the reserved tag is managed.
func addAnalyticsService(cfg map[string]any, p *ConnectionAnalyticsSettings) error {
	if p == nil || !p.Enabled {
		return nil
	}
	if err := validateConnectionAnalyticsSettings(p); err != nil {
		return err
	}
	host, portText, _ := net.SplitHostPort(p.Listen)
	port, _ := strconv.Atoi(portText)
	services, _ := cfg["services"].([]any)
	if _, exists := cfg["services"]; exists && services == nil {
		return errors.New("基础模板 services 格式无效")
	}
	for _, item := range services {
		service, ok := item.(map[string]any)
		if !ok {
			return errors.New("基础模板 service 格式无效")
		}
		if service["tag"] == analyticsServiceTag {
			return errors.New("基础模板已使用分析服务标识")
		}
		servicePort := 0
		switch n := service["listen_port"].(type) {
		case float64:
			servicePort = int(n)
		case int:
			servicePort = n
		}
		if service["listen"] == host && servicePort == port {
			return errors.New("基础模板已占用分析 API 监听地址")
		}
	}
	cfg["services"] = append(services, map[string]any{"type": "api", "tag": analyticsServiceTag, "listen": host, "listen_port": port, "secret": p.Secret, "dashboard": false})
	return nil
}
