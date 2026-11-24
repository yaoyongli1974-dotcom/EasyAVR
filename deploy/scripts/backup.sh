#!/usr/bin/env bash
# EasyAVR 热备份：SQLite 一致性快照 + 非敏感配置白名单快照。
#
# 三层分离（EASYAVR-TECH-002 / TASK-002）：
#   1) DB 备份   默认 /var/backups/easyavr/db      仅 SQLite 库（*.db.gz）
#   2) 配置备份  默认 /var/backups/easyavr/config  仅白名单非敏感键（config-*.gz）
#   3) 主密钥    EASYAVR_CREDENTIAL_KEY 由人工或外部 KMS 独立备份，
#                绝不进入以上任何目录，也不进入任何打包产物。
# 数据库里存的是 AES-256-GCM 密文：库 + 密钥同处泄露等于全部凭据明文泄露；
# 库泄露而密钥在别处则密文不可解。密钥丢失后加密凭据不可恢复。
#
# 生产机没有 sqlite3 命令，SQLite 又开着 WAL，所以这里用 python3 的
# sqlite3.Connection.backup() 做在线备份；不要改成 `sqlite3 .backup` 或 cp，
# 前者在目标机上不存在，后者在 WAL 下拿到的是不一致快照。
#
# 配置备份使用显式白名单 CONFIG_KEYS：只写列在里面的非敏感键。任何
# *_KEY / *_SECRET / *_PASSWORD / *_TOKEN 变量以及未列入白名单的未知变量
# 一律不写入，即使它们出现在 /etc/easyavr/env 里；生成器还会拒绝把任何
# 秘密变量的取值混入配置产物。
#
# 校验：DB 用 gzip -t + 解压后的 PRAGMA integrity_check；配置用 gzip -t
# 与生成时的秘密值检查。失败保留 .part 并报错退出，绝不发布坏备份。
#
# 可配置项（环境变量，均可选，默认值即生产机实际路径）：
#   DB                 SQLite 数据库路径，默认 /opt/easyavr/server/data/easyavr.db
#   BACKUP_DIR         备份根目录，默认 /var/backups/easyavr
#                      （DB 落在 $BACKUP_DIR/db，配置落在 $BACKUP_DIR/config）
#   DB_BACKUP_DIR      直接指定 DB 输出目录，覆盖 $BACKUP_DIR/db
#   CONFIG_BACKUP_DIR  直接指定配置输出目录，覆盖 $BACKUP_DIR/config
#   KEEP               每类备份保留份数，默认 7
#   ENV_FILE           环境变量文件，默认 /etc/easyavr/env
#
# 示例：DB=/tmp/test.db BACKUP_DIR=/tmp/bk KEEP=2 ./backup.sh

set -euo pipefail
umask 077

DB=${DB:-/opt/easyavr/server/data/easyavr.db}
BACKUP_ROOT=${BACKUP_DIR:-/var/backups/easyavr}
DB_BACKUP_DIR=${DB_BACKUP_DIR:-$BACKUP_ROOT/db}
CONFIG_BACKUP_DIR=${CONFIG_BACKUP_DIR:-$BACKUP_ROOT/config}
KEEP=${KEEP:-7}
ENV_FILE=${ENV_FILE:-/etc/easyavr/env}
KEEP_PARTS=0

# 显式白名单：只有这里列出的键才会进入配置备份；秘密类键即使被误加进来，
# 下面的生成器也会拒绝执行（防御纵深）。注意不要加入 *_TOKEN 之类的键。
CONFIG_KEYS=(
  EASYAVR_ENV
  EASYAVR_LISTEN
  EASYAVR_DB_DRIVER
  EASYAVR_PGVECTOR
  EASYAVR_JWT_EXPIRE_HOURS
  EASYAVR_ALLOW_ORIGIN
  EASYAVR_WEB_DIST
  EASYAVR_SNAPSHOT_DIR
  EASYAVR_MONITOR_SEC
  EASYAVR_ZLM_API
  EASYAVR_MEDIA_HOST
  EASYAVR_MEDIA_SCHEME
  EASYAVR_ZLM_HTTP_PORT
  EASYAVR_ZLM_RTSP_PORT
  EASYAVR_ZLM_RTMP_PORT
  EASYAVR_PLAY_WHITELIST
  EASYAVR_PLAY_AUTH
  EASYAVR_GB_ENABLED
  EASYAVR_GB_LISTEN
  EASYAVR_GB_ID
  EASYAVR_GB_REALM
  EASYAVR_GB_RTP_IP
  EASYAVR_GB_RTP_PORT_LOW
  EASYAVR_GB_RTP_PORT_HIGH
  EASYAVR_GA1400_ENABLED
  EASYAVR_EHOME_ENABLED
  EASYAVR_GB35114_ENABLED
  EASYAVR_GB35114_CERT_DIR
  EASYAVR_CLUSTER_ENABLED
)

log() { printf '%s %s\n' "$(date '+%F %T')" "$*"; }
die() { KEEP_PARTS=1; log "FAILED: $*"; exit 1; }

# 打印 integrity_check 全部结果行；只有唯一一行 "ok" 才算通过。
integrity_check() {
  python3 - "$1" <<'PY'
import sqlite3
import sys

con = sqlite3.connect(sys.argv[1])
try:
    rows = con.execute("PRAGMA integrity_check").fetchall()
finally:
    con.close()
text = "\n".join(str(row[0]) for row in rows)
print(text)
sys.exit(0 if text.strip().lower() == "ok" else 1)
PY
}

if ! [[ $KEEP =~ ^[1-9][0-9]*$ ]]; then
  die "KEEP must be a positive integer, got: $KEEP"
fi

mkdir -p -- "$DB_BACKUP_DIR" || die "cannot create db backup dir: $DB_BACKUP_DIR"
chmod 0700 -- "$DB_BACKUP_DIR"
mkdir -p -- "$CONFIG_BACKUP_DIR" || die "cannot create config backup dir: $CONFIG_BACKUP_DIR"
chmod 0700 -- "$CONFIG_BACKUP_DIR"

# 串行化：锁放在 DB 目录，flock 随 fd 9 存活到脚本结束。
exec 9>"$DB_BACKUP_DIR/.backup.lock"
if ! flock -n 9; then
  log "another backup is already running, skip this round"
  exit 1
fi

TS=$(date '+%Y%m%d-%H%M%S')
PART_DB="$DB_BACKUP_DIR/.easyavr-$TS.db.part"
PART_CFG="$CONFIG_BACKUP_DIR/.config-$TS.gz.part"
VERIFY_DB="$DB_BACKUP_DIR/.verify-$TS.db"
CFG_PLAIN="$CONFIG_BACKUP_DIR/.config-plain-$TS"

cleanup() {
  rm -f -- "$VERIFY_DB" "$CFG_PLAIN"
  if (( KEEP_PARTS == 1 )); then
    local -a kept=()
    local f
    for f in "${PART_DB:-}" "${PART_DB:-}.gz" "${PART_CFG:-}" "${PART_CFG:-}.gz"; do
      if [[ -n $f && -f $f ]]; then
        kept+=("$f")
      fi
    done
    if (( ${#kept[@]} > 0 )); then
      log "kept partial files for inspection: ${kept[*]}"
    fi
  else
    rm -f -- "${PART_DB:-}" "${PART_DB:-}.gz" "${PART_CFG:-}" "${PART_CFG:-}.gz"
  fi
}
trap cleanup EXIT
trap 'KEEP_PARTS=1; log "FAILED at line $LINENO: $BASH_COMMAND"' ERR

# ---------------------------------------------------------------------------
# 1) DB：SQLite 一致性快照（与主密钥的物理位置无关）
# ---------------------------------------------------------------------------
DB_OUT=''
if [[ ! -f $DB ]]; then
  log "database not found: $DB (db backup skipped)"
else
  log "backing up $DB -> $DB_BACKUP_DIR"

  # backup API 走 SQLite 自身的页拷贝，运行中的库也能得到一致快照。
  python3 - "$DB" "$PART_DB" <<'PY'
import sqlite3
import sys

src_path, dst_path = sys.argv[1], sys.argv[2]
src = sqlite3.connect(src_path)
dst = sqlite3.connect(dst_path)
try:
    src.backup(dst)
    dst.commit()
finally:
    dst.close()
    src.close()
PY

  [[ -s $PART_DB ]] || die "sqlite backup produced an empty file: $PART_DB"
  # 目标库用回滚日志（无 -wal），恢复时不用连带处理 WAL/SHM。
  python3 - "$PART_DB" <<'PY'
import sqlite3
import sys
con = sqlite3.connect(sys.argv[1])
try:
    con.execute("PRAGMA journal_mode=DELETE")
    con.commit()
finally:
    con.close()
PY

  gzip -9 -- "$PART_DB" || die "gzip failed for database backup"
  chmod 0600 -- "$PART_DB.gz"

  log "verify: gzip -t $PART_DB.gz"
  gzip -t -- "$PART_DB.gz" || die "gzip -t failed for database backup"
  log "verify: gzip -t ok"

  gunzip -c -- "$PART_DB.gz" >"$VERIFY_DB" || die "cannot decompress database backup for verification"
  CHECK=''
  if CHECK=$(integrity_check "$VERIFY_DB"); then
    log "verify: PRAGMA integrity_check=$CHECK"
  else
    die "PRAGMA integrity_check failed: $CHECK"
  fi
  rm -f -- "$VERIFY_DB"

  DB_OUT="$DB_BACKUP_DIR/easyavr-$TS.db.gz"
  mv -f -- "$PART_DB.gz" "$DB_OUT"
  chmod 0600 -- "$DB_OUT"
  [[ -s $DB_OUT ]] || die "database backup is empty after gzip: $DB_OUT"
  log "database backup: $DB_OUT ($(stat -c '%s' -- "$DB_OUT") bytes, verified gzip+integrity)"
fi

# ---------------------------------------------------------------------------
# 2) 配置：显式白名单，绝不打包整个 env（主密钥/口令/secret 都不会出现）
# ---------------------------------------------------------------------------
if [[ -f $ENV_FILE ]]; then
  log "config backup: $ENV_FILE -> $CONFIG_BACKUP_DIR (whitelist only, no secrets)"
  python3 - "$ENV_FILE" "$CFG_PLAIN" "${CONFIG_KEYS[@]}" <<'PY'
import re
import sys

env_path, out_path = sys.argv[1], sys.argv[2]
whitelist = set(sys.argv[3:])

def is_secret(name):
    n = name.upper()
    # 宽松匹配优先：包含 KEY / SECRET / PASSWORD / TOKEN / CREDENTIAL / PRIVATE
    # 任一片段就拒绝，覆盖 *_KEY / *_SECRET / *_PASSWORD / *_TOKEN 以及
    # EASYAVR_CREDENTIAL_KEY_ID / _PREVIOUS 这类后缀变体和连写命名。
    return any(token in n for token in ("KEY", "SECRET", "PASSWORD", "PASSWD", "TOKEN", "CREDENTIAL", "PRIVATE"))

bad = sorted(k for k in whitelist if is_secret(k))
if bad:
    sys.stderr.write("refuse to back up sensitive whitelist keys: " + ",".join(bad) + "\n")
    sys.exit(2)

line_re = re.compile(r"^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=(.*)$")
kept = []
secret_values = []
seen = set()
with open(env_path, "r", encoding="utf-8", errors="replace") as fh:
    for raw in fh:
        line = raw.rstrip("\n").rstrip("\r")
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        m = line_re.match(line)
        if not m:
            continue
        name, value = m.group(1), m.group(2)
        if is_secret(name):
            v = value.strip()
            # 短值（如 changeme）容易和正常 URL 子串偶合，只检查有辨识度的长度。
            if len(v) >= 8:
                secret_values.append(v)
            continue
        if name not in whitelist or name in seen:
            continue
        seen.add(name)
        kept.append(name + "=" + value + "\n")

text = "".join(kept)
# 最后一道闸门：任何秘密取值出现在产物里就拒绝发布（错误信息不含取值）。
if any(v and v in text for v in secret_values):
    sys.stderr.write("refuse to publish config backup: a secret value leaked into whitelisted output\n")
    sys.exit(3)

with open(out_path, "w", encoding="utf-8") as fh:
    fh.write(text)
PY

  if [[ ! -s $CFG_PLAIN ]]; then
    log "no whitelisted keys found in $ENV_FILE (config backup skipped)"
  else
    gzip -9 -c -- "$CFG_PLAIN" >"$PART_CFG" || die "cannot gzip config backup"
    [[ -s $PART_CFG ]] || die "config backup is empty: $PART_CFG"
    gzip -t -- "$PART_CFG" || die "gzip -t failed for config backup"
    log "verify: config gzip -t ok"
    chmod 0600 -- "$PART_CFG"
    CFG_OUT="$CONFIG_BACKUP_DIR/config-$TS.gz"
    mv -f -- "$PART_CFG" "$CFG_OUT"
    chmod 0600 -- "$CFG_OUT"
    log "config backup: $CFG_OUT ($(stat -c '%s' -- "$CFG_OUT") bytes, whitelist-only, no secrets)"
  fi
else
  log "env file not found: $ENV_FILE (config backup skipped)"
fi

# ---------------------------------------------------------------------------
# 3) 轮转：只删除本流程自己生成的 DB / config 文件。
#    主密钥从不写入这两个目录，因此轮转不可能删到密钥。
# ---------------------------------------------------------------------------
prune() {
  local dir=$1 pattern=$2 keep=$3
  local -a files=()
  local excess f
  mapfile -t files < <(find "$dir" -maxdepth 1 -type f -name "$pattern" -printf '%f\n' | LC_ALL=C sort)
  excess=$(( ${#files[@]} - keep ))
  (( excess > 0 )) || return 0
  for f in "${files[@]:0:excess}"; do
    rm -f -- "$dir/$f"
    log "pruned old backup: $dir/$f"
  done
}

prune "$DB_BACKUP_DIR" 'easyavr-*.db.gz' "$KEEP"
prune "$CONFIG_BACKUP_DIR" 'config-*.gz' "$KEEP"

DB_COUNT=$(find "$DB_BACKUP_DIR" -maxdepth 1 -type f -name 'easyavr-*.db.gz' | wc -l)
CFG_COUNT=$(find "$CONFIG_BACKUP_DIR" -maxdepth 1 -type f -name 'config-*.gz' | wc -l)
log "done: db_dir=$DB_BACKUP_DIR config_dir=$CONFIG_BACKUP_DIR kept=db:$DB_COUNT config:$CFG_COUNT (limit $KEEP each); credential key is never stored here"
