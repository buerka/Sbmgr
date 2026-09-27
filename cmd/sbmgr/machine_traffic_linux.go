//go:build linux

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// readPhysicalUplinkCounters reads host-level NIC counters, independent of
// sing-box users and mesh transit. Only the kernel's default-route devices
// are selected, so bridge members and overlay interfaces are not summed on
// top of the egress device. A changing selected device set breaks the sample
// interval; the caller starts a fresh baseline.
func readPhysicalUplinkCounters() (string, int64, int64, error) {
	interfaces := map[string]bool{}
	routes, err := os.Open("/proc/net/route")
	if err != nil {
		return "", 0, 0, err
	}
	scanner := bufio.NewScanner(routes)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 4 && fields[1] == "00000000" {
			flags, parseErr := strconv.ParseUint(fields[3], 16, 32)
			if parseErr == nil && flags&1 != 0 {
				interfaces[fields[0]] = true
			}
		}
	}
	routeErr := scanner.Err()
	_ = routes.Close()
	if routeErr != nil {
		return "", 0, 0, routeErr
	}
	// IPv6-only hosts can have no IPv4 default route.
	if data, err := os.ReadFile("/proc/net/ipv6_route"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 10 && fields[0] == strings.Repeat("0", 32) && fields[1] == "00" {
				interfaces[fields[len(fields)-1]] = true
			}
		}
	}
	var names []string
	for name := range interfaces {
		if name == "lo" || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "tun") || strings.HasPrefix(name, "tap") || strings.HasPrefix(name, "wg") || strings.HasPrefix(name, "dummy") {
			continue
		}
		device := filepath.Join("/sys/class/net", name)
		if _, err := os.Stat(filepath.Join(device, "device")); err != nil {
			// Bond and bridge devices can be the sole default-route
			// aggregate. Include their counters once at that layer.
			if _, bond := os.Stat(filepath.Join(device, "bonding")); bond != nil {
				if _, bridge := os.Stat(filepath.Join(device, "bridge")); bridge != nil {
					continue
				}
			}
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return "", 0, 0, errors.New("没有可计量的物理默认路由网卡")
	}
	// If both an aggregate and one of its members have default routes,
	// count the aggregate once. Linux exposes bridge/bond membership through
	// the physical device's master symlink.
	selected := map[string]bool{}
	for _, name := range names {
		selected[name] = true
	}
	filtered := names[:0]
	for _, name := range names {
		master, err := os.Readlink(filepath.Join("/sys/class/net", name, "master"))
		if err == nil && selected[filepath.Base(master)] {
			continue
		}
		filtered = append(filtered, name)
	}
	names = filtered
	sort.Strings(names)
	var rx, tx int64
	for _, name := range names {
		base := filepath.Join("/sys/class/net", name, "statistics")
		read := func(filename string) (int64, error) {
			data, err := os.ReadFile(filepath.Join(base, filename))
			if err != nil {
				return 0, err
			}
			value, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
			if err != nil || value < 0 {
				return 0, fmt.Errorf("网卡计数器无效")
			}
			return value, nil
		}
		x, err := read("rx_bytes")
		if err != nil {
			return "", 0, 0, err
		}
		y, err := read("tx_bytes")
		if err != nil {
			return "", 0, 0, err
		}
		if rx > int64(^uint64(0)>>1)-x || tx > int64(^uint64(0)>>1)-y {
			return "", 0, 0, errors.New("网卡计数器超出范围")
		}
		rx += x
		tx += y
	}
	return strings.Join(names, ","), rx, tx, nil
}
