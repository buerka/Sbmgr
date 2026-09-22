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
	return webRouteNameWithAliases(s, r, webRouteAliasMap(s))
}

func webRouteNameWithAliases(s *State, r mesh.Route, aliases map[string]string) string {
	canonical := mesh.RouteTag(r.ID)
	for _, u := range s.Users {
		for _, n := range u.Nodes {
			if webCanonicalOutboundWithAliases(aliases, n.Outbound) == canonical {
				return n.Name
			}
		}
	}
	if r.Exit == "" {
		return strings.ToUpper(s.Mesh.Entry(r))
	}
	return r.Exit + " via " + strings.ToUpper(s.Mesh.Entry(r))
}

// webRouteAliasMap maps legacy local outbounds to the mesh route that now
// represents the same path.  The map is deliberately conservative: aliases
// are inferred only for an applied, local master route and only when the old
// outbound has the same sing-box semantics.  Ambiguous aliases are discarded
// instead of silently assigning an old node to the wrong entry.
func webRouteAliasMap(s *State) map[string]string {
	aliases := map[string]string{}
	if s == nil || s.Mesh == nil || s.MeshAgent.Active == nil || s.MeshRollout != nil || s.MeshAgent.Transaction != "" || s.MeshAgent.Active.Revision != s.Mesh.Revision || !s.MeshAgent.Active.Master {
		return aliases
	}
	base, err := readOutboundBaseConfig(s.BaseConfig)
	if err != nil {
		return aliases
	}
	tags := map[string]string{}
	plainDirect := map[string]bool{}
	for index, raw := range base.outbounds {
		object, err := decodeOutboundObject(raw, index)
		if err != nil {
			return map[string]string{}
		}
		tag, err := optionalJSONString(object, "tag")
		if err != nil || tag == "" {
			continue
		}
		tags[tag] = tag
		kind, err := optionalJSONString(object, "type")
		if err == nil && strings.EqualFold(kind, "direct") && len(object) == 2 {
			plainDirect[tag] = true
		}
	}
	final := base.finalOutboundTag()
	if final == "" && len(base.outbounds) > 0 {
		// sing-box and the rate renderer both resolve an omitted route.final
		// to the first outbound.  Treat the empty legacy tag as a direct
		// route only when that effective target is a plain direct outbound.
		if first, err := decodeOutboundObject(base.outbounds[0], 0); err == nil {
			final, _ = optionalJSONString(first, "tag")
		}
	}
	add := func(old, routeID string) {
		if _, exists := aliases[old]; exists {
			if aliases[old] != routeID {
				// Keep an explicit collision marker. It is removed below and
				// therefore cannot cause an unsafe merge.
				aliases[old] = ""
			}
			return
		}
		aliases[old] = routeID
	}
	for _, r := range s.Mesh.Routes {
		entry := s.Mesh.Entry(r)
		local := entry == s.Mesh.Master && len(r.Hops) == 1 && r.Hops[0] == entry && len(r.Transports) == 0
		if !local || !webActiveMasterRoute(s, r.ID) {
			continue
		}
		routeID := mesh.RouteTag(r.ID)
		if r.Exit != "" {
			// mesh.Plan.Augment clones the configured outbound, so the
			// legacy tag is equivalent only when that outbound exists.
			if _, ok := tags[r.Exit]; ok {
				add(r.Exit, routeID)
			}
			continue
		}
		// An empty legacy outbound follows route.final.  A mesh direct
		// route is equivalent only to a plain direct final, never to a
		// proxy, selector, or customised direct outbound.
		if plainDirect[final] {
			add("", routeID)
			add(final, routeID)
		}
	}
	for old, routeID := range aliases {
		if routeID == "" {
			delete(aliases, old)
		}
	}
	return aliases
}

func webActiveMasterRoute(s *State, id string) bool {
	if s == nil || s.Mesh == nil || s.MeshAgent.Active == nil || !s.MeshAgent.Active.Master || s.MeshAgent.Active.Revision != s.Mesh.Revision {
		return false
	}
	for _, route := range s.MeshAgent.Active.Catalog {
		if route.ID == id && route.Entry == s.Mesh.Master {
			return true
		}
	}
	return false
}

func webCanonicalOutbound(s *State, outbound string) string {
	return webCanonicalOutboundWithAliases(webRouteAliasMap(s), outbound)
}

func webCanonicalOutboundWithAliases(aliases map[string]string, outbound string) string {
	if strings.HasPrefix(outbound, "sbmgr-mesh-") {
		return outbound
	}
	if routeID, ok := aliases[outbound]; ok {
		return routeID
	}
	return outbound
}

func webRouteAliases(s *State, id string) []string {
	return webRouteAliasesFromMap(webRouteAliasMap(s), id)
}

func webRouteAliasesFromMap(aliases map[string]string, id string) []string {
	routeID := mesh.RouteTag(id)
	result := make([]string, 0, 2)
	for old, canonical := range aliases {
		if canonical == routeID {
			result = append(result, old)
		}
	}
	slices.Sort(result)
	return result
}

func webAssignmentVersion(s *State, u *User, device string) string {
	aliases := webRouteAliasMap(s)
	var values []any
	for _, n := range u.Nodes {
		if n.Device == device {
			// Include the raw tag as well as its canonical route.  This keeps
			// the optimistic lock sensitive to an identity/config change while
			// allowing a legacy alias and its mesh route to share one option.
			values = append(values, []any{n.Name, n.Outbound, webCanonicalOutboundWithAliases(aliases, n.Outbound), n.UUID, n.UploadMbps, n.DownloadMbps})
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
		if input.Fields["expected"] != webAssignmentVersion(s, u, d.Name) {
			return errors.New("此设备的线路已被修改，请关闭分配窗口并重新打开")
		}
		var selection []struct {
			Outbound string `json:"outbound"`
			Name     string `json:"name"`
		}
		if webDecode([]byte(input.Fields["selection"]), &selection) != nil || len(selection) == 0 || len(selection) > 512 {
			return errors.New("至少保留一条线路，最多选择 512 条")
		}
		aliases := webRouteAliasMap(s)
		available, existing, chosen := map[string]bool{}, map[string]bool{}, map[string]bool{}
		for _, n := range nodeTemplates(s) {
			available[webCanonicalOutboundWithAliases(aliases, n.Outbound)] = true
		}
		for _, n := range u.Nodes {
			if n.Device == d.Name {
				existing[webCanonicalOutboundWithAliases(aliases, n.Outbound)] = true
			}
		}
		for _, selected := range selection {
			canonical := webCanonicalOutboundWithAliases(aliases, selected.Outbound)
			if chosen[canonical] {
				return errors.New("线路不能重复选择")
			}
			chosen[canonical] = true
			if existing[canonical] {
				continue
			}
			if !available[canonical] {
				return errors.New("所选线路不存在或尚未应用")
			}
			if strings.HasPrefix(canonical, "sbmgr-mesh-") && (s.Mesh == nil || s.MeshRollout != nil || s.MeshAgent.Active == nil || s.MeshAgent.Active.Revision != s.Mesh.Revision) {
				return errors.New("请先应用线路拓扑，再分配新线路")
			}
			if err := validateManagedName(selected.Name); err != nil {
				return errors.New("新节点显示名称无效")
			}
		}
		kept := make([]Node, 0, len(u.Nodes)+len(selection))
		names := map[string]bool{}
		for _, n := range u.Nodes {
			if n.Device != d.Name || chosen[webCanonicalOutboundWithAliases(aliases, n.Outbound)] {
				kept = append(kept, n)
				if n.Device == d.Name {
					names[strings.ToLower(n.Name)] = true
				}
			}
		}
		for _, selected := range selection {
			canonical := webCanonicalOutboundWithAliases(aliases, selected.Outbound)
			if existing[canonical] {
				continue
			}
			name := selected.Name
			for suffix := 2; names[strings.ToLower(name)]; suffix++ {
				name = fmt.Sprintf("%s (%d)", selected.Name, suffix)
			}
			names[strings.ToLower(name)] = true
			n := Node{Name: name, Device: d.Name, UUID: newUUID(), Outbound: canonical, AuthUser: uniqueAuthUser(s, u.Name+":"+slug(d.Name)+":"+slug(name))}
			// Include each new identity in the uniqueness check for the next one.
			u.Nodes = append(u.Nodes, n)
			kept = append(kept, n)
		}
		u.Nodes = kept
		return saveState(a.statePath, s)
	})
}
