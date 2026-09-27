package main

import (
	"database/sql"
	"errors"
	"fmt"
	"sbmgr/internal/mesh"
	"slices"
	"time"
)

func analyticsRemoteCursor(path, member string) (int64, string, error) {
	db, _, err := openSQLiteState(path)
	if err != nil {
		return 0, "", err
	}
	defer db.Close()
	var cursor int64
	var epoch string
	err = db.QueryRow(`SELECT sequence,epoch FROM analytics_remote_cursor WHERE member=?`, member).Scan(&cursor, &epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", nil
	}
	return cursor, epoch, err
}

func mergeRemoteAnalytics(path string, s *State, member string, page analyticsPage) error {
	if s.Mesh == nil || !mesh.ValidID(member) || member == s.Mesh.Master || !slices.ContainsFunc(s.Mesh.Members, func(m mesh.Member) bool { return m.ID == member }) {
		return errors.New("分析数据节点不属于当前拓扑")
	}
	if len(page.Events) > 250 || page.Next < 0 || page.Head < page.Next || page.Gaps < 0 || len(page.Epoch) == 0 || len(page.Epoch) > 64 || page.First < 0 || page.Last < page.First {
		return errors.New("分析数据分页无效")
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
	var cursor int64
	var priorEpoch string
	err = tx.QueryRow(`SELECT sequence,epoch FROM analytics_remote_cursor WHERE member=?`, member).Scan(&cursor, &priorEpoch)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	resetCursor := (priorEpoch != "" && priorEpoch != page.Epoch) || page.Head < cursor
	if resetCursor {
		cursor = 0
	}
	last := cursor
	gap := 0
	if (page.Gap && page.Next > cursor) || resetCursor {
		gap = 1
	}
	for _, event := range page.Events {
		if event.Sequence <= cursor {
			continue
		}
		if event.Sequence <= last || event.Sequence > page.Next {
			return errors.New("分析序号无效")
		}
		last = event.Sequence
		if event.ObservedNS <= 0 || event.ObservedNS > time.Now().Add(5*time.Minute).UnixNano() {
			return errors.New("分析时间无效")
		}
		if event.ClosedNS > 0 && event.ClosedNS < time.Now().Add(-analyticsRetention).UnixNano() {
			continue
		}
		var userName, deviceName, uuid string
		for _, u := range s.Users {
			for _, n := range u.Nodes {
				if meshNodeIdentity(n) != event.NodeIdentity {
					continue
				}
				route, ok := meshRouteInfo(s, n.Outbound)
				if !ok || route.Entry != member {
					return errors.New("分析数据线路归属无效")
				}
				userName, deviceName, uuid = u.Name, n.Device, n.UUID
			}
		}
		if uuid == "" {
			continue
		} // revoked identity cannot reappear in history.
		var userID, deviceID, nodeID int64
		var storedUUID string
		err := tx.QueryRow(`SELECT n.user_id,n.device_id,n.id,n.uuid FROM nodes n JOIN users u ON u.id=n.user_id JOIN devices d ON d.id=n.device_id WHERE u.name_key=? AND d.name_key=? AND n.uuid=?`, sqliteNameKey(userName), sqliteNameKey(deviceName), uuid).Scan(&userID, &deviceID, &nodeID, &storedUUID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if storedUUID != uuid {
			return errors.New("分析身份不匹配")
		}
		if err := analyticsUpsert(tx, member, userID, deviceID, nodeID, event, false); err != nil {
			return err
		}
	}
	if last < page.Next {
		last = page.Next
	}
	_, err = tx.Exec(`INSERT INTO analytics_remote_cursor(member,epoch,sequence,last_sync_ns,gaps) VALUES(?,?,?,?,?) ON CONFLICT(member) DO UPDATE SET epoch=excluded.epoch,sequence=excluded.sequence,last_sync_ns=excluded.last_sync_ns,gaps=gaps+excluded.gaps`, member, page.Epoch, last, time.Now().UnixNano(), gap)
	if err != nil {
		return err
	}
	if page.Last > 0 {
		first := page.First
		if first == 0 {
			first = page.Last
		}
		var previousGaps int64
		_ = tx.QueryRow(`SELECT gaps FROM analytics_coverage WHERE member=?`, member).Scan(&previousGaps)
		_, err = tx.Exec(`INSERT INTO analytics_coverage(member,epoch,first_seen_ns,last_seen_ns,gaps,last_error) VALUES(?,?,?,?,?, '') ON CONFLICT(member) DO UPDATE SET epoch=excluded.epoch,first_seen_ns=min(first_seen_ns,excluded.first_seen_ns),last_seen_ns=max(last_seen_ns,excluded.last_seen_ns),gaps=max(gaps,excluded.gaps)+?`, member, page.Epoch, first, page.Last, page.Gaps, gap)
		if err != nil {
			return err
		}
		if gap > 0 || page.Gaps > previousGaps {
			_, err = tx.Exec(`INSERT OR IGNORE INTO analytics_gap_events(member,at_ns) VALUES(?,?)`, member, time.Now().UnixNano())
			if err != nil {
				return err
			}
		}
	}
	if err := analyticsPrune(tx, time.Now()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return chmodSQLiteFiles(path)
}

func (a *app) analyticsMeshCycle() error {
	s, err := a.loadCanonicalState()
	if err != nil {
		return err
	}
	if s.Analytics == nil || !s.Analytics.Enabled || s.Mesh == nil {
		return nil
	}
	var failures []error
	for _, member := range s.Mesh.Members {
		if member.ID == s.Mesh.Master {
			continue
		}
		cursor, epoch, err := analyticsRemoteCursor(a.statePath, member.ID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		// Catch up at most 4,000 events per maintenance cycle. Each RPC and
		// merge is bounded; a slower link retains its cursor and resumes later.
		for batch := 0; batch < 16; batch++ {
			response, err := meshExchange(a, member, meshRequest{Protocol: mesh.Protocol, Cluster: s.Mesh.ID, Member: member.ID, Operation: "analytics", AnalyticsAfter: cursor}, false)
			if err != nil {
				failures = append(failures, fmt.Errorf("分析节点 %s 同步失败: %w", member.ID, err))
				break
			}
			if response.Analytics == nil {
				failures = append(failures, errors.New("分析节点未返回数据"))
				break
			}
			if (epoch != "" && epoch != response.Analytics.Epoch) || response.Analytics.Head < cursor {
				cursor = 0
				epoch = response.Analytics.Epoch
				if batch < 15 {
					continue
				}
				break
			}
			err = a.withStateLock(func() error {
				current, err := loadState(a.statePath)
				if err != nil {
					return err
				}
				if current.Mesh == nil || current.Mesh.ID != s.Mesh.ID || current.MeshRollout != nil {
					return errors.New("分析同步期间拓扑变化")
				}
				return mergeRemoteAnalytics(a.statePath, current, member.ID, *response.Analytics)
			})
			if err != nil {
				failures = append(failures, fmt.Errorf("分析节点 %s 合并失败: %w", member.ID, err))
				break
			}
			if len(response.Analytics.Events) < 250 || response.Analytics.Next <= cursor {
				break
			}
			cursor = response.Analytics.Next
			epoch = response.Analytics.Epoch
		}
	}
	return errors.Join(failures...)
}
