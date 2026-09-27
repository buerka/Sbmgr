package main

import (
	"database/sql"
	"errors"
	"net"
	"strings"
	"time"
)

const analyticsRetention = 30 * 24 * time.Hour
const analyticsEventLimit = 10000

var sqliteAnalyticsSchema = []string{
	`CREATE TABLE IF NOT EXISTS analytics_connections (
 member TEXT NOT NULL, stream_id TEXT NOT NULL, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
 node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
 domain TEXT NOT NULL, started_ns INTEGER NOT NULL, closed_ns INTEGER NOT NULL DEFAULT 0,
 upload_bytes INTEGER NOT NULL CHECK(upload_bytes>=0), download_bytes INTEGER NOT NULL CHECK(download_bytes>=0),
 last_seen_ns INTEGER NOT NULL, abandoned INTEGER NOT NULL DEFAULT 0 CHECK(abandoned IN (0,1,2)), PRIMARY KEY(member,stream_id)) STRICT`,
	`CREATE INDEX IF NOT EXISTS analytics_connections_user_time_idx ON analytics_connections(user_id,last_seen_ns DESC)`,
	`CREATE INDEX IF NOT EXISTS analytics_connections_device_time_idx ON analytics_connections(device_id,last_seen_ns DESC)`,
	`CREATE TABLE IF NOT EXISTS analytics_daily (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
 domain TEXT NOT NULL, day TEXT NOT NULL,
 upload_bytes INTEGER NOT NULL CHECK(upload_bytes>=0), download_bytes INTEGER NOT NULL CHECK(download_bytes>=0),
 connections INTEGER NOT NULL CHECK(connections>=0), last_seen_ns INTEGER NOT NULL,
 PRIMARY KEY(user_id,device_id,domain,day)) STRICT`,
	`CREATE INDEX IF NOT EXISTS analytics_daily_user_day_idx ON analytics_daily(user_id,day)`,
	`CREATE INDEX IF NOT EXISTS analytics_daily_device_day_idx ON analytics_daily(device_id,day)`,
	`CREATE TABLE IF NOT EXISTS analytics_coverage (
 member TEXT PRIMARY KEY, epoch TEXT NOT NULL, first_seen_ns INTEGER NOT NULL, last_seen_ns INTEGER NOT NULL,
 gaps INTEGER NOT NULL CHECK(gaps>=0), last_error TEXT NOT NULL DEFAULT '') STRICT`,
	`CREATE TABLE IF NOT EXISTS analytics_gap_events (
 member TEXT NOT NULL, at_ns INTEGER NOT NULL, PRIMARY KEY(member,at_ns)) STRICT`,
	`CREATE TABLE IF NOT EXISTS analytics_events (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT, member TEXT NOT NULL, stream_id TEXT NOT NULL,
 node_identity TEXT NOT NULL, domain TEXT NOT NULL, destination TEXT NOT NULL,
 started_ns INTEGER NOT NULL, closed_ns INTEGER NOT NULL, upload_bytes INTEGER NOT NULL,
 download_bytes INTEGER NOT NULL, observed_ns INTEGER NOT NULL, abandoned INTEGER NOT NULL DEFAULT 0 CHECK(abandoned IN (0,1))) STRICT`,
	`CREATE INDEX IF NOT EXISTS analytics_events_member_seq_idx ON analytics_events(member,sequence)`,
	`CREATE TABLE IF NOT EXISTS analytics_remote_cursor (
 member TEXT PRIMARY KEY, epoch TEXT NOT NULL, sequence INTEGER NOT NULL CHECK(sequence>=0), last_sync_ns INTEGER NOT NULL,
 gaps INTEGER NOT NULL CHECK(gaps>=0)) STRICT`,
}

type analyticsEvent struct {
	Sequence     int64  `json:"sequence"`
	ID           string `json:"id"`
	NodeIdentity string `json:"node_identity"`
	Domain       string `json:"domain"`
	Destination  string `json:"destination"`
	StartedNS    int64  `json:"started_ns"`
	ClosedNS     int64  `json:"closed_ns"`
	Upload       int64  `json:"upload"`
	Download     int64  `json:"download"`
	ObservedNS   int64  `json:"observed_ns"`
	Abandoned    bool   `json:"abandoned,omitempty"`
}

type analyticsPage struct {
	Events []analyticsEvent `json:"events"`
	Epoch  string           `json:"epoch"`
	Head   int64            `json:"head"`
	Next   int64            `json:"next"`
	Gap    bool             `json:"gap"`
	First  int64            `json:"first"`
	Last   int64            `json:"last"`
	Gaps   int64            `json:"gaps"`
}

func analyticsDomain(value string) (string, error) {
	value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	if ip := net.ParseIP(value); ip != nil {
		return ip.String(), nil
	}
	if len(value) == 0 || len(value) > 253 || strings.ContainsAny(value, "/:?#@\\ \t\r\n\x00") {
		return "", errors.New("连接域名无效")
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("连接域名无效")
		}
		for _, c := range label {
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
				return "", errors.New("连接域名无效")
			}
		}
	}
	return value, nil
}

func analyticsLocalMember(s *State) string { return machineTrafficLocalMember(s) }

func (a *app) ingestAnalytics(samples []ConnectionSample, reset bool) error {
	if len(samples) > connectionStreamLimit {
		return errors.New("分析批次过大")
	}
	return a.withStateLock(func() error {
		s, err := loadState(a.statePath)
		if err != nil {
			return err
		}
		if s.Analytics == nil || !s.Analytics.Enabled {
			return nil
		}
		if !isSQLiteStatePath(a.statePath) {
			return errors.New("连接分析需要 SQLite 状态数据库")
		}
		db, _, err := openSQLiteState(a.statePath)
		if err != nil {
			return err
		}
		defer db.Close()
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		member := analyticsLocalMember(s)
		now := time.Now()
		if err := analyticsMarkCoverage(tx, member, now, reset); err != nil {
			return err
		}
		if reset {
			if _, err := tx.Exec(`UPDATE analytics_connections SET abandoned=2 WHERE member=? AND closed_ns=0 AND abandoned=0`, member); err != nil {
				return err
			}
		}
		for _, sample := range samples {
			if sample.ID == "" || len(sample.ID) > 160 || sample.Upload < 0 || sample.Download < 0 || sample.StartedAt.IsZero() || sample.StartedAt.After(now.Add(5*time.Minute)) {
				return errors.New("连接样本无效")
			}
			if !sample.ClosedAt.IsZero() && sample.ClosedAt.Before(now.Add(-analyticsRetention)) {
				continue
			}
			domain, err := analyticsDomain(sample.Domain)
			if err != nil {
				continue
			}
			var userID, deviceID, nodeID int64
			var uuid string
			err = tx.QueryRow(`SELECT n.user_id,n.device_id,n.id,n.uuid FROM nodes n WHERE n.auth_user=?`, sample.AuthUser).Scan(&userID, &deviceID, &nodeID, &uuid)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			event := analyticsEvent{ID: sample.ID, NodeIdentity: meshNodeIdentity(Node{UUID: uuid}), Domain: domain, StartedNS: sample.StartedAt.UnixNano(), Upload: sample.Upload, Download: sample.Download, ObservedNS: now.UnixNano()}
			if !sample.ClosedAt.IsZero() {
				event.ClosedNS = sample.ClosedAt.UnixNano()
			}
			if err := analyticsUpsert(tx, member, userID, deviceID, nodeID, event, true); err != nil {
				return err
			}
		}
		if reset {
			rows, err := tx.Query(`SELECT c.stream_id,n.uuid,c.domain,c.started_ns,c.upload_bytes,c.download_bytes FROM analytics_connections c JOIN nodes n ON n.id=c.node_id WHERE c.member=? AND c.abandoned=2`, member)
			if err != nil {
				return err
			}
			var abandoned []analyticsEvent
			for rows.Next() {
				var event analyticsEvent
				var uuid string
				if err := rows.Scan(&event.ID, &uuid, &event.Domain, &event.StartedNS, &event.Upload, &event.Download); err != nil {
					rows.Close()
					return err
				}
				event.NodeIdentity = meshNodeIdentity(Node{UUID: uuid})
				event.ObservedNS, event.Abandoned = now.UnixNano(), true
				abandoned = append(abandoned, event)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
			for _, event := range abandoned {
				if _, err := tx.Exec(`INSERT INTO analytics_events(member,stream_id,node_identity,domain,destination,started_ns,closed_ns,upload_bytes,download_bytes,observed_ns,abandoned) VALUES(?,?,?,?,?,?,?,?,?,?,1)`, member, event.ID, event.NodeIdentity, event.Domain, "", event.StartedNS, 0, event.Upload, event.Download, event.ObservedNS); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(`UPDATE analytics_connections SET abandoned=1 WHERE member=? AND abandoned=2`, member); err != nil {
				return err
			}
		}
		if err := analyticsPrune(tx, now); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return chmodSQLiteFiles(a.statePath)
	})
}

func analyticsMarkCoverage(tx *sql.Tx, member string, at time.Time, reset bool) error {
	ns := at.UnixNano()
	gap := 0
	epoch, err := machineTrafficEpoch()
	if err != nil {
		return err
	}
	if reset {
		var prior int64
		err := tx.QueryRow(`SELECT last_seen_ns FROM analytics_coverage WHERE member=?`, member).Scan(&prior)
		if err == nil && prior > 0 {
			gap = 1
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO analytics_coverage(member,epoch,first_seen_ns,last_seen_ns,gaps,last_error) VALUES(?,?,?,?,?, '')
 ON CONFLICT(member) DO UPDATE SET last_seen_ns=max(last_seen_ns,excluded.last_seen_ns),gaps=gaps+excluded.gaps,last_error=''`, member, epoch, ns, ns, gap)
	if err == nil && gap > 0 {
		_, err = tx.Exec(`INSERT OR IGNORE INTO analytics_gap_events(member,at_ns) VALUES(?,?)`, member, ns)
	}
	return err
}

func analyticsUpsert(tx *sql.Tx, member string, userID, deviceID, nodeID int64, e analyticsEvent, logEvent bool) error {
	if e.Upload < 0 || e.Download < 0 || e.StartedNS <= 0 || e.ObservedNS <= 0 || len(e.ID) == 0 || len(e.ID) > 160 {
		return errors.New("连接样本越界")
	}
	domain, err := analyticsDomain(e.Domain)
	if err != nil {
		return err
	}
	e.Domain = domain
	if e.Abandoned {
		_, err := tx.Exec(`UPDATE analytics_connections SET abandoned=1 WHERE member=? AND stream_id=? AND user_id=? AND device_id=? AND node_id=? AND closed_ns=0`, member, e.ID, userID, deviceID, nodeID)
		return err
	}
	var oldUser, oldDevice, oldNode, up, down, oldClosed int64
	var oldDomain string
	err = tx.QueryRow(`SELECT user_id,device_id,node_id,domain,upload_bytes,download_bytes,closed_ns FROM analytics_connections WHERE member=? AND stream_id=?`, member, e.ID).Scan(&oldUser, &oldDevice, &oldNode, &oldDomain, &up, &down, &oldClosed)
	isNew := errors.Is(err, sql.ErrNoRows)
	if err != nil && !isNew {
		return err
	}
	if !isNew && (oldUser != userID || oldDevice != deviceID || oldNode != nodeID) {
		return errors.New("连接归属变化")
	}
	if !isNew {
		domain = oldDomain
		e.Domain = oldDomain
	}
	if !isNew && (e.Upload < up || e.Download < down) {
		if _, err := tx.Exec(`UPDATE analytics_connections SET abandoned=0 WHERE member=? AND stream_id=? AND abandoned=2`, member, e.ID); err != nil {
			return err
		}
		return nil
	} // stale replay
	deltaUp, deltaDown := e.Upload-up, e.Download-down
	if isNew {
		_, err = tx.Exec(`INSERT INTO analytics_connections(member,stream_id,user_id,device_id,node_id,domain,started_ns,closed_ns,upload_bytes,download_bytes,last_seen_ns) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, member, e.ID, userID, deviceID, nodeID, domain, e.StartedNS, e.ClosedNS, e.Upload, e.Download, e.ObservedNS)
	} else {
		_, err = tx.Exec(`UPDATE analytics_connections SET upload_bytes=?,download_bytes=?,closed_ns=max(closed_ns,?),last_seen_ns=max(last_seen_ns,?),abandoned=0 WHERE member=? AND stream_id=?`, e.Upload, e.Download, e.ClosedNS, e.ObservedNS, member, e.ID)
	}
	if err != nil {
		return err
	}
	if deltaUp > 0 || deltaDown > 0 || isNew {
		day := time.Unix(0, e.ObservedNS).UTC().Format("2006-01-02")
		count := 0
		if isNew {
			count = 1
		}
		_, err = tx.Exec(`INSERT INTO analytics_daily(user_id,device_id,domain,day,upload_bytes,download_bytes,connections,last_seen_ns) VALUES(?,?,?,?,?,?,?,?)
   ON CONFLICT(user_id,device_id,domain,day) DO UPDATE SET upload_bytes=upload_bytes+excluded.upload_bytes,
   download_bytes=download_bytes+excluded.download_bytes,connections=connections+excluded.connections,last_seen_ns=max(last_seen_ns,excluded.last_seen_ns)`, userID, deviceID, domain, day, deltaUp, deltaDown, count, e.ObservedNS)
		if err != nil {
			return err
		}
	}
	if logEvent && (isNew || deltaUp > 0 || deltaDown > 0 || (e.ClosedNS > 0 && oldClosed == 0)) {
		_, err = tx.Exec(`INSERT INTO analytics_events(member,stream_id,node_identity,domain,destination,started_ns,closed_ns,upload_bytes,download_bytes,observed_ns) VALUES(?,?,?,?,?,?,?,?,?,?)`, member, e.ID, e.NodeIdentity, domain, e.Destination, e.StartedNS, e.ClosedNS, e.Upload, e.Download, e.ObservedNS)
		if err != nil {
			return err
		}
	}
	return nil
}

func analyticsPrune(tx *sql.Tx, now time.Time) error {
	cutoff := now.Add(-analyticsRetention).UnixNano()
	for _, query := range []string{
		`DELETE FROM analytics_connections WHERE closed_ns>0 AND closed_ns<?`,
		`DELETE FROM analytics_connections WHERE abandoned=1 AND last_seen_ns<?`,
		`DELETE FROM analytics_daily WHERE day<?`,
		`DELETE FROM analytics_events WHERE observed_ns<?`,
		`DELETE FROM analytics_gap_events WHERE at_ns<?`,
	} {
		arg := any(cutoff)
		if strings.Contains(query, "day<") {
			arg = now.Add(-analyticsRetention).UTC().Format("2006-01-02")
		}
		if _, err := tx.Exec(query, arg); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`DELETE FROM analytics_events WHERE sequence <= (SELECT coalesce(max(sequence),0)-? FROM analytics_events)`, analyticsEventLimit)
	return err
}

func analyticsReadEvents(path, member string, after int64) (analyticsPage, error) {
	var page analyticsPage
	db, _, err := openSQLiteState(path)
	if err != nil {
		return page, err
	}
	defer db.Close()
	if err := db.QueryRow(`SELECT coalesce(min(sequence),0),coalesce(max(sequence),0) FROM analytics_events WHERE member=?`, member).Scan(&page.First, &page.Head); err != nil {
		return page, err
	}
	if after > page.Head {
		after = 0
		page.Gap = true
	}
	page.Gap = page.Gap || page.First > after+1
	rows, err := db.Query(`SELECT sequence,stream_id,node_identity,domain,destination,started_ns,closed_ns,upload_bytes,download_bytes,observed_ns,abandoned FROM analytics_events WHERE member=? AND sequence>? ORDER BY sequence LIMIT 250`, member, after)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var e analyticsEvent
		if err := rows.Scan(&e.Sequence, &e.ID, &e.NodeIdentity, &e.Domain, &e.Destination, &e.StartedNS, &e.ClosedNS, &e.Upload, &e.Download, &e.ObservedNS, &e.Abandoned); err != nil {
			return page, err
		}
		page.Events = append(page.Events, e)
		page.Next = e.Sequence
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if page.Next == 0 {
		page.Next = after
	}
	var first, last, gaps int64
	err = db.QueryRow(`SELECT epoch,first_seen_ns,last_seen_ns,gaps FROM analytics_coverage WHERE member=?`, member).Scan(&page.Epoch, &first, &last, &gaps)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return page, err
	}
	page.First, page.Last, page.Gaps = first, last, gaps
	return page, nil
}
