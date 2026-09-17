package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sbmgr/internal/mesh"
	"slices"
	"strconv"
	"strings"
)

// Only metadata is returned. In particular no raw outbound documents or
// upstream credentials cross the browser or inventory RPC boundary.
type webExit struct {
	Tag  string `json:"tag"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// Visual connections join an entry to an exit on that same server. This keeps
// local-only exits local and cannot introduce an accidental relay round trip.
func (a *app) webWireRoute(input webActionInput) error {
	reply := a.webRouteInventory(input.Fields["entry"])
	if reply.Status != 200 {
		return errors.New("无法验证落地目录，请刷新服务器落地后重试")
	}
	var inventory struct {
		Member string    `json:"member"`
		Exits  []webExit `json:"exits"`
	}
	if json.Unmarshal(reply.Body, &inventory) != nil || !slices.ContainsFunc(inventory.Exits, func(e webExit) bool { return e.Tag == input.Fields["exit"] }) {
		return errors.New("该落地不属于所选入口服务器")
	}
	return a.withAuditedStateLock("mesh.wire", nil, func() error {
		s, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		revision, err := strconv.ParseUint(input.Fields["revision"], 10, 64)
		if err != nil || s.Mesh == nil || s.Mesh.Revision != revision {
			return errors.New("线路拓扑已变化，请刷新后重新连线")
		}
		if s.MeshRollout != nil || s.MeshAgent.Transaction != "" {
			return errors.New("请先恢复未完成的拓扑事务")
		}
		exists := slices.ContainsFunc(s.Mesh.Routes, func(r mesh.Route) bool { return r.ID == input.Fields["id"] })
		if exists != (input.Fields["replace"] == "true") {
			return errors.New("线路标识已存在或被删除，请刷新后重试")
		}
		if err := setMeshRouteOptions(s.Mesh, input.Fields["id"], []string{input.Fields["entry"]}, "", "", false, input.Fields["entry"], input.Fields["exit"]); err != nil {
			return err
		}
		s.Mesh.Revision++
		return saveState(a.statePath, s)
	})
}

func webLocalExits(s *State) ([]webExit, error) {
	exits := []webExit{{Tag: "", Name: "直接出站", Type: "direct"}}
	aliases := map[string]string{}
	for _, n := range nodeTemplates(s) {
		aliases[n.Outbound] = n.Name
	}
	for _, kind := range []ManagedProxyKind{ManagedProxyOutbound, ManagedProxyEndpoint} {
		docs, err := listManagedProxyDocuments(s, kind)
		if err != nil {
			return nil, errors.New("无法读取本机落地目录")
		}
		for _, d := range docs {
			if d.Type == "block" || d.Type == "dns" || d.Type == "direct" || strings.HasPrefix(d.Tag, "sbmgr-") {
				continue
			}
			name := aliases[d.Tag]
			if name == "" {
				name = d.Tag
			}
			exits = append(exits, webExit{Tag: d.Tag, Name: name, Type: d.Type})
		}
	}
	if len(exits) > 512 {
		return nil, errors.New("落地数量超过面板上限")
	}
	return exits, nil
}

func (a *app) webRouteInventory(memberID string) webReply {
	s, err := loadState(a.statePath)
	if err != nil {
		return webError(503, "无法读取线路目录")
	}
	if s.Mesh == nil {
		return webError(409, "请在主机管理线路")
	}
	i := slices.IndexFunc(s.Mesh.Members, func(m mesh.Member) bool { return m.ID == memberID })
	if i < 0 {
		return webError(404, "服务器不存在")
	}
	var exits []webExit
	if memberID == s.Mesh.Master {
		exits, err = webLocalExits(s)
	} else {
		worker := &app{statePath: a.statePath, out: io.Discard, err: io.Discard}
		var response meshResponse
		response, err = meshExchange(worker, s.Mesh.Members[i], meshRequest{Protocol: mesh.Protocol, Cluster: s.Mesh.ID, Member: memberID, Operation: "inventory"}, false)
		exits = response.Exits
	}
	if err != nil || len(exits) == 0 || len(exits) > 512 {
		return webError(503, "未能读取该服务器的落地，请检查连接及从机版本后重试；已有线路保持不变")
	}
	return webJSON(200, map[string]any{"member": memberID, "exits": exits})
}

// Reuse deployed client labels where possible without renaming any existing
// nodes or changing their subscription identities.
func webRouteName(s *State, r mesh.Route) string {
	for _, u := range s.Users {
		for _, n := range u.Nodes {
			if n.Outbound == mesh.RouteTag(r.ID) {
				return n.Name
			}
		}
	}
	if r.Exit == "" {
		return strings.ToUpper(s.Mesh.Entry(r))
	}
	return r.Exit + " via " + strings.ToUpper(s.Mesh.Entry(r))
}

func webAssignmentVersion(u *User, device string) string {
	var values []any
	for _, n := range u.Nodes {
		if n.Device == device {
			values = append(values, []any{n.Name, n.Outbound, n.UUID, n.UploadMbps, n.DownloadMbps})
		}
	}
	raw, _ := json.Marshal(values)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func (a *app) webAssignRoutes(input webActionInput) error {
	return a.withAuditedStateLock("node.assign", []string{input.Fields["user"], "--device=" + input.Fields["device"]}, func() error {
		s, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		if webSlaveMutation(s, input.Action) {
			return errors.New("从机用户授权由主机统一管理")
		}
		u := findUser(s, input.Fields["user"])
		if u == nil {
			return errors.New("用户不存在")
		}
		d := findDevice(u, input.Fields["device"])
		if d == nil {
			return errors.New("设备不存在")
		}
		if input.Fields["expected"] != webAssignmentVersion(u, d.Name) {
			return errors.New("此设备的线路已被修改，请关闭分配窗口并重新打开")
		}
		var selection []struct {
			Outbound string `json:"outbound"`
			Name     string `json:"name"`
		}
		if webDecode([]byte(input.Fields["selection"]), &selection) != nil || len(selection) == 0 || len(selection) > 512 {
			return errors.New("至少保留一条线路，最多选择 512 条")
		}
		available, existing, chosen := map[string]bool{}, map[string]bool{}, map[string]bool{}
		for _, n := range nodeTemplates(s) {
			available[n.Outbound] = true
		}
		for _, n := range u.Nodes {
			if n.Device == d.Name {
				existing[n.Outbound] = true
			}
		}
		for _, selected := range selection {
			if chosen[selected.Outbound] {
				return errors.New("线路不能重复选择")
			}
			chosen[selected.Outbound] = true
			if existing[selected.Outbound] {
				continue
			}
			if !available[selected.Outbound] {
				return errors.New("所选线路不存在或尚未应用")
			}
			if strings.HasPrefix(selected.Outbound, "sbmgr-mesh-") && (s.Mesh == nil || s.MeshRollout != nil || s.MeshAgent.Active == nil || s.MeshAgent.Active.Revision != s.Mesh.Revision) {
				return errors.New("请先应用线路拓扑，再分配新线路")
			}
			if err := validateManagedName(selected.Name); err != nil {
				return errors.New("新节点显示名称无效")
			}
		}
		kept := make([]Node, 0, len(u.Nodes)+len(selection))
		names := map[string]bool{}
		for _, n := range u.Nodes {
			if n.Device != d.Name || chosen[n.Outbound] {
				kept = append(kept, n)
				if n.Device == d.Name {
					names[strings.ToLower(n.Name)] = true
				}
			}
		}
		for _, selected := range selection {
			if existing[selected.Outbound] {
				continue
			}
			name := selected.Name
			for suffix := 2; names[strings.ToLower(name)]; suffix++ {
				name = fmt.Sprintf("%s (%d)", selected.Name, suffix)
			}
			names[strings.ToLower(name)] = true
			n := Node{Name: name, Device: d.Name, UUID: newUUID(), Outbound: selected.Outbound, AuthUser: uniqueAuthUser(s, u.Name+":"+slug(d.Name)+":"+slug(name))}
			// Include each new identity in the uniqueness check for the next one.
			u.Nodes = append(u.Nodes, n)
			kept = append(kept, n)
		}
		u.Nodes = kept
		return saveState(a.statePath, s)
	})
}
