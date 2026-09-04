package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
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
var fleetSafeUser = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)
var fleetSafeDirectory = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)

const (
	fleetMaxStdoutBytes  = 64 << 10
	fleetMaxStderrBytes  = 16 << 10
	fleetMaxErrorBytes   = 2 << 10
	fleetMaxHostnameSize = 255
	fleetMaxVersionSize  = 128
)

// fleetLimitedBuffer keeps remote output memory-bounded while still reporting
// the full write to os/exec. The caller rejects the result when overflowed.
type fleetLimitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func newFleetLimitedBuffer(limit int) *fleetLimitedBuffer {
	return &fleetLimitedBuffer{limit: limit}
}

func (b *fleetLimitedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = b.buffer.Write(p[:remaining])
	}
	if remaining < len(p) {
		b.overflow = true
	}
	return written, nil
}

func (b *fleetLimitedBuffer) Bytes() []byte {
	return b.buffer.Bytes()
}

func normalizeFleetAppDir(appDir string) string {
	// Preserve the POSIX root so it is rejected as an unsafe application
	// directory rather than silently becoming the empty/$HOME default.
	for len(appDir) > 1 && strings.HasSuffix(appDir, "/") {
		appDir = strings.TrimSuffix(appDir, "/")
	}
	return appDir
}

func normalizedFleetServer(server FleetServer) FleetServer {
	if server.Port == 0 {
		server.Port = 22
	}
	if server.User == "" {
		server.User = "root"
	}
	server.AppDir = normalizeFleetAppDir(server.AppDir)
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
		// Even as one argv element, an ssh destination beginning with '-' is
		// parsed as an option. Requiring a safe first username character closes
		// that option-injection edge case.
		if !fleetSafeUser.MatchString(server.User) {
			return fmt.Errorf("服务器 %s 的 SSH 用户无效", server.Name)
		}
		if server.KeyPath == "" || !filepath.IsAbs(server.KeyPath) {
			return fmt.Errorf("服务器 %s 的密钥必须使用绝对路径", server.Name)
		}
		if server.AppDir != "" {
			if server.AppDir == "/" || !path.IsAbs(server.AppDir) || !fleetSafeDirectory.MatchString(server.AppDir) || path.Clean(server.AppDir) != server.AppDir {
				return fmt.Errorf("服务器 %s 的应用目录必须是安全的 POSIX 绝对路径，且不能是根目录", server.Name)
			}
		}
	}
	return nil
}

func validateFleetSnapshot(snapshot FleetSnapshot) error {
	if err := validateFleetSnapshotText("hostname", snapshot.Hostname, fleetMaxHostnameSize); err != nil {
		return err
	}
	if err := validateFleetSnapshotText("version", snapshot.Version, fleetMaxVersionSize); err != nil {
		return err
	}
	counts := []struct {
		name  string
		value int64
	}{
		{"users", int64(snapshot.Users)},
		{"enabled_users", int64(snapshot.EnabledUsers)},
		{"devices", int64(snapshot.Devices)},
		{"upload_bytes", snapshot.UploadBytes},
		{"download_bytes", snapshot.DownloadBytes},
		{"unread_alerts", int64(snapshot.UnreadAlerts)},
		{"unhealthy_outbounds", int64(snapshot.UnhealthyRoutes)},
	}
	for _, count := range counts {
		if count.value < 0 {
			return fmt.Errorf("远端快照字段 %s 不能为负数", count.name)
		}
	}
	if snapshot.EnabledUsers > snapshot.Users {
		return errors.New("远端快照的启用用户数不能大于用户总数")
	}
	if snapshot.UploadBytes > math.MaxInt64-snapshot.DownloadBytes {
		return errors.New("远端快照的流量总数溢出")
	}
	return nil
}

func validateFleetSnapshotText(name, value string, maxBytes int) error {
	if value == "" {
		return fmt.Errorf("远端快照字段 %s 不能为空", name)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("远端快照字段 %s 超过 %d 字节", name, maxBytes)
	}
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("远端快照字段 %s 格式无效", name)
	}
	for _, r := range value {
		if !unicode.IsPrint(r) {
			return fmt.Errorf("远端快照字段 %s 含有控制字符", name)
		}
	}
	return nil
}

func decodeFleetSnapshot(raw []byte) (FleetSnapshot, error) {
	var snapshot FleetSnapshot
	if len(raw) > fleetMaxStdoutBytes {
		return snapshot, errors.New("远端标准输出超过安全上限")
	}
	if !utf8.Valid(raw) {
		return snapshot, errors.New("远端快照不是有效的 UTF-8")
	}
	for _, r := range string(raw) {
		if (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || (r >= 0x7f && r <= 0x9f) {
			return snapshot, errors.New("远端快照包含终端控制字符")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return snapshot, fmt.Errorf("JSON 无效: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return snapshot, errors.New("JSON 后存在多余数据")
		}
		return snapshot, fmt.Errorf("JSON 尾部无效: %w", err)
	}
	if err := validateFleetSnapshot(snapshot); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func sanitizeFleetDiagnostic(value string) string {
	runes := []rune(strings.ToValidUTF8(value, ""))
	var cleaned strings.Builder
	for index := 0; index < len(runes); {
		r := runes[index]
		switch r {
		case '\x1b':
			index = skipFleetEscapeSequence(runes, index+1)
			continue
		case '\u009b':
			index = skipFleetCSI(runes, index+1)
			continue
		case '\u009d':
			index = skipFleetOSC(runes, index+1)
			continue
		}
		index++
		if unicode.IsPrint(r) {
			cleaned.WriteRune(r)
		} else if unicode.IsSpace(r) {
			cleaned.WriteByte(' ')
		}
	}
	result := strings.Join(strings.Fields(cleaned.String()), " ")
	if len(result) <= fleetMaxErrorBytes {
		return result
	}
	cut := fleetMaxErrorBytes - len("…")
	for cut > 0 && !utf8.ValidString(result[:cut]) {
		cut--
	}
	return result[:cut] + "…"
}

func skipFleetEscapeSequence(runes []rune, index int) int {
	if index >= len(runes) {
		return index
	}
	switch runes[index] {
	case '[':
		return skipFleetCSI(runes, index+1)
	case ']':
		return skipFleetOSC(runes, index+1)
	default:
		// Other ANSI escape forms are two-rune sequences.
		return index + 1
	}
}

func skipFleetCSI(runes []rune, index int) int {
	for index < len(runes) {
		r := runes[index]
		index++
		if r >= 0x40 && r <= 0x7e {
			break
		}
	}
	return index
}

func skipFleetOSC(runes []rune, index int) int {
	for index < len(runes) {
		if runes[index] == '\a' || runes[index] == '\u009c' {
			return index + 1
		}
		if runes[index] == '\x1b' && index+1 < len(runes) && runes[index+1] == '\\' {
			return index + 2
		}
		index++
	}
	return index
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

func fleetSnapshotCommand(server FleetServer) string {
	server = normalizedFleetServer(server)
	if server.AppDir == "" {
		return `SBMGR_HOME="$HOME/sbmgr" "$HOME/sbmgr/sbmgr" admin snapshot`
	}
	return "SBMGR_HOME=" + server.AppDir + " " + server.AppDir + "/sbmgr admin snapshot"
}

func fleetSSHArgs(server FleetServer, command string) []string {
	return []string{
		"-p", strconv.Itoa(server.Port),
		"-i", server.KeyPath,
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "IdentitiesOnly=yes",
		"-o", "PasswordAuthentication=no",
		"-o", "KbdInteractiveAuthentication=no",
		"-o", "ClearAllForwardings=yes",
		"-o", "RequestTTY=no",
		server.User + "@" + server.Host,
		command,
	}
}

func (a *app) snapshotCmd(args []string) error {
	if len(args) != 0 {
		return errors.New("snapshot 不接受参数")
	}
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	snapshot := localFleetSnapshot(s)
	if err := validateFleetSnapshot(snapshot); err != nil {
		return err
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.out, string(data))
	return nil
}

func checkFleetServer(ctx context.Context, server FleetServer) FleetServerStatus {
	server = normalizedFleetServer(server)
	status := FleetServerStatus{CheckedAt: time.Now().Format(time.RFC3339)}
	if err := validateFleet(&State{Fleet: []FleetServer{server}}); err != nil {
		status.Error = "服务器配置无效"
		return status
	}
	if _, err := os.Stat(server.KeyPath); err != nil {
		status.Error = sanitizeFleetDiagnostic("读取 SSH 密钥: " + err.Error())
		return status
	}
	// An empty AppDir follows the remote login user's home instead of assuming
	// that every server is administered as root. Explicit directories have
	// already passed the restrictive validation above and contain no shell
	// metacharacters.
	command := fleetSnapshotCommand(server)
	started := time.Now()
	cmd := exec.CommandContext(ctx, "ssh", fleetSSHArgs(server, command)...)
	stdout := newFleetLimitedBuffer(fleetMaxStdoutBytes)
	stderr := newFleetLimitedBuffer(fleetMaxStderrBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	status.LatencyMS = time.Since(started).Milliseconds()
	if stdout.overflow {
		status.Error = "远端标准输出超过安全上限"
		return status
	}
	if stderr.overflow {
		status.Error = "远端标准错误输出超过安全上限"
		return status
	}
	if err != nil {
		// stderr is diagnostic only. Never mix failed-command stdout into the
		// stored error because it may contain a partial snapshot or other data.
		status.Error = sanitizeFleetDiagnostic(string(stderr.Bytes()))
		if status.Error == "" {
			status.Error = sanitizeFleetDiagnostic(err.Error())
		}
		return status
	}
	snapshot, err := decodeFleetSnapshot(stdout.Bytes())
	if err != nil {
		status.Error = sanitizeFleetDiagnostic("解析远端快照: " + err.Error())
		return status
	}
	status.Snapshot = snapshot
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
		appDir := fs.String("app-dir", "", "远端 sbmgr 目录；留空使用该 SSH 用户的 ~/sbmgr")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		server := FleetServer{Name: strings.TrimSpace(*name), Host: strings.TrimSpace(*host), Port: *port, User: strings.TrimSpace(*user), KeyPath: filepath.Clean(*keyPath), AppDir: normalizeFleetAppDir(strings.TrimSpace(*appDir)), Enabled: true}
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
