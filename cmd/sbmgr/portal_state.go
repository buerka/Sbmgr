package main

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// Portal credentials are never included in user projections or mesh grants.
// An account awaiting activation has no salt or password hash. Only the digest
// of an invitation is persisted; its bearer token is delivered once to admin.
type PortalAccount struct {
	Enabled       bool   `json:"enabled"`
	Epoch         string `json:"epoch"`
	Salt          string `json:"salt"`
	PasswordHash  string `json:"password_hash"`
	InviteHash    string `json:"invite_hash,omitempty"`
	InviteExpires string `json:"invite_expires,omitempty"`
}

func portalDigestValid(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func validatePortalAccounts(s *State) error {
	seen := map[string]bool{}
	for _, u := range s.Users {
		p := u.Portal
		if p == nil {
			continue
		}
		if len(u.Name) > 64 || (s.Mesh == nil && s.MeshAgent.Cluster != "") {
			return errors.New("面板账号只能绑定主机上的用户，登录名称不能超过 64 字节")
		}
		if !portalDigestValid(p.Epoch) || (p.Salt == "") != (p.PasswordHash == "") ||
			(p.PasswordHash != "" && (!portalDigestValid(p.Salt) || !portalDigestValid(p.PasswordHash))) {
			return errors.New("面板账号摘要无效")
		}
		if (p.InviteHash == "") != (p.InviteExpires == "") {
			return errors.New("面板邀请记录不完整")
		}
		if p.InviteHash != "" {
			if !p.Enabled || p.PasswordHash != "" || !portalDigestValid(p.InviteHash) {
				return errors.New("面板邀请状态无效")
			}
			if _, err := time.Parse(time.RFC3339, p.InviteExpires); err != nil {
				return errors.New("面板邀请有效期无效")
			}
		}
		if seen[p.Epoch] {
			return errors.New("面板账号身份重复")
		}
		seen[p.Epoch] = true
	}
	return nil
}

const sqlitePortalSchema = `CREATE TABLE IF NOT EXISTS portal_accounts (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 enabled INTEGER NOT NULL CHECK(enabled IN (0, 1)),
 epoch TEXT NOT NULL UNIQUE CHECK(length(epoch) = 64),
 salt TEXT NOT NULL CHECK(length(salt) IN (0, 64)),
 password_hash TEXT NOT NULL CHECK(length(password_hash) = length(salt)),
 invite_hash TEXT NOT NULL CHECK(length(invite_hash) IN (0, 64)),
 invite_expires TEXT NOT NULL,
 CHECK((invite_hash = '' AND invite_expires = '') OR
  (invite_hash != '' AND invite_expires != '' AND enabled = 1 AND password_hash = ''))
) STRICT`

func writeSQLitePortalAccount(tx *sql.Tx, userID int64, p *PortalAccount) error {
	if p == nil {
		_, err := tx.Exec(`DELETE FROM portal_accounts WHERE user_id = ?`, userID)
		return err
	}
	_, err := tx.Exec(`INSERT INTO portal_accounts(user_id, enabled, epoch, salt, password_hash, invite_hash, invite_expires) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(user_id) DO UPDATE SET enabled=excluded.enabled, epoch=excluded.epoch, salt=excluded.salt,
 password_hash=excluded.password_hash, invite_hash=excluded.invite_hash, invite_expires=excluded.invite_expires
 WHERE portal_accounts.enabled IS NOT excluded.enabled OR portal_accounts.epoch IS NOT excluded.epoch
 OR portal_accounts.salt IS NOT excluded.salt OR portal_accounts.password_hash IS NOT excluded.password_hash
 OR portal_accounts.invite_hash IS NOT excluded.invite_hash OR portal_accounts.invite_expires IS NOT excluded.invite_expires`,
		userID, boolInt(p.Enabled), p.Epoch, p.Salt, p.PasswordHash, p.InviteHash, p.InviteExpires)
	return err
}

func readSQLitePortalAccounts(tx *sql.Tx, state *State, userIDs map[int64]int) error {
	var version int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	// Historical snapshots have no portal credentials and remain restorable.
	if version < 5 {
		return nil
	}
	rows, err := tx.Query(`SELECT user_id, enabled, epoch, salt, password_hash, invite_hash, invite_expires FROM portal_accounts`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var userID int64
		var enabled int
		var p PortalAccount
		if err := rows.Scan(&userID, &enabled, &p.Epoch, &p.Salt, &p.PasswordHash, &p.InviteHash, &p.InviteExpires); err != nil {
			return err
		}
		index, ok := userIDs[userID]
		if !ok {
			return errors.New("面板账号绑定的用户不存在")
		}
		p.Enabled = enabled == 1
		state.Users[index].Portal = &p
	}
	return rows.Err()
}
