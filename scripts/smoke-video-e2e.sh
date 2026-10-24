#!/usr/bin/env bash
# End-to-end smoke test of the video path: synthetic RTSP source -> ZLMediaKit
# -> EasyAVR (channel start, play urls, diagnose, snapshot, VQD, record,
# playback URL, traffic). Needs no real camera.
#
# Prerequisites: ffmpeg on PATH, ZLMediaKit running, EasyAVR API running.
#
#   ./scripts/smoke-video-e2e.sh
#   EASYAVR_API=http://127.0.0.1:18000/api/v1 ZLM_SECRET=... ./scripts/smoke-video-e2e.sh
#
# Exits non-zero on the first failed assertion. KEEP=1 leaves the fixture
# device/channel (and the published stream) in place for manual inspection.
set -u

API=${EASYAVR_API:-http://127.0.0.1:18000/api/v1}
USER_NAME=${EASYAVR_USER:-easyavr}
PASS=${EASYAVR_PASS:-easyavr}
ZLM_API=${ZLM_API:-http://127.0.0.1/index/api}
ZLM_SECRET=${ZLM_SECRET:-EasyAVRzlmSecret2024x}
ZLM_RTSP=${ZLM_RTSP:-rtsp://127.0.0.1:554}
SRC_STREAM=${SRC_STREAM:-e2e_src}
KEEP=${KEEP:-0}

FAILED=0
step() { printf '\n== %s ==\n' "$*"; }
ok()   { printf '  [ok] %s\n' "$*"; }
bad()  { printf '  [FAIL] %s\n' "$*"; FAILED=$((FAILED + 1)); }
check() { if [[ "$1" == "$2" ]]; then ok "$3 ($1)"; else bad "$3: got '$1', want '$2'"; fi; }
jget() { python3 -c "import sys,json;d=json.load(sys.stdin);
for k in sys.argv[1].split('.'):
    d = d.get(k) if isinstance(d, dict) else None
    if d is None: break
print('' if d is None else d)" "$1"; }

cleanup() {
  [[ -n "${FF_PID:-}" ]] && kill "$FF_PID" 2>/dev/null
  if [[ "$KEEP" != "1" ]]; then
    [[ -n "${CH_ID:-}" ]] && api POST "/channels/$CH_ID/stop" >/dev/null 2>&1
    [[ -n "${DEV_ID:-}" ]] && api DELETE "/devices/$DEV_ID" >/dev/null 2>&1
    for key in ${STREAM_KEYS:-}; do
      curl -s "$ZLM_API/close_streams?secret=$ZLM_SECRET&vhost=__defaultVhost__&app=live&stream=$key&force=1" >/dev/null 2>&1
    done
  fi
}
trap cleanup EXIT

TOKEN=""
api() { # method path [body]
  local m=$1 p=$2 b=${3:-}
  if [[ -n "$b" ]]; then
    curl -s -X "$m" "$API$p" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d "$b"
  else
    curl -s -X "$m" "$API$p" -H "Authorization: Bearer $TOKEN"
  fi
}

step "preconditions"
command -v ffmpeg >/dev/null || { bad "ffmpeg not on PATH"; exit 1; }
command -v python3 >/dev/null || { bad "python3 not on PATH"; exit 1; }
curl -s "$ZLM_API/getServerConfig?secret=$ZLM_SECRET" | jget code | grep -qx 0 && ok "ZLMediaKit api" || { bad "ZLMediaKit api unreachable"; exit 1; }
TOKEN=$(curl -s -X POST "$API/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$USER_NAME\",\"password\":\"$PASS\"}" | jget data.token)
[[ -n "$TOKEN" ]] && ok "EasyAVR login" || { bad "login failed (is the API running?)"; exit 1; }

step "publish synthetic RTSP source"
ffmpeg -hide_banner -loglevel error -re -stream_loop -1 \
  -f lavfi -i "testsrc2=size=640x360:rate=25" \
  -c:v libx264 -preset veryfast -tune zerolatency -pix_fmt yuv420p -g 25 \
  -f rtsp -rtsp_transport tcp "$ZLM_RTSP/live/$SRC_STREAM" &
FF_PID=$!
published=0
for _ in $(seq 1 20); do
  sleep 1
  if curl -s "$ZLM_API/getMediaList?secret=$ZLM_SECRET" | grep -q "\"$SRC_STREAM\""; then published=1; break; fi
done
check "$published" 1 "source stream published"

step "create fixture device and channel"
DEV_ID=$(api POST /devices "{\"name\":\"e2e-source\",\"protocol\":\"rtsp\",\"accessMode\":\"pull\",\"ip\":\"127.0.0.1\",\"port\":554}" | jget data.id)
[[ -n "$DEV_ID" ]] && ok "device $DEV_ID" || { bad "device create failed"; exit 1; }
CH=$(api POST "/devices/$DEV_ID/channels" "{\"name\":\"e2e-ch\",\"sourceUrl\":\"$ZLM_RTSP/live/$SRC_STREAM\"}")
CH_ID=$(echo "$CH" | jget data.id)
STREAM_KEY=$(echo "$CH" | jget data.streamKey)
STREAM_KEYS="$STREAM_KEY"
[[ -n "$CH_ID" ]] && ok "channel $CH_ID ($STREAM_KEY)" || { bad "channel create failed"; exit 1; }

step "start channel and read play urls"
api POST "/channels/$CH_ID/start" >/dev/null
sleep 3
online=$(api GET "/channels/$CH_ID/play-urls" | jget data.online)
check "$online" True "channel online"
play_rtsp=$(api GET "/channels/$CH_ID/play-urls" | jget data.playUrls.rtsp)
[[ -n "$play_rtsp" ]] && ok "play url $play_rtsp" || bad "no play url"

step "playback diagnosis (ffprobe)"
probe=$(api GET "/channels/$CH_ID/diagnose?timeoutSec=10")
check "$(echo "$probe" | jget data.status)" ok "diagnose status"
check "$(echo "$probe" | jget data.probe.video.codec)" h264 "diagnose codec"

step "snapshot"
snap=$(api POST "/channels/$CH_ID/snapshot" '{}')
snap_url=$(echo "$snap" | jget data.url)
snap_bytes=$(echo "$snap" | jget data.sizeBytes)
[[ "${snap_bytes:-0}" -gt 0 ]] && ok "snapshot $snap_url ($snap_bytes bytes)" || bad "snapshot empty"
if [[ -n "$snap_url" ]]; then
  code=$(curl -s -o /dev/null -w '%{http_code}' "${API%/api/v1}$snap_url")
  check "$code" 200 "snapshot served"
fi

step "video quality diagnosis"
vqd_level=$(api GET "/channels/$CH_ID/vqd" | jget data.level)
[[ -n "$vqd_level" ]] && ok "vqd completed (level=$vqd_level, advisory)" || bad "vqd returned no level"

step "record, stop and expect an auto-cataloged playback entry"
api POST "/channels/$CH_ID/record/start" >/dev/null
sleep 6
api POST "/channels/$CH_ID/record/stop" >/dev/null
sleep 2
recs=$(api GET "/channels/$CH_ID/recordings")
count=$(echo "$recs" | python3 -c 'import sys,json;print(len(json.load(sys.stdin).get("data") or []))')
[[ "${count:-0}" -ge 1 ]] && ok "catalog entries $count" || bad "catalog empty after stop (recording not indexed)"
rec_url=$(echo "$recs" | python3 -c 'import sys,json;d=json.load(sys.stdin).get("data") or [];print(d[0]["url"] if d else "")')
if [[ -n "$rec_url" ]]; then
  size=$(curl -s -o /dev/null -w '%{size_download}' "$rec_url")
  [[ "${size:-0}" -gt 0 ]] && ok "recording playable ($size bytes)" || bad "recording url 404: $rec_url"
fi

step "traffic sampling"
api POST /video/traffic/sync >/dev/null
bytes=$(api GET "/channels/$CH_ID/traffic" | jget data.media.bytes)
[[ "${bytes:-0}" -gt 0 ]] && ok "traffic bytes $bytes" || bad "traffic bytes still 0 (ZLM totalBytes not read)"

printf '\n'
if [[ "$FAILED" -eq 0 ]]; then
  echo "SMOKE PASSED"
  exit 0
fi
echo "SMOKE FAILED ($FAILED checks)"
exit 1
