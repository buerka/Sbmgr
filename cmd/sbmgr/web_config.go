package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// Web credentials belong to the local control service, not the replicated
// business state. A slave never receives the administrator password hash.
type webConfig struct {
	Version      int    `json:"version"`
	Listen       string `json:"listen"`
	Origin       string `json:"origin"`
	BasePath     string `json:"base_path,omitempty"`
	Username     string `json:"username"`
	Salt         string `json:"salt"`
	PasswordHash string `json:"password_hash"`
	TLSCert      string `json:"tls_cert,omitempty"`
	TLSKey       string `json:"tls_key,omitempty"`
}

func webConfigPath(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), "web-admin.json")
}

func validateWebConfig(c webConfig) error {
	if err := validateWebBasePath(c.BasePath); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return errors.New("Web 监听地址必须是 host:port")
	}
	u, err := url.Parse(c.Origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("Web origin 必须是完整来源地址，例如 https://admin.example:9443，不能带路径")
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return errors.New("必须同时提供 TLS 证书和私钥")
	}
	if !localSubscriptionHost(host) && c.TLSCert == "" {
		return errors.New("非回环 Web 监听必须配置 TLS；反向代理请监听 127.0.0.1")
	}
	if c.TLSCert != "" && u.Scheme != "https" {
		return errors.New("TLS 监听必须使用 https origin")
	}
	if c.Version != 1 || strings.TrimSpace(c.Username) == "" || len(c.Username) > 64 || strings.IndexFunc(c.Username, unicode.IsControl) >= 0 {
		return errors.New("无效的 Web 管理员设置")
	}
	salt, e1 := hex.DecodeString(c.Salt)
	hash, e2 := hex.DecodeString(c.PasswordHash)
	if e1 != nil || e2 != nil || len(salt) != 32 || len(hash) != 32 {
		return errors.New("无效的 Web 密码摘要")
	}
	return nil
}

// Keep a single unambiguous URL segment; no escapes, separators or dot segments.
// An empty path preserves existing installations at the origin root.
func validateWebBasePath(base string) error {
	if base == "" {
		return nil
	}
	if len(base) < 2 || len(base) > 129 || base[0] != '/' {
		return errors.New("Web 路径须为 / 加 1–128 位英文字母、数字、下划线或连字符")
	}
	for _, ch := range base[1:] {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-') {
			return errors.New("Web 路径须为单层路径，只含英文字母、数字、下划线或连字符")
		}
	}
	return nil
}

func readWebConfig(statePath string) (webConfig, error) {
	var c webConfig
	f, err := os.Open(webConfigPath(statePath))
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 16385))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil {
		return c, errors.New("Web 管理配置不可读")
	}
	if err := validateWebConfig(c); err != nil {
		return c, err
	}
	return c, nil
}

func webPasswordHash(password, salt string) (string, error) {
	key, err := pbkdf2.Key(sha256.New, password, []byte(salt), 600000, 32)
	return hex.EncodeToString(key), err
}

func (a *app) webCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: sbmgr web configure|status；sbmgr serve 启动 Web 与后台维护")
	}
	if args[0] == "status" && len(args) == 1 {
		c, err := readWebConfig(a.statePath)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "Web: %s\n监听: %s\n管理员: %s\n", c.Origin, c.Listen, c.Username)
		return nil
	}
	if args[0] != "configure" {
		return errors.New("未知 Web 操作；使用 configure 或 status")
	}
	fs := a.newFlagSet("web configure")
	listen := fs.String("listen", "127.0.0.1:9090", "Web 监听地址")
	origin := fs.String("origin", "", "浏览器访问来源；默认按监听地址生成")
	basePath := fs.String("base-path", "", "页面与 API 的统一路径前缀；默认根路径，修改后需重启")
	username := fs.String("username", "admin", "管理员账号")
	passwordFile := fs.String("password-file", "", "从文件读取密码，不在参数或日志中传入密码")
	passwordStdin := fs.Bool("password-stdin", false, "从标准输入读取密码，适合自动化")
	cert := fs.String("tls-cert", "", "TLS 证书路径")
	key := fs.String("tls-key", "", "TLS 私钥路径")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || (*passwordFile == "") == !*passwordStdin {
		return errors.New("必须且只能使用 --password-file 或 --password-stdin")
	}
	var r io.Reader = os.Stdin
	if *passwordFile != "" {
		f, err := os.Open(*passwordFile)
		if err != nil {
			return errors.New("无法读取密码文件")
		}
		defer f.Close()
		r = f
	}
	password, err := io.ReadAll(io.LimitReader(r, 1025))
	if err != nil {
		return errors.New("无法读取密码")
	}
	defer clear(password)
	password = []byte(strings.TrimRight(string(password), "\r\n"))
	if len(password) < 12 || len(password) > 1024 {
		return errors.New("管理员密码长度须为 12–1024 字节")
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	c := webConfig{Version: 1, Listen: *listen, Origin: strings.TrimSuffix(*origin, "/"), BasePath: strings.TrimSuffix(*basePath, "/"), Username: strings.TrimSpace(*username), Salt: hex.EncodeToString(salt), TLSCert: *cert, TLSKey: *key}
	if c.Origin == "" {
		scheme := "http"
		if c.TLSCert != "" {
			scheme = "https"
		}
		c.Origin = scheme + "://" + c.Listen
	}
	c.PasswordHash, err = webPasswordHash(string(password), c.Salt)
	if err != nil {
		return err
	}
	if err := validateWebConfig(c); err != nil {
		return err
	}
	if c.TLSCert != "" {
		if err := readableRegularFile(c.TLSCert, "Web TLS 证书"); err != nil {
			return err
		}
		if err := readableRegularFile(c.TLSKey, "Web TLS 私钥"); err != nil {
			return err
		}
	}
	return a.withStateLock(func() error {
		data, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			return err
		}
		if err := atomicWrite(webConfigPath(a.statePath), append(data, '\n'), 0600); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "Web 管理已配置：%s\n运行 sbmgr serve 启动；已运行的服务需重启。旧登录会话随配置更新失效。\n", c.Origin)
		return nil
	})
}
