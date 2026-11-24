#!/usr/bin/env bash
# EasyAVR PostgreSQL 恢复：pg_restore -j4 --clean --if-exists。
#
# !!! EASYAVR_CREDENTIAL_KEY 必须独立备份，不能和 DB 备份放在同一处。
#
# --clean --if-exists 会 DROP 库中已有对象，务必先确认已备份；
# 默认 dry-run，只有 --yes 才真正连接数据库执行恢复。
# DSN（可能含口令）只作为 pg_restore 参数传入，日志里一律显示为 <dsn-redacted>。
#
# 用法：pg_restore.sh -f <backup.dump> [-d <PG_DSN>] [--dry-run|--yes]
#   -d 省略时取环境变量 PG_DSN；两者都给时 -d 优先。
#   并行度可用 PG_RESTORE_JOBS 覆盖（默认 4）。

set -euo pipefail
umask 077

log() { printf '%s %s\n' "$(date '+%F %T')" "$*"; }
die() { log "FAILED: $*"; exit 1; }

usage() {
  cat <<'EOF'
usage: pg_restore.sh -f <backup.dump> [-d <PG_DSN>] [--dry-run|--yes]
  -f, --file     pg_dump -Fc 生成的 custom-format 备份（必填）
  -d, --dsn      目标连接串，省略时取 PG_DSN（日志不输出其值）
      --dry-run  只校验备份并打印计划，不连接数据库写入（默认）
      --yes      实际执行 pg_restore
  -h, --help     显示帮助
EOF
}

BACKUP=''
DSN=${PG_DSN:-}
MODE='dry-run'
DRY_SEEN=0
YES_SEEN=0
JOBS=${PG_RESTORE_JOBS:-4}

while (( $# )); do
  case $1 in
    -f|--file)
      (( $# >= 2 )) || { usage >&2; exit 2; }
      BACKUP=$2; shift 2 ;;
    -d|--dsn)
      (( $# >= 2 )) || { usage >&2; exit 2; }
      DSN=$2; shift 2 ;;
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

if [[ -z $BACKUP ]]; then
  usage >&2
  exit 2
fi
if ! [[ $JOBS =~ ^[1-9][0-9]*$ ]]; then
  die "PG_RESTORE_JOBS must be a positive integer, got: $JOBS"
fi

[[ -f $BACKUP ]] || die "backup file not found: $BACKUP"
[[ -s $BACKUP ]] || die "backup file is empty: $BACKUP"

log "verify: dump file is non-empty ($(stat -c '%s' -- "$BACKUP") bytes)"
LIST=$(mktemp)
trap 'rm -f -- "$LIST"' EXIT
if ! pg_restore -l "$BACKUP" >"$LIST"; then
  die "pg_restore -l failed: not a readable custom-format archive"
fi
[[ -s $LIST ]] || die "pg_restore -l returned an empty catalog"
log "verify: catalog ok ($(wc -l <"$LIST") lines)"

if [[ $MODE == dry-run ]]; then
  if [[ -z $DSN ]]; then
    die "PG_DSN is required (value is never logged)"
  fi
  log "dry-run: would run pg_restore -j$JOBS --clean --if-exists -d <dsn-redacted> $BACKUP"
  log "WARNING: --clean --if-exists drops existing objects in the target database"
  log "dry-run: no changes made"
  exit 0
fi

if [[ -z $DSN ]]; then
  die "PG_DSN is required (value is never logged)"
fi

log "restoring $BACKUP with pg_restore -j$JOBS --clean --if-exists (DSN redacted)"
RESTORE_LOG=$(mktemp)
trap 'rm -f -- "$LIST" "$RESTORE_LOG"' EXIT
if ! pg_restore -j"$JOBS" --clean --if-exists -d "$DSN" "$BACKUP" >"$RESTORE_LOG" 2>&1; then
  cat -- "$RESTORE_LOG" >&2
  # pg_dump/pg_restore 17 emits `SET transaction_timeout = 0`, which older
  # servers do not know. It is a no-op header command; pg_restore still restores
  # all data and exits non-zero only because of these ignored statements.
  if grep -q 'unrecognized configuration parameter "transaction_timeout"' "$RESTORE_LOG" \
     && ! grep -v 'unrecognized configuration parameter "transaction_timeout"' "$RESTORE_LOG" | grep -q 'ERROR:'; then
    log "WARNING: benign pg_dump/pg_restore version skew (transaction_timeout); restore data completed"
  else
    die "pg_restore failed (DSN redacted); check target database state manually"
  fi
fi
log "done: restore command succeeded; verify with /api/v1/readyz and a read-only query"
