#!/usr/bin/env bash
set -euo pipefail

# Build script for avatar_win (Go + WebView2 + sherpa-onnx).
# Compiles inside Docker — outputs Windows .exe and release zip.
# No code signing (signtool.exe requires Windows).

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
IMAGE="avatar_win_build:1.0"
BINARY="avatar-desktop-x64"
GO_VERSION="1.24.13"
GO_TAR="go${GO_VERSION}.linux-amd64.tar.gz"
GO_URL="https://golang.google.cn/dl/${GO_TAR}"
DEPS_DIR="$SCRIPT_DIR/build/deps"

# ── Optional proxy (build.sh http_proxy=... https_proxy=... no_proxy=...) ──
HTTP_PROXY_VAL=""
HTTPS_PROXY_VAL=""
NO_PROXY_VAL=""
for arg in "$@"; do
  case "$arg" in
    http_proxy=*|HTTP_PROXY=*)   HTTP_PROXY_VAL="${arg#*=}" ;;
    https_proxy=*|HTTPS_PROXY=*) HTTPS_PROXY_VAL="${arg#*=}" ;;
    no_proxy=*|NO_PROXY=*)       NO_PROXY_VAL="${arg#*=}" ;;
    *) echo "WARNING: ignoring unknown arg: $arg" ;;
  esac
done
HTTP_PROXY_VAL="${HTTP_PROXY_VAL:-${HTTP_PROXY:-${http_proxy:-}}}"
HTTPS_PROXY_VAL="${HTTPS_PROXY_VAL:-${HTTPS_PROXY:-${https_proxy:-}}}"
NO_PROXY_VAL="${NO_PROXY_VAL:-${NO_PROXY:-${no_proxy:-}}}"

add_scheme() { local v="$1"; [[ -z "$v" || "$v" == *"://"* ]] && { printf '%s' "$v"; return; }; printf 'http://%s' "$v"; }
HTTP_PROXY_VAL="$(add_scheme "$HTTP_PROXY_VAL")"
HTTPS_PROXY_VAL="$(add_scheme "$HTTPS_PROXY_VAL")"

DOCKER_BUILD_ARGS=()
DOCKER_RUN_ENV=()
if [[ -n "$HTTP_PROXY_VAL" || -n "$HTTPS_PROXY_VAL" ]]; then
  echo "Proxy: http=${HTTP_PROXY_VAL:-<none>} https=${HTTPS_PROXY_VAL:-<none>} no_proxy=${NO_PROXY_VAL:-<none>}"
  export HTTP_PROXY="$HTTP_PROXY_VAL" HTTPS_PROXY="$HTTPS_PROXY_VAL"
  export http_proxy="$HTTP_PROXY_VAL" https_proxy="$HTTPS_PROXY_VAL"
  export NO_PROXY="$NO_PROXY_VAL"     no_proxy="$NO_PROXY_VAL"
  DOCKER_BUILD_ARGS=(--build-arg "HTTP_PROXY=$HTTP_PROXY_VAL" --build-arg "HTTPS_PROXY=$HTTPS_PROXY_VAL" --build-arg "NO_PROXY=$NO_PROXY_VAL")
  DOCKER_RUN_ENV=(-e "HTTP_PROXY=$HTTP_PROXY_VAL" -e "HTTPS_PROXY=$HTTPS_PROXY_VAL" -e "NO_PROXY=$NO_PROXY_VAL" -e "http_proxy=$HTTP_PROXY_VAL" -e "https_proxy=$HTTPS_PROXY_VAL" -e "no_proxy=$NO_PROXY_VAL")
fi

cd "$SCRIPT_DIR"

# ── Cleanup hook: always restore web/index.html and clean temp dir ──
INDEX_BAK="web/index.html.orig"
TMP_DIR=""
cleanup() {
  if [[ -f "$INDEX_BAK" ]]; then
    mv -f "$INDEX_BAK" "web/index.html"
  fi
  if [[ -n "$TMP_DIR" ]] && [[ -d "$TMP_DIR" ]]; then
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT

# ── 1. Check prerequisites ──────────────────────────────────────
if [[ ! -f "main.go" ]] || [[ ! -f "go.mod" ]]; then
  echo "ERROR: run this script from the avatar_win/ directory"
  exit 1
fi

if ! command -v docker &>/dev/null; then
  echo "ERROR: docker not found"
  exit 1
fi

# ── 2. Cache Go toolchain ───────────────────────────────────────
mkdir -p "$DEPS_DIR"

if [[ ! -f "$DEPS_DIR/$GO_TAR" ]]; then
  echo "Downloading Go $GO_VERSION ..."
  if [[ -n "$HTTP_PROXY_VAL" || -n "$HTTPS_PROXY_VAL" ]]; then
    wget -q --show-progress -e use_proxy=yes "$GO_URL" -O "$DEPS_DIR/$GO_TAR"
  else
    wget -q --show-progress "$GO_URL" -O "$DEPS_DIR/$GO_TAR"
  fi
  echo "Go tarball cached at build/deps/$GO_TAR"
else
  echo "Go $GO_VERSION cached ($(du -h "$DEPS_DIR/$GO_TAR" | cut -f1))"
fi

# ── 3. Build Docker image if missing ────────────────────────────
if ! docker image inspect "$IMAGE" &>/dev/null; then
  echo "Building Docker image $IMAGE ..."
  docker build "${DOCKER_BUILD_ARGS[@]}" -t "$IMAGE" -f Dockerfile .
  echo "Docker image $IMAGE built"
else
  echo "Docker image $IMAGE ready"
fi

# ── 4. Build Windows .exe in Docker ─────────────────────────────
GOCACHE_DIR="$SCRIPT_DIR/build/gocache"
GOMODCACHE_DIR="$SCRIPT_DIR/build/gomodcache"
mkdir -p "$GOCACHE_DIR" "$GOMODCACHE_DIR" "$SCRIPT_DIR/dist"

VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(date -u +"%Y-%m-%d_%H:%M:%S_UTC")

echo ""
echo "╔══════════════════════════════════════════════════╗"
echo "║  Avatar Desktop — Docker Build (Windows .exe)   ║"
echo "╠══════════════════════════════════════════════════╣"
echo "║  Version:    $VERSION"
echo "║  Build time: $BUILD_TIME"
echo "╚══════════════════════════════════════════════════╝"

# Back up web/index.html before JS obfuscation.
cp web/index.html "$INDEX_BAK"

echo ""
echo "=== Building ${BINARY}.exe (Windows) ==="
docker run --rm \
  -v "$SCRIPT_DIR":/workspace \
  -v "$GOCACHE_DIR":/tmp/gocache \
  -v "$GOMODCACHE_DIR":/go/pkg/mod \
  -w /workspace \
  -e GOFLAGS="-buildvcs=false" \
  -e GOCACHE=/tmp/gocache \
  -e GOMODCACHE=/go/pkg/mod \
  -e GOPROXY="https://goproxy.cn,direct" \
  -e CGO_ENABLED=1 \
  -e GOOS=windows \
  -e GOARCH=amd64 \
  -e CC=x86_64-w64-mingw32-gcc \
  -e CXX=x86_64-w64-mingw32-g++ \
  -e GOTOOLCHAIN=local \
  -e GOGARBLE="github.com/liuyngchng/avatar-desktop-x64" \
  -e HOST_UID="$(id -u)" \
  -e HOST_GID="$(id -g)" \
  ${DOCKER_RUN_ENV[@]+"${DOCKER_RUN_ENV[@]}"} \
  "$IMAGE" \
  bash -c "
    set -euo pipefail
    echo '    JS obfuscation...'
    node scripts/obfuscate.js
    echo '    Building garble (linux host binary)...'
    GOOS=linux GOARCH=amd64 CGO_ENABLED=1 CC=gcc GOBIN=/tmp go install mvdan.cc/garble@v0.14.2
    echo '    Building with garble -literals...'
    /tmp/garble -literals build \
      -ldflags=\"-s -w -H windowsgui -X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}\" \
      -o dist/${BINARY}.exe .
    chown \$HOST_UID:\$HOST_GID dist/${BINARY}.exe
    echo '    Windows build complete.'
  "

# Restore the original index.html immediately after the build.
mv -f "$INDEX_BAK" "web/index.html"

echo ""
echo "=== Build complete ==="
EXE_SIZE=$(du -h "$SCRIPT_DIR/dist/${BINARY}.exe" | cut -f1)
echo "  Windows: dist/${BINARY}.exe  (${EXE_SIZE})"

# ── 5. Packaging: zip with .exe + DLLs + KWS model + config ─────
echo ""
echo "Packaging Windows distribution..."

TMP_DIR=$(mktemp -d)

# Locate sherpa-onnx Windows DLLs.
WIN_SHERPA_DLL_DIR="$GOMODCACHE_DIR/github.com/k2-fsa/sherpa-onnx-go-windows@v1.13.6/lib/x86_64-pc-windows-gnu"
# Fall back to host GOPATH in case the project cache doesn't have it yet.
if [[ ! -d "$WIN_SHERPA_DLL_DIR" ]]; then
  WIN_SHERPA_DLL_DIR="$HOME/go/pkg/mod/github.com/k2-fsa/sherpa-onnx-go-windows@v1.13.6/lib/x86_64-pc-windows-gnu"
fi

PKG_NAME="${BINARY}-$(date +%Y%m%d)"
PKG_DIR="$TMP_DIR/$PKG_NAME"
mkdir -p "$PKG_DIR"

# Binary
cp "dist/${BINARY}.exe" "$PKG_DIR/"

# sherpa-onnx runtime DLLs (required for KWS wake word detection).
if [[ -d "$WIN_SHERPA_DLL_DIR" ]]; then
  cp "$WIN_SHERPA_DLL_DIR"/*.dll "$PKG_DIR/"
  echo "  Added: onnxruntime.dll, sherpa-onnx-c-api.dll, sherpa-onnx-cxx-api.dll"
else
  echo "  WARNING: sherpa-onnx DLLs not found at $WIN_SHERPA_DLL_DIR"
  echo "           The exe will fail to start without them."
fi

# KWS wake word model (int8 only).
KWS_DIR="models/kws"
if [[ -d "$KWS_DIR" ]]; then
  mkdir -p "$PKG_DIR/$KWS_DIR"
  for f in "$KWS_DIR"/*; do
    name=$(basename "$f")
    # Only include int8 models + tokens.txt; skip fp32 models and test_wavs.
    if [[ "$name" == "tokens.txt" ]] || [[ "$name" == *".int8.onnx" ]]; then
      cp "$f" "$PKG_DIR/$KWS_DIR/"
      echo "  Added: $KWS_DIR/$name"
    fi
  done
else
  echo "  WARNING: $KWS_DIR not found — wake word detection will fail"
fi

# cfg.yml template
if [[ -f "cfg.yml.template" ]]; then
  cp "cfg.yml.template" "$PKG_DIR/cfg.yml"
  echo "  Added: cfg.yml"
fi

# User manual
if [[ -f "USER_MANUAL.md" ]]; then
  cp "USER_MANUAL.md" "$PKG_DIR/使用说明.md"
  echo "  Added: 使用说明.md"
fi

# Write a brief README for the zip.
cat > "$PKG_DIR/README.txt" << 'WINEOF'
Avatar Desktop - Windows 版本使用说明
======================================

运行前请确保以下文件在同一目录中：
  avatar-desktop-x64.exe     主程序
  onnxruntime.dll            ONNX Runtime 运行库
  sherpa-onnx-c-api.dll      Sherpa-ONNX 运行库
  sherpa-onnx-cxx-api.dll    Sherpa-ONNX C++ 运行库
  models/kws/                KWS 唤醒词模型（端到端 Zipformer）

配置：
  1. 编辑 cfg.yml，填入 llm.url 和 api_key
  2. ASR/TTS 默认在线模式（阿里云 DashScope API）
  3. 如需离线 ASR/TTS，设置 asr.mode=offline / tts.mode=offline，
     并将模型文件放入 models/asr/ 和 models/tts/ 目录

唤醒词：默认"小然"，可在 cfg.yml 中修改 wake_word 字段。
WINEOF

# Create zip.
ARCHIVE="$SCRIPT_DIR/dist/$PKG_NAME.zip"
rm -f "$ARCHIVE"
(cd "$TMP_DIR" && zip -qr "$ARCHIVE" "$PKG_NAME")
echo "Windows package: dist/$PKG_NAME.zip  ($(du -h "$ARCHIVE" | cut -f1))"

echo ""
echo "=== All done ==="
echo "  dist/${BINARY}.exe"
echo "  dist/$PKG_NAME.zip"