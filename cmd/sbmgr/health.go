package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

func normalizedHealthSettings(settings HealthSettings) HealthSettings {
	if settings.Mode == "" {
		settings.Mode = "auto"
	}
	if settings.IntervalMinutes == 0 {
		settings.IntervalMinutes = 5
	}
	if settings.TimeoutSeconds == 0 {
		settings.TimeoutSeconds = 3
	}
	if settings.AlertAfterFailures == 0 {
		settings.AlertAfterFailures = 2
	}
	return settings
}

func validateHealthSettings(settings HealthSettings) error {
	if settings.Mode != "auto" && settings.Mode != "off" {
		return errors.New("出口健康模式必须是 auto 或 off")
	}
	if settings.IntervalMinutes < 1 || settings.IntervalMinutes > 1440 {
		return errors.New("出口健康探测间隔必须在 1–1440 分钟之间")
	}
	if settings.TimeoutSeconds < 1 || settings.TimeoutSeconds > 30 {
		return errors.New("出口健康探测超时必须在 1–30 秒之间")
	}
	if settings.AlertAfterFailures < 1 || settings.AlertAfterFailures > 100 {
		return errors.New("出口健康告警连续失败次数必须在 1–100 之间")
	}
	for tag, target := range settings.Targets {
		if strings.TrimSpace(tag) == "" {
			return errors.New("出口健康自定义目标包含空 tag")
		}
		if _, _, err := net.SplitHostPort(strings.TrimSpace(target)); err != nil {
			return fmt.Errorf("出口 %s 的探测目标 %q 必须是 host:port", tag, target)
		}
	}
	return nil
}

func normalizedNotificationSettings(settings NotificationSettings) NotificationSettings {
	if settings.TimeoutSeconds == 0 {
		settings.TimeoutSeconds = 5
	}
	return settings
}

func validateNotificationSettings(settings NotificationSettings) error {
	if settings.TimeoutSeconds < 1 || settings.TimeoutSeconds > 30 {
		return errors.New("Webhook 超时必须在 1–30 秒之间")
	}
	if settings.WebhookURL == "" {
		return nil
	}
	parsed, err := url.Parse(settings.WebhookURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("Webhook URL 必须是有效的 http 或 https 地址")
	}
	return nil
}

func outboundProbeTargets(s *State) (map[string]string, error) {
	settings := normalizedHealthSettings(s.Health)
	targets := map[string]string{}
	for tag, target := range settings.Targets {
		targets[strings.TrimSpace(tag)] = strings.TrimSpace(target)
	}
	raw, err := os.ReadFile(s.BaseConfig)
	if err != nil {
		return nil, fmt.Errorf("读取出口探测基础配置: %w", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("解析出口探测基础配置: %w", err)
	}
	outbounds, _ := cfg["outbounds"].([]any)
	for _, item := range outbounds {
		outbound, _ := item.(map[string]any)
		tag := stringValue(outbound["tag"])
		if tag == "" || targets[tag] != "" {
			continue
		}
		server := stringValue(outbound["server"])
		port := numericPort(outbound["server_port"])
		if server != "" && port > 0 {
			targets[tag] = net.JoinHostPort(server, strconv.Itoa(port))
		}
	}
	return targets, nil
}

func numericPort(value any) int {
	switch value := value.(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		result, _ := strconv.Atoi(value.String())
		return result
	case string:
		result, _ := strconv.Atoi(value)
		return result
	default:
		return 0
	}
}

func healthCheckDue(s *State, now time.Time) bool {
	settings := normalizedHealthSettings(s.Health)
	if settings.Mode == "off" {
		return false
	}
	last, err := time.Parse(time.RFC3339Nano, s.LastHealthCheck)
	return err != nil || now.Sub(last) >= time.Duration(settings.IntervalMinutes)*time.Minute
}

func checkOutboundHealth(s *State, now time.Time, force bool) (bool, error) {
	settings := normalizedHealthSettings(s.Health)
	if settings.Mode == "off" || (!force && !healthCheckDue(s, now)) {
		return false, nil
	}
	targets, err := outboundProbeTargets(s)
	if err != nil {
		return false, err
	}
	if s.OutboundHealth == nil {
		s.OutboundHealth = map[string]OutboundHealth{}
	}
	type result struct {
		tag     string
		target  string
		latency time.Duration
		err     error
	}
	results := make(chan result, len(targets))
	var wg sync.WaitGroup
	for tag, target := range targets {
		wg.Add(1)
		go func(tag, target string) {
			defer wg.Done()
			started := time.Now()
			connection, err := net.DialTimeout("tcp", target, time.Duration(settings.TimeoutSeconds)*time.Second)
			if err == nil {
				_ = connection.Close()
			}
			results <- result{tag: tag, target: target, latency: time.Since(started), err: err}
		}(tag, target)
	}
	wg.Wait()
	close(results)
	for item := range results {
		previous := s.OutboundHealth[item.tag]
		status := OutboundHealth{Tag: item.tag, Target: item.target, CheckedAt: now.Format(time.RFC3339), LatencyMS: item.latency.Milliseconds()}
		if item.err == nil {
			status.Healthy = true
			if previous.CheckedAt != "" && !previous.Healthy && previous.Failures >= settings.AlertAfterFailures {
				appendAlert(s, Alert{At: now.Format(time.RFC3339), Kind: "outbound_recovered", Message: fmt.Sprintf("出口 %s 已恢复，TCP 延迟 %d ms（%s）", item.tag, status.LatencyMS, item.target)})
			}
		} else {
			status.Failures = previous.Failures + 1
			status.Error = item.err.Error()
			if status.Failures == settings.AlertAfterFailures {
				appendAlert(s, Alert{At: now.Format(time.RFC3339), Kind: "outbound_unhealthy", Message: fmt.Sprintf("出口 %s 连续 %d 次探测失败（%s）：%v", item.tag, status.Failures, item.target, item.err)})
			}
		}
		s.OutboundHealth[item.tag] = status
	}
	s.LastHealthCheck = now.Format(time.RFC3339Nano)
	return true, nil
}

func deliverPendingAlerts(s *State, now time.Time) (bool, error) {
	settings := normalizedNotificationSettings(s.Notifications)
	if settings.WebhookURL == "" {
		return false, nil
	}
	client := &http.Client{Timeout: time.Duration(settings.TimeoutSeconds) * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	changed := false
	var deliveryErrors []error
	delivered := 0
	for index := range s.Alerts {
		if ctx.Err() != nil {
			break
		}
		alert := &s.Alerts[index]
		if alert.NotifiedAt != "" || delivered >= 10 {
			continue
		}
		if last, err := time.Parse(time.RFC3339Nano, alert.LastNotifyAttempt); err == nil && now.Sub(last) < 5*time.Minute {
			continue
		}
		body, _ := json.Marshal(map[string]any{"source": "sbmgr", "version": appVersion, "alert": alert})
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, settings.WebhookURL, bytes.NewReader(body))
		if err == nil {
			request.Header.Set("Content-Type", "application/json")
			if settings.WebhookSecret != "" {
				request.Header.Set("Authorization", "Bearer "+settings.WebhookSecret)
			}
			var response *http.Response
			response, err = client.Do(request)
			if response != nil {
				_, _ = io.CopyN(io.Discard, response.Body, 4096)
				_ = response.Body.Close()
				if err == nil && (response.StatusCode < 200 || response.StatusCode >= 300) {
					err = fmt.Errorf("HTTP %s", response.Status)
				}
			}
		}
		alert.NotifyAttempts++
		alert.LastNotifyAttempt = now.Format(time.RFC3339Nano)
		alert.NotifyError = ""
		changed = true
		delivered++
		if err != nil {
			// net/url errors contain the full webhook URL, often a credential.
			alert.NotifyError = "Webhook 投递失败（连接、超时或非成功状态码）"
			deliveryErrors = append(deliveryErrors, errors.New(alert.NotifyError))
			continue
		}
		alert.NotifiedAt = now.Format(time.RFC3339)
	}
	return changed, errors.Join(deliveryErrors...)
}

func parseHealthTargets(value string) (map[string]string, error) {
	result := map[string]string{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("自定义探测目标 %q 应为 tag=host:port", item)
		}
		result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return result, nil
}

func formatHealthTargets(targets map[string]string) string {
	keys := make([]string, 0, len(targets))
	for key := range targets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key+"="+targets[key])
	}
	return strings.Join(values, ",")
}

func (a *app) healthCmd(args []string) error {
	return a.withAuditedStateLock(auditAction("health", args), args, func() error { return a.healthCmdLocked(args) })
}

func (a *app) healthCmdLocked(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: sbmgr admin health list|check|set")
	}
	s, err := loadState(a.statePath)
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		statuses := make([]OutboundHealth, 0, len(s.OutboundHealth))
		for _, status := range s.OutboundHealth {
			statuses = append(statuses, status)
		}
		sort.Slice(statuses, func(i, j int) bool { return statuses[i].Tag < statuses[j].Tag })
		fmt.Fprintln(a.out, "TAG\tSTATUS\tLATENCY_MS\tFAILURES\tTARGET\tCHECKED")
		for _, status := range statuses {
			label := "healthy"
			if !status.Healthy {
				label = "failed"
			}
			fmt.Fprintf(a.out, "%s\t%s\t%d\t%d\t%s\t%s\n", status.Tag, label, status.LatencyMS, status.Failures, status.Target, status.CheckedAt)
		}
		return nil
	case "check":
		changed, err := checkOutboundHealth(s, time.Now(), true)
		if changed {
			if saveErr := saveState(a.statePath, s); saveErr != nil {
				return saveErr
			}
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(a.out, "已探测 %d 个出口\n", len(s.OutboundHealth))
		return nil
	case "set":
		fs := a.newFlagSet("health set")
		mode := fs.String("mode", "", "auto 或 off")
		interval := fs.Int("interval", 0, "探测间隔分钟")
		timeout := fs.Int("timeout", 0, "单次超时秒数")
		failures := fs.Int("failures", 0, "连续失败多少次告警")
		targets := fs.String("targets", "", "tag=host:port，逗号分隔")
		webhook := fs.String("webhook", "", "告警 Webhook URL；留空关闭")
		webhookSecret := fs.String("webhook-secret", "", "Webhook Bearer 密钥")
		webhookTimeout := fs.Int("webhook-timeout", 0, "Webhook 超时秒数")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("health set 不接受位置参数")
		}
		health := normalizedHealthSettings(s.Health)
		notifications := normalizedNotificationSettings(s.Notifications)
		changed := false
		var parseErr error
		fs.Visit(func(flag *flag.Flag) {
			changed = true
			switch flag.Name {
			case "mode":
				health.Mode = strings.ToLower(strings.TrimSpace(*mode))
			case "interval":
				health.IntervalMinutes = *interval
			case "timeout":
				health.TimeoutSeconds = *timeout
			case "failures":
				health.AlertAfterFailures = *failures
			case "targets":
				health.Targets, parseErr = parseHealthTargets(*targets)
			case "webhook":
				notifications.WebhookURL = strings.TrimSpace(*webhook)
			case "webhook-secret":
				notifications.WebhookSecret = *webhookSecret
			case "webhook-timeout":
				notifications.TimeoutSeconds = *webhookTimeout
			}
		})
		if !changed {
			return errors.New("没有指定要修改的健康或通知设置")
		}
		if parseErr != nil {
			return parseErr
		}
		if err := validateHealthSettings(health); err != nil {
			return err
		}
		if err := validateNotificationSettings(notifications); err != nil {
			return err
		}
		s.Health, s.Notifications = health, notifications
		if err := saveState(a.statePath, s); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "已更新出口健康与告警通知设置")
		return nil
	default:
		return fmt.Errorf("未知 health 子命令 %q", args[0])
	}
}
