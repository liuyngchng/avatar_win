#!/bin/bash
# ==============================================================================
# build.sh — Avatar PC Windows 构建脚本
# ==============================================================================
# 使用方式:
#   bash build.sh          # 增量构建
#   bash build.sh clean    # 清理后重新构建
#   bash build.sh release  # 构建 + 签名 + 打包 zip 分发包
#   bash build.sh sign     # 仅对已有 exe 签名（不重新构建）
#
# 产物:
#   dist/avatar-pc.exe     # 独立可执行文件（已签名）
#   dist/avatar-pc.zip     # 带配置模板和文档的压缩包（release 模式）
#
# 签名说明:
#   使用自签名证书（cert/avatar-pc.pfx），构建时自动生成。
#   自签名 ≠ 防冒充，但能检测文件是否被篡改——
#   文件被改过，Windows 会明确提示"数字签名无效"。
#   如需真正防冒充，需购买 CA 代码签名证书（OV/EV）。
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
CERT_DIR="cert"
CERT_PFX="${CERT_DIR}/${APP_NAME}.pfx"
CERT_PASS="avatar-pc-selfsign"  # 自签名证书密码（仅本地开发用）

# 构建参数
LDFLAGS="-s -w -H windowsgui"
BUILD_FLAGS="-trimpath"

# signtool.exe — try multiple known install locations.
# We call it directly from bash (Git Bash can execute Windows binaries).
_find_signtool() {
  for p in \
    "/c/Program Files (x86)/Windows Kits/10/bin/10.0.26100.0/x64/signtool.exe" \
    "/c/Program Files (x86)/Windows Kits/10/App Certification Kit/signtool.exe" \
    ; do
    if [ -f "$p" ]; then echo "$p"; return 0; fi
  done
  # Fallback: search via PowerShell
  powershell.exe -NoProfile -Command "
    \$p = Get-ChildItem 'C:\Program Files (x86)\Windows Kits\10\bin' -Recurse -Filter 'signtool.exe' -ErrorAction SilentlyContinue |
      Select-Object -First 1 -ExpandProperty FullName
    if (\$p) { Write-Output \$p }
  " 2>/dev/null | tr -d '\r\n'
}
SIGNTOOL="$(_find_signtool)"

# 获取版本信息（如果有 git tag）
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}"
BUILD_TIME="$(date -u '+%Y-%m-%d_%H:%M:%S_UTC')"

echo "╔══════════════════════════════════════════════════╗"
echo "║  Avatar PC — Windows Build Script              ║"
echo "╠══════════════════════════════════════════════════╣"
echo "║  Version:   ${VERSION}"
echo "║  Build time: ${BUILD_TIME}"
echo "║  CGO:       disabled (pure Go, no DLL deps)"
echo "║  Sign:      self-signed (${CERT_PFX})"
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
  build|release|sign)
    ;;
  *)
    echo "Usage: bash build.sh [build|clean|release|sign]"
    echo "  build   — build the exe + sign (default)"
    echo "  clean   — remove dist/"
    echo "  release — build + sign + package zip"
    echo "  sign    — sign existing exe only (no rebuild)"
    exit 1
    ;;
esac

# ── 签名函数 ────────────────────────────────────────────────

# generate_cert creates a self-signed code signing certificate if it
# doesn't exist yet.  Uses PowerShell's New-SelfSignedCertificate
# (Windows 10+), then exports to PFX.
generate_cert() {
  if [ -f "${CERT_PFX}" ]; then
    echo "    Certificate exists: ${CERT_PFX}"
    return 0
  fi

  echo "    Generating self-signed code signing certificate..."
  mkdir -p "${CERT_DIR}"

  powershell.exe -NoProfile -Command "
    \$cert = New-SelfSignedCertificate \
      -Type CodeSigningCert \
      -Subject 'CN=Avatar PC' \
      -FriendlyName 'Avatar PC Self-Signed' \
      -CertStoreLocation 'Cert:\CurrentUser\My' \
      -KeyUsage DigitalSignature \
      -KeyLength 2048 \
      -KeyAlgorithm RSA \
      -KeyExportPolicy Exportable

    \$thumb = \$cert.Thumbprint
    \$pass = ConvertTo-SecureString -String '${CERT_PASS}' -Force -AsPlainText
    Export-PfxCertificate -Cert \$cert -FilePath '${CERT_PFX}' -Password \$pass
    Remove-Item -Path \"Cert:\CurrentUser\My\\\$thumb\" -Force
    Write-Output \$thumb
  " 2>&1 | tail -1

  if [ -f "${CERT_PFX}" ]; then
    echo "    Certificate created: ${CERT_PFX}"
  else
    echo "    ERROR: Failed to create certificate"
    return 1
  fi
}

# sign_exe signs the given .exe file with the self-signed certificate.
sign_exe() {
  local target="$1"

  if [ -z "${SIGNTOOL}" ]; then
    echo "    WARNING: signtool.exe not found, skipping signature"
    echo "    Install Windows SDK from: https://developer.microsoft.com/windows/downloads/windows-sdk/"
    return 0
  fi

  generate_cert || return 1

  echo "    Signing ${target}..."

  # signtool sign: /fd SHA256 for modern hash, /td SHA256 for RFC 3161
  # timestamp, /tr with a public timestamp server so the signature
  # remains valid after the certificate expires.
  # Note: use // for flags — Git Bash would otherwise mangle /fd into a path.
  "${SIGNTOOL}" sign \
    //fd SHA256 \
    //td SHA256 \
    //tr "http://timestamp.digicert.com" \
    //f "${CERT_PFX}" \
    //p "${CERT_PASS}" \
    //v \
    "${target}"

  echo "    Signature applied OK"
}

# ── 纯签名模式 ──────────────────────────────────────────────
if [ "${MODE}" = "sign" ]; then
  if [ ! -f "${DIST_DIR}/${EXE_NAME}" ]; then
    echo "ERROR: ${DIST_DIR}/${EXE_NAME} not found. Run 'bash build.sh' first."
    exit 1
  fi
  sign_exe "${DIST_DIR}/${EXE_NAME}"
  echo ""
  echo "    Signed: ${DIST_DIR}/${EXE_NAME}"
  exit 0
fi

# ── 构建 ────────────────────────────────────────────────────
echo ""
echo ">>> Step 1/4: Building ${EXE_NAME}..."

mkdir -p "${DIST_DIR}"

# 注入版本信息
GO_LDFLAGS="${LDFLAGS} -X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}"

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build ${BUILD_FLAGS} -ldflags="${GO_LDFLAGS}" -o "${DIST_DIR}/${EXE_NAME}" .

echo "    OK — $(ls -lh ${DIST_DIR}/${EXE_NAME} | awk '{print $5}')"

# ── 签名 ────────────────────────────────────────────────────
echo ""
echo ">>> Step 2/4: Signing ${EXE_NAME}..."
sign_exe "${DIST_DIR}/${EXE_NAME}"

# ── 验证二进制 ──────────────────────────────────────────────
echo ""
echo ">>> Step 3/4: Verifying..."

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

# 验证签名
if [ -n "${SIGNTOOL}" ]; then
  echo "    Verifying signature..."
  "${SIGNTOOL}" verify //pa //v "${DIST_DIR}/${EXE_NAME}" 2>&1 || true
fi

# ── 打包 (release 模式) ─────────────────────────────────────
if [ "${MODE}" = "release" ]; then
  echo ""
  echo ">>> Step 4/4: Packaging release archive..."

  # 将所有待分发文件放入暂存目录打包。
  # 注意：暂存目录名不能与 .exe 同名（Windows 上 rm -rf 会连带删掉 .exe）。
  STAGE_DIR="${DIST_DIR}/__stage"
  rm -rf "${STAGE_DIR}"
  mkdir -p "${STAGE_DIR}"

  cp "${DIST_DIR}/${EXE_NAME}" "${STAGE_DIR}/${EXE_NAME}"
  cp cfg.yml.example "${STAGE_DIR}/cfg.yml.example"
  if [ -f USER_MANUAL.md ]; then cp USER_MANUAL.md "${STAGE_DIR}/使用说明.md"; fi

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
echo "    1. ${EXE_NAME} (self-contained, signed, double-click to run)"
echo "    2. cfg.yml.example → rename to cfg.yml and fill in API key"
echo ""
echo "  The user does NOT need to install:"
echo "    - Go runtime"
echo "    - WebView2 runtime (built into Windows 10+)"
echo "    - Any DLL files"
echo "    - Any browser or web server"
echo ""
echo "  Signature: self-signed (${CERT_PFX})"
echo "    - Detects file corruption / tampering"
echo "    - Does NOT prevent impersonation (attacker can re-sign)"
echo "    - For real anti-tampering, buy a CA code signing certificate"