#!/usr/bin/env bash
# EASYAVR-TECH-003 / TASK-017: DSN 主机门禁自检（CHECK_ONLY，不连接任何数据库）。
# 覆盖远程拒绝、三种回环写法、远程显式开关+三项确认、确认不匹配/缺失、
# 业务库/生产样式库名拒绝与无 DSN SKIP；断言退出码、无秘密泄漏、无临时目录创建。
# 输出 PASS/FAIL 汇总；FAIL>0 以非 0 退出。

set -uo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
GATE="$SCRIPT_DIR/verify-tech003-postgres.sh"

PASS=0
FAIL=0
SECRET='S3cr3t-Dsn-Gate-Test-2026'
REMOTE_HOST='pg.internal.example.test'
BASE_UNSET=(
  -u PG_TEST_ALLOW_REMOTE -u PG_TEST_REMOTE_HOST -u PG_TEST_REMOTE_PORT -u PG_TEST_MAINTENANCE_DB
)
BASE_SET=(PG_TEST_DSN_CHECK_ONLY=1)

CHECK_TMP=$(mktemp -d "${TMPDIR:-/tmp}/dsn-gate-test.XXXXXX") || { printf 'FAIL  无法创建自检临时目录\n'; exit 1; }
trap 'rm -rf -- "$CHECK_TMP"' EXIT

log() { printf '%s %s\n' "$(date '+%F %T')" "$*"; }
pass() { PASS=$((PASS + 1)); log "PASS  $*"; }
fail() { FAIL=$((FAIL + 1)); log "FAIL  $*"; }

# check_case <标签> <期望退出码> <DSN|-> [额外 env 赋值...]
check_case() {
  local label=$1 expect=$2 dsn=$3
  shift 3
  local out rc
  local -a cmd=(env "${BASE_UNSET[@]}")
  if [[ $dsn == - ]]; then cmd+=(-u PG_TEST_DSN); else cmd+=("PG_TEST_DSN=$dsn"); fi
  cmd+=("${BASE_SET[@]}" "$@" TMPDIR="$CHECK_TMP" bash "$GATE")
  out=$("${cmd[@]}" 2>&1)
  rc=$?

  if [[ $rc == "$expect" ]]; then
    pass "$label: 退出码 $rc"
  else
    fail "$label: 退出码 $rc（期望 $expect）"
  fi
  if printf '%s' "$out" | grep -Fq -- "$SECRET"; then
    fail "$label: 输出泄漏口令"
  else
    pass "$label: 输出无口令"
  fi
  if [[ $dsn != - ]] && printf '%s' "$out" | grep -Fq -- "$dsn"; then
    fail "$label: 输出泄漏完整 DSN"
  else
    pass "$label: 输出无完整 DSN"
  fi
  if printf '%s' "$out" | grep -Fq -- "$REMOTE_HOST"; then
    fail "$label: 输出泄漏真实主机地址"
  else
    pass "$label: 输出无真实主机地址"
  fi
  if printf '%s' "$out" | grep -Fq '未连接'; then
    pass "$label: 明确未连接数据库"
  else
    fail "$label: 缺少未连接数据库声明"
  fi
  if [[ -n $(find "$CHECK_TMP" -mindepth 1 -print -quit 2>/dev/null) ]]; then
    fail "$label: CHECK_ONLY 创建了临时文件/目录"
  else
    pass "$label: CHECK_ONLY 未创建目录/库"
  fi
}

log "== DSN 安全门 CHECK_ONLY 自检（不连数据库） =="

DSN_LOCALHOST="postgres://gateuser:${SECRET}@localhost:5432/postgres?sslmode=disable"
DSN_IPV4="postgres://gateuser:${SECRET}@127.0.0.1:5432/postgres?sslmode=disable"
DSN_IPV6="postgres://gateuser:${SECRET}@[::1]:5432/postgres?sslmode=disable"
DSN_IPV6_ENC="postgres://gateuser:${SECRET}@%5B%3A%3A1%5D:5432/postgres?sslmode=disable"
DSN_REMOTE="postgres://gateuser:${SECRET}@${REMOTE_HOST}:5432/postgres?sslmode=disable"
DSN_BUSINESS="postgres://gateuser:${SECRET}@127.0.0.1:5432/easyavr?sslmode=disable"
DSN_PRODLIKE="postgres://gateuser:${SECRET}@127.0.0.1:5432/easyavr_prod?sslmode=disable"

check_case "远程地址拒绝（无开关）" 1 "$DSN_REMOTE"
check_case "localhost 通过" 0 "$DSN_LOCALHOST"
check_case "127.0.0.1 通过" 0 "$DSN_IPV4"
check_case "::1 方括号通过" 0 "$DSN_IPV6"
check_case "::1 URL 编码通过" 0 "$DSN_IPV6_ENC"
check_case "远程显式开关+三项确认通过" 0 "$DSN_REMOTE" \
  PG_TEST_ALLOW_REMOTE=1 PG_TEST_REMOTE_HOST="$REMOTE_HOST" \
  PG_TEST_REMOTE_PORT=5432 PG_TEST_MAINTENANCE_DB=postgres
check_case "确认变量不匹配拒绝" 1 "$DSN_REMOTE" \
  PG_TEST_ALLOW_REMOTE=1 PG_TEST_REMOTE_HOST=other.host.test \
  PG_TEST_REMOTE_PORT=5432 PG_TEST_MAINTENANCE_DB=postgres
check_case "确认变量缺失拒绝" 1 "$DSN_REMOTE" \
  PG_TEST_ALLOW_REMOTE=1 PG_TEST_REMOTE_HOST="$REMOTE_HOST" PG_TEST_REMOTE_PORT=5432
check_case "业务库名拒绝" 1 "$DSN_BUSINESS"
check_case "生产样式库名拒绝" 1 "$DSN_PRODLIKE"
check_case "无 DSN SKIP" 2 -

log ""
log "== 汇总 =="
log "SUMMARY PASS=$PASS FAIL=$FAIL"
if ((FAIL > 0)); then exit 1; fi
exit 0
