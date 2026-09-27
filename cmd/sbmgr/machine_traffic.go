package main

import (
	"errors"
	"fmt"
	"sbmgr/internal/mesh"
	"slices"
	"sync"
	"time"
)

// MachineTrafficPeriod is one revision of a local-calendar renewal rule.
// End is read only while migrating v19; effective bounds preserve earlier
// allocations when an administrator edits the rule.
type MachineTrafficPeriod struct {
	Member           string `json:"member"`
	Start            string `json:"start"`
	Interval         int    `json:"interval,omitempty"`
	Unit             string `json:"unit,omitempty"`
	EffectiveFromNS  int64  `json:"effective_from_ns,omitempty"`
	EffectiveUntilNS int64  `json:"effective_until_ns,omitempty"`
	Legacy           bool   `json:"legacy,omitempty"`
	End              string `json:"end,omitempty"`
}

type machineTrafficReading struct {
	Epoch     string `json:"epoch"`
	SampledNS int64  `json:"sampled_ns"`
	Upload    int64  `json:"upload"`
	Download  int64  `json:"download"`
}

type machineTrafficView struct {
	Member           string  `json:"member"`
	AnchorStart      string  `json:"anchor_start"`
	Interval         int     `json:"interval"`
	Unit             string  `json:"unit"`
	Future           bool    `json:"future"`
	NextReset        string  `json:"next_reset"`
	EffectiveStartAt string  `json:"effective_start_at,omitempty"`
	EffectiveEndAt   string  `json:"effective_end_at,omitempty"`
	PeriodStart      string  `json:"period_start"`
	PeriodEnd        string  `json:"period_end"`
	UploadBytes      int64   `json:"upload_bytes"`
	DownloadBytes    int64   `json:"download_bytes"`
	TotalBytes       int64   `json:"total_bytes"`
	CoveredSeconds   int64   `json:"covered_seconds"`
	PeriodSeconds    int64   `json:"period_seconds"`
	CoveragePercent  float64 `json:"coverage_percent"`
	FirstSampleAt    string  `json:"first_sample_at"`
	LastSampleAt     string  `json:"last_sample_at"`
	Status           string  `json:"status"`
	Note             string  `json:"note"`
}

func machineTrafficBounds(start, end string) (time.Time, time.Time, error) {
	a, err := machineTrafficAnchor(start)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("起始日期必须为 YYYY-MM-DD")
	}
	endDate, err := time.Parse("2006-01-02", end)
	if err != nil || endDate.Format("2006-01-02") != end || end < start {
		return time.Time{}, time.Time{}, errors.New("结束日期必须为不早于起始日的 YYYY-MM-DD")
	}
	// Work with calendar dates before finding the first valid local instant;
	// midnight may not exist on a daylight-saving transition day.
	nextDate := endDate.AddDate(0, 0, 1)
	exclusive, err := machineTrafficLocalDayStart(nextDate.Year(), nextDate.Month(), nextDate.Day())
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("机器账期日期超出可统计范围")
	}
	if !time.Unix(0, a.UnixNano()).Equal(a) || !time.Unix(0, exclusive.UnixNano()).Equal(exclusive) || exclusive.Sub(a) == time.Duration(1<<63-1) {
		return time.Time{}, time.Time{}, errors.New("机器账期日期超出可统计范围")
	}
	return a, exclusive, nil
}

func machineTrafficAnchor(start string) (time.Time, error) {
	day, err := time.Parse("2006-01-02", start)
	if err != nil || day.Format("2006-01-02") != start || day.Year() < 1677 || day.Year() > 2262 {
		return time.Time{}, errors.New("机器账期开始日必须为可统计范围内的 YYYY-MM-DD")
	}
	a, err := machineTrafficLocalDayStart(day.Year(), day.Month(), day.Day())
	if err != nil || !time.Unix(0, a.UnixNano()).Equal(a) {
		return time.Time{}, errors.New("机器账期开始日必须为可统计范围内的 YYYY-MM-DD")
	}
	return a, nil
}

// Midnight can be skipped by a timezone transition. Use the first valid
// instant of the requested calendar day, without carrying that hour to later
// boundaries.
func machineTrafficLocalDayStart(year int, month time.Month, day int) (time.Time, error) {
	for hour := 0; hour < 24; hour++ {
		candidate := time.Date(year, month, day, hour, 0, 0, 0, time.Local)
		y, m, d := candidate.In(time.Local).Date()
		if y == year && m == month && d == day {
			return candidate, nil
		}
	}
	return time.Time{}, errors.New("本地时区不存在该日")
}

func machineTrafficPreviousDate(next time.Time) string {
	y, m, d := next.In(time.Local).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1).Format("2006-01-02")
}

func machineTrafficBoundary(p MachineTrafficPeriod, index int64) (time.Time, error) {
	a, err := machineTrafficAnchor(p.Start)
	if err != nil || index < 0 || index > 200000 || p.Interval < 1 {
		return time.Time{}, errors.New("机器账期超出可计算范围")
	}
	step := index * int64(p.Interval)
	switch p.Unit {
	case "day":
		date := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(step))
		return machineTrafficLocalDayStart(date.Year(), date.Month(), date.Day())
	case "month", "year":
		months := step
		if p.Unit == "year" {
			months *= 12
		}
		absolute := int64(a.Year())*12 + int64(a.Month()-1) + months
		if absolute/12 > 9999 {
			return time.Time{}, errors.New("机器账期超出可计算范围")
		}
		year, month := int(absolute/12), time.Month(absolute%12+1)
		last := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
		day := a.Day()
		if day > last {
			day = last
		}
		return machineTrafficLocalDayStart(year, month, day)
	default:
		return time.Time{}, errors.New("机器账期单位无效")
	}
}

func machineTrafficIndex(p MachineTrafficPeriod, at time.Time) (int64, error) {
	a, err := machineTrafficAnchor(p.Start)
	if err != nil {
		return 0, err
	}
	if at.Before(a) {
		return -1, nil
	}
	at = at.In(time.Local)
	var estimate int64
	switch p.Unit {
	case "day":
		// UTC calendar ordinals avoid 23/25-hour daylight-saving days.
		ay, am, ad := a.Date()
		ty, tm, td := at.In(time.Local).Date()
		startDay := time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)
		endDay := time.Date(ty, tm, td, 0, 0, 0, 0, time.UTC)
		estimate = ((endDay.Unix() - startDay.Unix()) / 86400) / int64(p.Interval)
	case "month":
		estimate = (int64(at.Year()-a.Year())*12 + int64(at.Month()-a.Month())) / int64(p.Interval)
	case "year":
		estimate = int64(at.Year()-a.Year()) / int64(p.Interval)
	default:
		return 0, errors.New("机器账期单位无效")
	}
	if estimate < 0 {
		estimate = 0
	}
	for estimate > 0 {
		boundary, err := machineTrafficBoundary(p, estimate)
		if err != nil {
			return 0, err
		}
		if !boundary.After(at) {
			break
		}
		estimate--
	}
	for {
		next, err := machineTrafficBoundary(p, estimate+1)
		if err != nil {
			return 0, err
		}
		if next.After(at) {
			break
		}
		estimate++
	}
	return estimate, nil
}

func validateMachineTrafficRule(p MachineTrafficPeriod) error {
	if p.End != "" {
		return errors.New("旧机器账期结束日未完成迁移")
	}
	if _, err := machineTrafficAnchor(p.Start); err != nil {
		return err
	}
	if p.Interval < 1 || (p.Interval > 366 && !(p.Legacy && p.Unit == "day")) || p.Interval > 200000 {
		return errors.New("机器账期间隔必须在 1—366 之间")
	}
	if p.Unit != "day" && p.Unit != "month" && p.Unit != "year" {
		return errors.New("机器账期单位必须为 day、month 或 year")
	}
	if p.EffectiveFromNS < 0 || p.EffectiveUntilNS < 0 || (p.EffectiveUntilNS > 0 && p.EffectiveUntilNS <= p.EffectiveFromNS) {
		return errors.New("机器账期修订时间无效")
	}
	_, err := machineTrafficBoundary(p, 1)
	return err
}

func migrateMachineTrafficPeriods(s *State) error {
	for i := range s.MachineTraffic {
		p := &s.MachineTraffic[i]
		if p.Interval != 0 {
			continue
		}
		start, end, err := machineTrafficBounds(p.Start, p.End)
		if err != nil {
			return err
		}
		p.Interval, p.Unit = 0, ""
		for n := 1; n <= 366; n++ {
			candidate := *p
			candidate.Interval, candidate.Unit = n, "month"
			boundary, _ := machineTrafficBoundary(candidate, 1)
			if boundary.Equal(end) {
				p.Interval, p.Unit = n, "month"
				break
			}
		}
		if p.Interval%12 == 0 && p.Interval > 0 {
			p.Interval /= 12
			p.Unit = "year"
		}
		if p.Interval == 0 {
			ay, am, ad := start.Date()
			ey, em, ed := end.Date()
			a := time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)
			b := time.Date(ey, em, ed, 0, 0, 0, 0, time.UTC)
			p.Interval, p.Unit, p.Legacy = int(b.Sub(a).Hours()/24), "day", true
		}
		p.End = ""
		if err := validateMachineTrafficRule(*p); err != nil {
			return err
		}
	}
	return nil
}

func validateMachineTraffic(s *State) error {
	if len(s.MachineTraffic) > mesh.MaxMembers {
		return errors.New("机器账期数量超过上限")
	}
	seen := map[string]bool{}
	lastHistoryEnd := map[string]int64{}
	for _, p := range s.MachineTrafficHistory {
		if p.EffectiveUntilNS == 0 {
			return errors.New("历史机器账期缺少结束时间")
		}
		if err := validateMachineTrafficRule(p); err != nil {
			return err
		}
		if lastHistoryEnd[p.Member] != p.EffectiveFromNS {
			return errors.New("机器账期修订历史不连续")
		}
		lastHistoryEnd[p.Member] = p.EffectiveUntilNS
	}
	for _, p := range s.MachineTraffic {
		validMember := s.Mesh != nil && slices.ContainsFunc(s.Mesh.Members, func(m mesh.Member) bool { return m.ID == p.Member })
		validLocal := s.Mesh == nil && s.MeshAgent.Cluster == "" && p.Member == "local"
		if (!validMember && !validLocal) || seen[p.Member] {
			return errors.New("机器账期成员无效或重复")
		}
		seen[p.Member] = true
		if p.EffectiveUntilNS != 0 || lastHistoryEnd[p.Member] != p.EffectiveFromNS {
			return errors.New("当前机器账期修订时间无效")
		}
		if err := validateMachineTrafficRule(p); err != nil {
			return err
		}
	}
	for member := range lastHistoryEnd {
		if !seen[member] {
			return errors.New("历史机器账期缺少当前规则")
		}
	}
	return nil
}

// Called by the privileged action boundary. It does not alter sing-box or
// grants and is saved through the normal cross-process state transaction.
func (a *app) setMachineTrafficPeriod(member, start, unit string, interval int) error {
	newRule := MachineTrafficPeriod{Member: member, Start: start, Interval: interval, Unit: unit}
	if err := validateMachineTrafficRule(newRule); err != nil {
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
				old := s.MachineTraffic[i]
				if old.Start == start && old.Interval == interval && old.Unit == unit {
					return nil
				}
				now := time.Now().UnixNano()
				if old.EffectiveFromNS >= now {
					return errors.New("机器账期修改时间无效")
				}
				old.EffectiveUntilNS = now
				s.MachineTrafficHistory = append(s.MachineTrafficHistory, old)
				newRule.EffectiveFromNS = now
				s.MachineTraffic[i] = newRule
				return saveState(a.statePath, s)
			}
		}
		s.MachineTraffic = append(s.MachineTraffic, newRule)
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
