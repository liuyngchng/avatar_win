#!/bin/bash
# ==============================================================================
# build.sh — Avatar PC Windows 构建脚本
# ==============================================================================
# 使用方式:
#   bash build.sh          # 增量构建
#   bash build.sh clean    # 清理后重新构建
#   bash build.sh release  # 构建并打包 zip 分发包
#
# 产物:
#   dist/avatar-pc.exe     # 独立可执行文件
#   dist/avatar-pc.zip     # 带配置模板和文档的压缩包（release 模式）
#
# 特点:
#   - CGO_ENABLED=0，纯 Go 静态链接，不依赖任何系统 DLL
#   - WebView2Loader.dll 通过 go:embed 内嵌到二进制
#   - Web 前端资源（HTML/JS/VRM 模型）通过 go:embed 内嵌
#   - -H windowsgui 隐藏控制台窗口，用户双击即开
# ==============================================================================

set -euo pipefail

# ── 配置 ────────────────────────────────────────────────────
APP_NAME="avatar-pc"
DIST_DIR="dist"
ZIP_NAME="${APP_NAME}.zip"
EXE_NAME="${APP_NAME}.exe"

# 构建参数
LDFLAGS="-s -w -H windowsgui"
BUILD_FLAGS="-trimpath"

# 获取版本信息（如果有 git tag）
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}"
BUILD_TIME="$(date -u '+%Y-%m-%d_%H:%M:%S_UTC')"

echo "╔══════════════════════════════════════════════════╗"
echo "║  Avatar PC — Windows Build Script              ║"
echo "╠══════════════════════════════════════════════════╣"
echo "║  Version:   ${VERSION}"
echo "║  Build time: ${BUILD_TIME}"
echo "║  CGO:       disabled (pure Go, no DLL deps)"
echo "╚══════════════════════════════════════════════════╝"

# ── 参数处理 ────────────────────────────────────────────────
MODE="${1:-build}"

case "${MODE}" in
  clean)
    echo ""
    echo ">>> Cleaning dist/..."
    rm -rf "${DIST_DIR}"
    echo ">>> Done."
    exit 0
    ;;
  build|release)
    ;;
  *)
    echo "Usage: bash build.sh [build|clean|release]"
    echo "  build   — build the exe only (default)"
    echo "  clean   — remove dist/"
    echo "  release — build + package zip"
    exit 1
    ;;
esac

# ── 构建 ────────────────────────────────────────────────────
echo ""
echo ">>> Step 1/3: Building ${EXE_NAME}..."

mkdir -p "${DIST_DIR}"

# 注入版本信息
GO_LDFLAGS="${LDFLAGS} -X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}"

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build ${BUILD_FLAGS} -ldflags="${GO_LDFLAGS}" -o "${DIST_DIR}/${EXE_NAME}" .

echo "    OK — $(ls -lh ${DIST_DIR}/${EXE_NAME} | awk '{print $5}')"

# ── 验证二进制 ──────────────────────────────────────────────
echo ""
echo ">>> Step 2/3: Verifying..."

# 检查是否纯静态链接（无 DLL 依赖）
if command -v objdump &>/dev/null; then
  DLL_COUNT=$(objdump -p "${DIST_DIR}/${EXE_NAME}" 2>/dev/null | grep -c "DLL Name:" || true)
  if [ "${DLL_COUNT}" -eq 0 ]; then
    echo "    Static link: YES (no DLL imports)"
  else
    echo "    Warning: found ${DLL_COUNT} DLL imports"
  fi
else
  echo "    (objdump not available, skipping DLL check)"
fi

# 检查 .exe 存在且 > 1MB
FILE_SIZE=$(stat -c%s "${DIST_DIR}/${EXE_NAME}" 2>/dev/null || echo 0)
if [ "${FILE_SIZE}" -lt 1000000 ]; then
  echo "    ERROR: binary too small (${FILE_SIZE} bytes), build may have failed!"
  exit 1
fi
echo "    Binary size: $((FILE_SIZE / 1024 / 1024)) MB"

# ── 打包 (release 模式) ─────────────────────────────────────
if [ "${MODE}" = "release" ]; then
  echo ""
  echo ">>> Step 3/3: Packaging release archive..."

  # 将所有待分发文件放入暂存目录，再打包。
  # （不能直接打包 dist/ 本身，否则 zip 会包含自己导致文件锁。）
  STAGE_DIR="${DIST_DIR}/${APP_NAME}"
  rm -rf "${STAGE_DIR}"
  mkdir -p "${STAGE_DIR}"

  cp "${DIST_DIR}/${EXE_NAME}" "${STAGE_DIR}/${EXE_NAME}"
  cp cfg.yml.example "${STAGE_DIR}/cfg.yml.example"
  if [ -f usermanual.md ]; then cp usermanual.md "${STAGE_DIR}/使用说明.md"; fi
  if [ -f README.md ]; then cp README.md "${STAGE_DIR}/README.md"; fi

  echo "    Staged files:"
  (cd "${STAGE_DIR}" && ls -lh)

  # 创建 zip（PowerShell 原生支持，Windows 下更可靠）
  if command -v powershell.exe &>/dev/null; then
    echo "    Using PowerShell to create zip..."
    powershell.exe -NoProfile -Command "
      \$stage = (Resolve-Path '${STAGE_DIR}').Path
      \$zip = Join-Path (Resolve-Path '${DIST_DIR}').Path '${ZIP_NAME}'
      if (Test-Path \$zip) { Remove-Item \$zip -Force }
      Add-Type -AssemblyName System.IO.Compression.FileSystem
      [System.IO.Compression.ZipFile]::CreateFromDirectory(\$stage, \$zip, [System.IO.Compression.CompressionLevel]::Optimal, \$false)
    "
  else
    echo "    Using tar to create zip..."
    (cd "${STAGE_DIR}" && tar -czf "${DIST_DIR}/${ZIP_NAME%.zip}.tar.gz" .)
    echo "    Created ${DIST_DIR}/${ZIP_NAME%.zip}.tar.gz"
  fi

  # 清理暂存目录
  rm -rf "${STAGE_DIR}"
  echo "    OK — ${ZIP_NAME} ($(ls -lh ${DIST_DIR}/${ZIP_NAME} 2>/dev/null | awk '{print $5}'))"
fi

# ── 完成 ────────────────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════════════════╗"
echo "║  Build complete!                                ║"
echo "╠══════════════════════════════════════════════════╣"
echo "║  Output: ${DIST_DIR}/${EXE_NAME}"
if [ "${MODE}" = "release" ]; then
  echo "║  Archive: ${DIST_DIR}/${ZIP_NAME}"
fi
echo "╚══════════════════════════════════════════════════╝"
echo ""
echo "  To distribute, send the user:"
echo "    1. ${EXE_NAME} (self-contained, double-click to run)"
echo "    2. cfg.yml.example → rename to cfg.yml and fill in API key"
echo ""
echo "  The user does NOT need to install:"
echo "    - Go runtime"
echo "    - WebView2 runtime (built into Windows 10+)"
echo "    - Any DLL files"
echo "    - Any browser or web server"