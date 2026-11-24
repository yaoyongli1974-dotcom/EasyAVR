# EASYAVR-TECH-002 第二轮安全漏洞修复与可靠性专项技术改造报告

## 1. 代码基线

- 仓库：`/mnt/data/project/easyavr`
- 起始基线：`main @ 287a007cd4624e7c8eeaef1e8ec2c87e5d4f1468`（已推送）
- 本轮工作分支：`feature/easyavr-tech-002-security-reliability`（从基线创建，未自动推送）
- TASK-000 基线检查：工作区干净；`go test ./...`、`go vet ./...`、`npm run build` 全部通过；检查报告见 `PROGRESS.md` 的 2026-11-24 条目。

## 2. 任务完成状态

状态口径：**已完成并通过验证** / **已完成但待真实环境验证** / **未完成**。

| 任务 | 内容 | 状态 |
|---|---|---|
| TASK-000 | 基线检查、工作区保护、测试记录 | 已完成并通过验证 |
| TASK-001 | 凭据加密 `enc:` 前缀绕过 | 已完成并通过验证 |
| TASK-002 | 数据库备份与加密主密钥分离 | 已完成但待真实环境验证（SQLite 往返实测；PG 仅静态/桩验证，真机恢复待做） |
| TASK-003 | 快照签名 URL 实时权限复核 | 已完成并通过验证 |
| TASK-004 | AI 任务启动状态一致性 | 已完成并通过验证 |
| TASK-005 | AI 运行记录创建失败不得执行 | 已完成并通过验证 |
| TASK-006 | 生产环境密钥强度校验 | 已完成并通过验证 |
| TASK-007 | 快照路径穿越/符号链接逃逸 | 已完成并通过验证 |
| TASK-008 | AI Runner 生命周期 | 已完成并通过验证 |
| TASK-009 | 健康检查反映 AI 调度真实状态 | 已完成并通过验证 |
| TASK-010 | 第二轮安全回归测试 | 已完成但待真实环境验证（真实 PG 恢复按只读约束显式 SKIP） |

## 3. 修改文件与函数清单

### 批次 1（TASK-001/002）
- `server/internal/security/credential.go`：`Encrypt()` 删除明文前缀跳过；`EncryptStrings()` 改为基于真实解密的幂等判断（无前缀信任）；`ParseStrictKey()`/`NewStrict()` 见批次 4。
- `server/internal/store/credential_migrate.go`：`migrateValue` 改为真实解密判定，存量 `enc:` 开头明文按明文加密并校验，结构合法却无法解密的数据 fail-closed。
- `server/internal/model/credentials.go`、`credential_test.go`、`model/credentials_test.go`：钩子契约注释与前缀绕过/重复保存测试。
- `deploy/scripts/backup.sh`：DB 备份写入 `db/`，配置备份走显式白名单写 `config/`，不再备份整份 env；保留一致性备份、`gzip -t`、`integrity_check`、flock、KEEP 轮转与权限控制。
- `deploy/scripts/restore.sh`、`pg_backup.sh`、`pg_restore.sh`、`deploy/README.md`、`deploy/env.example`、`deploy/cron.d/easyavr`：三层备份模型、主密钥人工恢复提示、目录与命令更新。

### 批次 2（TASK-003）
- `server/internal/server/handler_snapshot.go`：`(*App).snapshotSignedAccess` 使用 Token 内 UserID，按数据库当前用户（`Enabled`/`Username`/`Role`）重建 claims，调用 `snapshotChannelAllowed` 实时复核分组/角色/policy；401/403/404 语义与无路径泄露。
- `server/internal/server/handler_snapshot_test.go`：12 项权限矩阵测试。

### 批次 3（TASK-004/005）
- `server/internal/ai/runner.go`：`Start()` 在启动 goroutine 前完成所有失败检查，失败即 cancel/close/dropState 并返回错误；并发启动等待启动结果；新增 `RunTimeout` 终态与持久化失败计数器/告警。
- `server/internal/ai/runner_run.go`：`doWork()` 先持久化 `AITaskRun` 成功后才执行分析；每次重试生成独立 run；超时落明确终态；finalizer 处理更新失败。
- `server/internal/ai/runner_test.go`、`runner_reliability_test.go`：状态一致性、并发、重复启动、创建失败、重试独立记录、恢复不重放成功历史。

### 批次 4（TASK-006）
- `server/internal/security/credential.go`：`ParseStrictKey()` 仅接受 base64/hex 32 字节且拒绝弱模式；`NewStrict()` 仅主密钥严格、历史密钥宽松（仅用于解密轮换）。
- `server/internal/config/config.go`：`Validate()` 生产环境改用严格解析；`cmd/easyavr/main.go` 生产环境改用 `NewStrict`；`config_test.go`、`credential_test.go` 补充弱密钥拒绝/历史解密/互操作测试。

### 批次 5（TASK-007/008）
- `server/internal/snapshot/service.go`：`ResolvePath()` 规范化并拒绝逃逸根部的符号链；内部 `resolve(rel,requireExists)` 供清理使用；`ErrPathNotFound`。
- `server/internal/server/handler_snapshot.go`：`serveSnapshotFile()` 用 `Lstat→Open→Lstat+SameFile→http.ServeContent`，替换检查与使用之间的竞态窗口，非普通文件拒绝。
- `server/internal/ai/runner.go`、`runner_run.go`：统一生命周期 context；`Close()/Shutdown()` 幂等阻塞（上限 `ShutdownTimeout`）；关闭后拒绝任务与数据库写入；`Recover()` 串行化并返回错误；取消后停止心跳/重试/等待者。
- `server/internal/server/app.go`、`cmd/easyavr/main.go`：Recover 错误日志；关闭流程不再固定睡眠。
- 新增 `runner_lifecycle_test.go`、`access_test.go`/`handler_snapshot_test.go` 符号链与并发删除用例。

### 批次 6（TASK-009/010）
- `server/internal/ai/runner_health.go`（新）：`HealthStats` 暴露活跃/等待任务、运行记录心跳、连续失败、长期运行、恢复结果、关闭状态；恢复错误脱敏为类别，不泄露 DSN/密钥。
- `server/internal/server/handler_health.go`：`checks.ai` 保持原字段并新增聚合/详细统计；AI 异常只产生 warning，不使 readiness 失败；DB 不可用时 `/readyz` 仍 503。
- `server/internal/server/tech002_regression_test.go`、`tech002_backup_test.go`（新）：13 组回归测试覆盖任务书 15 类场景；SQLite 备份恢复真实往返；PG 静态检查（真实恢复 SKIP）。
- `deploy/scripts/verify-tech002.sh`（新）：一键 41 项 PASS 的脚本化验证入口。
- `server/internal/storage/storage_test.go`：将动态磁盘容量断言改为带容差比较（修复 `-race` 全量下偶发失败，未跳过测试）。

## 4. 数据库与 API 变化

- **数据库结构**：本轮无新增表或字段。迁移层行为变更：`credential_migrate.go` 不再靠明文前缀识别密文，避免 `enc:` 明文绕过；旧密文与旧密钥轮换保持兼容。
- **API 保持兼容**：无删除/改名的现有路由。`/api/v1/healthz`、`/readyz`、`/health/details` 保持原响应结构，`checks.ai` 只新增字段；签名快照 URL 行为不变但增加实时权限复核（用户被禁用/撤权后旧 URL 失效）；AI Runner `Recover()` 由无返回值改为 `error`（Go 内部 API，`go a.runner.Recover()` 调用兼容）。

## 5. 安全问题修复摘要

- 所有非空明文（含 `enc:` 开头）都经过 AES-256-GCM 加密；GORM 重复保存不会二次加密；未知 kid/篡改/错误密钥 fail-closed，不回退明文。
- 数据库备份目录不含凭据主密钥；配置备份为白名单且拒绝敏感变量；主密钥必须人工独立备份，恢复脚本绝不生成新密钥替代。
- 快照 URL 是短期凭证，不再代替授权：Token 用户必须实时存在/启用/具备当前通道权限；路径与符号链接均限制在快照根目录内。
- 生产环境要求随机 32 字节主密钥（base64/hex），拒绝口令派生与弱模式；历史密钥仅用于解密轮换。
- AI 任务每次实际执行都有运行记录；记录创建失败绝不执行分析；状态、数据库与执行状态一致。
- 健康检查只报告状态，不序列化密码、Token、DSN 或配置值。

## 6. 测试结果

| 命令 | 结果 |
|---|---|
| `cd server && go test ./...` | 全绿 |
| `cd server && go vet ./...` | 零输出 |
| `cd server && go test -race ./...` | 全绿（修复了一处磁盘容量测试的动态断言） |
| `cd web && npm run build` | 通过（仅既有 vendor chunk 体积警告） |
| `bash -n deploy/scripts/*.sh` | 全部通过 |
| `bash deploy/scripts/verify-tech002.sh` | PASS=41 FAIL=0 SKIP=1 |
| `cd server && go test ./internal/server -run Tech002Regression -count=1` | 13 组用例全 PASS（2 个子用例 SKIP：真实 PG） |
| SQLite 备份→恢复真实往返 | 通过（dry-run 无写入、`--yes` 数据一致、损坏备份拒绝、轮转正常） |
| PostgreSQL 备份/恢复 | 静态与桩验证通过；真实恢复按只读约束 SKIP |

## 7. 未解决问题与人工操作

1. **真实 PostgreSQL 恢复演练**：本机仅提供只读账号 `easyavr_ro`，按任务约束未使用可写业务库；需在隔离环境用一次性库完成。
2. **凭据主密钥独立备份**：本轮只实现"不落 DB 备份目录"和人工流程说明；生产密钥必须离线/外部密钥管理设施备份，丢失后加密凭据不可恢复。
3. **生产配置与密钥轮换**：`EASYAVR_ENV=production` 下的密钥、JWT/管理员口令、GB/集群密钥的实际配置需运维人工审批，本轮未改生产服务器。
4. **真实设备与浏览器联调**：RTSP/GB28181 真机烟雾测试、快照/密钥 UI 的点击路径未做。
5. **已知遗留策略**：录像"唯一未归档副本"保护仅在库中存在至少一条归档记录时启用；磁盘压力下快照清理只淘汰已过期项。

## 8. Git 提交记录（分支 `feature/easyavr-tech-002-security-reliability`）

- `dfedba8` chore: TASK-000 baseline check for TECH-002
- `7f2723f` feat/fix: TASK-001 + TASK-002（凭据前缀绕过、备份与密钥分离）
- `66a703b` fix: TASK-003 快照签名实时权限复核
- `09ed20c` fix(ai): TASK-004 + TASK-005（Runner 一致性）
- `396908a` docs: TECH-002 第三批进度
- `d78a9ae` feat(security): TASK-006 生产密钥强度
- `c1fa5a7` fix(storage,ai): TASK-007 + TASK-008（符号链接安全、Runner 生命周期）
- 最后一批（TASK-009/010 + 本报告）：见 `git log -1`

**本轮未推送**，按任务书停在分支上等待人工审查。

## 9. 后续建议

1. 在隔离环境完成真实 PG 备份→恢复 + 密钥轮换（v1→v2）演练。
2. 真实 RTSP/GB28181 设备接入并跑 `scripts/smoke-video-e2e.sh`。
3. 浏览器逐项点击验证「留空不改 / 勾选清除 / 输入新密钥」与磁盘健康卡片。
4. 审查并决定是否收紧录像唯一副本保护与磁盘压力淘汰策略。
