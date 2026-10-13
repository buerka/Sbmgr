package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

type webCredentialInput struct {
	Username string `json:"username,omitempty"`
	User     string `json:"user,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
	Current  string `json:"current_password,omitempty"`
	Password string `json:"new_password,omitempty"`
	Reset    string `json:"password,omitempty"`
}

// These dedicated operations never enter generic action arguments or durable
// task records. The closure retains the request only until hashing completes.
func (b *webBackend) startCredentialJobLocked(q webRequest, session webSession, config webConfig, now time.Time) webReply {
	var input webCredentialInput
	if webDecode(q.Body, &input) != nil || len(input.Username) > 64 || len(input.User) > 64 || len(input.Current) > 1024 || len(input.Password) > 1024 || len(input.Reset) > 1024 {
		return webError(400, "账号设置格式不正确")
	}
	input.Username = strings.TrimSpace(input.Username)
	switch q.Path {
	case "/api/portal-account":
		if input.Enabled == nil || input.User == "" || input.Current != "" || input.Password != "" || input.Username != "" {
			return webError(400, "登录账号设置格式不正确")
		}
		input.Password, input.Reset = input.Reset, ""
	case "/api/me/password":
		if len(input.Password) < 12 || input.User != "" || input.Username != "" || input.Enabled != nil || input.Reset != "" {
			return webError(400, "新密码长度须为 12–1024 字节")
		}
	case "/api/account":
		if input.Username == "" || input.User != "" || input.Enabled != nil || input.Reset != "" {
			return webError(400, "账号设置格式不正确")
		}
	default:
		return webError(404, "接口不存在")
	}
	if input.Password != "" && len(input.Password) < 12 {
		return webError(400, "密码长度须为 12–1024 字节")
	}
	key := "account"
	if session.Role == "user" {
		key = "portal-password:" + session.Epoch
	}
	if q.Path != "/api/portal-account" {
		attempt := b.attempts[key]
		if now.Before(attempt.Until) && attempt.Count >= 5 {
			return webError(429, "当前密码尝试过于频繁，请 5 分钟后重试")
		}
	}
	path := q.Path
	return b.startJobLocked(q, "保存登录设置", func() (string, bool, error) {
		// A deadline limits lock acquisition; a committed write is never reported
		// as failed simply because the browser stopped waiting.
		ctx, cancel := context.WithTimeout(context.Background(), stateLockWaitLimit)
		defer cancel()
		var warnings bytes.Buffer
		a := &app{statePath: b.a.statePath, out: io.Discard, err: &warnings, actor: "web:" + session.Username}
		if session.Role == "user" {
			a.actor = "portal:" + session.Username
		}
		var message string
		var err error
		if path == "/api/account" {
			message, err = b.updateAdminCredentials(ctx, a, config, &input, key)
		} else {
			message, err = b.updatePortalCredentials(ctx, a, config, session, &input, key, path == "/api/me/password")
		}
		input = webCredentialInput{}
		if err != nil {
			return "", false, err
		}
		if warnings.Len() > 0 {
			message += "；操作已保存，但审计记录写入失败，请检查服务器。"
		}
		return message, path != "/api/portal-account", nil
	})
}

func (b *webBackend) checkCurrentPassword(password, salt, expected, key string) error {
	hash, err := webPasswordHash(password, salt)
	if err == nil && subtle.ConstantTimeCompare([]byte(hash), []byte(expected)) == 1 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	attempt := b.attempts[key]
	now := time.Now()
	if !now.Before(attempt.Until) {
		attempt = webAttempt{Until: now.Add(5 * time.Minute)}
	}
	attempt.Count++
	b.attempts[key] = attempt
	return errors.New("当前密码不正确")
}

func (b *webBackend) updateAdminCredentials(ctx context.Context, a *app, expected webConfig, input *webCredentialInput, key string) (string, error) {
	if err := b.checkCurrentPassword(input.Current, expected.Salt, expected.PasswordHash, key); err != nil {
		return "", err
	}
	input.Current = ""
	next := expected
	next.Username = input.Username
	if input.Password != "" {
		next.Salt = webRandom()
		hash, err := webPasswordHash(input.Password, next.Salt)
		input.Password = ""
		if err != nil {
			return "", errors.New("无法生成密码摘要")
		}
		next.PasswordHash = hash
	}
	if err := validateWebConfig(next); err != nil {
		return "", err
	}
	if next == expected {
		return "", errors.New("账号设置未发生变化")
	}
	err := a.withAuditedStateLockContext(ctx, "web.account", nil, func() error {
		current, err := readWebConfig(a.statePath)
		if err != nil || current != expected {
			return errors.New("管理设置已变化，请重新登录后再修改")
		}
		state, err := loadState(a.statePath)
		if err != nil {
			return errors.New("读取用户状态失败，无法检查登录名")
		}
		if user := findUser(state, next.Username); user != nil && user.Portal != nil {
			return errors.New("管理员登录名不能与已开通或已邀请的用户重名")
		}
		data, err := json.MarshalIndent(next, "", "  ")
		if err != nil {
			return errors.New("无法保存管理设置")
		}
		if err := atomicWrite(webConfigPath(a.statePath), append(data, '\n'), 0600); err != nil {
			return errors.New("无法保存管理设置，原账号保持不变")
		}
		return nil
	})
	if err != nil {
		return "", errors.New("账号保存未完成：" + err.Error())
	}
	b.mu.Lock()
	clear(b.sessions)
	delete(b.attempts, key)
	b.config = next
	b.mu.Unlock()
	return "管理账号已更新，请使用新账号重新登录。", nil
}

func (b *webBackend) updatePortalCredentials(ctx context.Context, a *app, config webConfig, session webSession, input *webCredentialInput, key string, self bool) (string, error) {
	username := input.User
	if self {
		username = session.Username
	}
	var expected PortalAccount
	err := a.withStateLockContext(ctx, func() error {
		s, err := loadState(a.statePath)
		if err != nil {
			return errors.New("读取用户状态失败")
		}
		if s.Mesh == nil && s.MeshAgent.Cluster != "" {
			return errors.New("请在主机管理用户登录账号")
		}
		u := findUser(s, username)
		if u == nil {
			return errors.New("用户不存在")
		}
		if len(u.Name) > 64 || strings.EqualFold(u.Name, config.Username) {
			return errors.New("登录名不能与管理员重名，且不能超过 64 字节")
		}
		if u.Portal == nil || (u.Portal.PasswordHash == "" && (self || *input.Enabled || input.Password != "")) {
			return errors.New("首次开通请生成邀请，由用户自行设置密码")
		}
		expected = *u.Portal
		if self && (!expected.Enabled || expected.Epoch != session.Epoch) {
			return errors.New("登录已失效，请重新登录")
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if self {
		if err := b.checkCurrentPassword(input.Current, expected.Salt, expected.PasswordHash, key); err != nil {
			return "", err
		}
	}
	input.Current = ""
	next := expected
	next.InviteHash, next.InviteExpires = "", ""
	next.Epoch = webRandom()
	if !self {
		next.Enabled = *input.Enabled
	}
	if input.Password != "" {
		next.Salt = webRandom()
		hash, err := webPasswordHash(input.Password, next.Salt)
		input.Password = ""
		if err != nil {
			return "", errors.New("生成密码摘要失败")
		}
		next.PasswordHash = hash
	}
	action := "portal.account"
	if self {
		action = "portal.password"
	}
	err = a.withAuditedStateLockContext(ctx, action, nil, func() error {
		current, err := readWebConfig(a.statePath)
		if err != nil || current != config {
			return errors.New("管理设置已变化，请重新登录后再修改")
		}
		s, err := loadState(a.statePath)
		if err != nil {
			return errors.New("读取用户状态失败")
		}
		u := findUser(s, username)
		if (s.Mesh == nil && s.MeshAgent.Cluster != "") || u == nil || u.Portal == nil || *u.Portal != expected {
			return errors.New("账号已被修改，请刷新后重试；本次未覆盖新设置")
		}
		u.Portal = &next
		if err := saveState(a.statePath, s); err != nil {
			return errors.New("保存账号失败，请检查当前设置后再操作")
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	b.mu.Lock()
	b.revokePortalSessions(username)
	delete(b.attempts, key)
	b.mu.Unlock()
	if self {
		return "密码已更新，请重新登录。", nil
	}
	return "用户登录设置已保存，即时生效；该用户的旧会话已失效。", nil
}
