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
| TASK-013 PostgreSQL 真实备份恢复 | 待执行 |
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
