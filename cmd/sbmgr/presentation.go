package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

func endpointDisplayAddress(server string, port int) string {
	if strings.TrimSpace(server) == "" || port <= 0 {
		return "-"
	}
	return net.JoinHostPort(server, strconv.Itoa(port))
}
func configurationPending(s *State) bool {
	rendered, err := renderConfig(s)
	if err != nil {
		return true
	}
	current, err := os.ReadFile(s.ConfigPath)
	if err != nil {
		return true
	}
	var want, have any
	if json.Unmarshal(rendered, &want) != nil || json.Unmarshal(current, &have) != nil {
		return true
	}
	return !reflect.DeepEqual(want, have)
}
func runtimeApplyPending(s *State) bool {
	return s != nil && (s.IPApplyPending || s.BurstApplyPending || s.RateApplyPending || s.StatsApplyPending)
}
func userStatus(u User) string {
	if expired(u, time.Now()) {
		return "已到期"
	}
	if overQuota(u) {
		return "配额用尽"
	}
	if burstBlocked(u, time.Now()) {
		if burstSoftBlocked(u, time.Now()) {
			return "软封限速"
		}
		return "硬封断连"
	}
	if !u.Enabled {
		return "已禁用"
	}
	return "已启用"
}
func defaultExportPath(statePath, user string, now time.Time) string {
	chinaTime := now.In(time.FixedZone("Asia/Shanghai", 8*60*60))
	filename := fmt.Sprintf("%s-%s.yaml", slug(user), chinaTime.Format("20060102-150405"))
	return filepath.Join(filepath.Dir(statePath), "exports", filename)
}
func defaultDeviceExportPath(statePath, user, device string, now time.Time) string {
	chinaTime := now.In(time.FixedZone("Asia/Shanghai", 8*60*60))
	devicePart := slug(device)
	if devicePart == "" {
		devicePart = "device"
	}
	filename := fmt.Sprintf("%s-%s-%s.yaml", slug(user), devicePart, chinaTime.Format("20060102-150405"))
	return filepath.Join(filepath.Dir(statePath), "exports", filename)
}
func formatMbpsUI(value float64) string {
	if value == 0 {
		return "不限"
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}
func formatCurrentMbps(value float64) string {
	if value < 0.005 {
		return "0"
	}
	if value < 1 {
		return strconv.FormatFloat(value, 'f', 2, 64)
	}
	return strconv.FormatFloat(value, 'f', 1, 64)
}
func activeConnectionsForUser(s *State, userName string) []ActiveConnection {

	if s == nil {
		return nil
	}
	connections := make([]ActiveConnection, 0, len(s.ActiveConnections))
	now := time.Now()
	for _, connection := range s.ActiveConnections {
		if !connectionActiveAt(connection, now) {
			continue
		}
		if strings.EqualFold(connection.User, userName) {
			connections = append(connections, connection)
		}
	}
	sort.Slice(connections, func(i, j int) bool {
		return connections[i].LastSeen > connections[j].LastSeen
	})
	return connections
}
func formatQuotaForInput(n int64) string {
	if n == 0 {
		return "0"
	}
	return formatSize(n)
}

func normalizeQuotaInput(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "0" {
		return "0"
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return value + "G"
	}
	return value
}
func subscriptionDeliveryLink(s *State, userName, deviceName string) (string, error) {
	u := findUser(s, userName)
	if u == nil {
		return "", errors.New("用户不存在")
	}
	d := findDevice(u, deviceName)
	if d == nil {
		return "", errors.New("设备不存在")
	}
	if !s.Subscription.Enabled {
		return "", errors.New("订阅服务未开启，请先完成 HTTPS/服务设置")
	}
	if err := subscriptionDeviceAvailable(*u, *d, time.Now()); err != nil {
		return "", err
	}
	if d.SubscriptionToken == "" {
		return "", errors.New("设备订阅尚未初始化，请刷新后重试")
	}
	return subscriptionURL(s, *d), nil
}

func userRateSummary(u User) string {
	if rateLimited(u) {
		return formatMbpsUI(u.UploadMbps) + " / " + formatMbpsUI(u.DownloadMbps)
	}
	limited := 0
	for _, n := range u.Nodes {
		if nodeRateLimited(n) {
			limited++
		}
	}
	if limited == 0 {
		return "不限"
	}
	return fmt.Sprintf("逐节点 (%d)", limited)
}
