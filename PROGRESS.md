# PROGRESS

### 2026-10-07
- **已完成**：搭建 EasyAVR MVP（AI 原生视频融合与智能视频资源管理平台）。三大平面（设备接入/视频中台/AI 智能中台）+ 两中心（视频资源中心/AI 事件中心）+ 开放层全部落地：
  - 后端 Go（Gin + GORM + 纯 Go SQLite + JWT）：设备/通道 CRUD、ZLMediaKit 拉流与播放地址、CV/VLM/LLM Provider 抽象、AI 任务调度与事件中心。
  - 前端 Vue3 + TS + Element Plus：登录、总览、设备接入、实时直播（FLV/HLS/WebRTC）、AI 能力、AI 任务、AI 事件、资源中心。
  - 基础设施与文档：docker-compose(ZLMediaKit)、Makefile、README、docs/architecture.md、examples/cv-worker、AGENTS.md。
  - 验证通过：`make test`（含平台闭环测试）、`go vet`、`web` 类型检查与构建、实机烟雾测试。
- **当前状态**：完成
- **下一步**：
  1. 二期：GB28181/SIP 信令接入、录像计划与回看、快照。
  2. 语义检索（事件向量化 + 向量库）。
  3. 告警通知（邮件/Webhook）与多屏播放。
  4. 视需要提交 git 仓库。

### 2026-10-07（二期）
- **已完成**：完成二期（GB28181/SIP 信令、录像计划与回看、快照）。
  - GB28181：自研 Go SIP 信令服务器（`internal/gb28181`）——REGISTER 摘要鉴权、Keepalive 心跳与离线检测、Catalog 目录自动建设备/通道、Alarm 告警入事件中心、实时 INVITE 点播（`openRtpServer` 由 ZLMediaKit 收流）；设备接入页面新增「GB28181 接入」，展示平台 SIP 参数与已注册设备。
  - 录像：ZLM 录像控制（startRecord/getMp4RecordFile）、录像编目与按日期检索、回放/下载、按天/时段自动录像计划与保留期清理；新增「录像回看」页与通道录像计划配置。
  - 快照：ffmpeg 抽帧、定时抓拍调度、编目与 `/snapshots/*` 静态托管；新增「快照中心」页与通道抓拍配置。
  - 抽取共享抽帧包 `internal/media`，供 AI 与快照复用。
  - 验证通过：`go test ./...`（新增 GB28181 UDP 握手测试、录像计划匹配测试、二期接口测试）、`go vet`、前端 build、带 GB 启用的实机烟雾测试。
- **当前状态**：完成
- **下一步**：
  1. 三期：GA/T1400、EHOME/ISUP、国密 GB35114、GB28181/1400 级联、集群。
  2. 语义检索（事件向量化 + 向量库）。
  3. 告警通知（邮件/Webhook）与多屏播放。
  4. 视需要提交 git 仓库。

### 2026-10-07（修复 ZLM 离线）
- **已完成**：修复总览页「ZLMediaKit 离线」。原因是该 ZLM 镜像的 API secret 是随机构建的，且 ZLM 在 secret 为官方默认值时会自动重生成，导致后端默认 secret 鉴权失败（`Please login first`）。方案：`deploy/zlm/config.ini` 固定一个非默认 secret（`EasyAVRzlmSecret2024x`）并在 `docker-compose.yml` 挂载，后端默认 `EASYAVR_ZLM_SECRET` 同步为该值。
  - 验证：`docker compose up -d --force-recreate` 后容器内 secret 保持不变，API 返回 `code:0`；临时启动后端实例 `/video/stats` 显示 `zlmHealthy:true`。
- **当前状态**：完成
- **下一步**：
  1. 重启正在运行的 `make run` 以加载新默认 secret（或显式设置 `EASYAVR_ZLM_SECRET=EasyAVRzlmSecret2024x`）。
  2. 三期：GA/T1400、EHOME/ISUP、国密 GB35114、级联、集群。
  3. 语义检索（向量库）、告警通知、多屏播放。

### 2026-10-07（添加设备表单联动）
- **已完成**：设备添加表单联动——切换「协议」自动带出该协议默认端口（rtsp 554 / rtmp·rtmp_push 1935 / onvif 80 / gb28181 5060 / ehome 7660）；切换「厂商」自动带出该厂商默认协议与端口（海康/大华/宇视 → rtsp 554）。
- **当前状态**：完成
- **下一步**：三期功能（GA/T1400、EHOME/ISUP、国密、级联、集群）。

### 2026-10-08（三期第一批）
- **已完成**：按“先易后难、先闭环后协议”实现三期第一批（多项）。
  - 多屏播放：1/4/9/16 分屏、轮播、播放记忆（WebRTC）。
  - 告警通知：`internal/notify` 渠道（Webhook/邮件）+ 规则（级别/类型/能力匹配），事件经 `eventSink` 异步投递，支持测试发送。
  - 语义检索：`internal/ai` 嵌入客户端（OpenAI /v1/embeddings）+ `internal/search` 余弦检索，未配置嵌入模型自动退化为关键词；新增 `embedding` 类型 AI 能力。
  - 集群：`internal/cluster` 节点注册/心跳/共享密钥，`Device.NodeID` 设备归属；新增集群页。
  - GB28181 级联：`internal/gb28181` 向下（注册/心跳）与新增级联/白名单 CRUD 与页面页签。
  - GA/T1400：`internal/ga1400` VIID 注册/心跳/结构化/告警 XML 入库（事件入中心）。
  - EHOME/ISUP：`internal/ehome` UDP CMS/SMS 接入端点（完整私有编解码待对接厂商规范）。
  - GB35114：白名单与配置基础。
  - 前端新增：多屏播放、智能检索、告警通知、集群页；GB28181 页增加级联/白名单页签；AI 能力支持 embedding。
  - 验证通过：`go test ./...`（新增 notify/search/cluster/ga1400 单测）、`go vet`、前端 build、全功能启用的实机烟雾测试（GB/GA1400/EHOME/集群/通知/检索）。
- **当前状态**：待续（EHOME 完整编解码、GB35114 国密、GA/T1400 级联订阅、集群设备级调度未完成）
- **下一步**：
  1. 三期剩余：EHOME 二进制协议编解码、GB35114 国密证书认证、GA/T1400 上下级级联与订阅、集群设备级资源调度。
  2. APP 端与第三方开放 API 鉴权细化。

### 2026-10-08（三期第二批）
- **已完成**：攻坚三期剩余中的三项。
  - GA/T1400 上下级级联（`internal/ga1400/cascade.go`）：向上级视图库注册/心跳/推送 AI 事件（含 HTTP Digest 摘要鉴权），订阅下级视图库并记录订阅；新增 `/ga1400/cascades`、`/ga1400/subscriptions` API 与「GA/T1400 级联」页。
  - 集群设备级调度（`internal/cluster`）：`AssignNode()` 最少负载分配、`NodeLoads()` 各节点设备统计；设备创建自动归属节点，新增 `/cluster/stats`，集群页展示设备分布。
  - GB35114 国密（`internal/gb35114`，引入 `github.com/tjfoc/gmsm`）：SM2 平台 CA 生成/持久化、用平台 CA 签发设备证书（兼容 x509 解析出的 ECDSA-on-SM2 公钥）、SM3 摘要；新增 `/gb35114/*` API 与「国密 GB35114」页（生成/下载平台证书、签发 CSR、SM3 工具）。
  - 验证通过：`go test ./...`（新增 gb35114 证书链路、ga1400 级联注册/推送/摘要/订阅、cluster 最少负载分配测试）、`go vet`、前端 build、实机烟雾测试（SM3("abc") 与标准值一致）。
- **当前状态**：待续（仅剩 EHOME 完整二进制编解码需厂商协议规范）
- **下一步**：
  1. EHOME/ISUP 二进制帧编解码（需海康协议规范）。
  2. GB35114 双向认证与 SIP TLS 通道；GA/T1400 订阅自动续订与全量同步。
  3. APP 端与第三方开放 API 鉴权细化。

### 2026-10-08（三期第三批）
- **已完成**：完成三项「待完善」：
  - GA/T1400 订阅自动续订与全量同步：`GA1400Subscription` 新增有效期/续订计数，`CascadeService.ensureSubscription` 在到期窗口内自动续订；向上级联按 `LastSyncAt` 增量全量同步本地事件，新增 `POST /ga1400/cascades/:id/sync` 与页面「同步」按钮，订阅表展示续订次数/到期时间。
  - GB35114 设备证书双向认证：新增 `model.GB35114Cert`，`Service.EnrollDevice`（签发+登记）、`VerifyCert`（校验平台 CA 签名 + 有效期 + 吊销状态）、`RevokeCert`、`Fingerprint`（SM3 指纹）；新增 `/gb35114/enroll|verify|certs|certs/:id/revoke` API 与页面登记/校验/吊销/证书列表。**修复** gmsm `CreateCertificate` 的 SM2 双重哈希问题（需显式设置 `SignatureAlgorithm: SM2WithSM3`），使证书可被 `CheckSignatureFrom` 正确验证。
  - 开放 API 密钥鉴权：新增 `model.APIKey`（仅存 SHA-256，展示前缀）与 `internal/auth/apikey.go`；`apiKeyRequired(scope)` 中间件支持 `X-API-Key` / `Authorization: ApiKey`，scope 读写校验；新增 `/api/v1/apikeys` 管理 CRUD 与 `/api/v1/open/*` 第三方/APP 只读接口 + 事件上报，新增「开放 API」页面（生成密钥仅显示一次、启用/停用/删除、最近使用）。
  - 验证通过：`go test ./...`（新增订阅续订、全量同步、证书登记/校验/吊销/异地 CA 拒绝、开放 API scope 测试）、`go vet`、`web` 类型检查与构建。
- **当前状态**：待续（仅剩 EHOME 完整二进制编解码需厂商协议规范；GB35114 SIP TLS 加密通道）
- **下一步**：
  1. EHOME/ISUP 二进制帧编解码（需海康协议规范）。
  2. GB35114 SIP TLS 加密通道与设备侧双向证书握手。
  3. 开放 API 文档（OpenAPI/Swagger）与调用配额/审计。

### 2026-10-08（三期第四批）
- **已完成**：完成「开放 API 文档 + 配额/审计」与「GB35114 SIP TLS」。
  - 开放 API 配额与审计：`model.APIKey` 新增 `RateLimit`/`QuotaPerDay`/`UsedToday`/`QuotaResetAt`，新增 `model.APIRequestLog`；`apiKeyRequired` 中间件实现每分钟固定窗口限速（内存 `internal/openapi.Limiter`）、每日配额（跨天自动重置）与全量调用审计（方法/路径/状态/耗时/IP），超限返回 429 + `Retry-After`；新增 `GET /apikeys/stats`、`GET /apikeys/logs`、`GET /apikeys/:id/logs`。
  - 开放 API 文档：新增 `internal/openapi.Spec()`（OpenAPI 3.0，含安全方案与请求体 schema），`GET /api/v1/openapi.json` 公开访问；前端「开放 API」页新增 密钥/文档/审计 三个页签、统计卡片、按密钥用量与限速列、日志抽屉、限速与配额配置。
  - GB35114 SIP TLS：`gb35114` 新增平台双证书（签名+加密）生成/持久化 `PlatformTLS()`、设备双证书 `IssueDeviceTLS()`、`CAPool()`；`gb28181` 重构为 **Peer 抽象传输层**（UDP/TLS 统一收发，TLS 使用 Content-Length 分帧），新增 `StartTLS()` 基于 `gmsm/gmtls` 的 **GM/T 0024 安全 SIP**，支持 `RequireAndVerifyClientCert` 双向认证；配置 `EASYAVR_GB35114_SIP_LISTEN` / `EASYAVR_GB35114_SIP_REQUIRE_CLIENT`，`App.Run()` 自动拉起；前端 GB35114 页展示安全 SIP 状态。
  - 验证通过：`go test ./...`（新增 GM/TLS REGISTER 双向认证成功 + 无客户端证书被拒、限速 429、每日配额 429、审计/统计、OpenAPI 文档），`go vet`，前端类型检查与构建。
- **当前状态**：待续（仅剩 EHOME 完整二进制编解码需厂商协议规范）
- **下一步**：
  1. EHOME/ISUP 二进制帧编解码（需海康协议规范）。
  2. 开放 API 调用明细导出 / 按 IP 与时间范围筛选审计。
  3. 视需要提交 git 仓库。

### 2026-10-08（主动发现设备）
- **已完成**：设备接入新增「主动发现监控设备」。
  - 新增 `internal/discovery`：ONVIF **WS-Discovery** 多播探测（SOAP Probe → 解析 ProbeMatches 的 XAddrs/Scopes，推断 IP/端口/名称/厂商/型号/位置）与**网段 TCP 端口扫描**（554/8000/80，限 /24，256 并发上限，按 IP 去重并优先 554）。
  - 新增 `POST /api/v1/discovery/scan`（`mode`: onvif/subnet/all、`subnet`、`timeoutSec`），结果标注 `added`（该 IP 是否已接入）。
  - 前端「设备接入」页新增「主动发现」弹窗：选择方式/网段/超时，扫描结果表格可勾选（已接入禁用），填写统一协议/用户名/密码后一键导入（复用设备创建，自动建主通道）。
  - 验证通过：`go test ./...`（新增 ProbeMatches 解析、网段限制、主机枚举、发现接口校验单测/接口测试）、`go vet`、前端类型检查与构建。
- **当前状态**：完成
- **下一步**：
  1. EHOME/ISUP 二进制帧编解码（需海康协议规范）。
  2. 发现结果支持 ONVIF 设备鉴权探测（Media/GetProfiles 自动获取 RTSP 主/子码流地址）。
  3. 视需要提交 git 仓库。

### 2026-10-08（侧边菜单分组）
- **已完成**：侧边栏菜单项过多，改为分组下级菜单：`总览` + `设备接入`（设备管理/GB28181/GA1400/国密 GB35114）、`视频中心`（实时/多屏/录像/快照/资源）、`AI 智能中台`、`平台管理`（告警通知/集群/开放 API）。保留 `:default-active` 自动展开当前子菜单。
- **当前状态**：完成
- **下一步**：EHOME/ISUP 二进制帧编解码（需海康协议规范）；ONVIF 设备鉴权自动获取码流地址；视需要提交 git 仓库。

### 2026-10-08（可插拔数据库）
- **已完成**：数据库改为可插拔，默认体验不变。
  - `internal/store` 新增 `OpenWith(driver, dsn)`：`sqlite`（默认）/`postgres`（`gorm.io/driver/postgres` + 纯 Go `jackc/pgx`），`Open(path)` 保留为 SQLite 快捷入口；迁移与种子抽到 `migrate()`。
  - SQLite 启用生产参数：`WAL + busy_timeout(5000) + foreign_keys(1) + synchronous(NORMAL)`。
  - 配置新增 `EASYAVR_DB_DRIVER`（默认 sqlite）与 `EASYAVR_DB_DSN`（PostgreSQL 连接串），`Config.DatabaseDSN()` 选择生效 DSN；`cmd/easyavr` 改用 `store.OpenWith`。
  - 依赖：新增 `gorm.io/driver/postgres`（经 goproxy.cn 镜像拉取）。
  - 验证通过：`go test ./...`（新增驱动校验与 SQLite PRAGMA 单测）、`go vet`。README / docs / AGENTS 同步更新。
- **当前状态**：完成
- **下一步**：
  1. 如切 PostgreSQL，建议启用 `pgvector` 替换当前事件向量的 JSON+余弦实现。
  2. EHOME/ISUP 二进制帧编解码（需海康协议规范）。
  3. 视需要提交 git 仓库。

### 2026-10-08（PostgreSQL 本地联调）
- **已完成**：`docker-compose.yml` 新增可选 `postgres` 服务（`postgres:16-alpine`，profile: `postgres`，账号 easyavr/easyavr，数据卷 `easyavr-pgdata`，健康检查），默认 `make up` 仍只起 ZLM，互不影响；Makefile 新增 `db-up` / `db-down` / `run-pg`（`run-pg` 用 `EASYAVR_DB_DRIVER=postgres` + 本地 DSN 启动后端）。README 补充使用说明。
- **验证**：`docker compose config` 与 `docker compose --profile postgres config --services` 通过；`make help` 显示新目标。本机 Docker Hub 不可达，未能实拉镜像启动容器，PostgreSQL 运行路径待有镜像环境实测。
- **当前状态**：完成（容器实测受网络限制）
- **下一步**：EHOME/ISUP 二进制编解码；如切 PG 可启用 pgvector；提交 git 仓库。

### 2026-10-08（PostgreSQL 实测通过）
- **已完成**：重拉镜像成功并完成 PostgreSQL 端到端实测。
  - `docker compose --profile postgres up -d postgres`：`postgres:16-alpine` 拉取成功，容器 `easyavr-postgres` 健康（volume `easyavr-pgdata`）。
  - 以 `EASYAVR_DB_DRIVER=postgres EASYAVR_DB_DSN=postgres://easyavr:easyavr@127.0.0.1:5432/easyavr?sslmode=disable` 启动后端：自动迁移建 **21 张表**、种子管理员成功；`/api/v1/healthz` 返回 `code:0`，`/api/v1/auth/login` 正常签发 JWT。
  - 验证后已停止临时后端进程；PostgreSQL 容器保持运行（`make db-down` 可停）。
- **当前状态**：完成（SQLite 与 PostgreSQL 双路径均实测可用）
- **下一步**：EHOME/ISUP 二进制编解码；如切 PG 可启用 pgvector；提交 git 仓库。

### 2026-10-08（ONVIF 自动取流）
- **已完成**：推进 ONVIF，新增凭据探测与自动获取码流地址。
  - 新增 `internal/onvif`：极简 ONVIF SOAP 客户端，实现 `GetDeviceInformation`/`GetCapabilities`(Media XAddr)/`GetProfiles`/`GetStreamUri`；鉴权同时支持 **WS-Security UsernameToken（PasswordDigest）** 与 **HTTP Digest** 回退；解析 SOAP Fault。
  - 后端新增 `POST /api/v1/onvif/probe`（返回厂商/型号/固件/序列号 + 各媒体配置的 RTSP 地址）与 `POST /api/v1/onvif/import`（探测后按媒体配置自动创建主/子码流通道，SourceURL 为该配置 RTSP 地址；`sub` 名称识别为子码流）。
  - 前端「主动发现」弹窗：结果行新增「探测」（查看设备信息与码流）；导入区新增「ONVIF 自动取流」开关，开启后用统一凭据调用 ONVIF 导入。
  - 验证通过：`go test ./...`（onvif 客户端 Digest 鉴权 + 解析、SOAP Fault、HostPort；onvif probe/import 接口测试）、`go vet`、前端类型检查与构建。
- **当前状态**：完成
- **下一步**：
  1. EHOME/ISUP 二进制帧编解码（需海康协议规范）。
  2. 如切 PostgreSQL 可启用 pgvector 语义检索。
  3. 视需要提交 git 仓库。

### 2026-10-08（pgvector 语义检索）
- **已完成**：语义检索在 PostgreSQL 下自动切换到 pgvector，SQLite 保持原 JSON+余弦实现。
  - `internal/search` 抽出 `vectorStore` 接口：`jsonVectorStore`（可移植，写 `ai_events.embedding` + 内存余弦）与 `pgVectorStore`（`CREATE EXTENSION IF NOT EXISTS vector` + 侧表 `event_vectors(event_id, model, embedding vector)`，`INSERT ... ON CONFLICT` 写入，`ORDER BY embedding <=> ?::vector` 检索）。
  - `NewService(db, pgvector)` 按驱动与扩展可用性自动选择后端；扩展缺失自动回落 JSON。`IndexEvent`/`Search` 改用该后端。
  - 配置 `EASYAVR_PGVECTOR`（默认 true），`/ai/search` 响应新增 `backend: json|pgvector`。
  - `docker-compose.yml` 的 postgres 服务镜像改为 `pgvector/pgvector:pg16`（postgres16 + pgvector）。
  - 验证：`go test ./...`、`go vet`；新增 gated 集成测试 `TestPGVectorStore`（`EASYAVR_TEST_PG_DSN`），并做真实端到端——本地 pgvector 容器 + 假嵌入服务：写事件后检索 `backend=pgvector`、fire 命中 score=1、`event_vectors` 表数据正确。
- **当前状态**：完成
- **下一步**：
  1. EHOME/ISUP 二进制帧编解码（需海康协议规范）。
  2. pgvector 固定维度 + HNSW 索引（需为嵌入模型约定维度）以支持 ANN 与大数据量。
  3. 视需要提交 git 仓库。

### 2026-10-08（海康 ISAPI 接入）
- **背景**：查证海康 EHOME/ISUP 为私有协议，官方无公开规范，公开实现均为封装海康私有 SDK（如 `corenel/ip-camera-ehome-server` 依赖 HCEHOMESDK、`CharlesPu/HIKPusher` 基于 EHome SDK v4.0），纯 Go 无法据规范实现。经确认改走官方公开的 **Hikvision ISAPI**。
- **已完成**：
  - 新增 `internal/isapi`：ISAPI（HTTP REST）客户端，**HTTP Digest**（含 qop=auth）鉴权；`/ISAPI/System/deviceInfo` 设备信息、`/ISAPI/Streaming/channels` 码流通道，并按通道 id（101/102…）判定主/子码流、生成 RTSP 地址。
  - 后端新增 `POST /api/v1/isapi/probe` 与 `POST /api/v1/isapi/import`（按码流通道自动创建主/子码流通道，SourceURL 为 RTSP 地址，厂商 hikvision）。
  - 前端「主动发现」导入区改为「自动取流」下拉：ONVIF / 海康 ISAPI / 不自动；「探测」按所选方式返回对应结果（新增海康 ISAPI 结果弹窗）。
  - 验证通过：`go test ./...`（isapi Digest 探测 + 解析 + 主/子码流 + RTSP 地址、challenge 解析；isapi 接口探测/导入端到端）、`go vet`、前端类型检查与构建。
- **当前状态**：完成
- **下一步**：
  1. 如需 EHOME：请提供官方协议 PDF 或授权 SDK，再评估纯 Go 编解码或 CGO 可选集成。
  2. pgvector 固定维度 + HNSW 索引；开放 API 审计按时间/IP 过滤。
  3. 视需要提交 git 仓库。

### 2026-10-08（EHOME/ISUP 可选 SDK 接入位）
- **背景**：查证海康官网无公开 EHOME/ISUP 协议规范；官方形态为原生 SDK（头文件 + `.so/.dll`，如 HCEHOMESDK/ISUP SDK），需合作账号在 open.hikvision.com 获取。
- **已完成**：为 EHOME/ISUP 预留**可选 CGO 接入位**，默认构建保持纯 Go。
  - `internal/ehome` 抽出 `Backend` 接口；默认 `udpBackend`（现有 UDP 端点行为不变）。
  - 新增 `sdk_stub.go`（`!ehome_sdk` → 回落 UDP）与 `sdk_enabled.go`（`-tags ehome_sdk`：预留位，未填绑定前返回明确错误并回落 UDP）。
  - `NewServer` 按构建标签选择后端；已验证 `go build ./...`、`go build -tags ehome_sdk ./...`、`go vet`、`make test` 均通过。
  - README / AGENTS 说明启用方式（提供 SDK 后 `go build -tags ehome_sdk`）。
- **当前状态**：待续（等待海康 SDK 头文件/库以填充 cgo 绑定）
- **下一步**：
  1. 提供 `HCECMS.h` 等头文件与 `.so`：在 `sdk_enabled.go` 中绑定 `NET_ECMS_*`、注册 Register/Keepalive/Alarm/Stream 回调，落库设备与媒体通道。
  2. 若提供官方协议 PDF，则改为纯 Go 编解码。
  3. 视需要提交 git 仓库。

### 2026-10-08（git 首次提交）
- **已完成**：仓库首次提交 `08e409d`，122 个文件、19195 行，工作区干净。提交前已确认 `.gitignore` 排除 `server/data/`、`server/bin/`、`web/dist/`、`web/node_modules/`、`*.db`、`.env`、日志与 ZLM 录像；无密钥/证书/二进制泄漏。
- **当前状态**：完成
- **下一步**：EHOME/ISUP（待 SDK/协议）；pgvector HNSW；开放 API 审计筛选/导出。

### 2026-10-08（推送 GitHub）
- **已完成**：创建并推送公开仓库 https://github.com/yaoyongli1974-dotcom/easyavr （用户 yaoyongli1974-dotcom），本地分支 `main` 跟踪 `origin/main`；`.git/config` 未残留 token。
- **当前状态**：完成
- **安全提示**：本次使用的 PAT 已在聊天中明文出现，务必尽快在 GitHub 撤销/轮换；公开仓库中 `EASYAVR_JWT_SECRET`、管理员账号、ZLM secret、Postgres 口令均为**开发默认值**，生产部署必须修改。
- **下一步**：EHOME/ISUP（待 SDK/协议）；pgvector HNSW；开放 API 审计筛选/导出。

### 2026-10-08（对照 EasyCVR 手册：用户与角色 RBAC）
- **已完成**：按 EasyCVR 使用手册 3.4「用户管理」补齐 RBAC。
  - 新增 `Role` 模型（名称/描述/权限清单/内置），启动种子内置角色 admin/operator/viewer。
  - 用户管理：`GET/POST /users`、`PUT/DELETE /users/:id`（管理员）；创建/编辑/删除、重置密码、启用停用；保护：不能删除/禁用当前用户、不能删除或降级最后一个管理员。
  - 角色管理：`GET/POST /roles`、`PUT/DELETE /roles/:id`（管理员）；内置角色不可删除/改名、被用户占用的角色不可删除。
  - 自助改密 `POST /auth/password`；中间件 `adminRequired` 与 `requirePerm(perm)`（管理员或角色权限命中）；notify/cluster/apikeys/gb35114 组按权限收敛。
  - 前端「用户与角色」页（用户/角色两页签）+ 顶栏「修改密码」。
  - 验证：`go test ./...`（新增用户/角色/权限/改密/最后管理员保护测试）、`go vet`、前端类型检查与构建。
- **当前状态**：完成
- **下一步（对照手册 3.x 的路线）**：
  1. 设备分组（多层级 + 绑定设备/通道 + 按分组授权）。
  2. 电子地图/轨迹跟踪（通道经纬度）。
  3. 运维审计（操作记录/日志）。
  4. 告警预案模板、VQD 视频质量诊断、全局功能搜索、播放诊断。
  5. EHOME/ISUP（待 SDK/协议）；pgvector HNSW。

### 2026-10-08（对照 EasyCVR 手册：设备分组 3.2.4 + 按分组授权 3.4）
- **已完成**：按手册 3.2.4「设备分组」与 3.4「用户管理-按分组授权」实现分组体系。
  - 后端：`DeviceGroup`（多层级，`parent_id` + `path` 祖先路径）、`DeviceGroupDevice`/`ChannelGroupChannel`（设备/通道多对多绑定）、`UserGroup`（用户-分组关联，含可选权限清单，空=继承角色）。
  - 中间件 `requireGroupPerm(perm)`：先查角色权限，再查用户所在分组权限；应用到 `/devices`（需 `device` 权限）与 `/channels`（需 `video` 权限）。
  - 接口：`GET/POST/PUT/DELETE /groups`、`POST /groups/devices/bind|unbind`、`GET /groups/:id/devices`、同理 channels、`GET/POST/PUT/DELETE /groups/user-groups`。
  - 前端：`Groups.vue`（左侧树形分组，右侧 Tab：基本信息/绑定设备/绑定通道/用户权限）。
  - 测试：`TestGroupManagement` 覆盖 CRUD、层级、绑定、用户-分组授权、组隔离（viewer 无分组权限被拒、有权限放行、跨组被拒）。
- **当前状态**：完成
- **下一步（对照手册 3.x 路线）**：
  1. 电子地图/轨迹跟踪（通道经纬度，3.3.4/3.3.5）。
  2. 运维审计（操作记录/日志，3.7.5）。
  3. 告警预案模板（3.7.3.2）、VQD 视频质量诊断（3.6.3）、全局功能搜索（3.8）、播放诊断（3.3.1）。

### 2026-10-08（对照 EasyCVR 手册：电子地图 3.3.4 + 轨迹跟踪 3.3.5）
- **已完成**：按手册 3.3.4「电子地图」与 3.3.5「轨迹跟踪」实现地图与轨迹功能。
  - 后端：`Device`/`Channel` 新增 GPS 字段（经度/纬度/海拔/航向/速度/时间），`Track` 模型存储轨迹点（设备/通道/来源/精度/时间）。
  - 接口：`GET /map/devices|channels`（带坐标的设备/通道）、`POST /map/devices|channels/:id/gps`（更新 GPS 并自动记录轨迹）、`GET /map/tracks`（按设备/通道/时间范围查询轨迹）、`GET /map/tracks/stats`（里程/最高速/时长/海拔统计）。
  - 权限：复用 `requireGroupPerm("video")` 保护地图相关接口。
  - 前端：`Map.vue` 基于 Leaflet，支持 OSM/高德卫星/高德路网三图层切换，左侧设备树（按分组），地图标记设备/通道，轨迹回放与统计弹窗，支持手动设置 GPS。
  - 测试：`TestMapAndTrack` 覆盖设备/通道 GPS 更新、轨迹点记录、查询与统计验证（里程/速度/时长/海拔）。
- **当前状态**：完成
- **下一步（对照手册 3.x 路线）**：
  1. 运维审计（操作记录/日志，3.7.5）。
  2. 告警预案模板（3.7.3.2）、VQD 视频质量诊断（3.6.3）、全局功能搜索（3.8）、播放诊断（3.3.1）。

### 2026-10-08（对照 EasyCVR 手册：运维审计 3.7.5）
- **已完成**：按手册 3.7.5「运维审计」实现操作审计日志。
  - 后端：`AuditLog` 模型（用户/用户名/IP/方法/路径/操作/资源/资源ID/结果/错误/请求体/耗时）。
  - 中间件 `auditLog()`：自动记录所有 JWT 认证请求（排除 healthz/静态文件），推断 action/resource/resourceId，记录请求体（限 2KB）、响应状态、耗时。
  - 接口：`GET /audit/logs`（分页、多维过滤：用户名/操作/资源/结果/IP/时间/关键词）、`GET /audit/logs/:id`、`GET /audit/logs/export`（CSV 导出）。
  - 权限：仅管理员可访问（`adminRequired`）。
  - 前端：`AuditLog.vue` 表格展示，多条件筛选，分页，详情弹窗（含 JSON 请求体格式化），CSV 导出。
  - 测试：`TestAuditLog` 覆盖创建操作记录、多维过滤、CSV 导出验证。
- **当前状态**：完成
- **下一步（对照手册 3.x 路线）**：
  1. 告警预案模板（3.7.3.2）。
  2. VQD 视频质量诊断（3.6.3）。
  3. 全局功能搜索（3.8）。
  4. 播放诊断（3.3.1）。
