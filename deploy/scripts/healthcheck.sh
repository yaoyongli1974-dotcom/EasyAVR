#!/usr/bin/env bash
# EasyAVR 存活探测：systemd 单元 + 健康检查端点。
#
# 连续失败 MAX_FAILS 次（默认 3）才重启服务，避免一次抖动就打断录像/AI 任务。
# 状态文件里只存一个整数，成功时清零。cron 每 5 分钟跑一次，脚本须在 5 秒内结束。
#
# 可配置项（环境变量，均可选，默认值即生产机实际值）：
#   SERVICE      systemd 单元名，默认 easyavr
#   HEALTH_URL   健康检查地址，默认 http://127.0.0.1:18000/api/v1/healthz
#   STATE_FILE   失败计数文件，默认 /var/tmp/easyavr-healthcheck.fails
#   MAX_FAILS    连续失败多少次后重启，默认 3
#   RESTART_CMD  重启命令，默认 "systemctl restart easyavr"
#                （测试时覆盖成 echo，就不会真的动服务）

set -euo pipefail
umask 077

SERVICE=${SERVICE:-easyavr}
HEALTH_URL=${HEALTH_URL:-http://127.0.0.1:18000/api/v1/healthz}
STATE_FILE=${STATE_FILE:-/var/tmp/easyavr-healthcheck.fails}
MAX_FAILS=${MAX_FAILS:-3}
RESTART_CMD=${RESTART_CMD:-systemctl restart easyavr}

log() { printf '%s %s\n' "$(date '+%F %T')" "$*"; }

# 根分区用量一并记进日志：这台机只有 19G，磁盘打满比进程挂掉更难自救，
# 而日志是唯一无侵入的观测手段（不在这里触发任何清理或重启）。
ROOT_USED=$(df --output=pcent / 2>/dev/null | tail -1 | tr -dc '0-9' || true)
ROOT_USED=${ROOT_USED:-0}

fails=0
if [[ -r $STATE_FILE ]]; then
  raw=$(<"$STATE_FILE") || raw=0
  if [[ $raw =~ ^[0-9]+$ ]]; then
    fails=$raw
  fi
fi

ok=1
reason=''

if ! systemctl is-active --quiet "$SERVICE"; then
  ok=0
  reason="systemctl is-active $SERVICE != active"
fi

if (( ok )); then
  body=''
  if body=$(curl -fsS --max-time 5 "$HEALTH_URL" 2>/dev/null); then
    if ! grep -q '"code":0' <<<"$body"; then
      ok=0
      reason="healthz returned code != 0"
    fi
  else
    ok=0
    reason="healthz unreachable: $HEALTH_URL"
  fi
fi

if (( ok )); then
  printf '0\n' >"$STATE_FILE"
  log "ok disk=${ROOT_USED}%"
  exit 0
fi

fails=$(( fails + 1 ))
printf '%s\n' "$fails" >"$STATE_FILE"

if (( fails >= MAX_FAILS )); then
  if $RESTART_CMD >/dev/null 2>&1; then
    printf '0\n' >"$STATE_FILE"
    log "restarted (was fail#$fails: $reason) disk=${ROOT_USED}%"
  else
    log "fail#$fails ($reason; restart command failed: $RESTART_CMD) disk=${ROOT_USED}%"
    exit 1
  fi
else
  log "fail#$fails ($reason) disk=${ROOT_USED}%"
fi
