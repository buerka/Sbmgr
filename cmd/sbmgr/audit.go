package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type AuditRecord struct {
	At     string   `json:"at"`
	Actor  string   `json:"actor"`
	Action string   `json:"action"`
	Args   []string `json:"args,omitempty"`
	PID    int      `json:"pid"`
}

func auditAction(prefix string, args []string) string {
	if len(args) == 0 {
		return prefix
	}
	return prefix + "." + args[0]
}

func (a *app) withAuditedStateLock(action string, args []string, fn func() error) error {
	return a.withStateLock(func() error {
		if err := fn(); err != nil {
			return err
		}
		if auditReadOnlyAction(action) {
			return nil
		}
		if err := appendAuditRecord(a.statePath, AuditRecord{At: time.Now().Format(time.RFC3339Nano), Actor: auditActor(), Action: action, Args: sanitizeAuditArgs(args), PID: os.Getpid()}); err != nil {
			// The mutation has already been durably saved. Returning the audit
			// error as the command result would invite a retry and duplicate the
			// operation, so surface it explicitly as a partial-success warning.
			fmt.Fprintln(a.err, "警告：操作已完成，但写入审计日志失败:", err)
		}
		return nil
	})
}

func auditReadOnlyAction(action string) bool {
	if action == "template.check" {
		return true
	}
	for _, suffix := range []string{".list", ".show", ".templates", ".status", ".link"} {
		if strings.HasSuffix(action, suffix) {
			return true
		}
	}
	return false
}

func auditActor() string {
	for _, key := range []string{"SUDO_USER", "USER", "USERNAME"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return "unknown"
}

func sanitizeAuditArgs(args []string) []string {
	result := append([]string(nil), args...)
	redactNext := false
	for index := range result {
		if redactNext {
			result[index] = "[REDACTED]"
			redactNext = false
			continue
		}
		name := strings.SplitN(result[index], "=", 2)[0]
		if strings.Contains(result[index], "://") {
			if strings.HasPrefix(name, "--") && strings.Contains(result[index], "=") {
				result[index] = safeTerminalText(name) + "=[REDACTED]"
			} else {
				result[index] = "[REDACTED]"
			}
			continue
		}
		if !strings.HasPrefix(name, "--") {
			if strings.Contains(result[index], "://") {
				result[index] = "[REDACTED]"
			} else {
				result[index] = safeTerminalText(result[index])
			}
			continue
		}
		switch name {
		// New flags are confidential until explicitly reviewed here.
		case "--upload", "--download", "--quota", "--expires", "--enabled", "--mode", "--interval", "--timeout", "--failures", "--webhook-timeout", "--max-connections", "--connection-action", "--device", "--name", "--apply", "--restart", "--force", "--dry-run", "--tag", "--type", "--from", "--to", "--username-change", "--password-change":
			result[index] = safeTerminalText(result[index])
		default:
			if strings.Contains(result[index], "=") {
				result[index] = name + "=[REDACTED]"
			} else {
				redactNext = true
			}
		}
	}
	return result
}

func auditPath(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), "audit.jsonl")
}

func appendAuditRecord(statePath string, record AuditRecord) error {
	path := auditPath(statePath)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil && info.Size() >= 5*1024*1024 {
		previous := filepath.Join(filepath.Dir(path), "audit.previous.jsonl")
		_ = os.Remove(previous)
		if err := os.Rename(path, previous); err != nil {
			return fmt.Errorf("轮换审计日志: %w", err)
		}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func readAuditRecords(statePath string, limit int) ([]AuditRecord, error) {
	file, err := os.Open(auditPath(statePath))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	records := []AuditRecord{}
	for scanner.Scan() {
		var record AuditRecord
		if json.Unmarshal(scanner.Bytes(), &record) == nil {
			records = append(records, record)
			if limit > 0 && len(records) > limit {
				records = append([]AuditRecord(nil), records[len(records)-limit:]...)
			}
		}
	}
	return records, scanner.Err()
}
