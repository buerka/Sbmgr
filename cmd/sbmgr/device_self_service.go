package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"slices"
	"strings"
	"time"
)

const maxSelfServiceDevices = 100

func flagWasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func deviceDisplayName(d Device) string {
	if d.Label != "" {
		return d.Label
	}
	return d.Name
}

func validateDeviceSelfService(s *State) error {
	for _, u := range s.Users {
		if u.DeviceLimit < 0 || u.DeviceLimit > maxSelfServiceDevices {
			return errors.New("设备名额必须为 0–100")
		}
		if s.Mesh == nil && s.MeshAgent.Cluster != "" && u.DeviceLimit != 0 {
			return errors.New("设备名额仅由主机管理")
		}
		labels := map[string]bool{}
		for _, d := range u.Devices {
			name := deviceDisplayName(d)
			if err := validateManagedName(name); err != nil {
				return errors.New("设备显示名称无效")
			}
			key := strings.ToLower(name)
			if labels[key] {
				return errors.New("设备显示名称不能重复")
			}
			labels[key] = true
		}
	}
	return nil
}

// Ignore counters but include every field that controls the operation. Neither
// the version nor the response exposes identity material to the browser.
func portalDeviceVersion(u *User) string {
	rawUser, _ := json.Marshal(u)
	var copyUser User
	_ = json.Unmarshal(rawUser, &copyUser)
	copyUser.Upload, copyUser.Download = 0, 0
	s := &State{Users: []User{copyUser}}
	stripSQLiteRuntime(s)
	raw, _ := json.Marshal(s.Users[0])
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

type portalDeviceInput struct {
	Action   string `json:"action"`
	Device   string `json:"device,omitempty"`
	Name     string `json:"name,omitempty"`
	From     string `json:"from,omitempty"`
	Expected string `json:"expected"`
}

func newPersonalDevice(s *State, u *User, name, sourceName string, now time.Time) error {
	if len(u.Devices) >= u.DeviceLimit {
		return errors.New("设备名额已用完，请删除不再使用的设备或联系管理员增加名额")
	}
	source := findDevice(u, sourceName)
	if source == nil || subscriptionDeviceAvailable(*u, *source, now) != nil || len(nodesForDevice(*u, source.Name)) == 0 {
		return errors.New("请选择一台可用且已有线路的设备作为配置来源")
	}
	if until, err := time.Parse(time.RFC3339Nano, source.Access.ConnectionBlockedUntil); err == nil && now.Before(until) {
		return errors.New("来源设备当前受访问限制，请稍后重试")
	}
	for _, d := range u.Devices {
		if strings.EqualFold(name, d.Name) || strings.EqualFold(name, deviceDisplayName(d)) {
			return errors.New("设备名称已存在")
		}
	}
	d := Device{Name: name, Enabled: true, CreatedAt: now.Format(time.RFC3339), SubscriptionToken: newSubscriptionToken(), IPPolicy: source.IPPolicy, Access: source.Access}
	// New hardware gets a new learned binding. Fixed administrator restrictions
	// and temporary allowlists are retained; user-wide controls still apply.
	d.IPPolicy.BoundLastSeen = nil
	if d.IPPolicy.Binding != "manual" {
		d.IPPolicy.BoundIPs = nil
	}
	d.Access.ConnectionBlockedUntil, d.Access.LastConnectionAlert = "", ""
	sources := nodesForDevice(*u, source.Name)
	u.Devices = append(u.Devices, d)
	for _, source := range sources {
		mark, err := allocateRateMark(s)
		if err != nil {
			return err
		}
		u.Nodes = append(u.Nodes, Node{Name: source.Name, Device: name, AuthUser: uniqueAuthUser(s, u.Name+":"+slug(name)+":"+slug(source.Name)), UUID: newUUID(), Outbound: source.Outbound, UploadMbps: source.UploadMbps, DownloadMbps: source.DownloadMbps, RateMark: mark})
	}
	return nil
}

func changePersonalDevice(s *State, u *User, input portalDeviceInput, now time.Time) error {
	if u.DeviceLimit == 0 && input.Action != "rotate-link" {
		return errors.New("管理员尚未开放自助设备管理")
	}
	if input.Expected != portalDeviceVersion(u) {
		return errors.New("设备或授权已变化，请刷新后重试；没有保存任何修改")
	}
	if input.Action != "rotate-link" && (!u.Enabled || expired(*u, now) || overQuota(*u) || burstBlocked(*u, now)) {
		return errors.New("账号当前受限，暂时不能修改设备，请联系管理员")
	}
	name := strings.TrimSpace(input.Name)
	if input.Action == "add" || input.Action == "rename" {
		if err := validateManagedName(name); err != nil {
			return errors.New("请输入有效的设备名称")
		}
	}
	switch input.Action {
	case "rotate-link":
		d := findDevice(u, input.Device)
		if d == nil {
			return errors.New("设备不存在")
		}
		d.SubscriptionToken = newSubscriptionToken()
		return nil
	case "add":
		if input.Device != "" {
			return errors.New("新增设备请求格式不正确")
		}
		return newPersonalDevice(s, u, name, input.From, now)
	case "rename":
		d := findDevice(u, input.Device)
		if d == nil {
			return errors.New("设备不存在")
		}
		for _, other := range u.Devices {
			if other.Name != d.Name && strings.EqualFold(deviceDisplayName(other), name) {
				return errors.New("设备名称已存在")
			}
		}
		// Labels do not change stable device/node keys, subscriptions or bindings.
		d.Label = name
		return nil
	case "delete":
		d := findDevice(u, input.Device)
		if d == nil {
			return errors.New("设备不存在")
		}
		if len(u.Devices) <= 1 {
			return errors.New("请至少保留一台设备；可以重命名现有设备，或联系管理员更换配置")
		}
		usable := false
		for _, other := range u.Devices {
			if other.Name != d.Name && subscriptionDeviceAvailable(*u, other, now) == nil && len(nodesForDevice(*u, other.Name)) > 0 {
				usable = true
			}
		}
		if !usable {
			return errors.New("请至少保留一台可用且已有线路的设备，作为新增设备的配置来源")
		}
		name := d.Name
		u.Devices = slices.DeleteFunc(u.Devices, func(d Device) bool { return d.Name == name })
		u.Nodes = slices.DeleteFunc(u.Nodes, func(n Node) bool { return n.Device == name })
		// User accounting/history are deliberately retained after device deletion.
		return nil
	default:
		return errors.New("不支持的设备操作")
	}
}
