package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type FleetServer struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	User    string `json:"user"`
	KeyPath string `json:"key_path"`
	AppDir  string `json:"app_dir"`
	Enabled bool   `json:"enabled"`
}

type FleetSnapshot struct {
	Hostname        string `json:"hostname"`
	Version         string `json:"version"`
	Users           int    `json:"users"`
	EnabledUsers    int    `json:"enabled_users"`
	Devices         int    `json:"devices"`
	UploadBytes     int64  `json:"upload_bytes"`
	DownloadBytes   int64  `json:"download_bytes"`
	UnreadAlerts    int    `json:"unread_alerts"`
	UnhealthyRoutes int    `json:"unhealthy_outbounds"`
}

type FleetServerStatus struct {
	CheckedAt string        `json:"checked_at"`
	Online    bool          `json:"online"`
	LatencyMS int64         `json:"latency_ms,omitempty"`
	Snapshot  FleetSnapshot `json:"snapshot,omitempty"`
	Error     string        `json:"error,omitempty"`
}

var fleetSafeName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
var fleetSafeHost = regexp.MustCompile(`^[A-Za-z0-9.:-]+$`)
var fleetSafeDirectory = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)

func normalizedFleetServer(server FleetServer) FleetServer {
	if server.Port == 0 {
		server.Port = 22
	}
	if server.User == "" {
		server.User = "root"
	}
	if server.AppDir == "" {
		server.AppDir = "/root/sbmgr"
	}
	return server
}

func validateFleet(s *State) error {
	names := map[string]bool{}
	for _, raw := range s.Fleet {
		server := normalizedFleetServer(raw)
		key := strings.ToLower(server.Name)
		if !fleetSafeName.MatchString(server.Name) || names[key] {
			return fmt.Errorf("多服务器名称 %q 无效或重复", server.Name)
		}
		names[key] = true
		if !fleetSafeHost.MatchString(server.Host) {
			return fmt.Errorf("服务器 %s 的主机地址无效", server.Name)
		}
		if server.Port < 1 || server.Port > 65535 {
			return fmt.Errorf("服务器 %s 的 SSH 端口无效", server.Name)
		}
		if !fleetSafeName.MatchString(server.User) {
			return fmt.Errorf("服务器 %s 的 SSH 用户无效", server.Name)
		}
		if server.KeyPath == "" || !filepath.IsAbs(server.KeyPath) {
			return fmt.Errorf("服务器 %s 的密钥必须使用绝对路径", server.Name)
		}
		if !fleetSafeDirectory.MatchString(server.AppDir) || strings.Contains(server.AppDir, "..") {
			return fmt.Errorf("服务器 %s 的应用目录无效", server.Name)
		}
	}
	return nil
}

func localFleetSnapshot(s *State) FleetSnapshot {
	hostname, _ := os.Hostname()
	snapshot := FleetSnapshot{Hostname: hostname, Version: appVersion, Users: len(s.Users), UnreadAlerts: unreadAlertCount(s)}
	for _, user := range s.Users {
		if user.Enabled {
			snapshot.EnabledUsers++
		}
		snapshot.Devices += len(user.Devices)
		snapshot.UploadBytes += user.Upload
		snapshot.DownloadBytes += user.Download
	}
	for _, status := range s.OutboundHealth {
		if status.CheckedAt != "" && !status.Healthy {
			snapshot.UnhealthyRoutes++
		}
	}
	return snapshot
}

func (a *app) snapshotCmd(args []string) error {
	if len(args) != 0 {
		return errors.New("snapshot 不接受参数")
	}
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	data, err := json.Marshal(localFleetSnapshot(s))
	if err != nil {
		return err
	}
	fmt.Fprintln(a.out, string(data))
	return nil
}

func checkFleetServer(ctx context.Context, server FleetServer) FleetServerStatus {
	server = normalizedFleetServer(server)
	status := FleetServerStatus{CheckedAt: time.Now().Format(time.RFC3339)}
	if _, err := os.Stat(server.KeyPath); err != nil {
		status.Error = "读取 SSH 密钥: " + err.Error()
		return status
	}
	command := server.AppDir + "/sbmgr --state " + server.AppDir + "/state.json admin snapshot"
	started := time.Now()
	cmd := exec.CommandContext(ctx, "ssh", "-p", strconv.Itoa(server.Port), "-i", server.KeyPath,
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=yes",
		server.User+"@"+server.Host, command)
	output, err := cmd.CombinedOutput()
	status.LatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		status.Error = strings.TrimSpace(string(output))
		if status.Error == "" {
			status.Error = err.Error()
		}
		return status
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(output))), &status.Snapshot); err != nil {
		status.Error = "解析远端快照: " + err.Error()
		return status
	}
	status.Online = true
	return status
}

func refreshFleet(s *State, now time.Time) bool {
	if s.FleetStatus == nil {
		s.FleetStatus = map[string]FleetServerStatus{}
	}
	type result struct {
		name   string
		status FleetServerStatus
	}
	results := make(chan result, len(s.Fleet))
	var wg sync.WaitGroup
	for _, server := range s.Fleet {
		if !server.Enabled {
			continue
		}
		wg.Add(1)
		go func(server FleetServer) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			status := checkFleetServer(ctx, server)
			status.CheckedAt = now.Format(time.RFC3339)
			results <- result{name: server.Name, status: status}
		}(server)
	}
	wg.Wait()
	close(results)
	changed := false
	for item := range results {
		previous := s.FleetStatus[item.name]
		s.FleetStatus[item.name] = item.status
		changed = true
		if previous.CheckedAt != "" && previous.Online && !item.status.Online {
			appendAlert(s, Alert{At: now.Format(time.RFC3339), Kind: "fleet_offline", Message: fmt.Sprintf("服务器 %s 已离线：%s", item.name, item.status.Error)})
		} else if previous.CheckedAt != "" && !previous.Online && item.status.Online {
			appendAlert(s, Alert{At: now.Format(time.RFC3339), Kind: "fleet_recovered", Message: fmt.Sprintf("服务器 %s 已恢复，版本 %s", item.name, item.status.Snapshot.Version)})
		}
	}
	return changed
}

func fleetCheckDue(s *State, now time.Time) bool {
	for _, server := range s.Fleet {
		if !server.Enabled {
			continue
		}
		status := s.FleetStatus[server.Name]
		checked, err := time.Parse(time.RFC3339, status.CheckedAt)
		if err != nil || now.Sub(checked) >= 5*time.Minute {
			return true
		}
	}
	return false
}

func (a *app) fleetCmd(args []string) error {
	return a.withAuditedStateLock(auditAction("fleet", args), args, func() error { return a.fleetCmdLocked(args) })
}

func (a *app) fleetCmdLocked(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: sbmgr admin fleet list|add|remove|check")
	}
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		servers := append([]FleetServer(nil), s.Fleet...)
		sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
		fmt.Fprintln(a.out, "NAME\tSTATUS\tHOST\tVERSION\tUSERS\tTRAFFIC\tALERTS\tCHECKED")
		for _, server := range servers {
			status := s.FleetStatus[server.Name]
			label := "unknown"
			if status.Online {
				label = "online"
			} else if status.CheckedAt != "" {
				label = "offline"
			}
			fmt.Fprintf(a.out, "%s\t%s\t%s:%d\t%s\t%d\t%s\t%d\t%s\n", server.Name, label, server.Host, normalizedFleetServer(server).Port, dash(status.Snapshot.Version), status.Snapshot.Users, formatSize(status.Snapshot.UploadBytes+status.Snapshot.DownloadBytes), status.Snapshot.UnreadAlerts, dash(status.CheckedAt))
		}
		return nil
	case "add":
		fs := a.newFlagSet("fleet add")
		name := fs.String("name", "", "服务器显示名称")
		host := fs.String("host", "", "SSH 主机")
		port := fs.Int("port", 22, "SSH 端口")
		user := fs.String("user", "root", "SSH 用户")
		keyPath := fs.String("key", "", "SSH 私钥绝对路径")
		appDir := fs.String("app-dir", "/root/sbmgr", "远端 sbmgr 目录")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		server := FleetServer{Name: strings.TrimSpace(*name), Host: strings.TrimSpace(*host), Port: *port, User: strings.TrimSpace(*user), KeyPath: filepath.Clean(*keyPath), AppDir: strings.TrimRight(*appDir, "/"), Enabled: true}
		s.Fleet = append(s.Fleet, server)
		if err := validateFleet(s); err != nil {
			return err
		}
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "已添加服务器 %s；首次检查要求主机密钥已存在于 known_hosts\n", server.Name)
		return nil
	case "remove":
		if len(args) != 2 {
			return errors.New("用法: sbmgr admin fleet remove NAME")
		}
		index := -1
		for i := range s.Fleet {
			if strings.EqualFold(s.Fleet[i].Name, args[1]) {
				index = i
				break
			}
		}
		if index < 0 {
			return fmt.Errorf("服务器 %q 不存在", args[1])
		}
		name := s.Fleet[index].Name
		s.Fleet = append(s.Fleet[:index], s.Fleet[index+1:]...)
		delete(s.FleetStatus, name)
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "已移除服务器 %s\n", name)
		return nil
	case "check":
		refreshFleet(s, time.Now())
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "已检查 %d 台远端服务器\n", len(s.Fleet))
		return nil
	default:
		return fmt.Errorf("未知 fleet 子命令 %q", args[0])
	}
}
