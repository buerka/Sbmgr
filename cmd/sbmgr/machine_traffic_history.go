package main

import (
	"database/sql"
	"errors"
	"math"
	"sbmgr/internal/mesh"
	"slices"
	"time"
)

const machineTrafficHistoryPageSize = 20

type machineTrafficHistorySegment struct {
	rule  MachineTrafficPeriod
	first int64
	last  int64
	count int64
}

type machineTrafficHistoryResult struct {
	Member   string               `json:"member"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
	Total    int64                `json:"total"`
	Periods  []machineTrafficView `json:"periods"`
}

func machineTrafficHistory(path string, s *State, member string, page int, now time.Time) (machineTrafficHistoryResult, error) {
	result := machineTrafficHistoryResult{Member: member, Page: page, PageSize: machineTrafficHistoryPageSize, Periods: []machineTrafficView{}}
	if page < 1 || page > 100000 {
		return result, errors.New("历史页码超出范围")
	}
	validMember := s.Mesh != nil && slices.ContainsFunc(s.Mesh.Members, func(m mesh.Member) bool { return m.ID == member })
	validLocal := s.Mesh == nil && s.MeshAgent.Cluster == "" && member == "local"
	if !validMember && !validLocal {
		return result, errors.New("机器不存在")
	}
	if !isSQLiteStatePath(path) {
		return result, nil
	}
	db, _, err := openSQLiteState(path)
	if err != nil {
		return result, err
	}
	defer db.Close()
	queryMember := member
	if s.Mesh != nil && member == s.Mesh.Master && member != "local" {
		var present int
		err := db.QueryRow(`SELECT 1 FROM machine_traffic_baselines WHERE member='local'`).Scan(&present)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
		if err == nil {
			queryMember = "local"
		}
	}
	var earliest sql.NullInt64
	if err := db.QueryRow(`SELECT MIN(start_ns) FROM machine_traffic_intervals WHERE member IN (?,?)`, member, queryMember).Scan(&earliest); err != nil {
		return result, err
	}
	if !earliest.Valid {
		return result, nil
	}
	rules := make([]MachineTrafficPeriod, 0, len(s.MachineTrafficHistory)+1)
	for _, p := range s.MachineTraffic {
		if p.Member == member {
			rules = append(rules, p)
			break
		}
	}
	for i := len(s.MachineTrafficHistory) - 1; i >= 0; i-- {
		p := s.MachineTrafficHistory[i]
		if p.Member == member {
			rules = append(rules, p)
		}
	}
	segments := make([]machineTrafficHistorySegment, 0, len(rules))
	for _, p := range rules {
		anchor, err := machineTrafficAnchor(p.Start)
		if err != nil {
			return result, err
		}
		begin := anchor
		if earliest.Int64 > begin.UnixNano() {
			begin = time.Unix(0, earliest.Int64)
		}
		if p.EffectiveFromNS > 0 && p.EffectiveFromNS > begin.UnixNano() {
			begin = time.Unix(0, p.EffectiveFromNS)
		}
		end := now
		if p.EffectiveUntilNS > 0 && p.EffectiveUntilNS < end.UnixNano() {
			end = time.Unix(0, p.EffectiveUntilNS)
		}
		if !end.After(begin) {
			continue
		}
		first, err := machineTrafficIndex(p, begin)
		if err != nil {
			return result, err
		}
		last, err := machineTrafficIndex(p, end.Add(-time.Nanosecond))
		if err != nil {
			return result, err
		}
		if p.EffectiveUntilNS == 0 {
			current, err := machineTrafficIndex(p, now)
			if err != nil {
				return result, err
			}
			if last >= current {
				last = current - 1
			}
		}
		if last < first {
			continue
		}
		count := last - first + 1
		if result.Total > math.MaxInt64-count {
			return result, errors.New("机器账期历史数量溢出")
		}
		result.Total += count
		segments = append(segments, machineTrafficHistorySegment{rule: p, first: first, last: last, count: count})
	}
	offset := int64(page-1) * machineTrafficHistoryPageSize
	if offset >= result.Total {
		return result, nil
	}
	for _, segment := range segments {
		if offset >= segment.count {
			offset -= segment.count
			continue
		}
		for idx := segment.last - offset; idx >= segment.first && len(result.Periods) < machineTrafficHistoryPageSize; idx-- {
			view, err := machineTrafficPeriodView(db, member, queryMember, segment.rule, idx, now)
			if err != nil {
				return result, err
			}
			result.Periods = append(result.Periods, view)
		}
		offset = 0
		if len(result.Periods) == machineTrafficHistoryPageSize {
			break
		}
	}
	return result, nil
}
