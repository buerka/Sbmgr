package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// setClientEndpoint updates the public address embedded in future exports and
// subscription responses. It deliberately does not touch the sing-box listen
// address or Reality keys: moving the relay entry point is a separate concern
// from changing its credentials.
func (a *app) setClientEndpoint(server string, port int) error {
	server = strings.TrimSpace(server)
	args := []string{"--from", "-", "--to", net.JoinHostPort(server, strconv.Itoa(port))}
	return a.withAuditedStateLock("client.endpoint.update", args, func() error {
		if err := validateOutboundServer(server); err != nil {
			return fmt.Errorf("中转入口地址: %w", err)
		}
		if err := validateOutboundPort(port); err != nil {
			return fmt.Errorf("中转入口端口: %w", err)
		}
		s, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		args[1] = net.JoinHostPort(s.Client.Server, strconv.Itoa(s.Client.Port))
		if s.Client.Server == server && s.Client.Port == port {
			fmt.Fprintln(a.out, "中转入口没有变化")
			return nil
		}
		s.Client.Server, s.Client.Port = server, port
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "中转入口已更新为 %s；订阅会立即使用新地址，已导出的旧文件需要重新导出\n", net.JoinHostPort(server, strconv.Itoa(port)))
		return nil
	})
}
