package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

const maxMihomoTemplateSize = 2 * 1024 * 1024

func readMihomoTemplate(path string) ([]byte, *yaml.Node, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil, errors.New("尚未配置 Mihomo 导出模板")
	}
	if !filepath.IsAbs(path) {
		return nil, nil, errors.New("Mihomo 模板必须使用绝对路径")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("读取 Mihomo 模板: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("Mihomo 模板不是普通文件")
	}
	if info.Size() <= 0 || info.Size() > maxMihomoTemplateSize {
		return nil, nil, fmt.Errorf("Mihomo 模板大小必须在 1B–%s 之间", formatSize(maxMihomoTemplateSize))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("读取 Mihomo 模板: %w", err)
	}
	document, err := parseMihomoTemplate(data)
	if err != nil {
		return nil, nil, err
	}
	return data, document, nil
}

func parseMihomoTemplate(data []byte) (*yaml.Node, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("解析 Mihomo 模板: %w", err)
	}
	root, err := yamlRootMapping(&document)
	if err != nil {
		return nil, err
	}
	for _, field := range []string{"proxies", "proxy-groups", "rules"} {
		value, ok := yamlMapValue(root, field)
		if !ok || value.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("Mihomo 模板缺少列表字段 %q", field)
		}
		if len(value.Content) == 0 {
			return nil, fmt.Errorf("Mihomo 模板字段 %q 不能为空", field)
		}
	}
	return &document, nil
}

func renderMihomoFromTemplate(s *State, u User, device Device, nodes []Node) ([]byte, error) {
	_, document, err := readMihomoTemplate(s.Client.MihomoTemplate)
	if err != nil {
		return nil, err
	}
	root, _ := yamlRootMapping(document)
	templateProxies, _ := yamlMapValue(root, "proxies")
	groups, _ := yamlMapValue(root, "proxy-groups")
	templateByName, oldNames, skeleton, err := indexTemplateProxies(templateProxies)
	if err != nil {
		return nil, err
	}
	groupNames := yamlProxyGroupNames(groups)
	exportNames, nameByNode := chooseExportProxyNames(u, device, nodes, groupNames)
	newProxies := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: templateProxies.Style, HeadComment: templateProxies.HeadComment, LineComment: templateProxies.LineComment, FootComment: templateProxies.FootComment}
	for index, node := range nodes {
		proxyTemplate := templateByName[strings.ToLower(strings.TrimSpace(node.Name))]
		if proxyTemplate == nil {
			proxyTemplate = skeleton
		}
		proxy, err := buildMihomoProxy(proxyTemplate, exportNames[index], s.Client, node.UUID)
		if err != nil {
			return nil, fmt.Errorf("生成节点 %s: %w", node.Name, err)
		}
		newProxies.Content = append(newProxies.Content, proxy)
	}
	yamlSetMapValue(root, "proxies", newProxies)
	rewriteMihomoProxyGroups(groups, oldNames, nameByNode, exportNames)

	if document.HeadComment == "" {
		document.HeadComment = "由 sbmgr 按用户与设备授权生成；节点凭据会随重新导出更新"
	} else if !strings.Contains(document.HeadComment, "sbmgr") {
		document.HeadComment = "由 sbmgr 按用户与设备授权生成\n" + document.HeadComment
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	encoder.CompactSeqIndent()
	if err := encoder.Encode(document); err != nil {
		return nil, fmt.Errorf("编码 Mihomo 模板: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("结束编码 Mihomo 模板: %w", err)
	}
	if _, err := parseMihomoTemplate(output.Bytes()); err != nil {
		return nil, fmt.Errorf("生成的 Mihomo 配置无效: %w", err)
	}
	return output.Bytes(), nil
}

func indexTemplateProxies(sequence *yaml.Node) (map[string]*yaml.Node, map[string]string, *yaml.Node, error) {
	byName := map[string]*yaml.Node{}
	names := map[string]string{}
	var skeleton *yaml.Node
	for _, proxy := range sequence.Content {
		if proxy.Kind != yaml.MappingNode {
			return nil, nil, nil, errors.New("Mihomo 模板 proxies 包含非对象节点")
		}
		nameNode, ok := yamlMapValue(proxy, "name")
		if !ok || nameNode.Kind != yaml.ScalarNode || strings.TrimSpace(nameNode.Value) == "" {
			return nil, nil, nil, errors.New("Mihomo 模板节点缺少 name")
		}
		name := strings.TrimSpace(nameNode.Value)
		key := strings.ToLower(name)
		if _, exists := byName[key]; exists {
			return nil, nil, nil, fmt.Errorf("Mihomo 模板包含重复节点名 %q", name)
		}
		byName[key] = proxy
		names[key] = name
		if skeleton == nil {
			skeleton = proxy
		}
		if typeNode, ok := yamlMapValue(proxy, "type"); ok && strings.EqualFold(typeNode.Value, "vless") {
			skeleton = proxy
		}
	}
	if skeleton == nil {
		return nil, nil, nil, errors.New("Mihomo 模板没有可复用的节点定义")
	}
	return byName, names, skeleton, nil
}

func chooseExportProxyNames(u User, device Device, nodes []Node, groupNames map[string]bool) ([]string, map[string]string) {
	used := map[string]bool{"direct": true, "reject": true, "pass": true, "compatible": true}
	for name := range groupNames {
		used[strings.ToLower(name)] = true
	}
	names := make([]string, 0, len(nodes))
	byNode := map[string]string{}
	for _, node := range nodes {
		name := strings.TrimSpace(node.Name)
		if name == "" || used[strings.ToLower(name)] {
			name = strings.ReplaceAll(deviceNodeLabel(u.Name, device.Name, node.Name), "/", "-")
		}
		base := name
		for suffix := 2; used[strings.ToLower(name)]; suffix++ {
			name = fmt.Sprintf("%s-%d", base, suffix)
		}
		used[strings.ToLower(name)] = true
		names = append(names, name)
		byNode[strings.ToLower(strings.TrimSpace(node.Name))] = name
	}
	return names, byNode
}

func buildMihomoProxy(template *yaml.Node, name string, client ClientSettings, uuid string) (*yaml.Node, error) {
	proxy := cloneYAMLNode(template)
	if proxy == nil || proxy.Kind != yaml.MappingNode {
		return nil, errors.New("节点模板不是对象")
	}
	for key, value := range map[string]any{
		"name": name, "type": "vless", "server": client.Server, "port": client.Port, "uuid": uuid,
		"network": "tcp", "tls": true, "udp": true, "servername": client.ServerName,
		"client-fingerprint": "chrome", "packet-encoding": "xudp",
	} {
		yamlSetMapScalar(proxy, key, value)
	}
	reality, ok := yamlMapValue(proxy, "reality-opts")
	if !ok || reality.Kind != yaml.MappingNode {
		reality = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		yamlSetMapValue(proxy, "reality-opts", reality)
	}
	yamlSetMapScalar(reality, "public-key", client.PublicKey)
	yamlSetMapScalar(reality, "short-id", client.ShortID)
	return proxy, nil
}

func rewriteMihomoProxyGroups(groups *yaml.Node, oldNames map[string]string, nameByNode map[string]string, newNames []string) {
	groupNames := yamlProxyGroupNames(groups)
	primary := ""
	for name := range groupNames {
		if primary == "" {
			primary = name
		}
		if name == "节点选择" {
			primary = name
			break
		}
	}
	for _, group := range groups.Content {
		if group.Kind != yaml.MappingNode {
			continue
		}
		name := yamlStringMapValue(group, "name")
		groupType := strings.ToLower(yamlStringMapValue(group, "type"))
		proxies, ok := yamlMapValue(group, "proxies")
		if !ok || proxies.Kind != yaml.SequenceNode {
			continue
		}
		oldRefs := map[string]bool{}
		for _, item := range proxies.Content {
			if item.Kind == yaml.ScalarNode {
				key := strings.ToLower(strings.TrimSpace(item.Value))
				if _, exists := oldNames[key]; exists {
					oldRefs[key] = true
				}
			}
		}
		replaceAll := len(oldNames) > 0 && len(oldRefs) == len(oldNames)
		result := []string{}
		inserted := false
		for _, item := range proxies.Content {
			if item.Kind != yaml.ScalarNode {
				continue
			}
			value := strings.TrimSpace(item.Value)
			key := strings.ToLower(value)
			if _, isOld := oldNames[key]; isOld {
				if replaceAll {
					if !inserted {
						result = append(result, newNames...)
						inserted = true
					}
				} else if replacement := nameByNode[key]; replacement != "" {
					result = append(result, replacement)
				}
				continue
			}
			result = append(result, value)
		}
		result = uniqueStringsFold(result)
		if len(result) == 0 {
			if groupType == "select" && primary != "" && name != primary {
				result = []string{primary}
			} else {
				result = append(result, newNames...)
			}
		}
		proxies.Content = proxies.Content[:0]
		for _, value := range result {
			proxies.Content = append(proxies.Content, yamlScalarNode(value))
		}
	}
}

func yamlProxyGroupNames(groups *yaml.Node) map[string]bool {
	names := map[string]bool{}
	if groups == nil || groups.Kind != yaml.SequenceNode {
		return names
	}
	for _, group := range groups.Content {
		if name := yamlStringMapValue(group, "name"); name != "" {
			names[name] = true
		}
	}
	return names
}

func yamlRootMapping(document *yaml.Node) (*yaml.Node, error) {
	if document == nil || document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("Mihomo 模板顶层必须是单个 YAML 对象")
	}
	return document.Content[0], nil
}

func yamlMapValue(mapping *yaml.Node, key string) (*yaml.Node, bool) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, false
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1], true
		}
	}
	return nil, false
}

func yamlStringMapValue(mapping *yaml.Node, key string) string {
	value, ok := yamlMapValue(mapping, key)
	if !ok || value.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(value.Value)
}

func yamlSetMapValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			old := mapping.Content[index+1]
			if value.HeadComment == "" {
				value.HeadComment = old.HeadComment
			}
			mapping.Content[index+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content, yamlScalarNode(key), value)
}

func yamlSetMapScalar(mapping *yaml.Node, key string, value any) {
	var encoded yaml.Node
	if err := encoded.Encode(value); err != nil {
		panic(err)
	}
	if encoded.Kind == yaml.DocumentNode && len(encoded.Content) == 1 {
		encoded = *encoded.Content[0]
	}
	yamlSetMapValue(mapping, key, &encoded)
}

func yamlScalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	cloned := *node
	cloned.Content = make([]*yaml.Node, len(node.Content))
	for index, child := range node.Content {
		cloned.Content[index] = cloneYAMLNode(child)
	}
	return &cloned
}

func uniqueStringsFold(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
	}
	return result
}

func (a *app) templateCmd(args []string) error {
	return a.withAuditedStateLock(auditAction("template", args), args, func() error { return a.templateCmdLocked(args) })
}

func (a *app) templateCmdLocked(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: sbmgr admin template status|check|set")
	}
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	switch args[0] {
	case "status":
		fmt.Fprintf(a.out, "mihomo_template=%s\n", dash(s.Client.MihomoTemplate))
		return nil
	case "check":
		_, _, err := readMihomoTemplate(s.Client.MihomoTemplate)
		if err != nil {
			return err
		}
		fmt.Fprintln(a.out, "Mihomo 模板校验通过")
		return nil
	case "set":
		fs := a.newFlagSet("template set")
		path := fs.String("path", "", "Mihomo YAML 模板绝对路径；留空恢复简易导出")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		pathSet := false
		fs.Visit(func(current *flag.Flag) { pathSet = pathSet || current.Name == "path" })
		if !pathSet || fs.NArg() != 0 {
			return errors.New("用法: sbmgr admin template set --path /root/sbmgr/mihomo.template.yaml")
		}
		cleaned := strings.TrimSpace(*path)
		if cleaned != "" {
			if !filepath.IsAbs(cleaned) {
				return errors.New("Mihomo 模板必须使用绝对路径")
			}
			cleaned = filepath.Clean(cleaned)
			if _, _, err := readMihomoTemplate(cleaned); err != nil {
				return err
			}
		}
		s.Client.MihomoTemplate = cleaned
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		if cleaned == "" {
			fmt.Fprintln(a.out, "已关闭 Mihomo 模板导出，恢复简易配置")
		} else {
			fmt.Fprintf(a.out, "已启用 Mihomo 模板导出: %s\n", cleaned)
		}
		return nil
	default:
		return fmt.Errorf("未知 template 子命令 %q", args[0])
	}
}
