package sql

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/gtsteffaniak/filebrowser/backend/pkg/indexing/iteminfo"
)

func (db *IndexDB) createDirectoryStateTable() error {
	collation := ""
	if db.postgres {
		collation = ` COLLATE "C"`
	}
	_, err := db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS index_directory_state (
		source TEXT%s NOT NULL, path TEXT%s NOT NULL,
		mod_time_ns BIGINT NOT NULL, direct_files BIGINT NOT NULL,
		direct_size BIGINT NOT NULL, valid BOOLEAN NOT NULL,
		PRIMARY KEY (source, path))`, collation, collation))
	return err
}

func (db *IndexDB) bind(query string) string {
	if db.postgres {
		return bindPostgres(query)
	}
	return query
}

func (db *IndexDB) LoadDirectoryStates(source string) (map[string]iteminfo.DirectoryScanState, error) {
	rows, err := db.Query(`SELECT path, mod_time_ns, direct_files, direct_size, valid FROM index_directory_state WHERE source = ?`, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := make(map[string]iteminfo.DirectoryScanState)
	for rows.Next() {
		var path string
		var state iteminfo.DirectoryScanState
		if err := rows.Scan(&path, &state.ModTimeNS, &state.DirectFiles, &state.DirectSize, &state.Valid); err != nil {
			return nil, err
		}
		states[path] = state
	}
	return states, rows.Err()
}

func (db *IndexDB) writeDirectoryStates(tx *sql.Tx, source string, items []*iteminfo.FileInfo) error {
	for _, item := range items {
		state := item.ScanState
		if state == nil {
			continue
		}
		if !state.Valid {
			// An interactive listing may have updated rows. Preserve the last
			// completed scan's baseline and reconcile it on the next quick pass.
			if _, err := tx.Exec(db.bind(`UPDATE index_directory_state SET valid = FALSE WHERE source = ? AND path = ? AND valid = TRUE`), source, item.Path); err != nil {
				return err
			}
			continue
		}
		_, err := tx.Exec(db.bind(`INSERT INTO index_directory_state
			(source, path, mod_time_ns, direct_files, direct_size, valid) VALUES (?, ?, ?, ?, ?, TRUE)
			ON CONFLICT (source, path) DO UPDATE SET mod_time_ns = excluded.mod_time_ns,
			direct_files = excluded.direct_files, direct_size = excluded.direct_size, valid = TRUE`),
			source, item.Path, state.ModTimeNS, int64(state.DirectFiles), state.DirectSize)
		if err != nil {
			return err
		}
	}
	return nil
}

func (db *IndexDB) deleteSubtreeTx(tx *sql.Tx, source, path string) error {
	where := "source = ? AND path = ?"
	args := []interface{}{source, path}
	if strings.HasSuffix(path, "/") {
		where = "source = ? AND path >= ? AND path < ?"
		args = append(args, getNextPathPrefix(path))
	}
	for _, table := range []string{"index_items", "index_directory_state"} {
		if _, err := tx.Exec(db.bind("DELETE FROM "+table+" WHERE "+where), args...); err != nil {
			return err
		}
	}
	return nil
}

// DeleteScannedSubtree is used only after absence/exclusion has been confirmed.
func (db *IndexDB) DeleteScannedSubtree(source, path string) error {
	tx, err := db.BeginTransaction()
	defer db.mu.Unlock()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := db.deleteSubtreeTx(tx, source, path); err != nil {
		return err
	}
	return tx.Commit()
}

// ReconcileDirectory commits a complete directory listing and its baseline
// atomically. Only direct children absent from this successful listing are
// removed; an unchanged directory never needs a SQL heartbeat.
func (db *IndexDB) ReconcileDirectory(source, path string, items []*iteminfo.FileInfo) ([]string, error) {
	tx, err := db.BeginTransaction()
	defer db.mu.Unlock()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(db.bind(`SELECT path FROM index_items WHERE source = ? AND parent_path = ? AND path <> ?`), source, path, path)
	if err != nil {
		return nil, err
	}
	wanted := make(map[string]bool, len(items))
	for _, item := range items {
		wanted[item.Path] = true
	}
	var removed []string
	for rows.Next() {
		var old string
		if err := rows.Scan(&old); err != nil {
			rows.Close()
			return nil, err
		}
		if !wanted[old] {
			removed = append(removed, old)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, old := range removed {
		if err := db.deleteSubtreeTx(tx, source, old); err != nil {
			return nil, err
		}
	}
	if db.postgres {
		if err := db.bulkInsertPostgresTx(tx, source, items); err != nil {
			return nil, err
		}
	} else {
		stmt, err := tx.Prepare(indexItemUpsertSQL)
		if err != nil {
			return nil, err
		}
		defer stmt.Close()
		for _, info := range items {
			if _, err := stmt.Exec(indexItemArgs(source, info, time.Now().UnixNano())...); err != nil {
				return nil, err
			}
		}
	}
	if err := db.writeDirectoryStates(tx, source, items); err != nil {
		return nil, err
	}
	return removed, tx.Commit()
}

func indexItemArgs(source string, info *iteminfo.FileInfo, now int64) []interface{} {
	return []interface{}{source, info.Path, getParentPath(info.Path), info.Name, info.Size,
		createdUnix(info.Created), info.ModTime.Unix(), info.Type, info.Type == "directory",
		info.Hidden, info.HasPreview, now, fileCountValue(info.FileCount)}
}

func (db *IndexDB) PruneDirectoryStates(source, path string) error {
	if path == "/" {
		return nil
	}
	_, err := db.Exec(`DELETE FROM index_directory_state WHERE source = ? AND path >= ? AND path < ? AND NOT EXISTS (
		SELECT 1 FROM index_items i WHERE i.source = index_directory_state.source AND i.path = index_directory_state.path AND i.is_dir = TRUE)`, source, path, getNextPathPrefix(path))
	return err
}
