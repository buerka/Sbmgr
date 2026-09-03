package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type BackupInfo struct {
	Name     string
	Size     int64
	Modified time.Time
}

func (a *app) backupCmd(args []string) error {
	return a.withAuditedStateLock(auditAction("backup", args), args, func() error { return a.backupCmdLocked(args) })
}

func (a *app) backupCmdLocked(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: sbmgr admin backup list|create|restore")
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return errors.New("backup list 不接受额外参数")
		}
		backups, err := listStateBackups(a.statePath)
		if err != nil {
			return err
		}
		fmt.Fprintln(a.out, "NAME\tMODIFIED\tSIZE")
		for _, backup := range backups {
			fmt.Fprintf(a.out, "%s\t%s\t%s\n", backup.Name, backup.Modified.Format(time.RFC3339), formatSize(backup.Size))
		}
		return nil
	case "create":
		if len(args) != 1 {
			return errors.New("backup create 不接受额外参数")
		}
		name, err := createManualStateBackup(a.statePath, time.Now())
		if err != nil {
			return err
		}
		fmt.Fprintln(a.out, "已创建状态备份", name)
		return nil
	case "restore":
		fs := a.newFlagSet("backup restore")
		doApply := fs.Bool("apply", false, "恢复后校验并应用 sing-box 配置")
		if len(args) < 2 {
			return errors.New("用法: sbmgr admin backup restore NAME [--apply]")
		}
		name := args[1]
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("用法: sbmgr admin backup restore NAME [--apply]")
		}
		restored, err := readStateBackup(a.statePath, name)
		if err != nil {
			return err
		}
		previous, err := loadState(a.statePath)
		if err != nil {
			return fmt.Errorf("读取恢复前状态: %w", err)
		}
		if _, err := createManualStateBackup(a.statePath, time.Now()); err != nil {
			return fmt.Errorf("恢复前创建安全备份: %w", err)
		}
		if err := saveState(a.statePath, restored); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "已恢复状态备份", name)
		if *doApply {
			if err := applyState(restored, false, true, a.out); err != nil {
				if rollbackErr := saveState(a.statePath, previous); rollbackErr != nil {
					return errors.Join(fmt.Errorf("配置应用失败: %w", err), fmt.Errorf("恢复原状态失败: %w", rollbackErr))
				}
				return fmt.Errorf("配置应用失败，状态已恢复到操作前: %w", err)
			}
		}
		return nil
	default:
		return fmt.Errorf("未知 backup 子命令 %q", args[0])
	}
}

func stateBackupDir(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), "backups")
}

func createManualStateBackup(statePath string, now time.Time) (string, error) {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return "", err
	}
	dir := stateBackupDir(statePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	base := "state-manual-" + now.Format("20060102-150405")
	name := base + ".json"
	for suffix := 1; ; suffix++ {
		if _, err := os.Stat(filepath.Join(dir, name)); errors.Is(err, os.ErrNotExist) {
			break
		} else if err != nil {
			return "", err
		}
		name = fmt.Sprintf("%s-%d.json", base, suffix)
	}
	if err := atomicWrite(filepath.Join(dir, name), raw, 0600); err != nil {
		return "", err
	}
	if err := pruneManualStateBackups(dir, 20); err != nil {
		return "", fmt.Errorf("备份已创建，但清理旧手动备份失败: %w", err)
	}
	return name, nil
}

func pruneManualStateBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	type item struct {
		name     string
		modified time.Time
	}
	items := []item{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "state-manual-") || !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		items = append(items, item{name: name, modified: info.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].modified.After(items[j].modified) })
	if keep < 0 {
		keep = 0
	}
	for _, old := range items[min(keep, len(items)):] {
		if err := os.Remove(filepath.Join(dir, old.name)); err != nil {
			return err
		}
	}
	return nil
}

func listStateBackups(statePath string) ([]BackupInfo, error) {
	entries, err := os.ReadDir(stateBackupDir(statePath))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result []BackupInfo
	for _, entry := range entries {
		if entry.IsDir() || !validStateBackupName(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		result = append(result, BackupInfo{Name: entry.Name(), Size: info.Size(), Modified: info.ModTime()})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Modified.After(result[j].Modified) })
	return result, nil
}

func validStateBackupName(name string) bool {
	return filepath.Base(name) == name && strings.HasPrefix(name, "state") && strings.HasSuffix(strings.ToLower(name), ".json")
}

func readStateBackup(statePath, name string) (*State, error) {
	if !validStateBackupName(name) {
		return nil, errors.New("无效的状态备份文件名")
	}
	raw, err := os.ReadFile(filepath.Join(stateBackupDir(statePath), name))
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("解析备份: %w", err)
	}
	if err := migrateState(&s); err != nil {
		return nil, err
	}
	normalizeLegacyNodeNames(&s)
	if err := validateState(&s); err != nil {
		return nil, fmt.Errorf("备份校验失败: %w", err)
	}
	return &s, nil
}
