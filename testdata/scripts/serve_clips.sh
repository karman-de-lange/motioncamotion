#!/usr/bin/env bash
# Publish empty|walk clips on rtsp://127.0.0.1:8556/... via MediaMTX + ffmpeg (test only).
# Usage:
#   ./testdata/scripts/serve_clips.sh empty
#   ./testdata/scripts/serve_clips.sh walk
# Then:
#   ./motioncamotion cameras_dummy_clips.json
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CLIPDIR="${ROOT}/testdata/clips"
MTX="${ROOT}/testdata/tools/mediamtx"
CONF="${ROOT}/testdata/mediamtx_clips.yml"
TAG="${1:-empty}"
if [[ "$TAG" != "empty" && "$TAG" != "walk" ]]; then
  echo "usage: $0 empty|walk" >&2
  exit 1
fi
for cam in nvr161_2 nvr161_3; do
  for stream in main sub; do
    f="${CLIPDIR}/${TAG}_${cam}_${stream}.mp4"
    if [[ ! -s "$f" ]]; then
      echo "missing $f — run: ./testdata/scripts/record_clips.sh $TAG" >&2
      exit 1
    fi
  done
done
if [[ ! -x "$MTX" ]]; then
  echo "missing mediamtx at $MTX" >&2
  exit 1
fi

# stop previous publishers/server if any
pkill -f 'testdata/tools/mediamtx' 2>/dev/null || true
pkill -f 'ffmpeg.*8556/nvr161' 2>/dev/null || true
sleep 0.5

cd "${ROOT}/testdata"
"$MTX" "$CONF" > /tmp/mediamtx_clips.log 2>&1 &
echo "mediamtx pid $!"
sleep 1

publish() {
  local path=$1 file=$2
  # -re realtime, loop forever, copy bitstream (HEVC/H264 as recorded)
  ffmpeg -hide_banner -loglevel error -re -stream_loop -1 -i "$file" \
    -c copy -f rtsp -rtsp_transport tcp "rtsp://127.0.0.1:8556/${path}" \
    >"/tmp/ffmpeg_${path}.log" 2>&1 &
  echo "publish $path <- $(basename "$file") pid $!"
}

for cam in nvr161_2 nvr161_3; do
  for stream in main sub; do
    publish "${cam}_${stream}" "${CLIPDIR}/${TAG}_${cam}_${stream}.mp4"
  done
done

echo "serving TAG=$TAG on rtsp://127.0.0.1:8556/{nvr161_2,nvr161_3}_{main,sub}"
echo "run: cd $ROOT && ./motioncamotion cameras_dummy_clips.json"
echo "stop: pkill -f testdata/tools/mediamtx; pkill -f 'ffmpeg.*8556/nvr161'"
