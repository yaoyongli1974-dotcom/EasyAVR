#!/usr/bin/env bash
# EasyAVR SQLite 备份恢复：把 gzip 过的备份原子恢复到指定 DB 路径。
#
# 用法：restore.sh -f <backup.db.gz> -d <target.db> [--dry-run|--yes]
# 默认是 dry-run，只有 --yes 才真正写入；两种都给时 dry-run 优先。
#
# 安全约束：
#   - 恢复前若目标库已存在，先在同目录留 <target>.before-restore-<TS>-<PID> 安全副本；
#   - 先解压到同目录 .restore.tmp，校验通过后再 mv 原子替换；
#   - 不删除任何业务数据，也绝不碰 -wal / -shm 残留——若有残留会给出警告，
#     由人工停服务后处理，否则旧 WAL 可能回放覆盖刚恢复的库。
#
# 主密钥（EASYAVR-TECH-002 / TASK-002）：
#   - 本脚本永远不会生成新密钥，也不会改写 /etc/easyavr/env；
#   - 库里是 AES-256-GCM 密文，必须由人工先把原 EASYAVR_CREDENTIAL_KEY
#     恢复到 /etc/easyavr/env（或外部 KMS 注入），再启动服务；
#   - 原密钥丢失则加密凭据不可恢复，切勿用新密钥"顶上"——那只会让
#     所有 enc:<kid>:... 行解密失败。
#
# 示例：
#   restore.sh -f /var/backups/easyavr/db/easyavr-20261124-030000.db.gz \
#              -d /opt/easyavr/server/data/easyavr.db --dry-run
#   restore.sh -f ... -d ... --yes

set -euo pipefail
umask 077

log() { printf '%s %s\n' "$(date '+%F %T')" "$*"; }
die() { log "FAILED: $*"; exit 1; }

usage() {
  cat <<'EOF'
usage: restore.sh -f <backup.db.gz> -d <target.db> [--dry-run|--yes]
  -f, --file     SQLite gzip 备份文件（必填）
  -d, --db       要恢复到的 DB 路径（必填）
      --dry-run  只检查并打印计划，不写入（默认）
      --yes      实际执行恢复
  -h, --help     显示帮助
EOF
}

# 与 backup.sh 同款校验：只有唯一一行 "ok" 才算通过。
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

BACKUP=''
TARGET=''
MODE='dry-run'
DRY_SEEN=0
YES_SEEN=0

while (( $# )); do
  case $1 in
    -f|--file)
      (( $# >= 2 )) || { usage >&2; exit 2; }
      BACKUP=$2; shift 2 ;;
    -d|--db)
      (( $# >= 2 )) || { usage >&2; exit 2; }
      TARGET=$2; shift 2 ;;
    --dry-run) MODE='dry-run'; DRY_SEEN=1; shift ;;
    --yes) YES_SEEN=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

if (( DRY_SEEN && YES_SEEN )); then
  log "WARNING: --dry-run and --yes both given; dry-run wins (no changes)"
  MODE='dry-run'
elif (( YES_SEEN )); then
  MODE='yes'
fi

if [[ -z $BACKUP || -z $TARGET ]]; then
  usage >&2
  exit 2
fi

[[ -f $BACKUP ]] || die "backup file not found: $BACKUP"
[[ -s $BACKUP ]] || die "backup file is empty: $BACKUP"
if [[ -e $TARGET && ! -f $TARGET ]]; then
  die "target is not a regular file: $TARGET"
fi
TARGET_DIR=$(dirname -- "$TARGET")
[[ -d $TARGET_DIR ]] || die "target directory not found: $TARGET_DIR"

log "REMINDER: restore the original EASYAVR_CREDENTIAL_KEY manually before starting the service; this script never creates or rotates keys (lost key = encrypted credentials unrecoverable)"

for suffix in -wal -shm; do
  if [[ -e "$TARGET$suffix" ]]; then
    log "WARNING: found $TARGET$suffix; this script does not touch WAL/SHM. Stop the service and handle it manually before starting EasyAVR."
  fi
done

log "verify: gzip -t $BACKUP"
gzip -t -- "$BACKUP" || die "gzip -t failed: $BACKUP"
log "verify: gzip -t ok"

STAMP=$(date '+%Y%m%d-%H%M%S')
SAFE=''
TMP="$TARGET_DIR/.restore-$STAMP-$$-$RANDOM.tmp"
if [[ -f $TARGET ]]; then
  SAFE="$TARGET.before-restore-$STAMP-$$-$RANDOM"
fi

if [[ $MODE == dry-run ]]; then
  log "dry-run: source=$BACKUP target=$TARGET"
  if [[ -n $SAFE ]]; then
    log "dry-run: would create safety copy $SAFE"
  else
    log "dry-run: target does not exist, no safety copy needed"
  fi
  log "dry-run: would decompress to $TMP, verify, then atomic mv into place"
  log "dry-run: no changes made"
  exit 0
fi

if [[ -n $SAFE ]]; then
  cp -p -- "$TARGET" "$SAFE" || die "cannot create safety copy: $SAFE"
  [[ -s $SAFE ]] || die "safety copy is empty: $SAFE"
  log "safety copy: $SAFE"
else
  log "no existing target at $TARGET; skipping safety copy"
fi

trap 'rm -f -- "$TMP"' EXIT
gunzip -c -- "$BACKUP" >"$TMP" || die "cannot decompress $BACKUP"
[[ -s $TMP ]] || die "decompressed backup is empty"

# 保留权限：有旧库时沿用其 mode/owner；新路径按生产库的 0640 设置。
if [[ -f $TARGET ]]; then
  chmod --reference="$TARGET" "$TMP" || die "cannot copy mode from $TARGET"
  if ! chown --reference="$TARGET" "$TMP" 2>/dev/null; then
    log "WARNING: cannot preserve owner from $TARGET (continuing with current owner)"
  fi
else
  chmod 0640 -- "$TMP" || die "cannot set mode on $TMP"
fi

CHECK=''
if CHECK=$(integrity_check "$TMP"); then
  log "verify: PRAGMA integrity_check=$CHECK (before publish)"
else
  die "PRAGMA integrity_check failed: $CHECK (target unchanged)"
fi

mv -f -- "$TMP" "$TARGET" || die "atomic replace failed: $TARGET"
log "restored: $TARGET <- $BACKUP (atomic rename from $TMP)"

FINAL=''
if FINAL=$(integrity_check "$TARGET"); then
  log "verify: PRAGMA integrity_check=$FINAL (after restore)"
else
  die "post-restore integrity check failed: $FINAL (safety copy kept)"
fi
log "done: restore verified, safety copy=${SAFE:-none}"
