package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const outboundEndpointBackupLimit = 20

// OutboundEndpointSummary is the non-sensitive portion of a remotely managed
// outbound. Passwords, keys and protocol-specific options deliberately never
// leave the base configuration parser.
type OutboundEndpointSummary struct {
	Tag       string
	Type      string
	Server    string
	Port      int
	UserCount int
	NodeCount int
}

// OutboundEndpointChange describes a durable base-config update. BackupPath is
// empty for a no-op, and Applied reports whether the generated sing-box config
// was successfully applied as part of the same transaction.
type OutboundEndpointChange struct {
	Before          OutboundEndpointSummary
	After           OutboundEndpointSummary
	BackupPath      string
	Changed         bool
	Applied         bool
	UsernameChanged bool
	PasswordChanged bool
	candidateHash   [sha256.Size]byte
}

// OutboundCredentialUpdate is deliberately tri-state: nil keeps the current
// value, a pointer to an empty string removes the field, and any other value
// replaces it. Password values must never be included in status or audit text.
type OutboundCredentialUpdate struct {
	Username *string
	Password *string
}

type outboundEndpointApplyFunc func(*State, bool, bool, io.Writer) error

// listOutboundEndpoints reads the remote outbounds that can safely be edited
// through the endpoint UI. An outbound is manageable only when it has a unique
// tag and a valid server/server_port pair.
func listOutboundEndpoints(s *State) ([]OutboundEndpointSummary, error) {
	if s == nil {
		return nil, errors.New("状态不能为空")
	}
	config, err := readOutboundBaseConfig(s.BaseConfig)
	if err != nil {
		return nil, err
	}
	endpoints, err := config.endpoints()
	if err != nil {
		return nil, err
	}
	defaultTag := config.finalOutboundTag()
	nodeCounts := map[string]int{}
	userCounts := map[string]int{}
	for _, user := range s.Users {
		used := map[string]bool{}
		for _, node := range user.Nodes {
			tag := node.Outbound
			if tag == "" {
				tag = defaultTag
			}
			if tag != "" {
				nodeCounts[tag]++
				used[tag] = true
			}
		}
		for tag := range used {
			userCounts[tag]++
		}
	}
	for index := range endpoints {
		endpoints[index].UserCount = userCounts[endpoints[index].Tag]
		endpoints[index].NodeCount = nodeCounts[endpoints[index].Tag]
	}
	sort.Slice(endpoints, func(i, j int) bool {
		return strings.ToLower(endpoints[i].Tag) < strings.ToLower(endpoints[j].Tag)
	})
	return endpoints, nil
}

// setOutboundEndpoint is the UI-facing transaction. It serializes against all
// state mutations, creates an audit record, backs up and atomically replaces
// config.base.json, and optionally validates/applies the generated config. If
// apply fails, the base configuration is restored before the error is returned.
func (a *app) setOutboundEndpoint(tag, server string, port int, apply bool) (OutboundEndpointChange, error) {
	return a.setOutboundEndpointWithCredentials(tag, server, port, OutboundCredentialUpdate{}, apply)
}

func (a *app) setOutboundEndpointWithCredentials(tag, server string, port int, credentials OutboundCredentialUpdate, apply bool) (OutboundEndpointChange, error) {
	var change OutboundEndpointChange
	args := []string{"--tag", tag, "--from", "-", "--to", net.JoinHostPort(server, strconv.Itoa(port))}
	if credentials.Username != nil {
		args = append(args, "--username-change", credentialChangeLabel(*credentials.Username))
	}
	if credentials.Password != nil {
		args = append(args, "--password-change", credentialChangeLabel(*credentials.Password))
	}
	err := a.withAuditedStateLock("outbound.endpoint.update", args, func() error {
		s, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		change, err = setOutboundEndpointOnStateWithCredentials(s, tag, server, port, credentials, apply, a.out, applyState, time.Now())
		if change.Before.Server != "" && change.Before.Port > 0 {
			args[3] = net.JoinHostPort(change.Before.Server, strconv.Itoa(change.Before.Port))
		}
		return err
	})
	return change, err
}

func setOutboundEndpointOnState(s *State, tag, server string, port int, apply bool, output io.Writer, applyFn outboundEndpointApplyFunc, now time.Time) (OutboundEndpointChange, error) {
	return setOutboundEndpointOnStateWithCredentials(s, tag, server, port, OutboundCredentialUpdate{}, apply, output, applyFn, now)
}

func setOutboundEndpointOnStateWithCredentials(s *State, tag, server string, port int, credentials OutboundCredentialUpdate, apply bool, output io.Writer, applyFn outboundEndpointApplyFunc, now time.Time) (OutboundEndpointChange, error) {
	change, err := writeOutboundEndpointBaseWithCredentials(s, tag, server, port, credentials, now)
	if err != nil || !change.Changed || !apply {
		return change, err
	}
	if applyFn == nil {
		if restoreErr := restoreOutboundEndpointBackup(s.BaseConfig, change.BackupPath, change.candidateHash); restoreErr != nil {
			return change, errors.Join(errors.New("缺少出口配置应用程序"), fmt.Errorf("恢复基础模板失败: %w", restoreErr))
		}
		return change, errors.New("缺少出口配置应用程序；基础模板已恢复")
	}
	if output == nil {
		output = io.Discard
	}
	restart := change.UsernameChanged || change.PasswordChanged
	if err := applyFn(s, false, restart, output); err != nil {
		if restoreErr := restoreOutboundEndpointBackup(s.BaseConfig, change.BackupPath, change.candidateHash); restoreErr != nil {
			return change, errors.Join(fmt.Errorf("应用出口配置失败: %w", err), fmt.Errorf("恢复基础模板失败: %w", restoreErr))
		}
		return change, fmt.Errorf("应用出口配置失败，基础模板已恢复: %w", err)
	}
	change.Applied = true
	return change, nil
}

func writeOutboundEndpointBase(s *State, tag, server string, port int, now time.Time) (OutboundEndpointChange, error) {
	return writeOutboundEndpointBaseWithCredentials(s, tag, server, port, OutboundCredentialUpdate{}, now)
}

func writeOutboundEndpointBaseWithCredentials(s *State, tag, server string, port int, credentials OutboundCredentialUpdate, now time.Time) (OutboundEndpointChange, error) {
	var change OutboundEndpointChange
	if s == nil {
		return change, errors.New("状态不能为空")
	}
	if err := validateOutboundTag(tag); err != nil {
		return change, err
	}
	if err := validateOutboundServer(server); err != nil {
		return change, err
	}
	if err := validateOutboundPort(port); err != nil {
		return change, err
	}
	config, err := readOutboundBaseConfig(s.BaseConfig)
	if err != nil {
		return change, err
	}
	before, index, object, err := config.findEndpoint(tag)
	if err != nil {
		return change, err
	}
	change.Before = before
	change.After = before
	change.After.Server = server
	change.After.Port = port
	if err := validateOutboundCredentialUpdate(before.Type, credentials); err != nil {
		return change, err
	}
	usernameChanged, err := updateOptionalOutboundString(object, "username", credentials.Username)
	if err != nil {
		return change, fmt.Errorf("更新出口 %q 的用户名: %w", tag, err)
	}
	passwordChanged, err := updateOptionalOutboundString(object, "password", credentials.Password)
	if err != nil {
		return change, fmt.Errorf("更新出口 %q 的密码: %w", tag, err)
	}
	change.UsernameChanged = usernameChanged
	change.PasswordChanged = passwordChanged
	if before.Server == server && before.Port == port && !usernameChanged && !passwordChanged {
		return change, nil
	}
	serverJSON, _ := json.Marshal(server)
	portJSON, _ := json.Marshal(port)
	object["server"] = serverJSON
	object["server_port"] = portJSON
	updatedOutbound, err := json.Marshal(object)
	if err != nil {
		return change, fmt.Errorf("编码出口 %q: %w", tag, err)
	}
	config.outbounds[index] = updatedOutbound
	updatedOutbounds, err := json.Marshal(config.outbounds)
	if err != nil {
		return change, fmt.Errorf("编码出站列表: %w", err)
	}
	config.root["outbounds"] = updatedOutbounds
	candidate, err := json.MarshalIndent(config.root, "", "  ")
	if err != nil {
		return change, fmt.Errorf("编码基础模板: %w", err)
	}
	candidate = append(candidate, '\n')
	if !json.Valid(candidate) {
		return change, errors.New("更新后的基础模板不是有效 JSON")
	}
	change.candidateHash = sha256.Sum256(candidate)
	backup, err := createOutboundEndpointBackup(s.BaseConfig, config.original, now)
	if err != nil {
		return change, fmt.Errorf("备份基础模板: %w", err)
	}
	change.BackupPath = backup
	current, err := os.ReadFile(s.BaseConfig)
	if err != nil {
		return change, fmt.Errorf("写入前重新读取基础模板（原文件备份在 %s）: %w", backup, err)
	}
	if !bytes.Equal(current, config.original) {
		return change, fmt.Errorf("基础模板在编辑期间已被其他进程修改，已取消覆盖（原文件备份在 %s）", backup)
	}
	if err := atomicWrite(s.BaseConfig, candidate, 0600); err != nil {
		return change, fmt.Errorf("写入基础模板失败（原文件备份在 %s）: %w", backup, err)
	}
	change.Changed = true
	return change, nil
}

func credentialChangeLabel(value string) string {
	if value == "" {
		return "clear"
	}
	return "replace"
}

func outboundSupportsUsername(outboundType string) bool {
	switch strings.ToLower(strings.TrimSpace(outboundType)) {
	case "socks", "http":
		return true
	default:
		return false
	}
}

func outboundSupportsPassword(outboundType string) bool {
	switch strings.ToLower(strings.TrimSpace(outboundType)) {
	case "socks", "http", "shadowsocks":
		return true
	default:
		return false
	}
}

func validateOutboundCredentialUpdate(outboundType string, update OutboundCredentialUpdate) error {
	if update.Username != nil {
		if !outboundSupportsUsername(outboundType) {
			return fmt.Errorf("%s 出站不支持在此编辑用户名", dash(outboundType))
		}
		if err := validateOutboundCredential("用户名", *update.Username, 1024); err != nil {
			return err
		}
	}
	if update.Password != nil {
		if !outboundSupportsPassword(outboundType) {
			return fmt.Errorf("%s 出站不支持在此编辑密码", dash(outboundType))
		}
		if strings.EqualFold(strings.TrimSpace(outboundType), "shadowsocks") && *update.Password == "" {
			return errors.New("Shadowsocks 密码不能清除")
		}
		if err := validateOutboundCredential("密码", *update.Password, 4096); err != nil {
			return err
		}
	}
	return nil
}

func validateOutboundCredential(label, value string, maxRunes int) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s必须是有效文本", label)
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return fmt.Errorf("%s不能超过 %d 个字符", label, maxRunes)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s不能包含控制字符", label)
		}
	}
	return nil
}

func updateOptionalOutboundString(object map[string]json.RawMessage, key string, update *string) (bool, error) {
	if update == nil {
		return false, nil
	}
	current, exists := object[key]
	if *update == "" {
		if !exists {
			return false, nil
		}
		delete(object, key)
		return true, nil
	}
	encoded, err := json.Marshal(*update)
	if err != nil {
		return false, err
	}
	if exists {
		var currentValue string
		if err := json.Unmarshal(current, &currentValue); err != nil {
			return false, fmt.Errorf("现有 %s 必须是字符串", key)
		}
		if currentValue == *update {
			return false, nil
		}
	}
	object[key] = encoded
	return true, nil
}

// outboundEndpointUsername returns only the non-secret account name needed by
// the editor. The password is never loaded into the TUI model.
func outboundEndpointUsername(s *State, tag string) (string, error) {
	if s == nil {
		return "", errors.New("状态不能为空")
	}
	config, err := readOutboundBaseConfig(s.BaseConfig)
	if err != nil {
		return "", err
	}
	endpoint, _, object, err := config.findEndpoint(tag)
	if err != nil {
		return "", err
	}
	if !outboundSupportsUsername(endpoint.Type) {
		return "", nil
	}
	username, err := optionalJSONString(object, "username")
	if err != nil {
		return "", fmt.Errorf("出站 %q: %w", tag, err)
	}
	return username, nil
}

type outboundBaseConfig struct {
	original  []byte
	root      map[string]json.RawMessage
	outbounds []json.RawMessage
}

func (config *outboundBaseConfig) finalOutboundTag() string {
	rawRoute, ok := config.root["route"]
	if !ok {
		return ""
	}
	var route map[string]json.RawMessage
	if json.Unmarshal(rawRoute, &route) != nil {
		return ""
	}
	final, _ := optionalJSONString(route, "final")
	return final
}

func readOutboundBaseConfig(path string) (*outboundBaseConfig, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("状态缺少基础模板路径")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取基础模板: %w", err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("解析基础模板: %w", err)
	}
	if root == nil {
		return nil, errors.New("基础模板必须是 JSON 对象")
	}
	rawOutbounds, ok := root["outbounds"]
	if !ok {
		return nil, errors.New("基础模板缺少 outbounds")
	}
	var outbounds []json.RawMessage
	if err := json.Unmarshal(rawOutbounds, &outbounds); err != nil {
		return nil, fmt.Errorf("基础模板的 outbounds 必须是数组: %w", err)
	}
	return &outboundBaseConfig{original: append([]byte(nil), raw...), root: root, outbounds: outbounds}, nil
}

func (config *outboundBaseConfig) endpoints() ([]OutboundEndpointSummary, error) {
	seen := map[string]bool{}
	result := make([]OutboundEndpointSummary, 0, len(config.outbounds))
	for index, raw := range config.outbounds {
		object, err := decodeOutboundObject(raw, index)
		if err != nil {
			return nil, err
		}
		tag, err := optionalJSONString(object, "tag")
		if err != nil {
			return nil, fmt.Errorf("出站 #%d: %w", index+1, err)
		}
		if tag != "" {
			if seen[tag] {
				return nil, fmt.Errorf("基础模板包含重复出站 tag %q", tag)
			}
			seen[tag] = true
		}
		endpoint, manageable, err := endpointFromObject(object)
		if err != nil {
			return nil, fmt.Errorf("出站 %q: %w", dash(tag), err)
		}
		if manageable {
			if err := validateOutboundTag(endpoint.Tag); err != nil {
				return nil, fmt.Errorf("出站 #%d: %w", index+1, err)
			}
			result = append(result, endpoint)
		}
	}
	return result, nil
}

func (config *outboundBaseConfig) findEndpoint(tag string) (OutboundEndpointSummary, int, map[string]json.RawMessage, error) {
	seen := map[string]bool{}
	var found OutboundEndpointSummary
	foundIndex := -1
	var foundObject map[string]json.RawMessage
	for index, raw := range config.outbounds {
		object, err := decodeOutboundObject(raw, index)
		if err != nil {
			return found, -1, nil, err
		}
		currentTag, err := optionalJSONString(object, "tag")
		if err != nil {
			return found, -1, nil, fmt.Errorf("出站 #%d: %w", index+1, err)
		}
		if currentTag != "" {
			if seen[currentTag] {
				return found, -1, nil, fmt.Errorf("基础模板包含重复出站 tag %q", currentTag)
			}
			seen[currentTag] = true
		}
		if currentTag != tag {
			continue
		}
		endpoint, manageable, err := endpointFromObject(object)
		if err != nil {
			return found, -1, nil, fmt.Errorf("出站 %q: %w", tag, err)
		}
		if !manageable {
			return found, -1, nil, fmt.Errorf("出站 %q 没有可管理的 server/server_port", tag)
		}
		found, foundIndex, foundObject = endpoint, index, object
	}
	if foundIndex < 0 {
		return found, -1, nil, fmt.Errorf("基础模板中不存在出站 %q", tag)
	}
	return found, foundIndex, foundObject, nil
}

func decodeOutboundObject(raw json.RawMessage, index int) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		if err == nil {
			err = errors.New("不是 JSON 对象")
		}
		return nil, fmt.Errorf("出站 #%d 必须是对象: %w", index+1, err)
	}
	return object, nil
}

func endpointFromObject(object map[string]json.RawMessage) (OutboundEndpointSummary, bool, error) {
	var endpoint OutboundEndpointSummary
	serverRaw, hasServer := object["server"]
	portRaw, hasPort := object["server_port"]
	if !hasServer || !hasPort {
		return endpoint, false, nil
	}
	var err error
	if endpoint.Tag, err = requiredJSONString(object, "tag"); err != nil {
		return endpoint, false, err
	}
	if endpoint.Type, err = optionalJSONString(object, "type"); err != nil {
		return endpoint, false, err
	}
	if err := json.Unmarshal(serverRaw, &endpoint.Server); err != nil {
		return endpoint, false, errors.New("server 必须是字符串")
	}
	port, err := decodeOutboundPort(portRaw)
	if err != nil {
		return endpoint, false, err
	}
	endpoint.Port = port
	if err := validateOutboundServer(endpoint.Server); err != nil {
		return endpoint, false, err
	}
	if err := validateOutboundPort(endpoint.Port); err != nil {
		return endpoint, false, err
	}
	return endpoint, true, nil
}

func requiredJSONString(object map[string]json.RawMessage, key string) (string, error) {
	value, ok := object[key]
	if !ok {
		return "", fmt.Errorf("缺少 %s", key)
	}
	var result string
	if err := json.Unmarshal(value, &result); err != nil || result == "" {
		return "", fmt.Errorf("%s 必须是非空字符串", key)
	}
	return result, nil
}

func optionalJSONString(object map[string]json.RawMessage, key string) (string, error) {
	value, ok := object[key]
	if !ok {
		return "", nil
	}
	var result string
	if err := json.Unmarshal(value, &result); err != nil {
		return "", fmt.Errorf("%s 必须是字符串", key)
	}
	return result, nil
}

func decodeOutboundPort(raw json.RawMessage) (int, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, errors.New("server_port 必须是整数")
	}
	var text string
	switch typed := value.(type) {
	case json.Number:
		text = typed.String()
	case string:
		text = typed
	default:
		return 0, errors.New("server_port 必须是整数")
	}
	if text == "" || strings.TrimSpace(text) != text || strings.ContainsAny(text, ".eE+-") {
		return 0, errors.New("server_port 必须是整数")
	}
	port, err := strconv.Atoi(text)
	if err != nil {
		return 0, errors.New("server_port 必须是整数")
	}
	return port, nil
}

func validateOutboundTag(tag string) error {
	if tag == "" || strings.TrimSpace(tag) != tag {
		return errors.New("出口 tag 不能为空或带首尾空白")
	}
	if !utf8.ValidString(tag) || utf8.RuneCountInString(tag) > 128 {
		return errors.New("出口 tag 必须是最多 128 个字符的有效文本")
	}
	for _, r := range tag {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_.:@+", r) {
			continue
		}
		return fmt.Errorf("出口 tag %q 包含不允许的字符 %q", tag, r)
	}
	return nil
}

func validateOutboundServer(server string) error {
	if server == "" || strings.TrimSpace(server) != server {
		return errors.New("出口地址不能为空或带首尾空白")
	}
	if len(server) > 253 {
		return errors.New("出口地址不能超过 253 个字符")
	}
	if net.ParseIP(server) != nil {
		return nil
	}
	if strings.Contains(server, ":") || strings.ContainsAny(server, "/\\[]%") {
		return errors.New("出口地址必须是纯 IP 或域名，不能包含协议、端口或路径")
	}
	host := strings.TrimSuffix(server, ".")
	if host == "" {
		return errors.New("出口域名无效")
	}
	allNumericDots := strings.Contains(host, ".")
	for _, r := range host {
		if (r < '0' || r > '9') && r != '.' {
			allNumericDots = false
			break
		}
	}
	if allNumericDots {
		return errors.New("出口 IPv4 地址无效")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("出口域名标签无效")
		}
		for _, char := range label {
			if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' {
				continue
			}
			return errors.New("出口域名只能包含字母、数字、点和连字符")
		}
	}
	return nil
}

func validateOutboundPort(port int) error {
	if port < 1 || port > 65535 {
		return errors.New("出口端口必须在 1–65535 之间")
	}
	return nil
}

func createOutboundEndpointBackup(basePath string, original []byte, now time.Time) (string, error) {
	directory := filepath.Join(filepath.Dir(basePath), "backups")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	prefix := "config.base-pre-endpoint-" + now.Format("20060102-150405.000000000")
	name := prefix + ".json"
	for suffix := 1; ; suffix++ {
		if _, err := os.Stat(filepath.Join(directory, name)); errors.Is(err, os.ErrNotExist) {
			break
		} else if err != nil {
			return "", err
		}
		name = fmt.Sprintf("%s-%d.json", prefix, suffix)
	}
	path := filepath.Join(directory, name)
	if err := atomicWrite(path, original, 0600); err != nil {
		return "", err
	}
	if err := pruneOutboundEndpointBackups(directory, outboundEndpointBackupLimit); err != nil {
		return "", fmt.Errorf("备份已创建但清理旧备份失败: %w", err)
	}
	return path, nil
}

func pruneOutboundEndpointBackups(directory string, keep int) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasPrefix(name, "config.base-pre-endpoint-") && strings.HasSuffix(name, ".json") {
			names = append(names, name)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if keep < 0 {
		keep = 0
	}
	for _, name := range names[min(keep, len(names)):] {
		if err := os.Remove(filepath.Join(directory, name)); err != nil {
			return err
		}
	}
	return nil
}

func restoreOutboundEndpointBackup(basePath, backupPath string, expectedCurrentHash [sha256.Size]byte) error {
	if strings.TrimSpace(backupPath) == "" {
		return errors.New("出口基础模板备份路径为空")
	}
	current, err := os.ReadFile(basePath)
	if err != nil {
		return fmt.Errorf("回滚前读取当前基础模板: %w", err)
	}
	if sha256.Sum256(current) != expectedCurrentHash {
		return errors.New("基础模板在应用期间已被其他进程修改，已拒绝用旧备份覆盖；请人工检查当前文件")
	}
	raw, err := os.ReadFile(backupPath)
	if err != nil {
		return err
	}
	if !json.Valid(raw) {
		return errors.New("出口基础模板备份不是有效 JSON")
	}
	return atomicWrite(basePath, raw, 0600)
}
