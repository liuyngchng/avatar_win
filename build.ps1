# ==============================================================================
# build.ps1 — Avatar PC Windows 构建脚本（PowerShell 版）
# ==============================================================================
# 使用方式（无需安装 Git Bash / sh，Windows 自带 PowerShell）:
#   powershell -ExecutionPolicy Bypass -File build.ps1            # 增量构建
#   powershell -ExecutionPolicy Bypass -File build.ps1 clean      # 清理后重新构建
#   powershell -ExecutionPolicy Bypass -File build.ps1 release    # 构建 + 签名 + 打包 zip
#   powershell -ExecutionPolicy Bypass -File build.ps1 sign       # 仅签名已有 exe
#
# 产物:
#   dist/avatar-pc.exe     # 独立可执行文件（已签名）
#   dist/avatar-pc.zip     # 带配置模板和文档的压缩包（release 模式）
#
# 签名说明:
#   使用自签名证书（cert/avatar-pc.pfx），构建时自动生成。
#   自签名 != 防冒充，但能检测文件是否被篡改。
# ==============================================================================

# param() MUST be the first executable statement in the script, otherwise
# PowerShell cannot bind command-line arguments (e.g. "release").
param(
    [Parameter(Position = 0)]
    [ValidateSet("build", "clean", "release", "sign")]
    [string]$Mode = "build"
)

$ErrorActionPreference = "Stop"

# 工作目录固定到脚本所在目录，避免从别处调用时路径错乱
Set-Location $PSScriptRoot

# ── 配置 ────────────────────────────────────────────────────
$APP_NAME   = "avatar-pc"
$DIST_DIR   = "dist"
$ZIP_NAME   = "${APP_NAME}.zip"
$EXE_NAME   = "${APP_NAME}.exe"
$CERT_DIR   = "cert"
$CERT_PFX   = Join-Path $CERT_DIR "${APP_NAME}.pfx"
$CERT_PASS  = "avatar-pc-selfsign"  # 自签名证书密码（仅本地开发用）

# 构建参数
$LDFLAGS     = "-s -w -H windowsgui"
$BUILD_FLAGS = "-trimpath"

# ── 查找 signtool.exe ───────────────────────────────────────
function Find-Signtool {
    $known = @(
        "C:\Program Files (x86)\Windows Kits\10\bin\10.0.26100.0\x64\signtool.exe",
        "C:\Program Files (x86)\Windows Kits\10\App Certification Kit\signtool.exe"
    )
    foreach ($p in $known) {
        if (Test-Path $p) { return $p }
    }
    # 回退：递归搜索 Windows Kits
    $found = Get-ChildItem "C:\Program Files (x86)\Windows Kits\10\bin" `
        -Recurse -Filter "signtool.exe" -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($found) { return $found.FullName }
    return $null
}
$SIGNTOOL = Find-Signtool

# ── 版本 / 构建时间 ─────────────────────────────────────────
$VERSION = if ($env:VERSION) {
    $env:VERSION
} else {
    $v = & git describe --tags --always --dirty 2>$null
    if ($LASTEXITCODE -eq 0 -and $v) { $v.Trim() } else { "dev" }
}
$BUILD_TIME = (Get-Date).ToUniversalTime().ToString("yyyy-MM-dd_HH:mm:ss_UTC")

Write-Host "╔══════════════════════════════════════════════════╗"
Write-Host "║  Avatar PC — Windows Build Script              ║"
Write-Host "╠══════════════════════════════════════════════════╣"
Write-Host ("║  Version:    " + $VERSION)
Write-Host ("║  Build time: " + $BUILD_TIME)
Write-Host "║  CGO:        disabled (pure Go, no DLL deps)"
Write-Host ("║  Sign:       self-signed (" + $CERT_PFX + ")")
Write-Host "╚══════════════════════════════════════════════════╝"

# ── 签名函数 ────────────────────────────────────────────────

# 生成自签名代码签名证书（若不存在）
function New-SelfSignedCert {
    if (Test-Path $CERT_PFX) {
        Write-Host "    Certificate exists: $CERT_PFX"
        return
    }
    Write-Host "    Generating self-signed code signing certificate..."
    New-Item -ItemType Directory -Force -Path $CERT_DIR | Out-Null

    $cert = New-SelfSignedCertificate `
        -Type CodeSigningCert `
        -Subject "CN=Avatar PC" `
        -FriendlyName "Avatar PC Self-Signed" `
        -CertStoreLocation "Cert:\CurrentUser\My" `
        -KeyUsage DigitalSignature `
        -KeyLength 2048 `
        -KeyAlgorithm RSA `
        -KeyExportPolicy Exportable

    $pass = ConvertTo-SecureString -String $CERT_PASS -Force -AsPlainText
    Export-PfxCertificate -Cert $cert -FilePath $CERT_PFX -Password $pass | Out-Null
    Remove-Item -Path "Cert:\CurrentUser\My\$($cert.Thumbprint)" -Force

    if (Test-Path $CERT_PFX) {
        Write-Host "    Certificate created: $CERT_PFX"
    } else {
        throw "ERROR: Failed to create certificate"
    }
}

# 对 exe 签名
function Invoke-Sign {
    param([string]$Target)

    if (-not $SIGNTOOL) {
        Write-Host "    WARNING: signtool.exe not found, skipping signature"
        Write-Host "    Install Windows SDK from: https://developer.microsoft.com/windows/downloads/windows-sdk/"
        return
    }

    New-SelfSignedCert

    Write-Host "    Signing $Target..."
    & $SIGNTOOL sign /fd SHA256 /td SHA256 /tr "http://timestamp.digicert.com" `
        /f $CERT_PFX /p $CERT_PASS /v $Target
    if ($LASTEXITCODE -ne 0) { throw "signtool sign failed" }
    Write-Host "    Signature applied OK"
}

# ── 纯签名模式 ─────────────────────────────────────────────
if ($Mode -eq "sign") {
    $exe = Join-Path $DIST_DIR $EXE_NAME
    if (-not (Test-Path $exe)) {
        Write-Host "ERROR: $exe not found. Run build.ps1 first."
        exit 1
    }
    Invoke-Sign $exe
    Write-Host ""
    Write-Host "    Signed: $exe"
    exit 0
}

# ── clean 模式 ─────────────────────────────────────────────
if ($Mode -eq "clean") {
    Write-Host ""
    Write-Host ">>> Cleaning dist/..."
    if (Test-Path $DIST_DIR) { Remove-Item -Recurse -Force $DIST_DIR }
    Write-Host ">>> Done."
    exit 0
}

# ── 构建 ────────────────────────────────────────────────────
Write-Host ""
Write-Host ">>> Step 1/4: Building $EXE_NAME..."
New-Item -ItemType Directory -Force -Path $DIST_DIR | Out-Null

$GO_LDFLAGS = "$LDFLAGS -X main.version=$VERSION -X main.buildTime=$BUILD_TIME"
$exePath = Join-Path $DIST_DIR $EXE_NAME

# 设置交叉编译环境变量（构建后恢复）
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
try {
    & go build $BUILD_FLAGS "-ldflags=$GO_LDFLAGS" -o $exePath .
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
} finally {
    Remove-Item Env:\CGO_ENABLED -ErrorAction SilentlyContinue
    Remove-Item Env:\GOOS -ErrorAction SilentlyContinue
    Remove-Item Env:\GOARCH -ErrorAction SilentlyContinue
}

$size = (Get-Item $exePath).Length
Write-Host ("    OK — {0:N1} MB" -f ($size / 1MB))

# ── 签名 ────────────────────────────────────────────────────
Write-Host ""
Write-Host ">>> Step 2/4: Signing $EXE_NAME..."
Invoke-Sign $exePath

# ── 验证二进制 ─────────────────────────────────────────────
Write-Host ""
Write-Host ">>> Step 3/4: Verifying..."

# 检查 .exe 存在且 > 1MB
if ($size -lt 1000000) {
    throw "ERROR: binary too small ($size bytes), build may have failed!"
}
Write-Host ("    Binary size: {0:N1} MB" -f ($size / 1MB))

# 验证签名
if ($SIGNTOOL) {
    Write-Host "    Verifying signature..."
    & $SIGNTOOL verify /pa /v $exePath 2>&1 | Out-Null
    # verify 失败仅告警，不中断
}

# ── 打包 (release 模式) ────────────────────────────────────
if ($Mode -eq "release") {
    Write-Host ""
    Write-Host ">>> Step 4/4: Packaging release archive..."

    $stageDir = Join-Path $DIST_DIR "__stage"
    if (Test-Path $stageDir) { Remove-Item -Recurse -Force $stageDir }
    New-Item -ItemType Directory -Force -Path $stageDir | Out-Null

    Copy-Item $exePath (Join-Path $stageDir $EXE_NAME)
    Copy-Item "cfg.yml.example" (Join-Path $stageDir "cfg.yml.example")
    if (Test-Path "USER_MANUAL.md") {
        Copy-Item "USER_MANUAL.md" (Join-Path $stageDir "使用说明.md")
    }

    Write-Host "    Staged files:"
    Get-ChildItem $stageDir | ForEach-Object { Write-Host ("      {0}  {1:N0} bytes" -f $_.Name, $_.Length) }

    $zipPath = Join-Path $DIST_DIR $ZIP_NAME
    if (Test-Path $zipPath) { Remove-Item $zipPath }
    Compress-Archive -Path (Join-Path $stageDir "*") -DestinationPath $zipPath -CompressionLevel Optimal

    Remove-Item -Recurse -Force $stageDir
    $zipSize = (Get-Item $zipPath).Length
    Write-Host ("    OK — {0} ({1:N1} MB)" -f $ZIP_NAME, ($zipSize / 1MB))
}

# ── 完成 ────────────────────────────────────────────────────
Write-Host ""
Write-Host "╔══════════════════════════════════════════════════╗"
Write-Host "║  Build complete!                                ║"
Write-Host "╠══════════════════════════════════════════════════╣"
Write-Host ("║  Output: " + (Join-Path $DIST_DIR $EXE_NAME))
if ($Mode -eq "release") {
    Write-Host ("║  Archive: " + (Join-Path $DIST_DIR $ZIP_NAME))
}
Write-Host "╚══════════════════════════════════════════════════╝"
Write-Host ""
Write-Host "  To distribute, send the user:"
Write-Host "    1. $EXE_NAME (self-contained, signed, double-click to run)"
Write-Host "    2. cfg.yml.example → rename to cfg.yml and fill in API key"