//go:build !linux

package main

import "errors"

func readPhysicalUplinkCounters() (string, int64, int64, error) {
	return "", 0, 0, errors.New("机器网卡流量采样仅在 Linux 上可用")
}
