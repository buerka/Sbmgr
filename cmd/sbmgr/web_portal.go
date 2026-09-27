package main

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"io"
	"strings"
	"time"
)

func portalAccountView(u *User) map[string]any {
	view := map[string]any{"configured": false, "enabled": false, "invited": false, "invite_expires": ""}
	if p := u.Portal; p != nil {
		view["configured"], view["enabled"] = p.PasswordHash != "", p.Enabled
		view["invited"], view["invite_expires"] = p.InviteHash != "", p.InviteExpires
	}
	return view
}

// Passwords bypass command arguments, the generic action catalog and jobs.
func (a *app) setPortalAccount(username string, enabled bool, password, adminName string) error {
	return a.withAuditedStateLock("portal.account", []string{username}, func() error {
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
		if len(u.Name) > 64 || strings.EqualFold(u.Name, adminName) {
			return errors.New("登录名不能与管理员重名，且不能超过 64 字节")
		}
		if len(password) > 1024 || (password != "" && len(password) < 12) {
			return errors.New("密码长度须为 12–1024 字节")
		}
		if u.Portal == nil || (u.Portal.PasswordHash == "" && (enabled || password != "")) {
			return errors.New("首次开通请生成邀请，由用户自行设置密码")
		}
		// Disabling a pending account also revokes its invitation.
		u.Portal.InviteHash, u.Portal.InviteExpires = "", ""
		if password != "" {
			u.Portal.Salt = webRandom()
			u.Portal.PasswordHash, err = webPasswordHash(password, u.Portal.Salt)
			if err != nil {
				return errors.New("生成密码摘要失败")
			}
		}
		u.Portal.Enabled = enabled
		u.Portal.Epoch = webRandom()
		if err := saveState(a.statePath, s); err != nil {
			return errors.New("保存账号失败，原配置保持不变")
		}
		return nil
	})
}

func (b *webBackend) managePortalAccount(q webRequest) webReply {
	if b.busy {
		return webError(409, "请等待当前管理操作完成")
	}
	var input struct {
		User     string `json:"user"`
		Enabled  *bool  `json:"enabled"`
		Password string `json:"password"`
	}
	if webDecode(q.Body, &input) != nil || input.Enabled == nil || len(input.User) > 64 || len(input.Password) > 1024 {
		return webError(400, "登录账号设置格式不正确")
	}
	var warnings bytes.Buffer
	a := &app{statePath: b.a.statePath, out: io.Discard, err: &warnings, actor: "web:" + b.config.Username}
	if err := a.setPortalAccount(input.User, *input.Enabled, input.Password, b.config.Username); err != nil {
		return webError(400, err.Error())
	}
	b.revokePortalSessions(input.User)
	message := "用户登录设置已保存，即时生效；该用户的旧会话已失效。"
	if warnings.Len() > 0 {
		message += "审计记录写入失败，请检查服务器。"
	}
	return webJSON(200, map[string]string{"message": message})
}

func (b *webBackend) revokePortalSessions(username string) {
	for id, s := range b.sessions {
		if s.Role == "user" && strings.EqualFold(s.Username, username) {
			delete(b.sessions, id)
		}
	}
}

func (b *webBackend) authenticatePortal(username, password string) (webSession, bool, error) {
	s, err := loadState(b.a.statePath)
	if err != nil {
		return webSession{}, false, errors.New("用户登录暂不可用")
	}
	u := findUser(s, username)
	salt, expected := b.config.Salt, b.config.PasswordHash
	available := u != nil && u.Portal != nil && u.Portal.Enabled && u.Portal.PasswordHash != "" && s.MeshAgent.Cluster == ""
	// Masters may also have a local agent identity.
	if s.Mesh != nil {
		available = u != nil && u.Portal != nil && u.Portal.Enabled && u.Portal.PasswordHash != ""
	}
	if available {
		salt, expected = u.Portal.Salt, u.Portal.PasswordHash
	}
	hash, err := webPasswordHash(password, salt)
	if err != nil || subtle.ConstantTimeCompare([]byte(hash), []byte(expected)) != 1 || !available {
		return webSession{}, false, nil
	}
	return webSession{Role: "user", Username: u.Name, Epoch: u.Portal.Epoch}, true, nil
}

// Called with b.mu held. Recheck the account on EVERY request, under the same
// state lock as provisioning, resets, deletion and restore. Never accept a
// client-selected owner for data or delivery, including on forged IPC calls.
func (b *webBackend) lookupPortalLocked(q webRequest, session webSession, now time.Time) webReply {
	result := webError(503, "读取账户状态失败，请稍后重试")
	a := &app{statePath: b.a.statePath, out: io.Discard, err: io.Discard, actor: "portal:" + session.Username}
	err := a.withStateLock(func() error {
		s, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		u := findUser(s, session.Username)
		if u == nil || u.Portal == nil || !u.Portal.Enabled || u.Portal.PasswordHash == "" || subtle.ConstantTimeCompare([]byte(u.Portal.Epoch), []byte(session.Epoch)) != 1 || (s.Mesh == nil && s.MeshAgent.Cluster != "") {
			delete(b.sessions, q.Session)
			result = webError(401, "登录已失效，请重新登录或联系管理员")
			return nil
		}
		switch {
		case q.Path == "/api/session" && q.Method == "GET":
			result = webJSON(200, map[string]string{"csrf": session.CSRF, "username": u.Name, "role": "user"})
		case q.Path == "/api/logout" && q.Method == "POST":
			delete(b.sessions, q.Session)
			result = webJSON(200, map[string]bool{"ok": true})
			result.Logout = true
		case q.Path == "/api/me" && q.Method == "GET":
			result = webJSON(200, portalSnapshot(s, u, now))
		case q.Path == "/api/analytics" && q.Method == "POST":
			result = a.analyticsReply(s, u, q.Body)
		case q.Path == "/api/delivery" && q.Method == "POST":
			result = webDeliveryFromState(s, q.Body, u.Name)
		case q.Path == "/api/me/devices" && q.Method == "POST":
			result = b.changePortalDeviceLocked(a, s, u, q, now)
		case q.Path == "/api/me/site-blocks" && q.Method == "GET":
			result = portalSiteBlocksReply(u, configurationPending(s) || runtimeApplyPending(s), "")
		case q.Path == "/api/me/site-blocks" && q.Method == "POST":
			result = b.changePortalSiteBlocksLocked(a, s, u, q)
		case q.Path == "/api/me/password" && q.Method == "POST":
			result = b.changePortalPasswordLocked(a, s, u, q, now)
		default:
			result = webError(403, "此账号只能管理自己的已授权服务")
		}
		return nil
	})
	if err != nil {
		return webError(503, "读取账户状态失败，请稍后重试")
	}
	return result
}

func (b *webBackend) changePortalPasswordLocked(a *app, s *State, u *User, q webRequest, now time.Time) webReply {
	if b.busy {
		return webError(409, "管理员正在保存设置，请稍后重试")
	}
	var input struct {
		Current  string `json:"current_password"`
		Password string `json:"new_password"`
	}
	if webDecode(q.Body, &input) != nil || len(input.Current) > 1024 || len(input.Password) < 12 || len(input.Password) > 1024 {
		return webError(400, "新密码长度须为 12–1024 字节")
	}
	key := "portal-password:" + u.Portal.Epoch
	attempt := b.attempts[key]
	if now.After(attempt.Until) {
		attempt = webAttempt{}
	}
	if attempt.Count >= 5 {
		return webError(429, "当前密码尝试过于频繁，请 5 分钟后重试")
	}
	hash, err := webPasswordHash(input.Current, u.Portal.Salt)
	if err != nil || subtle.ConstantTimeCompare([]byte(hash), []byte(u.Portal.PasswordHash)) != 1 {
		if attempt.Count == 0 {
			attempt.Until = now.Add(5 * time.Minute)
		}
		attempt.Count++
		b.attempts[key] = attempt
		return webError(403, "当前密码不正确")
	}
	var warnings bytes.Buffer
	a.err = &warnings
	err = a.withAuditedStateLock("portal.password", nil, func() error {
		u.Portal.Salt = webRandom()
		u.Portal.Epoch = webRandom()
		u.Portal.PasswordHash, err = webPasswordHash(input.Password, u.Portal.Salt)
		if err != nil {
			return err
		}
		return saveState(a.statePath, s)
	})
	if err != nil {
		return webError(500, "密码保存失败，原密码保持不变")
	}
	b.revokePortalSessions(u.Name)
	delete(b.attempts, key)
	message := "密码已更新，请重新登录。"
	if warnings.Len() > 0 {
		message += "审计记录写入失败，请联系管理员。"
	}
	r := webJSON(200, map[string]string{"message": message})
	r.Logout = true
	return r
}

func portalSnapshot(s *State, u *User, now time.Time) map[string]any {
	devices := []any{}
	nodes := []any{}
	for _, d := range u.Devices {
		up, down := deviceTraffic(*u, d.Name)
		devices = append(devices, map[string]any{"name": d.Name, "label": deviceDisplayName(d), "enabled": d.Enabled, "upload": up, "download": down, "deliverable": subscriptionDeviceAvailable(*u, d, now) == nil, "last_seen": d.LastSeen})
	}
	for _, n := range u.Nodes {
		d := findDevice(u, n.Device)
		available := d != nil && subscriptionDeviceAvailable(*u, *d, now) == nil
		nodes = append(nodes, map[string]any{"name": n.Name, "device": n.Device, "available": available, "upload": n.Upload, "download": n.Download, "current_up": n.CurrentUploadMbps, "current_down": n.CurrentDownloadMbps})
	}
	return map[string]any{"time": now.Format(time.RFC3339), "device_version": portalDeviceVersion(u), "pending": runtimeApplyPending(s), "subscription_enabled": s.Subscription.Enabled,
		"user": map[string]any{"name": u.Name, "device_limit": u.DeviceLimit, "enabled": u.Enabled, "status": userStatus(*u), "quota": u.QuotaBytes, "extra_quota": u.ExtraQuotaBytes, "quota_mode": u.QuotaMode, "used": measuredUsage(*u), "upload": u.Upload, "download": u.Download, "expires": u.Expires, "up_mbps": u.UploadMbps, "down_mbps": u.DownloadMbps, "current_up": u.CurrentUploadMbps, "current_down": u.CurrentDownloadMbps, "devices": devices, "nodes": nodes, "billing": map[string]any{"enabled": u.Billing.Enabled, "cycle_day": u.Billing.CycleDay, "next_reset": u.Billing.NextReset}}}
}
