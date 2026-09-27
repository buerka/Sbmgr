package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

const personalSiteBlockLimit = 200

// A personal entry is a canonical DNS suffix. Raw URLs never enter state or logs.
func normalizePersonalSiteBlock(input string) (string, error) {
	bad := errors.New("请输入有效的网站域名或 HTTP(S) 地址")
	input = strings.TrimSpace(input)
	if len(input) == 0 || len(input) > 2048 || strings.ContainsAny(input, "\r\n\t\\") {
		return "", bad
	}
	var host string
	if strings.Contains(input, "://") {
		u, err := url.Parse(input)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Opaque != "" {
			return "", bad
		}
		host = u.Hostname()
		if p := u.Port(); p != "" {
			n, err := strconv.Atoi(p)
			if err != nil || n < 1 || n > 65535 {
				return "", bad
			}
		}
	} else {
		if strings.ContainsAny(input, "/?#@: ") {
			return "", bad
		}
		host = input
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "*."), ".")
	if host == "" || net.ParseIP(host) != nil || strings.ContainsAny(host, "*%[]") {
		return "", bad
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return "", bad
	}
	ascii = strings.ToLower(ascii)
	if len(ascii) > 253 || strings.Contains(ascii, "..") {
		return "", bad
	}
	for _, label := range strings.Split(ascii, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", bad
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return "", bad
			}
		}
	}
	if _, err := publicsuffix.EffectiveTLDPlusOne(ascii); err != nil {
		return "", bad
	}
	return ascii, nil
}

func validatePersonalSiteBlocks(domains []string) error {
	if len(domains) > personalSiteBlockLimit {
		return errors.New("个人网站限制超过上限")
	}
	for i, domain := range domains {
		normalized, err := normalizePersonalSiteBlock(domain)
		if err != nil || normalized != domain || (i > 0 && domains[i-1] >= domain) {
			return errors.New("个人网站限制条目无效或重复")
		}
	}
	return nil
}

func personalSiteBlockVersion(u *User) string {
	raw, _ := json.Marshal(struct {
		Owner   string   `json:"owner"`
		Domains []string `json:"domains"`
	}{u.Name, append([]string{}, u.PersonalBlockedDomains...)})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func changePersonalSiteBlock(u *User, action, input, expected string) (bool, error) {
	if expected != personalSiteBlockVersion(u) {
		return false, errors.New("网站列表已更新，请刷新后重试")
	}
	if action != "add" && action != "remove" {
		return false, errors.New("网站操作不支持")
	}
	domain, err := normalizePersonalSiteBlock(input)
	if err != nil {
		return false, err
	}
	i := sort.SearchStrings(u.PersonalBlockedDomains, domain)
	exists := i < len(u.PersonalBlockedDomains) && u.PersonalBlockedDomains[i] == domain
	if action == "add" {
		if exists {
			return false, nil
		}
		if len(u.PersonalBlockedDomains) >= personalSiteBlockLimit {
			return false, errors.New("个人网站限制已达上限")
		}
		u.PersonalBlockedDomains = append(u.PersonalBlockedDomains, domain)
		sort.Strings(u.PersonalBlockedDomains)
		return true, nil
	}
	if !exists {
		return false, nil
	}
	u.PersonalBlockedDomains = append(u.PersonalBlockedDomains[:i], u.PersonalBlockedDomains[i+1:]...)
	return true, nil
}
