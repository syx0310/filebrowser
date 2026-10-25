package sql

func fileCountValue(count *uint64) interface{} {
	if count == nil {
		return nil
	}
	return int64(*count)
}

func (db *IndexDB) ensureFileCountColumn() error {
	rows, err := db.Query("PRAGMA table_info(index_items)")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var def interface{}
		if err := rows.Scan(&cid, &name, &typ, &notNull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		found = found || name == "file_count"
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !found {
		_, err = db.Exec("ALTER TABLE index_items ADD COLUMN file_count INTEGER")
	}
	return err
}
