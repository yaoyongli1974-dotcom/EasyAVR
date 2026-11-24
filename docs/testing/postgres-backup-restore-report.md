# PostgreSQL 备份恢复与凭据密钥演练报告（EASYAVR-TECH-003 / TASK-013）

- 日期：2026-11-25（本机时间 00:40–00:41）
- 分支：`feature/easyavr-tech-003-reliability`
- 对象：`deploy/scripts/verify-tech003-postgres.sh`（本次新增）、既有 `pg_backup.sh` / `pg_restore.sh`
- 环境：容器 `easyavr-postgres` = PostgreSQL 16.15（监听 `127.0.0.1:5432`）；客户端 `pg_dump`/`pg_restore` 17.12；Go 1.26.0；平台以 `EASYAVR_ENV=production`、`EASYAVR_DB_DRIVER=postgres` 运行。
- 安全边界：只使用本地可写维护账号连接维护库 `postgres`；业务库 `easyavr` **未连接、未修改、未清空**；所有测试库名均为脚本随机生成的 `easyavr_pg003_<8 hex>_src/_dst`，退出时已 DROP。主密钥、管理员口令、测试凭据仅在演练进程环境变量/shell 变量中，演练结束后随进程销毁；本报告与所有日志均不含密钥、密码或完整 DSN（下面命令中的 `<口令>` 为占位符）。

## 执行的命令与退出码

| # | 命令（口令已脱敏） | 退出码 | 结果 |
|---|---|---:|---|
| 1 | `bash -n deploy/scripts/verify-tech003-postgres.sh` | 0 | 语法通过 |
| 2 | `env -u PG_TEST_DSN bash deploy/scripts/verify-tech003-postgres.sh` | 2 | SKIP：未传 `PG_TEST_DSN`，未连接任何库 |
| 3 | `PG_TEST_DSN='postgres://<user>:<口令>@127.0.0.1:5432/easyavr_prod?sslmode=disable' bash deploy/scripts/verify-tech003-postgres.sh` | 1 | 安全门拒绝生产样式库名（建库前退出） |
| 4 | 同上，`PG_TEST_DSN` 指向 `postgres` 维护库（第一次完整演练） | 1 | `SUMMARY PASS=68 FAIL=2 SKIP=0`，暴露 2 个 FAIL 与 3 个假 PASS，已修复 |
| 5 | 同上，第二次完整演练（修复后） | 0 | `SUMMARY PASS=70 FAIL=0 SKIP=0 CLEANUP_WARN=0` |

完整演练调用形式（口令按安全要求不落报告）：

```bash
PG_TEST_DSN='postgres://<user>:<口令>@127.0.0.1:5432/postgres?sslmode=disable' \
  bash deploy/scripts/verify-tech003-postgres.sh
```

第二次演练自建库名为 `easyavr_pg003_2f56e439_src` / `easyavr_pg003_2f56e439_dst`（随机值，每次不同）；备份产物名 `easyavr-pg-20261125-004105-740086187.dump`（临时目录内，随演练删除）。

## 第二次演练逐项结果（PASS=70 / FAIL=0 / SKIP=0）

| 阶段 | 实测结果 |
|---|---|
| 前置检查 | `psql`/`pg_dump`/`pg_restore`/`python3`/`openssl`/`curl`/`go`/`bash` 全可用；两个既有脚本 `bash -n` 通过 |
| DSN 安全门 | 打印脱敏 DSN `postgres://easyavr:***@127.0.0.1:5432/postgres?sslmode=disable`；维护库 `SELECT 1` 与 `CREATE DATABASE` 权限通过；测试库名白名单通过 |
| 建库 | 创建 `easyavr_pg003_2f56e439_src`、`easyavr_pg003_2f56e439_dst` |
| AutoMigrate | 平台以生产模式 + PG 驱动启动，`/api/v1/healthz` 返回 `code:0`；建表 48 张 |
| 测试数据 | 管理员登录取得 JWT；创建 viewer 用户、设备（密码经 GORM hook 加密）、主/子通道、AIProvider（APIKey 经 hook 加密）、AITask；SQL 插入 AITaskRun、快照、录像；API 中间件写入审计 5 行；种子写入平台配置 4 行 |
| 加密落库 | `devices.password` 与 `a_iproviders.api_key` 均为 `enc:v1:`（AIProvider 的 GORM 表名实际是 `a_iproviders`）；库中无测试密码/APIKey 明文 |
| 备份 | `pg_backup.sh` 退出 0；产物非空、魔数 `PGDMP`、`pg_restore -l` 目录 445 行、权限 `0600` |
| dry-run | `pg_restore.sh -d <目标库> --dry-run` 退出 0；目标库 public 表数 `0 → 0`，确认零写入 |
| 真实恢复 | `pg_restore.sh -d <目标库> --yes` 退出 0 |
| 结构校验 | 列定义 550 列、索引 149 个、约束 49 个，源/目标逐项 diff 一致；`channels(stream_key)`、`users(username)`、`platform_settings(key)` 唯一索引存在 |
| 数据校验 | 10 张关键表行数逐一相等（users=2 / devices=1 / channels=2 / a_iproviders=1 / ai_tasks=1 / ai_task_runs=1 / snapshots=1 / recordings=1 / audit_logs=5 / platform_settings=4）；设备→2 通道、任务→通道→Provider、快照/录像/运行记录外键关联均命中；`platform_settings.gb_enabled=false` 保留；密文仍为 `enc:v1:` |
| 密钥 v1 | 目标库用生成时的 v1 密钥启动成功（启动时校验全部凭据列可解密）；更新 Provider 使密文 nonce 变化且仍为 `enc:v1:`，证明解密→重加密往返 |
| 错误密钥 | 换用随机错误主密钥（无 `PREVIOUS`）启动，进程退出码 1，日志报解密/打开数据库错误；库中原有 `enc:v1:` 未被改写，fail-closed 成立 |
| 密钥轮换 | `EASYAVR_CREDENTIAL_KEY=<v2>` + `EASYAVR_CREDENTIAL_KEY_PREVIOUS=v1:<v1>` 启动成功；设备与 Provider 共 2 行密文由 `enc:v1:` 变为 `enc:v2:`；v2 下再次更新 Provider 往返成功 |
| 日志脱敏 | 遍历全部演练日志核对：完整 DSN、DSN 口令、v1/v2/错误主密钥、JWT 密钥、管理员/viewer 口令、设备密码、AIKey 均无字面值命中 |
| 清理 | trap 停止全部平台进程、删除临时目录、DROP 两个一次性库；`CLEANUP_WARN=0`；复核 `pg_database` 无 `easyavr_pg003_*` 残留，`18009`/`18010` 无监听进程 |

## 第一次演练发现的问题与修复

1. **结构/索引/约束校验是假 PASS（3 项）**：`ORDER BY 1,2` 用在只输出一列拼接结果的查询上，PG 报 `ORDER BY position 2 is not in select list`；`contype`（`"char"`）拼接也报 `operator is not unique`。两个查询都输出空文件，空文件 diff 相等导致误报 PASS。修复：按真实列名排序、`contype::text` 显式转换，并要求两边目录查询非空才判 PASS。修复后实际比对 550 列 / 149 索引 / 49 约束。
2. **平台配置字段值 FAIL（误报）**：脚本按 `gb.enabled` 查询，实际种子键名是 `gb_enabled`（见 `server/internal/server/app.go` 的 `seedPlatformSettings`）。修复为 `gb_enabled`，修复后 PASS。
3. **日志脱敏审计 FAIL（误报）**：维护账号口令恰好是短字符串 `easyavr`，与一次性库名 `easyavr_pg003_*`、脚本路径等普通文本子串重合，导致误判“日志含口令”。修复：口令检查改为 `:<口令>@` 这一完整 DSN 片段，并给每个检查项加标签（失败时只报标签与日志文件名，不报秘密值）。修复后 PASS。
4. 以上均为验证脚本自身的缺陷，未修改 `server/`、`web/` 或其他 deploy 脚本；`pg_backup.sh` / `pg_restore.sh` 行为符合预期。

## SKIP / 待验证（演练范围外，不冒称已验证）

- **生产实例**：按任务约束未连接生产服务器，未改生产配置或密钥；生产环境的备份目录、`/etc/easyavr/env` 独立密钥保管流程仍须人工落实。
- **业务库 `easyavr`**：绝对禁止连接，因此未做“对真实业务数据做备份恢复”的演练；本报告的结论仅适用于一次性隔离库。
- **`pg_restore.sh` 的版本偏差告警分支**：本机客户端 17.12 / 服务端 16.15 存在已知 `transaction_timeout` 偏差，脚本中有专门放行逻辑；本次恢复退出码为 0，但告警行本身在临时日志中且已按策略清理，未单独取证，标记待验证。
- **pgvector 路径**：演练设置 `EASYAVR_PGVECTOR=false`，未覆盖 pgvector 扩展与事件向量表的恢复。
- **大规模/并发写入场景**：只覆盖小数据量的静态备份恢复，未测 TB 级归档、并发写入中热备的性能与一致性窗口。
- **真实主密钥保管**：本次 v1/v2 主密钥由 `openssl rand -base64 32` 在进程内生成并丢弃，未演练外部 KMS/密码管理器的取回流程（按设计也不应落盘）。

## TASK-017：DSN 主机门禁与 CHECK_ONLY 自检（安全跟进）

- 日期：2026-11-25
- 分支：`feature/easyavr-tech-003-security-followup`
- 范围：只改 `deploy/scripts/verify-tech003-postgres.sh`、新增 `deploy/scripts/verify-tech003-dsn-gate-test.sh` 与两份文档；**未改 `server/`、`web/`，未连数据库、未做真实备份恢复演练**。
- 缺陷背景：原脚本只校验维护库名必须为 `postgres`，未限制 `PG_TEST_DSN` 的主机；误传远程 DSN 可能对非本地库执行 `pg_restore --clean --if-exists`。

### 新增逻辑

1. 主机门禁与库名检查在解析后、任何 psql 调用之前同时执行。默认只放行 `localhost` / `127.0.0.1` / `::1`；`[::1]` 方括号与 `%5B%3A%3A1%5D`、`?host=%3A%3A1` 等 URL 编码写法先解码、规范化后判定。非回环主机默认直接拒绝（原因标签 `remote-not-allowed`）。
2. 远程放行要求 `PG_TEST_ALLOW_REMOTE=1` 且 `PG_TEST_REMOTE_HOST` / `PG_TEST_REMOTE_PORT` / `PG_TEST_MAINTENANCE_DB=postgres` 三项与解析后的 DSN 完全一致（主机解码/去括号/小写，端口数字规范化，缺省 5432）；缺失为 `remote-confirm-missing`，不一致为 `remote-confirm-mismatch`。远程模式仍拒绝业务库/生产样式库名；即使放行也只允许本人明确创建的隔离测试库，绝不指向生产。
3. `PG_TEST_DSN_CHECK_ONLY=1` 在 `mktemp`/trap/前置检查之前退出，只解析+过门；输出脱敏结果（例：`postgres://***@<host>/postgres?sslmode=disable`），不含口令、完整 DSN、真实主机地址。退出码 0=允许 / 1=拒绝 / 2=缺 DSN。
4. 主流程仍沿用原 `REDACTED_DSN` 日志与末尾的日志泄漏审计（`完整DSN|$PG_TEST_DSN` 等），新增日志不输出完整 DSN；CHECK_ONLY 另用进一步遮主机的脱敏串。

### 执行的命令与结果（全部为门禁静态检查，无数据库连接）

| 命令 | 退出码 | 结果 |
|---|---:|---|
| `bash -n deploy/scripts/*.sh` | 0 | 全部脚本语法通过 |
| `bash deploy/scripts/verify-tech003-dsn-gate-test.sh` | 0 | `SUMMARY PASS=66 FAIL=0`（11 个用例 × 6 项断言） |
| `env -u PG_TEST_DSN PG_TEST_DSN_CHECK_ONLY=1 bash deploy/scripts/verify-tech003-postgres.sh` | 2 | SKIP；未连接数据库 |
| `env -u PG_TEST_DSN bash deploy/scripts/verify-tech003-postgres.sh` | 2 | 主流程未传 DSN 仍 SKIP，行为不变 |

自检覆盖：远程地址拒绝（无开关）、localhost / 127.0.0.1 / `[::1]` / URL 编码 `::1` 放行、远程显式开关+三项确认放行、确认变量不匹配拒绝、确认变量缺失拒绝、业务库 `easyavr` 与生产样式 `easyavr_prod` 拒绝、无 DSN SKIP。每个用例均断言退出码、输出无口令/完整 DSN/真实主机地址、明确声明未连接数据库、且 `TMPDIR` 下无新建目录或库。实测各用例原因标签分别为 `remote-not-allowed`、`ok`、`remote-confirm-mismatch`、`remote-confirm-missing`、`maintenance-db`。

## 结论

`deploy/scripts/verify-tech003-postgres.sh` 已完成一次真实隔离演练：第二次运行 70 项 PASS、0 FAIL、0 SKIP、0 清理告警，退出码 0；安全门、加密落库、备份校验、dry-run 零写入、真实恢复、结构/数据/关联/索引/约束校验、v1 解密、错误密钥 fail-closed 与 v1→v2 轮换全部实测通过。剩余项均在上面的“SKIP / 待验证”中明确列出。

TASK-017 安全跟进另以 CHECK_ONLY 静态自检验证：`bash -n deploy/scripts/*.sh` 通过，门禁自检 `PASS=66 FAIL=0`，未传 `PG_TEST_DSN` 仍 SKIP/exit 2；未连接任何数据库、未修改备份恢复主流程。
