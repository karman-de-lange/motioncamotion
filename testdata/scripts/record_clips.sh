#!/usr/bin/env bash
# Capture 30s video-only clips from NVR 10.0.0.161 (ffmpeg = debug/test only).
# Usage:
#   ./testdata/scripts/record_clips.sh empty
#   ./testdata/scripts/record_clips.sh walk    # stand in FOV / walk in-out for ~30s
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CLIPDIR="${ROOT}/testdata/clips"
mkdir -p "$CLIPDIR"
TAG="${1:-empty}"
if [[ "$TAG" != "empty" && "$TAG" != "walk" ]]; then
  echo "usage: $0 empty|walk" >&2
  exit 1
fi
UA="${NVR_RTSP:-rtsp://homeassistant:Karman112233@10.0.0.161:554}"
record() {
  local name=$1 url=$2
  echo "recording ${name}.mp4 (30s)..."
  timeout 45 ffmpeg -hide_banner -loglevel error -rtsp_transport tcp -i "$url" \
    -t 30 -map 0:v:0 -c copy -an -y "${CLIPDIR}/${name}.mp4"
  ls -lh "${CLIPDIR}/${name}.mp4"
}
record "${TAG}_nvr161_2_main" "${UA}/Streaming/Channels/201" &
record "${TAG}_nvr161_2_sub"  "${UA}/Streaming/Channels/202" &
record "${TAG}_nvr161_3_main" "${UA}/Streaming/Channels/301" &
record "${TAG}_nvr161_3_sub"  "${UA}/Streaming/Channels/302" &
wait
echo "done — clips in ${CLIPDIR}"
ls -lh "${CLIPDIR}/${TAG}"_*.mp4
