package main

import (
	"bufio"
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const stateVersion = 9

// These values are display/build metadata only; release builds inject values
// from Git with -ldflags and the application never manages its own binary.
var (
	appVersion = "0.23.1-dev"
	gitCommit  = "unknown"
)

type State struct {
	Version           int                          `json:"version"`
	BaseConfig        string                       `json:"base_config"`
	ConfigPath        string                       `json:"config_path"`
	InboundTag        string                       `json:"inbound_tag"`
	SingBoxBin        string                       `json:"sing_box_bin"`
	Service           string                       `json:"service"`
	StatsAPI          string                       `json:"stats_api,omitempty"`
	Counters          map[string]int64             `json:"stats_counters,omitempty"`
	JournalCursor     string                       `json:"journal_cursor,omitempty"`
	PendingSources    map[string]PendingSource     `json:"pending_sources,omitempty"`
	IPApplyPending    bool                         `json:"ip_apply_pending,omitempty"`
	BurstApplyPending bool                         `json:"burst_apply_pending,omitempty"`
	RateApplyPending  bool                         `json:"rate_apply_pending,omitempty"`
	StatsApplyPending bool                         `json:"stats_apply_pending,omitempty"`
	ActiveConnections map[string]ActiveConnection  `json:"active_connections,omitempty"`
	Health            HealthSettings               `json:"health,omitempty"`
	OutboundHealth    map[string]OutboundHealth    `json:"outbound_health,omitempty"`
	LastHealthCheck   string                       `json:"last_health_check,omitempty"`
	Notifications     NotificationSettings         `json:"notifications,omitempty"`
	Subscription      SubscriptionSettings         `json:"subscription,omitempty"`
	Fleet             []FleetServer                `json:"fleet,omitempty"`
	FleetStatus       map[string]FleetServerStatus `json:"fleet_status,omitempty"`
	Alerts            []Alert                      `json:"alerts,omitempty"`
	ReservedAuthUsers []string                     `json:"reserved_auth_users,omitempty"`
	Client            ClientSettings               `json:"client"`
	Users             []User                       `json:"users"`
}

type ClientSettings struct {
	Server         string `json:"server"`
	Port           int    `json:"port"`
	ServerName     string `json:"server_name"`
	PublicKey      string `json:"reality_public_key"`
	ShortID        string `json:"short_id"`
	MihomoTemplate string `json:"mihomo_template,omitempty"`
}

type User struct {
	Name                string                  `json:"name"`
	Enabled             bool                    `json:"enabled"`
	QuotaBytes          int64                   `json:"quota_bytes"`
	QuotaMode           string                  `json:"quota_mode"`
	ExtraQuotaBytes     int64                   `json:"extra_quota_bytes,omitempty"`
	Expires             string                  `json:"expires,omitempty"`
	Upload              int64                   `json:"upload_bytes"`
	Download            int64                   `json:"download_bytes"`
	UploadMbps          float64                 `json:"upload_mbps,omitempty"`
	DownloadMbps        float64                 `json:"download_mbps,omitempty"`
	RateMark            uint32                  `json:"rate_mark,omitempty"`
	Throttle            ThrottlePolicy          `json:"throttle,omitempty"`
	Burst               BurstPolicy             `json:"burst_protection,omitempty"`
	IPPolicy            IPPolicy                `json:"ip_policy,omitempty"`
	SourceIPs           map[string]SourceIPStat `json:"source_ips,omitempty"`
	Devices             []Device                `json:"devices,omitempty"`
	TrafficSamples      []TrafficSample         `json:"traffic_samples,omitempty"`
	UsageHistory        []UsagePoint            `json:"usage_history,omitempty"`
	CurrentUploadMbps   float64                 `json:"current_upload_mbps,omitempty"`
	CurrentDownloadMbps float64                 `json:"current_download_mbps,omitempty"`
	BlockedUntil        string                  `json:"blocked_until,omitempty"`
	BlockReason         string                  `json:"block_reason,omitempty"`
	DisabledReason      string                  `json:"disabled_reason,omitempty"`
	Billing             BillingPolicy           `json:"billing,omitempty"`
	BillingHistory      []BillingRecord         `json:"billing_history,omitempty"`
	QuotaAlertStage     int                     `json:"quota_alert_stage,omitempty"`
	ExpiryAlertStage    int                     `json:"expiry_alert_stage,omitempty"`
	Access              AccessPolicy            `json:"access_policy,omitempty"`
	RecentAccesses      []RecentAccess          `json:"recent_accesses,omitempty"`
	Nodes               []Node                  `json:"nodes"`
}

type Node struct {
	Name                string                `json:"name"`
	Device              string                `json:"device,omitempty"`
	AuthUser            string                `json:"auth_user"`
	UUID                string                `json:"uuid"`
	Outbound            string                `json:"outbound,omitempty"`
	UploadMbps          float64               `json:"upload_mbps,omitempty"`
	DownloadMbps        float64               `json:"download_mbps,omitempty"`
	RateMark            uint32                `json:"rate_mark,omitempty"`
	Upload              int64                 `json:"upload_bytes,omitempty"`
	Download            int64                 `json:"download_bytes,omitempty"`
	Destinations        map[string]AccessStat `json:"destinations,omitempty"`
	CurrentUploadMbps   float64               `json:"current_upload_mbps,omitempty"`
	CurrentDownloadMbps float64               `json:"current_download_mbps,omitempty"`
	RateUpdatedAt       string                `json:"rate_updated_at,omitempty"`
}

type ThrottlePolicy struct {
	Enabled    bool    `json:"enabled,omitempty"`
	Tier1Usage float64 `json:"tier1_usage_percent,omitempty"`
	Tier1Speed float64 `json:"tier1_speed_percent,omitempty"`
	Tier2Usage float64 `json:"tier2_usage_percent,omitempty"`
	Tier2Speed float64 `json:"tier2_speed_percent,omitempty"`
}

type AccessStat struct {
	Count    int64  `json:"count"`
	LastSeen string `json:"last_seen"`
}

type RecentAccess struct {
	Target    string `json:"target"`
	Device    string `json:"device,omitempty"`
	Node      string `json:"node,omitempty"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
	Count     int64  `json:"count"`
}

type BurstPolicy struct {
	Enabled          bool    `json:"enabled,omitempty"`
	WindowMinutes    int     `json:"window_minutes,omitempty"`
	LimitBytes       int64   `json:"limit_bytes,omitempty"`
	BlockMinutes     int     `json:"block_minutes,omitempty"`
	Action           string  `json:"action,omitempty"`
	SoftUploadKbps   float64 `json:"soft_upload_kbps,omitempty"`
	SoftDownloadKbps float64 `json:"soft_download_kbps,omitempty"`
}

type TrafficSample struct {
	At    string `json:"at"`
	Bytes int64  `json:"bytes"`
}

type UsagePoint struct {
	At            string  `json:"at"`
	UploadBytes   int64   `json:"upload_bytes"`
	DownloadBytes int64   `json:"download_bytes"`
	UploadMbps    float64 `json:"upload_mbps"`
	DownloadMbps  float64 `json:"download_mbps"`
}

type ActiveConnection struct {
	ID        string `json:"id"`
	User      string `json:"user"`
	Device    string `json:"device"`
	Node      string `json:"node"`
	AuthUser  string `json:"auth_user"`
	SourceIP  string `json:"source_ip,omitempty"`
	Target    string `json:"target,omitempty"`
	StartedAt string `json:"started_at"`
	LastSeen  string `json:"last_seen"`
}

type BillingPolicy struct {
	Enabled   bool   `json:"enabled,omitempty"`
	CycleDay  int    `json:"cycle_day,omitempty"`
	TimeZone  string `json:"time_zone,omitempty"`
	LastReset string `json:"last_reset,omitempty"`
	NextReset string `json:"next_reset,omitempty"`
}

type BillingRecord struct {
	StartedAt     string `json:"started_at"`
	EndedAt       string `json:"ended_at"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
	QuotaBytes    int64  `json:"quota_bytes"`
}

type Alert struct {
	At                string `json:"at"`
	User              string `json:"user"`
	Kind              string `json:"kind"`
	Message           string `json:"message"`
	Acknowledged      bool   `json:"acknowledged,omitempty"`
	NotifiedAt        string `json:"notified_at,omitempty"`
	NotifyAttempts    int    `json:"notify_attempts,omitempty"`
	LastNotifyAttempt string `json:"last_notify_attempt,omitempty"`
	NotifyError       string `json:"notify_error,omitempty"`
}

type HealthSettings struct {
	Mode               string            `json:"mode,omitempty"`
	IntervalMinutes    int               `json:"interval_minutes,omitempty"`
	TimeoutSeconds     int               `json:"timeout_seconds,omitempty"`
	AlertAfterFailures int               `json:"alert_after_failures,omitempty"`
	Targets            map[string]string `json:"targets,omitempty"`
}

type OutboundHealth struct {
	Tag       string `json:"tag"`
	Target    string `json:"target"`
	Healthy   bool   `json:"healthy"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Failures  int    `json:"consecutive_failures,omitempty"`
	CheckedAt string `json:"checked_at"`
	Error     string `json:"error,omitempty"`
}

type NotificationSettings struct {
	WebhookURL     string `json:"webhook_url,omitempty"`
	WebhookSecret  string `json:"webhook_secret,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type AccessPolicy struct {
	AllowedDomains      []string `json:"allowed_domains,omitempty"`
	BlockedDomains      []string `json:"blocked_domains,omitempty"`
	BlockedPorts        []int    `json:"blocked_ports,omitempty"`
	MaxConnections      int      `json:"max_connections,omitempty"`
	ConnectionAction    string   `json:"connection_action,omitempty"`
	LastConnectionAlert string   `json:"last_connection_alert,omitempty"`
}

type IPPolicy struct {
	Enabled         bool     `json:"enabled,omitempty"`
	Mode            string   `json:"mode,omitempty"`
	Binding         string   `json:"binding,omitempty"`
	MaxIPs          int      `json:"max_ips,omitempty"`
	HandoverSeconds int      `json:"handover_seconds,omitempty"`
	BoundIPs        []string `json:"bound_ips,omitempty"`
	TemporaryIPs    []string `json:"temporary_ips,omitempty"`
	TemporaryUntil  string   `json:"temporary_until,omitempty"`
}

type SourceIPStat struct {
	Count      int64  `json:"count"`
	Violations int64  `json:"violations,omitempty"`
	FirstSeen  string `json:"first_seen"`
	LastSeen   string `json:"last_seen"`
	LastNode   string `json:"last_node,omitempty"`
	LastAlert  string `json:"last_alert,omitempty"`
}

type PendingSource struct {
	IP string `json:"ip"`
	At string `json:"at"`
}

type Device struct {
	Name              string                  `json:"name"`
	Enabled           bool                    `json:"enabled"`
	CreatedAt         string                  `json:"created_at,omitempty"`
	LastSeen          string                  `json:"last_seen,omitempty"`
	IPPolicy          IPPolicy                `json:"ip_policy,omitempty"`
	SourceIPs         map[string]SourceIPStat `json:"source_ips,omitempty"`
	SubscriptionToken string                  `json:"subscription_token,omitempty"`
	Access            AccessPolicy            `json:"access_policy,omitempty"`
}

type NodeTemplate struct {
	Name     string
	Outbound string
}

type app struct {
	statePath          string
	out                io.Writer
	err                io.Writer
	stateLockHeld      bool
	lastNftUsageSample time.Time
	lastStatsSample    time.Time
}

func (a *app) newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.err)
	return fs
}

func main() {
	a := &app{out: os.Stdout, err: os.Stderr}
	if err := a.run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func (a *app) run(args []string) error {
	global := flag.NewFlagSet("sbmgr", flag.ContinueOnError)
	global.SetOutput(a.err)
	global.StringVar(&a.statePath, "state", defaultStatePath(), "状态数据库路径（.db；旧 .json 仅兼容迁移）")
	global.Usage = func() { usage(a.out) }
	if err := global.Parse(args); err != nil {
		return err
	}
	args = global.Args()
	if len(args) == 0 {
		if _, err := a.loadCanonicalState(); err != nil {
			return err
		}
		return a.menu()
	}
	switch args[0] {
	case "ui", "menu":
		if _, err := a.loadCanonicalState(); err != nil {
			return err
		}
		return a.menu()
	case "daemon":
		return a.daemonCmd(args[1:])
	case "admin":
		return a.adminCmd(args[1:])
	case "version":
		if len(args) == 2 && args[1] == "--verbose" {
			fmt.Fprintf(a.out, "sbmgr %s\ncommit %s\n", appVersion, gitCommit)
			return nil
		}
		if len(args) != 1 {
			return errors.New("用法: sbmgr version [--verbose]")
		}
		fmt.Fprintf(a.out, "sbmgr %s\n", appVersion)
		return nil
	case "help", "-h", "--help":
		usage(a.out)
		return nil
	default:
		return fmt.Errorf("未知命令 %q；运行 sbmgr help 查看帮助", args[0])
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `sbmgr - sing-box 多用户管理器

用法:
	  sbmgr             打开管理界面（日常只需这个）
	  sbmgr version     显示版本
	  sbmgr version --verbose  显示版本与 Git commit
	  sbmgr help        显示本帮助

后台维护由 sbmgr.service 自动完成。`)
}

// adminCmd keeps recovery and automation operations available without turning
// the normal user experience into a wall of command-line switches.
func (a *app) adminCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("缺少维护操作；这些命令仅用于安装、恢复和调试")
	}
	switch args[0] {
	case "init":
		return a.withStateLock(func() error { return a.initCmd(args[1:]) })
	case "user":
		return a.userCmd(args[1:])
	case "node":
		return a.nodeCmd(args[1:])
	case "device":
		return a.deviceCmd(args[1:])
	case "traffic":
		return a.trafficCmd(args[1:])
	case "rate":
		return a.rateCmd(args[1:])
	case "sync":
		return a.syncCmd(args[1:])
	case "render":
		return a.renderCmd(args[1:])
	case "check":
		return a.checkCmd(args[1:])
	case "apply":
		return a.applyCmd(args[1:])
	case "export":
		return a.exportCmd(args[1:])
	case "enforce":
		return a.enforceCmd(args[1:])
	case "backup":
		return a.backupCmd(args[1:])
	case "health":
		return a.healthCmd(args[1:])
	case "subscription":
		if _, err := a.loadCanonicalState(); err != nil {
			return err
		}
		return a.subscriptionCmd(args[1:])
	case "template":
		return a.templateCmd(args[1:])
	case "policy":
		return a.policyCmd(args[1:])
	case "fleet":
		return a.fleetCmd(args[1:])
	case "snapshot":
		return a.snapshotCmd(args[1:])
	case "proxy":
		return a.proxyAdminCmd(args[1:])
	case "simple-menu":
		return a.simpleMenu()
	default:
		return fmt.Errorf("未知维护操作 %q", args[0])
	}
}

func (a *app) initCmd(args []string) error {
	fs := a.newFlagSet("init")
	config := fs.String("config", "", "现有 sing-box 配置")
	base := fs.String("base", "", "生成的基础模板路径")
	inbound := fs.String("inbound", "vless-in", "受管 VLESS 入站 tag")
	server := fs.String("server", "", "客户端连接域名或 IP")
	port := fs.Int("port", 0, "客户端连接端口（默认从入站读取）")
	serverName := fs.String("server-name", "", "Reality servername（默认从入站读取）")
	publicKey := fs.String("public-key", "", "Reality 公钥，用于客户端导出")
	shortID := fs.String("short-id", "", "Reality short-id（默认从入站读取）")
	singbox := fs.String("sing-box", "sing-box", "sing-box 可执行文件")
	service := fs.String("service", "sing-box", "systemd 服务名")
	statsAPI := fs.String("stats-api", "", "启用 V2Ray 用户统计并监听于该地址，如 127.0.0.1:8080")
	importUsers := fs.Bool("import-users", false, "把现有入站凭据导入为受管用户（默认原样保留为非托管凭据）")
	force := fs.Bool("force", false, "覆盖已有状态文件")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *config == "" {
		return errors.New("必须提供 --config")
	}
	legacyBackupName := ""
	legacyStatePath := ""
	if !*force {
		if _, err := os.Stat(a.statePath); err == nil {
			return fmt.Errorf("状态文件已存在: %s（如需覆盖请加 --force）", a.statePath)
		}
		if isSQLiteStatePath(a.statePath) {
			legacyPath := filepath.Join(filepath.Dir(a.statePath), "state.json")
			if _, err := os.Stat(legacyPath); err == nil {
				if _, markerErr := os.Stat(sqliteMigrationMarkerPath(a.statePath)); markerErr == nil {
					return fmt.Errorf("状态数据库缺失且旧 JSON 已有迁移标记；请从 SQLite 备份恢复，不会自动回灌 %s", legacyPath)
				} else if !errors.Is(markerErr, os.ErrNotExist) {
					return fmt.Errorf("检查旧状态迁移标记: %w", markerErr)
				}
				return fmt.Errorf("检测到待迁移的旧状态文件 %s；请先正常启动以安全迁移，或明确加 --force 放弃旧状态", legacyPath)
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("检查旧状态文件: %w", err)
			}
		}
	} else {
		if _, err := os.Stat(a.statePath); err == nil {
			if _, err := createManualStateBackup(a.statePath, time.Now()); err != nil {
				return fmt.Errorf("覆盖前备份现有状态: %w", err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("检查现有状态: %w", err)
		}
		if isSQLiteStatePath(a.statePath) {
			legacyStatePath = filepath.Join(filepath.Dir(a.statePath), "state.json")
			if _, err := os.Stat(legacyStatePath); err == nil {
				legacyBackupName, err = backupLegacyJSONFile(a.statePath, legacyStatePath, "state-replaced-json-", time.Now())
				if err != nil {
					return fmt.Errorf("覆盖前备份旧 state.json: %w", err)
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("检查旧状态文件: %w", err)
			}
		}
	}
	raw, err := os.ReadFile(*config)
	if err != nil {
		return err
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("解析配置: %w", err)
	}
	state, clean, err := importConfig(cfg, *inbound, *importUsers)
	if err != nil {
		return err
	}
	state.Version = stateVersion
	state.ConfigPath = absOrOriginal(*config)
	state.InboundTag = *inbound
	state.SingBoxBin = *singbox
	state.Service = *service
	state.StatsAPI = *statsAPI
	state.Counters = map[string]int64{}
	state.Client.Server = *server
	state.Client.PublicKey = *publicKey
	if state.Client.PublicKey == "" {
		if privateKey := realityPrivateKey(cfg, *inbound); privateKey != "" {
			derived, err := deriveRealityPublicKey(privateKey)
			if err != nil {
				return fmt.Errorf("从 Reality 私钥推导公钥: %w", err)
			}
			state.Client.PublicKey = derived
		}
	}
	if *port != 0 {
		state.Client.Port = *port
	}
	if *serverName != "" {
		state.Client.ServerName = *serverName
	}
	if *shortID != "" {
		state.Client.ShortID = *shortID
	}
	if *base == "" {
		*base = filepath.Join(filepath.Dir(a.statePath), "config.base.json")
	}
	state.BaseConfig = absOrOriginal(*base)
	baseBytes, _ := json.MarshalIndent(clean, "", "  ")
	baseBytes = append(baseBytes, '\n')
	previousBase, readBaseErr := os.ReadFile(*base)
	baseExisted := readBaseErr == nil
	if readBaseErr == nil {
		if _, err := createOutboundEndpointBackup(*base, previousBase, time.Now()); err != nil {
			return fmt.Errorf("覆盖前备份基础模板: %w", err)
		}
	} else if !errors.Is(readBaseErr, os.ErrNotExist) {
		return fmt.Errorf("覆盖前读取基础模板: %w", readBaseErr)
	}
	// A forced replacement of legacy JSON must record the archive before the
	// new base/database commit begins. If the marker cannot be persisted, no
	// live configuration has changed; if a later write fails, the marker still
	// prevents an unsafe future re-import of the deliberately replaced JSON.
	if legacyBackupName != "" {
		if err := writeSQLiteMigrationMarker(a.statePath, legacyStatePath, legacyBackupName); err != nil {
			return fmt.Errorf("记录旧 state.json 已归档: %w", err)
		}
	}
	if err := atomicWrite(*base, baseBytes, 0600); err != nil {
		return err
	}
	if err := saveState(a.statePath, state); err != nil {
		var restoreErr error
		if baseExisted {
			restoreErr = atomicWrite(*base, previousBase, 0600)
		} else {
			restoreErr = os.Remove(*base)
			if errors.Is(restoreErr, os.ErrNotExist) {
				restoreErr = nil
			}
		}
		if restoreErr != nil {
			return errors.Join(err, fmt.Errorf("恢复初始化前基础模板: %w", restoreErr))
		}
		return fmt.Errorf("写入状态失败，基础模板已恢复到初始化前: %w", err)
	}
	if *importUsers {
		fmt.Fprintf(a.out, "已导入 %d 个现有凭据为受管用户\n", countNodes(state.Users))
	} else {
		fmt.Fprintf(a.out, "已保留 %d 个现有凭据为非托管节点；受管用户列表为空\n", len(state.ReservedAuthUsers))
	}
	fmt.Fprintf(a.out, "基础模板: %s\n状态数据库: %s\n", state.BaseConfig, absOrOriginal(a.statePath))
	if state.Client.Server == "" || state.Client.PublicKey == "" {
		fmt.Fprintln(a.out, "提示：导出客户端前需要设置 --server 和 --public-key；请通过管理界面或维护入口设置。")
	}
	return nil
}

func realityPrivateKey(cfg map[string]any, inboundTag string) string {
	inbounds, _ := cfg["inbounds"].([]any)
	for _, item := range inbounds {
		inbound, _ := item.(map[string]any)
		if stringValue(inbound["tag"]) != inboundTag {
			continue
		}
		tls, _ := inbound["tls"].(map[string]any)
		reality, _ := tls["reality"].(map[string]any)
		return stringValue(reality["private_key"])
	}
	return ""
}

func deriveRealityPublicKey(encodedPrivate string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encodedPrivate)
	if err != nil {
		return "", fmt.Errorf("解析 X25519 私钥: %w", err)
	}
	privateKey, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", fmt.Errorf("创建 X25519 私钥: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()), nil
}

func importConfig(cfg map[string]any, inboundTag string, importUsers bool) (*State, map[string]any, error) {
	copyBytes, _ := json.Marshal(cfg)
	var clean map[string]any
	_ = json.Unmarshal(copyBytes, &clean)
	inbounds, ok := clean["inbounds"].([]any)
	if !ok {
		return nil, nil, errors.New("配置中没有 inbounds")
	}
	var target map[string]any
	for _, item := range inbounds {
		m, _ := item.(map[string]any)
		if stringValue(m["tag"]) == inboundTag && stringValue(m["type"]) == "vless" {
			target = m
			break
		}
	}
	if target == nil {
		return nil, nil, fmt.Errorf("找不到 VLESS 入站 %q", inboundTag)
	}
	state := &State{}
	state.InboundTag = inboundTag
	if p, ok := target["listen_port"].(float64); ok {
		state.Client.Port = int(p)
	}
	if tls, ok := target["tls"].(map[string]any); ok {
		state.Client.ServerName = stringValue(tls["server_name"])
		if reality, ok := tls["reality"].(map[string]any); ok {
			if ids, ok := reality["short_id"].([]any); ok && len(ids) > 0 {
				state.Client.ShortID = stringValue(ids[0])
			}
		}
	}
	if users, ok := target["users"].([]any); ok {
		for _, item := range users {
			m, _ := item.(map[string]any)
			name, uuid := stringValue(m["name"]), stringValue(m["uuid"])
			if name == "" || uuid == "" {
				continue
			}
			if importUsers {
				routes := routeMap(clean)
				state.Users = append(state.Users, User{Name: name, Enabled: true, Nodes: []Node{{Name: name, AuthUser: name, UUID: uuid, Outbound: routes[name]}}})
			} else {
				state.ReservedAuthUsers = append(state.ReservedAuthUsers, name)
			}
		}
	}
	if importUsers {
		target["users"] = []any{}
		stripManagedRoutes(clean)
	}
	return state, clean, nil
}

func routeMap(cfg map[string]any) map[string]string {
	result := map[string]string{}
	route, _ := cfg["route"].(map[string]any)
	rules, _ := route["rules"].([]any)
	for _, item := range rules {
		m, _ := item.(map[string]any)
		if !isSimpleAuthRouteRule(m) {
			continue
		}
		out := stringValue(m["outbound"])
		if users, ok := m["auth_user"].([]any); ok {
			for _, u := range users {
				result[stringValue(u)] = out
			}
		}
	}
	return result
}

func nodeTemplates(s *State) []NodeTemplate {
	raw, err := os.ReadFile(s.BaseConfig)
	if err != nil {
		return []NodeTemplate{{Name: "默认线路"}}
	}
	var cfg map[string]any
	if json.Unmarshal(raw, &cfg) != nil {
		return []NodeTemplate{{Name: "默认线路"}}
	}
	routes := routeMap(cfg)
	templates := make([]NodeTemplate, 0, len(s.ReservedAuthUsers))
	seen := map[string]bool{}
	for _, name := range s.ReservedAuthUsers {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		templates = append(templates, NodeTemplate{Name: name, Outbound: routes[name]})
	}
	if len(templates) == 0 {
		route, _ := cfg["route"].(map[string]any)
		templates = append(templates, NodeTemplate{Name: "默认线路", Outbound: stringValue(route["final"])})
	}
	// Expose every additional proxy-capable outbound as a selectable
	// node template. Preserved auth_user route names stay first-class aliases;
	// their targets are not duplicated. Local terminal actions are omitted
	// because assigning a customer node to direct/block/dns is almost always a
	// configuration mistake, while selector/urltest and future proxy types are
	// intentionally accepted without a hard-coded protocol whitelist.
	seenNames := map[string]bool{}
	seenTargets := map[string]bool{}
	for _, template := range templates {
		seenNames[strings.ToLower(strings.TrimSpace(template.Name))] = true
		if template.Outbound != "" {
			seenTargets[template.Outbound] = true
		}
	}
	for _, section := range []string{"outbounds"} {
		items, _ := cfg[section].([]any)
		for _, item := range items {
			object, _ := item.(map[string]any)
			tag := strings.TrimSpace(stringValue(object["tag"]))
			typeName := strings.ToLower(strings.TrimSpace(stringValue(object["type"])))
			if tag == "" || seenTargets[tag] || seenNames[strings.ToLower(tag)] {
				continue
			}
			if typeName == "direct" || typeName == "block" || typeName == "dns" {
				continue
			}
			seenNames[strings.ToLower(tag)] = true
			seenTargets[tag] = true
			templates = append(templates, NodeTemplate{Name: tag, Outbound: tag})
		}
	}
	sort.SliceStable(templates, func(i, j int) bool {
		if (templates[i].Outbound == "") != (templates[j].Outbound == "") {
			return templates[i].Outbound == ""
		}
		return strings.ToLower(templates[i].Name) < strings.ToLower(templates[j].Name)
	})
	return templates
}

func findNodeTemplate(s *State, name string) (NodeTemplate, bool) {
	for _, template := range nodeTemplates(s) {
		if strings.EqualFold(template.Name, strings.TrimSpace(name)) {
			return template, true
		}
	}
	return NodeTemplate{}, false
}

func stripManagedRoutes(cfg map[string]any) {
	route, _ := cfg["route"].(map[string]any)
	if route == nil {
		return
	}
	rules, _ := route["rules"].([]any)
	kept := make([]any, 0, len(rules))
	for _, item := range rules {
		m, _ := item.(map[string]any)
		if !isSimpleAuthRouteRule(m) {
			kept = append(kept, item)
		}
	}
	route["rules"] = kept
}

// isSimpleAuthRouteRule identifies only the one-purpose auth_user routing rule
// that sbmgr replaces while importing credentials. Complex domain, port,
// source-IP and reject rules remain part of the base template.
func isSimpleAuthRouteRule(rule map[string]any) bool {
	if rule == nil || rule["auth_user"] == nil || stringValue(rule["outbound"]) == "" {
		return false
	}
	if action := stringValue(rule["action"]); action != "" && action != "route" {
		return false
	}
	for key := range rule {
		switch key {
		case "auth_user", "outbound", "action":
		default:
			return false
		}
	}
	return true
}

func (a *app) userCmd(args []string) error {
	return a.withAuditedStateLock(auditAction("user", args), args, func() error { return a.userCmdLocked(args) })
}

func (a *app) userCmdLocked(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: sbmgr user add|clone|set|list|show|enable|disable|unblock|delete")
	}
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	switch args[0] {
	case "add":
		fs := a.newFlagSet("user add")
		quota := fs.String("quota", "0", "总流量配额，如 100G；0 为不限")
		quotaMode := fs.String("quota-mode", quotaModeTotal, "配额计量方式: total/upload/download（双向合计/仅上传/仅下载）")
		expire := fs.String("expire", "", "有效期最后一天 YYYY-MM-DD")
		upMbps := fs.Float64("up-mbps", 0, "实时上传限速 Mbps；0 为不限")
		downMbps := fs.Float64("down-mbps", 0, "实时下载限速 Mbps；0 为不限")
		outbound := fs.String("outbound", "", "默认节点的出站 tag；留空使用 route.final")
		nodeName := fs.String("node-name", "default", "自动创建的默认节点名称")
		if len(args) < 2 {
			return errors.New("用法: sbmgr user add NAME [--quota 100G] [--quota-mode total|upload|download] [--expire YYYY-MM-DD] [--up-mbps N] [--down-mbps N] [--outbound TAG] [--node-name default]")
		}
		name := args[1]
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("用法: sbmgr user add NAME [--quota 100G] [--quota-mode total|upload|download] [--expire YYYY-MM-DD] [--up-mbps N] [--down-mbps N] [--outbound TAG] [--node-name default]")
		}
		if findUser(s, name) != nil {
			return fmt.Errorf("用户 %q 已存在", name)
		}
		q, err := parseSize(*quota)
		if err != nil {
			return err
		}
		if err := validateQuotaMode(*quotaMode); err != nil {
			return err
		}
		if err := validateDate(*expire); err != nil {
			return err
		}
		if err := validateMbps(*upMbps, *downMbps); err != nil {
			return err
		}
		if strings.TrimSpace(*nodeName) == "" {
			return errors.New("--node-name 不能为空")
		}
		u := User{
			Name: name, Enabled: true, QuotaBytes: q, QuotaMode: normalizedQuotaMode(*quotaMode), Expires: *expire,
			IPPolicy: IPPolicy{Enabled: true, Mode: "enforce", Binding: "dynamic", MaxIPs: 1, HandoverSeconds: defaultIPPolicyHandoverSeconds},
			Devices:  []Device{{Name: defaultDeviceName, Enabled: true, CreatedAt: time.Now().Format(time.RFC3339)}},
		}
		n := Node{Name: *nodeName, Device: defaultDeviceName, AuthUser: uniqueAuthUser(s, name), UUID: newUUID(), Outbound: *outbound, UploadMbps: *upMbps, DownloadMbps: *downMbps}
		if nodeRateLimited(n) {
			n.RateMark = allocateRateMark(s)
		}
		u.Nodes = append(u.Nodes, n)
		s.Users = append(s.Users, u)
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "已添加用户 %s\n", name)
		return nil
	case "clone":
		fs := a.newFlagSet("user clone")
		from := fs.String("from", "", "已有模板用户名")
		if len(args) < 2 {
			return errors.New("用法: sbmgr admin user clone NAME --from TEMPLATE_USER")
		}
		name := args[1]
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 0 || strings.TrimSpace(*from) == "" {
			return errors.New("用法: sbmgr admin user clone NAME --from TEMPLATE_USER")
		}
		cloned, err := cloneUserFromTemplate(s, *from, name, time.Now())
		if err != nil {
			return err
		}
		s.Users = append(s.Users, cloned)
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "已套用用户 %s 创建 %s，并重新生成 %d 个 UUID 与全部订阅凭据\n", *from, cloned.Name, len(cloned.Nodes))
		return nil
	case "set":
		fs := a.newFlagSet("user set")
		quota := fs.String("quota", "", "新的总流量配额，如 100G；0 为不限")
		quotaMode := fs.String("quota-mode", "", "配额计量方式: total/upload/download（双向合计/仅上传/仅下载）")
		extraQuota := fs.String("extra-quota", "", "本账期附加流量包，如 20G；0 清除")
		expire := fs.String("expire", "", "新的到期日 YYYY-MM-DD")
		clearExpire := fs.Bool("clear-expire", false, "清除到期日")
		upMbps := fs.Float64("up-mbps", 0, "实时上传限速 Mbps；0 为不限")
		downMbps := fs.Float64("down-mbps", 0, "实时下载限速 Mbps；0 为不限")
		tiered := fs.String("tiered", "", "阶梯限速开关: true/false")
		tier1Usage := fs.Float64("tier1-usage", 0, "第一档触发用量百分比")
		tier1Speed := fs.Float64("tier1-speed", 0, "第一档保留速度百分比")
		tier2Usage := fs.Float64("tier2-usage", 0, "第二档触发用量百分比")
		tier2Speed := fs.Float64("tier2-speed", 0, "第二档保留速度百分比")
		burstEnabled := fs.String("burst-enabled", "", "异常流量保护开关: true/false")
		burstWindow := fs.Int("burst-window", 0, "滑动检测窗口（分钟）")
		burstLimit := fs.String("burst-limit", "", "窗口内流量阈值，如 2G")
		burstBlock := fs.Int("burst-block", 0, "触发后的临时封禁分钟数")
		burstAction := fs.String("burst-action", "", "封禁类型: soft/hard")
		burstSoftUp := fs.Float64("burst-soft-up-kbps", 0, "软封禁上传 Kbps")
		burstSoftDown := fs.Float64("burst-soft-down-kbps", 0, "软封禁下载 Kbps")
		ipEnabled := fs.String("ip-enabled", "", "来源 IP 规则开关: true/false")
		ipMode := fs.String("ip-mode", "", "来源 IP 模式: enforce/monitor")
		ipBinding := fs.String("ip-binding", "", "绑定方式: dynamic/auto/manual")
		ipMax := fs.Int("ip-max", 0, "最多允许的来源 IP 数量")
		ipHandoverSeconds := fs.Int("ip-handover-seconds", 0, "动态单活换绑宽限秒数")
		ipAllowed := fs.String("ip-allowed", "", "固定允许 IP，逗号分隔")
		ipTemp := fs.String("ip-temp", "", "临时替代 IP，逗号分隔；留空清除")
		ipTempMinutes := fs.Int("ip-temp-minutes", 0, "临时 IP 有效分钟数")
		billingEnabled := fs.String("billing-enabled", "", "按月自动清零开关: true/false")
		billingDay := fs.Int("billing-day", 0, "每月账期日 1-28")
		if len(args) < 2 {
			return errors.New("用法: sbmgr user set NAME [--quota 100G] [--expire YYYY-MM-DD|--clear-expire] [--up-mbps N] [--down-mbps N]")
		}
		name := args[1]
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 0 || (*expire != "" && *clearExpire) {
			return errors.New("用法: sbmgr user set NAME [--quota 100G] [--expire YYYY-MM-DD|--clear-expire] [--up-mbps N] [--down-mbps N]")
		}
		u := findUser(s, name)
		if u == nil {
			return fmt.Errorf("用户 %q 不存在", name)
		}
		hadBurstBlock := u.BlockedUntil != ""
		quotaSet, quotaModeSet, extraQuotaSet, expireSet, upSet, downSet, throttleSet, burstSet, ipSet, billingSet := false, false, false, false, false, false, false, false, false, false
		fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "quota":
				quotaSet = true
			case "quota-mode":
				quotaModeSet = true
			case "extra-quota":
				extraQuotaSet = true
			case "expire":
				expireSet = true
			case "clear-expire":
				expireSet = *clearExpire
			case "up-mbps":
				upSet = true
			case "down-mbps":
				downSet = true
			case "tiered", "tier1-usage", "tier1-speed", "tier2-usage", "tier2-speed":
				throttleSet = true
			case "burst-enabled", "burst-window", "burst-limit", "burst-block", "burst-action", "burst-soft-up-kbps", "burst-soft-down-kbps":
				burstSet = true
			case "ip-enabled", "ip-mode", "ip-binding", "ip-max", "ip-handover-seconds", "ip-allowed", "ip-temp", "ip-temp-minutes":
				ipSet = true
			case "billing-enabled", "billing-day":
				billingSet = true
			}
		})
		if quotaSet {
			q, err := parseSize(*quota)
			if err != nil {
				return err
			}
			u.QuotaBytes = q
			u.QuotaAlertStage = 0
		}
		if quotaModeSet {
			if err := validateQuotaMode(*quotaMode); err != nil {
				return err
			}
			u.QuotaMode = normalizedQuotaMode(*quotaMode)
			u.QuotaAlertStage = 0
		}
		if extraQuotaSet {
			q, err := parseSize(*extraQuota)
			if err != nil {
				return err
			}
			u.ExtraQuotaBytes = q
			u.QuotaAlertStage = 0
		}
		if *clearExpire {
			u.Expires = ""
			u.ExpiryAlertStage = 0
		} else if expireSet {
			if err := validateDate(*expire); err != nil {
				return err
			}
			u.Expires = *expire
			u.ExpiryAlertStage = 0
		}
		if err := validateMbps(*upMbps, *downMbps); err != nil {
			return err
		}
		if upSet || downSet {
			for i := range u.Nodes {
				if upSet {
					u.Nodes[i].UploadMbps = *upMbps
				}
				if downSet {
					u.Nodes[i].DownloadMbps = *downMbps
				}
				if nodeRateLimited(u.Nodes[i]) && u.Nodes[i].RateMark == 0 {
					u.Nodes[i].RateMark = allocateRateMark(s)
				}
			}
			u.UploadMbps, u.DownloadMbps, u.RateMark = 0, 0, 0
		}
		if throttleSet {
			policy := normalizedThrottle(u.Throttle)
			if *tiered != "" {
				enabled, err := strconv.ParseBool(*tiered)
				if err != nil {
					return errors.New("--tiered 必须是 true 或 false")
				}
				policy.Enabled = enabled
			}
			fs.Visit(func(f *flag.Flag) {
				switch f.Name {
				case "tier1-usage":
					policy.Tier1Usage = *tier1Usage
				case "tier1-speed":
					policy.Tier1Speed = *tier1Speed
				case "tier2-usage":
					policy.Tier2Usage = *tier2Usage
				case "tier2-speed":
					policy.Tier2Speed = *tier2Speed
				}
			})
			if err := validateThrottle(policy); err != nil {
				return err
			}
			u.Throttle = policy
		}
		if burstSet {
			policy := normalizedBurst(u.Burst)
			if *burstEnabled != "" {
				enabled, err := strconv.ParseBool(*burstEnabled)
				if err != nil {
					return errors.New("--burst-enabled 必须是 true 或 false")
				}
				policy.Enabled = enabled
			}
			fs.Visit(func(f *flag.Flag) {
				switch f.Name {
				case "burst-window":
					policy.WindowMinutes = *burstWindow
				case "burst-limit":
					if value, err := parseSize(*burstLimit); err == nil {
						policy.LimitBytes = value
					}
				case "burst-block":
					policy.BlockMinutes = *burstBlock
				case "burst-action":
					policy.Action = strings.ToLower(strings.TrimSpace(*burstAction))
				case "burst-soft-up-kbps":
					policy.SoftUploadKbps = *burstSoftUp
				case "burst-soft-down-kbps":
					policy.SoftDownloadKbps = *burstSoftDown
				}
			})
			if *burstLimit != "" {
				value, err := parseSize(*burstLimit)
				if err != nil {
					return fmt.Errorf("异常流量阈值: %w", err)
				}
				policy.LimitBytes = value
			}
			if err := validateBurst(policy); err != nil {
				return err
			}
			u.Burst = policy
			if !policy.Enabled {
				u.TrafficSamples = nil
				u.BlockedUntil, u.BlockReason = "", ""
			}
			if hadBurstBlock {
				s.BurstApplyPending = true
			}
		}
		if ipSet {
			oldPolicy := normalizedIPPolicy(u.IPPolicy)
			policy := oldPolicy
			if *ipEnabled != "" {
				enabled, err := strconv.ParseBool(*ipEnabled)
				if err != nil {
					return errors.New("--ip-enabled 必须是 true 或 false")
				}
				policy.Enabled = enabled
			}
			tempSet, tempMinutesSet := false, false
			fs.Visit(func(f *flag.Flag) {
				switch f.Name {
				case "ip-mode":
					policy.Mode = strings.ToLower(strings.TrimSpace(*ipMode))
				case "ip-binding":
					policy.Binding = strings.ToLower(strings.TrimSpace(*ipBinding))
				case "ip-max":
					policy.MaxIPs = *ipMax
				case "ip-handover-seconds":
					policy.HandoverSeconds = *ipHandoverSeconds
				case "ip-allowed":
					policy.BoundIPs, err = parseIPList(*ipAllowed)
				case "ip-temp":
					tempSet = true
					policy.TemporaryIPs, err = parseIPList(*ipTemp)
				case "ip-temp-minutes":
					tempMinutesSet = true
				}
			})
			if err != nil {
				return err
			}
			if tempSet {
				if len(policy.TemporaryIPs) == 0 {
					policy.TemporaryUntil = ""
				} else {
					if *ipTempMinutes <= 0 {
						return errors.New("设置临时 IP 时，临时分钟数必须大于 0")
					}
					policy.TemporaryUntil = time.Now().Add(time.Duration(*ipTempMinutes) * time.Minute).Format(time.RFC3339Nano)
				}
			} else if tempMinutesSet {
				if len(policy.TemporaryIPs) == 0 || *ipTempMinutes <= 0 {
					return errors.New("延长临时 IP 时必须已有临时 IP，且分钟数大于 0")
				}
				policy.TemporaryUntil = time.Now().Add(time.Duration(*ipTempMinutes) * time.Minute).Format(time.RFC3339Nano)
			}
			if policy.Enabled && policy.Binding == "dynamic" && len(policy.BoundIPs) == 0 && len(policy.TemporaryIPs) == 0 {
				if active := activeSourceIPs(s, u.Name, ""); len(active) == 1 {
					policy.BoundIPs = active
				}
			}
			if err := validateIPPolicy(policy); err != nil {
				return err
			}
			u.IPPolicy = policy
			if ipPolicyRuleSignature(oldPolicy, time.Now()) != ipPolicyRuleSignature(policy, time.Now()) {
				s.IPApplyPending = true
			}
		}
		if billingSet {
			policy := normalizedBilling(u.Billing)
			if *billingEnabled != "" {
				enabled, err := strconv.ParseBool(*billingEnabled)
				if err != nil {
					return errors.New("--billing-enabled 必须是 true 或 false")
				}
				if enabled && !policy.Enabled {
					now := time.Now()
					location, _ := billingLocation(policy.TimeZone)
					policy.LastReset = now.In(location).Format(time.RFC3339)
					policy.NextReset = nextBillingReset(now, policy).Format(time.RFC3339)
				}
				policy.Enabled = enabled
			}
			fs.Visit(func(f *flag.Flag) {
				if f.Name == "billing-day" {
					policy.CycleDay = *billingDay
					if policy.Enabled {
						policy.NextReset = nextBillingReset(time.Now(), policy).Format(time.RFC3339)
					}
				}
			})
			if err := validateBilling(policy); err != nil {
				return err
			}
			u.Billing = policy
		}
		if !quotaSet && !quotaModeSet && !extraQuotaSet && !expireSet && !upSet && !downSet && !throttleSet && !burstSet && !ipSet && !billingSet {
			return errors.New("没有指定要修改的字段")
		}
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		burstOnly := burstSet && !quotaSet && !quotaModeSet && !extraQuotaSet && !expireSet && !upSet && !downSet && !throttleSet && !ipSet && !billingSet
		ipOnly := ipSet && !quotaSet && !quotaModeSet && !extraQuotaSet && !expireSet && !upSet && !downSet && !throttleSet && !burstSet && !billingSet
		if burstOnly && !(hadBurstBlock && !u.Burst.Enabled) {
			fmt.Fprintf(a.out, "已更新用户 %s 的异常流量保护，后台下个维护周期生效\n", u.Name)
		} else if ipOnly {
			fmt.Fprintf(a.out, "已更新用户 %s 的来源 IP 规则，后台下个维护周期自动应用\n", u.Name)
		} else {
			fmt.Fprintf(a.out, "已更新用户 %s（按 p 应用配置后生效）\n", u.Name)
		}
		return nil
	case "list":
		sort.Slice(s.Users, func(i, j int) bool { return s.Users[i].Name < s.Users[j].Name })
		fmt.Fprintln(a.out, "NAME\tSTATUS\tUSAGE\tMETERING\tQUOTA\tRATE\tEXPIRES\tNODES")
		for _, u := range s.Users {
			status := "enabled"
			if !u.Enabled {
				status = "disabled"
			}
			if expired(u, time.Now()) {
				status = "expired"
			}
			if overQuota(u) {
				status = "quota"
			}
			fmt.Fprintf(a.out, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\n", u.Name, status, formatSize(measuredUsage(u)), normalizedQuotaMode(u.QuotaMode), formatQuota(userQuota(u)), userRateSummary(u), dash(u.Expires), len(u.Nodes))
		}
		return nil
	case "show":
		if len(args) != 2 {
			return errors.New("用法: sbmgr user show NAME")
		}
		u := findUser(s, args[1])
		if u == nil {
			return fmt.Errorf("用户 %q 不存在", args[1])
		}
		b, _ := json.MarshalIndent(u, "", "  ")
		fmt.Fprintln(a.out, string(b))
		return nil
	case "enable", "disable":
		if len(args) != 2 {
			return fmt.Errorf("用法: sbmgr user %s NAME", args[0])
		}
		u := findUser(s, args[1])
		if u == nil {
			return fmt.Errorf("用户 %q 不存在", args[1])
		}
		u.Enabled = args[0] == "enable"
		if u.Enabled {
			u.DisabledReason = ""
		} else {
			u.DisabledReason = "manual"
		}
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "用户 %s 已%s\n", u.Name, map[bool]string{true: "启用", false: "禁用"}[u.Enabled])
		return nil
	case "unblock":
		fs := a.newFlagSet("user unblock")
		all := fs.Bool("all", false, "解除所有用户的临时封禁")
		apply := fs.Bool("apply", false, "解除后立即校验并应用配置")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		names := fs.Args()
		if (*all && len(names) != 0) || (!*all && len(names) != 1) {
			return errors.New("用法: sbmgr admin user unblock USER [--apply]，或 unblock --all [--apply]")
		}
		var target *User
		if !*all {
			target = findUser(s, names[0])
			if target == nil {
				return fmt.Errorf("用户 %q 不存在", names[0])
			}
		}
		unblocked := make([]string, 0)
		now := time.Now()
		for index := range s.Users {
			u := &s.Users[index]
			if target != nil && !strings.EqualFold(u.Name, target.Name) {
				continue
			}
			if strings.TrimSpace(u.BlockedUntil) == "" {
				continue
			}
			action := burstActionText(u.Burst)
			u.BlockedUntil, u.BlockReason = "", ""
			u.TrafficSamples = nil
			unblocked = append(unblocked, u.Name)
			appendAlert(s, Alert{At: now.Format(time.RFC3339), User: u.Name, Kind: "burst_unblocked_manual", Message: fmt.Sprintf("管理员已手动解除用户 %s 的%s；异常保护规则保持不变", u.Name, action)})
		}
		if len(unblocked) == 0 {
			if target != nil {
				fmt.Fprintf(a.out, "用户 %s 当前没有临时封禁\n", target.Name)
			} else {
				fmt.Fprintln(a.out, "当前没有临时封禁用户")
			}
			return nil
		}
		s.BurstApplyPending = true
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "已解除 %d 个用户的临时封禁：%s；异常保护规则保持不变\n", len(unblocked), strings.Join(unblocked, "、"))
		if *apply {
			if err := applyState(s, false, false, a.out); err != nil {
				return err
			}
			s.BurstApplyPending = false
			return saveState(a.statePath, s)
		}
		fmt.Fprintln(a.out, "按 p 应用配置后恢复连接")
		return nil
	case "delete":
		if len(args) != 2 {
			return errors.New("用法: sbmgr user delete NAME")
		}
		idx := userIndex(s, args[1])
		if idx < 0 {
			return fmt.Errorf("用户 %q 不存在", args[1])
		}
		s.Users = append(s.Users[:idx], s.Users[idx+1:]...)
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "已删除用户 %s（运行 apply 后生效）\n", args[1])
		return nil
	default:
		return fmt.Errorf("未知 user 子命令 %q", args[0])
	}
}

func (a *app) nodeCmd(args []string) error {
	return a.withAuditedStateLock(auditAction("node", args), args, func() error { return a.nodeCmdLocked(args) })
}

func (a *app) nodeCmdLocked(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: sbmgr node add|list|delete")
	}
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	switch args[0] {
	case "templates":
		if len(args) != 1 {
			return errors.New("node templates 不接受额外参数")
		}
		fmt.Fprintln(a.out, "NAME\tOUTBOUND")
		for _, template := range nodeTemplates(s) {
			fmt.Fprintf(a.out, "%s\t%s\n", template.Name, dash(template.Outbound))
		}
		return nil
	case "add":
		fs := a.newFlagSet("node add")
		name := fs.String("name", "", "节点显示名称")
		deviceName := fs.String("device", "", "所属设备；留空使用第一个设备")
		outbound := fs.String("outbound", "", "sing-box 出站 tag；留空使用 route.final")
		uuid := fs.String("uuid", "", "指定 UUID；留空自动生成")
		upMbps := fs.Float64("up-mbps", 0, "该节点实时上传限速 Mbps；0 为不限")
		downMbps := fs.Float64("down-mbps", 0, "该节点实时下载限速 Mbps；0 为不限")
		if len(args) < 2 {
			return errors.New("用法: sbmgr node add USER --name NAME [--outbound TAG] [--uuid UUID]")
		}
		userName := args[1]
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 0 || *name == "" {
			return errors.New("用法: sbmgr node add USER --name NAME [--outbound TAG] [--uuid UUID]")
		}
		u := findUser(s, userName)
		if u == nil {
			return fmt.Errorf("用户 %q 不存在", userName)
		}
		if *deviceName == "" {
			*deviceName = u.Devices[0].Name
		}
		device := findDevice(u, *deviceName)
		if device == nil {
			return fmt.Errorf("设备 %q 不存在", *deviceName)
		}
		for _, n := range u.Nodes {
			if strings.EqualFold(n.Device, device.Name) && strings.EqualFold(n.Name, *name) {
				return fmt.Errorf("设备 %q 中的节点 %q 已存在", device.Name, *name)
			}
		}
		if *uuid == "" {
			*uuid = newUUID()
		}
		if err := validateMbps(*upMbps, *downMbps); err != nil {
			return err
		}
		auth := uniqueAuthUser(s, u.Name+":"+slug(device.Name)+":"+slug(*name))
		n := Node{Name: *name, Device: device.Name, AuthUser: auth, UUID: *uuid, Outbound: *outbound, UploadMbps: *upMbps, DownloadMbps: *downMbps}
		if nodeRateLimited(n) {
			n.RateMark = allocateRateMark(s)
		}
		u.Nodes = append(u.Nodes, n)
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "已添加节点 %s/%s/%s，UUID: %s\n", u.Name, device.Name, *name, *uuid)
		return nil
	case "list":
		if len(args) != 2 {
			return errors.New("用法: sbmgr node list USER")
		}
		u := findUser(s, args[1])
		if u == nil {
			return fmt.Errorf("用户 %q 不存在", args[1])
		}
		fmt.Fprintln(a.out, "DEVICE\tNAME\tAUTH_USER\tOUTBOUND\tUP_Mbps\tDOWN_Mbps\tUUID")
		for _, n := range u.Nodes {
			fmt.Fprintf(a.out, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", n.Device, n.Name, n.AuthUser, dash(n.Outbound), formatMbps(n.UploadMbps), formatMbps(n.DownloadMbps), n.UUID)
		}
		return nil
	case "set":
		fs := a.newFlagSet("node set")
		deviceName := fs.String("device", "", "所属设备；同名节点不唯一时必须指定")
		upMbps := fs.Float64("up-mbps", 0, "该节点实时上传限速 Mbps；0 为不限")
		downMbps := fs.Float64("down-mbps", 0, "该节点实时下载限速 Mbps；0 为不限")
		if len(args) < 3 {
			return errors.New("用法: sbmgr node set USER NODE --up-mbps N --down-mbps N")
		}
		if err := fs.Parse(args[3:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("用法: sbmgr node set USER NODE --up-mbps N --down-mbps N")
		}
		if err := validateMbps(*upMbps, *downMbps); err != nil {
			return err
		}
		u := findUser(s, args[1])
		if u == nil {
			return fmt.Errorf("用户 %q 不存在", args[1])
		}
		n, err := findUserNode(u, *deviceName, args[2])
		if err != nil {
			return err
		}
		n.UploadMbps, n.DownloadMbps = *upMbps, *downMbps
		if nodeRateLimited(*n) && n.RateMark == 0 {
			n.RateMark = allocateRateMark(s)
		}
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "已更新节点 %s/%s/%s 的限速（运行 apply 后生效）\n", u.Name, n.Device, n.Name)
		return nil
	case "delete":
		fs := a.newFlagSet("node delete")
		deviceName := fs.String("device", "", "所属设备；同名节点不唯一时必须指定")
		if len(args) < 3 {
			return errors.New("用法: sbmgr node delete USER NODE [--device DEVICE]")
		}
		if err := fs.Parse(args[3:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("用法: sbmgr node delete USER NODE [--device DEVICE]")
		}
		u := findUser(s, args[1])
		if u == nil {
			return fmt.Errorf("用户 %q 不存在", args[1])
		}
		node, err := findUserNode(u, *deviceName, args[2])
		if err != nil {
			return err
		}
		if len(nodesForDevice(*u, node.Device)) == 1 {
			return errors.New("每台设备至少需要保留一个节点，不能删除最后一个 UUID")
		}
		idx := -1
		for i := range u.Nodes {
			if &u.Nodes[i] == node {
				idx = i
				break
			}
		}
		u.Nodes = append(u.Nodes[:idx], u.Nodes[idx+1:]...)
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "节点已删除（运行 apply 后生效）")
		return nil
	default:
		return fmt.Errorf("未知 node 子命令 %q", args[0])
	}
}

func (a *app) trafficCmd(args []string) error {
	return a.withAuditedStateLock(auditAction("traffic", args), args, func() error { return a.trafficCmdLocked(args) })
}

func (a *app) trafficCmdLocked(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: sbmgr traffic add|set|reset USER")
	}
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	if len(args) < 2 {
		return errors.New("缺少用户名")
	}
	u := findUser(s, args[1])
	if u == nil {
		return fmt.Errorf("用户 %q 不存在", args[1])
	}
	switch args[0] {
	case "reset":
		fs := a.newFlagSet("traffic reset")
		apply := fs.Bool("apply", false, "重置后立即校验并应用配置")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("用法: sbmgr admin traffic reset USER [--apply]")
		}
		if err := snapshotUserTrafficCounterBaselines(s, u); err != nil {
			return fmt.Errorf("建立本月流量重置基线: %w", err)
		}
		oldUpload, oldDownload := u.Upload, u.Download
		oldThrottleStage := throttleStage(*u)
		hadBlock := strings.TrimSpace(u.BlockedUntil) != ""
		wasQuotaDisabled := !u.Enabled && u.DisabledReason == "quota" && !expired(*u, time.Now())
		manualResetUserMonthlyTraffic(s, u, time.Now())
		if oldThrottleStage != throttleStage(*u) {
			s.RateApplyPending = true
		}
		if hadBlock || wasQuotaDisabled {
			// The background daemon retries pending config changes if an explicit
			// --apply fails or the command intentionally omits it.
			s.BurstApplyPending = true
		}
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "已重置用户 %s 的本月流量：原上传 %s，原下载 %s；配额和账期日保持不变\n", u.Name, formatSize(oldUpload), formatSize(oldDownload))
		if *apply {
			if err := applyState(s, false, false, a.out); err != nil {
				return err
			}
			if s.BurstApplyPending || s.RateApplyPending || s.StatsApplyPending {
				s.BurstApplyPending = false
				s.RateApplyPending = false
				s.StatsApplyPending = false
				return saveState(a.statePath, s)
			}
		}
		return nil
	case "add", "set":
		fs := a.newFlagSet("traffic " + args[0])
		up := fs.String("upload", "0", "上传字节或 1G 格式")
		down := fs.String("download", "0", "下载字节或 1G 格式")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		uv, err := parseSize(*up)
		if err != nil {
			return err
		}
		dv, err := parseSize(*down)
		if err != nil {
			return err
		}
		if args[0] == "add" {
			u.Upload += uv
			u.Download += dv
		} else {
			u.Upload = uv
			u.Download = dv
		}
	default:
		return fmt.Errorf("未知 traffic 子命令 %q", args[0])
	}
	if err := saveState(a.statePath, s); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "%s 计费用量 %s（%s；原始上传 %s，原始下载 %s）\n", u.Name, formatSize(measuredUsage(*u)), quotaModeText(u.QuotaMode), formatSize(u.Upload), formatSize(u.Download))
	return nil
}

func (a *app) renderCmd(args []string) error {
	fs := a.newFlagSet("render")
	output := fs.String("output", "config.generated.json", "输出路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	b, err := renderConfig(s)
	if err != nil {
		return err
	}
	if err := atomicWrite(*output, b, 0600); err != nil {
		return err
	}
	fmt.Fprintln(a.out, "已生成", *output)
	return nil
}

func (a *app) checkCmd(args []string) error {
	if len(args) != 0 {
		return errors.New("check 不接受额外参数")
	}
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	return checkRendered(s, a.out)
}

func (a *app) applyCmd(args []string) error {
	return a.withAuditedStateLock("config.apply", args, func() error { return a.applyCmdLocked(args) })
}

func (a *app) applyCmdLocked(args []string) error {
	fs := a.newFlagSet("apply")
	noReload := fs.Bool("no-reload", false, "只替换配置，不通知服务")
	restart := fs.Bool("restart", false, "重启服务，而不是发送 SIGHUP")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	if err := applyState(s, *noReload, *restart, a.out); err != nil {
		return err
	}
	if !*noReload && (s.IPApplyPending || s.BurstApplyPending || s.RateApplyPending || s.StatsApplyPending) {
		s.IPApplyPending = false
		s.BurstApplyPending = false
		s.RateApplyPending = false
		s.StatsApplyPending = false
		return saveState(a.statePath, s)
	}
	return nil
}

func (a *app) enforceCmd(args []string) error {
	return a.withStateLock(func() error { return a.enforceCmdLocked(args) })
}

func (a *app) enforceCmdLocked(args []string) error {
	fs := a.newFlagSet("enforce")
	doApply := fs.Bool("apply", false, "禁用后立即应用配置")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	changed := false
	now := time.Now()
	for i := range s.Users {
		u := &s.Users[i]
		if u.Enabled && (expired(*u, now) || overQuota(*u)) {
			u.Enabled = false
			u.DisabledReason = automaticDisableReason(*u, now)
			changed = true
			fmt.Fprintf(a.out, "已禁用 %s：%s\n", u.Name, disableReason(*u, now))
		}
	}
	if changed {
		// StatsApplyPending is retained as the compatible on-disk field name,
		// but represents any full eligibility/config apply (including nft quota
		// and clock-based expiry enforcement). Save it before attempting apply so
		// a failed reload is retried by a later daemon cycle.
		s.StatsApplyPending = true
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
	}
	if !changed && !s.StatsApplyPending {
		fmt.Fprintln(a.out, "没有需要禁用的用户")
		return nil
	}
	if *doApply {
		if err := applyState(s, false, false, a.out); err != nil {
			return err
		}
		s.StatsApplyPending = false
		return saveState(a.statePath, s)
	}
	if changed {
		fmt.Fprintln(a.out, "运行 sbmgr apply 使变更生效")
	} else {
		fmt.Fprintln(a.out, "仍有未应用的资格变更；运行 sbmgr apply 重试")
	}
	return nil
}

func (a *app) exportCmd(args []string) error {
	fs := a.newFlagSet("export")
	output := fs.String("output", "", "输出 YAML 路径，默认标准输出")
	deviceName := fs.String("device", "", "导出的设备名称；用户有多台设备时必须指定")
	if len(args) < 1 {
		return errors.New("用法: sbmgr export USER [--output USER.yaml]")
	}
	userName := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("用法: sbmgr export USER [--output USER.yaml]")
	}
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	u := findUser(s, userName)
	if u == nil {
		return fmt.Errorf("用户 %q 不存在", userName)
	}
	if s.Client.Server == "" || s.Client.PublicKey == "" {
		return errors.New("状态文件缺少 client.server 或 client.reality_public_key")
	}
	if *deviceName == "" {
		if len(u.Devices) != 1 {
			return fmt.Errorf("用户 %s 有 %d 台设备，请使用 --device 指定", u.Name, len(u.Devices))
		}
		*deviceName = u.Devices[0].Name
	}
	b, err := renderMihomoDevice(s, *u, *deviceName)
	if err != nil {
		return err
	}
	if *output == "" {
		_, err = a.out.Write(b)
		return err
	}
	if err := atomicWrite(*output, b, 0600); err != nil {
		return err
	}
	fmt.Fprintln(a.out, "已导出", *output)
	return nil
}

func renderConfig(s *State) ([]byte, error) {
	normalizeDeviceModel(s)
	ensureNodeMarks(s)
	raw, err := os.ReadFile(s.BaseConfig)
	if err != nil {
		return nil, fmt.Errorf("读取基础模板: %w", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	inbounds, _ := cfg["inbounds"].([]any)
	var target map[string]any
	for _, item := range inbounds {
		m, _ := item.(map[string]any)
		if stringValue(m["tag"]) == s.InboundTag {
			target = m
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("基础模板缺少入站 %q", s.InboundTag)
	}
	route, _ := cfg["route"].(map[string]any)
	if route == nil {
		route = map[string]any{}
		cfg["route"] = route
	}
	baseUsers, _ := target["users"].([]any)
	users := append([]any(nil), baseUsers...)
	managedRules := ipRestrictionRules(s, time.Now())
	managedRules = append(managedRules, accessRestrictionRules(s, time.Now())...)
	activeUsers := []User{}
	now := time.Now()
	for _, u := range s.Users {
		if !u.Enabled || expired(u, now) || overQuota(u) || burstHardBlocked(u, now) {
			continue
		}
		active := activeUserDevices(u)
		if len(active.Nodes) == 0 {
			continue
		}
		activeUsers = append(activeUsers, active)
		for _, n := range active.Nodes {
			users = append(users, map[string]any{"name": n.AuthUser, "uuid": n.UUID})
		}
	}
	rateOutbounds, err := addRateOutbounds(cfg, s, activeUsers)
	if err != nil {
		return nil, err
	}
	for _, u := range activeUsers {
		for _, n := range u.Nodes {
			outbound := n.Outbound
			_, _, mark := effectiveNodeRate(u, n)
			if validRateMark(mark) {
				outbound = rateOutbounds[n.AuthUser]
			}
			if outbound != "" {
				managedRules = append(managedRules, map[string]any{"auth_user": []any{n.AuthUser}, "action": "route", "outbound": outbound})
			}
		}
	}
	target["users"] = users
	if s.StatsAPI != "" {
		experimental, _ := cfg["experimental"].(map[string]any)
		if experimental == nil {
			experimental = map[string]any{}
			cfg["experimental"] = experimental
		}
		statUsers := make([]any, 0, len(users))
		for _, item := range users {
			m, _ := item.(map[string]any)
			statUsers = append(statUsers, m["name"])
		}
		experimental["v2ray_api"] = map[string]any{
			"listen": s.StatsAPI,
			"stats": map[string]any{
				"enabled":  true,
				"inbounds": []any{s.InboundTag},
				"users":    statUsers,
			},
		}
	}
	existing, _ := route["rules"].([]any)
	route["rules"] = append(managedRules, existing...)
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func checkRendered(s *State, w io.Writer) error {
	b, err := renderConfig(s)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.ConfigPath)
	f, err := os.CreateTemp(dir, "sbmgr-check-*.json")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	cmd := exec.Command(s.SingBoxBin, "check", "-c", name)
	var diagnostics cappedDiagnosticBuffer
	cmd.Stdout = &diagnostics
	cmd.Stderr = &diagnostics
	if err := cmd.Run(); err != nil {
		diagnostics.writeRedactedTo(w, b)
		return fmt.Errorf("sing-box 配置校验失败: %w", err)
	}
	diagnostics.writeRedactedTo(w, b)
	fmt.Fprintln(w, "sing-box 配置校验通过")
	return nil
}

func applyState(s *State, noReload, restart bool, w io.Writer) error {
	b, err := renderConfig(s)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.ConfigPath)
	f, err := os.CreateTemp(dir, "sbmgr-apply-*.json")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	check := exec.Command(s.SingBoxBin, "check", "-c", tmp)
	var diagnostics cappedDiagnosticBuffer
	check.Stdout = &diagnostics
	check.Stderr = &diagnostics
	if err := check.Run(); err != nil {
		diagnostics.writeRedactedTo(w, b)
		return fmt.Errorf("校验失败，未修改现有配置: %w", err)
	}
	diagnostics.writeRedactedTo(w, b)
	if !noReload && runtime.GOOS == "windows" {
		return errors.New("Windows 下不能自动调用 systemctl；配置尚未写入，请使用 --no-reload 后手动部署")
	}
	rateRestart := !noReload && rateTopologyChanged(s)
	backup := filepath.Join(filepath.Dir(s.ConfigPath), "backups", "sing-box.previous.json")
	old, readErr := os.ReadFile(s.ConfigPath)
	hadOldConfig := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("读取现有配置: %w", readErr)
	}
	if hadOldConfig {
		if err := atomicWrite(backup, old, 0600); err != nil {
			return fmt.Errorf("创建备份: %w", err)
		}
	}
	if noReload {
		if err := atomicWrite(s.ConfigPath, b, 0600); err != nil {
			return err
		}
		fmt.Fprintf(w, "配置已写入；备份: %s\n", dash(map[bool]string{true: backup}[hadOldConfig]))
		if hasRateLimits(s) {
			fmt.Fprintln(w, "提示：--no-reload 未应用 nftables；重载服务后请运行 sbmgr rate apply")
		}
		return nil
	}
	if err := checkRateLimits(s, w); err != nil {
		return fmt.Errorf("限速规则校验失败，未修改现有配置: %w", err)
	}
	rateSnapshot, err := captureNftRateSnapshot()
	if err != nil {
		return fmt.Errorf("备份现有限速规则: %w", err)
	}
	if err := applyRateLimits(s, w); err != nil {
		rollbackErr := restoreNftRateSnapshot(s, rateSnapshot, w)
		return errors.Join(fmt.Errorf("限速规则应用失败，配置未修改: %w", err), rollbackErr)
	}
	if err := atomicWrite(s.ConfigPath, b, 0600); err != nil {
		return errors.Join(fmt.Errorf("写入配置失败: %w", err), restoreNftRateSnapshot(s, rateSnapshot, w))
	}
	if rateRestart && !restart {
		fmt.Fprintln(w, "检测到限速 mark 拓扑变化，将重启 sing-box 以关闭旧的未标记连接")
	}
	if err := reloadSingBoxService(s, restart || rateRestart, w); err != nil {
		rollbackErrors := []error{fmt.Errorf("服务重载失败: %w", err)}
		if hadOldConfig {
			if restoreErr := atomicWrite(s.ConfigPath, old, 0600); restoreErr != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("恢复旧配置: %w", restoreErr))
			}
		} else if removeErr := os.Remove(s.ConfigPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("移除新配置: %w", removeErr))
		}
		if restoreErr := restoreNftRateSnapshot(s, rateSnapshot, w); restoreErr != nil {
			rollbackErrors = append(rollbackErrors, restoreErr)
		}
		if hadOldConfig {
			if restoreErr := reloadSingBoxService(s, true, w); restoreErr != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("重新加载旧配置: %w", restoreErr))
			}
		}
		return fmt.Errorf("应用失败，已执行配置与限速回滚: %w", errors.Join(rollbackErrors...))
	}
	fmt.Fprintf(w, "配置已应用；备份: %s\n", dash(map[bool]string{true: backup}[hadOldConfig]))
	fmt.Fprintln(w, "sing-box 已重载")
	return nil
}

func reloadSingBoxService(s *State, restart bool, w io.Writer) error {
	var cmd *exec.Cmd
	if restart {
		cmd = exec.Command("systemctl", "restart", s.Service)
	} else {
		cmd = exec.Command("systemctl", "kill", "-s", "HUP", s.Service)
	}
	cmd.Stdout, cmd.Stderr = w, w
	return cmd.Run()
}

func renderMihomo(s *State, u User) ([]byte, error) {
	if len(u.Devices) == 0 {
		u.Devices = []Device{{Name: defaultDeviceName, Enabled: true}}
		for i := range u.Nodes {
			u.Nodes[i].Device = defaultDeviceName
		}
	}
	if len(u.Devices) != 1 {
		return nil, errors.New("用户有多台设备，必须选择一台设备导出")
	}
	return renderMihomoDevice(s, u, u.Devices[0].Name)
}

func renderMihomoDevice(s *State, u User, deviceName string) ([]byte, error) {
	if !u.Enabled {
		return nil, errors.New("用户已禁用")
	}
	if expired(u, time.Now()) {
		return nil, errors.New("用户已过期")
	}
	if overQuota(u) {
		return nil, errors.New("用户已用完流量")
	}
	if burstHardBlocked(u, time.Now()) {
		return nil, fmt.Errorf("用户因异常流量被临时封禁至 %s", u.BlockedUntil)
	}
	device := findDevice(&u, deviceName)
	if device == nil {
		return nil, fmt.Errorf("设备 %q 不存在", deviceName)
	}
	if !device.Enabled {
		return nil, fmt.Errorf("设备 %q 已禁用", device.Name)
	}
	nodes := nodesForDevice(u, device.Name)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("设备 %q 没有可导出的节点", device.Name)
	}
	if strings.TrimSpace(s.Client.MihomoTemplate) != "" {
		return renderMihomoFromTemplate(s, u, *device, nodes)
	}
	var b strings.Builder
	b.WriteString("mixed-port: 7893\nallow-lan: false\nmode: rule\nlog-level: info\nipv6: false\n\nproxies:\n")
	names := []string{}
	for _, n := range nodes {
		name := strings.ReplaceAll(deviceNodeLabel(u.Name, device.Name, n.Name), "/", "-")
		names = append(names, name)
		fmt.Fprintf(&b, "  - name: %s\n    type: vless\n    server: %s\n    port: %d\n    uuid: %s\n    network: tcp\n    tls: true\n    udp: true\n    servername: %s\n    client-fingerprint: chrome\n    reality-opts:\n      public-key: %s\n      short-id: %s\n    packet-encoding: xudp\n", yamlQuote(name), yamlQuote(s.Client.Server), s.Client.Port, yamlQuote(n.UUID), yamlQuote(s.Client.ServerName), yamlQuote(s.Client.PublicKey), yamlQuote(s.Client.ShortID))
	}
	b.WriteString("\nproxy-groups:\n  - name: \"节点选择\"\n    type: select\n    proxies:\n")
	for _, n := range names {
		fmt.Fprintf(&b, "      - %s\n", yamlQuote(n))
	}
	b.WriteString("      - DIRECT\n\nrules:\n  - MATCH,节点选择\n")
	return []byte(b.String()), nil
}

func (a *app) simpleMenu() error {
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Fprintln(a.out, "\n1) 用户列表  2) 添加用户  3) 启用/禁用  4) 添加节点  5) 应用配置  6) 导出用户  7) 设置实时限速  0) 退出")
		fmt.Fprint(a.out, "> ")
		if !in.Scan() {
			return in.Err()
		}
		switch strings.TrimSpace(in.Text()) {
		case "0":
			return nil
		case "1":
			if err := a.userCmd([]string{"list"}); err != nil {
				fmt.Fprintln(a.err, err)
			}
		case "2":
			name := prompt(in, a.out, "用户名")
			quota := prompt(in, a.out, "配额(如100G，0不限)")
			expire := prompt(in, a.out, "到期日(留空不限)")
			argv := []string{"add", name, "--quota", quota}
			if expire != "" {
				argv = append(argv, "--expire", expire)
			}
			if err := a.userCmd(argv); err != nil {
				fmt.Fprintln(a.err, err)
			}
		case "3":
			name := prompt(in, a.out, "用户名")
			op := prompt(in, a.out, "enable/disable")
			if err := a.userCmd([]string{op, name}); err != nil {
				fmt.Fprintln(a.err, err)
			}
		case "4":
			user := prompt(in, a.out, "用户名")
			name := prompt(in, a.out, "节点名")
			out := prompt(in, a.out, "出口tag(留空走默认)")
			argv := []string{"add", user, "--name", name}
			if out != "" {
				argv = append(argv, "--outbound", out)
			}
			if err := a.nodeCmd(argv); err != nil {
				fmt.Fprintln(a.err, err)
			}
		case "5":
			if err := a.applyCmd(nil); err != nil {
				fmt.Fprintln(a.err, err)
			}
		case "6":
			user := prompt(in, a.out, "用户名")
			path := prompt(in, a.out, "输出路径")
			if err := a.exportCmd([]string{user, "--output", path}); err != nil {
				fmt.Fprintln(a.err, err)
			}
		case "7":
			user := prompt(in, a.out, "用户名")
			up := prompt(in, a.out, "上传 Mbps (0 不限)")
			down := prompt(in, a.out, "下载 Mbps (0 不限)")
			if err := a.userCmd([]string{"set", user, "--up-mbps", up, "--down-mbps", down}); err != nil {
				fmt.Fprintln(a.err, err)
			}
		default:
			fmt.Fprintln(a.err, "无效选择")
		}
	}
}

func prompt(s *bufio.Scanner, w io.Writer, label string) string {
	fmt.Fprintf(w, "%s: ", label)
	if s.Scan() {
		return strings.TrimSpace(s.Text())
	}
	return ""
}
func loadState(path string) (*State, error) {
	s, _, err := loadStateWithCanonicalChange(path)
	return s, err
}

// loadStateWithCanonicalChange remains read-only. It reports whether migrations
// or normalizers changed the decoded model so callers that already own the state
// lock can durably commit generated credentials and other canonical fields.
func loadStateWithCanonicalChange(path string) (*State, bool, error) {
	if isSQLiteStatePath(path) {
		return loadSQLiteStateWithCanonicalChange(path)
	}
	return loadJSONStateWithCanonicalChange(path)
}

func loadJSONStateWithCanonicalChange(path string) (*State, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("读取状态文件 %s: %w", path, err)
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, false, err
	}
	before, err := json.Marshal(&s)
	if err != nil {
		return nil, false, err
	}
	if err := migrateState(&s); err != nil {
		return nil, false, err
	}
	normalizeQuotaModes(&s)
	normalizeDeviceModel(&s)
	ensureNodeMarks(&s)
	normalizeLegacyNodeNames(&s)
	if err := validateState(&s); err != nil {
		return nil, false, fmt.Errorf("状态文件校验失败: %w", err)
	}
	after, err := json.Marshal(&s)
	if err != nil {
		return nil, false, err
	}
	return &s, !bytes.Equal(before, after), nil
}

// loadCanonicalState is the explicit write boundary for state migration. It is
// used before independently-reading surfaces (TUI and subscription HTTP) start,
// preventing a legacy missing token from being regenerated on every load.
func (a *app) loadCanonicalState() (*State, error) {
	var state *State
	err := a.withStateLock(func() error {
		loaded, changed, err := loadStateWithCanonicalChange(a.statePath)
		if err != nil {
			return err
		}
		if changed {
			if err := saveState(a.statePath, loaded); err != nil {
				return fmt.Errorf("保存状态迁移: %w", err)
			}
		}
		state = loaded
		return nil
	})
	return state, err
}

func migrateState(s *State) error {
	if s.Version <= 0 || s.Version > stateVersion {
		return fmt.Errorf("不支持的状态版本 %d", s.Version)
	}
	for s.Version < stateVersion {
		switch s.Version {
		case 1:
			// Version 2 formalizes transactional state writes, validation and
			// rolling backups. Existing JSON fields remain wire compatible.
			s.Version = 2
		case 2:
			normalizeDeviceModel(s)
			s.Version = 3
		case 3:
			// Version 4 adds optional real-time rate history and active
			// connection snapshots without changing existing traffic totals.
			s.Version = 4
		case 4:
			// Version 5 adds optional billing-cycle archives and lifecycle
			// alert milestones. Existing usage stays in the current period.
			s.Version = 5
		case 5:
			// Version 6 adds an optional Mihomo client template path. Existing
			// installations continue using the built-in minimal export.
			s.Version = 6
		case 6:
			// Version 7 adds the dynamic single-active-IP binding mode. Existing
			// disabled, automatic and manual policies retain their behavior.
			s.Version = 7
		case 7:
			// Version 8 adds a bounded, per-user recent destination archive.
			// Seed it from existing aggregate node counters so an upgrade starts
			// with useful recent data instead of an empty page.
			seedRecentAccessesFromNodeStats(s, time.Now())
			s.Version = 8
		case 8:
			// Version 9 splits anomaly punishment into soft throttling and hard
			// disconnects. Existing installations adopt the requested soft mode.
			for index := range s.Users {
				policy := normalizedBurst(s.Users[index].Burst)
				policy.Action = burstActionSoft
				s.Users[index].Burst = policy
			}
			s.Version = 9
		default:
			return fmt.Errorf("缺少从状态版本 %d 开始的迁移程序", s.Version)
		}
	}
	return nil
}

func validateState(s *State) error {
	if err := validateHealthSettings(normalizedHealthSettings(s.Health)); err != nil {
		return err
	}
	if err := validateNotificationSettings(normalizedNotificationSettings(s.Notifications)); err != nil {
		return err
	}
	if err := validateSubscriptionSettings(normalizedSubscriptionSettings(s.Subscription)); err != nil {
		return err
	}
	if err := validateFleet(s); err != nil {
		return err
	}
	if err := validateRateMarks(s); err != nil {
		return err
	}
	users := map[string]bool{}
	authUsers := map[string]string{}
	uuids := map[string]string{}
	subscriptionTokens := map[string]string{}
	for _, u := range s.Users {
		name := strings.TrimSpace(u.Name)
		if name == "" {
			return errors.New("存在空用户名")
		}
		key := strings.ToLower(name)
		if users[key] {
			return fmt.Errorf("用户名 %q 重复", u.Name)
		}
		users[key] = true
		if err := validateQuotaMode(u.QuotaMode); err != nil {
			return fmt.Errorf("用户 %s: %w", u.Name, err)
		}
		if u.QuotaBytes < 0 || u.ExtraQuotaBytes < 0 || u.Upload < 0 || u.Download < 0 {
			return fmt.Errorf("用户 %s 的配额或流量不能为负数", u.Name)
		}
		if u.QuotaBytes > 0 && u.ExtraQuotaBytes > int64(^uint64(0)>>1)-u.QuotaBytes {
			return fmt.Errorf("用户 %s 的基础配额与附加流量包之和过大", u.Name)
		}
		if err := validateBilling(u.Billing); err != nil {
			return fmt.Errorf("用户 %s: %w", u.Name, err)
		}
		if err := validateAccessPolicy(u.Access); err != nil {
			return fmt.Errorf("用户 %s: %w", u.Name, err)
		}
		if err := validateRecentAccesses(u); err != nil {
			return fmt.Errorf("用户 %s: %w", u.Name, err)
		}
		if err := validateDate(u.Expires); err != nil {
			return fmt.Errorf("用户 %s: %w", u.Name, err)
		}
		if err := validateThrottle(u.Throttle); err != nil {
			return fmt.Errorf("用户 %s: %w", u.Name, err)
		}
		if err := validateBurst(normalizedBurst(u.Burst)); err != nil {
			return fmt.Errorf("用户 %s: %w", u.Name, err)
		}
		if err := validateIPPolicy(normalizedIPPolicy(u.IPPolicy)); err != nil {
			return fmt.Errorf("用户 %s: %w", u.Name, err)
		}
		deviceNames := map[string]bool{}
		for _, device := range u.Devices {
			deviceKey := strings.ToLower(strings.TrimSpace(device.Name))
			if deviceKey == "" || deviceNames[deviceKey] {
				return fmt.Errorf("用户 %s 存在空设备名或重复设备 %q", u.Name, device.Name)
			}
			deviceNames[deviceKey] = true
			if len(device.SubscriptionToken) < 20 {
				return fmt.Errorf("用户 %s 的设备 %s 缺少有效订阅 token", u.Name, device.Name)
			}
			if owner, exists := subscriptionTokens[device.SubscriptionToken]; exists {
				return fmt.Errorf("设备 %s/%s 与 %s 使用重复订阅 token", u.Name, device.Name, owner)
			}
			subscriptionTokens[device.SubscriptionToken] = u.Name + "/" + device.Name
			if err := validateIPPolicy(normalizedIPPolicy(device.IPPolicy)); err != nil {
				return fmt.Errorf("用户 %s 的设备 %s: %w", u.Name, device.Name, err)
			}
			if err := validateAccessPolicy(device.Access); err != nil {
				return fmt.Errorf("用户 %s 的设备 %s: %w", u.Name, device.Name, err)
			}
		}
		nodeNames := map[string]bool{}
		for _, n := range u.Nodes {
			nodeKey := strings.ToLower(strings.TrimSpace(n.Device)) + "\x00" + strings.ToLower(strings.TrimSpace(n.Name))
			if strings.TrimSpace(n.Name) == "" || nodeNames[nodeKey] {
				return fmt.Errorf("用户 %s 的设备 %s 存在空节点名或重复节点 %q", u.Name, n.Device, n.Name)
			}
			nodeNames[nodeKey] = true
			if !deviceNames[strings.ToLower(strings.TrimSpace(n.Device))] {
				return fmt.Errorf("用户 %s 的节点 %s 引用了不存在的设备 %q", u.Name, n.Name, n.Device)
			}
			if strings.TrimSpace(n.AuthUser) == "" || strings.TrimSpace(n.UUID) == "" {
				return fmt.Errorf("用户 %s 的节点 %s 缺少 auth_user 或 UUID", u.Name, n.Name)
			}
			if owner, exists := authUsers[n.AuthUser]; exists {
				return fmt.Errorf("节点 %s/%s 与 %s 使用重复 auth_user %q", u.Name, n.Name, owner, n.AuthUser)
			}
			authUsers[n.AuthUser] = u.Name + "/" + n.Name
			if owner, exists := uuids[strings.ToLower(n.UUID)]; exists {
				return fmt.Errorf("节点 %s/%s 与 %s 使用重复 UUID", u.Name, n.Name, owner)
			}
			uuids[strings.ToLower(n.UUID)] = u.Name + "/" + n.Name
			if n.Upload < 0 || n.Download < 0 {
				return fmt.Errorf("节点 %s/%s 的流量不能为负数", u.Name, n.Name)
			}
			if err := validateMbps(n.UploadMbps, n.DownloadMbps); err != nil {
				return fmt.Errorf("节点 %s/%s: %w", u.Name, n.Name, err)
			}
		}
	}
	return nil
}

func normalizeLegacyNodeNames(s *State) {
	defaultName := ""
	for _, template := range nodeTemplates(s) {
		if template.Outbound == "" && !strings.EqualFold(template.Name, "默认线路") {
			defaultName = template.Name
			break
		}
	}
	if defaultName == "" {
		return
	}
	for i := range s.Users {
		for j := range s.Users[i].Nodes {
			n := &s.Users[i].Nodes[j]
			if strings.EqualFold(n.Name, "default") && n.Outbound == "" {
				n.Name = defaultName
			}
		}
	}
}
func saveState(path string, s *State) error {
	s.Version = stateVersion
	normalizeQuotaModes(s)
	normalizeDeviceModel(s)
	ensureNodeMarks(s)
	sort.Slice(s.Users, func(i, j int) bool { return s.Users[i].Name < s.Users[j].Name })
	if err := validateState(s); err != nil {
		return fmt.Errorf("拒绝保存无效状态: %w", err)
	}
	if isSQLiteStatePath(path) {
		if err := saveSQLiteState(path, s); err != nil {
			return fmt.Errorf("保存状态数据库: %w", err)
		}
		return nil
	}
	return saveJSONState(path, s)
}

func saveJSONState(path string, s *State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := backupStateBeforeWrite(path, b); err != nil {
		return fmt.Errorf("创建状态备份: %w", err)
	}
	return atomicWrite(path, b, 0600)
}

func backupStateBeforeWrite(path string, next []byte) error {
	previous, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if bytes.Equal(previous, next) {
		return nil
	}
	dir := filepath.Join(filepath.Dir(path), "backups")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(dir, "state.previous.json"), previous, 0600); err != nil {
		return err
	}
	daily := filepath.Join(dir, "state-"+time.Now().Format("20060102")+".json")
	if _, err := os.Stat(daily); errors.Is(err, os.ErrNotExist) {
		if err := atomicWrite(daily, previous, 0600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return pruneDailyStateBackups(dir, 14)
}

func pruneDailyStateBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && len(name) == len("state-20060102.json") && strings.HasPrefix(name, "state-") && strings.HasSuffix(name, ".json") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for len(names) > keep {
		if err := os.Remove(filepath.Join(dir, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}

func ensureNodeMarks(s *State) bool {
	changed := false
	for i := range s.Users {
		for j := range s.Users[i].Nodes {
			n := &s.Users[i].Nodes[j]
			if !validRateMark(n.RateMark) {
				n.RateMark = allocateRateMark(s)
				changed = true
			}
		}
	}
	return changed
}
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".sbmgr-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		_ = os.Remove(path)
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}
func findUser(s *State, name string) *User {
	for i := range s.Users {
		if strings.EqualFold(s.Users[i].Name, name) {
			return &s.Users[i]
		}
	}
	return nil
}
func userIndex(s *State, name string) int {
	for i := range s.Users {
		if strings.EqualFold(s.Users[i].Name, name) {
			return i
		}
	}
	return -1
}
func countNodes(users []User) int {
	n := 0
	for _, u := range users {
		n += len(u.Nodes)
	}
	return n
}
func stringValue(v any) string { s, _ := v.(string); return s }
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
func formatDisplayTime(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return parsed.In(applicationLocation()).Format("2006-01-02 15:04:05")
}
func absOrOriginal(p string) string {
	a, err := filepath.Abs(p)
	if err == nil {
		return a
	}
	return p
}
func defaultStatePath() string {
	if home := strings.TrimSpace(os.Getenv("SBMGR_HOME")); home != "" {
		return filepath.Join(home, "state.db")
	}
	if executable, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(executable), "state.db")
	}
	return "state.db"
}
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	r := strings.NewReplacer(" ", "-", "/", "-", "\\", "-", ":", "-")
	return r.Replace(s)
}
func uniqueAuthUser(s *State, base string) string {
	used := map[string]bool{}
	for _, auth := range s.ReservedAuthUsers {
		used[auth] = true
	}
	for _, u := range s.Users {
		for _, n := range u.Nodes {
			used[n.AuthUser] = true
		}
	}
	if !used[base] {
		return base
	}
	for i := 2; ; i++ {
		v := fmt.Sprintf("%s-%d", base, i)
		if !used[v] {
			return v
		}
	}
}
func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
func validateDate(s string) error {
	if s == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return fmt.Errorf("无效到期日 %q，应为 YYYY-MM-DD", s)
	}
	return nil
}
func expired(u User, now time.Time) bool {
	return u.Expires != "" && now.In(applicationLocation()).Format("2006-01-02") > u.Expires
}
func disableReason(u User, now time.Time) string {
	if expired(u, now) {
		return "已过期"
	}
	return "流量配额已用完"
}

func automaticDisableReason(u User, now time.Time) string {
	if expired(u, now) {
		return "expired"
	}
	if overQuota(u) {
		return "quota"
	}
	return "automatic"
}
func formatQuota(n int64) string {
	if n == 0 {
		return "unlimited"
	}
	return formatSize(n)
}
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" || s == "0" {
		return 0, nil
	}
	mult := int64(1)
	// Longest suffixes must be checked first. This also makes explicit byte
	// values such as 2B round-trip through formatSize instead of failing.
	units := []struct {
		name string
		mult int64
	}{
		{"KIB", 1 << 10}, {"MIB", 1 << 20}, {"GIB", 1 << 30}, {"TIB", 1 << 40},
		{"KB", 1 << 10}, {"MB", 1 << 20}, {"GB", 1 << 30}, {"TB", 1 << 40},
		{"K", 1 << 10}, {"M", 1 << 20}, {"G", 1 << 30}, {"T", 1 << 40}, {"B", 1},
	}
	for _, unit := range units {
		if strings.HasSuffix(s, unit.name) {
			mult = unit.mult
			s = strings.TrimSpace(strings.TrimSuffix(s, unit.name))
			break
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("无效容量 %q", s)
	}
	return int64(v * float64(mult)), nil
}
func formatSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for q := n / unit; q >= unit; q /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
func yamlQuote(s string) string { return strconv.Quote(s) }
