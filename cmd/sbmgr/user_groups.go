package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
)

const defaultGroupID = "default"

var groupScopes = []string{"quota", "rate", "expiry", "routes"}

type UserGroup struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Policy GroupPolicy `json:"policy"`
}
type GroupQuota struct {
	Bytes int64  `json:"bytes"`
	Mode  string `json:"mode"`
}
type GroupRate struct {
	Upload   float64 `json:"upload"`
	Download float64 `json:"download"`
}
type GroupRoute struct {
	Outbound string `json:"outbound"`
	Name     string `json:"name"`
}
type GroupPolicy struct {
	Quota  *GroupQuota   `json:"quota,omitempty"`
	Rate   *GroupRate    `json:"rate,omitempty"`
	Expiry *string       `json:"expiry,omitempty"`
	Routes *[]GroupRoute `json:"routes,omitempty"`
}

func findGroup(s *State, id string) *UserGroup {
	for i := range s.UserGroups {
		if s.UserGroups[i].ID == id {
			return &s.UserGroups[i]
		}
	}
	return nil
}

// The initial group has no enforced defaults. Upgrading never changes an
// existing quota, identity, authorization, accounting history or login.
func normalizeUserGroups(s *State) {
	if len(s.UserGroups) == 0 {
		s.UserGroups = []UserGroup{{ID: defaultGroupID, Name: "默认分组"}}
	}
	for i := range s.Users {
		if s.Users[i].GroupID == "" {
			s.Users[i].GroupID = defaultGroupID
		}
	}
}

func groupValue(p GroupPolicy, scope string) any {
	switch scope {
	case "quota":
		return p.Quota
	case "rate":
		return p.Rate
	case "expiry":
		return p.Expiry
	case "routes":
		return p.Routes
	}
	return nil
}
func groupConfigured(p GroupPolicy, scope string) bool {
	v := reflect.ValueOf(groupValue(p, scope))
	return v.IsValid() && !v.IsNil()
}
func groupOverride(u *User, scope string) bool { return slices.Contains(u.GroupOverrides, scope) }

// Adding a route is not a speed override. New nodes retain the group's speed
// unless the user already has a personal rate policy or explicitly supplies one.
func inheritGroupNodeRate(s *State, u *User, n *Node) {
	if g := findGroup(s, u.GroupID); g != nil && g.Policy.Rate != nil && !groupOverride(u, "rate") {
		n.UploadMbps, n.DownloadMbps = g.Policy.Rate.Upload, g.Policy.Rate.Download
	}
}

func markGroupOverride(u *User, scope string) {
	if !groupOverride(u, scope) {
		u.GroupOverrides = append(u.GroupOverrides, scope)
		sort.Strings(u.GroupOverrides)
	}
}

func validateGroupPolicy(p GroupPolicy) error {
	if p.Quota != nil {
		if p.Quota.Bytes < 0 {
			return errors.New("分组配额不能为负数")
		}
		if err := validateQuotaMode(p.Quota.Mode); err != nil {
			return err
		}
	}
	if p.Rate != nil {
		if err := validateMbps(p.Rate.Upload, p.Rate.Download); err != nil {
			return err
		}
	}
	if p.Expiry != nil {
		if err := validateDate(*p.Expiry); err != nil {
			return err
		}
	}
	if p.Routes != nil {
		if len(*p.Routes) == 0 || len(*p.Routes) > 512 {
			return errors.New("分组线路需选择 1–512 条")
		}
		seen := map[string]bool{}
		for _, r := range *p.Routes {
			if len(r.Outbound) > 256 || strings.ContainsAny(r.Outbound, "\r\n\x00") || seen[r.Outbound] {
				return errors.New("分组线路无效或重复")
			}
			seen[r.Outbound] = true
			if err := validateManagedName(r.Name); err != nil {
				return errors.New("分组线路名称无效")
			}
		}
	}
	return nil
}

func validateUserGroups(s *State) error {
	if len(s.UserGroups) > 256 {
		return errors.New("分组数量超过上限")
	}
	if len(s.UserGroups) == 0 { // Legacy snapshots and uninitialized fixtures.
		for _, u := range s.Users {
			if u.GroupID != "" || len(u.GroupOverrides) != 0 {
				return errors.New("用户分组不存在")
			}
		}
		return nil
	}
	ids, names := map[string]bool{}, map[string]bool{}
	for _, g := range s.UserGroups {
		if len(g.ID) == 0 || len(g.ID) > 64 || strings.Trim(g.ID, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" || ids[g.ID] {
			return errors.New("分组标识无效或重复")
		}
		if err := validateManagedName(g.Name); err != nil {
			return errors.New("分组名称无效")
		}
		key := strings.ToLower(g.Name)
		if names[key] {
			return errors.New("分组名称不能重复")
		}
		ids[g.ID], names[key] = true, true
		if err := validateGroupPolicy(g.Policy); err != nil {
			return err
		}
	}
	if !ids[defaultGroupID] {
		return errors.New("默认分组不能删除")
	}
	for _, u := range s.Users {
		if !ids[u.GroupID] {
			return errors.New("用户所属分组不存在")
		}
		seen := map[string]bool{}
		for _, k := range u.GroupOverrides {
			if !slices.Contains(groupScopes, k) || seen[k] {
				return errors.New("用户分组覆盖项无效")
			}
			seen[k] = true
		}
	}
	return nil
}

// Existing mutation paths (Web, CLI, batch and node edits) keep using the
// materialized user fields. A deliberate divergence becomes a durable personal
// exception at the same save boundary, not a second write after the mutation.
func captureGroupOverrides(s *State) {
	aliases := map[string]string{}
	for _, g := range s.UserGroups {
		if g.Policy.Routes != nil {
			aliases = webRouteAliasMap(s)
			break
		}
	}
	for i := range s.Users {
		u := &s.Users[i]
		g := findGroup(s, u.GroupID)
		if g == nil {
			continue
		}
		p := g.Policy
		if p.Quota != nil && (u.QuotaBytes != p.Quota.Bytes || normalizedQuotaMode(u.QuotaMode) != normalizedQuotaMode(p.Quota.Mode)) {
			markGroupOverride(u, "quota")
		}
		if p.Expiry != nil && u.Expires != *p.Expiry {
			markGroupOverride(u, "expiry")
		}
		if p.Rate != nil {
			if u.UploadMbps != 0 || u.DownloadMbps != 0 {
				markGroupOverride(u, "rate")
			}
			for _, n := range u.Nodes {
				if n.UploadMbps != p.Rate.Upload || n.DownloadMbps != p.Rate.Download {
					markGroupOverride(u, "rate")
					break
				}
			}
		}
		if p.Routes != nil && !(s.Mesh != nil && (s.MeshRollout != nil || s.MeshAgent.Transaction != "" || s.MeshAgent.Active == nil || s.MeshAgent.Active.Revision != s.Mesh.Revision)) {
			want := map[string]bool{}
			for _, r := range *p.Routes {
				want[webCanonicalOutboundWithAliases(aliases, r.Outbound)] = true
			}
			for _, d := range u.Devices {
				got := map[string]bool{}
				for _, n := range u.Nodes {
					if n.Device == d.Name {
						got[webCanonicalOutboundWithAliases(aliases, n.Outbound)] = true
					}
				}
				if !reflect.DeepEqual(got, want) {
					markGroupOverride(u, "routes")
					break
				}
			}
		}
	}
}

func applyGroupPolicy(s *State, u *User, scopes []string) error {
	g := findGroup(s, u.GroupID)
	if g == nil {
		return errors.New("用户所属分组不存在")
	}
	p := g.Policy
	want := func(k string) bool { return slices.Contains(scopes, k) && !groupOverride(u, k) }
	if want("routes") && p.Routes != nil {
		for _, d := range u.Devices {
			if err := assignGroupRoutes(s, u, d.Name, *p.Routes); err != nil {
				return err
			}
		}
	}
	if p.Rate != nil && !groupOverride(u, "rate") && (want("rate") || want("routes")) {
		u.UploadMbps, u.DownloadMbps, u.RateMark = 0, 0, 0
		for i := range u.Nodes {
			u.Nodes[i].UploadMbps, u.Nodes[i].DownloadMbps = p.Rate.Upload, p.Rate.Download
		}
	}
	if want("quota") && p.Quota != nil {
		u.QuotaBytes, u.QuotaMode, u.QuotaAlertStage = p.Quota.Bytes, normalizedQuotaMode(p.Quota.Mode), 0
	}
	if want("expiry") && p.Expiry != nil {
		u.Expires, u.ExpiryAlertStage = *p.Expiry, 0
	}
	return nil
}

func groupRouteSelection(s *State, routes []GroupRoute) (map[string]string, map[string]bool, error) {
	aliases := webRouteAliasMap(s)
	available, chosen := map[string]bool{}, map[string]bool{}
	for _, n := range nodeTemplates(s) {
		available[webCanonicalOutboundWithAliases(aliases, n.Outbound)] = true
	}
	for _, r := range routes {
		key := webCanonicalOutboundWithAliases(aliases, r.Outbound)
		if chosen[key] {
			return nil, nil, errors.New("分组包含指向同一线路的重复授权")
		}
		chosen[key] = true
		if !available[key] {
			return nil, nil, errors.New("分组线路不存在或尚未应用，请先检查线路拓扑")
		}
		if strings.HasPrefix(key, "sbmgr-mesh-") && (s.Mesh == nil || s.MeshRollout != nil || s.MeshAgent.Active == nil || s.MeshAgent.Active.Revision != s.Mesh.Revision) {
			return nil, nil, errors.New("请先应用线路拓扑，再更新分组授权")
		}
	}
	return aliases, chosen, nil
}

func assignGroupRoutes(s *State, u *User, device string, routes []GroupRoute) error {
	aliases, chosen, err := groupRouteSelection(s, routes)
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	kept := make([]Node, 0, len(u.Nodes)+len(routes))
	names := map[string]bool{}
	for _, n := range u.Nodes {
		key := webCanonicalOutboundWithAliases(aliases, n.Outbound)
		if n.Device != device || chosen[key] {
			kept = append(kept, n)
			if n.Device == device {
				existing[key] = true
				names[strings.ToLower(n.Name)] = true
			}
		}
	}
	for _, r := range routes {
		key := webCanonicalOutboundWithAliases(aliases, r.Outbound)
		if existing[key] {
			continue
		}
		name := r.Name
		for suffix := 2; names[strings.ToLower(name)]; suffix++ {
			name = fmt.Sprintf("%s (%d)", r.Name, suffix)
		}
		names[strings.ToLower(name)] = true
		n := Node{Name: name, Device: device, Outbound: key, UUID: newUUID(), AuthUser: uniqueAuthUser(s, u.Name+":"+slug(device)+":"+slug(name))}
		if groupOverride(u, "rate") {
			for _, prior := range u.Nodes {
				if prior.Device == device {
					if prior.UploadMbps > 0 && (n.UploadMbps == 0 || prior.UploadMbps < n.UploadMbps) {
						n.UploadMbps = prior.UploadMbps
					}
					if prior.DownloadMbps > 0 && (n.DownloadMbps == 0 || prior.DownloadMbps < n.DownloadMbps) {
						n.DownloadMbps = prior.DownloadMbps
					}
				}
			}
		}
		u.Nodes = append(u.Nodes, n)
		kept = append(kept, n)
	}
	u.Nodes = kept
	return nil
}

// Does not change with usage polling. Detects concurrent member/policy edits,
// including identity changes, without returning credentials to the browser.
func userGroupVersion(s *State) string {
	values := []any{s.UserGroups}
	for _, u := range s.Users {
		nodes := []any{}
		devices := []any{}
		for _, d := range u.Devices {
			devices = append(devices, []any{d.Name, d.Enabled})
		}
		for _, n := range u.Nodes {
			nodes = append(nodes, []any{n.Device, n.Name, n.Outbound, n.UUID, n.UploadMbps, n.DownloadMbps})
		}
		values = append(values, []any{u.Name, u.GroupID, u.GroupOverrides, u.QuotaBytes, u.QuotaMode, u.Expires, u.UploadMbps, u.DownloadMbps, nodes, devices})
	}
	raw, _ := json.Marshal(values)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
