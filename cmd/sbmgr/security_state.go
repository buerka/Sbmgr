package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

func validateManagedName(name string) error {
	if strings.TrimSpace(name) != name || name == "" || len(name) > 256 || !utf8.ValidString(name) {
		return errors.New("名称必须为 1–256 字节且首尾无空白")
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && !strings.ContainsRune(" -_.():@", r) {
			return errors.New("名称仅允许文字、数字、空格和 -_.():@；不允许斜杠或控制字符")
		}
	}
	return nil
}

func canonicalConfigPath(path string) string {
	p, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	if parent, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(parent, filepath.Base(p))
	}
	return p
}

func validateConfigPaths(base, live string) error {
	if base == "" || live == "" {
		return nil
	}
	a, b := canonicalConfigPath(base), canonicalConfigPath(live)
	same := a == b
	if ai, err := os.Stat(a); err == nil {
		if bi, err := os.Stat(b); err == nil {
			same = same || os.SameFile(ai, bi)
		}
	}
	if same {
		return errors.New("基础模板与运行配置必须是不同文件（含符号链接和硬链接）；请修正 BaseConfig/ConfigPath")
	}
	return nil
}

var serviceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:@-]{0,254}$`)
var binaryNamePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

func validateRuntimeSettings(s *State) error {
	if err := validateConfigPaths(s.BaseConfig, s.ConfigPath); err != nil {
		return err
	}
	if s.Service != "" && !serviceNamePattern.MatchString(s.Service) {
		return errors.New("Service 必须是有效 systemd 单元名称，不能包含选项或控制字符")
	}
	if s.SingBoxBin != "" && (safeTerminalText(s.SingBoxBin) != s.SingBoxBin || (!filepath.IsAbs(s.SingBoxBin) && !binaryNamePattern.MatchString(s.SingBoxBin))) {
		return errors.New("SingBoxBin 必须为绝对路径或 PATH 中的单一程序名，不能包含选项或控制字符")
	}
	if s.StatsAPI != "" {
		if strings.HasPrefix(s.StatsAPI, "unix:") {
			p := strings.TrimPrefix(s.StatsAPI, "unix:")
			if !filepath.IsAbs(p) || safeTerminalText(p) != p {
				return errors.New("StatsAPI Unix socket 必须为绝对路径")
			}
		} else {
			h, p, err := net.SplitHostPort(s.StatsAPI)
			port, pe := strconv.Atoi(p)
			if err != nil || pe != nil || port < 1 || port > 65535 || !localSubscriptionHost(h) {
				return errors.New("StatsAPI 仅允许 localhost/回环 IP 的 host:port 或本机 Unix socket")
			}
		}
	}
	return nil
}

func inboundAuthNames(cfg map[string]any) map[string]bool {
	names := map[string]bool{}
	inbounds, _ := cfg["inbounds"].([]any)
	for _, raw := range inbounds {
		in, _ := raw.(map[string]any)
		users, _ := in["users"].([]any)
		for _, rawUser := range users {
			u, _ := rawUser.(map[string]any)
			for _, key := range []string{"name", "username"} {
				if n := stringValue(u[key]); n != "" {
					names[n] = true
				}
			}
		}
	}
	return names
}

func validateBaseIdentities(s *State, cfg map[string]any) error {
	names := inboundAuthNames(cfg)
	for _, u := range s.Users {
		for _, n := range u.Nodes {
			if names[n.AuthUser] {
				return fmt.Errorf("用户 %s 的节点 %s 与基础模板中的非托管身份重名；请改名或明确导入后再应用", u.Name, n.Name)
			}
		}
	}
	return nil
}
