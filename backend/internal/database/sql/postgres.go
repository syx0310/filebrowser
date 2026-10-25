package sql

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gtsteffaniak/filebrowser/backend/pkg/indexing/iteminfo"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// NewPostgresIndexDB opens only the rebuildable file index. User data stays in
// the application's existing database. No filesystem paths or DSNs are logged.
func NewPostgresIndexDB(dsn string, batchSize int) (*IndexDB, bool, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, false, fmt.Errorf("open PostgreSQL index: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxIdleTime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, false, fmt.Errorf("connect PostgreSQL index: %w", err)
	}
	var exists bool
	if err := db.QueryRowContext(ctx, "SELECT to_regclass('index_items') IS NOT NULL").Scan(&exists); err != nil {
		db.Close()
		return nil, false, err
	}
	if batchSize <= 0 {
		batchSize = 1000
	}
	idx := &IndexDB{TempDB: &TempDB{db: db, postgres: true, BatchSize: batchSize}}
	if err := idx.CreateIndexTable(); err != nil {
		db.Close()
		return nil, false, fmt.Errorf("initialize PostgreSQL index: %w", err)
	}
	return idx, !exists, nil
}

func (db *IndexDB) createPostgresIndexTable() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS index_items (
			source TEXT COLLATE "C" NOT NULL,
			path TEXT COLLATE "C" NOT NULL,
			parent_path TEXT COLLATE "C" NOT NULL,
			name TEXT NOT NULL,
			size BIGINT NOT NULL,
			created_time BIGINT NOT NULL DEFAULT 0,
			mod_time BIGINT NOT NULL,
			type TEXT NOT NULL,
			is_dir BOOLEAN NOT NULL,
			is_hidden BOOLEAN NOT NULL,
			has_preview BOOLEAN NOT NULL,
			last_updated BIGINT NOT NULL DEFAULT 0,
			file_count BIGINT,
			PRIMARY KEY (source, path)
		)`,
		`ALTER TABLE index_items ADD COLUMN IF NOT EXISTS file_count BIGINT`,
		`CREATE INDEX IF NOT EXISTS idx_source_parent_path ON index_items(source, parent_path)`,
		`CREATE INDEX IF NOT EXISTS idx_source_size ON index_items(source, size)`,
		`CREATE INDEX IF NOT EXISTS idx_last_updated ON index_items(source, last_updated)`,
		`CREATE INDEX IF NOT EXISTS idx_source_directories ON index_items(source, path) WHERE is_dir = TRUE`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	if err := db.createIndexSourcesTable(); err != nil {
		return err
	}
	return db.createDirectoryStateTable()
}

// bindPostgres translates placeholders in internal SQL templates, leaving
// quoted strings/identifiers intact. Values always remain bound parameters.
func bindPostgres(query string) string {
	var out strings.Builder
	parameter := 0
	var quote byte
	for i := 0; i < len(query); i++ {
		c := query[i]
		if quote != 0 {
			out.WriteByte(c)
			if c == quote {
				if i+1 < len(query) && query[i+1] == quote {
					i++
					out.WriteByte(query[i])
				} else {
					quote = 0
				}
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
		}
		if c == '?' {
			parameter++
			out.WriteByte('$')
			out.WriteString(strconv.Itoa(parameter))
		} else {
			out.WriteByte(c)
		}
	}
	return out.String()
}

func (db *IndexDB) bulkInsertPostgres(source string, items []*iteminfo.FileInfo) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := db.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := db.bulkInsertPostgresTx(tx, source, items); err != nil {
		return err
	}
	if err := db.writeDirectoryStates(tx, source, items); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *IndexDB) bulkInsertPostgresTx(tx *sql.Tx, source string, items []*iteminfo.FileInfo) error {
	// PostgreSQL cannot update the same conflict target twice in one statement.
	// A parent listing and its recursive scan may both supply a directory row.
	unique := make([]*iteminfo.FileInfo, 0, len(items))
	positions := make(map[string]int, len(items))
	for _, item := range items {
		if position, ok := positions[item.Path]; ok {
			unique[position] = item
		} else {
			positions[item.Path] = len(unique)
			unique = append(unique, item)
		}
	}
	batchSize := min(db.BatchSize, 1000) // below PostgreSQL's parameter limit
	if batchSize < 1 {
		batchSize = 1000
	}
	now := time.Now().UnixNano()
	for start := 0; start < len(unique); start += batchSize {
		batch := unique[start:min(start+batchSize, len(unique))]
		values := make([]string, 0, len(batch))
		args := make([]interface{}, 0, len(batch)*13)
		for _, info := range batch {
			values = append(values, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
			args = append(args, source, info.Path, getParentPath(info.Path), info.Name,
				info.Size, createdUnix(info.Created), info.ModTime.Unix(), info.Type,
				info.Type == "directory", info.Hidden, info.HasPreview, now, fileCountValue(info.FileCount))
		}
		query := `INSERT INTO index_items
			(source, path, parent_path, name, size, created_time, mod_time, type, is_dir, is_hidden, has_preview, last_updated, file_count)
			VALUES ` + strings.Join(values, ",") + ` ON CONFLICT (source, path) DO UPDATE SET
			parent_path = excluded.parent_path, name = excluded.name, size = excluded.size,
			created_time = excluded.created_time, mod_time = excluded.mod_time, type = excluded.type,
			is_dir = excluded.is_dir, is_hidden = excluded.is_hidden, has_preview = excluded.has_preview,
			last_updated = excluded.last_updated, file_count = COALESCE(excluded.file_count, index_items.file_count)`
		if _, err := tx.Exec(bindPostgres(query), args...); err != nil {
			return err
		}
	}
	return nil
}

// SQLite GLOB has *, ?, and bracket classes; backslash is a literal character.
// Anchor the expression to preserve whole-name matching in PostgreSQL.
func postgresGlob(pattern string) string {
	runes := []rune(pattern)
	var out strings.Builder
	out.WriteString("^(?:")
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '*':
			out.WriteString(".*")
		case '?':
			out.WriteByte('.')
		case '[':
			end := i + 1
			if end < len(runes) && runes[end] == '^' {
				end++
			}
			if end < len(runes) && runes[end] == ']' {
				end++
			}
			for end < len(runes) && runes[end] != ']' {
				end++
			}
			if end == len(runes) {
				return "a^"
			}
			out.WriteByte('[')
			for j := i + 1; j < end; j++ {
				c := runes[j]
				if c == '\\' || c == '[' || c == ']' || (c == '^' && j != i+1) {
					out.WriteByte('\\')
				}
				out.WriteRune(c)
			}
			out.WriteByte(']')
			i = end
		default:
			out.WriteString(regexp.QuoteMeta(string(runes[i])))
		}
	}
	out.WriteString(")$")
	return out.String()
}
