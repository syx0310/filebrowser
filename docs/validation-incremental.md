# Incremental scanning and startup validation — 2026-10-24

Built and tested on an isolated Linux host, using generated fixtures and PostgreSQL 18.
Image version: `pg-incremental`, platform: `linux/amd64`.
The published registry manifest digest matched the tested image.

## Checks

* Complete backend `go test -race -timeout=180s ./...` passed with a dedicated PostgreSQL DSN.
* Final affected indexing, command and HTTP packages passed race tests again after the last fixes.
* SQLite/PostgreSQL contracts cover atomic directory reconciliation, checkpoint invalidation and subtree deletion.
* Scanner tests cover unchanged scans without a database handle, additions, nested discovery, deletion,
  moves, repeated scans, backwards timestamps, API write-through, failed transactions and read failures.
* Recovery tests restore counters without trusting application DB counters and repair parent aggregates
  after a child transaction committed before the parent sync.
* Full scans cover same-second deletion, top-level deletion and allocated-size empty-directory transitions.
* Startup tests exercise delayed opening/cancellation and browsing while SQLite's only connection is occupied.
* The complete Docker image built successfully; deployment Compose passed `config --quiet`.

## Runtime startup under a blocked index

An exclusive PostgreSQL lock held `index_items` for 25 seconds while starting the application.

| Observation | Result |
| --- | --- |
| HTTP health available after container start | 1.157 seconds |
| File listing while index initialization remained blocked | HTTP 200, 0.001 seconds on the small fixture |
| Source state during the lock | `status: indexing`, `initialization: opening` |
| After releasing the lock | Index recovered; root = 6 files / 47 bytes |
| Restart with persisted index | Ready in 1.322 seconds; index rows were not rewritten during startup |

The fixture includes top-level and nested hidden entries. File modification timestamps and size-mode
`blob` types / false preview flags were checked in PostgreSQL after recovery.

A separate container started with PostgreSQL unreachable. Health and file browsing remained available
with source state `unavailable` / `failed`. After connecting the database network, the application
automatically recovered in 30.196 seconds and restored the expected 6 files / 47 bytes. That temporary
container, network and database were removed after the check.

These timings validate separation of HTTP startup from index readiness on test data. They do not
estimate recovery time or metadata scanning throughput for large production arrays.

## Benchmark

See [扫描与启动优化](scanning-performance.zh-CN.md) for the before/after measurements, fixture,
scope and reproduction command. Benchmark source is `backend/pkg/indexing/quick_scan_benchmark_test.go`;
the same source was run against the previous release and the modified checkout, sequentially on one host.

Remote evidence:

```text
/tmp/filebrowser-incremental-all-tests.log
/tmp/filebrowser-incremental-final-index-tests.log
/tmp/filebrowser-incremental-startup-validation.json
/tmp/filebrowser-incremental-retry-validation.json
/tmp/filebrowser-incremental-docker-build.log
/tmp/filebrowser-incremental-push.log
```
