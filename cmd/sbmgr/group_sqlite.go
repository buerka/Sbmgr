package main

import (
	"database/sql"
	"encoding/json"
	"errors"
)

var sqliteGroupSchema = []string{
	`CREATE TABLE IF NOT EXISTS user_groups (
 id TEXT PRIMARY KEY, ordinal INTEGER NOT NULL, name TEXT NOT NULL,
 policy_json TEXT NOT NULL CHECK(json_valid(policy_json) AND json_type(policy_json) = 'object')
) STRICT`,
	`CREATE TABLE IF NOT EXISTS user_group_members (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 group_id TEXT NOT NULL REFERENCES user_groups(id),
 overrides_json TEXT NOT NULL CHECK(json_valid(overrides_json) AND json_type(overrides_json) = 'array')
) STRICT`,
	`CREATE INDEX IF NOT EXISTS user_group_members_group_idx ON user_group_members(group_id)`,
}

func writeSQLiteGroups(tx *sql.Tx, s *State) error {
	for i, g := range s.UserGroups {
		policy, err := json.Marshal(g.Policy)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO user_groups(id,ordinal,name,policy_json) VALUES(?,?,?,?)
ON CONFLICT(id) DO UPDATE SET ordinal=excluded.ordinal,name=excluded.name,policy_json=excluded.policy_json
WHERE user_groups.ordinal IS NOT excluded.ordinal OR user_groups.name IS NOT excluded.name OR user_groups.policy_json IS NOT excluded.policy_json`, g.ID, i, g.Name, string(policy)); err != nil {
			return err
		}
	}
	return nil
}
func writeSQLiteGroupMember(tx *sql.Tx, id int64, u *User) error {
	if u.GroupID == "" {
		_, err := tx.Exec(`DELETE FROM user_group_members WHERE user_id=?`, id)
		return err
	}
	overrides := u.GroupOverrides
	if overrides == nil {
		overrides = []string{}
	}
	raw, err := json.Marshal(overrides)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO user_group_members(user_id,group_id,overrides_json) VALUES(?,?,?)
ON CONFLICT(user_id) DO UPDATE SET group_id=excluded.group_id,overrides_json=excluded.overrides_json
WHERE user_group_members.group_id IS NOT excluded.group_id OR user_group_members.overrides_json IS NOT excluded.overrides_json`, id, u.GroupID, string(raw))
	return err
}
func pruneSQLiteGroups(tx *sql.Tx, s *State) error {
	ids := []string{}
	for _, g := range s.UserGroups {
		ids = append(ids, g.ID)
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM user_groups WHERE id NOT IN (SELECT value FROM json_each(?))`, string(raw))
	return err
}
func readSQLiteGroups(tx *sql.Tx, s *State, userIDs map[int64]int) error {
	var version int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version < 6 {
		return nil
	}
	rows, err := tx.Query(`SELECT id,name,policy_json FROM user_groups ORDER BY ordinal,id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var g UserGroup
		var raw string
		if err := rows.Scan(&g.ID, &g.Name, &raw); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal([]byte(raw), &g.Policy); err != nil {
			rows.Close()
			return err
		}
		s.UserGroups = append(s.UserGroups, g)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = tx.Query(`SELECT user_id,group_id,overrides_json FROM user_group_members`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var group, raw string
		if err := rows.Scan(&id, &group, &raw); err != nil {
			return err
		}
		index, ok := userIDs[id]
		if !ok {
			return errors.New("分组成员不存在")
		}
		s.Users[index].GroupID = group
		if err := json.Unmarshal([]byte(raw), &s.Users[index].GroupOverrides); err != nil {
			return err
		}
	}
	return rows.Err()
}
