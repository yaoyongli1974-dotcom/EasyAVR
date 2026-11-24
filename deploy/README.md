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
| `deploy/scripts/backup.sh` | SQLite + env 热备份，按份数轮转（默认保留 7 份）。 |
| `deploy/scripts/healthcheck.sh` | 单元/端点存活探测，连续失败 3 次重启服务。 |
| `deploy/cron.d/easyavr` | `/etc/cron.d` 片段，驱动上面两个脚本。 |
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
  -apply -yes -backup /var/backups/easyavr/audit-scrub-$(date +%F).db
# PostgreSQL：先用 pg_dump 备份，再传备份文件路径（工具不会替你生成 PG 备份）：
pg_dump "$EASYAVR_DB_DSN" -f /var/backups/easyavr/audit-scrub.sql
go run ./cmd/auditscrub -driver postgres -dsn "$EASYAVR_DB_DSN" \
  -apply -yes -backup /var/backups/easyavr/audit-scrub.sql
```

工具只处理 `request_body` 命中密码/密钥类关键词的行（大小写不敏感），每次最多 500 行（`-limit` 可调整），拒绝缺少 `--yes` 或备份文件/路径的执行；SQLite 备份通过 `VACUUM INTO` 创建且目标已存在时拒绝覆盖。生产库执行前仍须按人工流程完成备份审批，工具不会自动连接生产环境。

### GB28181 SIP 密钥配置（TASK-004）

- `GET /api/v1/gb/config` 与 `GET /api/v1/config/platform` **不返回密码明文**，只返回 `passwordConfigured` 布尔值；前端只显示「已配置（不回显）/未配置」。
- 管理员可通过 `PUT /api/v1/gb/config/password`（body `{"password":"..."}`）修改运行中的 SIP 摘要认证密码：对**后续 REGISTER 鉴权立即生效**，无需重启 SIP 服务；已注册设备保持原会话，重新注册时改用新密码。
- 该修改**即时生效并加密持久化**（写入 `platform_settings` 的 `gb.sip.password`，密文格式 `enc:<kid>:...`），服务重启后继续有效，且覆盖环境变量初值；不再修改 `/etc/easyavr/env`。如需清除密钥须由人工直接处理数据库/环境变量（接口未提供清除操作）。

### 凭据加密与生产安全启动（TASK-002 / TASK-003）

- 数据库里的设备密码、AI Provider API Key、对象存储 AccessKey/SecretKey、通知渠道 Secret/SMTP 密码、级联密码由 GORM 钩子透明加解密（AES-256-GCM，随机 nonce，密文带 `enc:<kid>:` 前缀）；`User.PasswordHash`、`APIKey.KeyHash` 本来就是哈希，不在加密范围。
- 主密钥由 `EASYAVR_CREDENTIAL_KEY` 提供（`openssl rand -base64 32` 生成），**不得写入数据库或与数据库备份一起存放**。启动时会把旧明文行加密、把旧 kid 的密文用当前密钥重加密；遇到未知 kid 或无法解密的数据会直接报错停止，不退回明文。轮换步骤见 `deploy/env.example` 顶部注释。
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

两个脚本覆盖生产机的日常运维，由 `deploy/cron.d/easyavr` 驱动。

| 脚本 | 用途 | 触发 |
|---|---|---|
| `deploy/scripts/backup.sh` | 用 python3 的 `sqlite3.Connection.backup()` 热备 SQLite（WAL 下 `cp` 会拿到不一致快照，生产机也没有 `sqlite3` 命令），同时把 `/etc/easyavr/env` 压成 `env-*.gz`（0600）；每类只留最近 `KEEP` 份（默认 7）。 | 每天 03:30 |
| `deploy/scripts/healthcheck.sh` | 查 `systemctl is-active easyavr` 与 `/api/v1/healthz` 里的 `"code":0`；失败计数写 `/var/tmp/easyavr-healthcheck.fails`，**连续 3 次**才重启，成功后清零。 | 每 5 分钟 |

产物固定在 `/var/backups/easyavr`：`easyavr-YYYYmmdd-HHMMSS.db.gz` 与 `env-YYYYmmdd-HHMMSS.gz`。目录 0700，脚本用 `flock` 防止备份重叠。可配置项（环境变量）：`DB`、`BACKUP_DIR`、`KEEP`、`ENV_FILE`；健康检查侧是 `SERVICE`、`HEALTH_URL`、`STATE_FILE`、`MAX_FAILS`、`RESTART_CMD`。

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
/opt/easyavr/deploy/scripts/backup.sh      # 应输出 4~5 行带时间戳日志，末尾打印大小与份数
/opt/easyavr/deploy/scripts/healthcheck.sh # 正常时输出一行 "... ok"
gzip -t /var/backups/easyavr/easyavr-*.db.gz && echo gzip-ok
systemctl status easyavr --no-pager | head -3
```

健康检查若想验证重启分支，先把 `RESTART_CMD` 覆盖成 `echo`（`RESTART_CMD=echo HEALTH_URL=http://127.0.0.1:1/healthz .../healthcheck.sh`，跑 3 次），避免真的重启生产服务。

**恢复**

```bash
systemctl stop easyavr
cp /opt/easyavr/server/data/easyavr.db /root/easyavr.db.before-restore   # 先留底
rm -f /opt/easyavr/server/data/easyavr.db-wal /opt/easyavr/server/data/easyavr.db-shm
gunzip -c /var/backups/easyavr/easyavr-YYYYmmdd-HHMMSS.db.gz > /opt/easyavr/server/data/easyavr.db
chown easyavr:easyavr /opt/easyavr/server/data/easyavr.db
chmod 0640 /opt/easyavr/server/data/easyavr.db
systemctl start easyavr && curl -fsS http://127.0.0.1:18000/api/v1/healthz
```

必须在停服务后删掉残留的 `-wal`/`-shm`：否则 SQLite 会把旧 WAL 回放到刚恢复的库上，数据对不上。`env-*.gz` 同理 `gunzip` 回 `/etc/easyavr/env` 并保持 `0640 root:easyavr`。

**日志**

cron 把输出追加到 `/var/log/easyavr-backup.log` 和 `/var/log/easyavr-health.log`。健康检查每 5 分钟一行（约 100 行/天，日志行尾带根分区占用百分比），备份每天 4~5 行，一年不到 200KB——增长很慢但确实无上限，所以配了切割：

```bash
install -m 0644 deploy/logrotate/easyavr /etc/logrotate.d/easyavr
logrotate -d /etc/logrotate.d/easyavr     # 干跑，确认配置被解析
```

`squid` 之类的通用规则不覆盖这两个文件，必须是上面这条独立配置（weekly / 保留 4 份 / 压缩）。备份目录本身受份数上限约束，不受日志增长影响。
