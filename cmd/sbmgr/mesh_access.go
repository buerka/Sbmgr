package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"sbmgr/internal/mesh"
	"time"
)

const meshLeaseDuration = 90 * time.Second

// Leases are small control policy. Usage remains in the structured users,
// nodes and stats_counters tables, including remote cumulative baselines.
type MeshLease struct {
	Sequence uint64            `json:"sequence"`
	Until    string            `json:"until"`
	Expired  bool              `json:"expired,omitempty"`
	Quota    map[string]int64  `json:"quota,omitempty"`
	Blocked  map[string]bool   `json:"blocked,omitempty"`
	Policies map[string]string `json:"policies,omitempty"`
}

type meshAccess struct {
	Sequence uint64           `json:"sequence"`
	Until    string           `json:"until"`
	Revision uint64           `json:"revision"`
	Users    []User           `json:"users"`
	Quota    map[string]int64 `json:"quota,omitempty"`
}

type meshUsage struct {
	Auth     string `json:"auth"`
	Identity string `json:"identity"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
}

func meshNodeIdentity(n Node) string {
	digest := sha256.Sum256([]byte(n.UUID))
	return hex.EncodeToString(digest[:])
}

func meshRouteInfo(s *State, tag string) (mesh.RouteInfo, bool) {
	if s.MeshAgent.Active != nil {
		for _, r := range s.MeshAgent.Active.Catalog {
			if mesh.RouteTag(r.ID) == tag {
				return r, true
			}
		}
	}
	return mesh.RouteInfo{}, false
}

func meshNodeLocal(s *State, n Node) bool {
	r, ok := meshRouteInfo(s, n.Outbound)
	return !ok || r.Entry == s.MeshAgent.Member
}

func meshClientForNode(s *State, n Node) ClientSettings {
	if r, ok := meshRouteInfo(s, n.Outbound); ok && r.Client != nil {
		c := r.Client
		return ClientSettings{Server: c.Server, Port: c.Port, ServerName: c.ServerName, PublicKey: c.PublicKey, ShortID: c.ShortID}
	}
	return s.Client
}

func meshLeaseValid(s *State, now time.Time) bool {
	if s.MeshLease == nil {
		return true
	}
	until, err := time.Parse(time.RFC3339Nano, s.MeshLease.Until)
	return err == nil && now.Before(until)
}

// Build a local execution view. The master retains the complete accounts and
// counters for subscriptions and management but never authenticates a node
// assigned to another entry. Transit listeners have separate credentials.
func meshLocalView(s *State) *State {
	copyState := *s
	copyState.Users = nil
	for _, u := range s.Users {
		u.Nodes = append([]Node(nil), u.Nodes...)
		local := u.Nodes[:0]
		for _, n := range u.Nodes {
			if meshNodeLocal(s, n) {
				local = append(local, n)
			}
		}
		u.Nodes = local
		if !meshLeaseValid(s, time.Now()) {
			u.Enabled = false
		}
		if s.MeshLease != nil {
			if limit, ok := s.MeshLease.Quota[u.Name]; ok && measuredUsage(u) >= limit {
				u.Enabled = false
			}
		}
		copyState.Users = append(copyState.Users, u)
	}
	return &copyState
}

func validateMeshAccessState(s *State) error {
	if s.MeshLease != nil {
		if s.Mesh != nil || s.MeshAgent.Identity == nil || s.MeshAgent.Identity.Master || s.MeshLease.Sequence == 0 {
			return errors.New("入口授权租约角色无效")
		}
		if _, err := time.Parse(time.RFC3339Nano, s.MeshLease.Until); err != nil {
			return errors.New("入口授权租约时间无效")
		}
		for name, limit := range s.MeshLease.Quota {
			if limit < 0 || findUser(s, name) == nil {
				return errors.New("入口授权配额无效")
			}
		}
	}
	return nil
}

func meshUsageSnapshot(s *State) []meshUsage {
	var result []meshUsage
	for _, u := range s.Users {
		for _, n := range u.Nodes {
			result = append(result, meshUsage{Auth: n.AuthUser, Identity: meshNodeIdentity(n), Upload: n.Upload, Download: n.Download})
		}
	}
	return result
}

func mergeMeshUsage(s *State, member string, samples []meshUsage) error {
	if s.Counters == nil {
		s.Counters = map[string]int64{}
	}
	seen := map[string]bool{}
	for _, sample := range samples {
		if sample.Upload < 0 || sample.Download < 0 || len(sample.Identity) != 64 || seen[sample.Auth] {
			return errors.New("从机用量快照无效")
		}
		seen[sample.Auth] = true
		for ui := range s.Users {
			u := &s.Users[ui]
			for ni := range u.Nodes {
				n := &u.Nodes[ni]
				r, ok := meshRouteInfo(s, n.Outbound)
				if !ok || r.Entry != member || n.AuthUser != sample.Auth || meshNodeIdentity(*n) != sample.Identity {
					continue
				}
				key := "mesh:" + member + ":" + sample.Identity + ":"
				oldUp, oldDown := s.Counters[key+"upload"], s.Counters[key+"download"]
				// A reset or restore must not double-charge historical bytes. A
				// smaller cumulative value is held until it reaches the baseline.
				up, down := max(0, sample.Upload-oldUp), max(0, sample.Download-oldDown)
				if up > math.MaxInt64-u.Upload || down > math.MaxInt64-u.Download || up > math.MaxInt64-n.Upload || down > math.MaxInt64-n.Download {
					return errors.New("从机用量超过计数范围")
				}
				u.Upload += up
				u.Download += down
				n.Upload += up
				n.Download += down
				s.Counters[key+"upload"] = max(oldUp, sample.Upload)
				s.Counters[key+"download"] = max(oldDown, sample.Download)
			}
		}
	}
	return nil
}

func buildMeshAccess(s *State, member string, sequence uint64, now time.Time) meshAccess {
	result := meshAccess{Sequence: sequence, Until: now.Add(meshLeaseDuration).Format(time.RFC3339Nano), Revision: s.MeshAgent.Active.Revision, Quota: map[string]int64{}}
	for _, original := range s.Users {
		u := User{Name: original.Name, Enabled: original.Enabled, QuotaMode: original.QuotaMode, Expires: original.Expires,
			UploadMbps: original.UploadMbps, DownloadMbps: original.DownloadMbps, RateMark: original.RateMark,
			Throttle: original.Throttle, Burst: original.Burst, IPPolicy: meshEntryIPPolicy(original.IPPolicy), Access: original.Access,
			BlockedUntil: original.BlockedUntil, BlockReason: original.BlockReason, DisabledReason: original.DisabledReason}
		for _, n := range original.Nodes {
			r, ok := meshRouteInfo(s, n.Outbound)
			if !ok || r.Entry != member {
				continue
			}
			n.UploadMbps, n.DownloadMbps, _ = effectiveNodeRate(original, n)
			n.Upload, n.Download = 0, 0
			n.CurrentUploadMbps, n.CurrentDownloadMbps, n.RateUpdatedAt = 0, 0, ""
			n.Destinations = nil
			u.Nodes = append(u.Nodes, n)
		}
		if len(u.Nodes) == 0 {
			continue
		}
		for _, d := range original.Devices {
			needed := false
			for _, n := range u.Nodes {
				needed = needed || n.Device == d.Name
			}
			if needed {
				u.Devices = append(u.Devices, Device{Name: d.Name, Enabled: d.Enabled, CreatedAt: d.CreatedAt, IPPolicy: meshEntryIPPolicy(d.IPPolicy), Access: d.Access})
			}
		}
		if expired(original, now) || overQuota(original) || burstHardBlocked(original, now) {
			u.Enabled = false
		}
		if original.QuotaBytes > 0 {
			// Give this entry a bounded share of currently remaining quota.
			// The master remains authoritative; grants refresh on each sync.
			remaining := max(int64(0), original.QuotaBytes+original.ExtraQuotaBytes-measuredUsage(original))
			result.Quota[u.Name] = max(int64(1), remaining/int64(max(1, len(s.Mesh.Members))))
		}
		// Local usage is cumulative across master billing resets. Only the
		// refreshed lease budget may constrain it, not the master's cycle quota.
		u.Throttle = ThrottlePolicy{}
		result.Users = append(result.Users, u)
	}
	return result
}

func meshEntryIPPolicy(p IPPolicy) IPPolicy {
	p.BoundLastSeen = nil
	if p.Binding != "manual" {
		p.BoundIPs = nil
	}
	return p
}

func meshPolicyKey(ip IPPolicy, access AccessPolicy, burst BurstPolicy) string {
	access.ConnectionBlockedUntil, access.LastConnectionAlert = "", ""
	raw, _ := json.Marshal(struct {
		IP     IPPolicy
		Access AccessPolicy
		Burst  BurstPolicy
	}{meshEntryIPPolicy(ip), access, burst})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func preserveMeshIPRuntime(next *IPPolicy, previous IPPolicy) {
	if next.Binding != "manual" {
		next.BoundIPs = previous.BoundIPs
	}
	next.BoundLastSeen = previous.BoundLastSeen
}

func laterMeshBlock(a, b string) string {
	x, _ := time.Parse(time.RFC3339, a)
	y, _ := time.Parse(time.RFC3339, b)
	if y.After(x) {
		return b
	}
	return a
}

var meshApplyAccess = func(s *State) error { return applyState(s, false, true, io.Discard) }

// Called under the agent's process lock. Validation and sing-box/nft checks
// precede the durable desired-state write. Failed applies retain a retry flag.
func (a *app) installMeshAccess(s *State, access *meshAccess) error {
	if s.Mesh != nil || s.MeshAgent.Active == nil || s.MeshAgent.Transaction != "" || access == nil || access.Revision != s.MeshAgent.Active.Revision {
		return errors.New("入口授权与已应用拓扑不符")
	}
	until, err := time.Parse(time.RFC3339Nano, access.Until)
	if err != nil || !until.After(time.Now()) || until.After(time.Now().Add(meshLeaseDuration+10*time.Second)) || access.Sequence == 0 {
		return errors.New("入口授权租约无效")
	}
	if s.MeshLease != nil && access.Sequence <= s.MeshLease.Sequence {
		return errors.New("拒绝过期的入口授权序列")
	}
	old := map[string]User{}
	for _, u := range s.Users {
		old[u.Name] = u
	}
	if s.MeshLease == nil && len(old) > 0 {
		return errors.New("从机存在本地账户，拒绝接管")
	}
	next := *s
	next.Users = nil
	next.MeshLease = &MeshLease{Sequence: access.Sequence, Until: access.Until, Quota: map[string]int64{}, Policies: map[string]string{}}
	for _, supplied := range access.Users {
		// Do not accept runtime histories, subscriptions, billing or remote
		// file paths in an identity grant.
		if supplied.Upload != 0 || supplied.Download != 0 || len(supplied.TrafficSamples) > 0 || len(supplied.UsageHistory) > 0 || len(supplied.RecentAccesses) > 0 || len(supplied.BillingHistory) > 0 || supplied.Billing.Enabled {
			return errors.New("入口授权包含运行状态")
		}
		prior := old[supplied.Name]
		policyID := supplied.Name + "/"
		policyKey := meshPolicyKey(supplied.IPPolicy, supplied.Access, supplied.Burst)
		next.MeshLease.Policies[policyID] = policyKey
		if s.MeshLease != nil && s.MeshLease.Policies[policyID] == policyKey {
			preserveMeshIPRuntime(&supplied.IPPolicy, prior.IPPolicy)
			supplied.Access.ConnectionBlockedUntil = laterMeshBlock(supplied.Access.ConnectionBlockedUntil, prior.Access.ConnectionBlockedUntil)
			if laterMeshBlock(supplied.BlockedUntil, prior.BlockedUntil) == prior.BlockedUntil {
				supplied.BlockedUntil, supplied.BlockReason = prior.BlockedUntil, prior.BlockReason
			}
		}
		for i := range supplied.Devices {
			if supplied.Devices[i].SubscriptionToken != "" {
				return errors.New("从机授权不接受订阅凭据")
			}
			if d := findDevicePtr(old[supplied.Name], supplied.Devices[i].Name); d != nil {
				supplied.Devices[i].SubscriptionToken = d.SubscriptionToken
				supplied.Devices[i].SourceIPs, supplied.Devices[i].LastSeen = d.SourceIPs, d.LastSeen
			}
			device := &supplied.Devices[i]
			id := supplied.Name + "/" + device.Name
			key := meshPolicyKey(device.IPPolicy, device.Access, BurstPolicy{})
			next.MeshLease.Policies[id] = key
			if d := findDevicePtr(prior, device.Name); d != nil && s.MeshLease != nil && s.MeshLease.Policies[id] == key {
				preserveMeshIPRuntime(&device.IPPolicy, d.IPPolicy)
				device.Access.ConnectionBlockedUntil = laterMeshBlock(device.Access.ConnectionBlockedUntil, d.Access.ConnectionBlockedUntil)
			}
		}
		supplied.Upload, supplied.Download = prior.Upload, prior.Download
		supplied.SourceIPs, supplied.TrafficSamples, supplied.UsageHistory, supplied.RecentAccesses = prior.SourceIPs, prior.TrafficSamples, prior.UsageHistory, prior.RecentAccesses
		for i := range supplied.Nodes {
			n := &supplied.Nodes[i]
			r, ok := meshRouteInfo(s, n.Outbound)
			if !ok || r.Entry != s.MeshAgent.Member || n.Upload != 0 || n.Download != 0 || len(n.Destinations) > 0 {
				return errors.New("从机只能接收本机入口的用户节点")
			}
			for _, previous := range prior.Nodes {
				if previous.AuthUser == n.AuthUser && previous.UUID == n.UUID {
					n.Upload, n.Download = previous.Upload, previous.Download
					n.Destinations = previous.Destinations
				}
			}
		}
		if remaining, ok := access.Quota[supplied.Name]; ok {
			if remaining < 0 || remaining > math.MaxInt64-measuredUsage(supplied) {
				return errors.New("入口配额超限")
			}
			next.MeshLease.Quota[supplied.Name] = remaining + measuredUsage(supplied)
		}
		next.Users = append(next.Users, supplied)
	}
	normalizeDeviceModel(&next)
	if err := validateState(&next); err != nil {
		return errors.New("入口用户授权校验失败")
	}
	oldConfig, err := renderConfig(s)
	if err != nil {
		return err
	}
	newConfig, err := renderConfig(&next)
	if err != nil {
		return err
	}
	changed := !reflect.DeepEqual(oldConfig, newConfig) || !sameMeshRates(s, &next) || s.StatsApplyPending
	if changed {
		if err := meshCheckCandidate(&next); err != nil {
			return err
		}
		next.StatsApplyPending = true
	}
	if err := saveState(a.statePath, &next); err != nil {
		return err
	}
	*s = next
	if changed {
		if err := meshApplyAccess(s); err != nil {
			return err
		}
		s.StatsApplyPending = false
		return saveState(a.statePath, s)
	}
	return nil
}

func findDevicePtr(u User, name string) *Device { return findDevice(&u, name) }
func sameMeshRates(a, b *State) bool {
	x, e1 := renderNftables(a)
	y, e2 := renderNftables(b)
	return e1 == nil && e2 == nil && x == y
}

func (a *app) meshLeaseCycle() error {
	return a.withStateLock(func() error {
		s, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		if s.MeshLease == nil {
			return nil
		}
		blocked := map[string]bool{}
		for _, u := range s.Users {
			if limit, ok := s.MeshLease.Quota[u.Name]; ok && measuredUsage(u) >= limit {
				blocked[u.Name] = true
			}
		}
		expired := !meshLeaseValid(s, time.Now())
		if expired == s.MeshLease.Expired && len(blocked) == len(s.MeshLease.Blocked) && (len(blocked) == 0 || reflect.DeepEqual(blocked, s.MeshLease.Blocked)) {
			return nil
		}
		s.StatsApplyPending = true
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		if err := meshApplyAccess(s); err != nil {
			return err
		}
		s.MeshLease.Expired = expired
		s.MeshLease.Blocked = blocked
		s.StatsApplyPending = false
		return saveState(a.statePath, s)
	})
}

func (a *app) meshSyncAccess() error {
	s, err := a.loadCanonicalState()
	if err != nil {
		return err
	}
	if s.Mesh == nil || s.MeshAgent.Active == nil || s.MeshRollout != nil {
		return nil
	}
	var failures []error
	for _, member := range s.Mesh.Members {
		if member.ID == s.Mesh.Master || member.Client == nil {
			continue
		}
		request := meshRequest{Protocol: mesh.Protocol, Cluster: s.Mesh.ID, Member: member.ID, Operation: "usage"}
		response, err := meshExchange(a, member, request, false)
		if err != nil {
			failures = append(failures, fmt.Errorf("入口 %s 用量同步失败", member.ID))
			continue
		}
		var access meshAccess
		err = a.withStateLock(func() error {
			current, err := loadState(a.statePath)
			if err != nil {
				return err
			}
			if current.Mesh == nil || current.MeshRollout != nil || current.MeshAgent.Active == nil || response.Revision != current.MeshAgent.Active.Revision {
				return errors.New("同步期间拓扑变化")
			}
			if err := mergeMeshUsage(current, member.ID, response.Usage); err != nil {
				return err
			}
			current.MeshSyncSequence++
			access = buildMeshAccess(current, member.ID, current.MeshSyncSequence, time.Now())
			return saveState(a.statePath, current)
		})
		if err != nil {
			failures = append(failures, err)
			continue
		}
		request.Operation, request.Access = "access", &access
		if _, err = meshExchange(a, member, request, false); err != nil {
			failures = append(failures, fmt.Errorf("入口 %s 授权同步失败", member.ID))
		}
	}
	return errors.Join(failures...)
}
