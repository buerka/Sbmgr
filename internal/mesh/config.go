package mesh

import "errors"

// Augment adds traffic listeners and dialers without OS interfaces or host routes.
func (p Plan) Augment(cfg map[string]any) error {
	if err := p.Validate(); err != nil {
		return err
	}
	used := map[string]bool{}
	for _, section := range []string{"inbounds", "outbounds", "endpoints"} {
		items, _ := cfg[section].([]any)
		for _, item := range items {
			m, _ := item.(map[string]any)
			tag, _ := m["tag"].(string)
			used[tag] = true
		}
	}
	for _, h := range p.Hops {
		for _, c := range []*Connection{h.Incoming, h.Outgoing} {
			if c != nil {
				if err := c.checkRuntime(); err != nil {
					return err
				}
			}
		}
		if used[RouteTag(h.ID)] || used[RouteTag(h.ID)+"-in"] {
			return errors.New("基础配置与受管主从线路 tag 冲突")
		}
		used[RouteTag(h.ID)], used[RouteTag(h.ID)+"-in"] = true, true
	}
	inbounds, _ := cfg["inbounds"].([]any)
	outbounds, _ := cfg["outbounds"].([]any)
	endpoints, _ := cfg["endpoints"].([]any)
	route, _ := cfg["route"].(map[string]any)
	if route == nil {
		route = map[string]any{}
		cfg["route"] = route
	}
	var rules []any
	for _, h := range p.Hops {
		tag := RouteTag(h.ID)
		if h.Outgoing == nil {
			if h.Exit == "" {
				outbounds = append(outbounds, map[string]any{"type": "direct", "tag": tag})
			} else {
				found := false
				for _, item := range outbounds {
					original, _ := item.(map[string]any)
					if original["tag"] != h.Exit {
						continue
					}
					copied := map[string]any{}
					for k, v := range original {
						copied[k] = v
					}
					copied["tag"] = tag
					outbounds = append(outbounds, copied)
					found = true
					break
				}
				if !found {
					return errors.New("线路末跳缺少指定的本机落地出站")
				}
			}
		} else if h.Outgoing.Type == "wireguard" {
			endpoints = append(endpoints, h.Outgoing.wireGuard(tag, false))
		} else {
			outbounds = append(outbounds, h.Outgoing.proxy(tag, false))
		}
		if h.Incoming != nil {
			in := tag + "-in"
			if h.Incoming.Type == "wireguard" {
				endpoints = append(endpoints, h.Incoming.wireGuard(in, true))
			} else {
				inbounds = append(inbounds, h.Incoming.proxy(in, true))
			}
			rules = append(rules, map[string]any{"inbound": []any{in}, "action": "route", "outbound": tag})
		}
	}
	existing, _ := route["rules"].([]any)
	route["rules"] = append(rules, existing...)
	cfg["inbounds"], cfg["outbounds"] = inbounds, outbounds
	if len(endpoints) > 0 {
		cfg["endpoints"] = endpoints
	}
	return nil
}
func (c Connection) proxy(tag string, incoming bool) map[string]any {
	m := map[string]any{"type": c.Type, "tag": tag}
	if incoming {
		m["listen"], m["listen_port"] = "::", c.Port
	} else {
		m["server"], m["server_port"] = c.Server, c.Port
	}
	if c.Type == "socks" {
		if incoming {
			m["users"] = []any{map[string]any{"username": "sbmgr", "password": c.Credential}}
		} else {
			m["version"], m["username"], m["password"] = "5", "sbmgr", c.Credential
			m["udp_over_tcp"] = true
		}
	} else {
		tls := map[string]any{"enabled": true, "certificate": []any{c.Certificate}}
		if incoming {
			m["users"] = []any{map[string]any{"name": tag, "password": c.Credential}}
			tls["key"] = []any{c.PrivateKey}
		} else {
			m["password"] = c.Credential
			tls["server_name"] = "sbmgr-relay"
		}
		m["tls"] = tls
	}
	return m
}
func (c Connection) wireGuard(tag string, incoming bool) map[string]any {
	// Each userspace endpoint has its own stack; addresses never add host routes.
	address := []any{"10.89.0.1/32", "fd89::1/128"}
	peer := map[string]any{"public_key": c.PeerKey, "allowed_ips": []any{"0.0.0.0/0", "::/0"}, "address": c.Server, "port": c.Port, "persistent_keepalive_interval": 25}
	m := map[string]any{"type": "wireguard", "tag": tag, "system": false, "mtu": 1408, "private_key": c.PrivateKey}
	if incoming {
		address = []any{"10.89.0.2/32", "fd89::2/128"}
		peer = map[string]any{"public_key": c.PeerKey, "allowed_ips": []any{"10.89.0.1/32", "fd89::1/128"}}
		m["listen_port"] = c.Port
	}
	m["address"], m["peers"] = address, []any{peer}
	return m
}
