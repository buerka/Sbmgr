package main

import (
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/skip2/go-qrcode"
)

type SubscriptionSettings struct {
	Enabled     bool   `json:"enabled,omitempty"`
	Listen      string `json:"listen,omitempty"`
	BaseURL     string `json:"base_url,omitempty"`
	TLSCertFile string `json:"tls_cert_file,omitempty"`
	TLSKeyFile  string `json:"tls_key_file,omitempty"`
}

type subscriptionCertificateInfo struct {
	Enabled     bool
	NotBefore   time.Time
	NotAfter    time.Time
	DNSNames    []string
	IPAddresses []string
}

func subscriptionTLSInfo(settings SubscriptionSettings) (subscriptionCertificateInfo, error) {
	if settings.TLSCertFile == "" && settings.TLSKeyFile == "" {
		return subscriptionCertificateInfo{}, nil
	}
	if settings.TLSCertFile == "" || settings.TLSKeyFile == "" {
		return subscriptionCertificateInfo{}, errors.New("订阅 HTTPS 必须同时配置 TLS 证书和私钥路径")
	}
	pair, err := tls.LoadX509KeyPair(settings.TLSCertFile, settings.TLSKeyFile)
	if err != nil {
		return subscriptionCertificateInfo{}, fmt.Errorf("订阅 TLS 证书与私钥无法配对: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return subscriptionCertificateInfo{}, errors.New("订阅 TLS 证书链为空")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return subscriptionCertificateInfo{}, fmt.Errorf("解析订阅 TLS 证书: %w", err)
	}
	info := subscriptionCertificateInfo{
		Enabled:   true,
		NotBefore: leaf.NotBefore,
		NotAfter:  leaf.NotAfter,
		DNSNames:  append([]string(nil), leaf.DNSNames...),
	}
	for _, ip := range leaf.IPAddresses {
		info.IPAddresses = append(info.IPAddresses, ip.String())
	}
	return info, nil
}

func (info subscriptionCertificateInfo) status(now time.Time) string {
	if !info.Enabled {
		return "未启用"
	}
	if now.Before(info.NotBefore) {
		return "尚未生效"
	}
	if !now.Before(info.NotAfter) {
		return "已过期"
	}
	if info.NotAfter.Sub(now) <= 48*time.Hour {
		return "即将过期"
	}
	return "有效"
}

func normalizedSubscriptionSettings(settings SubscriptionSettings) SubscriptionSettings {
	if settings.Listen == "" {
		settings.Listen = "127.0.0.1:18080"
	}
	return settings
}

func validateSubscriptionSettings(settings SubscriptionSettings) error {
	settings = normalizedSubscriptionSettings(settings)
	_, _, err := net.SplitHostPort(settings.Listen)
	if err != nil {
		return fmt.Errorf("订阅监听地址 %q 必须是 host:port", settings.Listen)
	}
	if settings.BaseURL != "" {
		parsed, err := url.Parse(settings.BaseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return errors.New("订阅基础 URL 必须是有效的 http 或 https 地址")
		}
		if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("订阅基础 URL 不能包含账号、查询参数或片段")
		}
	}
	return nil
}

// validateSubscriptionRuntime checks external files and fail-closed exposure
// rules only when the listener is about to be enabled. State loading uses the
// structural validator above so a missing/rotating certificate can never lock
// the administrator out of the CLI needed to disable or repair HTTPS.
func validateSubscriptionRuntime(settings SubscriptionSettings) error {
	settings = normalizedSubscriptionSettings(settings)
	if err := validateSubscriptionSettings(settings); err != nil {
		return err
	}
	host, _, _ := net.SplitHostPort(settings.Listen)
	tlsEnabled := settings.TLSCertFile != "" || settings.TLSKeyFile != ""
	if tlsEnabled {
		if settings.TLSCertFile == "" || settings.TLSKeyFile == "" {
			return errors.New("订阅 HTTPS 必须同时配置 TLS 证书和私钥路径")
		}
		if !filepath.IsAbs(settings.TLSCertFile) || !filepath.IsAbs(settings.TLSKeyFile) {
			return errors.New("订阅 TLS 证书和私钥必须使用绝对路径")
		}
		if err := readableRegularFile(settings.TLSCertFile, "TLS 证书"); err != nil {
			return err
		}
		if err := readableRegularFile(settings.TLSKeyFile, "TLS 私钥"); err != nil {
			return err
		}
		if _, err := subscriptionTLSInfo(settings); err != nil {
			return err
		}
	}
	if !localSubscriptionHost(host) && !tlsEnabled {
		return fmt.Errorf("订阅监听地址 %q 可被外部访问，必须配置 TLS 证书和私钥；无 TLS 时仅允许监听 localhost 或回环地址", settings.Listen)
	}
	if settings.BaseURL != "" {
		parsed, _ := url.Parse(settings.BaseURL)
		if parsed.Scheme == "http" && !localSubscriptionHost(parsed.Hostname()) {
			return errors.New("公网订阅基础 URL 必须使用 https；http 仅允许 localhost 或回环地址")
		}
	}
	return nil
}

func readableRegularFile(path, label string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("无法读取订阅%s %q: %w", label, path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("订阅%s %q 不是普通文件", label, path)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("无法读取订阅%s %q: %w", label, path, err)
	}
	return file.Close()
}

func localSubscriptionHost(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func newSubscriptionToken() string {
	data := make([]byte, 24)
	if _, err := rand.Read(data); err != nil {
		return strings.ReplaceAll(newUUID(), "-", "")
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func subscriptionURL(s *State, device Device) string {
	settings := normalizedSubscriptionSettings(s.Subscription)
	base := strings.TrimRight(settings.BaseURL, "/")
	if base == "" {
		scheme := "http"
		if settings.TLSCertFile != "" && settings.TLSKeyFile != "" {
			scheme = "https"
		}
		base = scheme + "://" + settings.Listen
	}
	return base + "/sub/" + device.SubscriptionToken
}

func findSubscriptionDevice(s *State, token string) (*User, *Device) {
	for userIndex := range s.Users {
		u := &s.Users[userIndex]
		for deviceIndex := range u.Devices {
			device := &u.Devices[deviceIndex]
			if len(token) == len(device.SubscriptionToken) && subtle.ConstantTimeCompare([]byte(token), []byte(device.SubscriptionToken)) == 1 {
				return u, device
			}
		}
	}
	return nil, nil
}

type subscriptionRateEntry struct {
	window time.Time
	count  int
}

func subscriptionClientIP(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	remoteIP := net.ParseIP(strings.Trim(host, "[]"))
	// Limit the authenticated TCP peer. Forwarded headers are untrusted,
	// including on loopback; local reverse proxies share a peer budget.
	if remoteIP != nil {
		return remoteIP.String()
	}
	return host
}

func subscriptionReportedUsage(u User) (upload, download int64) {
	// Keep this helper separate from the response formatting: quota modes can
	// select a billable direction here without changing the wire format.
	upload, download = max(int64(0), u.Upload), max(int64(0), u.Download)
	switch normalizedQuotaMode(u.QuotaMode) {
	case quotaModeUpload:
		return upload, 0
	case quotaModeDownload:
		return 0, download
	default:
		return upload, download
	}
}

func subscriptionUserInfo(u User) (string, error) {
	upload, download := subscriptionReportedUsage(u)
	expire := int64(0)
	if u.Expires != "" {
		date, err := time.ParseInLocation("2006-01-02", u.Expires, applicationLocation())
		if err != nil {
			return "", fmt.Errorf("解析用户到期日 %q: %w", u.Expires, err)
		}
		// The configured date remains valid for its whole Asia/Shanghai day.
		expire = date.AddDate(0, 0, 1).Add(-time.Second).Unix()
	}
	return fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d", upload, download, userQuota(u), expire), nil
}

func subscriptionProfileTitle(userName, deviceName string) string {
	clean := func(value string) string {
		value = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || r == '/' || r == '\\' {
				return '-'
			}
			return r
		}, strings.TrimSpace(value))
		return strings.Trim(value, " .-")
	}
	userName, deviceName = clean(userName), clean(deviceName)
	switch {
	case userName == "" && deviceName == "":
		return "sbmgr-subscription"
	case userName == "":
		return deviceName
	case deviceName == "":
		return userName
	default:
		return userName + "-" + deviceName
	}
}

func setSubscriptionProfileHeaders(response http.ResponseWriter, u User, device Device) error {
	return setSubscriptionProfileHeadersAt(response, u, device, time.Now())
}

func setSubscriptionProfileHeadersAt(response http.ResponseWriter, u User, device Device, now time.Time) error {
	headers, err := subscriptionProfileHeadersAt(u, device, now)
	if err != nil {
		return err
	}
	for name := range headers {
		response.Header()[name] = headers[name]
	}
	return nil
}

func subscriptionProfileHeadersAt(u User, device Device, now time.Time) (http.Header, error) {
	headers := make(http.Header)
	userinfo, err := subscriptionUserInfo(u)
	if err != nil {
		return nil, err
	}
	title := subscriptionProfileTitle(u.Name, device.Name)
	headers.Set("Subscription-Userinfo", userinfo)
	headers.Set("Profile-Update-Interval", "1")
	headers.Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(title)))
	filename := title + "-" + now.In(applicationLocation()).Format("20060102-150405") + ".yaml"
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	if disposition == "" {
		disposition = "attachment; filename=subscription-" + now.In(applicationLocation()).Format("20060102-150405") + ".yaml"
	}
	headers.Set("Content-Disposition", disposition)
	headers.Set("Content-Type", "application/yaml; charset=utf-8")
	return headers, nil
}

func subscriptionDeviceAvailable(u User, device Device, now time.Time) error {
	if !u.Enabled {
		return errors.New("用户已禁用")
	}
	if expired(u, now) {
		return errors.New("用户已过期")
	}
	if overQuota(u) {
		return errors.New("用户已用完流量")
	}
	if burstHardBlocked(u, now) {
		return fmt.Errorf("用户因异常流量被临时封禁至 %s", u.BlockedUntil)
	}
	if !device.Enabled {
		return fmt.Errorf("设备 %q 已禁用", device.Name)
	}
	if len(nodesForDevice(u, device.Name)) == 0 {
		return fmt.Errorf("设备 %q 没有可导出的节点", device.Name)
	}
	return nil
}

func subscriptionQRText(link string) string {
	code, err := qrcode.New(link, qrcode.Medium)
	if err != nil {
		return ""
	}
	bitmap := code.Bitmap()
	var result strings.Builder
	for _, row := range bitmap {
		for _, dark := range row {
			if dark {
				result.WriteString("██")
			} else {
				result.WriteString("  ")
			}
		}
		result.WriteByte('\n')
	}
	return strings.TrimSuffix(result.String(), "\n")
}

func (a *app) subscriptionCmd(args []string) error {
	return a.withAuditedStateLock(auditAction("subscription", args), args, func() error { return a.subscriptionCmdLocked(args) })
}

func (a *app) subscriptionCmdLocked(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: sbmgr admin subscription status|set|link|qr")
	}
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	switch args[0] {
	case "status":
		settings := normalizedSubscriptionSettings(s.Subscription)
		fmt.Fprintf(a.out, "enabled=%v listen=%s base_url=%s tls_cert=%s tls_key=%s\n", settings.Enabled, settings.Listen, dash(settings.BaseURL), dash(settings.TLSCertFile), dash(settings.TLSKeyFile))
		return nil
	case "set":
		fs := a.newFlagSet("subscription set")
		enabled := fs.String("enabled", "", "true 或 false")
		listen := fs.String("listen", "", "监听 host:port")
		baseURL := fs.String("base-url", "", "外部访问基础 URL")
		tlsCert := fs.String("tls-cert", "", "TLS 证书绝对路径；与 --tls-key 同时配置，留空则关闭原生 HTTPS")
		tlsKey := fs.String("tls-key", "", "TLS 私钥绝对路径；与 --tls-cert 同时配置，留空则关闭原生 HTTPS")
		restart := fs.Bool("restart", false, "保存后重启 sbmgr.service")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		settings := normalizedSubscriptionSettings(s.Subscription)
		changed := false
		fs.Visit(func(flag *flag.Flag) {
			if flag.Name == "restart" {
				return
			}
			changed = true
			switch flag.Name {
			case "enabled":
				settings.Enabled, err = strconv.ParseBool(*enabled)
			case "listen":
				settings.Listen = strings.TrimSpace(*listen)
			case "base-url":
				settings.BaseURL = strings.TrimRight(strings.TrimSpace(*baseURL), "/")
			case "tls-cert":
				settings.TLSCertFile = strings.TrimSpace(*tlsCert)
			case "tls-key":
				settings.TLSKeyFile = strings.TrimSpace(*tlsKey)
			}
		})
		if err != nil {
			return errors.New("--enabled 必须是 true 或 false")
		}
		if !changed {
			return errors.New("没有指定要修改的订阅设置")
		}
		validate := validateSubscriptionSettings
		if settings.Enabled {
			validate = validateSubscriptionRuntime
		}
		if err := validate(settings); err != nil {
			return err
		}
		s.Subscription = settings
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "已更新订阅设置")
		if *restart && runtime.GOOS == "linux" {
			if err := exec.Command("systemctl", "restart", "sbmgr").Run(); err != nil {
				return fmt.Errorf("设置已保存，但重启 sbmgr 失败: %w", err)
			}
			fmt.Fprintln(a.out, "sbmgr 已重启")
		}
		return nil
	case "link", "qr":
		if len(args) < 3 {
			return fmt.Errorf("用法: sbmgr admin subscription %s USER DEVICE", args[0])
		}
		u := findUser(s, args[1])
		if u == nil {
			return fmt.Errorf("用户 %q 不存在", args[1])
		}
		device := findDevice(u, args[2])
		if device == nil {
			return fmt.Errorf("设备 %q 不存在", args[2])
		}
		link := subscriptionURL(s, *device)
		if args[0] == "link" {
			fmt.Fprintln(a.out, link)
			return nil
		}
		fs := a.newFlagSet("subscription qr")
		output := fs.String("output", "", "PNG 输出路径；留空输出终端二维码")
		if err := fs.Parse(args[3:]); err != nil {
			return err
		}
		if *output == "" {
			fmt.Fprintln(a.out, subscriptionQRText(link))
			return nil
		}
		png, err := qrcode.Encode(link, qrcode.Medium, 512)
		if err != nil {
			return err
		}
		if err := atomicWrite(*output, png, 0600); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "已导出", *output)
		return nil
	default:
		return fmt.Errorf("未知 subscription 子命令 %q", args[0])
	}
}

func defaultSubscriptionQRPath(statePath, user, device string) string {
	filename := fmt.Sprintf("%s-%s-subscription.png", slug(user), slug(device))
	return filepath.Join(filepath.Dir(statePath), "exports", filename)
}
