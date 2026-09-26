package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"
)

const portalInviteLifetime = 24 * time.Hour

func portalInviteDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

// Administrator-only; lookup has already checked role, origin and CSRF.
func (b *webBackend) createPortalInvite(q webRequest, now time.Time) webReply {
	if b.busy {
		return webError(409, "请等待当前管理操作完成")
	}
	var input struct {
		User string `json:"user"`
	}
	if webDecode(q.Body, &input) != nil || len(input.User) > 64 {
		return webError(400, "邀请设置格式不正确")
	}
	token := webRandom()
	expires := now.Add(portalInviteLifetime).UTC().Format(time.RFC3339)
	var warnings bytes.Buffer
	a := &app{statePath: b.a.statePath, out: io.Discard, err: &warnings, actor: "web:" + b.config.Username}
	err := a.withAuditedStateLock("portal.invite", []string{input.User}, func() error {
		s, err := loadState(a.statePath)
		if err != nil {
			return errors.New("读取用户状态失败")
		}
		if s.Mesh == nil && s.MeshAgent.Cluster != "" {
			return errors.New("请在主机管理用户登录账号")
		}
		u := findUser(s, input.User)
		if u == nil {
			return errors.New("用户不存在")
		}
		if len(u.Name) > 64 || strings.EqualFold(u.Name, b.config.Username) {
			return errors.New("登录名不能与管理员重名，且不能超过 64 字节")
		}
		if u.Portal != nil && u.Portal.PasswordHash != "" {
			return errors.New("该用户已设置密码，如需修改请使用重置密码")
		}
		u.Portal = &PortalAccount{Enabled: true, Epoch: webRandom(), InviteHash: portalInviteDigest(token), InviteExpires: expires}
		if err := saveState(a.statePath, s); err != nil {
			return errors.New("邀请保存失败，原设置保持不变")
		}
		return nil
	})
	if err != nil {
		return webError(400, err.Error())
	}
	b.revokePortalSessions(input.User)
	message := "邀请已生成，24 小时内有效；旧邀请已作废。"
	if warnings.Len() > 0 {
		message += "审计记录写入失败，请检查服务器。"
	}
	// Keep the bearer token out of HTTP paths, access logs and referrers.
	return webJSON(200, map[string]string{"url": b.config.Origin + b.config.BasePath + "/#/activate?token=" + token, "expires": expires, "message": message})
}

func findPortalInvite(s *State, token string, now time.Time) *User {
	if !portalDigestValid(token) || (s.Mesh == nil && s.MeshAgent.Cluster != "") {
		return nil
	}
	digest := portalInviteDigest(token)
	for i := range s.Users {
		u := &s.Users[i]
		p := u.Portal
		if p == nil || !p.Enabled || p.PasswordHash != "" || p.InviteHash == "" {
			continue
		}
		expiry, err := time.Parse(time.RFC3339, p.InviteExpires)
		if err == nil && now.Before(expiry) && subtle.ConstantTimeCompare([]byte(digest), []byte(p.InviteHash)) == 1 {
			return u
		}
	}
	return nil
}

// Invitation possession authorizes only first-time password creation, never
// login, user selection, configuration changes, or password reset. Origin/Host
// validation happens in the privileged lookup before this public endpoint.
func (b *webBackend) usePortalInvite(q webRequest, now time.Time) webReply {
	for key, attempt := range b.attempts {
		if !now.Before(attempt.Until) {
			delete(b.attempts, key)
		}
	}
	keys := []string{"invite:" + q.Remote, "invite:*"}
	limits := []int{30, 120}
	for i, key := range keys {
		if b.attempts[key].Count >= limits[i] || len(b.attempts) >= 1024 {
			return webError(429, "邀请操作过于频繁，请稍后重试")
		}
	}
	for _, key := range keys {
		attempt := b.attempts[key]
		if attempt.Count == 0 {
			attempt.Until = now.Add(time.Minute)
		}
		attempt.Count++
		b.attempts[key] = attempt
	}
	var input struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if webDecode(q.Body, &input) != nil || !portalDigestValid(input.Token) {
		return webError(400, "邀请链接无效或已失效，请联系管理员重新生成")
	}
	accept := q.Path == "/api/invite/accept"
	if accept && b.busy {
		return webError(409, "管理员正在保存设置，请稍后重试")
	}
	if accept && (len(input.Password) < 12 || len(input.Password) > 1024) {
		return webError(400, "密码长度须为 12–1024 字节")
	}
	result := webError(400, "邀请链接无效或已失效，请联系管理员重新生成")
	var warnings bytes.Buffer
	a := &app{statePath: b.a.statePath, out: io.Discard, err: &warnings, actor: "portal:activation"}
	err := a.withStateLock(func() error {
		s, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		u := findPortalInvite(s, input.Token, time.Now())
		if u == nil || strings.EqualFold(u.Name, b.config.Username) {
			return nil
		}
		if !accept {
			result = webJSON(200, map[string]string{"username": u.Name, "expires": u.Portal.InviteExpires})
			return nil
		}
		a.actor = "portal:" + u.Name
		err = a.withAuditedStateLock("portal.activate", nil, func() error {
			salt := webRandom()
			hash, err := webPasswordHash(input.Password, salt)
			if err != nil {
				return err
			}
			// Password creation and invitation consumption commit together. A competing
			// backend/process re-reads after the lock, so only one request can succeed.
			u.Portal = &PortalAccount{Enabled: true, Epoch: webRandom(), Salt: salt, PasswordHash: hash}
			return saveState(a.statePath, s)
		})
		if err != nil {
			return err
		}
		b.revokePortalSessions(u.Name)
		message := "密码已设置，邀请已作废。请使用账号和新密码登录。"
		if warnings.Len() > 0 {
			message += "审计记录写入失败，请联系管理员。"
		}
		result = webJSON(200, map[string]string{"message": message})
		return nil
	})
	if err != nil {
		return webError(503, "邀请处理失败，请稍后重试；未成功保存时邀请不会被消耗")
	}
	return result
}
