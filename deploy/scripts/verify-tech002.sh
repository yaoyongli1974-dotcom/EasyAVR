#!/usr/bin/env bash
# EASYAVR-TECH-002 安全回归验证脚本。
#
# 默认只在临时目录做合成演练：不连接生产、不连接业务库、不读取真实凭据文件。
# 分类输出 PASS / FAIL / SKIP；退出码等于 FAIL 数（上限 255），全部通过为 0。
#
# 覆盖：脚本语法、主密钥不被生成/落盘、备份分离与白名单、SQLite 备份恢复往返、
#       PostgreSQL 脚本静态保护（真实 PG 恢复明确 SKIP）。
#
# 用法：bash deploy/scripts/verify-tech002.sh

set -uo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
ROOT=$(cd -- "$SCRIPT_DIR/../.." && pwd)
TMP=$(mktemp -d "${TMPDIR:-/tmp}/verify-tech002.XXXXXX")
trap 'rm -rf -- "$TMP"' EXIT

PASS=0
FAIL=0
SKIP=0

log() { printf '%s\n' "$*"; }
pass() { PASS=$((PASS + 1)); log "PASS  $*"; }
fail() { FAIL=$((FAIL + 1)); log "FAIL  $*"; }
skip() { SKIP=$((SKIP + 1)); log "SKIP  $*"; }
heading() { log ""; log "== $* =="; }

assert_file() {
  if [[ -f $1 ]]; then pass "$2"; else fail "$2 (missing: $1)"; fi
}
assert_grep() {
  # assert_grep <file> <pattern> <message>
  if grep -Eq -- "$2" "$1"; then pass "$3"; else fail "$3"; fi
}
assert_no_grep() {
  if grep -Eq -- "$2" "$1"; then fail "$3"; else pass "$3"; fi
}
assert_no_literal() {
  # assert_no_literal <file> <literal> <message>
  if grep -Fq -- "$2" "$1"; then fail "$3"; else pass "$3"; fi
}

heading "脚本语法检查"
ALL_SH=("$SCRIPT_DIR"/*.sh)
for f in "${ALL_SH[@]}"; do
  if bash -n "$f"; then
    pass "bash -n $(basename -- "$f")"
  else
    fail "bash -n $(basename -- "$f")"
  fi
done

heading "主密钥不被生成或落盘"
for f in "${ALL_SH[@]}"; do
  name=$(basename -- "$f")
  assert_no_grep "$f" '^[[:space:]]*(export[[:space:]]+)?EASYAVR_CREDENTIAL_KEY[[:space:]]*=' \
    "$name 不生成/赋值主密钥"
  assert_no_grep "$f" 'openssl[[:space:]]+rand|dd[[:space:]]+if=/dev/(u?random)' \
    "$name 不使用 CSPRNG 生成主密钥"
done
assert_grep "$SCRIPT_DIR/backup.sh" 'CONFIG_KEYS=' 'backup.sh 使用显式配置白名单'
assert_grep "$SCRIPT_DIR/restore.sh" "MODE='dry-run'" 'restore.sh 默认 dry-run'
assert_grep "$SCRIPT_DIR/pg_restore.sh" '<dsn-redacted>' 'pg_restore.sh 日志脱敏 DSN'

heading "PostgreSQL 脚本静态保护"
assert_grep "$SCRIPT_DIR/pg_backup.sh" 'pg_dump -Fc' 'pg_backup.sh 使用 -Fc 定制格式'
assert_grep "$SCRIPT_DIR/pg_backup.sh" 'pg_restore -l' 'pg_backup.sh 校验目录列表'
assert_grep "$SCRIPT_DIR/pg_restore.sh" '--clean --if-exists' 'pg_restore.sh 使用 clean 恢复'
assert_grep "$SCRIPT_DIR/pg_restore.sh" 'dry-run' 'pg_restore.sh 提供 dry-run'

if [[ -z ${EASYAVR_TEST_PG_DSN:-} ]]; then
  skip "真实 PG 恢复演练（默认不连接；本机 easyavr_ro 只读，禁止写业务库）"
elif ! command -v psql >/dev/null 2>&1; then
  skip "真实 PG 只读探测（psql 不可用）"
else
  if psql "$EASYAVR_TEST_PG_DSN" -Atc 'SELECT 1' >"$TMP/pg.out" 2>"$TMP/pg.err"; then
    if [[ $(tr -d '[:space:]' <"$TMP/pg.out") == 1 ]]; then
      pass "PG 只读探测成功（未写入）"
    else
      fail "PG 只读探测返回异常结果"
    fi
  else
    fail "PG 只读探测失败（DSN 未记录）"
  fi
fi

heading "SQLite 备份/恢复合成演练"
MISSING=''
for tool in python3 gzip flock date find; do
  command -v "$tool" >/dev/null 2>&1 || MISSING="$MISSING $tool"
done
if [[ -n $MISSING ]]; then
  skip "SQLite 演练（缺少工具:$MISSING）"
else
  SRC_DB="$TMP/source/easyavr.db"
  TARGET_DB="$TMP/restore/target.db"
  ENV_FILE="$TMP/env"
  BK_ROOT="$TMP/backups"
  PAYLOAD='SQLITE-VERIFY-PAYLOAD'
  OLD_VALUE='OLD-TARGET-VALUE'
  MASTER='MASTER-KEY-DO-NOT-BACKUP-123'
  JWT='JWT-SECRET-DO-NOT-BACKUP-456'
  ADMIN='ADMIN-PASS-DO-NOT-BACKUP-789'

  mkdir -p -- "$(dirname -- "$SRC_DB")" "$(dirname -- "$TARGET_DB")"
  python3 - "$SRC_DB" "$PAYLOAD" <<'PY'
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
try:
    con.execute("CREATE TABLE reg_rows (id INTEGER PRIMARY KEY, val TEXT)")
    con.execute("INSERT INTO reg_rows(val) VALUES (?)", (sys.argv[2],))
    con.commit()
finally:
    con.close()
PY
  python3 - "$TARGET_DB" "$OLD_VALUE" <<'PY'
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
try:
    con.execute("CREATE TABLE reg_rows (id INTEGER PRIMARY KEY, val TEXT)")
    con.execute("INSERT INTO reg_rows(val) VALUES (?)", (sys.argv[2],))
    con.commit()
finally:
    con.close()
PY

  {
    printf 'EASYAVR_ENV=production\n'
    printf 'EASYAVR_LISTEN=:18000\n'
    printf 'EASYAVR_ALLOW_ORIGIN=https://example.com\n'
    printf 'EASYAVR_CREDENTIAL_KEY=%s\n' "$MASTER"
    printf 'EASYAVR_JWT_SECRET=%s\n' "$JWT"
    printf 'EASYAVR_ADMIN_PASS=%s\n' "$ADMIN"
  } >"$ENV_FILE"
  ENV_BEFORE=$(sha256sum -- "$ENV_FILE" | cut -d' ' -f1)

  run_backup() {
    DB="$SRC_DB" BACKUP_DIR="$BK_ROOT" ENV_FILE="$ENV_FILE" KEEP="$1" \
      bash "$SCRIPT_DIR/backup.sh" >"$TMP/backup.log" 2>&1
  }
  if run_backup 2; then
    pass "backup.sh 执行成功"
  else
    fail "backup.sh 执行失败"
    sed 's/^/    /' "$TMP/backup.log"
  fi

  DB_GZ=$(find "$BK_ROOT/db" -maxdepth 1 -type f -name 'easyavr-*.db.gz' -print -quit 2>/dev/null)
  CFG_GZ=$(find "$BK_ROOT/config" -maxdepth 1 -type f -name 'config-*.gz' -print -quit 2>/dev/null)
  [[ -n $DB_GZ ]] && pass "生成 DB 备份" || fail "未生成 DB 备份"
  [[ -n $CFG_GZ ]] && pass "生成配置备份" || fail "未生成配置备份"
  if find "$BK_ROOT" -type f -name 'env-*.gz' -print -quit | grep -q .; then
    fail "备份产生了整份 env 文件"
  else
    pass "未备份整份 env 文件"
  fi

  gunzip -c -- "$CFG_GZ" >"$TMP/config.plain" 2>/dev/null
  assert_grep "$TMP/config.plain" '^EASYAVR_ENV=production$' '配置白名单保留非敏感键'
  for secret in "$MASTER" "$JWT" "$ADMIN"; do
    assert_no_literal "$TMP/config.plain" "$secret" '配置备份不含秘密值'
  done

  gunzip -c -- "$DB_GZ" >"$TMP/db.plain" 2>/dev/null
  VALUE=$(python3 - "$TMP/db.plain" <<'PY'
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
try:
    print(con.execute("SELECT val FROM reg_rows LIMIT 1").fetchone()[0])
finally:
    con.close()
PY
)
  if [[ $VALUE == "$PAYLOAD" ]]; then pass "DB 备份内容完整"; else fail "DB 备份内容异常: $VALUE"; fi

  DB_VALUE() {
    python3 - "$1" <<'PY'
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
try:
    row = con.execute("SELECT val FROM reg_rows LIMIT 1").fetchone()
    print(row[0] if row else "")
finally:
    con.close()
PY
  }

  if bash "$SCRIPT_DIR/restore.sh" -f "$DB_GZ" -d "$TARGET_DB" --dry-run >"$TMP/dry.log" 2>&1; then
    pass "restore.sh dry-run 执行成功"
  else
    fail "restore.sh dry-run 执行失败"
  fi
  if [[ $(DB_VALUE "$TARGET_DB") == "$OLD_VALUE" ]]; then
    pass "dry-run 未修改目标库"
  else
    fail "dry-run 修改了目标库"
  fi

  if bash "$SCRIPT_DIR/restore.sh" -f "$DB_GZ" -d "$TARGET_DB" --yes >"$TMP/restore.log" 2>&1; then
    pass "restore.sh --yes 执行成功"
  else
    fail "restore.sh --yes 执行失败"
    sed 's/^/    /' "$TMP/restore.log"
  fi
  if [[ $(DB_VALUE "$TARGET_DB") == "$PAYLOAD" ]]; then
    pass "恢复后数据一致"
  else
    fail "恢复后数据不一致"
  fi

  printf 'not a gzip archive' >"$TMP/corrupt.db.gz"
  if bash "$SCRIPT_DIR/restore.sh" -f "$TMP/corrupt.db.gz" -d "$TARGET_DB" --yes >"$TMP/corrupt.log" 2>&1; then
    fail "损坏备份被错误接受"
  else
    pass "损坏备份被拒绝"
  fi
  if [[ $(DB_VALUE "$TARGET_DB") == "$PAYLOAD" ]]; then
    pass "拒绝损坏备份时目标库未被改动"
  else
    fail "拒绝损坏备份时目标库被改动"
  fi

  ENV_AFTER=$(sha256sum -- "$ENV_FILE" | cut -d' ' -f1)
  if [[ $ENV_BEFORE == "$ENV_AFTER" ]]; then
    pass "演练未改写 env 文件（主密钥未生成/替换）"
  else
    fail "演练改写了 env 文件"
  fi
fi

heading "汇总"
log "PASS=$PASS FAIL=$FAIL SKIP=$SKIP"
if (( FAIL > 0 )); then
  exit $(( FAIL > 255 ? 255 : FAIL ))
fi
exit 0
