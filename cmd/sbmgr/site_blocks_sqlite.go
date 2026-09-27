package main

import (
	"database/sql"
	"fmt"
)

const sqlitePersonalSiteBlocksSchema = `CREATE TABLE IF NOT EXISTS user_site_blocks (
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	domain TEXT NOT NULL,
	PRIMARY KEY(user_id, domain)
) STRICT`

func writeSQLitePersonalSiteBlocks(tx *sql.Tx, userID int64, domains []string) error {
	// The primary key makes repeated state saves incremental. Only entries no
	// longer in the desired set are removed; existing rows retain their identity.
	for _, domain := range domains {
		if _, err := tx.Exec(`INSERT INTO user_site_blocks(user_id, domain) VALUES(?, ?) ON CONFLICT DO NOTHING`, userID, domain); err != nil {
			return fmt.Errorf("保存个人网站限制: %w", err)
		}
	}
	if len(domains) == 0 {
		_, err := tx.Exec(`DELETE FROM user_site_blocks WHERE user_id = ?`, userID)
		return err
	}
	args := make([]any, 0, len(domains)+1)
	args = append(args, userID)
	query := `DELETE FROM user_site_blocks WHERE user_id = ? AND domain NOT IN (`
	for i, domain := range domains {
		if i > 0 {
			query += ","
		}
		query += "?"
		args = append(args, domain)
	}
	query += ")"
	_, err := tx.Exec(query, args...)
	return err
}

func readSQLitePersonalSiteBlocks(tx *sql.Tx, state *State, userIDs map[int64]int) error {
	var version int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version < 10 {
		return nil
	}
	rows, err := tx.Query(`SELECT user_id, domain FROM user_site_blocks ORDER BY user_id, domain`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var userID int64
		var domain string
		if err := rows.Scan(&userID, &domain); err != nil {
			return err
		}
		index, ok := userIDs[userID]
		if !ok {
			return fmt.Errorf("个人网站限制引用不存在的用户")
		}
		state.Users[index].PersonalBlockedDomains = append(state.Users[index].PersonalBlockedDomains, domain)
	}
	return rows.Err()
}
