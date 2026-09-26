package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Stored in SQLite's constrained global settings document, not a new table.
// State omits the zero value to preserve hashes of pre-retention databases.
type BackupSettings struct {
	RetentionDays int `json:"retention_days"`
}

func validateBackupSettings(settings BackupSettings) error {
	if settings.RetentionDays < 0 || settings.RetentionDays > 3650 {
		return errors.New("备份保留天数必须在 0–3650 之间；0 表示关闭按时间清理")
	}
	return nil
}

var (
	dailyBackupPattern  = regexp.MustCompile(`^state-([0-9]{8})\.(db|json)$`)
	manualBackupPattern = regexp.MustCompile(`^state-manual-([0-9]{8}-[0-9]{6})(-[0-9]+)?\.(db|json)$`)
)

func expirableBackupKind(name string) string {
	if match := dailyBackupPattern.FindStringSubmatch(name); match != nil {
		if _, err := time.Parse("20060102", match[1]); err == nil {
			return "daily"
		}
	}
	if match := manualBackupPattern.FindStringSubmatch(name); match != nil {
		if _, err := time.Parse("20060102-150405", match[1]); err == nil {
			return "manual"
		}
	}
	return ""
}

type backupRetentionEntry struct {
	BackupInfo
	ExpiresAt time.Time
	Protected string
}

// The inventory is newest-first. Both cleanup and Web use this exact plan so
// retained recovery files are never advertised as due for deletion.
func planBackupRetention(backups []BackupInfo, settings BackupSettings) []backupRetentionEntry {
	result := make([]backupRetentionEntry, 0, len(backups))
	seen := map[string]bool{}
	for _, backup := range backups {
		entry := backupRetentionEntry{BackupInfo: backup}
		kind := expirableBackupKind(backup.Name)
		switch {
		case kind == "":
			entry.Protected = "恢复或迁移留存"
		case !seen[kind]:
			entry.Protected = "保留最新一份"
		case settings.RetentionDays > 0:
			entry.ExpiresAt = backup.Modified.Add(time.Duration(settings.RetentionDays) * 24 * time.Hour)
		}
		seen[kind] = true
		result = append(result, entry)
	}
	return result
}

type backupStorage struct {
	TotalBytes int64 `json:"total_bytes"`
	StateBytes int64 `json:"state_bytes"`
	OtherBytes int64 `json:"other_bytes"`
}

// Count all regular files under backups/, including deployment/config recovery
// copies. Do not follow symlinks or expose any file contents to the Web process.
func measureBackupStorage(statePath string, backups []BackupInfo) (backupStorage, error) {
	var storage backupStorage
	root, err := os.OpenRoot(stateBackupDir(statePath))
	if errors.Is(err, os.ErrNotExist) {
		return storage, nil
	}
	if err != nil {
		return storage, err
	}
	defer root.Close()
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			storage.TotalBytes += info.Size()
		}
		return nil
	})
	if err != nil {
		return storage, err
	}
	for _, backup := range backups {
		storage.StateBytes += backup.Size
	}
	storage.OtherBytes = max(0, storage.TotalBytes-storage.StateBytes)
	return storage, nil
}

// Called under the cross-process state lock, once per maintenance cycle. File
// removal is independent per backup; failures leave remaining files for retry.
func (a *app) cleanupExpiredBackupsLocked(settings BackupSettings, now time.Time) error {
	if err := validateBackupSettings(settings); err != nil {
		return err
	}
	if settings.RetentionDays == 0 {
		return nil
	}
	backups, err := listStateBackups(a.statePath)
	if err != nil || len(backups) == 0 {
		return err
	}
	root, err := os.OpenRoot(stateBackupDir(a.statePath))
	if err != nil {
		return err
	}
	defer root.Close()
	removed := 0
	var freed int64
	var cleanupErrors []error
	for _, entry := range planBackupRetention(backups, settings) {
		if entry.ExpiresAt.IsZero() || entry.ExpiresAt.After(now) {
			continue
		}
		info, err := root.Lstat(entry.Name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			cleanupErrors = append(cleanupErrors, err)
			continue
		}
		if !info.Mode().IsRegular() || info.Size() != entry.Size || !info.ModTime().Equal(entry.Modified) {
			continue
		}
		// Never remove a database with an unfinished transaction or a live WAL.
		if filepath.Ext(entry.Name) == ".db" {
			unsafe := false
			for _, suffix := range []string{"-wal", "-shm", "-journal"} {
				if _, err := root.Lstat(entry.Name + suffix); !errors.Is(err, os.ErrNotExist) {
					unsafe = true
					if err != nil {
						cleanupErrors = append(cleanupErrors, err)
					}
				}
			}
			if unsafe {
				continue
			}
		}
		if err := root.Remove(entry.Name); err != nil {
			cleanupErrors = append(cleanupErrors, err)
			continue
		}
		removed++
		freed += entry.Size
	}
	if removed > 0 {
		fmt.Fprintf(a.out, "自动清理到期状态备份 %d 份，移除文件大小 %s\n", removed, formatSize(freed))
		if err := appendAuditRecord(a.statePath, AuditRecord{
			At: now.Format(time.RFC3339Nano), Actor: "daemon", Action: "backup.cleanup",
			Args: []string{fmt.Sprintf("removed=%d", removed), fmt.Sprintf("bytes=%d", freed)}, PID: os.Getpid(),
		}); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("备份已清理，但写入审计失败: %w", err))
		}
	}
	return errors.Join(cleanupErrors...)
}
