package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

// Called with b.mu held, after session, origin and CSRF validation. Passwords
// never enter the action/job catalog, CLI arguments, audit records or snapshots.
func (b *webBackend) changeWebAccount(q webRequest, now time.Time) webReply {
	if b.busy {
		return webError(409, "请等待当前管理操作完成后再修改账号")
	}
	var input struct {
		Username string `json:"username"`
		Current  string `json:"current_password"`
		Password string `json:"new_password"`
	}
	if webDecode(q.Body, &input) != nil || len(input.Current) > 1024 || len(input.Password) > 1024 || len(input.Username) > 64 {
		return webError(400, "账号设置格式不正确")
	}
	input.Username = strings.TrimSpace(input.Username)
	if input.Username == "" || (input.Password != "" && len(input.Password) < 12) {
		return webError(400, "用户名不能为空；新密码至少需要 12 字节")
	}
	attempt := b.attempts["account"]
	if now.After(attempt.Until) {
		attempt = webAttempt{}
	}
	if attempt.Count >= 5 {
		return webError(429, "当前密码尝试过于频繁，请 5 分钟后重试")
	}
	status := 500
	next := b.config
	var warnings bytes.Buffer
	a := &app{statePath: b.a.statePath, out: io.Discard, err: &warnings, actor: "web:" + b.config.Username}
	err := a.withAuditedStateLock("web.account", nil, func() error {
		current, err := readWebConfig(a.statePath)
		if err != nil {
			return errors.New("管理设置不可用")
		}
		if current != b.config {
			status = 409
			return errors.New("管理设置已变化，请重新登录后再修改")
		}
		hash, err := webPasswordHash(input.Current, current.Salt)
		if err != nil || subtle.ConstantTimeCompare([]byte(hash), []byte(current.PasswordHash)) != 1 {
			if attempt.Count == 0 {
				attempt.Until = now.Add(5 * time.Minute)
			}
			attempt.Count++
			b.attempts["account"] = attempt
			status = 403
			return errors.New("当前密码不正确")
		}
		next = current
		next.Username = input.Username
		if input.Password != "" {
			salt := make([]byte, 32)
			if _, err := rand.Read(salt); err != nil {
				return errors.New("无法生成密码摘要")
			}
			next.Salt = hex.EncodeToString(salt)
			next.PasswordHash, err = webPasswordHash(input.Password, next.Salt)
			if err != nil {
				return errors.New("无法生成密码摘要")
			}
		}
		if err := validateWebConfig(next); err != nil {
			status = 400
			return err
		}
		if next == current {
			status = 400
			return errors.New("账号设置未发生变化")
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
		return webError(status, err.Error())
	}
	clear(b.sessions)
	delete(b.attempts, "account")
	b.config = next
	message := "管理账号已更新，请使用新账号重新登录"
	if warnings.Len() > 0 {
		message += "；审计记录写入失败，请检查审计目录权限"
	}
	r := webJSON(200, map[string]string{"message": message})
	r.Logout = true
	return r
}
