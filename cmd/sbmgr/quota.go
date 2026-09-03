package main

import (
	"errors"
	"strings"
)

const (
	quotaModeTotal    = "total"
	quotaModeUpload   = "upload"
	quotaModeDownload = "download"
)

// normalizedQuotaMode keeps states written before quota_mode was introduced
// wire-compatible: an omitted or blank value retains the historic behaviour
// of counting upload and download together.
func normalizedQuotaMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return quotaModeTotal
	}
	return mode
}

func validateQuotaMode(mode string) error {
	switch normalizedQuotaMode(mode) {
	case quotaModeTotal, quotaModeUpload, quotaModeDownload:
		return nil
	default:
		return errors.New("配额计量方式必须是 total、upload 或 download")
	}
}

// measuredUsage is the sole accounting value used for quota enforcement and
// tiered throttling. Raw upload/download counters remain independently stored.
func measuredUsage(u User) int64 {
	upload := max(int64(0), u.Upload)
	download := max(int64(0), u.Download)
	switch normalizedQuotaMode(u.QuotaMode) {
	case quotaModeUpload:
		return upload
	case quotaModeDownload:
		return download
	default:
		maximum := int64(^uint64(0) >> 1)
		if upload > maximum-download {
			return maximum
		}
		return upload + download
	}
}

func quotaModeText(mode string) string {
	switch normalizedQuotaMode(mode) {
	case quotaModeUpload:
		return "仅上传"
	case quotaModeDownload:
		return "仅下载"
	default:
		return "双向合计"
	}
}

func overQuota(u User) bool {
	quota := userQuota(u)
	return quota > 0 && measuredUsage(u) >= quota
}

func usagePercent(u User) float64 {
	quota := userQuota(u)
	if quota <= 0 {
		return 0
	}
	return float64(measuredUsage(u)) * 100 / float64(quota)
}

func normalizeQuotaModes(s *State) {
	if s == nil {
		return
	}
	for index := range s.Users {
		s.Users[index].QuotaMode = normalizedQuotaMode(s.Users[index].QuotaMode)
	}
}
