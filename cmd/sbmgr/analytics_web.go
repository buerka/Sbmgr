package main

import (
	"database/sql"
	"errors"
	"sbmgr/internal/mesh"
	"strings"
	"time"
)

type analyticsInput struct {
	User   string `json:"user,omitempty"`
	Device string `json:"device,omitempty"`
	Days   int    `json:"days"`
	Page   int    `json:"page,omitempty"`
	Search string `json:"search,omitempty"`
	Sort   string `json:"sort,omitempty"`
}

type analyticsCount struct {
	Upload      int64 `json:"upload"`
	Download    int64 `json:"download"`
	Connections int64 `json:"connections"`
	Domains     int64 `json:"domains"`
}

type analyticsCoverage struct {
	Status    string `json:"status"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
	Gaps      int64  `json:"gaps"`
	Note      string `json:"note"`
}

func (a *app) analyticsReply(s *State, owner *User, body []byte) webReply {
	var input analyticsInput
	if webDecode(body, &input) != nil || (input.Days != 1 && input.Days != 7 && input.Days != 30) || input.Page < 0 || input.Page > 10000 || len(input.Search) > 100 || len(input.Device) > 256 || len(input.User) > 256 || (input.Sort != "" && input.Sort != "traffic" && input.Sort != "connections") {
		return webError(400, "分析查询参数无效")
	}
	if owner != nil {
		if input.User != "" && input.User != owner.Name {
			return webError(403, "只能查看本人数据")
		}
		input.User = owner.Name
	}
	if input.User == "" {
		return webError(400, "请选择用户")
	}
	u := findUser(s, input.User)
	if u == nil {
		return webError(404, "用户不存在")
	}
	if input.Device != "" && findDevice(u, input.Device) == nil {
		return webError(404, "设备不属于该用户")
	}
	result, err := queryConnectionAnalytics(a.statePath, s, u, input)
	if err != nil {
		return webError(503, "分析数据暂不可用")
	}
	return webJSON(200, result)
}

func queryConnectionAnalytics(path string, s *State, u *User, in analyticsInput) (map[string]any, error) {
	now := time.Now().UTC()
	to := now.Format("2006-01-02")
	fromDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -in.Days+1)
	from := fromDate.Format("2006-01-02")
	empty := func() map[string]any {
		return map[string]any{"user": u.Name, "device": in.Device, "days": in.Days, "from": from, "to": to, "coverage": analyticsCoverage{Status: "unavailable", Note: "尚无采集数据"}, "totals": analyticsCount{}, "devices": []any{}, "series": []any{}, "domains": []any{}, "pagination": map[string]any{"page": max(1, in.Page), "page_size": 50, "total": 0}, "recent": []any{}}
	}
	result := empty()
	if s.Analytics == nil || !s.Analytics.Enabled || !isSQLiteStatePath(path) {
		return result, nil
	}
	db, _, err := openSQLiteState(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var userID int64
	if err := db.QueryRow(`SELECT id FROM users WHERE name_key=?`, sqliteNameKey(u.Name)).Scan(&userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return result, nil
		}
		return nil, err
	}
	var deviceID int64
	deviceFilter := ""
	args := []any{userID, from, to}
	if in.Device != "" {
		if err := db.QueryRow(`SELECT id FROM devices WHERE user_id=? AND name_key=?`, userID, sqliteNameKey(in.Device)).Scan(&deviceID); err != nil {
			return nil, err
		}
		deviceFilter = " AND d.device_id=?"
		args = append(args, deviceID)
	}
	var totals analyticsCount
	q := `SELECT coalesce(sum(d.upload_bytes),0),coalesce(sum(d.download_bytes),0),coalesce(sum(d.connections),0),count(DISTINCT d.domain) FROM analytics_daily d WHERE d.user_id=? AND d.day BETWEEN ? AND ?` + deviceFilter
	if err := db.QueryRow(q, args...).Scan(&totals.Upload, &totals.Download, &totals.Connections, &totals.Domains); err != nil {
		return nil, err
	}
	result["totals"] = totals
	rows, err := db.Query(`SELECT d.day,coalesce(sum(d.upload_bytes),0),coalesce(sum(d.download_bytes),0),coalesce(sum(d.connections),0) FROM analytics_daily d WHERE d.user_id=? AND d.day BETWEEN ? AND ?`+deviceFilter+` GROUP BY d.day`, args...)
	if err != nil {
		return nil, err
	}
	daily := map[string]analyticsCount{}
	for rows.Next() {
		var date string
		var c analyticsCount
		if err := rows.Scan(&date, &c.Upload, &c.Download, &c.Connections); err != nil {
			rows.Close()
			return nil, err
		}
		daily[date] = c
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	series := make([]any, 0, in.Days)
	for day := 0; day < in.Days; day++ {
		date := fromDate.AddDate(0, 0, day).Format("2006-01-02")
		c := daily[date]
		series = append(series, map[string]any{"date": date, "upload": c.Upload, "download": c.Download, "connections": c.Connections})
	}
	result["series"] = series
	rows, err = db.Query(`SELECT v.name,coalesce(sum(d.upload_bytes),0),coalesce(sum(d.download_bytes),0),coalesce(sum(d.connections),0),coalesce(max(d.last_seen_ns),0) FROM devices v LEFT JOIN analytics_daily d ON d.device_id=v.id AND d.day BETWEEN ? AND ? WHERE v.user_id=? GROUP BY v.id ORDER BY v.ordinal,v.id`, from, to, userID)
	if err != nil {
		return nil, err
	}
	devices := []any{}
	for rows.Next() {
		var name string
		var up, down, count, last int64
		if err := rows.Scan(&name, &up, &down, &count, &last); err != nil {
			rows.Close()
			return nil, err
		}
		label := name
		if d := findDevice(u, name); d != nil {
			label = deviceDisplayName(*d)
		}
		devices = append(devices, map[string]any{"name": name, "label": label, "upload": up, "download": down, "connections": count, "last_seen": analyticsTime(last)})
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	result["devices"] = devices
	search := strings.ToLower(strings.TrimSpace(in.Search))
	where := ` WHERE d.user_id=? AND d.day BETWEEN ? AND ?` + deviceFilter
	domainArgs := append([]any(nil), args...)
	if search != "" {
		where += ` AND d.domain LIKE ? ESCAPE '\'`
		domainArgs = append(domainArgs, "%"+strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(search)+"%")
	}
	var total int
	if err := db.QueryRow(`SELECT count(DISTINCT d.domain) FROM analytics_daily d`+where, domainArgs...).Scan(&total); err != nil {
		return nil, err
	}
	page := max(1, in.Page)
	result["pagination"] = map[string]any{"page": page, "page_size": 50, "total": total}
	order := "traffic"
	if in.Sort == "connections" {
		order = "connections"
	}
	orderSQL := ` ORDER BY (sum(d.upload_bytes)+sum(d.download_bytes)) DESC,d.domain`
	if order == "connections" {
		orderSQL = ` ORDER BY sum(d.connections) DESC,d.domain`
	}
	domainRows, err := db.Query(`SELECT d.domain,sum(d.upload_bytes),sum(d.download_bytes),sum(d.connections),max(d.last_seen_ns) FROM analytics_daily d`+where+` GROUP BY d.domain`+orderSQL+` LIMIT 50 OFFSET ?`, append(domainArgs, (page-1)*50)...)
	if err != nil {
		return nil, err
	}
	domains := []any{}
	for domainRows.Next() {
		var domain string
		var up, down, count, last int64
		if err := domainRows.Scan(&domain, &up, &down, &count, &last); err != nil {
			domainRows.Close()
			return nil, err
		}
		domains = append(domains, map[string]any{"domain": domain, "upload": up, "download": down, "connections": count, "last_seen": analyticsTime(last)})
	}
	if err := domainRows.Close(); err != nil {
		return nil, err
	}
	result["domains"] = domains
	recentArgs := []any{userID, fromDate.UnixNano()}
	recentWhere := ` WHERE c.user_id=? AND c.last_seen_ns>=?`
	if deviceID > 0 {
		recentWhere += ` AND c.device_id=?`
		recentArgs = append(recentArgs, deviceID)
	}
	if search != "" {
		recentWhere += ` AND c.domain LIKE ? ESCAPE '\'`
		recentArgs = append(recentArgs, "%"+strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(search)+"%")
	}
	recentRows, err := db.Query(`SELECT c.domain,v.name,c.started_ns,c.closed_ns,c.upload_bytes,c.download_bytes,c.abandoned FROM analytics_connections c JOIN devices v ON v.id=c.device_id`+recentWhere+` ORDER BY c.last_seen_ns DESC LIMIT 50`, recentArgs...)
	if err != nil {
		return nil, err
	}
	recent := []any{}
	for recentRows.Next() {
		var domain, device string
		var started, closed, up, down, abandoned int64
		if err := recentRows.Scan(&domain, &device, &started, &closed, &up, &down, &abandoned); err != nil {
			recentRows.Close()
			return nil, err
		}
		label := device
		if d := findDevice(u, device); d != nil {
			label = deviceDisplayName(*d)
		}
		status := "active"
		if closed > 0 {
			status = "closed"
		} else if abandoned > 0 {
			status = "unknown"
		}
		recent = append(recent, map[string]any{"domain": domain, "device": device, "label": label, "started_at": analyticsTime(started), "closed_at": analyticsTime(closed), "upload": up, "download": down, "status": status})
	}
	if err := recentRows.Close(); err != nil {
		return nil, err
	}
	result["recent"] = recent
	var firstSeen, lastSeen, effectiveFirst, gaps int64
	expected := analyticsExpectedMembers(s, u)
	seen := 0
	for _, member := range expected {
		var first, last, memberGaps int64
		err = db.QueryRow(`SELECT first_seen_ns,last_seen_ns,gaps FROM analytics_coverage WHERE member=?`, member).Scan(&first, &last, &memberGaps)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		seen++
		if firstSeen == 0 || first < firstSeen {
			firstSeen = first
		}
		if first > effectiveFirst {
			effectiveFirst = first
		}
		if lastSeen == 0 || last < lastSeen {
			lastSeen = last
		}
		var windowGaps int64
		if err := db.QueryRow(`SELECT count(*) FROM analytics_gap_events WHERE member=? AND at_ns>=?`, member, fromDate.UnixNano()).Scan(&windowGaps); err != nil {
			return nil, err
		}
		gaps += windowGaps
		_ = memberGaps // durable lifetime count remains available for diagnostics.
	}
	coverage := analyticsCoverage{FirstSeen: analyticsTime(firstSeen), LastSeen: analyticsTime(lastSeen), Gaps: gaps}
	switch {
	case seen == 0:
		coverage.Status = "unavailable"
		coverage.Note = "尚无采集数据"
	case seen < len(expected) || gaps > 0 || effectiveFirst > fromDate.UnixNano() || now.Sub(time.Unix(0, lastSeen)) > 5*time.Minute:
		coverage.Status = "partial"
		coverage.Note = "采集存在中断，统计可能不完整"
	default:
		coverage.Status = "collecting"
		coverage.Note = "仅统计启用后采集的连接"
	}
	result["coverage"] = coverage
	return result, nil
}

func analyticsTime(ns int64) string {
	if ns <= 0 {
		return ""
	}
	return time.Unix(0, ns).UTC().Format(time.RFC3339Nano)
}

func analyticsExpectedMembers(s *State, u *User) []string {
	if s.Mesh == nil {
		return []string{analyticsLocalMember(s)}
	}
	seen := map[string]bool{}
	for _, n := range u.Nodes {
		member := s.Mesh.Master
		for _, route := range s.Mesh.Routes {
			if mesh.RouteTag(route.ID) == n.Outbound {
				member = s.Mesh.Entry(route)
				break
			}
		}
		seen[member] = true
	}
	if len(seen) == 0 {
		seen[s.Mesh.Master] = true
	}
	members := make([]string, 0, len(seen))
	for member := range seen {
		members = append(members, member)
	}
	return members
}
