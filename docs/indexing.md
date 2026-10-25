# Index storage and size-only scanning

For a configurable Docker Compose example, see [部署说明（中文）](deployment-pg.zh-CN.md).
For incremental quick scans and asynchronous startup, see [扫描与启动优化（中文）](scanning-performance.zh-CN.md).

The file index supports SQLite (default) and PostgreSQL. This affects the file
index only. Users, permissions, shares and other application data keep using
`server.database.path` (SQLite).

```yaml
server:
  indexSqlConfig:
    driver: postgres
    dsn: "${FILEBROWSER_INDEX_DSN}"
    mode: size
    includeHidden: true
    batchSize: 1000
  sources:
    - path: /srv
      name: archive
      config:
        useLogicalSize: true
userDefaults:
  listing:
    showHidden: true
```

The equivalent direct environment overrides are:

```sh
FILEBROWSER_INDEX_DRIVER=postgres
FILEBROWSER_INDEX_DSN='postgres://filebrowser:password@postgres:5432/filebrowser_index?sslmode=require'
FILEBROWSER_INDEX_MODE=size
FILEBROWSER_INDEX_INCLUDE_HIDDEN=true
FILEBROWSER_SHOW_HIDDEN=true
```

Environment overrides take precedence over YAML and work without a config file.
Boolean overrides accept `true` and `false` (also Go's standard boolean spellings);
invalid values fail configuration loading. Use TLS settings appropriate to your
PostgreSQL deployment. Create the database before starting the app and grant its
user permission to create tables and indexes. Use a dedicated database/schema per
FileBrowser installation.

| Setting | Default | Behavior |
| --- | --- | --- |
| `driver` / `FILEBROWSER_INDEX_DRIVER` | `sqlite` | `sqlite` or `postgres` |
| `dsn` / `FILEBROWSER_INDEX_DSN` | empty | Required for PostgreSQL; omitted from JSON settings responses and redacted from YAML exports |
| `mode` / `FILEBROWSER_INDEX_MODE` | `full` | `full` or `size` |
| `includeHidden` / `FILEBROWSER_INDEX_INCLUDE_HIDDEN` | `false` | Include hidden files and directories at every depth in indexing and size totals |
| `userDefaults.listing.showHidden` / `FILEBROWSER_SHOW_HIDDEN` | `false` | Default listing preference; existing users' saved preferences remain unchanged |

In `size` mode **every indexed file and directory still has a row** containing its
path, parent, name, size, available creation time, modification time and hidden flag.
The list/compact view shows Name, Count, Size and Last modified. Count is the
recursive number of files (including subfolders), following the same hidden-file
policy as sizes. Files and directories with an unknown count display an em dash;
indexed empty folders show 0. Click Count to sort ascending or descending; unknown
counts stay after known counts, ties use natural name order, and pinned items retain
their priority. The sort preference is saved for the directory. Counts are persisted
and restored with sizes. Legacy
indexes acquire counts during subsequent scans, without a startup-wide backfill.

Scanning skips file-header/MIME detection and preview/cover-art probing. File types
are stored as `blob`, directories as `directory`, and preview flags are false.
Filename, size and time searches remain available; MIME-based search classification
is unavailable until a full index is built. Interactive browsing/previews can still
detect a file's type on demand, without persisting those probe results into the
size-only index. This reduces scanning I/O; storage still grows with file count.

Accounting and visibility are independent. Hidden entries can contribute to folder
sizes without appearing in the listing. Explicit source exclusion rules still apply,
including `ignoreHidden`. Neither switch overrides access permissions. Logical size
versus allocated size follows the source's existing `useLogicalSize` setting.

The first PostgreSQL start builds a new index from the configured sources; it does
not import or delete the existing SQLite index. Index policy is persisted per source.
Changing mode, hidden inclusion, source path, size basis or source rules rebuilds
the affected index in the background. Legacy SQLite indexes under default full,
hidden-excluded settings retain their existing scan schedule on upgrade. Routine
quick/full scan scheduling remains in effect; external file-content changes can
require the next full scan to be reflected.

## Scan scheduling

By default, scheduling adapts between 5 minutes and 12 hours, bounded by scanner
complexity. Each scanner starts with a full scan and then performs four quick scans.
To use a fixed delay after each scan completes, set:

```yaml
server:
  indexing:
    scanInterval: 4h
    fullScanEvery: 6
```

Or set `FILEBROWSER_INDEX_SCAN_INTERVAL=4h` and
`FILEBROWSER_INDEX_FULL_SCAN_EVERY=6`. A source may override either field under
`server.sources[].config.indexing`. Per-source explicit values override the global
environment defaults; global environment values override global YAML values.
An empty source interval or zero cycle inherits the global setting. The global
defaults are `auto` and `5`; a global zero cycle also means the default five runs.
An explicit `auto` uses adaptive scheduling even under a global fixed interval.

Fixed intervals accept Go durations of at least `1m`, such as `30m`, `1h30m` or
`24h` (not `1d`). They bypass complexity bounds and wall-clock alignment. Scans
within a source remain serial, so queueing can delay a run but cannot overlap it.
`fullScanEvery: 6` means one full scan and five quick scans; `1` means always full.
This is a run count, not a daily full-scan schedule.

Restart the process after changing configuration. Saved completion times and cycle
positions are reused; overdue scans run promptly, and an already-exceeded shorter
cycle makes the next run full. Scheduling changes do not rebuild the file index.
`/api/settings/sources?scanners=true` reports the effective source `indexing` settings
and per-scanner `nextScanUnixTime` (Unix seconds, before any queueing delay).

SQLite's `walMode`, `cacheSizeMB`, `startupIntegrityCheck` and `disableReuse` are
SQLite-specific. `disableReuse: true` is rejected for PostgreSQL; other SQLite
tuning settings do not affect it. PostgreSQL startup checks connectivity/schema
without a SQLite-style integrity scan. PostgreSQL uses a bounded connection pool
and parameterized multi-row upserts. Stale rows are removed by scans, not simply
because their last-update timestamp is more than 24 hours old.

Run the shared contract tests against a **dedicated test database**:

```sh
cd backend
FILEBROWSER_TEST_POSTGRES_DSN='postgres://test:password@127.0.0.1:5432/test_index?sslmode=disable' \
  go test ./internal/database/sql ./pkg/indexing ./pkg/settings
```
