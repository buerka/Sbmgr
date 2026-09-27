package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"sbmgr/internal/mesh"
	"time"
)

var sqliteMachineTrafficSchema = []string{
	`CREATE TABLE IF NOT EXISTS machine_traffic_baselines (
	 member TEXT PRIMARY KEY, epoch TEXT NOT NULL, interfaces TEXT NOT NULL,
	 sampled_ns INTEGER NOT NULL, raw_rx INTEGER NOT NULL CHECK(raw_rx >= 0),
	 raw_tx INTEGER NOT NULL CHECK(raw_tx >= 0), cumulative_rx INTEGER NOT NULL CHECK(cumulative_rx >= 0),
	 cumulative_tx INTEGER NOT NULL CHECK(cumulative_tx >= 0)
	) STRICT`,
	`CREATE TABLE IF NOT EXISTS machine_traffic_remote_baselines (
	 member TEXT PRIMARY KEY, epoch TEXT NOT NULL, sampled_ns INTEGER NOT NULL,
	 cumulative_rx INTEGER NOT NULL CHECK(cumulative_rx >= 0),
	 cumulative_tx INTEGER NOT NULL CHECK(cumulative_tx >= 0)
	) STRICT`,
	`CREATE TABLE IF NOT EXISTS machine_traffic_intervals (
	 member TEXT NOT NULL, start_ns INTEGER NOT NULL, end_ns INTEGER NOT NULL,
	 upload_bytes INTEGER NOT NULL CHECK(upload_bytes >= 0),
	 download_bytes INTEGER NOT NULL CHECK(download_bytes >= 0),
	 PRIMARY KEY(member, end_ns), CHECK(end_ns > start_ns)
	) STRICT`,
	`CREATE INDEX IF NOT EXISTS machine_traffic_intervals_period_idx
	 ON machine_traffic_intervals(member, start_ns, end_ns)`,
}

var machineTrafficCounters = readPhysicalUplinkCounters

func machineTrafficEpoch() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func machineTrafficLocalMember(s *State) string {
	if s.Mesh != nil {
		return s.Mesh.Master
	}
	if s.MeshAgent.Member != "" {
		return s.MeshAgent.Member
	}
	return "local"
}

// The first read is a baseline. A changed interface set or a smaller raw
// counter starts a fresh epoch and leaves an explicit gap in interval rows.
// All changes commit as one SQLite transaction under the process state lock.
func sampleLocalMachineTraffic(path string, at time.Time) error {
	if !isSQLiteStatePath(path) {
		return nil // Legacy JSON is migration input, never a high-frequency sink.
	}
	s, err := loadState(path)
	if err != nil {
		return err
	}
	interfaces, rx, txBytes, err := machineTrafficCounters()
	if err != nil {
		return err
	}
	db, _, err := openSQLiteState(path)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	member := machineTrafficLocalMember(s)
	var epoch, previousInterfaces string
	var previousNS, oldRX, oldTX, cumulativeRX, cumulativeTX int64
	err = tx.QueryRow(`SELECT epoch,interfaces,sampled_ns,raw_rx,raw_tx,cumulative_rx,cumulative_tx
	 FROM machine_traffic_baselines WHERE member=?`, member).Scan(&epoch, &previousInterfaces, &previousNS, &oldRX, &oldTX, &cumulativeRX, &cumulativeTX)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && at.UnixNano() <= previousNS {
		// A backwards wall clock must not replace a durable baseline or
		// create overlapping intervals. Resume after time passes it.
		return nil
	}
	reset := errors.Is(err, sql.ErrNoRows) || interfaces != previousInterfaces || rx < oldRX || txBytes < oldTX
	if reset {
		epoch, err = machineTrafficEpoch()
		if err != nil {
			return err
		}
	} else {
		up, down := txBytes-oldTX, rx-oldRX
		if up > math.MaxInt64-cumulativeTX || down > math.MaxInt64-cumulativeRX {
			return errors.New("机器累计流量超出范围")
		}
		cumulativeTX += up
		cumulativeRX += down
		if _, err := tx.Exec(`INSERT INTO machine_traffic_intervals(member,start_ns,end_ns,upload_bytes,download_bytes)
		 VALUES(?,?,?,?,?) ON CONFLICT(member,end_ns) DO NOTHING`, member, previousNS, at.UnixNano(), up, down); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO machine_traffic_baselines(member,epoch,interfaces,sampled_ns,raw_rx,raw_tx,cumulative_rx,cumulative_tx)
	 VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(member) DO UPDATE SET epoch=excluded.epoch,interfaces=excluded.interfaces,
	 sampled_ns=excluded.sampled_ns,raw_rx=excluded.raw_rx,raw_tx=excluded.raw_tx,
	 cumulative_rx=excluded.cumulative_rx,cumulative_tx=excluded.cumulative_tx`, member, epoch, interfaces, at.UnixNano(), rx, txBytes, cumulativeRX, cumulativeTX); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return chmodSQLiteFiles(path)
}

func localMachineTrafficReading(path string) (*machineTrafficReading, error) {
	s, err := loadState(path)
	if err != nil {
		return nil, err
	}
	db, _, err := openSQLiteState(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var reading machineTrafficReading
	err = db.QueryRow(`SELECT epoch,sampled_ns,cumulative_tx,cumulative_rx FROM machine_traffic_baselines WHERE member=?`, machineTrafficLocalMember(s)).Scan(&reading.Epoch, &reading.SampledNS, &reading.Upload, &reading.Download)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &reading, nil
}

func mergeRemoteMachineTraffic(path, member string, reading machineTrafficReading) error {
	if !mesh.ValidID(member) || len(reading.Epoch) != 32 || reading.SampledNS <= 0 || reading.Upload < 0 || reading.Download < 0 {
		return errors.New("机器流量读数无效")
	}
	for _, c := range reading.Epoch {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return errors.New("机器流量世代标识无效")
		}
	}
	now := time.Now()
	if time.Unix(0, reading.SampledNS).After(now.Add(5 * time.Minute)) {
		return errors.New("从机采样时间超出允许偏差")
	}
	db, _, err := openSQLiteState(path)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var epoch string
	var previousNS, oldUp, oldDown int64
	err = tx.QueryRow(`SELECT epoch,sampled_ns,cumulative_tx,cumulative_rx FROM machine_traffic_remote_baselines WHERE member=?`, member).Scan(&epoch, &previousNS, &oldUp, &oldDown)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && epoch == reading.Epoch && reading.SampledNS == previousNS && reading.Upload == oldUp && reading.Download == oldDown {
		return nil
	}
	if err == nil && reading.SampledNS == previousNS {
		return errors.New("从机同一采样时间返回不一致读数")
	}
	if err == nil && epoch == reading.Epoch && (reading.Upload < oldUp || reading.Download < oldDown) {
		// A restored slave database can reuse its old epoch with smaller
		// cumulative counters. Hold the high-water mark; lowering it would
		// charge the same bytes again after the counters catch up.
		return nil
	}
	if err == nil && epoch == reading.Epoch && reading.SampledNS > previousNS && reading.Upload >= oldUp && reading.Download >= oldDown {
		if _, err := tx.Exec(`INSERT INTO machine_traffic_intervals(member,start_ns,end_ns,upload_bytes,download_bytes)
		 VALUES(?,?,?,?,?) ON CONFLICT(member,end_ns) DO NOTHING`, member, previousNS, reading.SampledNS, reading.Upload-oldUp, reading.Download-oldDown); err != nil {
			return err
		}
	} else if err == nil && reading.SampledNS < previousNS {
		// Out-of-order replies cannot move a durable baseline backwards.
		return nil
	}
	if _, err := tx.Exec(`INSERT INTO machine_traffic_remote_baselines(member,epoch,sampled_ns,cumulative_tx,cumulative_rx)
	 VALUES(?,?,?,?,?) ON CONFLICT(member) DO UPDATE SET epoch=excluded.epoch,sampled_ns=excluded.sampled_ns,
	 cumulative_tx=excluded.cumulative_tx,cumulative_rx=excluded.cumulative_rx`, member, reading.Epoch, reading.SampledNS, reading.Upload, reading.Download); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return chmodSQLiteFiles(path)
}

func machineTrafficOverview(path string, s *State, now time.Time) ([]machineTrafficView, error) {
	var members []string
	if s.Mesh != nil {
		for _, m := range s.Mesh.Members {
			members = append(members, m.ID)
		}
	} else if s.MeshAgent.Cluster == "" {
		members = []string{"local"}
	} else {
		return []machineTrafficView{}, nil
	}
	views := make([]machineTrafficView, 0, len(members))
	if !isSQLiteStatePath(path) {
		for _, member := range members {
			views = append(views, machineTrafficView{Member: member, Status: "no_data", Note: "数据库迁移前无法查询机器流量"})
		}
		return views, nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, _, err := openSQLiteState(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	legacyLocal := false
	if s.Mesh != nil && s.Mesh.Master != "local" {
		var present int
		err = db.QueryRow(`SELECT 1 FROM machine_traffic_baselines WHERE member='local'`).Scan(&present)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		legacyLocal = err == nil
	}
	for _, member := range members {
		queryMember := member
		if legacyLocal && s.Mesh != nil && member == s.Mesh.Master {
			queryMember = "local"
		}
		v := machineTrafficView{Member: member, Status: "unconfigured", Note: "请设置此机器的续费账期"}
		for _, p := range s.MachineTraffic {
			if p.Member != member {
				continue
			}
			anchor, err := machineTrafficAnchor(p.Start)
			if err != nil {
				return nil, err
			}
			if now.Before(anchor) || (p.EffectiveFromNS > 0 && now.UnixNano() < p.EffectiveFromNS) {
				futureStart := anchor
				if p.EffectiveFromNS > 0 && time.Unix(0, p.EffectiveFromNS).After(futureStart) {
					futureStart = time.Unix(0, p.EffectiveFromNS)
				}
				index, err := machineTrafficIndex(p, futureStart)
				if err != nil {
					return nil, err
				}
				periodStart, err := machineTrafficBoundary(p, index)
				if err != nil {
					return nil, err
				}
				next, err := machineTrafficBoundary(p, index+1)
				if err != nil {
					return nil, err
				}
				v = machineTrafficView{Member: member, AnchorStart: p.Start, Interval: p.Interval, Unit: p.Unit, PeriodStart: periodStart.Format("2006-01-02"), PeriodEnd: machineTrafficPreviousDate(next), Future: true, NextReset: futureStart.Format("2006-01-02"), Status: "future", Note: "规则尚未开始生效；此前采样不计入此规则"}
				break
			}
			index, err := machineTrafficIndex(p, now)
			if err != nil {
				return nil, err
			}
			v, err = machineTrafficPeriodView(db, member, queryMember, p, index, now)
			if err != nil {
				return nil, err
			}
			break
		}
		views = append(views, v)
	}
	return views, nil
}

func machineTrafficPeriodView(db *sql.DB, member, queryMember string, p MachineTrafficPeriod, index int64, now time.Time) (machineTrafficView, error) {
	start, err := machineTrafficBoundary(p, index)
	if err != nil {
		return machineTrafficView{}, err
	}
	end, err := machineTrafficBoundary(p, index+1)
	if err != nil {
		return machineTrafficView{}, err
	}
	v := machineTrafficView{Member: member, AnchorStart: p.Start, Interval: p.Interval, Unit: p.Unit, PeriodStart: start.Format("2006-01-02"), PeriodEnd: machineTrafficPreviousDate(end), NextReset: end.Format("2006-01-02"), Status: "no_data", Note: "该期尚无完整采样区间；未采集和跨期采样的流量未知"}
	actualStart, actualEnd := start, end
	if p.EffectiveFromNS > 0 {
		change := time.Unix(0, p.EffectiveFromNS)
		if change.After(actualStart) {
			actualStart = change
			v.EffectiveStartAt = change.Format(time.RFC3339)
		}
	}
	if p.EffectiveUntilNS > 0 {
		change := time.Unix(0, p.EffectiveUntilNS)
		if change.Before(actualEnd) {
			actualEnd = change
			v.EffectiveEndAt = change.Format(time.RFC3339)
			v.Status = "settled"
		}
	}
	if !actualEnd.After(actualStart) {
		return v, nil
	}
	spanSeconds := float64(actualEnd.Unix()-actualStart.Unix()) + float64(actualEnd.Nanosecond()-actualStart.Nanosecond())/float64(time.Second)
	if spanSeconds <= 0 {
		return v, nil
	}
	v.PeriodSeconds = int64(math.Ceil(spanSeconds))
	queryEnd := int64(math.MaxInt64)
	if actualEnd.Year() < 2262 || (actualEnd.Year() == 2262 && actualEnd.Before(time.Unix(0, math.MaxInt64))) {
		queryEnd = actualEnd.UnixNano()
	}
	var first, last, up, down, coveredNS sql.NullInt64
	err = db.QueryRow(`SELECT SUM(upload_bytes),SUM(download_bytes),SUM(end_ns-start_ns),MIN(start_ns),MAX(end_ns)
	 FROM machine_traffic_intervals WHERE member IN (?,?) AND start_ns>=? AND end_ns<=?`, member, queryMember, actualStart.UnixNano(), queryEnd).Scan(&up, &down, &coveredNS, &first, &last)
	if err != nil {
		return machineTrafficView{}, err
	}
	if up.Valid && down.Valid {
		if up.Int64 > math.MaxInt64-down.Int64 {
			return machineTrafficView{}, errors.New("机器账期流量合计溢出")
		}
		v.UploadBytes, v.DownloadBytes, v.TotalBytes = up.Int64, down.Int64, up.Int64+down.Int64
		v.CoveredSeconds = coveredNS.Int64 / int64(time.Second)
		v.CoveragePercent = 100 * float64(coveredNS.Int64) / (spanSeconds * float64(time.Second))
		if v.CoveragePercent > 100 {
			v.CoveragePercent = 100
		}
		v.FirstSampleAt = time.Unix(0, first.Int64).Format(time.RFC3339)
		v.LastSampleAt = time.Unix(0, last.Int64).Format(time.RFC3339)
		v.Note = "只累计完整落在本期的采样区间；未覆盖及跨期区间流量未知"
		if p.EffectiveUntilNS > 0 && actualEnd.Equal(time.Unix(0, p.EffectiveUntilNS)) {
			v.Status = "settled"
			v.Note = "规则变更时结算；仅累计变更前完整采样区间，边界流量未知"
		} else if !now.Before(end) && v.CoveragePercent >= 99.99 {
			v.Status = "complete"
		} else {
			v.Status = "collecting"
		}
		if v.Status == "collecting" && now.Before(actualEnd) && now.Sub(time.Unix(0, last.Int64)) > 15*time.Minute {
			v.Status = "error"
			v.Note = "最近 15 分钟没有新采样；缺口流量未知"
		}
	} else if v.Status == "settled" {
		v.Note = "规则变更时结算；该期没有完整采样区间，流量未知"
	}
	return v, nil
}
