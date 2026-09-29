package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
)

func wireGuardUserAssignable(endpoint map[string]any) bool {
	if stringValue(endpoint["type"]) != "wireguard" || endpoint["system"] == true {
		return false
	}
	peers, _ := endpoint["peers"].([]any)
	for _, item := range peers {
		peer, _ := item.(map[string]any)
		if stringValue(peer["address"]) != "" {
			return true
		}
	}
	return false
}

// Passive exit peers are bridgeable without becoming automatically discovered
// node templates. A default route distinguishes an exit from a transit listener.
// sing-box check remains the authoritative protocol configuration validation.
func wireGuardBridgeable(endpoint map[string]any) bool {
	if wireGuardUserAssignable(endpoint) {
		return true
	}
	if stringValue(endpoint["type"]) != "wireguard" || endpoint["system"] != false {
		return false
	}
	port := 0
	switch value := endpoint["listen_port"].(type) {
	case int:
		port = value
	case float64:
		if value < 1 || value > 65535 || value != float64(int(value)) {
			return false
		}
		port = int(value)
	}
	if port < 1 || port > 65535 {
		return false
	}
	peers, _ := endpoint["peers"].([]any)
	for _, item := range peers {
		peer, _ := item.(map[string]any)
		if stringValue(peer["address"]) != "" {
			continue
		}
		publicKey, err := base64.StdEncoding.DecodeString(stringValue(peer["public_key"]))
		if err != nil || len(publicKey) != 32 {
			continue
		}
		allowed, _ := peer["allowed_ips"].([]any)
		if single, ok := peer["allowed_ips"].(string); ok {
			allowed = []any{single}
		}
		valid, defaultRoute := len(allowed) > 0, false
		for _, value := range allowed {
			prefix, err := netip.ParsePrefix(stringValue(value))
			if err != nil {
				valid = false
				break
			}
			defaultRoute = defaultRoute || prefix.Bits() == 0
		}
		if valid && defaultRoute {
			return true
		}
	}
	return false
}

// A WireGuard endpoint is shared state and must not be cloned with the same
// peer identity per user. An authenticated loopback SOCKS bridge provides real
// per-user sockets for routing_mark/nft accounting before entering that stack.
// UDP-over-TCP also keeps UDP accounting on those per-user loopback sockets.
func addWireGuardBridge(cfg map[string]any, endpoint map[string]any, usedTags map[string]bool) (map[string]any, error) {
	if !wireGuardBridgeable(endpoint) {
		return nil, errors.New("WG 桥接需要已配置远端的用户态端点，或具有有效监听端口、peer 公钥及默认路由 allowed_ips 的被动用户态端点")
	}
	key := stringValue(endpoint["private_key"])
	if key == "" {
		return nil, errors.New("WG 端点缺少协议私钥")
	}
	target := stringValue(endpoint["tag"])
	id := sha256.Sum256([]byte(target))
	tag := fmt.Sprintf("sbmgr-wg-bridge-%x", id[:8])
	if usedTags[tag] {
		return nil, errors.New("基础配置占用了 WG 内部桥接 tag")
	}
	usedTags[tag] = true
	ports := map[int]bool{}
	for _, section := range []string{"inbounds", "endpoints"} {
		items, _ := cfg[section].([]any)
		for _, item := range items {
			object, _ := item.(map[string]any)
			switch port := object["listen_port"].(type) {
			case float64:
				ports[int(port)] = true
			case int:
				ports[port] = true
			}
		}
	}
	port := 48000
	for ports[port] && port <= 65535 {
		port++
	}
	if port > 65535 {
		return nil, errors.New("没有可用的 WG 本机桥接端口")
	}
	secret := sha256.Sum256([]byte("sbmgr/wg-bridge/v1\x00" + target + "\x00" + key))
	password := hex.EncodeToString(secret[:])
	inbound := map[string]any{"type": "socks", "tag": tag, "listen": "127.0.0.1", "listen_port": port, "users": []any{map[string]any{"username": tag, "password": password}}}
	inbounds, _ := cfg["inbounds"].([]any)
	cfg["inbounds"] = append(inbounds, inbound)
	route, _ := cfg["route"].(map[string]any)
	if route == nil {
		route = map[string]any{}
		cfg["route"] = route
	}
	rules, _ := route["rules"].([]any)
	route["rules"] = append([]any{map[string]any{"inbound": []any{tag}, "action": "route", "outbound": target}}, rules...)
	return map[string]any{"type": "socks", "server": "127.0.0.1", "server_port": port, "version": "5", "username": tag, "password": password, "udp_over_tcp": true}, nil
}
