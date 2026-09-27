package main

import (
	"errors"
	"fmt"
	"sbmgr/internal/mesh"
	"slices"
	"sync"
	"time"
)

// MachineTrafficPeriod is an administrator-entered calendar window. End is
// inclusive in the server's local timezone; samples outside it are never
// inferred from a provider's dashboard or from a preexisting NIC counter.
type MachineTrafficPeriod struct {
	Member string `json:"member"`
	Start  string `json:"start"`
	End    string `json:"end"`
}

type machineTrafficReading struct {
	Epoch     string `json:"epoch"`
	SampledNS int64  `json:"sampled_ns"`
	Upload    int64  `json:"upload"`
	Download  int64  `json:"download"`
}

type machineTrafficView struct {
	Member          string  `json:"member"`
	PeriodStart     string  `json:"period_start"`
	PeriodEnd       string  `json:"period_end"`
	UploadBytes     int64   `json:"upload_bytes"`
	DownloadBytes   int64   `json:"download_bytes"`
	TotalBytes      int64   `json:"total_bytes"`
	CoveredSeconds  int64   `json:"covered_seconds"`
	PeriodSeconds   int64   `json:"period_seconds"`
	CoveragePercent float64 `json:"coverage_percent"`
	FirstSampleAt   string  `json:"first_sample_at"`
	LastSampleAt    string  `json:"last_sample_at"`
	Status          string  `json:"status"`
	Note            string  `json:"note"`
}

func machineTrafficBounds(start, end string) (time.Time, time.Time, error) {
	a, err := time.ParseInLocation("2006-01-02", start, time.Local)
	if err != nil || a.Format("2006-01-02") != start {
		return time.Time{}, time.Time{}, errors.New("起始日期必须为 YYYY-MM-DD")
	}
	b, err := time.ParseInLocation("2006-01-02", end, time.Local)
	if err != nil || b.Format("2006-01-02") != end || b.Before(a) {
		return time.Time{}, time.Time{}, errors.New("结束日期必须为不早于起始日的 YYYY-MM-DD")
	}
	// Exclusive boundary after the inclusive end calendar date. AddDate
	// accounts for daylight-saving days where a day is not 24 hours.
	exclusive := b.AddDate(0, 0, 1)
	if !time.Unix(0, a.UnixNano()).Equal(a) || !time.Unix(0, exclusive.UnixNano()).Equal(exclusive) || exclusive.Sub(a) == time.Duration(1<<63-1) {
		return time.Time{}, time.Time{}, errors.New("机器账期日期超出可统计范围")
	}
	return a, exclusive, nil
}

func validateMachineTraffic(s *State) error {
	if len(s.MachineTraffic) > mesh.MaxMembers {
		return errors.New("机器账期数量超过上限")
	}
	seen := map[string]bool{}
	for _, p := range s.MachineTraffic {
		validMember := s.Mesh != nil && slices.ContainsFunc(s.Mesh.Members, func(m mesh.Member) bool { return m.ID == p.Member })
		validLocal := s.Mesh == nil && s.MeshAgent.Cluster == "" && p.Member == "local"
		if (!validMember && !validLocal) || seen[p.Member] {
			return errors.New("机器账期成员无效或重复")
		}
		seen[p.Member] = true
		if _, _, err := machineTrafficBounds(p.Start, p.End); err != nil {
			return err
		}
	}
	return nil
}

// Called by the privileged action boundary. It does not alter sing-box or
// grants and is saved through the normal cross-process state transaction.
func (a *app) setMachineTrafficPeriod(member, start, end string) error {
	if _, _, err := machineTrafficBounds(start, end); err != nil {
		return err
	}
	return a.withStateLock(func() error {
		s, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		validMember := s.Mesh != nil && slices.ContainsFunc(s.Mesh.Members, func(m mesh.Member) bool { return m.ID == member })
		validLocal := s.Mesh == nil && s.MeshAgent.Cluster == "" && member == "local"
		if !validMember && !validLocal {
			return errors.New("只能设置已登记机器的账期")
		}
		for i := range s.MachineTraffic {
			if s.MachineTraffic[i].Member == member {
				s.MachineTraffic[i] = MachineTrafficPeriod{Member: member, Start: start, End: end}
				return saveState(a.statePath, s)
			}
		}
		s.MachineTraffic = append(s.MachineTraffic, MachineTrafficPeriod{Member: member, Start: start, End: end})
		return saveState(a.statePath, s)
	})
}

func (a *app) machineTrafficCycle() error {
	// Sampling is local to every sbmgr daemon. The master polls cumulative
	// slave readings outside its state lock and merges each reply under lock.
	localErr := a.withStateLock(func() error { return sampleLocalMachineTraffic(a.statePath, time.Now()) })
	s, err := a.loadCanonicalState()
	if err != nil {
		return errors.Join(localErr, err)
	}
	if s.Mesh == nil {
		return localErr
	}
	type remoteResult struct {
		member  string
		reading *machineTrafficReading
		err     error
	}
	var members []mesh.Member
	for _, member := range s.Mesh.Members {
		if member.ID != s.Mesh.Master {
			members = append(members, member)
		}
	}
	results := make([]remoteResult, len(members))
	gate := make(chan struct{}, 32)
	var workers sync.WaitGroup
	for i, member := range members {
		workers.Add(1)
		go func(i int, member mesh.Member) {
			defer workers.Done()
			gate <- struct{}{}
			defer func() { <-gate }()
			request := meshRequest{Protocol: mesh.Protocol, Cluster: s.Mesh.ID, Member: member.ID, Operation: "machine_traffic"}
			response, err := meshExchange(a, member, request, false)
			results[i] = remoteResult{member: member.ID, reading: response.MachineTraffic, err: err}
		}(i, member)
	}
	workers.Wait()
	var failures []error
	for _, result := range results {
		if result.err != nil {
			failures = append(failures, fmt.Errorf("机器 %s 流量同步失败: %w", result.member, result.err))
			continue
		}
		if result.reading == nil {
			failures = append(failures, fmt.Errorf("机器 %s 未返回流量读数", result.member))
			continue
		}
		err = a.withStateLock(func() error {
			current, err := loadState(a.statePath)
			if err != nil {
				return err
			}
			if current.Mesh == nil || current.Mesh.ID != s.Mesh.ID || current.MeshRollout != nil || !slices.ContainsFunc(current.Mesh.Members, func(m mesh.Member) bool { return m.ID == result.member }) {
				return errors.New("流量同步期间拓扑变化")
			}
			return mergeRemoteMachineTraffic(a.statePath, result.member, *result.reading)
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("机器 %s 流量合并失败: %w", result.member, err))
		}
	}
	return errors.Join(append([]error{localErr}, failures...)...)
}
