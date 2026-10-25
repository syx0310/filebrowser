# FileBrowser PostgreSQL / 纯大小索引版部署

本文提供通用部署模板。镜像地址、主机目录、端口和密码由部署者通过环境变量填写。
`FILEBROWSER_IMAGE` 必须指向包含 PostgreSQL 索引支持的构建；
`compose.pg.env.example` 中的 `registry.example.com` 是占位域名，需要替换。
也可在仓库根目录自行构建，并将 `FILEBROWSER_IMAGE` 设置为 `filebrowser:pg`：

```sh
docker build -f _docker/Dockerfile --build-arg VERSION=pg -t filebrowser:pg .
```

配套文件：[compose.pg.example.yaml](compose.pg.example.yaml)、[环境变量示例](compose.pg.env.example)、[验证记录](validation-pg.md)。

2026-10-24 更新：[快扫与大索引启动优化、实测结果和更新方法](scanning-performance.zh-CN.md)。
本次[验证记录](validation-incremental.md)包含数据库阻塞期间的 HTTP/浏览测试。
追加更新：手动扫描间隔、完整扫描轮次、Count 排序和“56 年前”显示修复，
见[调度与页面验证](validation-schedule.md)。

## 目录与端口

配套 Compose 使用服务名 `filebrowser`，容器名由 Compose 项目生成。
`FILEBROWSER_HTTP_PORT` 默认为 `8080`，映射容器端口 `80`；
配置文件中的 `http.port` 应设为 `80`。
示例使用 `user: 0:0`、`DAC_READ_SEARCH` 和只读数据源，可按文件访问权限需求调整。
默认目录均相对于 Compose 项目目录：

```text
./data/
├── config/       # config.yaml，以及用户/设置等应用数据库
├── cache/        # 应用缓存
└── postgres/     # PostgreSQL 数据，亦可放到本地 SSD
```

Compose 中对应的是：

```yaml
volumes:
  - ${FILEBROWSER_CONFIG_DIR:-./data/config}:/config
  - ${FILEBROWSER_FILES:?Set FILEBROWSER_FILES}:/mnt:ro
  - ${FILEBROWSER_CACHE_DIR:-./data/cache}:/.cache
```

`FILEBROWSER_FILES` 必须填写要浏览的主机目录；其他目录变量可以指向现有持久化目录。
从已有部署迁移时，先备份原 Compose、配置与应用数据库，再填入实际路径。
若保留原服务名或容器内端口，需要同步调整下文命令、端口映射和健康检查。

只将**文件索引**切换为 PostgreSQL。用户、密码、权限、分享、设置等应用数据
继续使用原有 SQLite；不能把应用数据库当成旧索引删除。
没有 SQLite 索引导入步骤，第一次启动会重新扫描数据源，无需复制旧索引文件。
重建期间目录容量和文件数会逐步完整。

## 部署前检查配置

保留现有 `config/config.yaml` 的全部认证、权限和数据源设置。确认：

* `http.port` 为 `80`，与示例的端口映射和健康检查一致。
* 数据源 `server.sources[].path` 仍是容器内的 `/mnt`（或原有 `/mnt` 下的子路径）。
* `server.cacheDir` 指向 `/.cache`，或当前持久化挂载内的实际路径。
* `server.database.path` 指向**复制过来的应用数据库**，路径位于 `/config`
  等持久化挂载内。继续用原文件名，不要为了示例创建空库。
* `server.indexSqlConfig.disableReuse` 缺省或为 `false`；PostgreSQL 不支持 `true`。
* 如果希望统计所有隐藏文件，检查原数据源规则是否有 `ignoreHidden: true`
  或其他排除规则。这些显式规则仍然有效，环境变量不会覆盖它们。

大小口径沿用每个数据源现有的 `useLogicalSize`：`true` 统计文件逻辑长度，
`false` 按现有分配空间逻辑统计。不要为了这次迁移顺手改口径，否则前后结果无法比较。
ZFS 数据集的压缩、快照和共享块使用量不等同于逐目录文件大小汇总。

## 环境变量

配套 Compose 已设置：

| 环境变量 | 值 | 含义 |
| --- | --- | --- |
| `FILEBROWSER_INDEX_DRIVER` | `postgres` | 文件索引使用 PostgreSQL |
| `FILEBROWSER_INDEX_DSN` | 连接 `filebrowser-postgres` 服务 | 索引连接串，包含数据库密码 |
| `FILEBROWSER_INDEX_MODE` | `size` | 每个文件、目录保留基础元数据，扫描跳过类型和预览探测 |
| `FILEBROWSER_INDEX_INCLUDE_HIDDEN` | `true` | 各层隐藏文件/目录参与索引、大小和文件数统计 |
| `FILEBROWSER_SHOW_HIDDEN` | `true` | 默认展示隐藏文件 |

直接环境变量优先于 YAML。`FILEBROWSER_SHOW_HIDDEN` 是用户默认偏好，
已有用户在数据库里保存的显示偏好仍然保留；若原账号隐藏文件未显示，
在该账号的列表设置中开启“显示隐藏文件”。显示开关不会改变索引统计口径。

`size` 模式仍保留名称、路径、大小、创建时间（文件系统可提供时）、修改时间和隐藏标记。
每个条目仍占一条索引记录；不会把索引缩减成只有目录。
扫描时文件类型统一为 `blob`，不读取文件头或探测缩略图/封面。
实际打开目录或预览文件时可以按需识别类型；不会把探测结果写回纯大小索引。

## 手动配置扫描频率

在 FileBrowser 服务的 `environment` 中增加以下两项（示例值，可自行调整）：

```yaml
FILEBROWSER_INDEX_SCAN_INTERVAL: '4h'
FILEBROWSER_INDEX_FULL_SCAN_EVERY: '6'
```

每个扫描器本轮结束后等待 4 小时，每 6 轮做一次完整扫描，其余 5 轮快扫。
`1` 表示每轮完整扫描；间隔最短 `1m`，支持 `30m`、`1h30m`、`24h`。
不设置时仍是自适应间隔及每 5 轮完整扫描一次。
配置修改后重新创建应用容器生效，无需清空索引。

使用配套示例的服务名时，更新命令为：

```sh
docker compose pull filebrowser
docker compose up -d filebrowser
```

使用本地构建的镜像时，先重新构建镜像并跳过 `pull`。

也可用 `server.indexing.scanInterval` / `server.indexing.fullScanEvery` 全局 YAML，
或 `server.sources[].config.indexing` 分数据源覆盖。
数据源显式值优先于全局环境变量；[完整规则与示例](scanning-performance.zh-CN.md)说明
优先级、重启恢复与默认自适应档位。

## 替换 Compose 并启动

在**原 Compose 项目目录**操作，保留原项目名。用配套 `compose.pg.example.yaml`
内容替换该项目正在使用的 Compose 文件，不要另起项目与旧容器争用名字/端口。
如果使用管理面板，把配套 YAML 放入原应用的 Compose 编辑器，并设置对应环境变量。

先保存原 Compose 文件。以下假设原文件名为 `compose.yaml`：

```sh
cp compose.yaml compose.before-pg.yaml
# 将配套 compose.pg.example.yaml 的内容保存为本目录的 compose.yaml
```

把配套 `compose.pg.env.example` 的内容合并到本目录 `.env`，保留已有其他变量。
填写镜像地址、目录、主机端口、原管理员密码，以及用下面命令生成的数据库密码：

```sh
openssl rand -hex 32
chmod 600 .env
```

数据库密码使用上述十六进制字符串，避免连接串内的特殊字符需要额外 URL 编码。
配套 Compose 从环境变量读取密码，不在模板中保存明文。
本地已有的 `docs/.env.pg.local`（如存在）被 Git 忽略，可通过
`docker compose --env-file docs/.env.pg.local -f docs/compose.pg.example.yaml …` 使用；
部署时也可将其中的值填入项目 `.env`，不要把该本地文件提交到仓库。

`POSTGRES_PASSWORD` 和连接串中的密码必须一致。
修改 `.env` 不会自动修改已初始化 PostgreSQL 的密码。

`FILEBROWSER_PG_DATA` 默认指向 `./data/postgres`；如有本地 SSD，可在第一次
启动前改为 SSD 上的目录。容器使用 PostgreSQL 18，挂载目标为
`/var/lib/postgresql`，其实际数据目录为 `/var/lib/postgresql/18/docker`。
数据库不映射主机端口，仅由同一个 Compose 网络中的应用连接。
应用仅等待 PostgreSQL 容器启动（`service_started`），连接及索引恢复在后台重试，
因此 HTTP 健康不等同于索引已就绪；恢复状态见页面数据源状态。
示例的 `sslmode=disable` 对应该容器内网；连接已有远端 PostgreSQL 时使用其 TLS 设置。

确认配置存在后启动：

```sh
test -f ./data/config/config.yaml  # 使用自定义 FILEBROWSER_CONFIG_DIR 时调整此路径
docker compose config --quiet
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=100 filebrowser-postgres filebrowser
curl -fsS http://127.0.0.1:8080/health  # 使用自定义 FILEBROWSER_HTTP_PORT 时调整端口
```

保留原镜像和原数据目录，迁移时不需要删除它们。
如果原项目配置文件名或项目名通过 `-f` / `-p` 指定，以上每条命令沿用相同参数。

## 验收

1. `/health` 返回成功，原账号能正常登录，权限/分享保留。
2. 切换到列表或紧凑视图，列顺序为 `Name / Count / Size / Last modified`。
   Count 是目录下所有层级的文件数，不含目录本身；普通文件显示 `—`，
   已完成扫描的空目录显示 `0`，未完成统计的目录显示 `—`。
   点击 Count 可切换升序/降序并保存该目录的排序偏好；未知数量排在已知数量后，
   相同数量按名称自然排序，保留置顶项目优先级。
3. 在包含隐藏文件和隐藏子目录的小目录上核对大小和 Count。
   统计与数据源的排除、大小口径规则保持一致。
4. 初次扫描完成后，重启 `filebrowser`，确认大小和文件数能恢复。
   未改变策略时复用索引；修改 `mode`、隐藏统计开关、源路径、大小口径或规则
   会使受影响数据源重新建立索引。

可以在 PostgreSQL 里只查看源根目录，避免为了验收对海量条目执行全表聚合：

```sh
docker compose exec -T filebrowser-postgres psql -U filebrowser -d filebrowser_index \
  -c "SELECT source, path, size, file_count FROM index_items WHERE path = '/';"
docker compose exec -T filebrowser-postgres psql -U filebrowser -d filebrowser_index \
  -c "SELECT pg_size_pretty(pg_database_size(current_database()));"
```

统计仍依赖扫描。快速扫描检查目录变化；外部程序直接改写已有文件内容但未改变目录
时间时，容量可能需要下一次完整扫描才能更新。此版本不承诺文件系统实时事件级同步。

页面示例（测试数据，非正式文件）：

![Name / Count / Size / Last modified](screenshots/count-list.png)

## 回退与维护

回退时恢复原 Compose、原镜像和原配置/缓存挂载路径，然后重新 `docker compose up -d`。
切勿同时让两个实例写入同一个应用数据库。新增 PostgreSQL 服务可先停止并保留数据：

```sh
docker compose stop filebrowser-postgres
cp compose.before-pg.yaml compose.yaml
docker compose up -d
```

若原目录的索引也已删除，旧版回退后仍需要自行重建旧索引。
新目录里更新后的应用数据库不要直接覆盖原版备份；原目录和备份应保留到验收结束。
PostgreSQL 只存可重建索引，重建代价是全盘扫描；应用 SQLite、配置和密钥需要单独备份。
应用数据库正在写入时，应停应用后复制或使用 SQLite 的在线备份机制。

PostgreSQL 索引需要备份时可以使用：

```sh
docker compose exec -T filebrowser-postgres pg_dump -U filebrowser -d filebrowser_index -Fc \
  > filebrowser-index.dump
```

本次验证覆盖功能正确性和小规模重启复用，没有对大规模生产数据集做性能基准。
PostgreSQL 避免了原 SQLite 索引启动完整性检查路径，并提供批量写入和连接池；
纯大小扫描减少文件内容读取，但首轮仍需遍历、stat 每个条目。
机械盘上的收益取决于文件数量、目录分布、元数据缓存和数据库所在介质，不能据此给出固定倍数。
