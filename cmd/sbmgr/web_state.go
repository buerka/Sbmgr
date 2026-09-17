package main

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

// Explicit projections keep UUIDs, subscription tokens, SSH keys, upstream
// credentials and transport secrets out of the management inventory.
func (a *app) webSnapshot() (map[string]any, error) {
	s, err := loadState(a.statePath)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	users := []any{}
	for _, u := range s.Users {
		devices := []any{}
		nodes := []any{}
		for _, d := range u.Devices {
			up, down := deviceTraffic(u, d.Name)
			devices = append(devices, map[string]any{"name": d.Name, "enabled": d.Enabled, "upload": up, "download": down, "ip_policy": d.IPPolicy, "access": d.Access, "assignment_version": webAssignmentVersion(&u, d.Name), "deliverable": subscriptionDeviceAvailable(u, d, now) == nil})
		}
		for _, n := range u.Nodes {
			entry := "本机"
			if s.Mesh != nil {
				entry = s.Mesh.Master
				for _, r := range s.Mesh.Routes {
					if "sbmgr-mesh-"+r.ID == n.Outbound {
						entry = s.Mesh.Entry(r)
					}
				}
			}
			nodes = append(nodes, map[string]any{"name": n.Name, "device": n.Device, "outbound": n.Outbound, "entry": entry, "upload": n.Upload, "download": n.Download, "up_mbps": n.UploadMbps, "down_mbps": n.DownloadMbps, "current_up": n.CurrentUploadMbps, "current_down": n.CurrentDownloadMbps})
		}
		connections := []any{}
		for _, c := range firstWebItems(activeConnectionsForUser(s, u.Name), 50) {
			connections = append(connections, map[string]string{"device": c.Device, "node": c.Node, "source": c.SourceIP, "target": c.Target, "since": c.StartedAt})
		}
		users = append(users, map[string]any{"name": u.Name, "enabled": u.Enabled, "status": userStatus(u), "quota": u.QuotaBytes, "extra_quota": u.ExtraQuotaBytes, "quota_mode": u.QuotaMode, "used": measuredUsage(u), "upload": u.Upload, "download": u.Download, "expires": u.Expires, "current_up": u.CurrentUploadMbps, "current_down": u.CurrentDownloadMbps, "devices": devices, "nodes": nodes, "connections": connections, "accesses": firstWebItems(u.RecentAccesses, 100), "history": lastWebItems(u.UsageHistory, 120), "access": u.Access, "up_mbps": u.UploadMbps, "down_mbps": u.DownloadMbps, "ip_policy": u.IPPolicy, "burst": u.Burst, "throttle": u.Throttle, "billing": u.Billing})
	}
	routes := []any{}
	members := []any{}
	role := "standalone"
	var revision uint64
	if s.Mesh != nil {
		role = "master"
		revision = s.Mesh.Revision
		for _, m := range s.Mesh.Members {
			members = append(members, map[string]any{"id": m.ID, "host": m.SSHHost, "master": m.ID == s.Mesh.Master, "client": m.Client})
		}
		for _, r := range s.Mesh.Routes {
			protocols := make([]string, 0, len(r.Transports))
			for _, transport := range r.Transports {
				protocols = append(protocols, transport.Type)
			}
			routes = append(routes, map[string]any{"id": r.ID, "name": webRouteName(s, r), "entry": s.Mesh.Entry(r), "hops": r.Hops, "exit": r.Exit, "protocols": protocols})
		}
	} else if s.MeshAgent.Cluster != "" {
		role = "slave"
		if s.MeshAgent.Active != nil {
			revision = s.MeshAgent.Active.Revision
		}
	}
	outbounds := []any{}
	for _, n := range nodeTemplates(s) {
		outbounds = append(outbounds, map[string]string{"name": n.Name, "outbound": n.Outbound})
	}
	backups, err := listStateBackups(a.statePath)
	if err != nil {
		return nil, err
	}
	backupList := []any{}
	for _, b := range backups {
		backupList = append(backupList, map[string]any{"name": b.Name, "size": b.Size, "modified": b.Modified.Format(time.RFC3339)})
	}
	proxies := []any{}
	for _, kind := range []ManagedProxyKind{ManagedProxyOutbound, ManagedProxyEndpoint} {
		docs, err := listManagedProxyDocuments(s, kind)
		if err != nil {
			continue
		}
		for _, d := range docs {
			proxies = append(proxies, map[string]any{"kind": kind, "tag": d.Tag, "type": d.Type})
		}
	}
	audit, err := readAuditRecords(a.statePath, 100)
	if err != nil {
		return nil, err
	}
	auditView := []any{}
	for _, r := range audit {
		auditView = append(auditView, map[string]string{"at": r.At, "actor": r.Actor, "action": r.Action})
	}
	fleet := []any{}
	for _, server := range s.Fleet {
		status := s.FleetStatus[server.Name]
		fleet = append(fleet, map[string]any{"name": server.Name, "host": server.Host, "online": status.Online, "checked": status.CheckedAt})
	}
	return map[string]any{"audit": auditView, "fleet": fleet, "client": map[string]any{"server": s.Client.Server, "port": s.Client.Port}, "version": appVersion, "time": now.Format(time.RFC3339), "users": users, "outbounds": outbounds, "proxies": proxies, "members": members, "routes": routes, "role": role, "revision": revision, "pending": configurationPending(s) || runtimeApplyPending(s), "mesh_pending": s.MeshRollout != nil || (s.Mesh != nil && (s.MeshAgent.Active == nil || s.MeshAgent.Active.Revision != s.Mesh.Revision)), "backups": backupList, "alerts": s.Alerts, "health": s.OutboundHealth, "health_settings": normalizedHealthSettings(s.Health), "subscription": map[string]any{"enabled": s.Subscription.Enabled, "base_url": s.Subscription.BaseURL, "listen": s.Subscription.Listen, "template": s.Client.MihomoTemplate != "", "template_path": s.Client.MihomoTemplate, "tls_configured": s.Subscription.TLSCertFile != "" && s.Subscription.TLSKeyFile != ""}}, nil
}

func (a *app) webDelivery(body []byte) webReply {
	var input struct {
		User   string `json:"user"`
		Device string `json:"device"`
		Format string `json:"format"`
	}
	if webDecode(body, &input) != nil {
		return webError(400, "交付请求格式不正确")
	}
	s, err := loadState(a.statePath)
	if err != nil {
		return webError(503, "状态不可用")
	}
	u := findUser(s, input.User)
	if u == nil {
		return webError(404, "用户不存在")
	}
	d := findDevice(u, input.Device)
	if d == nil {
		return webError(404, "设备不存在")
	}
	if err := subscriptionDeviceAvailable(*u, *d, time.Now()); err != nil {
		return webError(403, "该用户或设备当前不可交付")
	}
	var data []byte
	mime, name := "text/plain; charset=utf-8", "subscription.txt"
	switch input.Format {
	case "link":
		if !s.Subscription.Enabled || s.Subscription.BaseURL == "" {
			return webError(409, "请先启用订阅服务并设置公开地址")
		}
		data = []byte(subscriptionURL(s, *d) + "\n")
	case "yaml":
		data, err = renderMihomoDevice(s, *u, d.Name)
		mime = "application/yaml"
		name = "subscription.yaml"
	default:
		err = errors.New("unknown format")
	}
	if err != nil {
		return webError(400, "无法生成交付文件，请检查客户端入口设置")
	}
	if len(data) > subscriptionMaxBody {
		return webError(413, "交付文件过大")
	}
	return webReply{Status: http.StatusOK, Body: data, Type: mime, Filename: name}
}

func webSlaveMutation(s *State, action string) bool {
	return s.Mesh == nil && s.MeshAgent.Cluster != "" && (strings.HasPrefix(action, "user.") || strings.HasPrefix(action, "node.") || strings.HasPrefix(action, "device."))
}

func firstWebItems[T any](items []T, limit int) []T {
	if len(items) > limit {
		return items[:limit]
	}
	return items
}
func lastWebItems[T any](items []T, limit int) []T {
	if len(items) > limit {
		return items[len(items)-limit:]
	}
	return items
}
