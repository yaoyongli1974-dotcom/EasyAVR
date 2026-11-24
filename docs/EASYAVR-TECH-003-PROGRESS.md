# EASYAVR-TECH-003 进度记录

## TASK-000：基线检查与任务适配

- 分支：`feature/easyavr-tech-003-reliability`（从最新 `main` 创建）。
- 基线：`origin/main == d367931d073a4b360320e6b32b2411cf3f7771c7`，与任务书记录一致；`git fetch origin` 后无更新提交，工作区干净，未合并的其他分支仅有已合并的 `feature/easyavr-tech-002-security-reliability`。
- 基线测试：`cd server && go test ./...` 全绿；`go vet ./...` 零输出；`cd web && npm run build` 通过（仅既有 vendor chunk 体积警告）。
- 状态：**已完成并通过验证**。

## 尚未执行的任务（按本次指令暂缓）

| 任务 | 状态 |
|---|---|
| TASK-011 AI Runner 并发可靠性 | 已完成并通过验证（本批次） |
| TASK-012 快照父目录符号链接竞态 | 已完成并通过验证（Linux；Windows 全仓构建受既有 storage 问题限制） |
| TASK-013 PostgreSQL 真实备份恢复 | 已完成并通过验证（隔离测试库真实演练） |
| TASK-014 GB28181 真实设备互通 | 待执行 |
| TASK-015 视频稳定性与压力测试 | 待执行 |

---

## TASK-011：AI Runner 并发关闭与启动状态一致性修复

状态：**已完成并通过验证**（代码与测试均已提交；尚未人工审查）。

### 修改文件

- `server/internal/ai/runner.go`
- `server/internal/ai/runner_lifecycle_test.go`

未改动：`runner_run.go`、`runner_test.go`、`runner_reliability_test.go`、其他包、前端、部署脚本、生产配置。

### 关键实现

1. **拆分关闭信号**：新增 `shutdownStarted`（`Done()` 仍表示关闭开始，保持外部语义兼容）与 `shutdownDone`；`shutdownDone` 通过 `defer close` 仅在第一次 `Close()` 完成全部清理（含超时路径）后关闭。并发/重复 `Close()`/`Shutdown()` 均等待 `shutdownDone`，不再提前返回。
2. **Start/Close 竞争回滚**：`Start()` 在写 `running` 前保存原 `status`/`enabled`；若写入后因 Runner 关闭无法启动，调用 `rollbackStartStatus` 做**条件更新** `WHERE id=? AND status='running'`，避免覆盖并发写入的新状态。回滚失败计入 `taskStatusFailures` 并输出 `[ai] ALERT`。
3. **启动实例归属**：`runnerState` 新增 `replaced bool`，区分"被更新的 Start 取代"与"被 Close 清理"，回滚仅在未被取代时执行。DB 写操作不持有 Runner 全局锁。
4. **测试钩子**：新增未导出的 `startDBUpdatedHook`，在数据库状态落库后、启动检查前调用，用于确定性复现 Start/Close 竞争。

### 新增测试（runner_lifecycle_test.go）

- `TestLifecycleConcurrentCloseWaitsForCompletion`：并发 Close 全部等待清理完成；`Done()` 在关闭开始即关闭。
- `TestLifecycleStartCloseDBStateConsistency`：通过钩子在 DB 写入后立刻关闭 Runner，断言任务状态回滚为 `stopped`、`enabled` 保持、运行记录为 0。
- `TestLifecycleConcurrentStartCloseStress`：100 轮 Start/Close 并发压力，断言无虚假 running、无重复运行记录、`inflight`/`tasks`/`chSems` 无残留。

调试中发现并修正：初始回滚的归属判断使用 `r.tasks[taskID]==state`，但 Close 会清空 tasks map，导致误判；改用 `!state.replaced`。

### 执行过的测试与结果

| 命令 | 结果 |
|---|---|
| `gofmt -l server/internal/ai` | 无输出（干净） |
| `cd server && go test ./internal/ai -count=1` | PASS |
| `cd server && go test ./...` | PASS |
| `cd server && go vet ./...` | PASS（零输出） |
| `cd server && go test -race ./internal/ai/... -count=1` | PASS |

### 遗留问题

- 本批次未执行 TASK-012～TASK-015（按本次指令）。
- `go test -race ./...` 全仓与跨平台构建未在本批次运行（留待总体验收批次）。
- 真实环境（GB28181 设备、视频压力、PostgreSQL 真实恢复）尚未验证。

---

## TASK-012：消除快照父目录符号链接竞态

状态：**已完成并通过验证**（Linux 实机；Windows 全仓交叉编译被既有 `internal/storage` 问题阻断，见遗留）。

### 修改文件

- 新增 `server/internal/snapshot/openat_linux.go`（`//go:build linux`）
- 新增 `server/internal/snapshot/openat_other.go`（`//go:build !linux`）
- 改 `server/internal/snapshot/service.go`
- 改 `server/internal/server/handler_snapshot.go`
- 改 `server/internal/snapshot/access_test.go`、`server/internal/server/handler_snapshot_test.go`
- 改 `server/internal/server/tech002_regression_test.go`（修复既有回归用例的时序脆弱点）

### 关键实现

1. `Service.OpenSnapshot(rel) (*os.File, error)`：返回已安全打开的文件句柄，先做词法校验（非空/非绝对/无 `..` 逃逸），打开后 `f.Stat` 强制普通文件；错误仍只映射到 `ErrPathOutside`/`ErrPathNotFound`（后者包 `fs.ErrNotExist`），不泄露本地路径。
2. Linux 优先 `unix.Openat2`：固定根目录 fd，`RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS`，flags `O_RDONLY|O_CLOEXEC|O_NOFOLLOW|O_NONBLOCK`。`ENOSYS`/`EINVAL` 安全降级到 `os.OpenRoot`，**不退回无约束 `os.Open`**；其他 errno 按逃逸/符号链接/不存在/权限分别映射，不再统一当"文件不存在"。
3. 非 Linux 使用 `os.Root` 受限方案，注释明确安全边界（可跟随根内符号链接、无 magic link 保证，但绝不逃逸根目录）。
4. HTTP 层 `snapshotSignedAccess` 直接调用 `OpenSnapshot`，`serveSnapshotFile(c, rel, f)` 用已打开句柄 `http.ServeContent`，不再按字符串路径重开、不再 `c.File`。`ResolvePath` 保留给保留期清理等非读取场景。

### 新增/调整测试

- 快照包：`TestOpenSnapshotRejectsParentSymlink`、`TestOpenSnapshotRejectsFinalSymlink`、`TestOpenSnapshotRejectsTraversal`、`TestOpenSnapshotRejectsMagicLink`、父目录与末级文件并发替换各 **1500 次**（外部文件放唯一标识，断言绝不读到根目录外内容）、`TestOpenSnapshotRegularFileOnly`；环境不支持符号链接/FIFO/`/proc/self/fd` 时按规则 SKIP，本环境无 SKIP。
- HTTP：`TestSnapshotHTTPUsesOpenedHandle`（替换路径内容后仍返回已打开句柄的原始字节）。
- 修复既有 `TestTech002RegressionRunRecordConsistency` 的时序脆弱点：`tech002WaitRuns` 原先把"运行记录行出现"当作完成，在 `-race` 下偶尔先于终态返回；现等待记录进入终态（succeeded/failed/timeout/stopped 且 FinishedAt 非空），不跳过测试。

### 执行过的测试与结果

| 命令 | 结果 |
|---|---|
| `gofmt -l server/internal/snapshot server/internal/server/handler_snapshot*.go` | 无输出 |
| `cd server && go test ./...` | PASS |
| `cd server && go vet ./...` | PASS |
| `cd server && go test -race ./internal/snapshot/... ./internal/server/... -count=1` | PASS（修复时序用例后） |
| `cd server && go test -race ./internal/server -run TestTech002RegressionRunRecordConsistency -count=3` | PASS |
| `cd server && GOOS=linux GOARCH=amd64 go build ./...` | PASS |
| `cd server && GOOS=windows GOARCH=amd64 go build ./internal/snapshot/...` | PASS |

### 遗留问题

- `GOOS=windows GOARCH=amd64 go build ./...` 仍失败于**既有** `internal/storage/storage.go` 的 `syscall.Statfs_t`（TECH-001 TASK-010 引入），非本次改动；本批次按写入范围未修改它，留待单独批次修复。
- TASK-013～TASK-015 按本次指令未执行；前端无改动，未执行 `npm run build`。

---

## TASK-013：PostgreSQL 真实备份恢复与一致性验证

状态：**已完成并通过验证**（本机隔离一次性测试库真实演练；生产与业务库未连接）。

### 修改/新增文件
- `deploy/scripts/verify-tech003-postgres.sh`（新增，525 行）：显式 `PG_TEST_DSN` 安全门，维护库名白名单（仅允许自建 `easyavr_pg003_*`，拒绝业务库/生产样式库名）；自建自删源/目标库；用平台 AutoMigrate 建表并经 API/GORM 加密 hook 写入用户/设备/通道/Provider/AI 任务/快照/录像/审计/平台配置；运行 `pg_backup.sh`、`pg_restore.sh` dry-run 与 `--yes`；比较列/索引/约束/行数/关联/密文；验证 v1→v2 密钥轮换与错误密钥 fail-closed；trap 清理进程、临时文件与测试库。
- `deploy/README.md`：登记演练脚本、脱敏用法、安全边界与报告链接。
- `docs/testing/postgres-backup-restore-report.md`（新增）：真实命令、退出码、两轮结果、问题修复、SKIP/待验证清单。
- `server/internal/recording/catalog_test.go`：修复既有时间依赖夹具（`fakeZLM` 现在仅在首次请求的日期返回录制文件，避免当前日期下 `syncRecent` 产生重复编目）；未跳过测试。

### 真实演练结果（本机 docker easyavr-postgres，一次性库）
- 未传 `PG_TEST_DSN`：`exit 2 / SKIP=1`；指向生产样式库名：建库前拒绝，`exit 1`。
- 第一轮演练暴露脚本自身 3 类缺陷（目录查询排序/类型拼接导致假 PASS、`gb.enabled` 键名错误、脱敏审计误报），修复后第二轮：**`exit 0 / PASS=70 FAIL=0 SKIP=0 CLEANUP_WARN=0`**。
- 实测：48 张表、550 列、149 索引、49 约束一致；备份目录 445 行、权限 0600；dry-run 零写入；10 张关键表行数/关联一致；错误密钥 fail-closed 且不改写密文；v1→v2 共 2 行重加密；日志无秘密字面值；两个一次性库与 18009/18010 进程均已清理。

### 执行过的测试与结果
- `bash -n deploy/scripts/*.sh scripts/*.sh`：PASS。
- `cd server && go test ./...`：PASS（含修复后的 recording 套件）；`go vet ./...`：零输出；`cd web && npm run build`：PASS（无前端改动）。
- 真实 PG 演练命令与退出码见 `docs/testing/postgres-backup-restore-report.md`。

### 遗留问题（报告中已标记）
- 生产实例/业务库按约束未演练；`transaction_timeout` 版本偏差告警分支本次退出 0 但未单独取证；pgvector、大规模/并发写入、外部 KMS 取回主密钥流程未覆盖。

---

## 补充审查 TASK-016：Linux 快照安全降级路径

状态：**已完成并通过验证**。

- 文件：`server/internal/snapshot/openat_linux.go`（修改）、`server/internal/snapshot/openat_linux_test.go`（新增）。
- `openFallback` 不再通过字符串路径 `os.OpenRoot(r.dir)` 重新打开根目录；改为基于 `newSnapshotRoot()` 固定的 `r.fd`，用逐级 `unix.Openat(O_DIRECTORY|O_CLOEXEC|O_NOFOLLOW)` 解析各中间目录，末级使用 `snapshotOpenFlags` 打开，全部中间 fd 及时关闭。
- `splitFallbackPath` 做词法自保：拒绝绝对路径、空段、`.`/`..`、尾部斜杠；任何 errno 继续经 `snapshotOpenError` 区分 ENOENT / 逃逸 / 符号链接 / 权限，不统一当作文件不存在。
- 新增测试钩子 `forceFallbackForTest`；测试覆盖降级端到端、父目录/末级符号链接、`../` 逃逸、目录/FIFO 非普通文件、根目录重命名后经固定 fd 访问、根目录并发 rename/replace 1000 轮竞态（不得读到根外哨兵内容）。

---

## 补充审查 TASK-017：PostgreSQL 演练脚本 DSN 主机安全门

状态：**已完成并通过验证**（门禁测试；未做真实 PG 恢复）。

- 文件：`deploy/scripts/verify-tech003-postgres.sh`（修改）、`deploy/scripts/verify-tech003-dsn-gate-test.sh`（新增）、`deploy/README.md`、`docs/testing/postgres-backup-restore-report.md`。
- 新增 `dsn_gate()`：默认仅允许 `localhost` / `127.0.0.1` / `::1`（含 `[::1]`、`%5B%3A%3A1%5D`、`?host=%3A%3A1`、大小写与端口规范化）；非回环默认拒绝，日志只输出固定原因标签，不打印 DSN/口令。
- 远程需显式 `PG_TEST_ALLOW_REMOTE=1` 且 `PG_TEST_REMOTE_HOST`、`PG_TEST_REMOTE_PORT`、`PG_TEST_MAINTENANCE_DB=postgres` 三项与 DSN 完全一致；库名检查保留，远程仅靠库名不得放行。文档明确：即便打开开关也只允许本人明确创建的隔离测试库，绝不指向生产。
- 新增 `PG_TEST_DSN_CHECK_ONLY=1` 无连接门禁自检（不 psql/不建库/不建目录，退出 0 允许 / 1 拒绝 / 2 缺 DSN）；新增 11 用例门禁测试脚本，断言远程拒绝、三类回环通过、开关+确认通过、确认不匹配/缺失拒绝、业务库名拒绝、无 DSN SKIP、无秘密泄漏。
- 结果：`bash -n deploy/scripts/*.sh` PASS；`bash deploy/scripts/verify-tech003-dsn-gate-test.sh` → `PASS=66 FAIL=0`；无 DSN SKIP exit 2。真实 PG 恢复演练按任务约束未执行。

---

## 补充审查 TASK-011：Start/Stop/Close 三方并发压力测试

状态：**已完成并通过验证**。

- 文件：`server/internal/ai/runner_lifecycle_test.go`（仅新增测试与 `fmt`/`runtime` import）。
- 新增 `TestLifecycleConcurrentStartStopCloseStress`：100 轮，每轮新 DB/runner/task；先 Start 建立活跃循环与运行中记录，再用 barrier 同时释放重复 Start + Stop + Close。
- 断言：关闭后 `inflight`/`tasks`/`chSems` 清空、runner 已关闭、Close 后 20ms 窗口无新的 analyzer 调用与运行记录、每轮至多一个运行记录/调用、所有运行记录终态且含 `FinishedAt`、任务 `Enabled` 保持且 `Status != running`、`runtime.NumGoroutine()` 在 2s 内回落到基线+32 内。
- 验证：`go test ./internal/ai -run Lifecycle -count=1`、`go test -race ./internal/ai -run Lifecycle -count=1`、`go test -race ./internal/ai -run TestLifecycleConcurrentStartStopCloseStress -count=5`（500 轮）、`go vet ./internal/ai` 全绿。

## 补充审查总体验证记录

- `gofmt -l server/internal/snapshot server/internal/ai`：干净。
- `go test ./...`、`go vet ./...`：PASS / 零输出。
- `go test -race ./...`：首轮曾出现一次瞬时 FAIL（日志未保留具体用例）；随后两次全量（`-count=1`）与一次定向三包（server/snapshot/ai）重跑均 EXIT=0，疑为既有测试的时序抖动，列为待观察风险，未跳过或屏蔽任何测试。
- `GOOS=linux GOARCH=amd64 go build ./...`：PASS；`GOOS=windows GOARCH=amd64 go build ./internal/snapshot/...`：PASS（仓库既有 `internal/storage` syscall.Statfs 导致 Windows 全量构建仍失败，非本次引入）。
- `bash -n deploy/scripts/*.sh scripts/*.sh`：PASS；DSN 门禁脚本 PASS=66 FAIL=0。
- 无前端改动，`npm run build` 本批次未重跑（基线已通过）。
