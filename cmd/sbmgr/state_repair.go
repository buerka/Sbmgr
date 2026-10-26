package main

import (
	"errors"
	"fmt"
)

// rehashCmd recomputes the business and control hashes after an external,
// incomplete table edit. It is the only reader that skips verification; it
// never changes business data.
func (a *app) rehashCmd(args []string) error {
	if len(args) != 0 {
		return errors.New("rehash 不接受参数")
	}
	if !isSQLiteStatePath(a.statePath) {
		return errors.New("仅支持 SQLite 状态库")
	}
	return a.withStateLock(func() error {
		db, _, err := openSQLiteState(a.statePath)
		if err != nil {
			return err
		}
		defer db.Close()
		s, err := readSQLiteStateFromOpenDBMode(a.statePath, db, false)
		if err != nil {
			return err
		}
		stateHash, controlHash, err := sqliteStateHashes(s)
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for key, value := range map[string]string{"state_hash": stateHash, "control_hash": controlHash} {
			if _, err := tx.Exec(`INSERT INTO metadata(key,value) VALUES(?,?)
    ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "状态业务哈希已重新计算")
		return nil
	})
}
