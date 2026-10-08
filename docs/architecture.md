# EasyAVR 架构

EasyAVR 不是一个 EasyCVR 的克隆，而是一个 **AI 原生的视频融合与智能视频资源管理平台**。
平台由三大平面 + 两大中心 + 一个开放层组成：

```
                         EasyAVR
                            │
          ┌─────────────────┼─────────────────┐
          │                 │                 │
       设备接入           视频中台          AI智能中台
          │                 │                 │
   ┌──────┼──────┐    ┌─────┼─────┐     ┌────┼────┐
   │      │      │    │     │     │     │    │    │
 GB28181 RTSP  ONVIF  拉流  转码  分发   CV   VLM  LLM
   │      │      │    │     │     │     │    │    │
 EHOME  RTMP  SDK     │     │     │     │    │    │
   │      │      │    └─────┼─────┘     └────┼────┘
   └──────┴──────┘          │                │
                            │                │
                     ┌──────┴──────┐    ┌────┴─────┐
                     │ 视频资源中心 │    │ AI事件中心│
                     └──────┬──────┘    └────┬─────┘
                            │                │
                 ┌──────────┼────────────────┤
                 │          │                │
              实时视频     录像           智能检索
                 │          │                │
                 └──────────┼────────────────┘
                            │
                       EasyAVR Web
                            │
             ┌──────────────┼──────────────┐
             │              │              │
           Web端          APP端          第三方API
```

## 代码模块映射

| 架构层 | Go 包 | 说明 |
|---|---|---|
| 设备接入平面 | `internal/device` | 归一化 RTSP/RTMP/ONVIF/GB28181/EHOME 等源为 Channel，负责通道启停；`internal/discovery` 主动发现（ONVIF WS-Discovery + 网段扫描）；`internal/onvif` ONVIF SOAP 客户端；`internal/isapi` 海康 ISAPI（HTTP Digest，设备信息/码流通道/RTSP 地址） |
| 视频中台 | `internal/video` | ZLMediaKit HTTP API 客户端：`addStreamProxy` 拉流、`openRtpServer` 收流、播放/推流地址生成 |
| 视频资源中心 | `internal/model.VideoResource` + `internal/server/handler_video.go` | 实时流 / 录像 / 快照统一编目与统计 |
| AI 智能中台 | `internal/ai` | CV/VLM/LLM 的 `Analyzer` 统一抽象（`provider.go`）、OpenAI 兼容客户端、外部 CV worker 适配、ffmpeg 抽帧、任务调度器 `Runner` |
| AI 事件中心 | `internal/event` | 事件统一存储、检索（`Query`）与统计（`Stats`） |
| 智能检索 | `internal/search` | 事件向量化 + 余弦检索；PostgreSQL + pgvector 可用时自动改用向量库（`<=>`），否则用可移植 JSON + 内存余弦；未配置嵌入模型时退化为关键词 |
| 告警通知 | `internal/notify` | 规则匹配 + Webhook / 邮件投递 |
| 集群 | `internal/cluster` | 节点注册、心跳与设备归属 |
| GA/T1400（VIID） | `internal/ga1400` | 视图库结构化/告警 XML 入库 |
| EHOME/ISUP | `internal/ehome` | 海康私有协议接入端点（UDP CMS/SMS） |
| 持久化 | `internal/store` | 可插拔数据库：默认纯 Go SQLite（WAL/busy_timeout/外键），可切 PostgreSQL（pgx）；自动建表与管理员种子 |
| 开放层 | `internal/server` | Gin 路由 + JWT 中间件，Web / APP / 第三方 API 共用 |

## 关键设计

- **流媒体内核外置**：不重复造轮子，通过 `internal/video/zlm.go` 驱动 ZLMediaKit，
  平台自身只关心“资源编目 + 业务编排 + AI 编排”。
- **持久化可插拔**：默认 SQLite（单机/边缘，零外部依赖，WAL + busy_timeout + 外键）；
  生产多节点/高并发可切 PostgreSQL（`EASYAVR_DB_DRIVER=postgres` + `EASYAVR_DB_DSN`），
  驱动均为纯 Go（无 CGO），模型与迁移代码不变。
- **AI 能力可插拔**：任何 AI 后端实现统一接口即可接入：
  - `cv` → 外部检测服务（`HTTPDetector`，契约见 `examples/cv-worker`）
  - `vlm`/`llm` → 任意 OpenAI 兼容端点（OpenAI / vLLM / Ollama / Qwen / DeepSeek）
- **抽帧桥接**：`ai.GrabFrame` 用 ffmpeg 从任意流抓取单帧，让 CV/VLM 不需要经过浏览器。

## API 一览（`/api/v1`）

| 分组 | 端点 |
|---|---|
| 认证 | `POST /auth/login`、`GET /auth/profile` |
| 设备 | `GET/POST /devices`、`GET/PUT/DELETE /devices/:id`、`GET/POST /devices/:id/channels` |
| 主动发现 | `POST /discovery/scan`（ONVIF WS-Discovery / 网段端口扫描） |
| ONVIF | `POST /onvif/probe`（设备信息 + 媒体配置 + RTSP 地址）、`POST /onvif/import`（按媒体配置自动建主/子码流通道） |
| 海康 ISAPI | `POST /isapi/probe`（设备信息 + 码流通道 + RTSP 地址）、`POST /isapi/import`（按码流通道自动建主/子码流通道） |
| 通道 | `GET /channels`、`PUT/DELETE /channels/:id`、`POST /channels/:id/start|stop`、`GET /channels/:id/play-urls` |
| 视频资源 | `GET /video/resources`、`GET /video/stats`、`GET /video/streams`、`POST /video/sync` |
| AI 能力 | `GET/POST /ai/providers`、`PUT/DELETE /ai/providers/:id` |
| AI 任务 | `GET/POST /ai/tasks`、`PUT/DELETE /ai/tasks/:id`、`POST /ai/tasks/:id/start|stop|run` |
| 事件中心 | `GET /events`、`GET /events/stats`、`POST /events/ingest` |
| 智能检索 | `POST /ai/search` |
| 告警通知 | `GET/POST/PUT/DELETE /notify/channels`、`POST /notify/channels/:id/test`、`GET/POST/PUT/DELETE /notify/rules` |
| 录像 | `POST /channels/:id/record/start|stop`、`GET /channels/:id/recordings`、`POST /channels/:id/recordings/sync`、`GET/DELETE /recordings` |
| 录像计划 | `GET/PUT /channels/:id/recording-plan` |
| 快照 | `POST /channels/:id/snapshot`、`GET/DELETE /snapshots` |
| GB28181 | `GET /gb/devices`、`GET /gb/config`、`POST /gb/devices/:gbid/refresh` |
| GB28181 级联 | `GET/POST /gb/cascades`、`POST /gb/cascades/:id/refresh`、`DELETE /gb/cascades/:id` |
| 白名单 | `GET/POST /gb/whitelist`、`DELETE /gb/whitelist/:id` |
| GA/T1400 | `POST /VIID/System/Register|Keepalive`、`POST /VIID/Face|Person|MotorVehicle|NonMotorVehicle|Notification` |
| GA/T1400 级联 | `GET/POST /ga1400/cascades`、`POST /ga1400/cascades/:id/test|sync`、`DELETE /ga1400/cascades/:id`、`GET /ga1400/subscriptions` |
| 国密 GB35114 | `GET /gb35114/config`、`POST /gb35114/platform-cert`、`POST /gb35114/sign-csr|enroll|verify`、`GET /gb35114/certs`、`POST /gb35114/certs/:id/revoke`、`POST /gb35114/sm3` |
| 开放 API 密钥 | `GET/POST /apikeys`、`PUT/DELETE /apikeys/:id`、`GET /apikeys/stats`、`GET /apikeys/logs`、`GET /apikeys/:id/logs` |
| 开放 API（第三方/APP） | `GET /open/devices|channels|resources|events`、`GET /open/channels/:id/play-urls`、`POST /open/events`（`X-API-Key`，限速/配额/审计） |
| 开放 API 文档 | `GET /openapi.json`（OpenAPI 3.0） |
| 集群 | `GET /cluster/nodes`、`GET /cluster/stats`、`GET /cluster/config`、`POST /cluster/heartbeat`（共享密钥） |
| 系统 | `GET /healthz`、`GET /system/info` |

## 路线图

- **MVP（已完成）**：设备/通道接入（RTSP/RTMP/ONVIF）+ ZLMediaKit 拉流分发 + 实时播放 +
  CV/VLM/LLM 能力接入 + AI 任务调度 + 事件中心 + Web 端。
- **二期（已完成）**：
  - GB28181/SIP 信令：UDP SIP 服务器、REGISTER 摘要鉴权、Keepalive、Catalog 自动建通道、Alarm、实时 INVITE 点播（媒体由 ZLMediaKit 接收）。
  - 录像与回看：手动启停录像、录像目录编目、按日期检索与回放/下载、**按天/时段自动录像计划**与保留期清理。
  - 快照：手动/定时抓拍（ffmpeg 抽帧）、快照编目与静态托管。
- **三期（进行中）**：
  - 已完成：多屏播放、告警通知（Webhook/邮件）、语义检索（向量 + 关键词回退）、集群（节点注册/心跳/**设备级最少负载调度**）、GB28181 级联（向上注册）、GA/T1400 VIID 入库与**上下级级联/订阅（订阅自动续订 + 全量/增量同步）**、EHOME/ISUP 接入端点、**GB35114 国密（SM2 平台 CA + 设备证书签发/登记/校验/吊销 + SM3 + GM/T 0024 安全 SIP/TLS 双向认证，gmsm/gmtls）**、**开放层第三方/APP API 密钥鉴权（`/open/*`，scope + 限速 + 每日配额 + 审计，含 OpenAPI 文档）**。
  - 待完善：EHOME 完整二进制编解码需对接厂商协议规范。
  - 关键配置：`EASYAVR_GB35114_SIP_LISTEN=:5061` 启用国密安全 SIP（`EASYAVR_GB35114_SIP_REQUIRE_CLIENT=true` 强制双向证书认证）。
