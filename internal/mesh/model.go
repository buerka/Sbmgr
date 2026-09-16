// Package mesh separates node membership from traffic protocols.
package mesh

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"regexp"
	"strconv"
)

const (
	Protocol   = 2 // Management RPC version; unrelated to the traffic protocol.
	MaxMembers = 128
	MaxRoutes  = 256
	MaxHops    = 16
	MaxMessage = 2 << 20
)

var identifier = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
var hostname = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`)
var exitTag = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)

// Member describes a management connection, with no traffic-protocol fields.
type Member struct {
	ID         string  `json:"id"`
	SSHHost    string  `json:"ssh_host,omitempty"`
	SSHPort    int     `json:"ssh_port,omitempty"`
	SSHUser    string  `json:"ssh_user,omitempty"`
	SSHKeyPath string  `json:"ssh_key_path,omitempty"`
	AppDir     string  `json:"app_dir,omitempty"`
	Client     *Client `json:"client,omitempty"`
}

// Client contains only the public parameters of an independently managed entry.
type Client struct {
	Server     string `json:"server"`
	Port       int    `json:"port"`
	ServerName string `json:"server_name"`
	PublicKey  string `json:"reality_public_key"`
	ShortID    string `json:"short_id"`
}

func (c Client) Validate() error {
	key, err := base64.RawURLEncoding.DecodeString(c.PublicKey)
	_, shortErr := hex.DecodeString(c.ShortID)
	ip := net.ParseIP(c.Server)
	if c.Port < 1 || c.Port > 65535 || (!hostname.MatchString(c.Server) && ip == nil) || (ip != nil && (ip.IsUnspecified() || ip.IsMulticast())) || !hostname.MatchString(c.ServerName) || err != nil || len(key) != 32 || shortErr != nil || len(c.ShortID) > 16 {
		return errors.New("客户端入口参数无效")
	}
	return nil
}

type RouteInfo struct {
	ID     string  `json:"id"`
	Entry  string  `json:"entry"`
	Client *Client `json:"client,omitempty"`
}

// Transports describes the incoming connection of each listed hop.
// A sole master hop means local egress and needs no transport.
type Route struct {
	ID         string      `json:"id"`
	Entry      string      `json:"entry,omitempty"`
	Exit       string      `json:"exit,omitempty"`
	Hops       []string    `json:"hops"`
	Transports []Transport `json:"transports,omitempty"`
}
type Topology struct {
	ID       string   `json:"id"`
	Master   string   `json:"master"`
	Revision uint64   `json:"revision"`
	Members  []Member `json:"members"`
	Routes   []Route  `json:"routes"`
}
type Hop struct {
	ID       string      `json:"id"`
	Entry    bool        `json:"entry,omitempty"`
	Exit     string      `json:"exit,omitempty"`
	Incoming *Connection `json:"incoming,omitempty"`
	Outgoing *Connection `json:"outgoing,omitempty"`
}

// Plan contains local listeners and next-hop dialers only. Enrollment itself
// carries no listeners, traffic credentials, peer keys, or routing addresses.
type Plan struct {
	Protocol int         `json:"protocol"`
	Cluster  string      `json:"cluster"`
	Member   string      `json:"member"`
	Master   bool        `json:"master"`
	Revision uint64      `json:"revision"`
	Hops     []Hop       `json:"hops,omitempty"`
	Catalog  []RouteInfo `json:"catalog,omitempty"`
}

func (t Topology) Entry(r Route) string {
	if r.Entry != "" {
		return r.Entry
	}
	return t.Master
}

func ValidID(id string) bool    { return identifier.MatchString(id) }
func RouteTag(id string) string { return "sbmgr-mesh-" + id }
func ValidateEndpoint(value string) error {
	host, rawPort, err := net.SplitHostPort(value)
	port, portErr := strconv.Atoi(rawPort)
	if err != nil || portErr != nil || port < 1024 || port > 65535 {
		return errors.New("线路地址必须是主机:端口，端口范围 1024–65535")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.IsUnspecified() || ip.IsMulticast() || ip.Zone() != "" {
			return errors.New("线路目标地址无效")
		}
	} else if !hostname.MatchString(host) {
		return errors.New("线路目标主机名无效")
	}
	return nil
}
func (t Topology) Validate() error {
	if !ValidID(t.ID) || !ValidID(t.Master) || t.Revision == 0 || len(t.Members) < 1 || len(t.Members) > MaxMembers || len(t.Routes) > MaxRoutes {
		return errors.New("主从标识、修订号或数量无效")
	}
	members := map[string]bool{}
	for _, m := range t.Members {
		if !ValidID(m.ID) || members[m.ID] {
			return errors.New("节点标识无效或重复")
		}
		members[m.ID] = true
		if m.Client != nil {
			if err := m.Client.Validate(); err != nil {
				return err
			}
		}
	}
	if !members[t.Master] {
		return errors.New("主机不在节点集合中")
	}
	names, listeners := map[string]bool{}, map[string]bool{}
	for _, r := range t.Routes {
		if !ValidID(r.ID) || names[r.ID] || len(r.Hops) < 1 || len(r.Hops) > MaxHops {
			return errors.New("线路标识无效或重复；线路需要 1 至 16 跳")
		}
		entry := t.Entry(r)
		if !members[entry] || (r.Exit != "" && !exitTag.MatchString(r.Exit)) {
			return errors.New("线路入口或落地出站无效")
		}
		for _, m := range t.Members {
			if m.ID == entry && entry != t.Master && m.Client == nil {
				return errors.New("从机入口需要先登记公开连接参数")
			}
		}
		local := len(r.Hops) == 1 && r.Hops[0] == entry
		if (local && len(r.Transports) != 0) || (!local && len(r.Transports) != len(r.Hops)) {
			return errors.New("每一跳必须选择传输协议；本机落地不需要协议")
		}
		seen := map[string]bool{}
		for i, id := range r.Hops {
			if !members[id] || seen[id] || (id == entry && !local) {
				return errors.New("线路包含不存在的节点或循环")
			}
			seen[id] = true
			if local {
				continue
			}
			tr := r.Transports[i]
			if err := tr.Validate(); err != nil {
				return err
			}
			key := id + ":" + strconv.Itoa(tr.Port)
			if listeners[key] {
				return errors.New("同一节点的线路监听端口重复")
			}
			listeners[key] = true
		}
		names[r.ID] = true
	}
	return nil
}
func (t Topology) Compile(id string) (Plan, error) {
	if err := t.Validate(); err != nil {
		return Plan{}, err
	}
	found := false
	for _, m := range t.Members {
		found = found || m.ID == id
	}
	if !found {
		return Plan{}, errors.New("节点不存在")
	}
	p := Plan{Protocol: Protocol, Cluster: t.ID, Member: id, Master: id == t.Master, Revision: t.Revision}
	for _, r := range t.Routes {
		entry := t.Entry(r)
		if p.Master || entry == id {
			info := RouteInfo{ID: r.ID, Entry: entry}
			for _, m := range t.Members {
				if m.ID == entry {
					info.Client = m.Client
				}
			}
			p.Catalog = append(p.Catalog, info)
		}
		if entry == id {
			h := Hop{ID: r.ID, Entry: true}
			if len(r.Transports) > 0 {
				h.Outgoing = r.Transports[0].Connection(false)
			} else {
				h.Exit = r.Exit
			}
			p.Hops = append(p.Hops, h)
			continue
		}
		for i, member := range r.Hops {
			if member != id {
				continue
			}
			h := Hop{ID: r.ID, Incoming: r.Transports[i].Connection(true)}
			if i+1 < len(r.Hops) {
				h.Outgoing = r.Transports[i+1].Connection(false)
			} else {
				h.Exit = r.Exit
			}
			p.Hops = append(p.Hops, h)
		}
	}
	return p, p.Validate()
}
func (p Plan) Validate() error {
	if p.Protocol != Protocol || !ValidID(p.Cluster) || !ValidID(p.Member) || p.Revision == 0 || len(p.Hops) > MaxRoutes {
		return errors.New("节点管理协议、标识或修订号无效")
	}
	names, ports := map[string]bool{}, map[int]bool{}
	for _, h := range p.Hops {
		if !ValidID(h.ID) || names[h.ID] || (h.Entry && h.Incoming != nil) || (!p.Master && !h.Entry && h.Incoming == nil) || (h.Exit != "" && (h.Outgoing != nil || !exitTag.MatchString(h.Exit))) {
			return errors.New("节点线路角色无效或重复")
		}
		if h.Incoming != nil {
			if err := h.Incoming.Validate(true); err != nil {
				return err
			}
			if ports[h.Incoming.Port] {
				return errors.New("节点监听端口重复")
			}
			ports[h.Incoming.Port] = true
		}
		if h.Outgoing != nil {
			if err := h.Outgoing.Validate(false); err != nil {
				return err
			}
		}
		names[h.ID] = true
	}
	if len(p.Catalog) > MaxRoutes {
		return errors.New("入口线路数量超限")
	}
	seen := map[string]bool{}
	for _, r := range p.Catalog {
		if !ValidID(r.ID) || !ValidID(r.Entry) || seen[r.ID] || (!p.Master && r.Entry != p.Member) {
			return errors.New("入口线路目录无效")
		}
		seen[r.ID] = true
		if r.Client != nil {
			if err := r.Client.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}
