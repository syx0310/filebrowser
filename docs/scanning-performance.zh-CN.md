# 快扫与大索引启动优化（2026-10-24）

以下优化包含在 PostgreSQL 索引版本中，使用[通用部署模板](deployment-pg.zh-CN.md)配置镜像和环境变量。

## 快扫

索引库新增 `index_directory_state`，只保存目录的修改时间（纳秒）、直接文件数、
直接大小贡献和有效标记。目录索引与扫描基线在同一事务提交，提交成功后才更新内存统计。

* 不变目录只检查文件系统元数据，不再逐目录查库、整行写回或刷新 SQL 心跳。
* 变化目录读取完整直接子项，与旧索引比较；只在确认删除后移除旧条目及子树。
* 新的嵌套目录立即建立基线，文件数、目录数按已提交的基线变化维护。
* 快扫结束不再运行整棵子树的 SQL 计数；读取失败、写入失败和扫描中变化保留旧基线，允许重试。
* API 浏览写入元数据会使对应基线失效，下一轮核对，避免与扫描重复累计。

默认仍为“1 次完整扫描 + 4 次快扫”，也可手动配置间隔和轮次（见下文）。完整扫描仍做 SQL 计数校验。
旧索引在后续正常扫描中逐步建立基线，不强制启动全盘重扫。第一次补齐基线的扫描
不会获得“不变目录零 SQL”的收益。

内部 `last_updated` 改用纳秒，修复同一秒内连续完整扫描遗漏删除项的问题；
文件的 `created_time`、`mod_time` 不变。回滚此前 PG 镜像时应恢复旧索引备份或
重建派生索引，避免旧版按秒清理时保留过期记录；应用用户库不受影响。

## 默认扫描频率与时间显示

当前调度按数据源根目录及每个一级子目录分别维护，无固定的全源扫描周期。
首次没有扫描记录时立即完整扫描；有记录的重启恢复原调度，不强制全盘重扫。

自适应间隔档位为 **5 分钟、10 分钟、20 分钟、40 分钟、1 小时、2 小时、
3 小时、4 小时、8 小时、12 小时**。无变化往慢一档调整；有变化往快一档调整，
若原来慢于 40 分钟则先回到 40 分钟，再按复杂度限制。

| 扫描器复杂度 | 最快间隔 | 最慢间隔 |
| --- | --- | --- |
| 1 | 5 分钟 | 1 小时 |
| 2–3 | 5 分钟 | 12 小时 |
| 4–5 | 10 分钟 | 12 小时 |
| 6–7 | 20 分钟 | 12 小时 |
| 8–9 | 40 分钟 | 12 小时 |
| 10 | 1 小时 | 12 小时 |

复杂度取完整扫描耗时评分、目录数评分中的较高值，例如单个扫描器完整扫描
耗时达到 5 分钟或包含至少 100 万个目录时为 10。容量 TB 数本身不参与评分。
下一轮时间从上次扫描结束加上间隔，再向上对齐到该间隔的 UTC 时间槽；
同源扫描器串行，因此实际等待还会受时间对齐和队列影响。
未配置时，每个扫描器按 **1 次完整扫描 + 4 次快扫** 循环，完整扫描不是固定每天一次。

### 手动设置间隔和完整扫描轮次

Compose 的 FileBrowser 服务 `environment` 中可以增加：

```yaml
FILEBROWSER_INDEX_SCAN_INTERVAL: '4h'
FILEBROWSER_INDEX_FULL_SCAN_EVERY: '6'
```

含义是每个扫描器**完成本轮后等待 4 小时**，每 6 轮完整扫描一次，
即 1 次完整扫描加 5 次快扫，首次仍完整扫描。固定模式忽略复杂度下限，不向整点对齐；同源仍串行，
排队可能延后开始，不会因扫描耗时大于间隔而重叠启动。“6 轮”不是每天一次。
间隔支持 `30m`、`1h30m`、`24h`，最短 `1m`；一天写 `24h`，不支持 `1d`。
`fullScanEvery: 1` 表示每轮都完整扫描；`0` 使用默认值/继承全局值。

等价全局 YAML，以及只覆盖一个数据源的示例：

```yaml
server:
  indexing:
    scanInterval: 4h
    fullScanEvery: 6
  sources:
    - name: archive
      path: /mnt
      config:
        # 合并到现有 config，保留原有规则、权限与大小口径
        indexing:
          scanInterval: 12h
          fullScanEvery: 2
```

优先级按字段为：**数据源显式配置 > 全局环境变量 > 全局 YAML > 默认值**。
数据源间隔留空、轮次为 `0` 时继承全局值；显式 `scanInterval: auto` 可让该源
使用自适应调度。全局缺省为 `auto` / `5`。配置变更后重建应用容器或重启进程，
无需删除索引；按保存的扫描结束时间和新间隔恢复调度，已经到期的尽快执行，
保留扫描轮次。缩短轮次后若已超过新周期，下一轮做完整扫描。

`GET /api/settings/sources?scanners=true` 的源级 `indexing` 返回生效配置，
各扫描器的 `nextScanUnixTime` 返回预计下次开始时间（Unix 秒；队列可能延后）。
调度参数不属于索引内容策略，修改它们不会触发索引重建。

“上次扫描 56 年前”是页面将 Unix 秒误当成毫秒造成的显示错误：正常的
2026 年时间被格式化为 1970 年。已修复为按秒格式化；没有有效扫描时间时
显示“未知”，避免把未完成初始化误显示成“刚刚”。该修复不需要重建索引。

## 大索引启动

索引数据库打开、完整性检查和表结构初始化移到后台。数据源先注册，登录和文件系统
浏览先可用；恢复期间浏览不等待 SQLite 的恢复查询释放连接。
`/health` 表示 HTTP 可用，不代表索引恢复完成。

`GET /api/settings/sources` 的 `initialization` 字段标记：

| 值 | 含义 |
| --- | --- |
| `opening` | 正在打开数据库或初始化表结构 |
| `restoring` | 正在恢复目录缓存和调度 |
| `failed` | 打开/恢复失败，保留文件浏览，30 秒后重试 |
| 省略 | 初始化完成，扫描进度继续由原 `status` 表示 |

恢复只加载目录级信息，并从已提交摘要重建父目录统计，覆盖“子目录已提交但父目录
统计尚未同步就退出”的情况。恢复仍需与目录数对应的时间和内存。

SQLite 的 `server.indexSqlConfig.startupIntegrityCheck` 默认改为 `probe`，检查库结构
和一条记录是否可读；这不等同于全库完整性检查。显式 `quickCheck` 仍有效，也在后台执行。
新增目录专用索引用于恢复缓存，旧大库首次建立可能较慢。临时锁冲突、权限和 I/O 错误
不会直接被当作损坏而删除索引。PostgreSQL 不使用这些 SQLite PRAGMA 设置。

## 测量结果

在独立 Linux 测试主机上，使用 1000 个子目录、1 万个文件（含扫描根共 1001 个目录），
先完整扫描，再计时无变化快扫。优化前后依次运行相同 benchmark；每组 3 次、每次 5 轮，取中位值：

| 后端 | 优化前 | 优化后 | 样本耗时比 |
| --- | ---: | ---: | ---: |
| SQLite | 77.30 ms/轮 | 3.71 ms/轮 | 约 20.8 倍 |
| PostgreSQL | 271.04 ms/轮 | 3.67 ms/轮 | 约 73.9 倍 |

范围是 `runQuickScanChild`，不包括首次扫描、HTTP 和源级状态通知。这是热缓存小样本，
不能据此预测大规模机械盘数据集的性能；实盘目录 `stat` 占比越高，整体收益比例越小。

复现（使用独立测试数据库）：

```sh
cd backend
GOTOOLCHAIN=auto FILEBROWSER_TEST_POSTGRES_DSN='postgres://user:password@host/testdb?sslmode=disable' \
  go test -run '^$' -bench '^BenchmarkQuickScanLargeTree$' -benchtime=5x -count=3 -benchmem ./pkg/indexing
```

远端日志：`/tmp/filebrowser-before-quick-quick-benchmark.log`、
`/tmp/filebrowser-quick-benchmark.log`、`/tmp/filebrowser-incremental-all-tests.log`。

## 更新

在原 Compose 项目目录运行，无需清空 PostgreSQL 或应用数据库：

配套 Compose 将 PostgreSQL 的依赖条件改成 `service_started`。已有部署也建议把
`depends_on.filebrowser-postgres.condition` 从 `service_healthy` 改为 `service_started`，
让 HTTP 无需等待 PostgreSQL 完成自身恢复；应用会在后台重试连接。

```sh
docker compose pull filebrowser
docker compose up -d filebrowser
docker compose logs --tail=100 filebrowser
```

目录时间不能捕获所有已有文件的原地改写，大小变化仍可能需要下一次完整扫描反映。
本次没有引入文件事件监听或 ZFS 快照差异扫描。完整部署见[部署文档](deployment-pg.zh-CN.md)。
