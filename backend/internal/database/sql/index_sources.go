package sql

import "database/sql"

func (db *IndexDB) createIndexSourcesTable() error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS index_sources (source TEXT PRIMARY KEY, fingerprint TEXT NOT NULL)`)
	return err
}

// PrepareSource invalidates only this rebuildable source index when its scan
// policy changes, so switching modes cannot expose stale file search rows or
// retain totals calculated with a different hidden-file/size policy.
func (db *IndexDB) PrepareSource(source, fingerprint string, resetLegacy bool) (bool, error) {
	var previous string
	err := db.QueryRow("SELECT fingerprint FROM index_sources WHERE source = ?", source).Scan(&previous)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if err == nil && previous == fingerprint {
		return false, nil
	}
	// Existing releases did not record policies. Preserve a legacy full index
	// under default settings instead of forcing a large reindex on upgrade.
	changed := err == nil || resetLegacy
	tx, err := db.BeginTransaction()
	defer db.mu.Unlock()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	bind := func(q string) string {
		if db.postgres {
			return bindPostgres(q)
		}
		return q
	}
	if changed {
		if _, err := tx.Exec(bind("DELETE FROM index_directory_state WHERE source = ?"), source); err != nil {
			return false, err
		}
		if _, err := tx.Exec(bind("DELETE FROM index_items WHERE source = ?"), source); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(bind(`INSERT INTO index_sources (source, fingerprint) VALUES (?, ?)
		ON CONFLICT (source) DO UPDATE SET fingerprint = excluded.fingerprint`), source, fingerprint); err != nil {
		return false, err
	}
	return changed, tx.Commit()
}
