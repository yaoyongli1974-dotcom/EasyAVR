# API 权限矩阵（TASK-011）

认证层：`/api/v1/*` 默认经 `authRequired()` + 审计；公开例外仅有 `healthz`、`play/verify`、`zlm/on_play`、`auth/login`、`openapi.json`、`open/*`（API Key）、`cluster/heartbeat`（共享密钥）、`/VIID/*`（上级平台上报）、短期签名快照 `/api/v1/snapshot-access/:token`。

| 模块 | 查看 | 新增/修改/删除 | 管理配置 |
|---|---|---|---|
| 设备/通道/发现/ONVIF/ISAPI | `device` + 分组 | `device` + 分组 | admin |
| 视频播放/回放/录像/归档/流量 | `video` + 分组 | `video` + 分组（录像清理/归档） | admin（计划/保留期） |
| 快照 | `video` + 分组；图片仅经签名 URL | `video` + 分组 | admin（定时抓拍配置） |
| AI 任务/检索/模型/数据集 | `ai:task` 等策略 | 同策略 | provider 密钥 admin |
| AI Provider | 策略列表 | provider 密钥 admin（专用 DTO） | admin |
| 事件/地图/告警 | 策略 `event`/`map` | 确认 admin/策略 | admin |
| GB28181/GA1400/GB35114 | 策略 | 策略 | admin（密钥不回显） |
| 通知渠道 | 策略 | 密钥 admin（专用 DTO） | admin |
| 存储/转码 | 策略 | admin | admin |
| 用户/角色/策略/审计/系统配置 | admin | admin | admin |

分组归属由 `requireGroupPerm` 执行：admin 直通；角色有权限时放行；否则仅当用户所属用户组包含目标设备/通道（含 device→channel 继承）时才放行，目标不在任何组时拒绝。快照 ID 的归属由 `snapshotAccessURL/list/delete` handler 按通道复核（`:id` 不能靠路径启发式判断）。

敏感配置读接口一律只返回 `configured`/`hasSecret` 布尔；写入使用专用 DTO，空值保留旧密钥，显式 `clearXxx:true` 才清空；审计中间件从不记录请求体。GB SIP 口令还可经 `PUT /gb/config/password` 显式修改，加密持久化。
