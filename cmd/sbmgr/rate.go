package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	rateMarkPrefix uint32 = 0x53420000
	rateMarkMask   uint32 = 0xffff0000
)

func rateLimited(u User) bool {
	return u.UploadMbps > 0 || u.DownloadMbps > 0
}

func nodeRateLimited(n Node) bool {
	return n.UploadMbps > 0 || n.DownloadMbps > 0
}

func userRateLimited(u User) bool {
	if rateLimited(u) {
		return true
	}
	for _, n := range u.Nodes {
		if nodeRateLimited(n) {
			return true
		}
	}
	return false
}

func baseNodeRate(u User, n Node) (upload, download float64, mark uint32) {
	if nodeRateLimited(n) {
		return n.UploadMbps, n.DownloadMbps, n.RateMark
	}
	// User-level fields are retained only to preserve states written before
	// per-node limits were introduced. New writes always use the node fields.
	if rateLimited(u) {
		mark := n.RateMark
		if !validRateMark(mark) {
			mark = u.RateMark
		}
		return u.UploadMbps, u.DownloadMbps, mark
	}
	return 0, 0, n.RateMark
}

func effectiveNodeRate(u User, n Node) (upload, download float64, mark uint32) {
	upload, download, mark = baseNodeRate(u, n)
	if burstSoftBlocked(u, time.Now()) {
		policy := normalizedBurst(u.Burst)
		return policy.SoftUploadKbps / 1000, policy.SoftDownloadKbps / 1000, mark
	}
	factor := throttleFactor(u)
	return upload * factor, download * factor, mark
}

func normalizedThrottle(policy ThrottlePolicy) ThrottlePolicy {
	if policy.Tier1Usage == 0 {
		policy.Tier1Usage = 80
	}
	if policy.Tier1Speed == 0 {
		policy.Tier1Speed = 50
	}
	if policy.Tier2Usage == 0 {
		policy.Tier2Usage = 95
	}
	if policy.Tier2Speed == 0 {
		policy.Tier2Speed = 20
	}
	return policy
}

func validateThrottle(policy ThrottlePolicy) error {
	if !policy.Enabled {
		return nil
	}
	policy = normalizedThrottle(policy)
	if policy.Tier1Usage <= 0 || policy.Tier1Usage >= policy.Tier2Usage || policy.Tier2Usage >= 100 {
		return errors.New("阶梯用量必须满足 0 < 第一档 < 第二档 < 100")
	}
	if policy.Tier1Speed <= 0 || policy.Tier1Speed > 100 || policy.Tier2Speed <= 0 || policy.Tier2Speed > policy.Tier1Speed {
		return errors.New("阶梯速度必须满足 0 < 第二档速度% <= 第一档速度% <= 100")
	}
	return nil
}

func throttleStage(u User) int {
	if !u.Throttle.Enabled || userQuota(u) <= 0 {
		return 0
	}
	policy := normalizedThrottle(u.Throttle)
	used := usagePercent(u)
	if used >= policy.Tier2Usage {
		return 2
	}
	if used >= policy.Tier1Usage {
		return 1
	}
	return 0
}

func throttleFactor(u User) float64 {
	policy := normalizedThrottle(u.Throttle)
	switch throttleStage(u) {
	case 1:
		return policy.Tier1Speed / 100
	case 2:
		return policy.Tier2Speed / 100
	default:
		return 1
	}
}

func hasRateLimits(s *State) bool {
	for _, u := range s.Users {
		if rateLimited(u) {
			return true
		}
		for _, n := range u.Nodes {
			if nodeRateLimited(n) {
				return true
			}
		}
	}
	return false
}

func hasManagedNodes(s *State) bool {
	for _, u := range s.Users {
		if len(u.Nodes) > 0 {
			return true
		}
	}
	return false
}

func validateMbps(values ...float64) error {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return fmt.Errorf("无效 Mbps 值 %v；必须是大于等于 0 的有限数值", value)
		}
		if value*1_000_000/8 > float64(^uint32(0)) {
			return fmt.Errorf("Mbps 值 %v 超过 nftables 单条规则上限", value)
		}
	}
	return nil
}

func formatMbps(value float64) string {
	if value == 0 {
		return "unlimited"
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func allocateRateMark(s *State) uint32 {
	used := make(map[uint32]bool, len(s.Users))
	for _, u := range s.Users {
		if validRateMark(u.RateMark) {
			used[u.RateMark] = true
		}
		for _, n := range u.Nodes {
			if validRateMark(n.RateMark) {
				used[n.RateMark] = true
			}
		}
	}
	for id := uint32(1); id <= 0xffff; id++ {
		mark := rateMarkPrefix | id
		if !used[mark] {
			return mark
		}
	}
	panic("sbmgr rate mark space exhausted")
}

func validRateMark(mark uint32) bool {
	return mark&rateMarkMask == rateMarkPrefix && mark&^rateMarkMask != 0
}

func validateRateMarks(s *State) error {
	seen := map[uint32]string{}
	for _, u := range s.Users {
		for _, n := range u.Nodes {
			up, down, mark := effectiveNodeRate(u, n)
			if err := validateMbps(up, down); err != nil {
				return fmt.Errorf("节点 %s/%s: %w", u.Name, n.Name, err)
			}
			owner := deviceNodeLabel(u.Name, n.Device, n.Name)
			// A zero mark is valid only for an in-memory draft. loadState,
			// saveState and every renderer allocate stable marks before this
			// validation; allowing zero here keeps atomic batch previews pure.
			if mark == 0 {
				continue
			}
			if !validRateMark(mark) {
				return fmt.Errorf("节点 %s/%s 的 rate_mark 0x%08x 无效；请重新保存该节点限速", u.Name, n.Name, mark)
			}
			if previous, ok := seen[mark]; ok && previous != owner {
				return fmt.Errorf("%s 与 %s 使用了重复的 rate_mark 0x%08x", owner, previous, mark)
			}
			seen[mark] = owner
		}
	}
	return nil
}

// rateTopologyChanged detects transitions that leave established connections
// on an unmarked or obsolete outbound. A service restart is required only for
// those transitions; changing Mbps alone keeps the stable mark and is immediate.
func rateTopologyChanged(s *State) bool {
	normalizeDeviceModel(s)
	desired := map[uint32]bool{}
	now := time.Now()
	for _, u := range s.Users {
		if !u.Enabled || expired(u, now) || overQuota(u) || burstHardBlocked(u, now) {
			continue
		}
		for _, n := range u.Nodes {
			if !deviceEnabled(u, n.Device) {
				continue
			}
			_, _, mark := effectiveNodeRate(u, n)
			if validRateMark(mark) {
				desired[mark] = true
			}
		}
	}
	raw, err := os.ReadFile(s.ConfigPath)
	if err != nil {
		return len(desired) > 0
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return len(desired) > 0
	}
	current := map[uint32]bool{}
	if outbounds, ok := cfg["outbounds"].([]any); ok {
		for _, item := range outbounds {
			m, _ := item.(map[string]any)
			value, _ := m["routing_mark"].(float64)
			mark := uint32(value)
			if validRateMark(mark) {
				current[mark] = true
			}
		}
	}
	if len(current) != len(desired) {
		return true
	}
	for mark := range desired {
		if !current[mark] {
			return true
		}
	}
	return false
}

func (a *app) rateCmd(args []string) error {
	return a.withStateLock(func() error { return a.rateCmdLocked(args) })
}

func (a *app) rateCmdLocked(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: sbmgr rate apply|check|show")
	}
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	switch args[0] {
	case "apply":
		fs := flag.NewFlagSet("rate apply", flag.ContinueOnError)
		fs.SetOutput(a.err)
		ifMissing := fs.Bool("if-missing", false, "仅在 inet sbmgr 表不存在时恢复规则")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("rate apply 不接受额外参数")
		}
		if *ifMissing {
			return applyRateLimitsIfMissing(s, a.out)
		}
		return applyRateLimits(s, a.out)
	case "check":
		fs := flag.NewFlagSet("rate check", flag.ContinueOnError)
		fs.SetOutput(a.err)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("rate check 不接受额外参数")
		}
		return checkRateLimits(s, a.out)
	case "show":
		if len(args) != 1 {
			return errors.New("rate show 不接受额外参数")
		}
		users := append([]User(nil), s.Users...)
		sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })
		fmt.Fprintln(a.out, "USER\tNODE\tUPLOAD_Mbps\tDOWNLOAD_Mbps\tMARK")
		for _, u := range users {
			for _, n := range u.Nodes {
				up, down, rateMark := effectiveNodeRate(u, n)
				mark := "-"
				if rateMark != 0 {
					mark = fmt.Sprintf("0x%08x", rateMark)
				}
				fmt.Fprintf(a.out, "%s\t%s\t%s\t%s\t%s\n", u.Name, n.Name, formatMbps(up), formatMbps(down), mark)
			}
		}
		return nil
	default:
		return fmt.Errorf("未知 rate 子命令 %q", args[0])
	}
}

// addRateOutbounds clones the selected outbound chain for every limited user.
// A clone prevents a multiplexed upstream connection from being shared by users
// with different marks. Empty node outbounds intentionally resolve to route.final.
func addRateOutbounds(cfg map[string]any, s *State, active []User) (map[string]string, error) {
	if err := validateRateMarks(s); err != nil {
		return nil, err
	}
	outbounds, _ := cfg["outbounds"].([]any)
	index := make(map[string]map[string]any, len(outbounds))
	usedTags := make(map[string]bool, len(outbounds))
	for _, item := range outbounds {
		m, _ := item.(map[string]any)
		tag := stringValue(m["tag"])
		if tag != "" {
			index[tag] = m
			usedTags[tag] = true
		}
	}
	defaultTag := ""
	if route, _ := cfg["route"].(map[string]any); route != nil {
		defaultTag = stringValue(route["final"])
	}
	if defaultTag == "" && len(outbounds) > 0 {
		first, _ := outbounds[0].(map[string]any)
		defaultTag = stringValue(first["tag"])
	}

	result := map[string]string{}
	cache := map[string]string{}
	visiting := map[string]bool{}
	var clone func(string, uint32) (string, error)
	clone = func(originalTag string, mark uint32) (string, error) {
		key := fmt.Sprintf("%08x:%s", mark, originalTag)
		if tag, ok := cache[key]; ok {
			return tag, nil
		}
		if visiting[key] {
			return "", fmt.Errorf("出站 %q 的 detour/outbounds 存在循环", originalTag)
		}
		original := index[originalTag]
		if original == nil {
			return "", fmt.Errorf("找不到需要限速的出站 %q", originalTag)
		}
		visiting[key] = true
		defer delete(visiting, key)

		copyBytes, _ := json.Marshal(original)
		var copied map[string]any
		if err := json.Unmarshal(copyBytes, &copied); err != nil {
			return "", err
		}
		baseTag := fmt.Sprintf("sbmgr-rate-%04x-%s", mark&0xffff, slug(originalTag))
		newTag := baseTag
		for suffix := 2; usedTags[newTag]; suffix++ {
			newTag = fmt.Sprintf("%s-%d", baseTag, suffix)
		}
		usedTags[newTag] = true
		copied["tag"] = newTag

		typeName := stringValue(copied["type"])
		switch typeName {
		case "selector", "urltest":
			children, _ := copied["outbounds"].([]any)
			clonedChildren := make([]any, 0, len(children))
			mapping := map[string]string{}
			for _, child := range children {
				childTag := stringValue(child)
				clonedTag, err := clone(childTag, mark)
				if err != nil {
					return "", err
				}
				mapping[childTag] = clonedTag
				clonedChildren = append(clonedChildren, clonedTag)
			}
			copied["outbounds"] = clonedChildren
			if oldDefault := stringValue(copied["default"]); oldDefault != "" {
				if newDefault, ok := mapping[oldDefault]; ok {
					copied["default"] = newDefault
				}
			}
			delete(copied, "routing_mark")
		case "block", "dns":
			delete(copied, "routing_mark")
		default:
			if detour := stringValue(copied["detour"]); detour != "" {
				clonedDetour, err := clone(detour, mark)
				if err != nil {
					return "", err
				}
				copied["detour"] = clonedDetour
				delete(copied, "routing_mark") // sing-box ignores other dial fields with detour.
			} else {
				copied["routing_mark"] = mark
			}
		}

		outbounds = append(outbounds, copied)
		index[newTag] = copied
		cache[key] = newTag
		return newTag, nil
	}

	for _, u := range active {
		for _, n := range u.Nodes {
			_, _, mark := effectiveNodeRate(u, n)
			target := n.Outbound
			if target == "" {
				target = defaultTag
			}
			if target == "" {
				return nil, fmt.Errorf("用户 %s/%s 没有可用的 route.final 或首个出站", u.Name, n.Name)
			}
			clonedTag, err := clone(target, mark)
			if err != nil {
				return nil, fmt.Errorf("用户 %s/%s: %w", u.Name, n.Name, err)
			}
			result[n.AuthUser] = clonedTag
		}
	}
	cfg["outbounds"] = outbounds
	return result, nil
}

func renderNftables(s *State) (string, error) {
	return renderNftablesWithCounters(s, nil)
}

func renderNftablesWithCounters(s *State, liveCounters map[string]int64) (string, error) {
	normalizeDeviceModel(s)
	ensureNodeMarks(s)
	if err := validateRateMarks(s); err != nil {
		return "", err
	}
	type bucket struct {
		mark     uint32
		upload   float64
		download float64
		label    string
		soft     bool
	}
	bucketsByMark := map[uint32]bucket{}
	for _, u := range s.Users {
		for _, n := range u.Nodes {
			up, down, mark := effectiveNodeRate(u, n)
			label := deviceNodeLabel(u.Name, n.Device, n.Name)
			bucketsByMark[mark] = bucket{mark: mark, upload: up, download: down, label: label, soft: burstSoftBlocked(u, time.Now())}
		}
	}
	buckets := make([]bucket, 0, len(bucketsByMark))
	for _, item := range bucketsByMark {
		buckets = append(buckets, item)
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].mark < buckets[j].mark })
	if len(buckets) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("table inet sbmgr {\n")
	b.WriteString("  chain save_mark {\n")
	b.WriteString("    type filter hook output priority mangle; policy accept;\n")
	fmt.Fprintf(&b, "    meta mark & 0x%08x == 0x%08x ct mark set meta mark\n", rateMarkMask, rateMarkPrefix)
	b.WriteString("  }\n")
	b.WriteString("  chain upload {\n")
	b.WriteString("    type filter hook output priority filter; policy accept;\n")
	for _, item := range buckets {
		label := item.label + " upload"
		bytes := initialNftCounter(s, item.mark, "upload", label, liveCounters)
		if item.upload > 0 {
			fmt.Fprintf(&b, "    meta mark 0x%08x limit rate over %d bytes/second burst %d bytes drop\n", item.mark, mbpsBytes(item.upload), rateBurst(item.upload, item.soft))
		}
		fmt.Fprintf(&b, "    meta mark 0x%08x %s comment %s\n", item.mark, nftCounterExpression(bytes), nftQuote(label))
	}
	b.WriteString("  }\n")
	b.WriteString("  chain download {\n")
	b.WriteString("    type filter hook input priority filter; policy accept;\n")
	for _, item := range buckets {
		label := item.label + " download"
		bytes := initialNftCounter(s, item.mark, "download", label, liveCounters)
		if item.download > 0 {
			fmt.Fprintf(&b, "    ct mark 0x%08x limit rate over %d bytes/second burst %d bytes drop\n", item.mark, mbpsBytes(item.download), rateBurst(item.download, item.soft))
		}
		fmt.Fprintf(&b, "    ct mark 0x%08x %s comment %s\n", item.mark, nftCounterExpression(bytes), nftQuote(label))
	}
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String(), nil
}

func initialNftCounter(s *State, mark uint32, direction, label string, liveCounters map[string]int64) int64 {
	if value, ok := liveCounters[label]; ok && value >= 0 {
		return value
	}
	if value := s.Counters[fmt.Sprintf("nft:%08x:%s", mark, direction)]; value > 0 {
		return value
	}
	return 0
}

func nftCounterExpression(bytes int64) string {
	if bytes <= 0 {
		return "counter"
	}
	return fmt.Sprintf("counter packets 0 bytes %d", bytes)
}

func mbpsBytes(mbps float64) int64 {
	value := int64(math.Round(mbps * 1_000_000 / 8))
	if value < 1 {
		return 1
	}
	return value
}

func rateBurst(mbps float64, soft bool) int64 {
	if soft {
		// Keep a few TLS/TCP packets possible while avoiding the 128 KiB normal
		// burst, which would let an entire streamed answer escape unthrottled.
		return 4 * 1024
	}
	burst := mbpsBytes(mbps) / 4 // 250 ms token bucket.
	if burst < 128*1024 {        // Accommodate GRO/GSO packets.
		burst = 128 * 1024
	}
	if burst > 4*1024*1024 {
		burst = 4 * 1024 * 1024
	}
	return burst
}

func nftQuote(value string) string {
	return strconv.Quote(value)
}

func checkRateLimits(s *State, w io.Writer) error {
	return runNftables(s, true, w)
}

func applyRateLimits(s *State, w io.Writer) error {
	return runNftables(s, false, w)
}

func applyRateLimitsIfMissing(s *State, w io.Writer) error {
	if runtime.GOOS != "linux" || !hasManagedNodes(s) {
		return applyRateLimits(s, w)
	}
	if _, err := exec.LookPath("nft"); err != nil {
		return errors.New("找不到 nft；请安装 nftables")
	}
	exists, err := nftRateTableExists()
	if err != nil {
		return err
	}
	if exists {
		fmt.Fprintln(w, "nftables 实时限速规则已存在")
		return nil
	}
	return applyRateLimits(s, w)
}

func runNftables(s *State, checkOnly bool, w io.Writer) error {
	rules, err := renderNftables(s)
	if err != nil {
		return err
	}
	if runtime.GOOS != "linux" {
		if rules == "" {
			fmt.Fprintln(w, "没有需要应用的实时限速")
			return nil
		}
		return errors.New("实时 Mbps 限速仅支持 Linux nftables")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		if rules == "" {
			fmt.Fprintln(w, "没有需要应用的实时限速")
			return nil
		}
		return errors.New("找不到 nft；请安装 nftables")
	}

	exists, err := nftRateTableExists()
	if err != nil {
		return err
	}
	if exists {
		liveCounters, err := readNftRateCounters()
		if err != nil {
			return err
		}
		rules, err = renderNftablesWithCounters(s, liveCounters)
		if err != nil {
			return err
		}
	}
	if rules == "" && !exists {
		fmt.Fprintln(w, "没有需要应用的实时限速")
		return nil
	}
	var script strings.Builder
	if exists {
		script.WriteString("delete table inet sbmgr\n")
	}
	script.WriteString(rules)

	dir := filepath.Dir(s.ConfigPath)
	if dir == "." || dir == "" {
		dir = os.TempDir()
	}
	f, err := os.CreateTemp(dir, "sbmgr-nft-*.nft")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.WriteString(script.String()); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	args := []string{"-f", name}
	if checkOnly {
		args = []string{"--check", "-f", name}
	}
	cmd := exec.Command("nft", args...)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nftables 限速规则%s失败: %w", map[bool]string{true: "校验", false: "应用"}[checkOnly], err)
	}
	if checkOnly {
		fmt.Fprintln(w, "nftables 限速规则校验通过")
	} else if rules == "" {
		fmt.Fprintln(w, "已清除 sbmgr 实时限速规则")
	} else {
		fmt.Fprintln(w, "nftables 实时限速已应用")
	}
	return nil
}

func nftRateTableExists() (bool, error) {
	cmd := exec.Command("nft", "list", "table", "inet", "sbmgr")
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	text := strings.ToLower(stderr.String())
	if strings.Contains(text, "no such file") || strings.Contains(text, "does not exist") {
		return false, nil
	}
	return false, fmt.Errorf("检查 nftables 表失败: %w: %s", err, strings.TrimSpace(stderr.String()))
}

func readNftRateCounters() (map[string]int64, error) {
	out, err := exec.Command("nft", "-j", "list", "table", "inet", "sbmgr").Output()
	if err != nil {
		return nil, fmt.Errorf("读取 nftables 用量计数: %w", err)
	}
	counters, err := parseNftCounterJSON(out)
	if err != nil {
		return nil, err
	}
	return counters, nil
}

type nftRateSnapshot struct {
	Exists bool
	Rules  string
}

func captureNftRateSnapshot() (nftRateSnapshot, error) {
	if runtime.GOOS != "linux" {
		return nftRateSnapshot{}, nil
	}
	exists, err := nftRateTableExists()
	if err != nil || !exists {
		return nftRateSnapshot{Exists: exists}, err
	}
	out, err := exec.Command("nft", "list", "table", "inet", "sbmgr").CombinedOutput()
	if err != nil {
		return nftRateSnapshot{}, fmt.Errorf("备份 nftables 限速表: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nftRateSnapshot{Exists: true, Rules: string(out)}, nil
}

func restoreNftRateSnapshot(s *State, snapshot nftRateSnapshot, w io.Writer) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	exists, err := nftRateTableExists()
	if err != nil {
		return err
	}
	if !exists && !snapshot.Exists {
		return nil
	}
	var script strings.Builder
	if exists {
		script.WriteString("delete table inet sbmgr\n")
	}
	if snapshot.Exists {
		script.WriteString(snapshot.Rules)
		if !strings.HasSuffix(snapshot.Rules, "\n") {
			script.WriteByte('\n')
		}
	}
	dir := filepath.Dir(s.ConfigPath)
	if dir == "." || dir == "" {
		dir = os.TempDir()
	}
	f, err := os.CreateTemp(dir, "sbmgr-nft-restore-*.nft")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.WriteString(script.String()); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	cmd := exec.Command("nft", "-f", name)
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("恢复 nftables 限速表: %w", err)
	}
	return nil
}
