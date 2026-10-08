# EasyAVR 架构

EasyAVR 是一个 **AI 原生的视频融合与智能视频资源管理平台**。
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
- **模型管理平面**：`AIModel`（注册表）→ `AIModelVersion`（不可变版本：格式/大小/校验和/指标/标签/默认参数）→
  `AIModelDeployment`（把某版本绑定到运行时 `AIProvider`，含副本/状态/健康）。部署/停止会自动联动版本状态
  （`deployed` ⇄ `available`）。`/ai/models` 与 `/ai/deployments` 由 `ai:model` / `ai:deployment` 策略保护。
- **AI 事件分级分发**：`AlertPolicy`（匹配 kind/类型/最低级别/通道/关键词，含冷却与「需确认」）+  `AlertPolicyTier`（分级：即时或延迟升级、通道目标、模板）替代传统告警预案。事件经 `eventSink`
  触发即时 tier，未确认事件由后台 `Escalate` 调度器按 `delaySec` 升级；每次投递写入 `AlertDelivery`
  审计，事件可经 `POST /events/:id/ack` 确认以阻止升级。`/alert/*` 由 `alert` 策略保护。
- **标注/训练流水线**：`Dataset`（类别/来源/状态 + 样本/已标注计数）→ `DatasetSample`（图片/建议标签/
  划分/标注状态，可从 AI 事件快照批量导入）→ `AnnotationTask`（负责人/进度，完成后数据集转 `ready`）→
  `TrainingJob`（框架/超参/指标，`POST /ai/training/:id/run` 产出 `AIModel` 与 `AIModelVersion`）。
  `/ai/datasets`、`/ai/annotations`、`/ai/training` 分别由 `ai:dataset`/`ai:annotation`/`ai:training` 策略保护。
- **抽帧桥接**：`ai.GrabFrame` 用 ffmpeg 从任意流抓取单帧，让 CV/VLM 不需要经过浏览器。
- **云台 PTZ**：`internal/ptz` 提供命令编码——GB28181 产出标准 8 字节 `PTZCmd`（方向/变倍/预置位，含校验和），
  经 `gb28181.SendPTZ` 以 `DeviceControl` 下发；海康 ISAPI 走 `PTZContinuous`/预置位 HTTP 接口。平台侧
  `PTZPreset` 保存预置位名称，按设备协议自动路由；不支持协议的设备返回明确错误。
- **通道诊断**：`internal/media` 的 `Probe` 调用 ffprobe 解析流的封装/编码/分辨率/帧率/码率并测量连接耗时，
  `GET /channels/:id/diagnose` 返回结构化报告（源不可达或缺少 ffprobe 时仍返回报告，`status:"failed"`）。
  `internal/vqd` 在抽取的帧上计算亮度/对比度/清晰度（Laplacian 方差）/黑屏比/蓝屏比/噪声/偏色/主色占比/8px 方块度，
  并对连续帧做帧差与全局位移估计，`Evaluate`/`EvaluateTemporal` 按阈值判定黑屏、过暗、过曝、模糊、低对比、偏色、
  蓝屏、遮挡、马赛克、花屏/噪声、冻结、抖动等质量问题；`GET /channels/:id/vqd` 返回指标与问题，
  `record=1` 且存在告警级问题时生成 `AIEvent(kind=vqd)` 交由 `eventSink` 分发索引与通知。
- **运行时平台配置**：`PlatformSetting` 持久化开关，`GET/PUT /config/platform`（管理员）可即时启停
  GB28181/EHOME/GB35114 信令监听（`gb28181.Server` 支持 Start/Stop/StopTLS 生命周期），无需重启；
  环境变量作为首次启动的默认值，其余播放/运维参数仍由环境变量决定。
- **注册黑白名单**：`internal/access` 统一判定 GB28181/EHOME 注册——黑名单按「已填字段全部匹配」拦截，
  白名单在该协议存在启用规则时要求命中；`gb28181.handleRegister` 与 EHOME `udpBackend.identify` 均调用。
- **批量导入导出**：设备/通道/用户均支持 CSV（UTF-8 BOM，Excel 友好）导出与导入，字段按表头名解析。
- **播放鉴权**：`internal/playauth` 以 HMAC-SHA256 基于 `streamKey|exp` 签名，`EASYAVR_PLAY_AUTH=true` 时
  `play-urls`/`play-token` 附带 `token`+`exp`；`/play/verify` 与 `/zlm/on_play`（ZLM on_play 钩子）用于边缘校验，
  `EASYAVR_PLAY_WHITELIST` 限定可申请 token 的来源域名。
- **流量与状态记录**：`ChannelTraffic` 保存 ZLM 上报的累计入流字节，`StatusLog` 记录设备/通道上下线与检测结果；
  `POST /video/traffic/sync` 采样内核（`EASYAVR_MONITOR_SEC>0` 时后台周期采样），`POST /devices/:id/check` 做 TCP 健康检测。
- **录像增强**：`Recording.Marked/Mark` 支持紧急标记；`POST /recordings/cleanup` 按各通道录像计划保存天数清理目录项。
- **电子地图定位来源**：① 浏览器网络/GPS 定位（`navigator.geolocation`，「定位到我」/「使用我的当前位置」）；
  ② 设备 GPS：GB28181 `MobilePosition` 上报自动写入通道坐标与轨迹（`POST /gb/devices/:gbid/mobile-position` 可主动请求）；
  ③ 地点搜索/联想（`internal/geo`，配置 `EASYAVR_AMAP_KEY` 用高德，否则回落 OpenStreetMap Nominatim）；
  ④ CSV 批量导入（`POST /map/import`）；⑤ 分组基准坐标（组内无自身 GPS 的按基准显示）。
- **统一授权（Policy Engine）**：基于 Casbin（`internal/policy`，GORM 适配器，策略持久化到
  `casbin_rule` 表）。模型为 `sub, obj, act, dom` + `keyMatch`/域通配，支持 RBAC（`role:xxx`）
  与用户级（`user:id`）规则；`requirePolicy(resource, action, domain)` 中间件接管
  AI/事件/视频/GB/GA/通知/集群/开放 API 等路由，设备/通道/地图保留分组授权
  （`requireGroupPerm`）。`/policy` 提供策略 CRUD、权限测试器与默认策略重置。

## API 一览（`/api/v1`）

| 分组 | 端点 |
|---|---|
| 认证 | `POST /auth/login`、`GET /auth/profile`、`POST /auth/password` |
| 用户 | `GET/POST /users`、`GET /users/export`、`POST /users/import`、`PUT/DELETE /users/:id`（管理员） |
| 角色 | `GET/POST /roles`、`PUT/DELETE /roles/:id`（管理员，含权限清单） |
| 权限策略 | `GET/POST/DELETE /policy`、`POST /policy/test`、`GET /policy/user/:id/roles`、`POST /policy/seed`（管理员，Casbin 策略与权限测试器） |
| 设备 | `GET/POST /devices`、`GET/PUT/DELETE /devices/:id`、`GET/POST /devices/:id/channels`、`GET /devices/export`、`POST /devices/import`、`POST /devices/check`、`POST /devices/:id/check`、`GET /devices/:id/status-logs` |
| 主动发现 | `POST /discovery/scan`（ONVIF WS-Discovery / 网段端口扫描） |
| ONVIF | `POST /onvif/probe`（设备信息 + 媒体配置 + RTSP 地址）、`POST /onvif/import`（按媒体配置自动建主/子码流通道） |
| 海康 ISAPI | `POST /isapi/probe`（设备信息 + 码流通道 + RTSP 地址）、`POST /isapi/import`（按码流通道自动建主/子码流通道） |
| 通道 | `GET /channels`、`GET /channels/export`、`POST /channels/import`、`PUT/DELETE /channels/:id`、`POST /channels/:id/start|stop`、`GET /channels/:id/play-urls`、`POST /channels/:id/play-token`、`GET /channels/:id/traffic`、`GET /channels/:id/status-logs` |
| 播放鉴权 | `GET /playback/config`（鉴权开关/TTL/白名单）、`GET /play/verify`（公开校验）、`POST /zlm/on_play`（ZLM on_play 钩子，按 token 放行/拒绝） |
| 平台配置 | `GET /config/platform`、`PUT /config/platform`（管理员；运行时启停 GB28181/EHOME/GB35114 信令，读取播放/运维配置） |
| 云台 PTZ | `POST /channels/:id/ptz`、`GET/POST /channels/:id/ptz/presets`、`POST /channels/:id/ptz/presets/:preset/goto`、`DELETE /channels/:id/ptz/presets/:preset` |
| 播放诊断 | `GET /channels/:id/diagnose`（ffprobe 封装/编码/分辨率/帧率/码率/连接耗时，`timeoutSec` 可调） |
| 质量诊断 VQD | `GET /channels/:id/vqd`（抽帧图像质量指标与问题判定，`record=1` 时异常写入事件中心） |
| 视频资源 | `GET /video/resources`、`GET /video/stats`、`GET /video/streams`、`POST /video/sync` |
| AI 能力 | `GET/POST /ai/providers`、`PUT/DELETE /ai/providers/:id` |
| AI 模型 | `GET/POST /ai/models`、`GET/PUT/DELETE /ai/models/:id`、`GET/POST /ai/models/:id/versions`、`PUT/DELETE /ai/models/:id/versions/:versionId`、`POST /ai/models/:id/versions/:versionId/archive`、`GET /ai/model-stats` |
| AI 部署 | `GET/POST /ai/deployments`、`PUT/DELETE /ai/deployments/:id`、`POST /ai/deployments/:id/activate|stop` |
| 数据集 | `GET/POST /ai/datasets`、`GET/PUT/DELETE /ai/datasets/:id`、`GET/POST /ai/datasets/:id/samples`、`POST /ai/datasets/:id/samples/import`、`PUT/DELETE /ai/datasets/:id/samples/:sampleId` |
| 标注任务 | `GET/POST /ai/annotations`、`PUT/DELETE /ai/annotations/:id`、`POST /ai/annotations/:id/complete` |
| 训练作业 | `GET/POST /ai/training`、`PUT/DELETE /ai/training/:id`、`POST /ai/training/:id/run|cancel` |
| 流水线统计 | `GET /ai/pipeline-stats` |
| AI 任务 | `GET/POST /ai/tasks`、`PUT/DELETE /ai/tasks/:id`、`POST /ai/tasks/:id/start|stop|run`、`GET /ai/tasks/:id/schedule` |
| 事件中心 | `GET /events`、`GET /events/stats`、`POST /events/ingest` |
| 电子地图 | `GET /map/devices|channels|tracks|tracks/stats`、`POST /map/devices|channels/:id/gps`、`GET /map/geocode`（地点搜索/联想）、`POST /map/import`（CSV 批量坐标） |
| AI 事件地图 | `GET /map/events`、`GET /map/events/stats`（按通道 GPS 落点，支持级别/类型/时间/关键词过滤） |
| 智能检索 | `POST /ai/search` |
| 告警通知 | `GET/POST/PUT/DELETE /notify/channels`、`POST /notify/channels/:id/test`、`GET/POST/PUT/DELETE /notify/rules` |
| 告警策略 | `GET/POST /alert/policies`、`GET/PUT/DELETE /alert/policies/:id`、`POST /alert/policies/:id/test`、`GET/POST /alert/policies/:id/tiers`、`PUT/DELETE /alert/policies/:id/tiers/:tierId`、`GET /alert/deliveries`、`GET /alert/stats` |
| 事件确认 | `POST /events/:id/ack` |
| 录像 | `POST /channels/:id/record/start|stop`、`GET /channels/:id/recordings`、`POST /channels/:id/recordings/sync`、`GET/DELETE /recordings`、`PUT /recordings/:id/mark`、`POST /recordings/cleanup` |
| 录像计划 | `GET/PUT /channels/:id/recording-plan` |
| 快照 | `POST /channels/:id/snapshot`、`GET/DELETE /snapshots` |
| GB28181 | `GET /gb/devices`、`GET /gb/config`、`POST /gb/devices/:gbid/refresh`、`POST /gb/devices/:gbid/mobile-position`（请求移动位置） |
| GB28181 级联 | `GET/POST /gb/cascades`、`POST /gb/cascades/:id/refresh`、`DELETE /gb/cascades/:id` |
| 白名单 | `GET/POST /gb/whitelist`、`DELETE /gb/whitelist/:id` |
| 黑名单 | `GET/POST /gb/blacklist`、`DELETE /gb/blacklist/:id`（多条件全匹配拦截，GB28181/EHOME 注册时生效） |
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
  - **用户与角色（RBAC）**：`User` + `Role`（含权限清单）管理页；用户 CRUD、角色 CRUD、修改密码、管理员保护；非管理员按角色权限访问（`requirePerm`），管理员专属用户/角色管理。
  - **统一权限策略（Policy Engine）**：`internal/policy`（Casbin + GORM 适配器）；`role:admin/operator/viewer` 默认策略、用户级策略、策略 CRUD 与权限测试器；前端「权限策略」页，接管 AI/事件/视频/GB/GA/通知/集群/开放 API 授权。
  - **模型管理平面**：`AIModel`（注册）→ `AIModelVersion`（版本：格式/指标/标签/参数）→ `AIModelDeployment`（部署到运行时 `AIProvider`，副本/状态/健康）；`/ai/models`、`/ai/deployments`，前端「模型管理」页。
  - **AI 事件地图**：`GET /map/events` 将 AI 事件按其通道 GPS 落点（富化通道名/坐标），支持级别/类型/时间/关键词过滤与 `byType/byLevel` 统计；`Map.vue` 增加「AI 事件」图层（按级别着色、统计标签、事件详情含快照与实时画面视频）。
  - **AI 事件分级分发**：`AlertPolicy`/`AlertPolicyTier`/`AlertDelivery`；`/alert/*` 策略与分级 CRUD、测试、投递审计、统计；事件确认 `POST /events/:id/ack`；`eventSink` 即时分发 + 后台 `Escalate` 定时升级。前端「告警策略」页 + AI 事件中心「确认」。
  - **标注/训练流水线**：`Dataset`/`DatasetSample`/`AnnotationTask`/`TrainingJob`；事件快照导入、标注进度、训练作业产出模型版本；`/ai/datasets|annotations|training`；前端「数据与训练」页。
  - AI 原生演进优先级（① 授权策略 ② 模型管理 ③ AI 事件地图 ④ 分级分发 ⑤ 标注/训练）已全部落地。后续可选：真实训练器对接、向量库/知识图谱、AI 可观测性。
  - **电子地图定位增强**：浏览器定位、地点搜索/联想（AMap/Nominatim）、CSV 批量导入、分组基准坐标、GB28181 移动位置上报。
- **后续路线（`docs/architecture.md`）**：
  - 设备分组（多层级，绑定设备/通道）与按分组授权（手册 3.2.4 / 3.4）。
  - 电子地图与轨迹跟踪（通道经纬度 + 地图，手册 3.3.4 / 3.3.5）。
  - 运维审计（系统/信令日志、操作记录，手册 3.7.5）。
  - 告警预案模板（级别/方式/类型/事件，手册 3.7.3.2）。
  - 视频质量诊断 VQD（任务类型 `vqd`，手册 3.6.3）。
  - 全局功能搜索（命令面板，手册 3.8）、播放诊断（3.3.1）。
  - 关键配置：`EASYAVR_GB35114_SIP_LISTEN=:5061` 启用国密安全 SIP（`EASYAVR_GB35114_SIP_REQUIRE_CLIENT=true` 强制双向证书认证）。
