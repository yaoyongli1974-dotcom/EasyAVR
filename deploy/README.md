# 部署与上线

`deploy/` 下放配置、运维脚本和说明；生产部署按下面的顺序做，每一步都能独立验证。

## 目录

| 路径 | 内容 |
|---|---|
| `deploy/zlm/config.ini` | ZLMediaKit 配置（`docker compose up -d` 挂载），`secret` 必须与 `EASYAVR_ZLM_SECRET` 一致。ZLM 在检测到默认 secret 时会自行重置，别改回 `035c73f7-...`。 |
| `deploy/postgres/init/01-readonly-role.sql` | 只读角色 `easyavr_ro` 的定义，新数据卷首次启动时自动执行（给只读查库/BI 用）。 |
| `deploy/mail/README.md` | `avr@easyavr.cn` 的 Zoho 免费版 DNS 记录与验证方法。 |
| `deploy/nginx/easyavr.cn.conf` | 独立 nginx 用的完整站点（80→301 + 443）；本机是 1Panel/openresty，只取 443 段落作参考。 |
| `deploy/nginx/easyavr.cn.http.conf` | 现网在用的版本：仅 80，放进 1Panel 的 `conf.d`。 |
| `deploy/systemd/easyavr.service` | 后端单元，`WorkingDirectory=/opt/easyavr/server`。 |
| `deploy/scripts/backup.sh` | SQLite 一致性备份 → `db/`，非敏感配置白名单备份 → `config/`（各保留 7 份），发布前做 gzip + `PRAGMA integrity_check` 验证；**不打包整个 env**，不含任何密钥/口令。 |
| `deploy/scripts/restore.sh` | SQLite 备份恢复：默认 dry-run，`--yes` 才执行；先留安全副本，再原子替换；不生成/轮换任何密钥。 |
| `deploy/scripts/pg_backup.sh` / `pg_restore.sh` | PostgreSQL `pg_dump -Fc` → `pg/` 与 `pg_restore -j4 --clean --if-exists` 恢复（默认 dry-run）；不生成密钥。 |
| `deploy/scripts/verify-tech003-postgres.sh` | TASK-013 隔离演练：显式 `PG_TEST_DSN` 才运行，自建/自删 `easyavr_pg003_*` 一次性库，真实跑备份→dry-run→恢复→校验→密钥轮换与错误密钥 fail-closed；不碰业务库，密钥只在进程内。 |
| `deploy/scripts/healthcheck.sh` | 单元/端点存活探测，连续失败 3 次重启服务。 |
| `deploy/cron.d/easyavr` | `/etc/cron.d` 片段，驱动备份/恢复相关脚本与健康检查（密钥流程不在 cron 里）。 |
| `deploy/logrotate/easyavr` | 切割两个 cron 日志（weekly / 4 份 / 压缩）。 |
| `deploy/env.example` | 环境变量模板，键名与 `config.go` 一致。 |
| `site/README.md` | 官网静态站的本地预览与约束备忘。 |

## 上线顺序

**1. 域名可用（这一步现在卡着）**

`.cn` 域名注册后必须完成实名认证，否则注册局把域名置 `clientHold` + `pendingVerification`，**解析整体不发布**——注册商面板里加多少条记录都查不到。判断方法：

```bash
dig easyavr.cn NS @a.dns.cn     # 权威服务器直接问，NXDOMAIN 就是没有委派
python3 - <<'PY'               # 本机没有 whois 命令时用它
import socket; s=socket.create_connection(("whois.cnnic.cn",43),timeout=10)
s.sendall(b"easyavr.cn\r\n"); print(s.recv(4096).decode("utf-8","replace"))
PY
```

看到 `Domain Status: clientHold` / `pendingVerification` 就去注册商后台做实名认证，不是改 DNS。

**2. NS 指对，A 记录生效**

阿里云注册的域名 NS 是 `dns9/dns10.hichina.com`（或 `ns1/ns2.aliyun.com`）。NS 正确后加 A 记录：`@`、`www`、`app`（同机则同一 IP），`demo` 只在有独立实例时加。`@` **不要**加 CNAME，会与 MX / TXT 互斥。

**3. 邮箱可收信**

按 `deploy/mail/README.md` 加验证 TXT → MX → SPF → DKIM → DMARC。顺序不能颠倒：域名解析不出来时 Zoho 的域名验证必然失败。

**4. TLS**

域名解析到位后给 `easyavr.cn`、`www.easyavr.cn`、`app.easyavr.cn` 签同一张证书。本机由 1Panel 管 openresty，
在面板里申请即可（`/.well-known/acme-challenge/` 已在 vhost 留好）；独立 nginx 才用
`certbot --nginx -d ...`。注意页面与媒体必须同为 https，证书别只签主域。

**5. 反向代理**

`easyavr.cn` → 静态站（1Panel 站点目录，见下文 `/www/sites/easyavr.cn/index`）；`app.easyavr.cn` → 后端 `:18000`
（后端直接托管 `web/dist`），其中 `/live/`、`/record/`、`/index/api/webrtc` 转发到 ZLM 的 `127.0.0.1:8080`。
SPA 必须走 `app` 子域，放根域会让访客撞登录框、且爬虫抓不到内容。

## 上线前的安全红线

平台代码里这几处没有鉴权或默认放行，公网暴露前必须处理：

- `/snapshots/*` 不受 JWT 保护（`<img>` 需要），外网可直接拉取快照图。
- `/VIID/*` 与 `/api/v1/cluster/heartbeat` 挂在 JWT 中间件之外——等于开放匿名写入，公网实例应关闭或加 IP 白名单。
- 首次启动会用 `EASYAVR_ADMIN_USER` / `EASYAVR_ADMIN_PASS` 建管理员，默认值 `easyavr/easyavr`，**部署前必须改**。
- 公开演示实例务必独立部署、独立数据库、只读账号，不要复用生产库。

### 审计日志中的历史敏感数据排查（TASK-001）

新请求**不再写入原始请求体**（`AuditLog.RequestBody` 字段仅为兼容旧数据保留）。但升级前已写入的审计记录可能含有密码/密钥明文，升级后应人工排查并按数据治理流程处置：

```sql
-- SQLite（开发库）
SELECT id, username, path, substr(request_body, 1, 120) AS snippet
FROM audit_logs
WHERE lower(request_body) LIKE '%password%' OR lower(request_body) LIKE '%secret%'
   OR lower(request_body) LIKE '%apikey%' OR lower(request_body) LIKE '%token%'
ORDER BY id DESC LIMIT 100;

-- PostgreSQL
SELECT id, username, path, left(request_body, 120) AS snippet
FROM audit_logs
WHERE request_body ILIKE ANY (ARRAY['%password%', '%secret%', '%apikey%', '%token%'])
ORDER BY id DESC LIMIT 100;
```

确认存在敏感行时：先按「备份与健康检查」做一次完整备份，再由人工执行脱敏（例如 `UPDATE audit_logs SET request_body = '' WHERE ...`），**不要**由自动化任务直接改写历史审计数据。审计写入失败不会中断业务请求，错误以 `[audit] write failed` 前缀写入服务日志（journald），运维可据此监控。

也可使用随仓库提供的 `auditscrub` 工具（默认只扫描、不改数据）：

```bash
cd /opt/easyavr/server            # 或本地 server/ 目录
go run ./cmd/auditscrub -driver sqlite -dsn ./data/easyavr.db
# 确认命中数后，人工备份并执行脱敏（SQLite 会自动生成备份文件）：
go run ./cmd/auditscrub -driver sqlite -dsn ./data/easyavr.db \
  -apply -yes -backup /var/backups/easyavr/db/audit-scrub-$(date +%F).db
# PostgreSQL：先用 pg_dump 备份，再传备份文件路径（工具不会替你生成 PG 备份）：
pg_dump "$EASYAVR_DB_DSN" -f /var/backups/easyavr/pg/audit-scrub.sql
go run ./cmd/auditscrub -driver postgres -dsn "$EASYAVR_DB_DSN" \
  -apply -yes -backup /var/backups/easyavr/pg/audit-scrub.sql
```

工具只处理 `request_body` 命中密码/密钥类关键词的行（大小写不敏感），每次最多 500 行（`-limit` 可调整），拒绝缺少 `--yes` 或备份文件/路径的执行；SQLite 备份通过 `VACUUM INTO` 创建且目标已存在时拒绝覆盖。生产库执行前仍须按人工流程完成备份审批，工具不会自动连接生产环境。

### GB28181 SIP 密钥配置（TASK-004）

- `GET /api/v1/gb/config` 与 `GET /api/v1/config/platform` **不返回密码明文**，只返回 `passwordConfigured` 布尔值；前端只显示「已配置（不回显）/未配置」。
- 管理员可通过 `PUT /api/v1/gb/config/password`（body `{"password":"..."}`）修改运行中的 SIP 摘要认证密码：对**后续 REGISTER 鉴权立即生效**，无需重启 SIP 服务；已注册设备保持原会话，重新注册时改用新密码。
- 该修改**即时生效并加密持久化**（写入 `platform_settings` 的 `gb.sip.password`，密文格式 `enc:<kid>:...`），服务重启后继续有效，且覆盖环境变量初值；不再修改 `/etc/easyavr/env`。如需清除密钥须由人工直接处理数据库/环境变量（接口未提供清除操作）。

### 凭据加密与生产安全启动（TASK-002 / TASK-003）

- 数据库里的设备密码、AI Provider API Key、对象存储 AccessKey/SecretKey、通知渠道 Secret/SMTP 密码、级联密码由 GORM 钩子透明加解密（AES-256-GCM，随机 nonce，密文带 `enc:<kid>:` 前缀）；`User.PasswordHash`、`APIKey.KeyHash` 本来就是哈希，不在加密范围。
- 主密钥由 `EASYAVR_CREDENTIAL_KEY` 提供（`openssl rand -base64 32` 生成），**不得写入数据库或与数据库备份一起存放**。生产环境（`EASYAVR_ENV=production`）只接受 32 字节随机密钥的 Base64/Hex 形式，拒绝弱口令与低熵模式；旧口令派生密钥只能放在 `EASYAVR_CREDENTIAL_KEY_PREVIOUS` 中用于解密历史密文，不能作为当前主密钥。启动时会把旧明文行加密、把旧 kid 的密文用当前密钥重加密；遇到未知 kid 或无法解密的数据会直接报错停止，不退回明文。轮换步骤见 `deploy/env.example` 顶部注释。
- `EASYAVR_ENV=production` 会在服务启动前检查：JWT 密钥/管理员密码/集群密钥/GB 口令不是公开默认值且长度足够、凭据主密钥已配置、CORS 无通配符、GB/GB35114 启用参数完整、证书/快照/数据库目录可写。错误信息只列变量名，不输出密钥值。开发环境（默认）保留原有便利默认值。

### PostgreSQL 迁移验证（TASK-001 遗留项）

自动迁移由 `store.OpenWith` 中的 GORM `AutoMigrate` 执行；SQLite 与 PostgreSQL 共用同一份模型定义。要在真实 PG 上验证（使用**临时库**，不要拿业务库做实验）：

```bash
# 本地开发容器：POSTGRES_USER=easyavr，已发布到 127.0.0.1:5432
docker exec easyavr-postgres psql -U easyavr -d postgres -c 'CREATE DATABASE easyavr_migverify;'
cd server
EASYAVR_TEST_PG_DSN='postgres://easyavr:easyavr@127.0.0.1:5432/easyavr_migverify?sslmode=disable' \
  go test ./internal/store -run TestPostgresMigration -v
docker exec easyavr-postgres psql -U easyavr -d postgres -c 'DROP DATABASE IF EXISTS easyavr_migverify;'
```

该测试验证 `audit_logs` 及其全部字段在 PG 上建表成功、且重复执行迁移安全（不设置 `EASYAVR_TEST_PG_DSN` 时自动跳过）。

## 可选组件

```bash
docker compose up -d                        # ZLMediaKit
docker compose --profile postgres up -d     # PostgreSQL（pgvector），EASYAVR_DB_DRIVER=postgres
docker compose --profile archive up -d      # MinIO，云归档用（9000/9001 仅绑回环）
```
## 生产环境（152.32.135.156，已部署）

当前状态：**HTTP 阶段**。后端以 systemd 跑在 `127.0.0.1:18000`，ZLM 在容器里（回环 8080/554/1935 +
对外 8000/udp、10000、30000-30500/udp），官网与平台由 **1Panel 管理的 openresty** 在 80 端口提供。
HTTPS 要等 `easyavr.cn` 实名认证通过、能签发证书后再切（切法见文末）。

### 这台机器的前提（和通用方案不一样，别照抄别处）

- 80/443 已归 1Panel 的 openresty，机器上还跑着 geo-engine、n8n、sxbarcode.com、ltgy.help 等站点，
  靠 `server_name` 分流。**不要另装 nginx**，也不要给我们的 server 块加 `default_server`。
- 1Panel 的 vhost 目录是 `/opt/1panel/www/conf.d/`（容器内 `/usr/local/openresty/nginx/conf/conf.d`）。
  `/opt/1panel/apps/openresty/openresty/conf/conf.d/` 是一份陈旧副本，**改了不生效**——这里踩过一次。
- openresty 容器只挂载 `/www` 和 `/opt/1panel/**`，**看不到 `/opt/easyavr`**。所以官网静态文件必须落在
  `/opt/1panel/www/sites/easyavr.cn/index/`（容器内 `/www/sites/easyavr.cn/index`）。
- 没有 `nginx` 二进制、没有 `certbot`；证书走 1Panel 面板申请。
- 宿主机 `8000/tcp` 被 geo-engine 的 Python 进程占着，所以 ZLM `[rtc] tcpPort=0`，WebRTC 只走 8000/udp。
- 磁盘偏紧（19G 用了 17G），装完 ffmpeg 与 ZLM 镜像后余量约 2.3G，长期录像/归档不要放本机。

### 目录结构（实机）

```
/opt/easyavr/
├── server/bin/easyavr          静态编译的二进制（CGO_ENABLED=0）
├── server/data/                SQLite、快照、GB35114 证书（systemd 唯一可写目录）
├── web/dist/                   前端产物，后端直接托管
├── deploy/                     仓库副本：zlm config.ini、nginx、systemd、scripts、cron.d、env.example、mail
└── docker-compose.prod.yml
/etc/easyavr/env                            0640 root:easyavr
/opt/1panel/www/sites/easyavr.cn/index/     官网静态文件（真正生效的一份）
/opt/1panel/www/conf.d/easyavr.cn.conf      vhost，对应 deploy/nginx/easyavr.cn.http.conf
```

### 安装步骤（本次实际执行的顺序，可重放）

**1. 构建产物（本地）**

```bash
cd server && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/easyavr ./cmd/easyavr
cd web && npm run build          # 产出 web/dist
```

必须 `CGO_ENABLED=0`：默认构建会链到本机 glibc，目标机版本不同时可能起不来；静态链接的二进制更稳。

**2. 装依赖**

```bash
apt-get install -y ffmpeg fonts-noto-cjk
```

没有 ffmpeg 时 AI 取帧、快照、派生转码全部失败（平台是 shell 出 ffmpeg 的）；drawtext 需要 CJK 字体，
`FontCandidates` 优先找 NotoSansCJK，缺字体会让转码任务直接报错。

**3. 建用户、上传、装环境变量**

```bash
useradd --system --home-dir /opt/easyavr --shell /usr/sbin/nologin easyavr
# tar 上传 server/bin/easyavr、web/dist、deploy/、docker-compose.prod.yml 到 /opt/easyavr
chown -R root:root /opt/easyavr && chmod 0755 /opt/easyavr/server/bin/easyavr
install -d -o easyavr -g easyavr -m 0750 /opt/easyavr/server/data
install -d -m 0755 /etc/easyavr
# 以 env.example 为底，替换 JWT_SECRET / ADMIN_USER / ADMIN_PASS / GB_PASSWORD 等敏感值
install -m 0640 -o root -g easyavr /tmp/env /etc/easyavr/env
install -m 0644 /opt/easyavr/deploy/systemd/easyavr.service /etc/systemd/system/easyavr.service
systemctl daemon-reload && systemctl enable --now easyavr
```

`EnvironmentFile` 不认行尾注释：`KEY=value  # 说明` 会把 `# 说明` 当成值的一部分。

**4. 起 ZLM**

```bash
# 服务器上那份 config.ini：externIP 写死公网 IP、tcpPort 置 0（8000/tcp 被占用）
cd /opt/easyavr && docker compose -f docker-compose.prod.yml up -d
```

`externIP` 只改 `/opt` 下的副本，不动仓库文件。这个版本的 ZLM **不做环境变量替换**，写 `$EXTERN_IP`
只会得到字符串本身（实测）。

**5. 装 openresty vhost**

```bash
install -m 0644 deploy/nginx/easyavr.cn.http.conf /opt/1panel/www/conf.d/easyavr.cn.conf
mkdir -p /opt/1panel/www/sites/easyavr.cn/index && cp -a site/. /opt/1panel/www/sites/easyavr.cn/index/
chown -R root:root /opt/1panel/www/sites/easyavr.cn
docker exec 1Panel-openresty-GeCi nginx -t && docker exec 1Panel-openresty-GeCi nginx -s reload
```

改完必须 `nginx -t` 再 reload：同一份 openresty 还托着别家站点，语法错了会连带它们一起挂。

### 验证清单

| 检查 | 命令 | 期望 |
|---|---|---|
| 后端存活 | `systemctl is-active easyavr` | `active` |
| 登录 | `curl -sX POST http://127.0.0.1:18000/api/v1/auth/login -H 'Content-Type: application/json' -d '{"username":"admin","password":"..."}'` | `"code":0` |
| ZLM 连通 | `curl -s -H 'Authorization: Bearer <token>' http://127.0.0.1:18000/api/v1/system/info` | `"zlmHealthy":true` |
| 官网 | `curl -s -H 'Host: easyavr.cn' http://127.0.0.1/ \| grep title` | 官网标题 |
| 平台 | `curl -s -H 'Host: app.easyavr.cn' http://127.0.0.1/ \| grep title` | `EasyAVR · AI 原生视频融合平台` |
| 域名未生效时的外网自测 | `curl --resolve easyavr.cn:80:152.32.135.156 http://easyavr.cn/` | 200 |

管理员初始口令在 `/root/easyavr-credentials.txt`（0600），登录后立即改并记到密码管理器。

### 切 HTTPS（域名实名通过后）

1. `dig easyavr.cn A @223.5.5.5` 确认解析已发布（`clientHold` 期间做什么都没用）。
2. 1Panel 面板为 `easyavr.cn`、`app.easyavr.cn` 申请证书（HTTP-01，`/.well-known/acme-challenge/`
   已经在 vhost 里留好了 location）。
3. 把 `deploy/nginx/easyavr.cn.conf` 的 443 段落到现网 vhost：`easyavr.cn`/`www` 与 `app.easyavr.cn`
   两个 server，80 改 301，`root` 保留 `/www/sites/easyavr.cn/index`，证书路径指向 1Panel 签发结果。
4. `/etc/easyavr/env` 改三处：`EASYAVR_MEDIA_SCHEME=https`、`EASYAVR_ZLM_HTTP_PORT=443`、
   `EASYAVR_ALLOW_ORIGIN` 与 `EASYAVR_PLAY_WHITELIST` 全部换成 `https://`。
5. `systemctl restart easyavr`，再 `nginx -t && nginx -s reload`。
6. 页面打开后确认播放：https 页面加载 http 媒体会被浏览器按混合内容拦掉，这一步必须实测。

### 防火墙

| 端口 | 协议 | 用途 |
|---|---|---|
| 22 | tcp | SSH |
| 80 / 443 | tcp | openresty（官网 + 平台反代） |
| 8000 | udp | WebRTC 媒体（单端口复用） |
| 10000 | udp + tcp | GB28181 收流 |
| 30000-30500 | udp | GB28181 设备推流 |

`8000/tcp` 不发布（宿主被占用，且 `tcpPort=0`）。不需要 `49152-65535`：`[rtc] port=8000` 是单端口复用，
那一段 `port_range` 只在显式启用 `icePort`/旧 TURN 模式才用。

Docker 自己往 iptables 写规则，`ufw` 管不住它发布的端口；控制暴露只能靠 compose 里的绑定地址——
`docker-compose.prod.yml` 已经把 HTTP/RTSP/RTMP 绑在 `127.0.0.1`。

`EASYAVR_GB_ENABLED=false` 时不用开 5060；要启用国标接入再放通 `5060/udp+tcp`，并把
`EASYAVR_GB_PASSWORD` 从默认值改掉——那个端口是全网可达的。

GB28181 并发受端口区间限制：`30000-30500` 共 501 个端口，按每路约 2 个估算 ≈250 路；要更多就同时放宽
compose 区间与 `ufw` 规则（`config.ini` 的 `[rtp_proxy] port_range` 上限是 35000）。

### 运维

```bash
journalctl -u easyavr -f                                   # 后端日志
docker compose -f /opt/easyavr/docker-compose.prod.yml logs -f zlm
systemctl restart easyavr
# 更新后端：替换二进制后重启
install -m 0755 new/easyavr /opt/easyavr/server/bin/easyavr && systemctl restart easyavr
# 备份见下方「备份与健康检查」，别再用 sqlite3 .backup：生产机没有 sqlite3 命令
```

### 备份与健康检查

日常运维由 `deploy/cron.d/easyavr` 驱动，备份分三层存放（EASYAVR-TECH-002 / TASK-002）：

| 层 | 位置 | 内容 | 谁负责 |
|---|---|---|---|
| DB | `/var/backups/easyavr/db/` | `easyavr-YYYYmmdd-HHMMSS.db.gz`（SQLite）/ `easyavr-pg-*.dump`（PG 装在 `pg/`） | `backup.sh` / `pg_backup.sh` 自动 |
| 配置 | `/var/backups/easyavr/config/` | `config-YYYYmmdd-HHMMSS.gz`，**仅白名单非敏感键** | `backup.sh` 自动 |
| 主密钥 | 密码管理器 / 外部 KMS / 独立离线介质 | `EASYAVR_CREDENTIAL_KEY`，可以配合 `EASYAVR_CREDENTIAL_KEY_ID` / `_PREVIOUS` | **只能人工**，绝不进上述任何目录 |

| 脚本 | 用途 | 触发 |
|---|---|---|
| `deploy/scripts/backup.sh` | 用 python3 的 `sqlite3.Connection.backup()` 热备 SQLite（WAL 下 `cp` 会拿到不一致快照，生产机也没有 `sqlite3` 命令）到 `db/`；另用显式白名单从 `/etc/easyavr/env` 提取非敏感键压成 `config/*.gz`（0600）。每类只留最近 `KEEP` 份（默认 7）。 | 每天 03:30 |
| `deploy/scripts/healthcheck.sh` | 查 `systemctl is-active easyavr` 与 `/api/v1/healthz` 里的 `"code":0`；失败计数写 `/var/tmp/easyavr-healthcheck.fails`，**连续 3 次**才重启，成功后清零。 | 每 5 分钟 |

配置备份的白名单在 `backup.sh` 的 `CONFIG_KEYS` 数组里显式列出（如 `EASYAVR_ENV`、`EASYAVR_LISTEN`、`EASYAVR_DB_DRIVER`、`EASYAVR_ZLM_API`、`EASYAVR_ALLOW_ORIGIN`、`EASYAVR_MONITOR_SEC`、`EASYAVR_MEDIA_HOST` 等）。**不在白名单的未知变量不写；任何 `*_KEY` / `*_SECRET` / `*_PASSWORD` / `*_TOKEN`（以及 `EASYAVR_CREDENTIAL_KEY_ID` / `_PREVIOUS` 这类变体）即使出现在 env 里也不写**；生成器还会检查白名单输出里不能出现任何秘密变量的取值，否则拒绝发布。已不再产生 `env-*.gz`：整份 env 从不被打包。

产物固定在 `/var/backups/easyavr/{db,config,pg}`：DB 为 `easyavr-YYYYmmdd-HHMMSS.db.gz`，配置为 `config-YYYYmmdd-HHMMSS.gz`，PG 为 `pg/easyavr-pg-*.dump`。各目录 0700、文件 0600，脚本用 `flock` 防止备份重叠；轮转只删各自目录里自己生成的文件，因此不可能误删主密钥（它根本不在这些目录）。可配置项（环境变量）：`DB`、`BACKUP_DIR`（备份根，默认 `/var/backups/easyavr`）、`DB_BACKUP_DIR`、`CONFIG_BACKUP_DIR`、`KEEP`、`ENV_FILE`；健康检查侧是 `SERVICE`、`HEALTH_URL`、`STATE_FILE`、`MAX_FAILS`、`RESTART_CMD`。

**安装**

```bash
install -d -m 0755 /opt/easyavr/deploy/scripts /opt/easyavr/deploy/cron.d
install -m 0755 deploy/scripts/backup.sh deploy/scripts/healthcheck.sh /opt/easyavr/deploy/scripts/
cp deploy/cron.d/easyavr /etc/cron.d/easyavr
chmod 0644 /etc/cron.d/easyavr
```

`/etc/cron.d` 的文件属主必须是 root、不能被组/其他用户写，否则 cron 会静默忽略。

**手动验证一次**

```bash
/opt/easyavr/deploy/scripts/backup.sh      # 末尾打印 db_dir / config_dir / 各自份数
/opt/easyavr/deploy/scripts/healthcheck.sh # 正常时输出一行 "... ok"
gzip -t /var/backups/easyavr/db/easyavr-*.db.gz && echo db-gzip-ok
gzip -t /var/backups/easyavr/config/config-*.gz && echo config-gzip-ok
gunzip -c /var/backups/easyavr/config/config-*.gz | grep -E 'KEY|SECRET|PASSWORD|TOKEN' && echo LEAK || echo no-secrets
systemctl status easyavr --no-pager | head -3
```

健康检查若想验证重启分支，先把 `RESTART_CMD` 覆盖成 `echo`（`RESTART_CMD=echo HEALTH_URL=http://127.0.0.1:1/healthz .../healthcheck.sh`，跑 3 次），避免真的重启生产服务。

**恢复**

```bash
# 0) 先确认原主密钥已由人工恢复到 /etc/easyavr/env（或外部 KMS 注入），
#    且 EASYAVR_CREDENTIAL_KEY_ID / _PREVIOUS 与备份时的 kid 一致。
#    密钥丢失则 enc:<kid>:... 加密凭据不可恢复；任何脚本都不会生成新密钥替代。
systemctl stop easyavr
cp /opt/easyavr/server/data/easyavr.db /root/easyavr.db.before-restore   # 先留底
rm -f /opt/easyavr/server/data/easyavr.db-wal /opt/easyavr/server/data/easyavr.db-shm
gunzip -c /var/backups/easyavr/db/easyavr-YYYYmmdd-HHMMSS.db.gz > /opt/easyavr/server/data/easyavr.db
chown easyavr:easyavr /opt/easyavr/server/data/easyavr.db
chmod 0640 /opt/easyavr/server/data/easyavr.db
systemctl start easyavr && curl -fsS http://127.0.0.1:18000/api/v1/healthz
```

必须在停服务后删掉残留的 `-wal`/`-shm`：否则 SQLite 会把旧 WAL 回放到刚恢复的库上，数据对不上。配置备份 `config-*.gz` 只含白名单非敏感键，**不能**用来还原整份 `/etc/easyavr/env`；env 和主密钥都必须人工按密码管理器/KMS 中的记录恢复（`/etc/easyavr/env` 保持 `0640 root:easyavr`）。

**日志**

cron 把输出追加到 `/var/log/easyavr-backup.log` 和 `/var/log/easyavr-health.log`。健康检查每 5 分钟一行（约 100 行/天，日志行尾带根分区占用百分比），备份每天 4~5 行，一年不到 200KB——增长很慢但确实无上限，所以配了切割：

```bash
install -m 0644 deploy/logrotate/easyavr /etc/logrotate.d/easyavr
logrotate -d /etc/logrotate.d/easyavr     # 干跑，确认配置被解析
```

`squid` 之类的通用规则不覆盖这两个文件，必须是上面这条独立配置（weekly / 保留 4 份 / 压缩）。备份目录本身受份数上限约束，不受日志增长影响。

## 备份恢复、完整性校验与 PG 备份（TASK-013）

本节是追加说明，不替代上面的现网操作流程。新增脚本一并安装：

```bash
install -m 0755 deploy/scripts/backup.sh deploy/scripts/restore.sh \
  deploy/scripts/pg_backup.sh deploy/scripts/pg_restore.sh \
  deploy/scripts/healthcheck.sh /opt/easyavr/deploy/scripts/
```

### SQLite 备份的完整性与失败语义

`backup.sh` 在发布正式文件之前依次做 `gzip -t` 和**解压后**的 `PRAGMA integrity_check`；日志会打印两行校验结果：

```text
... verify: gzip -t ok
... verify: PRAGMA integrity_check=ok
```

任一步失败：保留 `.easyavr-<TS>.db.part` / `.db.gz.part` 供排查，报错以非 0 退出，**不生成** `easyavr-<TS>.db.gz`，也不会轮转掉旧备份。手工复核任一备份：

```bash
gzip -t /var/backups/easyavr/db/easyavr-YYYYmmdd-HHMMSS.db.gz
gunzip -c /var/backups/easyavr/db/easyavr-YYYYmmdd-HHMMSS.db.gz > /tmp/check.db
python3 -c 'import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); print(c.execute("PRAGMA integrity_check").fetchone()[0])' /tmp/check.db
# 期望输出 ok；最后删除 /tmp/check.db
```

### SQLite 恢复（推荐用脚本，别再手工 gunzip 覆盖）

```bash
BK=/var/backups/easyavr/db/easyavr-YYYYmmdd-HHMMSS.db.gz
DB=/opt/easyavr/server/data/easyavr.db

systemctl stop easyavr
/opt/easyavr/deploy/scripts/restore.sh -f "$BK" -d "$DB"            # 默认 dry-run：只校验并打印计划
/opt/easyavr/deploy/scripts/restore.sh -f "$BK" -d "$DB" --yes      # 确认计划后执行
systemctl start easyavr && curl -fsS http://127.0.0.1:18000/api/v1/readyz   # readyz 会查 DB SELECT 1
```

脚本会先打印 `verify: gzip -t ok`，再打印替换前/后的 `PRAGMA integrity_check` 结果；**恢复前**自动留 `easyavr.db.before-restore-<TS>-<PID>` 安全副本，权限沿用旧库（新路径为 0640）。它只做同目录 `.restore.tmp` → `mv` 的原子替换，绝不删除业务数据，也**不碰 `-wal` / `-shm`**：若目标旁存在残留会打印警告，仍需人工在停服务状态下处理，避免旧 WAL 回放覆盖恢复结果。脚本每次运行都会打印主密钥提醒，但**不会生成新密钥、也不会改写 env**：恢复前必须人工恢复原 `EASYAVR_CREDENTIAL_KEY`，否则库里的加密凭据全部解不开；白名单配置备份不能替代 env 还原。

### PostgreSQL 备份与恢复

仅当 `EASYAVR_DB_DRIVER=postgres` 时使用；DSN 写在 root 可读的 0600 文件里，脚本日志不输出 DSN/口令。

```bash
# 备份：pg_dump -Fc + timestamp 文件 + 非空/pg_restore -l 校验 + PG_KEEP 轮转
PG_DSN='postgres://user:pass@127.0.0.1:5432/easyavr?sslmode=disable' \
PG_BACKUP_DIR=/var/backups/easyavr/pg PG_KEEP=7 \
  /opt/easyavr/deploy/scripts/pg_backup.sh

# 恢复：默认 dry-run；--yes 才执行 pg_restore -j4 --clean --if-exists
PG_DSN='postgres://user:pass@127.0.0.1:5432/easyavr?sslmode=disable' \
  /opt/easyavr/deploy/scripts/pg_restore.sh \
  -f /var/backups/easyavr/pg/easyavr-pg-YYYYmmdd-HHMMSS-NNN.dump     # dry-run，打印计划
PG_DSN='postgres://user:pass@127.0.0.1:5432/easyavr?sslmode=disable' \
  /opt/easyavr/deploy/scripts/pg_restore.sh \
  -f /var/backups/easyavr/pg/easyavr-pg-YYYYmmdd-HHMMSS-NNN.dump --yes
```

`pg_restore.sh` 也接受 `-d <DSN>` 覆盖环境变量；并行度用 `PG_RESTORE_JOBS` 调整（默认 4）。`--clean --if-exists` 会 DROP 目标库中已有对象，执行前务必确认已备份。恢复完成后用 `/api/v1/readyz`（含 DB `SELECT 1`）和只读查询验证。

> 版本偏差：PostgreSQL 17 的 `pg_dump` 会在归档里写入服务端未知参数 `SET transaction_timeout = 0`；恢复到 PG 16 及更旧服务端时 `pg_restore` 会把它列为 ignored error 并非零退出，但**数据已完整恢复**。`pg_restore.sh` 只对这一条已知告警放行并打印 WARNING，其他错误仍判定失败。长期方案是让备份客户端版本与生产服务端一致。

### 备份恢复隔离演练（TASK-013 验证脚本）

真实演练不要拿业务库 `easyavr` 做实验。脚本只接受显式传入的**可写维护库** DSN，且维护库名必须是 `postgres`；源/目标库由脚本自建为 `easyavr_pg003_<随机>_src` / `_dst`，退出时 trap 停进程并 DROP。未传 `PG_TEST_DSN` 打印 SKIP 并以非 0 退出；任何指向 `easyavr` 或生产样式库名的 DSN 在建库前被拒绝。日志只打印脱敏 DSN，主密钥/口令只在演练进程的环境变量和 shell 变量中，不落文件。

```bash
PG_TEST_DSN='postgres://<可写维护账号>:<口令>@127.0.0.1:5432/postgres?sslmode=disable' \
  bash deploy/scripts/verify-tech003-postgres.sh
# 输出逐项 PASS / FAIL / SKIP，末尾 SUMMARY；有 FAIL 或清理告警则退出非 0。
```

演练内容：用 EasyAVR 自身（生产模式 + PG 驱动）AutoMigrate 建表并经 API/GORM hook 写入设备密码、AIProvider APIKey 等敏感数据，再跑 `pg_backup.sh`（非空/PGDMP 魔数/`pg_restore -l`/权限 0600）、`pg_restore.sh` dry-run（确认零写入）与 `--yes`，对比表结构/索引/约束/行数/关联与密文前缀，最后验证 v1 解密往返、错误密钥 fail-closed、v1→v2 轮换重加密。真实执行结果见 `docs/testing/postgres-backup-restore-report.md`。

### 三层备份与主密钥独立流程（EASYAVR-TECH-002 / TASK-002）

1. **DB 层**：SQLite 一致性快照落在 `/var/backups/easyavr/db`（PG 的 `pg_dump -Fc` 落在 `/var/backups/easyavr/pg`）。文件 0600、目录 0700。
2. **配置层**：`/var/backups/easyavr/config/config-*.gz` 由 `backup.sh` 通过显式白名单生成，只含 `EASYAVR_ENV`、`EASYAVR_ZLM_API`、`EASYAVR_ALLOW_ORIGIN`、`EASYAVR_MONITOR_SEC` 等非秘密项，便于确认运行参数；它不是 env 的完整副本，不可用于还原整份环境。
3. **密钥层**：`EASYAVR_CREDENTIAL_KEY` 是设备密码 / AI Provider API Key / 对象存储密钥等密文（`enc:<kid>:...`）的主密钥，只能由人工或外部 KMS 备份到与 `/var/backups/easyavr`（含 `db/`、`pg/`、`config/`）**物理分离**的位置，例如密码管理器或异地密钥库。备份时用 `openssl rand -base64 32` 生成并写入 0600、root 专属文件；同时记录 `EASYAVR_CREDENTIAL_KEY_ID` 与轮换时的 `EASYAVR_CREDENTIAL_KEY_PREVIOUS`（轮换方法见 `deploy/env.example` 顶部注释）。

风险边界：库备份泄露 + 同处密钥 = 全部凭据明文泄露；库备份泄露而密钥在别处 = 密文不可解；**密钥丢失则加密凭据不可恢复**。`backup.sh` 不打包整份 env，`pg_backup.sh` 只导出数据库，任何脚本都不会把主密钥写进备份目录或归档。恢复数据库前必须先由人工把原密钥恢复到 `/etc/easyavr/env`（或由 KMS 注入），两个 `restore.sh` / `pg_restore.sh` 只打印提醒、绝不生成新密钥——用新密钥只能让所有 `enc:<kid>:...` 行解密失败。轮换密钥时按 `EASYAVR_CREDENTIAL_KEY_PREVIOUS=<kid>:<旧密钥>` 启动一次，让服务自动用旧 kid 解密、新 kid 重加密，之后再移除旧项。

### cron 示例（追加，不要覆盖历史条目）

向现有 `/etc/cron.d/easyavr` **追加**新行，不要重建文件；文件必须以换行结尾、属主 root 且不可组/其他可写：

```bash
cat >> /etc/cron.d/easyavr <<'EOF'

# 每天 04:30 PostgreSQL 备份（仅 postgres 部署；DSN 不写在本文件）
# 30 4 * * * root bash -c 'set -a; . /etc/easyavr/pg-backup.env; set +a; PG_BACKUP_DIR=/var/backups/easyavr/pg /opt/easyavr/deploy/scripts/pg_backup.sh' >> /var/log/easyavr-pg-backup.log 2>&1
EOF
chmod 0644 /etc/cron.d/easyavr
```

SQLite 的每日 03:30 备份与健康检查条目已在 `deploy/cron.d/easyavr` 中，追加时保留原样。同一条 `backup.sh` 任务会同时写 `db/`（SQLite）与 `config/`（白名单配置），无需另加 cron 行；`pg-backup.env` 内容示例：`PG_DSN='postgres://...'`，权限 0600 root:root。主密钥**不通过 cron 备份**，只在人工/KMS 流程里处理。
