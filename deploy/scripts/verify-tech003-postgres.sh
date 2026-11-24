#!/usr/bin/env bash
# EASYAVR-TECH-003 / TASK-013: PostgreSQL 备份恢复与凭据密钥演练（隔离一次性库）。
#
# 安全约束：
#   - 必须显式传 PG_TEST_DSN=<本地可写维护库 DSN> 才运行；未传打印 SKIP 并以非 0 退出。
#   - 维护库只允许 postgres；测试库只允许本脚本自建的 easyavr_pg003_<hex>_src/_dst。
#     业务库 easyavr 与任何生产样式库名一律拒绝，脚本从不连接它们。
#   - 主密钥/管理员口令/测试凭据只在进程环境变量与 shell 变量中，不写文件、不写日志；
#     日志只出现脱敏 DSN。退出时 trap 停进程、删临时目录、DROP 两个一次性库。
#
# 用法：PG_TEST_DSN='postgres://<user>:<pass>@127.0.0.1:5432/postgres?sslmode=disable' \
#       bash deploy/scripts/verify-tech003-postgres.sh
# 退出码：0=全部 PASS（可含 SKIP），1=有 FAIL/清理告警，2=缺少 PG_TEST_DSN。

set -uo pipefail
umask 077

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
ROOT=$(cd -- "$SCRIPT_DIR/../.." && pwd)
SERVER_DIR="$ROOT/server"

PASS=0
FAIL=0
SKIP=0
CLEANUP_WARN=0
PIDS=()
DBS_CREATED=()
SRC_DB=''
DST_DB=''
TMP=''

log() { printf '%s %s\n' "$(date '+%F %T')" "$*"; }
pass() { PASS=$((PASS + 1)); log "PASS  $*"; }
fail() { FAIL=$((FAIL + 1)); log "FAIL  $*"; }
skip() { SKIP=$((SKIP + 1)); log "SKIP  $*"; }
heading() { log ""; log "== $* =="; }
has_cmd() { command -v "$1" >/dev/null 2>&1; }

if [[ -z ${PG_TEST_DSN:-} ]]; then
  log "SKIP  PG_TEST_DSN 未设置；未连接任何数据库，未做任何改动。"
  log "       用法: PG_TEST_DSN='postgres://<可写维护账号>@127.0.0.1:5432/postgres?sslmode=disable' bash $0"
  log "SUMMARY PASS=0 FAIL=0 SKIP=1 CLEANUP_WARN=0"
  exit 2
fi

TMP=$(mktemp -d "${TMPDIR:-/tmp}/verify-tech003-pg.XXXXXX") || { log "FAIL  无法创建临时目录"; exit 1; }

cleanup() {
  local rc=$? pid db i
  trap - EXIT
  if ((${#PIDS[@]})); then
    for pid in "${PIDS[@]}"; do
      if kill -0 "$pid" 2>/dev/null; then
        kill "$pid" 2>/dev/null || true
        for i in $(seq 1 30); do
          kill -0 "$pid" 2>/dev/null || break
          sleep 0.1
        done
        if kill -0 "$pid" 2>/dev/null; then
          kill -9 "$pid" 2>/dev/null || true
          log "WARNING 进程 $pid 未响应 TERM，已 SIGKILL"
          CLEANUP_WARN=$((CLEANUP_WARN + 1))
        fi
      fi
      wait "$pid" 2>/dev/null || true
    done
  fi
  if ((${#DBS_CREATED[@]})); then
    for db in "${DBS_CREATED[@]}"; do
      # 只 DROP 本脚本创建且名字合规的一次性库；任何异常名字都只告警，绝不执行 DROP。
      if [[ -n ${PG_TEST_DSN:-} && $db =~ ^easyavr_pg003_[0-9a-f]{8}_(src|dst)$ ]]; then
        if psql "$PG_TEST_DSN" -v ON_ERROR_STOP=1 -q -c "DROP DATABASE IF EXISTS \"$db\" WITH (FORCE)" >/dev/null 2>&1; then
          log "cleanup: dropped $db"
        else
          log "WARNING 清理失败: 无法 DROP $db，请人工处理"
          CLEANUP_WARN=$((CLEANUP_WARN + 1))
        fi
      else
        log "WARNING 清理跳过非法库名: $db"
        CLEANUP_WARN=$((CLEANUP_WARN + 1))
      fi
    done
  fi
  if ! rm -rf -- "$TMP"; then
    log "WARNING 清理失败: 无法删除临时目录 $TMP"
    CLEANUP_WARN=$((CLEANUP_WARN + 1))
  fi
  log "SUMMARY PASS=$PASS FAIL=$FAIL SKIP=$SKIP CLEANUP_WARN=$CLEANUP_WARN"
  if ((FAIL > 0 || CLEANUP_WARN > 0 || rc != 0)); then exit 1; fi
  exit 0
}
trap cleanup EXIT

heading "前置检查"
for tool in psql pg_dump pg_restore python3 openssl curl go bash; do
  if has_cmd "$tool"; then pass "工具可用: $tool"; else fail "工具缺失: $tool"; fi
done
for f in pg_backup.sh pg_restore.sh; do
  if bash -n "$SCRIPT_DIR/$f"; then pass "bash -n $f"; else fail "bash -n $f"; fi
done
if ((FAIL > 0)); then exit 1; fi

heading "DSN 安全门（拒绝业务库，打印脱敏 DSN）"
if ! META=$(python3 - "$PG_TEST_DSN" <<'PY'
import secrets, sys
from urllib.parse import urlsplit, urlunsplit, parse_qsl, urlencode
raw = sys.argv[1]
p = urlsplit(raw)
q = parse_qsl(p.query, keep_blank_values=True)
d = dict(q)
db = d.get('dbname') or p.path.lstrip('/') or ''
user = d.get('user') or p.username or ''
password = d.get('password') or p.password or ''
host = d.get('host') or p.hostname or ''
port = d.get('port') or p.port or '5432'
if '@' in p.netloc:
    auth, hostpart = p.netloc.rsplit('@', 1)
    auth_red = (auth.split(':', 1)[0] + ':***') if ':' in auth else '***'
    rednet = auth_red + '@' + hostpart
else:
    rednet = p.netloc
red = urlunsplit((p.scheme, rednet, p.path, p.query, p.fragment))
suffix = secrets.token_hex(4)
src, dst = 'easyavr_pg003_%s_src' % suffix, 'easyavr_pg003_%s_dst' % suffix
def with_db(name):
    if any(k == 'dbname' for k, _ in q):
        nq = urlencode([(k, name if k == 'dbname' else v) for k, v in q])
        path = p.path
    else:
        nq, path = p.query, '/' + name
    return urlunsplit((p.scheme, p.netloc, path, nq, p.fragment))
for line in (db, user, password, host, str(port), red, src, dst, with_db(src), with_db(dst)):
    print(line)
PY
); then
  fail "无法解析 PG_TEST_DSN（DSN 本身不记录）"
  exit 1
fi
mapfile -t M < <(printf '%s\n' "$META")
if ((${#M[@]} != 10)); then
  fail "PG_TEST_DSN 解析结果异常"
  exit 1
fi
M_DB=${M[0]}; M_USER=${M[1]}; M_PASS=${M[2]}; M_HOST=${M[3]}; M_PORT=${M[4]}
REDACTED_DSN=${M[5]}; SRC_DB=${M[6]}; DST_DB=${M[7]}; SRC_DSN=${M[8]}; DST_DSN=${M[9]}
log "PG_TEST_DSN (redacted) = $REDACTED_DSN"
if [[ $M_DB == easyavr || $M_DB != postgres ]]; then
  fail "维护库必须是 postgres；拒绝业务库/生产样式库名（实际库名不写入日志）"
  exit 1
fi
if [[ -z $M_USER || -z $M_HOST ]]; then
  fail "PG_TEST_DSN 缺少 user/host"
  exit 1
fi
for db in "$SRC_DB" "$DST_DB"; do
  if [[ ! $db =~ ^easyavr_pg003_[0-9a-f]{8}_(src|dst)$ ]]; then
    fail "测试库名不符合白名单 easyavr_pg003_*"
    exit 1
  fi
done
pass "库名白名单通过（源/目标均为脚本自建的 easyavr_pg003_*）"
if ! psql "$PG_TEST_DSN" -v ON_ERROR_STOP=1 -Atq -c 'SELECT 1' 2>/dev/null | grep -qx 1; then
  fail "维护库连接/只读探测失败"
  exit 1
fi
pass "维护库连通（SELECT 1）"
if [[ $(psql "$PG_TEST_DSN" -v ON_ERROR_STOP=1 -Atq -c "SELECT has_database_privilege(current_user, 'postgres', 'CREATE')" 2>/dev/null) != t ]]; then
  fail "维护账号无 CREATE DATABASE 权限，无法建隔离库"
  exit 1
fi
pass "维护账号具备建库权限"

heading "创建隔离源库/目标库"
for db in "$SRC_DB" "$DST_DB"; do
  if psql "$PG_TEST_DSN" -v ON_ERROR_STOP=1 -q -c "CREATE DATABASE \"$db\"" >"$TMP/create-$db.log" 2>&1; then
    DBS_CREATED+=("$db")
    pass "创建一次性库 ${db}"
  else
    fail "创建一次性库失败: ${db}（错误不回显 DSN）"
    exit 1
  fi
done

heading "编译平台（用于 AutoMigrate 与凭据 hook）"
if (cd "$SERVER_DIR" && go build -o "$TMP/easyavr" ./cmd/easyavr) >"$TMP/build.log" 2>&1; then
  pass "go build ./cmd/easyavr"
else
  fail "go build ./cmd/easyavr 失败"
  exit 1
fi

# 所有秘密只存在于 shell 变量；任何日志都不打印它们。
KEY_V1=$(openssl rand -base64 32)
KEY_V2=$(openssl rand -base64 32)
KEY_BAD=$(openssl rand -base64 32)
JWT_SECRET=$(openssl rand -base64 32)
ADMIN_USER=easyavr_probe
ADMIN_PASS=$(openssl rand -base64 18)
VIEWER_PASS=$(openssl rand -base64 18)
HEX1=$(python3 -c 'import secrets;print(secrets.token_hex(4))')
HEX2=$(python3 -c 'import secrets;print(secrets.token_hex(4))')
DEV_NAME="PG003-DEV-$HEX1"
DEV_PASS="PG003DevPass-$HEX1"
AI_NAME="PG003-AI-$HEX2"
AI_KEY="PG003ApiKey-$HEX2"
VIEWER_NAME="pg003_viewer_$HEX2"

psql_q() { psql "$1" -v ON_ERROR_STOP=1 -Atq -c "$2"; }
src_q() { psql_q "$SRC_DSN" "$1"; }
dst_q() { psql_q "$DST_DSN" "$1"; }

start_platform() {
  # start_platform <name> <dsn> <key> <kid> <previous> <port> <logfile>
  local name=$1 dsn=$2 key=$3 kid=$4 prev=$5 port=$6 logfile=$7
  mkdir -p "$TMP/snapshots-$name"
  EASYAVR_ENV=production \
  EASYAVR_LISTEN="127.0.0.1:$port" \
  EASYAVR_DB_DRIVER=postgres EASYAVR_DB_DSN="$dsn" \
  EASYAVR_CREDENTIAL_KEY="$key" EASYAVR_CREDENTIAL_KEY_ID="$kid" \
  EASYAVR_CREDENTIAL_KEY_PREVIOUS="$prev" \
  EASYAVR_JWT_SECRET="$JWT_SECRET" EASYAVR_ADMIN_USER="$ADMIN_USER" EASYAVR_ADMIN_PASS="$ADMIN_PASS" \
  EASYAVR_ALLOW_ORIGIN="http://127.0.0.1:$port" \
  EASYAVR_SNAPSHOT_DIR="$TMP/snapshots-$name" \
  EASYAVR_ZLM_API=http://127.0.0.1:1 EASYAVR_MONITOR_SEC=0 EASYAVR_PGVECTOR=false \
    "$TMP/easyavr" >"$logfile" 2>&1 &
  LAST_PID=$!
  PIDS+=("$LAST_PID")
}

wait_ready() {
  # wait_ready <pid> <port> <logfile>
  local pid=$1 port=$2 logfile=$3 i
  for i in $(seq 1 160); do
    if ! kill -0 "$pid" 2>/dev/null; then return 1; fi
    if curl -fsS --max-time 1 "http://127.0.0.1:$port/api/v1/healthz" 2>/dev/null | grep -q '"code":0'; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

stop_platform() {
  local pid=$1 i
  kill "$pid" 2>/dev/null || true
  for i in $(seq 1 30); do
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.1
  done
  if kill -0 "$pid" 2>/dev/null; then kill -9 "$pid" 2>/dev/null || true; fi
  wait "$pid" 2>/dev/null || true
}

api_call() {
  # api_call <method> <path> <body|-> <token> <port>
  local method=$1 path=$2 body=$3 token=$4 port=$5
  if [[ $body == - ]]; then
    curl -fsS --max-time 10 -X "$method" "http://127.0.0.1:$port/api/v1$path" \
      -H 'Content-Type: application/json' -H "Authorization: Bearer $token"
  else
    curl -fsS --max-time 10 -X "$method" "http://127.0.0.1:$port/api/v1$path" \
      -H 'Content-Type: application/json' -H "Authorization: Bearer $token" -d @- <<<"$body"
  fi
}

json_id() { python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["id"])'; }
json_channel_id() { python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["channels"][0]["id"])'; }
json_token() { python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["token"])'; }
json_code() { python3 -c 'import json,sys; print(json.load(sys.stdin).get("code","-1"))'; }

heading "源库 AutoMigrate + 种子（平台自身建表）"
start_platform src "$SRC_DSN" "$KEY_V1" v1 '' 18009 "$TMP/src-server.log"
SRC_PID=$LAST_PID
if wait_ready "$SRC_PID" 18009 "$TMP/src-server.log"; then
  pass "源平台启动 /api/v1/healthz=code:0（生产模式 + PG 驱动）"
else
  fail "源平台未能启动（详见本次演练日志；日志不记录密钥/DSN）"
  exit 1
fi
TABLE_COUNT=$(src_q "SELECT count(*) FROM pg_tables WHERE schemaname='public'")
if ((TABLE_COUNT >= 20)); then pass "AutoMigrate 建表 $TABLE_COUNT 张"; else fail "AutoMigrate 建表异常: $TABLE_COUNT"; fi

heading "通过平台 API/hook 写入敏感与非敏感测试数据"
TOKEN=$(curl -fsS --max-time 10 -X POST http://127.0.0.1:18009/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d @- <<<"{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}" | json_token) || TOKEN=''
if [[ -n $TOKEN ]]; then pass "管理员登录并取得 JWT（token 不写日志）"; else fail "管理员登录失败"; exit 1; fi

if R=$(api_call POST /users "{\"username\":\"$VIEWER_NAME\",\"password\":\"$VIEWER_PASS\",\"role\":\"viewer\"}" "$TOKEN" 18009); then
  [[ $(printf '%s' "$R" | json_code) == 0 ]] && pass "创建 viewer 用户（用户表覆盖）" || fail "创建用户返回非 0"
else fail "创建用户请求失败"; fi

if R=$(api_call POST /devices "{\"name\":\"$DEV_NAME\",\"protocol\":\"rtsp\",\"ip\":\"127.0.0.1\",\"port\":554,\"username\":\"cam\",\"password\":\"$DEV_PASS\"}" "$TOKEN" 18009); then
  DEV_ID=$(printf '%s' "$R" | json_id)
  CH_ID=$(printf '%s' "$R" | json_channel_id)
  [[ -n $DEV_ID && -n $CH_ID ]] && pass "创建设备（密码经 GORM hook 加密）+ 自动主通道" || fail "设备响应缺少 id/channel"
else fail "创建设备请求失败"; fi

if R=$(api_call POST "/devices/$DEV_ID/channels" '{"name":"PG003-sub","streamType":"sub","sourceUrl":"rtsp://127.0.0.1/low"}' "$TOKEN" 18009); then
  [[ $(printf '%s' "$R" | json_code) == 0 ]] && pass "创建子通道（通道关联覆盖）" || fail "创建通道返回非 0"
else fail "创建通道请求失败"; fi

if R=$(api_call POST /ai/providers "{\"name\":\"$AI_NAME\",\"kind\":\"cv\",\"vendor\":\"probe\",\"endpoint\":\"http://127.0.0.1:1/detect\",\"apiKey\":\"$AI_KEY\",\"model\":\"probe-1\",\"enabled\":true}" "$TOKEN" 18009); then
  PROV_ID=$(printf '%s' "$R" | json_id)
  [[ -n $PROV_ID ]] && pass "创建 AIProvider（APIKey 经 GORM hook 加密）" || fail "Provider 响应缺少 id"
else fail "创建 AIProvider 请求失败"; fi

if R=$(api_call POST /ai/tasks "{\"name\":\"PG003-task\",\"channelId\":$CH_ID,\"providerId\":$PROV_ID,\"taskType\":\"cv_detect\",\"enabled\":false}" "$TOKEN" 18009); then
  TASK_ID=$(printf '%s' "$R" | json_id)
  [[ -n $TASK_ID ]] && pass "创建 AITask（关联通道/Provider）" || fail "任务响应缺少 id"
else fail "创建 AITask 请求失败"; fi

if [[ -n ${DEV_ID:-} && -n ${CH_ID:-} && -n ${PROV_ID:-} && -n ${TASK_ID:-} ]]; then
  if src_q "INSERT INTO ai_task_runs (created_at,updated_at,task_id,status,retry_count,worker_id,heartbeat_at) VALUES (now(),now(),$TASK_ID,'succeeded',0,'verify-tech003',now())" >/dev/null 2>&1; then
    pass "插入 AITaskRun（无密文字段，直接 SQL）"
  else fail "插入 AITaskRun 失败"; fi
  if src_q "INSERT INTO snapshots (created_at,updated_at,channel_id,path,url,size_bytes,taken_at) VALUES (now(),now(),$CH_ID,'$TMP/no-file.jpg','http://127.0.0.1/snap.jpg',1024,now())" >/dev/null 2>&1; then
    pass "插入快照记录"
  else fail "插入快照记录失败"; fi
  if src_q "INSERT INTO recordings (created_at,updated_at,channel_id,stream_key,date,file,url,size_bytes,duration,start_time) SELECT now(),now(),id,stream_key,'2026-11-25','2026-11-25-00-00-00-0.mp4','http://127.0.0.1/record.mp4',2048,60,now() FROM channels WHERE id=$CH_ID" >/dev/null 2>&1; then
    pass "插入录像记录（取自通道 stream_key）"
  else fail "插入录像记录失败"; fi
else
  fail "关键 ID 缺失，跳过剩余测试数据插入（后续校验会 FAIL）"
fi

AUDIT_ROWS=$(src_q "SELECT count(*) FROM audit_logs")
SETTING_ROWS=$(src_q "SELECT count(*) FROM platform_settings")
((AUDIT_ROWS > 0)) && pass "审计日志由 API 中间件写入: $AUDIT_ROWS 行" || fail "审计日志为空"
((SETTING_ROWS > 0)) && pass "平台配置由种子写入: $SETTING_ROWS 行" || fail "平台配置为空"

CIPHER_DEV=$(src_q "SELECT count(*) FROM devices WHERE password LIKE 'enc:v1:%'")
CIPHER_AI=$(src_q "SELECT count(*) FROM a_iproviders WHERE api_key LIKE 'enc:v1:%'")
((CIPHER_DEV == 1)) && pass "设备密码为 enc:v1: 密文（非明文）" || fail "设备密码未加密: $CIPHER_DEV"
((CIPHER_AI == 1)) && pass "AIProvider APIKey 为 enc:v1: 密文（非明文）" || fail "APIKey 未加密: $CIPHER_AI"
if [[ $(src_q "SELECT count(*) FROM devices WHERE password='$DEV_PASS'") == 0 && $(src_q "SELECT count(*) FROM a_iproviders WHERE api_key='$AI_KEY'") == 0 ]]; then
  pass "库中不存在测试密码/APIKey 明文"
else fail "库中存在测试凭据明文"; fi

# 停掉源平台：备份针对静态数据，避免演练中继续写入。
stop_platform "$SRC_PID"
pass "源平台已停止（备份期间无写入）"

heading "备份：pg_backup.sh"
if PG_DSN="$SRC_DSN" PG_BACKUP_DIR="$TMP/backups" PG_KEEP=2 \
    bash "$SCRIPT_DIR/pg_backup.sh" >"$TMP/backup.log" 2>&1; then
  pass "pg_backup.sh 退出码 0"
else
  fail "pg_backup.sh 退出码非 0"
fi
BACKUP=$(find "$TMP/backups" -maxdepth 1 -type f -name 'easyavr-pg-*.dump' -print -quit 2>/dev/null || true)
if [[ -n $BACKUP && -s $BACKUP ]]; then pass "备份文件非空: $(basename -- "$BACKUP")"; else fail "备份文件缺失/为空"; fi
if [[ -n $BACKUP ]]; then
  if python3 - "$BACKUP" <<'PY'
import sys
with open(sys.argv[1], 'rb') as f:
    sys.exit(0 if f.read(5) == b'PGDMP' else 1)
PY
  then pass "备份文件魔数为 PG custom format (PGDMP)"; else fail "备份文件不是 PG custom format"; fi
  if pg_restore -l "$BACKUP" >"$TMP/backup-list.txt" 2>/dev/null && [[ -s $TMP/backup-list.txt ]]; then
    pass "pg_restore -l 可读目录清单 ($(wc -l <"$TMP/backup-list.txt") 行)"
  else fail "pg_restore -l 无法读取备份"; fi
  MODE=$(stat -c '%a' -- "$BACKUP")
  [[ $MODE == 600 ]] && pass "备份文件权限 0600" || fail "备份文件权限异常: $MODE"
else
  skip "备份格式/目录/权限检查（无备份文件）"
fi

heading "恢复 dry-run：pg_restore.sh（确认无写入）"
BEFORE_TABLES=$(dst_q "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'")
if bash "$SCRIPT_DIR/pg_restore.sh" -f "$BACKUP" -d "$DST_DSN" --dry-run >"$TMP/restore-dry.log" 2>&1; then
  pass "pg_restore.sh --dry-run 退出码 0"
else fail "pg_restore.sh --dry-run 退出码非 0"; fi
AFTER_TABLES=$(dst_q "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'")
if [[ $BEFORE_TABLES == 0 && $AFTER_TABLES == 0 ]]; then
  pass "dry-run 未写入目标库（public 表数 0 → 0）"
else fail "dry-run 疑似写入目标库: $BEFORE_TABLES → $AFTER_TABLES"; fi

heading "恢复 --yes：pg_restore.sh"
if bash "$SCRIPT_DIR/pg_restore.sh" -f "$BACKUP" -d "$DST_DSN" --yes >"$TMP/restore-yes.log" 2>&1; then
  pass "pg_restore.sh --yes 退出码 0（含已知版本偏差告警时脚本自行放行）"
else fail "pg_restore.sh --yes 退出码非 0"; fi

heading "恢复后结构/行数/关联/索引校验"
src_q "SELECT table_name||'|'||column_name||'|'||data_type FROM information_schema.columns WHERE table_schema='public' ORDER BY table_name, column_name" >"$TMP/src.schema"
dst_q "SELECT table_name||'|'||column_name||'|'||data_type FROM information_schema.columns WHERE table_schema='public' ORDER BY table_name, column_name" >"$TMP/dst.schema"
if [[ -s $TMP/src.schema && -s $TMP/dst.schema ]] && diff -q "$TMP/src.schema" "$TMP/dst.schema" >/dev/null; then
  pass "表/列/类型完全一致 ($(wc -l <"$TMP/dst.schema") 列)"
else fail "表结构不一致或目录查询为空"; fi
src_q "SELECT tablename||'|'||indexname||'|'||indexdef FROM pg_indexes WHERE schemaname='public' ORDER BY tablename, indexname" >"$TMP/src.idx"
dst_q "SELECT tablename||'|'||indexname||'|'||indexdef FROM pg_indexes WHERE schemaname='public' ORDER BY tablename, indexname" >"$TMP/dst.idx"
if [[ -s $TMP/src.idx && -s $TMP/dst.idx ]] && diff -q "$TMP/src.idx" "$TMP/dst.idx" >/dev/null; then
  pass "索引定义完全一致 ($(wc -l <"$TMP/dst.idx") 个)"
else fail "索引定义不一致或目录查询为空"; fi
src_q "SELECT conname||'|'||contype::text||'|'||pg_get_constraintdef(oid) FROM pg_constraint WHERE connamespace='public'::regnamespace ORDER BY conname" >"$TMP/src.con"
dst_q "SELECT conname||'|'||contype::text||'|'||pg_get_constraintdef(oid) FROM pg_constraint WHERE connamespace='public'::regnamespace ORDER BY conname" >"$TMP/dst.con"
if [[ -s $TMP/src.con && -s $TMP/dst.con ]] && diff -q "$TMP/src.con" "$TMP/dst.con" >/dev/null; then
  pass "约束定义完全一致 ($(wc -l <"$TMP/dst.con") 个)"
else fail "约束定义不一致或目录查询为空"; fi
for spec in 'channels:stream_key' 'users:username' 'platform_settings:key'; do
  table=${spec%%:*}; col=${spec##*:}
  if [[ $(dst_q "SELECT count(*) FROM pg_indexes WHERE schemaname='public' AND tablename='$table' AND indexdef ILIKE '%UNIQUE%' AND indexdef ILIKE '%$col%'") -ge 1 ]]; then
    pass "唯一索引存在: $table($col)"
  else fail "唯一索引缺失: $table($col)"; fi
done
TABLES='users devices channels a_iproviders ai_tasks ai_task_runs snapshots recordings audit_logs platform_settings'
for t in $TABLES; do
  S=$(src_q "SELECT count(*) FROM $t")
  D=$(dst_q "SELECT count(*) FROM $t")
  if [[ -n $S && $S == "$D" ]]; then pass "行数一致 $t=$D"; else fail "行数不一致 $t: src=$S dst=$D"; fi
done
if [[ $(dst_q "SELECT count(*) FROM channels c JOIN devices d ON d.id=c.device_id WHERE d.name='$DEV_NAME'") == 2 ]]; then
  pass "关联: 设备→2 个通道"
else fail "关联异常: 设备→通道"; fi
if [[ $(dst_q "SELECT count(*) FROM ai_tasks t JOIN channels c ON c.id=t.channel_id JOIN a_iproviders p ON p.id=t.provider_id WHERE t.id=$TASK_ID") == 1 ]]; then
  pass "关联: 任务→通道→Provider"
else fail "关联异常: 任务→通道/Provider"; fi
if [[ $(dst_q "SELECT count(*) FROM snapshots s JOIN channels c ON c.id=s.channel_id WHERE s.channel_id=$CH_ID") == 1 ]] \
   && [[ $(dst_q "SELECT count(*) FROM recordings r JOIN channels c ON c.id=r.channel_id WHERE r.channel_id=$CH_ID") == 1 ]] \
   && [[ $(dst_q "SELECT count(*) FROM ai_task_runs r JOIN ai_tasks t ON t.id=r.task_id WHERE r.task_id=$TASK_ID") == 1 ]]; then
  pass "关联: 快照/录像/运行记录均挂在正确外键上"
else fail "关联异常: 快照/录像/运行记录"; fi
if [[ $(dst_q "SELECT value FROM platform_settings WHERE key='gb_enabled' LIMIT 1") == false ]]; then
  pass "平台配置字段值保留（gb_enabled=false）"
else fail "平台配置字段值异常"; fi
if [[ $(dst_q "SELECT count(*) FROM a_iproviders WHERE api_key LIKE 'enc:v1:%'") == 1 ]] \
   && [[ $(dst_q "SELECT count(*) FROM devices WHERE password LIKE 'enc:v1:%'") == 1 ]]; then
  pass "恢复后密文仍为 enc:v1:，未退化为明文"
else fail "恢复后密文前缀异常"; fi

heading "密钥验证：v1 可解密、错误密钥 fail-closed、v1→v2 轮换"
# (1) 用生成时的 v1 密钥启动恢复库：migrateCredentials 会解密全部凭据列；失败即启动失败。
start_platform rot "$DST_DSN" "$KEY_V1" v1 '' 18010 "$TMP/v1-server.log"
ROT_PID=$LAST_PID
if wait_ready "$ROT_PID" 18010 "$TMP/v1-server.log"; then
  pass "v1 主密钥启动成功（加密列解密校验通过）"
else fail "v1 主密钥启动失败"; fi
ROT_TOKEN=$(curl -fsS --max-time 10 -X POST http://127.0.0.1:18010/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d @- <<<"{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}" | json_token) || ROT_TOKEN=''
CIPHER_BEFORE=$(dst_q "SELECT api_key FROM a_iproviders WHERE id=$PROV_ID")
if [[ -n $ROT_TOKEN ]] && api_call PUT "/ai/providers/$PROV_ID" "{\"name\":\"$AI_NAME-rot\",\"apiKey\":\"\",\"clearApiKey\":false,\"enabled\":true}" "$ROT_TOKEN" 18010 >/dev/null 2>&1; then
  CIPHER_AFTER=$(dst_q "SELECT api_key FROM a_iproviders WHERE id=$PROV_ID")
  if [[ $CIPHER_BEFORE != "$CIPHER_AFTER" && $CIPHER_AFTER == enc:v1:* ]]; then
    pass "v1 解密→重加密往返成功（密文 nonce 变化且仍 enc:v1:）"
  else fail "v1 解密往返未生效"; fi
else fail "v1 下 Provider 更新请求失败"; fi
stop_platform "$ROT_PID"

# (2) 错误主密钥：必须在读取密文时 fail-closed，不能回退明文、不能启动。
start_platform bad "$DST_DSN" "$KEY_BAD" badkid '' 18010 "$TMP/badkey-server.log"
BAD_PID=$LAST_PID
BAD_EXIT=1
for i in $(seq 1 60); do
  if ! kill -0 "$BAD_PID" 2>/dev/null; then
    wait "$BAD_PID" 2>/dev/null; BAD_EXIT=$?; break
  fi
  sleep 0.2
done
if kill -0 "$BAD_PID" 2>/dev/null; then
  fail "错误主密钥下平台仍在运行（未 fail-closed）"
  stop_platform "$BAD_PID"
elif ((BAD_EXIT != 0)) && grep -Eqi 'decrypt|credential|open database' "$TMP/badkey-server.log"; then
  pass "错误主密钥下平台退出码 $BAD_EXIT，报解密/打开数据库错误（fail-closed）"
else
  fail "错误主密钥退出行为异常（exit=$BAD_EXIT 或日志无解密错误）"
fi
if [[ $(dst_q "SELECT count(*) FROM a_iproviders WHERE api_key LIKE 'enc:v1:%'") == 1 ]]; then
  pass "错误密钥尝试未改写/清空原有密文"
else fail "错误密钥尝试影响了库内密文"; fi

# (3) v2 主密钥 + PREVIOUS=v1:<旧密钥>：启动时自动用 v1 解密、用 v2 重加密。
start_platform rotate "$DST_DSN" "$KEY_V2" v2 "v1:$KEY_V1" 18010 "$TMP/v2-server.log"
V2_PID=$LAST_PID
if wait_ready "$V2_PID" 18010 "$TMP/v2-server.log"; then
  pass "v2 + PREVIOUS=v1 启动成功（旧密文可读）"
else fail "v2 + PREVIOUS=v1 启动失败"; fi
V2_ROWS=$(( $(dst_q "SELECT count(*) FROM devices WHERE password LIKE 'enc:v2:%'") + $(dst_q "SELECT count(*) FROM a_iproviders WHERE api_key LIKE 'enc:v2:%'") ))
if ((V2_ROWS == 2)); then pass "旧密文已重新加密为 enc:v2:（设备+Provider 共 2 行）"; else fail "轮换后 v2 密文行数异常: $V2_ROWS"; fi
V2_TOKEN=$(curl -fsS --max-time 10 -X POST http://127.0.0.1:18010/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d @- <<<"{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}" | json_token) || V2_TOKEN=''
if [[ -n $V2_TOKEN ]] && api_call PUT "/ai/providers/$PROV_ID" "{\"name\":\"$AI_NAME-v2\",\"apiKey\":\"\",\"clearApiKey\":false,\"enabled\":true}" "$V2_TOKEN" 18010 >/dev/null 2>&1; then
  if [[ $(dst_q "SELECT api_key FROM a_iproviders WHERE id=$PROV_ID") == enc:v2:* ]]; then
    pass "v2 主密钥下解密→重加密往返成功"
  else fail "v2 往返后密文前缀异常"; fi
else fail "v2 下 Provider 更新请求失败"; fi
stop_platform "$V2_PID"

heading "日志脱敏审计（不把秘密写进日志/报告）"
LEAK=0
LEAK_WHERE=''
# 每项格式: <标签>|<字面值>；标签本身不含秘密，失败时只报标签与日志文件名。
SECRETS=(
  "完整DSN|$PG_TEST_DSN"
  "DSN口令|:$M_PASS@"
  "v1主密钥|$KEY_V1"
  "v2主密钥|$KEY_V2"
  "错误主密钥|$KEY_BAD"
  "JWT密钥|$JWT_SECRET"
  "管理员口令|$ADMIN_PASS"
  "viewer口令|$VIEWER_PASS"
  "设备密码|$DEV_PASS"
  "AIKey|$AI_KEY"
)
for f in "$TMP"/*.log; do
  [[ -f $f ]] || continue
  for item in "${SECRETS[@]}"; do
    label=${item%%|*}
    pat=${item#*|}
    if grep -Fq -- "$pat" "$f"; then
      LEAK=1
      LEAK_WHERE="$LEAK_WHERE $(basename -- "$f"):$label"
    fi
  done
done
if ((LEAK == 0)); then
  pass "所有演练日志均无 DSN/口令/主密钥/测试凭据"
else
  fail "演练日志中发现秘密字面值（仅列标签）:$LEAK_WHERE"
fi

heading "收尾"
log "演练完成；trap 将停止残留进程、删除临时目录并 DROP 一次性库。"
exit 0
