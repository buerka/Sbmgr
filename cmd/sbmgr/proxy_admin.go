package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const maxProxyCredentialFileBytes = 64 << 10

// proxyAdminCmd is deliberately reachable only below the hidden `admin`
// namespace. It provides a stable automation surface without adding protocol
// specific switches to the ordinary sbmgr help or exposing raw configuration.
func (a *app) proxyAdminCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("缺少 proxy 维护操作")
	}
	switch args[0] {
	case "list":
		return a.proxyAdminList(args[1:])
	case "add", "replace", "delete":
		return a.proxyAdminMutation(args[0], args[1:])
	case "credentials":
		return a.proxyAdminCredentials(args[1:])
	default:
		return fmt.Errorf("未知 proxy 维护操作 %q", args[0])
	}
}

func (a *app) proxyAdminList(args []string) error {
	fs := a.newFlagSet("proxy list")
	kindValue := fs.String("kind", "", "仅列出 outbound 或 endpoint")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("proxy list 最多接受一个对象类型")
	}
	if fs.NArg() == 1 {
		if *kindValue != "" {
			return errors.New("对象类型不能同时使用位置参数和 --kind")
		}
		*kindValue = fs.Arg(0)
	}

	kinds := []ManagedProxyKind{ManagedProxyOutbound, ManagedProxyEndpoint}
	if *kindValue != "" {
		kind, err := parseProxyAdminKind(*kindValue)
		if err != nil {
			return err
		}
		kinds = []ManagedProxyKind{kind}
	}

	var documents []ManagedProxyDocument
	if err := a.withStateLock(func() error {
		s, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		for _, kind := range kinds {
			items, listErr := listManagedProxyDocuments(s, kind)
			if listErr != nil {
				return listErr
			}
			documents = append(documents, items...)
		}
		return nil
	}); err != nil {
		return err
	}
	sort.SliceStable(documents, func(i, j int) bool {
		if documents[i].Kind != documents[j].Kind {
			return documents[i].Kind == ManagedProxyOutbound
		}
		return strings.ToLower(documents[i].Tag) < strings.ToLower(documents[j].Tag)
	})
	for _, document := range documents {
		// Keep this intentionally machine-readable and restricted to the three
		// explicitly non-sensitive identity fields.
		fmt.Fprintf(a.out, "%s\t%s\t%s\n", document.Kind, document.Tag, document.Type)
	}
	return nil
}

func (a *app) proxyAdminMutation(operation string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("proxy %s 缺少对象类型", operation)
	}
	kind, err := parseProxyAdminKind(args[0])
	if err != nil {
		return err
	}
	remaining := args[1:]
	targetTag := ""
	if operation == "replace" {
		if len(remaining) == 0 || strings.HasPrefix(remaining[0], "-") {
			return errors.New("proxy replace 缺少现有 tag")
		}
		targetTag = remaining[0]
		remaining = remaining[1:]
	}

	fs := a.newFlagSet("proxy " + operation)
	filePath := fs.String("file", "", "包含单个 JSON 对象的文件")
	apply := fs.Bool("apply", false, "校验并立即应用配置")
	if err := fs.Parse(remaining); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("proxy %s 不接受额外参数", operation)
	}
	if strings.TrimSpace(*filePath) == "" {
		return fmt.Errorf("proxy %s 必须指定 --file", operation)
	}
	raw, err := readProxyAdminFile(*filePath, maxManagedProxyJSONBytes)
	if err != nil {
		return err
	}
	defer clear(raw)
	identity, err := validateManagedProxyJSON(kind, raw)
	if err != nil {
		return err
	}
	if operation == "replace" && targetTag != identity.Tag {
		return errors.New("替换文件中的 tag 必须与现有 tag 完全一致")
	}

	// sing-box may print protocol-specific diagnostics while validating. The
	// admin automation command suppresses that subprocess stream because an
	// arbitrary protocol diagnostic could quote a secret option. The returned
	// error still states whether validation/application failed.
	quiet := *a
	quiet.out = io.Discard
	var change ManagedProxyChange
	switch operation {
	case "add":
		change, err = quiet.addManagedProxyJSON(kind, raw, *apply)
	case "replace":
		change, err = quiet.replaceManagedProxyJSON(kind, targetTag, raw, *apply)
	case "delete":
		change, err = quiet.deleteManagedProxy(kind, identity.Tag, *apply)
	default:
		return fmt.Errorf("未知 proxy 变更操作 %q", operation)
	}
	if err != nil {
		return err
	}
	status := "未变更"
	if change.Changed {
		status = "已写入，尚未应用"
	}
	if change.Applied {
		status = "已应用"
	}
	fmt.Fprintf(a.out, "%s %s %s (%s): %s\n", operation, identity.Kind, identity.Tag, identity.Type, status)
	return nil
}

func (a *app) proxyAdminCredentials(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New("proxy credentials 缺少 outbound tag")
	}
	tag := args[0]
	fs := a.newFlagSet("proxy credentials")
	filePath := fs.String("file", "", "仅含 username/password 的 JSON 文件")
	apply := fs.Bool("apply", false, "校验并立即应用配置")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("proxy credentials 不接受额外参数")
	}
	if strings.TrimSpace(*filePath) == "" {
		return errors.New("proxy credentials 必须指定 --file")
	}
	raw, err := readProxyAdminFile(*filePath, maxProxyCredentialFileBytes)
	if err != nil {
		return err
	}
	defer clear(raw)
	credentials, err := decodeProxyAdminCredentials(raw)
	if err != nil {
		return err
	}

	quiet := *a
	quiet.out = io.Discard
	var change OutboundEndpointChange
	err = quiet.withStateLock(func() error {
		s, loadErr := loadState(quiet.statePath)
		if loadErr != nil {
			return loadErr
		}
		endpoints, listErr := listOutboundEndpoints(s)
		if listErr != nil {
			return listErr
		}
		var current *OutboundEndpointSummary
		for index := range endpoints {
			if endpoints[index].Tag == tag {
				current = &endpoints[index]
				break
			}
		}
		if current == nil {
			return fmt.Errorf("基础模板中不存在带 server/server_port 的 outbound %q", tag)
		}
		change, listErr = quiet.setOutboundEndpointWithCredentials(tag, current.Server, current.Port, credentials, *apply)
		return listErr
	})
	if err != nil {
		return redactProxyCredentialError(err, credentials)
	}
	status := "未变更"
	if change.Changed {
		status = "已写入，尚未应用"
	}
	if change.Applied {
		status = "已应用"
	}
	fmt.Fprintf(a.out, "credentials outbound %s: %s\n", tag, status)
	return nil
}

func parseProxyAdminKind(value string) (ManagedProxyKind, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "outbound", "outbounds":
		return ManagedProxyOutbound, nil
	case "endpoint", "endpoints":
		return ManagedProxyEndpoint, nil
	default:
		return "", fmt.Errorf("对象类型必须是 outbound 或 endpoint，不能是 %q", value)
	}
}

func readProxyAdminFile(path string, limit int64) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("文件路径不能为空")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开 JSON 文件: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("检查 JSON 文件: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("JSON 输入必须是普通文件")
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("JSON 文件不能超过 %d 字节", limit)
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("读取 JSON 文件: %w", err)
	}
	if int64(len(raw)) > limit {
		clear(raw)
		return nil, fmt.Errorf("JSON 文件不能超过 %d 字节", limit)
	}
	return raw, nil
}

func decodeProxyAdminCredentials(raw []byte) (OutboundCredentialUpdate, error) {
	var update OutboundCredentialUpdate
	if err := validateManagedProxyJSONStructure(raw); err != nil {
		return update, fmt.Errorf("凭据文件不是安全的 JSON 对象: %w", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return update, errors.New("凭据文件必须是 JSON 对象")
	}
	if len(object) == 0 {
		return update, errors.New("凭据文件必须至少包含 username 或 password")
	}
	for key, value := range object {
		if key != "username" && key != "password" {
			return update, fmt.Errorf("凭据文件包含未知字段 %q", key)
		}
		var decoded any
		if err := json.Unmarshal(value, &decoded); err != nil {
			return update, fmt.Errorf("凭据字段 %s 必须是字符串", key)
		}
		text, ok := decoded.(string)
		if !ok {
			return update, fmt.Errorf("凭据字段 %s 必须是字符串", key)
		}
		copyOfText := text
		switch key {
		case "username":
			update.Username = &copyOfText
		case "password":
			update.Password = &copyOfText
		}
	}
	return update, nil
}

func redactProxyCredentialError(err error, update OutboundCredentialUpdate) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, value := range []*string{update.Username, update.Password} {
		if value != nil && *value != "" {
			message = strings.ReplaceAll(message, *value, "[REDACTED]")
		}
	}
	return errors.New(message)
}
