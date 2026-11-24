#!/usr/bin/env bash
# EasyAVR 热备份：SQLite 库 + /etc/easyavr/env。
#
# 生产机没有 sqlite3 命令，SQLite 又开着 WAL，所以这里用 python3 的
# sqlite3.Connection.backup() 做在线备份；不要改成 `sqlite3 .backup` 或 cp，
# 前者在目标机上不存在，后者在 WAL 下拿到的是不一致快照。
#
# 备份完成后做两道验证：gzip -t 与解压后的 PRAGMA integrity_check。
# 验证失败时保留 .part 文件并报错退出，绝不把坏备份发布成正式文件。
#
# 可配置项（环境变量，均可选，默认值即生产机实际路径）：
#   DB          SQLite 数据库路径，默认 /opt/easyavr/server/data/easyavr.db
#   BACKUP_DIR  备份输出目录，默认 /var/backups/easyavr
#   KEEP        每类备份保留份数，默认 7
#   ENV_FILE    环境变量文件，默认 /etc/easyavr/env
#
# 示例：DB=/tmp/test.db BACKUP_DIR=/tmp/bk KEEP=2 ./backup.sh

set -euo pipefail
umask 077

DB=${DB:-/opt/easyavr/server/data/easyavr.db}
BACKUP_DIR=${BACKUP_DIR:-/var/backups/easyavr}
KEEP=${KEEP:-7}
ENV_FILE=${ENV_FILE:-/etc/easyavr/env}
KEEP_PARTS=0

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

mkdir -p -- "$BACKUP_DIR" || die "cannot create backup dir: $BACKUP_DIR"
chmod 0700 -- "$BACKUP_DIR"

# 串行化：锁文件放在备份目录里，flock 随 fd 9 存活到脚本结束。
exec 9>"$BACKUP_DIR/.backup.lock"
if ! flock -n 9; then
  log "another backup is already running, skip this round"
  exit 1
fi

TS=$(date '+%Y%m%d-%H%M%S')
PART_DB="$BACKUP_DIR/.easyavr-$TS.db.part"
PART_ENV="$BACKUP_DIR/.env-$TS.gz.part"
VERIFY_DB="$BACKUP_DIR/.verify-$TS.db"

cleanup() {
  if [[ -n ${VERIFY_DB:-} ]]; then
    rm -f -- "$VERIFY_DB"
  fi
  if (( KEEP_PARTS == 1 )); then
    local -a kept=()
    local f
    for f in "${PART_DB:-}" "${PART_DB:-}.gz" "${PART_ENV:-}"; do
      if [[ -n $f && -f $f ]]; then
        kept+=("$f")
      fi
    done
    if (( ${#kept[@]} > 0 )); then
      log "kept partial files for inspection: ${kept[*]}"
    fi
  else
    rm -f -- "${PART_DB:-}" "${PART_DB:-}.gz" "${PART_ENV:-}"
  fi
}
trap cleanup EXIT
trap 'KEEP_PARTS=1; log "FAILED at line $LINENO: $BASH_COMMAND"' ERR

if [[ ! -f $DB ]]; then
  log "database not found: $DB (nothing to back up)"
  exit 0
fi

log "backing up $DB -> $BACKUP_DIR"

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

# 校验先于发布：任何一步失败都保留 .part 并以非 0 退出，正式备份不出现。
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

DB_OUT="$BACKUP_DIR/easyavr-$TS.db.gz"
mv -f -- "$PART_DB.gz" "$DB_OUT"
[[ -s $DB_OUT ]] || die "database backup is empty after gzip: $DB_OUT"
log "database backup: $DB_OUT ($(stat -c '%s' -- "$DB_OUT") bytes, verified gzip+integrity)"

if [[ -f $ENV_FILE ]]; then
  gzip -9 -c -- "$ENV_FILE" >"$PART_ENV" || die "cannot gzip $ENV_FILE"
  [[ -s $PART_ENV ]] || die "env backup is empty: $PART_ENV"
  gzip -t -- "$PART_ENV" || die "gzip -t failed for env backup"
  chmod 0600 -- "$PART_ENV"
  ENV_OUT="$BACKUP_DIR/env-$TS.gz"
  mv -f -- "$PART_ENV" "$ENV_OUT"
  log "env backup: $ENV_OUT ($(stat -c '%s' -- "$ENV_OUT") bytes, verify: gzip -t ok)"
else
  log "env file not found: $ENV_FILE (skipped)"
fi

# 按文件名（含时间戳）字典序取尾部保留，删除更早的。
prune() {
  local pattern=$1 keep=$2
  local -a files=()
  local excess f
  mapfile -t files < <(find "$BACKUP_DIR" -maxdepth 1 -type f -name "$pattern" -printf '%f\n' | LC_ALL=C sort)
  excess=$(( ${#files[@]} - keep ))
  (( excess > 0 )) || return 0
  for f in "${files[@]:0:excess}"; do
    rm -f -- "$BACKUP_DIR/$f"
    log "pruned old backup: $f"
  done
}

prune 'easyavr-*.db.gz' "$KEEP"
prune 'env-*.gz' "$KEEP"

DB_COUNT=$(find "$BACKUP_DIR" -maxdepth 1 -type f -name 'easyavr-*.db.gz' | wc -l)
ENV_COUNT=$(find "$BACKUP_DIR" -maxdepth 1 -type f -name 'env-*.gz' | wc -l)
log "done: latest=$(basename -- "$DB_OUT") size=$(du -h -- "$DB_OUT" | cut -f1) kept=db:$DB_COUNT env:$ENV_COUNT (limit $KEEP each)"
