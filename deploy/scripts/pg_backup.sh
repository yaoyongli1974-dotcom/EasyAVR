#!/usr/bin/env bash
# EasyAVR PostgreSQL 逻辑备份：pg_dump -Fc（custom format）。
#
# !!! EASYAVR_CREDENTIAL_KEY（凭据加密主密钥）必须独立备份，
# !!! 绝不能和数据库备份放在同一处：库里存的是 AES-256-GCM 密文，
# !!! 没有主密钥这些备份解不开；主密钥泄在同一处等于整库明文泄露。
# !!! 主密钥由人工/外部 KMS 保管，不进入 /var/backups/easyavr/{db,pg,config}
# !!! 任何目录。非敏感配置由 backup.sh 单独写入 .../config（显式白名单）。
#
# 环境变量：
#   PG_DSN        必需，pg_dump 连接串；脚本日志永不输出它（可能含口令）。
#   PG_BACKUP_DIR 备份输出目录，默认 /var/backups/easyavr/pg
#   PG_KEEP       保留份数，默认 7
#
# 产物：<PG_BACKUP_DIR>/easyavr-pg-<timestamp>.dump
# 校验：文件非空 + pg_restore -l 能列出目录；失败保留 .part 并不发布。

set -euo pipefail
umask 077

PG_DSN=${PG_DSN:-}
PG_BACKUP_DIR=${PG_BACKUP_DIR:-/var/backups/easyavr/pg}
PG_KEEP=${PG_KEEP:-7}
KEEP_PART=0

log() { printf '%s %s\n' "$(date '+%F %T')" "$*"; }
die() { KEEP_PART=1; log "FAILED: $*"; exit 1; }

if [[ -z $PG_DSN ]]; then
  log "FAILED: PG_DSN is required (value is never logged)"
  exit 1
fi
if ! [[ $PG_KEEP =~ ^[1-9][0-9]*$ ]]; then
  die "PG_KEEP must be a positive integer, got: $PG_KEEP"
fi

mkdir -p -- "$PG_BACKUP_DIR" || die "cannot create backup dir: $PG_BACKUP_DIR"
chmod 0700 -- "$PG_BACKUP_DIR"

exec 9>"$PG_BACKUP_DIR/.pg-backup.lock"
if ! flock -n 9; then
  log "another postgres backup is already running, skip this round"
  exit 1
fi

TS=$(date '+%Y%m%d-%H%M%S-%N')
PART="$PG_BACKUP_DIR/.easyavr-pg-$TS.dump.part"
OUT="$PG_BACKUP_DIR/easyavr-pg-$TS.dump"
LIST="$PG_BACKUP_DIR/.pg-list-$TS.txt"

cleanup() {
  if [[ -n ${LIST:-} ]]; then
    rm -f -- "$LIST"
  fi
  if (( KEEP_PART == 1 )); then
    if [[ -f ${PART:-} ]]; then
      log "kept partial dump for inspection: $PART"
    fi
  else
    rm -f -- "${PART:-}"
  fi
}
trap cleanup EXIT
trap 'KEEP_PART=1; log "FAILED at line $LINENO: $BASH_COMMAND"' ERR

log "backing up PostgreSQL to $PG_BACKUP_DIR (DSN redacted)"
pg_dump -Fc -f "$PART" "$PG_DSN" || die "pg_dump failed (DSN redacted)"
[[ -s $PART ]] || die "pg_dump produced an empty file: $PART"
log "verify: dump file is non-empty ($(stat -c '%s' -- "$PART") bytes)"

log "verify: pg_restore -l catalog listing"
if ! pg_restore -l "$PART" >"$LIST"; then
  die "pg_restore -l failed (dump is not a readable custom-format archive)"
fi
[[ -s $LIST ]] || die "pg_restore -l returned an empty catalog"
log "verify: catalog ok ($(wc -l <"$LIST") lines)"

chmod 0600 -- "$PART"
mv -f -- "$PART" "$OUT"
[[ -s $OUT ]] || die "published dump is empty: $OUT"
log "database backup: $OUT ($(stat -c '%s' -- "$OUT") bytes, verified non-empty+catalog)"

mapfile -t files < <(find "$PG_BACKUP_DIR" -maxdepth 1 -type f -name 'easyavr-pg-*.dump' -printf '%f\n' | LC_ALL=C sort)
excess=$(( ${#files[@]} - PG_KEEP ))
if (( excess > 0 )); then
  for f in "${files[@]:0:excess}"; do
    rm -f -- "$PG_BACKUP_DIR/$f"
    log "pruned old backup: $f"
  done
fi

COUNT=$(find "$PG_BACKUP_DIR" -maxdepth 1 -type f -name 'easyavr-pg-*.dump' | wc -l)
log "done: latest=$(basename -- "$OUT") kept=$COUNT (limit $PG_KEEP)"
