package main

import (
	"database/sql"
	"errors"
)

var sqliteDeviceSelfServiceSchema = []string{
	`CREATE TABLE IF NOT EXISTS user_device_limits (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 device_limit INTEGER NOT NULL CHECK(device_limit BETWEEN 1 AND 100)
 ) STRICT`,
	`CREATE TABLE IF NOT EXISTS device_labels (
 device_id INTEGER PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
 label TEXT NOT NULL CHECK(length(label) BETWEEN 1 AND 256)
 ) STRICT`,
}

func writeSQLiteDeviceLimit(tx *sql.Tx, id int64, limit int) error {
	if limit == 0 {
		_, err := tx.Exec(`DELETE FROM user_device_limits WHERE user_id=?`, id)
		return err
	}
	_, err := tx.Exec(`INSERT INTO user_device_limits(user_id,device_limit) VALUES(?,?)
 ON CONFLICT(user_id) DO UPDATE SET device_limit=excluded.device_limit
 WHERE user_device_limits.device_limit IS NOT excluded.device_limit`, id, limit)
	return err
}
func writeSQLiteDeviceLabel(tx *sql.Tx, id int64, label string) error {
	if label == "" {
		_, err := tx.Exec(`DELETE FROM device_labels WHERE device_id=?`, id)
		return err
	}
	_, err := tx.Exec(`INSERT INTO device_labels(device_id,label) VALUES(?,?)
 ON CONFLICT(device_id) DO UPDATE SET label=excluded.label WHERE device_labels.label IS NOT excluded.label`, id, label)
	return err
}
func readSQLiteDeviceSelfService(tx *sql.Tx, s *State, users map[int64]int, devices map[int64]sqliteDeviceLocation) error {
	var version int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version < 7 {
		return nil
	}
	rows, err := tx.Query(`SELECT user_id,device_limit FROM user_device_limits`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var limit int
		if err := rows.Scan(&id, &limit); err != nil {
			rows.Close()
			return err
		}
		i, ok := users[id]
		if !ok {
			rows.Close()
			return errors.New("设备名额的用户不存在")
		}
		s.Users[i].DeviceLimit = limit
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = tx.Query(`SELECT device_id,label FROM device_labels`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var label string
		if err := rows.Scan(&id, &label); err != nil {
			return err
		}
		loc, ok := devices[id]
		if !ok {
			return errors.New("显示名称的设备不存在")
		}
		s.Users[loc.userIndex].Devices[loc.deviceIndex].Label = label
	}
	return rows.Err()
}
