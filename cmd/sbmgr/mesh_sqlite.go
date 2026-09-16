package main

import (
	"database/sql"
	"encoding/json"
	"sbmgr/internal/mesh"
)

var sqliteMeshSchema = []string{
	`CREATE TABLE IF NOT EXISTS mesh_members (
 id TEXT PRIMARY KEY, ordinal INTEGER NOT NULL,
 ssh_host TEXT NOT NULL, ssh_port INTEGER NOT NULL, ssh_user TEXT NOT NULL,
 ssh_key_path TEXT NOT NULL, app_dir TEXT NOT NULL
 ) STRICT`,
	`CREATE TABLE IF NOT EXISTS mesh_routes (
 id TEXT PRIMARY KEY, ordinal INTEGER NOT NULL,
 hops_json TEXT NOT NULL CHECK(json_valid(hops_json) AND json_type(hops_json) = 'array'),
 transports_json TEXT NOT NULL CHECK(json_valid(transports_json) AND json_type(transports_json) = 'array')
 ) STRICT`,
}

func writeSQLiteMesh(tx *sql.Tx, s *State) error {
	for _, statement := range []string{
		`CREATE TEMP TABLE IF NOT EXISTS keep_mesh_members(id TEXT PRIMARY KEY) WITHOUT ROWID`,
		`CREATE TEMP TABLE IF NOT EXISTS keep_mesh_routes(id TEXT PRIMARY KEY) WITHOUT ROWID`,
		`DELETE FROM keep_mesh_members`, `DELETE FROM keep_mesh_routes`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	if s.Mesh != nil {
		for i, m := range s.Mesh.Members {
			if _, err := tx.Exec(`INSERT INTO mesh_members VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET
    ordinal=excluded.ordinal,ssh_host=excluded.ssh_host,ssh_port=excluded.ssh_port,ssh_user=excluded.ssh_user,
    ssh_key_path=excluded.ssh_key_path,app_dir=excluded.app_dir
    WHERE (mesh_members.ordinal,mesh_members.ssh_host,mesh_members.ssh_port,mesh_members.ssh_user,mesh_members.ssh_key_path,mesh_members.app_dir)
    IS NOT (excluded.ordinal,excluded.ssh_host,excluded.ssh_port,excluded.ssh_user,excluded.ssh_key_path,excluded.app_dir)`,
				m.ID, i, m.SSHHost, m.SSHPort, m.SSHUser, m.SSHKeyPath, m.AppDir); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO keep_mesh_members VALUES(?)`, m.ID); err != nil {
				return err
			}
		}
		for i, r := range s.Mesh.Routes {
			hops, err := json.Marshal(r.Hops)
			if err != nil {
				return err
			}
			transports := r.Transports
			if transports == nil {
				transports = []mesh.Transport{}
			}
			raw, err := json.Marshal(transports)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO mesh_routes VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET
    ordinal=excluded.ordinal,hops_json=excluded.hops_json,transports_json=excluded.transports_json
    WHERE (mesh_routes.ordinal,mesh_routes.hops_json,mesh_routes.transports_json)
    IS NOT (excluded.ordinal,excluded.hops_json,excluded.transports_json)`, r.ID, i, string(hops), string(raw)); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO keep_mesh_routes VALUES(?)`, r.ID); err != nil {
				return err
			}
		}
	}
	for _, table := range []string{"mesh_members", "mesh_routes"} {
		if _, err := tx.Exec("DELETE FROM " + table + " WHERE id NOT IN (SELECT id FROM keep_" + table + ")"); err != nil {
			return err
		}
	}
	return nil
}
func readSQLiteMesh(tx *sql.Tx, s *State) error {
	if s.Mesh == nil {
		return nil
	}
	rows, err := tx.Query(`SELECT id,ssh_host,ssh_port,ssh_user,ssh_key_path,app_dir FROM mesh_members ORDER BY ordinal`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var m mesh.Member
		if err := rows.Scan(&m.ID, &m.SSHHost, &m.SSHPort, &m.SSHUser, &m.SSHKeyPath, &m.AppDir); err != nil {
			return err
		}
		s.Mesh.Members = append(s.Mesh.Members, m)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	rows, err = tx.Query(`SELECT id,hops_json,transports_json FROM mesh_routes ORDER BY ordinal`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r mesh.Route
		var hops, transports string
		if err := rows.Scan(&r.ID, &hops, &transports); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(hops), &r.Hops); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(transports), &r.Transports); err != nil {
			return err
		}
		if len(r.Transports) == 0 {
			r.Transports = nil
		}
		s.Mesh.Routes = append(s.Mesh.Routes, r)
	}
	return rows.Err()
}
