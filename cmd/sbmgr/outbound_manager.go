package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ManagedProxyKind identifies either of the two top-level sing-box collections
// which may act as a route target. Endpoints were introduced separately from
// outbounds, but their tags share the same routing namespace in practice, so
// this manager deliberately validates tags across both collections.
type ManagedProxyKind string

const (
	ManagedProxyOutbound     ManagedProxyKind = "outbound"
	ManagedProxyEndpoint     ManagedProxyKind = "endpoint"
	maxManagedProxyJSONBytes                  = 1 << 20
	maxManagedProxyJSONDepth                  = 64
)

func (kind ManagedProxyKind) collectionName() (string, error) {
	switch kind {
	case ManagedProxyOutbound:
		return "outbounds", nil
	case ManagedProxyEndpoint:
		return "endpoints", nil
	default:
		return "", fmt.Errorf("未知的 sing-box 对象类型 %q", kind)
	}
}

func (kind ManagedProxyKind) displayName() string {
	if kind == ManagedProxyEndpoint {
		return "端点"
	}
	return "出站"
}

// ManagedProxyDocument is the privileged editor representation of a sing-box
// outbound or endpoint. RawJSON is excluded from ordinary JSON serialization
// and the String/GoString forms are redacted, reducing the chance that an
// accidental status or audit dump exposes credentials. Callers that explicitly
// implement a raw editor can still read the deep-copied RawJSON field.
type ManagedProxyDocument struct {
	Kind    ManagedProxyKind `json:"kind"`
	Tag     string           `json:"tag"`
	Type    string           `json:"type"`
	RawJSON json.RawMessage  `json:"-"`
}

type OutboundDocument = ManagedProxyDocument
type EndpointDocument = ManagedProxyDocument

func (document ManagedProxyDocument) String() string {
	return fmt.Sprintf("%s %q (%s; JSON 已隐藏)", document.Kind.displayName(), document.Tag, document.Type)
}

func (document ManagedProxyDocument) GoString() string { return document.String() }

// ManagedProxyIdentity and ManagedProxyChange are safe to surface in status
// text and audit metadata: neither contains the raw object or secret fields.
type ManagedProxyIdentity struct {
	Kind ManagedProxyKind `json:"kind"`
	Tag  string           `json:"tag"`
	Type string           `json:"type"`
}

type ManagedProxyChange struct {
	Operation  string               `json:"operation"`
	Before     ManagedProxyIdentity `json:"before,omitempty"`
	After      ManagedProxyIdentity `json:"after,omitempty"`
	BackupPath string               `json:"backup_path,omitempty"`
	Changed    bool                 `json:"changed"`
	Applied    bool                 `json:"applied"`
	candidate  [sha256.Size]byte
}

type OutboundConfigChange = ManagedProxyChange
type EndpointConfigChange = ManagedProxyChange

// ManagedProxyReference contains only a human-readable location. It never
// includes a raw JSON value, which keeps in-use errors safe for logs and audit.
type ManagedProxyReference struct {
	Path string `json:"path"`
}

type ManagedProxyInUseError struct {
	Kind       ManagedProxyKind
	Tag        string
	References []ManagedProxyReference
}

func (err *ManagedProxyInUseError) Error() string {
	paths := make([]string, 0, len(err.References))
	for _, reference := range err.References {
		paths = append(paths, reference.Path)
	}
	return fmt.Sprintf("%s %q 仍被引用，不能删除：%s", err.Kind.displayName(), err.Tag, strings.Join(paths, "、"))
}

// listManagedProxyDocuments returns independently owned raw objects. It first
// validates the complete outbound/endpoint tag namespace so an editor never
// operates on an ambiguous configuration.
func listManagedProxyDocuments(s *State, kind ManagedProxyKind) ([]ManagedProxyDocument, error) {
	if _, err := kind.collectionName(); err != nil {
		return nil, err
	}
	config, err := readManagedProxyConfig(s)
	if err != nil {
		return nil, err
	}
	if _, err := config.validatedDocuments(); err != nil {
		return nil, err
	}
	items := config.collections[kind]
	result := make([]ManagedProxyDocument, 0, len(items))
	for index, raw := range items {
		document, _, err := decodeManagedProxyDocument(kind, raw, index)
		if err != nil {
			return nil, err
		}
		result = append(result, document)
	}
	return result, nil
}

func listOutboundDocuments(s *State) ([]OutboundDocument, error) {
	return listManagedProxyDocuments(s, ManagedProxyOutbound)
}

func listEndpointDocuments(s *State) ([]EndpointDocument, error) {
	return listManagedProxyDocuments(s, ManagedProxyEndpoint)
}

func getManagedProxyDocument(s *State, kind ManagedProxyKind, tag string) (ManagedProxyDocument, error) {
	var zero ManagedProxyDocument
	if err := validateManagedProxyTag(tag); err != nil {
		return zero, err
	}
	documents, err := listManagedProxyDocuments(s, kind)
	if err != nil {
		return zero, err
	}
	for _, document := range documents {
		if document.Tag == tag {
			return document, nil
		}
	}
	return zero, fmt.Errorf("基础模板中不存在%s %q", kind.displayName(), tag)
}

func getOutboundDocument(s *State, tag string) (OutboundDocument, error) {
	return getManagedProxyDocument(s, ManagedProxyOutbound, tag)
}

func getEndpointDocument(s *State, tag string) (EndpointDocument, error) {
	return getManagedProxyDocument(s, ManagedProxyEndpoint, tag)
}

// validateManagedProxyJSON performs the editor's cheap, side-effect-free
// validation step. The authoritative sing-box validation still happens inside
// the apply transaction, against the fully rendered candidate configuration.
func validateManagedProxyJSON(kind ManagedProxyKind, rawJSON []byte) (ManagedProxyIdentity, error) {
	document, _, err := decodeManagedProxyDocument(kind, rawJSON, 0)
	if err != nil {
		return ManagedProxyIdentity{}, err
	}
	return managedProxyIdentity(document), nil
}

// App-facing wrappers serialize mutations with every other state/config
// operation. Raw JSON is intentionally never passed to the audit subsystem.
func (a *app) addOutboundJSON(rawJSON []byte, apply bool) (OutboundConfigChange, error) {
	return a.addManagedProxyJSON(ManagedProxyOutbound, rawJSON, apply)
}

func (a *app) addEndpointJSON(rawJSON []byte, apply bool) (EndpointConfigChange, error) {
	return a.addManagedProxyJSON(ManagedProxyEndpoint, rawJSON, apply)
}

func (a *app) addManagedProxyJSON(kind ManagedProxyKind, rawJSON []byte, apply bool) (ManagedProxyChange, error) {
	var change ManagedProxyChange
	document, _, err := decodeManagedProxyDocument(kind, rawJSON, 0)
	if err != nil {
		return change, err
	}
	args := []string{"--tag", document.Tag, "--type", document.Type}
	err = a.withAuditedStateLock("proxy."+string(kind)+".add", args, func() error {
		s, loadErr := loadState(a.statePath)
		if loadErr != nil {
			return loadErr
		}
		change, loadErr = addManagedProxyOnState(s, kind, rawJSON, apply, a.out, applyState, time.Now())
		return loadErr
	})
	return change, err
}

func (a *app) replaceOutboundJSON(tag string, rawJSON []byte, apply bool) (OutboundConfigChange, error) {
	return a.replaceManagedProxyJSON(ManagedProxyOutbound, tag, rawJSON, apply)
}

func (a *app) replaceEndpointJSON(tag string, rawJSON []byte, apply bool) (EndpointConfigChange, error) {
	return a.replaceManagedProxyJSON(ManagedProxyEndpoint, tag, rawJSON, apply)
}

func (a *app) replaceManagedProxyJSON(kind ManagedProxyKind, tag string, rawJSON []byte, apply bool) (ManagedProxyChange, error) {
	var change ManagedProxyChange
	document, _, err := decodeManagedProxyDocument(kind, rawJSON, 0)
	if err != nil {
		return change, err
	}
	args := []string{"--tag", tag, "--type", document.Type}
	err = a.withAuditedStateLock("proxy."+string(kind)+".replace", args, func() error {
		s, loadErr := loadState(a.statePath)
		if loadErr != nil {
			return loadErr
		}
		change, loadErr = replaceManagedProxyOnState(s, kind, tag, rawJSON, apply, a.out, applyState, time.Now())
		return loadErr
	})
	return change, err
}

// Delete deliberately has no force parameter. The manager will only remove an
// unreferenced object; callers must first reassign every user/config reference.
func (a *app) deleteOutbound(tag string, apply bool) (OutboundConfigChange, error) {
	return a.deleteManagedProxy(ManagedProxyOutbound, tag, apply)
}

func (a *app) deleteEndpoint(tag string, apply bool) (EndpointConfigChange, error) {
	return a.deleteManagedProxy(ManagedProxyEndpoint, tag, apply)
}

func (a *app) deleteManagedProxy(kind ManagedProxyKind, tag string, apply bool) (ManagedProxyChange, error) {
	var change ManagedProxyChange
	args := []string{"--tag", tag}
	err := a.withAuditedStateLock("proxy."+string(kind)+".delete", args, func() error {
		s, loadErr := loadState(a.statePath)
		if loadErr != nil {
			return loadErr
		}
		change, loadErr = deleteManagedProxyOnState(s, kind, tag, apply, a.out, applyState, time.Now())
		return loadErr
	})
	return change, err
}

func addOutboundOnState(s *State, rawJSON []byte, apply bool, output io.Writer, applyFn outboundEndpointApplyFunc, now time.Time) (OutboundConfigChange, error) {
	return addManagedProxyOnState(s, ManagedProxyOutbound, rawJSON, apply, output, applyFn, now)
}

func addEndpointOnState(s *State, rawJSON []byte, apply bool, output io.Writer, applyFn outboundEndpointApplyFunc, now time.Time) (EndpointConfigChange, error) {
	return addManagedProxyOnState(s, ManagedProxyEndpoint, rawJSON, apply, output, applyFn, now)
}

func replaceOutboundOnState(s *State, tag string, rawJSON []byte, apply bool, output io.Writer, applyFn outboundEndpointApplyFunc, now time.Time) (OutboundConfigChange, error) {
	return replaceManagedProxyOnState(s, ManagedProxyOutbound, tag, rawJSON, apply, output, applyFn, now)
}

func replaceEndpointOnState(s *State, tag string, rawJSON []byte, apply bool, output io.Writer, applyFn outboundEndpointApplyFunc, now time.Time) (EndpointConfigChange, error) {
	return replaceManagedProxyOnState(s, ManagedProxyEndpoint, tag, rawJSON, apply, output, applyFn, now)
}

func deleteOutboundOnState(s *State, tag string, apply bool, output io.Writer, applyFn outboundEndpointApplyFunc, now time.Time) (OutboundConfigChange, error) {
	return deleteManagedProxyOnState(s, ManagedProxyOutbound, tag, apply, output, applyFn, now)
}

func deleteEndpointOnState(s *State, tag string, apply bool, output io.Writer, applyFn outboundEndpointApplyFunc, now time.Time) (EndpointConfigChange, error) {
	return deleteManagedProxyOnState(s, ManagedProxyEndpoint, tag, apply, output, applyFn, now)
}

func addManagedProxyOnState(s *State, kind ManagedProxyKind, rawJSON []byte, apply bool, output io.Writer, applyFn outboundEndpointApplyFunc, now time.Time) (ManagedProxyChange, error) {
	change, err := writeManagedProxyBase(s, kind, "add", "", rawJSON, now)
	return applyManagedProxyChange(s, change, apply, output, applyFn, err)
}

func replaceManagedProxyOnState(s *State, kind ManagedProxyKind, tag string, rawJSON []byte, apply bool, output io.Writer, applyFn outboundEndpointApplyFunc, now time.Time) (ManagedProxyChange, error) {
	change, err := writeManagedProxyBase(s, kind, "replace", tag, rawJSON, now)
	return applyManagedProxyChange(s, change, apply, output, applyFn, err)
}

func deleteManagedProxyOnState(s *State, kind ManagedProxyKind, tag string, apply bool, output io.Writer, applyFn outboundEndpointApplyFunc, now time.Time) (ManagedProxyChange, error) {
	change, err := writeManagedProxyBase(s, kind, "delete", tag, nil, now)
	return applyManagedProxyChange(s, change, apply, output, applyFn, err)
}

func applyManagedProxyChange(s *State, change ManagedProxyChange, apply bool, output io.Writer, applyFn outboundEndpointApplyFunc, mutationErr error) (ManagedProxyChange, error) {
	if mutationErr != nil || !change.Changed || !apply {
		return change, mutationErr
	}
	if applyFn == nil {
		if restoreErr := restoreOutboundEndpointBackup(s.BaseConfig, change.BackupPath, change.candidate); restoreErr != nil {
			return change, errors.Join(errors.New("缺少 sing-box 配置应用程序"), fmt.Errorf("恢复基础模板失败: %w", restoreErr))
		}
		return change, errors.New("缺少 sing-box 配置应用程序；基础模板已恢复")
	}
	if output == nil {
		output = io.Discard
	}
	// An arbitrary protocol object may change listeners, transports or internal
	// chains. A restart is more conservative than assuming every version can
	// hot-reload every possible field.
	if err := applyFn(s, false, true, output); err != nil {
		displayKind := change.After.Kind
		if displayKind == "" {
			displayKind = change.Before.Kind
		}
		if restoreErr := restoreOutboundEndpointBackup(s.BaseConfig, change.BackupPath, change.candidate); restoreErr != nil {
			return change, errors.Join(fmt.Errorf("应用%s配置失败: %w", displayKind.displayName(), err), fmt.Errorf("恢复基础模板失败: %w", restoreErr))
		}
		return change, fmt.Errorf("应用%s配置失败，基础模板已恢复: %w", displayKind.displayName(), err)
	}
	change.Applied = true
	return change, nil
}

type managedProxyConfig struct {
	original    []byte
	root        map[string]json.RawMessage
	collections map[ManagedProxyKind][]json.RawMessage
}

func readManagedProxyConfig(s *State) (*managedProxyConfig, error) {
	if s == nil {
		return nil, errors.New("状态不能为空")
	}
	base, err := readOutboundBaseConfig(s.BaseConfig)
	if err != nil {
		return nil, err
	}
	config := &managedProxyConfig{
		original: append([]byte(nil), base.original...),
		root:     base.root,
		collections: map[ManagedProxyKind][]json.RawMessage{
			ManagedProxyOutbound: append([]json.RawMessage(nil), base.outbounds...),
			ManagedProxyEndpoint: {},
		},
	}
	if rawEndpoints, ok := config.root["endpoints"]; ok {
		var endpoints []json.RawMessage
		if err := json.Unmarshal(rawEndpoints, &endpoints); err != nil {
			return nil, fmt.Errorf("基础模板的 endpoints 必须是数组: %w", err)
		}
		config.collections[ManagedProxyEndpoint] = endpoints
	}
	return config, nil
}

func (config *managedProxyConfig) validatedDocuments() (map[string]ManagedProxyDocument, error) {
	result := make(map[string]ManagedProxyDocument)
	for _, kind := range []ManagedProxyKind{ManagedProxyOutbound, ManagedProxyEndpoint} {
		for index, raw := range config.collections[kind] {
			document, _, err := decodeManagedProxyDocument(kind, raw, index)
			if err != nil {
				return nil, err
			}
			if previous, exists := result[document.Tag]; exists {
				return nil, fmt.Errorf("%s %q 与%s tag 重复；outbounds/endpoints 的 tag 必须全局唯一", kind.displayName(), document.Tag, previous.Kind.displayName())
			}
			result[document.Tag] = document
		}
	}
	return result, nil
}

func decodeManagedProxyDocument(kind ManagedProxyKind, rawJSON []byte, index int) (ManagedProxyDocument, map[string]json.RawMessage, error) {
	var zero ManagedProxyDocument
	if _, err := kind.collectionName(); err != nil {
		return zero, nil, err
	}
	if err := validateManagedProxyJSONStructure(rawJSON); err != nil {
		return zero, nil, fmt.Errorf("%s #%d 不是安全的 JSON 对象: %w", kind.displayName(), index+1, err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(rawJSON, &object); err != nil || object == nil {
		if err == nil {
			err = errors.New("必须是 JSON 对象")
		}
		return zero, nil, fmt.Errorf("%s #%d 不是有效的 JSON 对象: %w", kind.displayName(), index+1, err)
	}
	tag, err := requiredJSONString(object, "tag")
	if err != nil {
		return zero, nil, fmt.Errorf("%s #%d: %w", kind.displayName(), index+1, err)
	}
	if err := validateManagedProxyTag(tag); err != nil {
		return zero, nil, fmt.Errorf("%s #%d: %w", kind.displayName(), index+1, err)
	}
	proxyType, err := requiredJSONString(object, "type")
	if err != nil {
		return zero, nil, fmt.Errorf("%s %q: %w", kind.displayName(), tag, err)
	}
	if err := validateManagedProxyType(proxyType); err != nil {
		return zero, nil, fmt.Errorf("%s %q: %w", kind.displayName(), tag, err)
	}
	return ManagedProxyDocument{Kind: kind, Tag: tag, Type: proxyType, RawJSON: append(json.RawMessage(nil), rawJSON...)}, object, nil
}

func validateManagedProxyTag(tag string) error {
	if err := validateManagedProxyIdentifier("tag", tag, 256); err != nil {
		return err
	}
	if strings.HasPrefix(strings.ToLower(tag), "sbmgr-rate-") {
		return errors.New("tag 不能使用 sbmgr-rate- 保留前缀")
	}
	return nil
}

func validateManagedProxyType(proxyType string) error {
	return validateManagedProxyIdentifier("type", proxyType, 128)
}

func validateManagedProxyIdentifier(label, value string, maxRunes int) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s 必须是非空且不带首尾空白的字符串", label)
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes {
		return fmt.Errorf("%s 必须是最多 %d 个字符的有效文本", label, maxRunes)
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return fmt.Errorf("%s 不能包含控制字符", label)
		}
	}
	return nil
}

// encoding/json accepts duplicate object keys and silently keeps the last
// value. That behavior is dangerous in a credential editor because the value a
// human reviews may not be the value sing-box receives. This token walk rejects
// duplicates, excessive nesting and unreasonably large single objects before
// normal decoding.
func validateManagedProxyJSONStructure(rawJSON []byte) error {
	if len(rawJSON) == 0 {
		return errors.New("JSON 不能为空")
	}
	if len(rawJSON) > maxManagedProxyJSONBytes {
		return fmt.Errorf("单个对象不能超过 %d MiB", maxManagedProxyJSONBytes>>20)
	}
	decoder := json.NewDecoder(bytes.NewReader(rawJSON))
	decoder.UseNumber()
	if err := validateManagedProxyJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON 对象后存在额外内容")
		}
		return fmt.Errorf("JSON 对象后存在无效内容: %w", err)
	}
	return nil
}

func validateManagedProxyJSONValue(decoder *json.Decoder, depth int) error {
	if depth > maxManagedProxyJSONDepth {
		return fmt.Errorf("JSON 嵌套不能超过 %d 层", maxManagedProxyJSONDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		keys := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON 对象键必须是字符串")
			}
			if keys[key] {
				return fmt.Errorf("JSON 对象包含重复字段 %q", key)
			}
			keys[key] = true
			if err := validateManagedProxyJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("JSON 对象没有正确结束")
		}
	case '[':
		for decoder.More() {
			if err := validateManagedProxyJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("JSON 数组没有正确结束")
		}
	default:
		return errors.New("JSON 结构包含无效分隔符")
	}
	return nil
}

func writeManagedProxyBase(s *State, kind ManagedProxyKind, operation, targetTag string, rawJSON []byte, now time.Time) (ManagedProxyChange, error) {
	change := ManagedProxyChange{Operation: operation}
	if _, err := kind.collectionName(); err != nil {
		return change, err
	}
	config, err := readManagedProxyConfig(s)
	if err != nil {
		return change, err
	}
	all, err := config.validatedDocuments()
	if err != nil {
		return change, err
	}
	items := append([]json.RawMessage(nil), config.collections[kind]...)
	index := -1
	var before ManagedProxyDocument
	if targetTag != "" {
		if err := validateManagedProxyTag(targetTag); err != nil {
			return change, err
		}
		for itemIndex, raw := range items {
			document, _, decodeErr := decodeManagedProxyDocument(kind, raw, itemIndex)
			if decodeErr != nil {
				return change, decodeErr
			}
			if document.Tag == targetTag {
				index, before = itemIndex, document
				break
			}
		}
		if index < 0 {
			return change, fmt.Errorf("基础模板中不存在%s %q", kind.displayName(), targetTag)
		}
		change.Before = managedProxyIdentity(before)
	}

	switch operation {
	case "add":
		after, object, decodeErr := decodeManagedProxyDocument(kind, rawJSON, len(items))
		if decodeErr != nil {
			return change, decodeErr
		}
		if previous, exists := all[after.Tag]; exists {
			return change, fmt.Errorf("tag %q 已被%s占用", after.Tag, previous.Kind.displayName())
		}
		encoded, encodeErr := json.Marshal(object)
		if encodeErr != nil {
			return change, fmt.Errorf("编码%s %q: %w", kind.displayName(), after.Tag, encodeErr)
		}
		items = append(items, encoded)
		change.After = managedProxyIdentity(after)
	case "replace":
		after, object, decodeErr := decodeManagedProxyDocument(kind, rawJSON, index)
		if decodeErr != nil {
			return change, decodeErr
		}
		if after.Tag != targetTag {
			return change, fmt.Errorf("替换%s时不能把 tag 从 %q 改为 %q；请先迁移引用后新增并删除", kind.displayName(), targetTag, after.Tag)
		}
		encoded, encodeErr := json.Marshal(object)
		if encodeErr != nil {
			return change, fmt.Errorf("编码%s %q: %w", kind.displayName(), after.Tag, encodeErr)
		}
		change.After = managedProxyIdentity(after)
		if compactJSONEqual(items[index], encoded) {
			return change, nil
		}
		items[index] = encoded
	case "delete":
		references, referenceErr := findManagedProxyReferences(s, config, kind, targetTag, index)
		if referenceErr != nil {
			return change, referenceErr
		}
		if len(references) != 0 {
			return change, &ManagedProxyInUseError{Kind: kind, Tag: targetTag, References: references}
		}
		items = append(items[:index], items[index+1:]...)
	default:
		return change, fmt.Errorf("未知的配置操作 %q", operation)
	}

	config.collections[kind] = items
	collection, _ := kind.collectionName()
	encodedItems, err := json.Marshal(items)
	if err != nil {
		return change, fmt.Errorf("编码 %s: %w", collection, err)
	}
	config.root[collection] = encodedItems
	if _, err := config.validatedDocuments(); err != nil {
		return change, fmt.Errorf("更新后的基础模板无效: %w", err)
	}
	candidate, err := json.MarshalIndent(config.root, "", "  ")
	if err != nil {
		return change, fmt.Errorf("编码基础模板: %w", err)
	}
	candidate = append(candidate, '\n')
	if !json.Valid(candidate) {
		return change, errors.New("更新后的基础模板不是有效 JSON")
	}
	change.candidate = sha256.Sum256(candidate)
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

func managedProxyIdentity(document ManagedProxyDocument) ManagedProxyIdentity {
	return ManagedProxyIdentity{Kind: document.Kind, Tag: document.Tag, Type: document.Type}
}

func compactJSONEqual(left, right []byte) bool {
	var leftCompact, rightCompact bytes.Buffer
	if json.Compact(&leftCompact, left) != nil || json.Compact(&rightCompact, right) != nil {
		return false
	}
	return bytes.Equal(leftCompact.Bytes(), rightCompact.Bytes())
}

func findManagedProxyReferences(s *State, config *managedProxyConfig, targetKind ManagedProxyKind, tag string, targetIndex int) ([]ManagedProxyReference, error) {
	seen := map[string]bool{}
	add := func(path string) {
		if !seen[path] {
			seen[path] = true
		}
	}
	for userIndex, user := range s.Users {
		for nodeIndex, node := range user.Nodes {
			if node.Outbound == tag {
				add(fmt.Sprintf("state.users[%d].nodes[%d].outbound", userIndex, nodeIndex))
			}
		}
	}
	for _, kind := range []ManagedProxyKind{ManagedProxyOutbound, ManagedProxyEndpoint} {
		collection, _ := kind.collectionName()
		for index, raw := range config.collections[kind] {
			if kind == targetKind && index == targetIndex {
				continue
			}
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, fmt.Errorf("扫描 %s[%d] 引用: %w", collection, index, err)
			}
			scanManagedProxyReferenceValue(value, tag, collection+fmt.Sprintf("[%d]", index), add)
		}
	}
	for key, raw := range config.root {
		if key == "outbounds" || key == "endpoints" {
			continue
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("扫描基础模板字段 %s: %w", key, err)
		}
		scanManagedProxyReferenceValue(value, tag, key, add)
	}
	// route.final is intentionally handled explicitly: other objects also have
	// fields named final (notably DNS), and those refer to different namespaces.
	finalTag := ""
	if rawRoute, exists := config.root["route"]; exists {
		var route map[string]json.RawMessage
		if err := json.Unmarshal(rawRoute, &route); err == nil {
			if final, err := optionalJSONString(route, "final"); err != nil {
				return nil, fmt.Errorf("扫描 route.final: %w", err)
			} else if final == tag {
				add("route.final")
				finalTag = final
			} else {
				finalTag = final
			}
		}
	}
	// With no explicit route.final, sing-box and sbmgr both fall back to the
	// first outbound. Deleting it would silently redirect every default node to
	// the next object while still passing validation, so require an explicit
	// migration first.
	if targetKind == ManagedProxyOutbound && targetIndex == 0 && finalTag == "" {
		add("route.final（未设置，隐式使用第一个出站）")
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	references := make([]ManagedProxyReference, 0, len(paths))
	for _, path := range paths {
		references = append(references, ManagedProxyReference{Path: path})
	}
	return references, nil
}

func scanManagedProxyReferenceValue(value any, tag, path string, add func(string)) {
	switch typed := value.(type) {
	case map[string]any:
		proxyType, _ := typed["type"].(string)
		for key, child := range typed {
			childPath := path + "." + key
			switch key {
			case "outbound", "detour", "download_detour", "external_ui_download_detour":
				if text, ok := child.(string); ok && text == tag {
					add(childPath)
				}
			case "outbounds":
				if text, ok := child.(string); ok && text == tag {
					add(childPath)
				}
				if values, ok := child.([]any); ok {
					for index, item := range values {
						if text, ok := item.(string); ok && text == tag {
							add(fmt.Sprintf("%s[%d]", childPath, index))
						}
					}
				}
			case "preferred_by":
				// route.rules[].preferred_by selects an outbound/endpoint.
				// DNS rules use the same field name for DNS-server tags, so
				// matching it globally would reject unrelated deletions.
				if strings.HasPrefix(path, "route.rules[") {
					scanManagedProxyTagReference(child, tag, childPath, add)
				}
			case "endpoint":
				// Tailscale DNS servers (and newer certificate providers)
				// directly reference an endpoint tag.
				if strings.HasPrefix(path, "dns.servers[") || strings.HasPrefix(path, "certificate_providers[") {
					scanManagedProxyTagReference(child, tag, childPath, add)
				}
			case "default":
				if (proxyType == "selector" || proxyType == "urltest") && child == tag {
					add(childPath)
				}
			}
			scanManagedProxyReferenceValue(child, tag, childPath, add)
		}
	case []any:
		for index, child := range typed {
			scanManagedProxyReferenceValue(child, tag, fmt.Sprintf("%s[%d]", path, index), add)
		}
	}
}

func scanManagedProxyTagReference(value any, tag, path string, add func(string)) {
	if text, ok := value.(string); ok && text == tag {
		add(path)
	}
	if values, ok := value.([]any); ok {
		for index, item := range values {
			if text, ok := item.(string); ok && text == tag {
				add(fmt.Sprintf("%s[%d]", path, index))
			}
		}
	}
}
