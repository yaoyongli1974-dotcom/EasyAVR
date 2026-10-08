# EasyAVR

**AI 原生的视频融合与智能视频资源管理平台。**

EasyAVR 不是 EasyCVR 的克隆版。它把视频能力组织成三大平面（设备接入 / 视频中台 / AI 智能中台）、
两大中心（视频资源中心 / AI 事件中心），并通过统一的开放层同时服务 Web、APP 与第三方 API。
详见 [docs/architecture.md](docs/architecture.md)。

## 技术栈

- **后端**：Go / Gin / GORM / 可插拔数据库（默认纯 Go SQLite 零依赖启动，生产可切 PostgreSQL）/ JWT
- **流媒体内核**：ZLMediaKit（拉流 / 转码 / 分发：HTTP-FLV、HLS、WebRTC、HTTP-FMP4、RTSP、RTMP）
- **AI 中台**：CV（外部检测 worker）/ VLM / LLM（任意 OpenAI 兼容端点）
- **前端**：Vue 3 + TypeScript + Vite + Pinia + Element Plus + mpegts.js + hls.js

## 快速开始

前置：Go 1.24+、Node 20+、Docker、ffmpeg（AI 抽帧用）。

```bash
# 1) 启动流媒体内核（ZLMediaKit）
docker compose up -d

# 2) 安装依赖
make deps

# 3) 启动后端 API（:18000，内置 SQLite，首次启动自动建管理员）
make run

# 4) 另开终端启动前端开发服务器（:5173，/api 代理到 :18000）
make web-dev
```

打开 http://localhost:5173 ，默认账号 **easyavr / easyavr**。

生产模式：`make web-build` 后，后端会自动托管 `web/dist`（访问 :18000）。

### 使用 PostgreSQL（可选）

默认使用纯 Go 内嵌 SQLite（零依赖）。如需 PostgreSQL：

```bash
make db-up       # 启动本地 PostgreSQL 容器（pgvector/pgvector:pg16，账号 easyavr/easyavr）
make run-pg      # 用 PostgreSQL 运行后端（语义检索自动使用 pgvector）

# 或手动：
EASYAVR_DB_DRIVER=postgres \
EASYAVR_DB_DSN='postgres://easyavr:easyavr@127.0.0.1:5432/easyavr?sslmode=disable' make run

make db-down     # 停止 PostgreSQL
```

## 常用命令

```bash
make test        # Go 单元/集成测试（含平台闭环测试）
make vet         # go vet
make build       # 生成 server/bin/easyavr 与 web/dist
make up / down   # 启动 / 停止 ZLMediaKit
make db-up / db-down  # 启动 / 停止 PostgreSQL（可选）
make run-pg      # 用本地 PostgreSQL 运行后端
```

## 配置（环境变量）

| 变量 | 默认值 | 说明 |
|---|---|---|
| `EASYAVR_LISTEN` | `:18000` | HTTP 监听地址 |
| `EASYAVR_DB_DRIVER` | `sqlite` | 数据库驱动：`sqlite`（默认，零依赖）或 `postgres` |
| `EASYAVR_DB` | `./data/easyavr.db` | SQLite 路径（driver=sqlite 时生效） |
| `EASYAVR_DB_DSN` | 空 | PostgreSQL DSN，如 `postgres://user:pass@host:5432/easyavr?sslmode=disable` |
| `EASYAVR_PGVECTOR` | `true` | PostgreSQL 下用 pgvector 存储/检索事件向量（扩展不可用时自动退化） |
| `EASYAVR_JWT_SECRET` | `easyavr-dev-secret-change-me` | JWT 密钥（生产必须修改） |
| `EASYAVR_ADMIN_USER/PASS` | `easyavr` / `easyavr` | 首次启动创建的管理员 |
| `EASYAVR_ZLM_API` | `http://127.0.0.1:80` | ZLMediaKit HTTP API |
| `EASYAVR_ZLM_SECRET` | `EasyAVRzlmSecret2024x` | ZLM API secret（`docker-compose.yml` 已通过 `deploy/zlm/config.ini` 固定为同值） |
| `EASYAVR_MEDIA_HOST` | `127.0.0.1` | 浏览器可访问的媒体主机 |
| `EASYAVR_WEB_DIST` | `../web/dist` | 静态前端目录 |
| `EASYAVR_SNAPSHOT_DIR` | `./data/snapshots` | 快照存储目录 |
| `EASYAVR_GB_ENABLED` | `false` | 启用 GB28181 SIP 信令 |
| `EASYAVR_GB_LISTEN` | `:5060` | SIP UDP 监听地址 |
| `EASYAVR_GB_ID` | `34020000002000000001` | 平台 SIP 编码（20 位） |
| `EASYAVR_GB_REALM` | `3402000000` | SIP 域 / Realm |
| `EASYAVR_GB_PASSWORD` | `easyavr123` | SIP 摘要鉴权密码 |
| `EASYAVR_GB_RTP_IP` | `127.0.0.1` | SDP 中通告的收流 IP（需设备可达） |
| `EASYAVR_GA1400_ENABLED` | `false` | 启用 GA/T1400（VIID）入库端点 `/VIID/*` |
| `EASYAVR_EHOME_ENABLED` | `false` | 启用 EHOME/ISUP UDP 接入 |
| `EASYAVR_GB35114_ENABLED` | `false` | 启用 GB35114 接入配置 |
| `EASYAVR_GB35114_CERT_DIR` | `./data/gb35114-certs` | 平台 CA / 证书目录 |
| `EASYAVR_GB35114_SIP_LISTEN` | 空 | GM/T 0024 安全 SIP 监听（如 `:5061`，留空不启用） |
| `EASYAVR_GB35114_SIP_REQUIRE_CLIENT` | `true` | 安全 SIP 是否强制设备双向证书认证 |
| `EASYAVR_CLUSTER_ENABLED` | `false` | 启用集群 |
| `EASYAVR_CLUSTER_NODE_ID` | `node-1` | 本节点 ID |
| `EASYAVR_CLUSTER_API` | 空 | 本节点对外 API 地址 |
| `EASYAVR_CLUSTER_PEERS` | 空 | 对端节点 API，逗号分隔 |

## 二期功能

- **主动发现**：设备接入页「主动发现」支持 ONVIF WS-Discovery 多播发现与 IP 网段（≤ /24）端口扫描，
  勾选后一键导入（自动建主通道）。多播发现需容器/主机与本网段二层可达。
- **自动取流（ONVIF / 海康 ISAPI）**：发现结果可「探测」查看设备信息与码流；导入时选择「ONVIF」或「海康 ISAPI」并填写设备账号，
  平台会据媒体配置/码流通道自动创建主/子码流通道并写入 RTSP 源地址。
  - ONVIF：`GetDeviceInformation`/`GetCapabilities`/`GetProfiles`/`GetStreamUri`（WS-Security + HTTP Digest）。
  - 海康 ISAPI：`/ISAPI/System/deviceInfo` + `/ISAPI/Streaming/channels`（HTTP Digest，官方公开接口，纯 Go）。
- **GB28181 接入**：启动时设置 `EASYAVR_GB_ENABLED=true`。设备 REGISTER 后平台自动查询
  通道目录并建通道；点播时平台向设备发 INVITE，媒体由 ZLMediaKit 接收。Web 端见「GB28181 接入」页。
- **录像回看**：通道启停录像、按日期同步与回放/下载；支持按天/时段自动录像计划与保留期清理。见「录像回看」页。
- **快照中心**：手动/定时抓拍（依赖 ffmpeg），按通道浏览、配置抓拍间隔。见「快照中心」页。

## 三期功能（进行中）

- **多屏播放**：1/4/9/16 分屏、轮播、播放记忆（WebRTC）。见「多屏播放」页。
- **告警通知**：Webhook / 邮件渠道 + 按级别/类型/能力的匹配规则。见「告警通知」页。
- **智能检索**：事件向量化 + 余弦检索，未配置嵌入模型时退化为关键词。见「智能检索」页；在「AI 能力接入」注册 `embedding` 类型能力即可启用向量检索。
- **集群**：节点注册/心跳/设备归属（`EASYAVR_CLUSTER_*`）。见「集群」页。
- **GB28181 级联**：作为下方向上级平台注册；白名单。见「GB28181 接入」页的级联/白名单页签。
- **GA/T1400（VIID）**：`/VIID/*` 结构化与告警 XML 入库（`EASYAVR_GA1400_ENABLED`）；上下级级联与订阅见「GA/T1400 级联」页。
- **EHOME/ISUP**：默认纯 Go UDP CMS/SMS 接入端点（`EASYAVR_EHOME_ENABLED`）。官方 EHOME/ISUP 只提供原生 SDK（头文件 + `.so/.dll`），无公开协议规范；已预留**可选 CGO 接入位**，提供 SDK 后以 `go build -tags ehome_sdk` 启用（默认构建不含 CGO）。
- **GB35114**：国密 SM2 平台 CA、设备证书签发/登记/校验/吊销、SM3 工具，以及 **GM/T 0024 安全 SIP/TLS** 双向证书认证（`EASYAVR_GB35114_ENABLED` + `EASYAVR_GB35114_SIP_LISTEN`）。见「国密 GB35114」页。
- **用户与角色**：用户 CRUD、角色 CRUD（权限清单）、修改密码；管理员专属管理，非管理员按角色权限访问。见「用户与角色」页。
- **集群**：节点心跳 + 设备级最少负载调度（`/cluster/stats` 展示各节点设备数）。
- **开放 API**：第三方/APP 通过 `/api/v1/open/*` 访问设备/通道/资源/事件（`X-API-Key`，scope 读写 + 限速 + 每日配额 + 审计），OpenAPI 文档见 `GET /api/v1/openapi.json`。见「开放 API」页。

## 端到端验证

`server/internal/server/server_test.go` 覆盖完整链路：
登录 → 建设备（自动建主通道）→ 注册 CV 能力 → 创建任务 → 试跑 → 事件入库与统计。
该测试使用假 CV worker，**不依赖 ZLMediaKit**，可直接 `make test` 运行。

## 目录结构

```
server/                  Go 后端
  cmd/easyavr/           入口
  internal/device/       设备接入平面
  internal/video/        视频中台（ZLMediaKit 客户端）
  internal/ai/           AI 智能中台（Provider/Runner/抽帧）
  internal/event/        AI 事件中心
  internal/server/       HTTP 路由与处理器
web/                     Vue3 前端
examples/cv-worker/      CV 能力接入示例（stdlib，无依赖）
deploy/                  流媒体部署资源
docs/architecture.md     架构说明
```
