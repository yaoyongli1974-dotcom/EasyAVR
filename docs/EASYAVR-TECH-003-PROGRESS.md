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
| TASK-012 快照父目录符号链接竞态 | 待执行 |
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
