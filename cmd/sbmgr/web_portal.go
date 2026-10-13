package main

import (
	"context"
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

func (b *webBackend) revokePortalSessions(username string) {
	for id, s := range b.sessions {
		if s.Role == "user" && strings.EqualFold(s.Username, username) {
			delete(b.sessions, id)
		}
	}
}

func (b *webBackend) authenticatePortal(username, password string, config webConfig) (webSession, bool, error) {
	s, err := loadState(b.a.statePath)
	if err != nil {
		return webSession{}, false, errors.New("用户登录暂不可用")
	}
	u := findUser(s, username)
	salt, expected := config.Salt, config.PasswordHash
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
func (b *webBackend) lookupPortalLocked(ctx context.Context, q webRequest, session webSession, now time.Time) webReply {
	b.mu.Unlock()
	result := webError(503, "读取账户状态失败，请稍后重试")
	revoke := false
	var analyticsState *State
	var analyticsOwner *User
	var analyticsVersion string
	a := &app{statePath: b.a.statePath, out: io.Discard, err: io.Discard, actor: "portal:" + session.Username}
	err := a.withStateLockContext(ctx, func() error {
		s, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		u := findUser(s, session.Username)
		if u == nil || u.Portal == nil || !u.Portal.Enabled || u.Portal.PasswordHash == "" || subtle.ConstantTimeCompare([]byte(u.Portal.Epoch), []byte(session.Epoch)) != 1 || (s.Mesh == nil && s.MeshAgent.Cluster != "") {
			revoke = true
			result = webError(401, "登录已失效，请重新登录或联系管理员")
			return nil
		}
		switch {
		case q.Path == "/api/session" && q.Method == "GET":
			result = webJSON(200, map[string]string{"csrf": session.CSRF, "username": u.Name, "role": "user"})
		case q.Path == "/api/logout" && q.Method == "POST":
			revoke = true
			result = webJSON(200, map[string]bool{"ok": true})
			result.Logout = true
		case q.Path == "/api/me" && q.Method == "GET":
			result = webJSON(200, portalSnapshot(s, u, now))
		case q.Path == "/api/analytics" && q.Method == "POST":
			analyticsVersion, err = analyticsControlVersion(a.statePath)
			if err != nil {
				return err
			}
			analyticsState, analyticsOwner = s, u
		case q.Path == "/api/delivery" && q.Method == "POST":
			result = webDeliveryFromState(s, q.Body, u.Name)
		case q.Path == "/api/me/devices" && q.Method == "POST":
			result = b.changePortalDeviceLocked(a, s, u, q, now)
		case q.Path == "/api/me/site-blocks" && q.Method == "GET":
			result = portalSiteBlocksReply(u, configurationPending(s) || runtimeApplyPending(s), "")
		case q.Path == "/api/me/site-blocks" && q.Method == "POST":
			result = b.changePortalSiteBlocksLocked(a, s, u, q)
		default:
			result = webError(403, "此账号只能管理自己的已授权服务")
		}
		return nil
	})
	if revoke {
		b.mu.Lock()
		delete(b.sessions, q.Session)
		b.mu.Unlock()
	}
	if err != nil {
		return webError(503, "读取账户状态失败，请稍后重试")
	}
	if analyticsState != nil {
		return a.analyticsReply(analyticsState, analyticsOwner, q.Body, analyticsVersion)
	}
	return result
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
