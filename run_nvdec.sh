#!/usr/bin/env bash
# Run on Linux NVIDIA (WSL/RTX): NVDEC decode + OpenVINO CPU.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
export PATH="${HOME}/.local/go/bin:${PATH}"
export LD_LIBRARY_PATH="${HOME}/opt/openvino/runtime/lib/intel64:${HOME}/opt/openvino/runtime/3rdparty/tbb/lib:/usr/lib/wsl/lib:${LD_LIBRARY_PATH:-}"
export OPENVINO_LIB_DIR="${HOME}/opt/openvino/runtime/lib/intel64"
CFG="${1:-cameras_live161.json}"
BIN="${ROOT}/motioncamotion"
if [[ ! -x "$BIN" ]]; then
  (cd "$ROOT" && go build -tags nvidia_nvdec -o motioncamotion .)
fi
exec "$BIN" "$CFG"
