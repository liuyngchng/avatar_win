# ==============================================================================
# build.ps1 — Avatar Desktop Windows 构建脚本（PowerShell 版）
# ==============================================================================
# 使用方式（无需安装 Git Bash / sh，Windows 自带 PowerShell）:
#   powershell -ExecutionPolicy Bypass -File build.ps1                    # 完整构建在线版 + 打包 zip
#   powershell -ExecutionPolicy Bypass -File build.ps1 -Variant offline   # 构建离线版（本地 ASR/TTS）
#   powershell -ExecutionPolicy Bypass -File build.ps1 clean              # 仅清理 dist/
#   powershell -ExecutionPolicy Bypass -File build.ps1 sign               # 仅签名已有 exe
#
# 版本说明:
#   - online  默认。使用在线 ASR/TTS API，KWS 唤醒词使用本地 sherpa-onnx 模型。
#             需要 MinGW-w64 gcc 编译。
#   - offline 使用本地 sherpa-onnx 模型（SenseVoiceSmall + Matcha-TTS），
#             无需网络。需要 MinGW-w64 gcc 编译，并额外打包 ASR/TTS 模型文件。
#
# 产物:
#   dist/avatar-desktop-x64.exe     # 可执行文件（已签名）
#   dist/avatar-desktop-x64.zip     # 发布包：exe + cfg.yml + 使用说明.md + sherpa DLL + KWS 模型
#                                   #（离线版额外含 ASR/TTS 模型）
#
# 签名说明:
#   使用自签名证书（cert/avatar-desktop-x64.pfx），构建时自动生成。
#   自签名 != 防冒充，但能检测文件是否被篡改。
# ==============================================================================

# param() MUST be the first executable statement in the script, otherwise
# PowerShell cannot bind command-line arguments (e.g. "release").
param(
    [Parameter(Position = 0)]
    [ValidateSet("build", "clean", "sign")]
    [string]$Mode = "build",

    [Parameter()]
    [ValidateSet("online", "offline")]
    [string]$Variant = "online"
)

$ErrorActionPreference = "Stop"

# 抑制 Compress-Archive 内部的 Write-Progress 输出。
# PowerShell 5.1 中 Write-Progress 经管道/格式化会触发
# IndexOutOfRangeException（索引超出数组界限），关闭进度条即可避免。
$ProgressPreference = "SilentlyContinue"

# 工作目录固定到脚本所在目录，避免从别处调用时路径错乱
Set-Location $PSScriptRoot

# ── 配置 ────────────────────────────────────────────────────
$APP_NAME   = "avatar-desktop-x64"
$DIST_DIR   = "dist"
$ZIP_NAME   = "${APP_NAME}.zip"
$EXE_NAME   = "${APP_NAME}.exe"
$CERT_DIR   = "cert"
$CERT_PFX   = Join-Path $CERT_DIR "${APP_NAME}.pfx"
$CERT_PASS  = "avatar-desktop-x64-selfsign"  # 自签名证书密码（仅本地开发用）

# 要打包进 zip 的附加文件（相对于项目根目录）
if ($Variant -eq "offline") {
    $EXTRA_FILES = @(
        @{ Src = "cfg.yml.template";       Dst = "cfg.yml" },
        @{ Src = "USER_MANUAL_OFFLINE.md"; Dst = "使用说明.md" }
    )
} else {
    $EXTRA_FILES = @(
        @{ Src = "cfg.yml.template"; Dst = "cfg.yml" },
        @{ Src = "USER_MANUAL.md";   Dst = "使用说明.md" }
    )
}

# 构建参数
$LDFLAGS = "-s -w -H windowsgui"
$GOPROXY = "https://goproxy.cn,direct"  # 国内 Go 模块代理
$env:GOPROXY = $GOPROXY                  # 对所有 go 命令生效

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

# ── 查找 MinGW-w64 x86_64 gcc ────────────────────────────────
function Find-MinGWGCC {
    # 1. 检查环境变量 CC
    if ($env:CC) {
        if (Test-Path $env:CC) { return $env:CC }
        # CC 可能只是一个名字（比如 "gcc"），尝试在 PATH 中解析
        $found = Get-Command $env:CC -ErrorAction SilentlyContinue
        if ($found) { return $found.Source }
    }

    # 2. 按优先级搜索已知路径
    $candidates = @(
        # PATH 中的 x86_64 gcc 优先
        (Get-Command x86_64-w64-mingw32-gcc.exe -ErrorAction SilentlyContinue).Source,
        # 常见安装路径
        "D:\software\MinGW-W64_x86_64-16.2.0-release-posix-seh-ucrt-rt_v14-rev1\mingw64\bin\x86_64-w64-mingw32-gcc.exe",
        "C:\mingw64\bin\x86_64-w64-mingw32-gcc.exe",
        "C:\msys64\mingw64\bin\x86_64-w64-mingw32-gcc.exe",
        # 最后回退：查找 PATH 中的 gcc 并通过 arch 验证
        (Get-Command gcc.exe -ErrorAction SilentlyContinue).Source
    )

    foreach ($c in $candidates) {
        if ($c -and (Test-Path $c)) {
            # 验证它是 x86_64 架构
            $arch = & $c -dumpmachine 2>&1
            if ($LASTEXITCODE -eq 0 -and $arch -match "x86_64|amd64") {
                return $c
            }
        }
    }
    return $null
}

# ── 查找 sherpa-onnx DLL 目录 ─────────────────────────────────
function Find-SherpaDLLDir {
    # 在 GOPATH 模块缓存中查找 sherpa-onnx-go-windows 的 DLL 目录
    $gomodcache = (go env GOMODCACHE)
    $dllDir = Join-Path $gomodcache "github.com\k2-fsa\sherpa-onnx-go-windows@v1.13.6\lib\x86_64-pc-windows-gnu"
    if (Test-Path (Join-Path $dllDir "onnxruntime.dll")) {
        return $dllDir
    }

    # 备选：手动拷贝到项目目录
    $localDir = Join-Path $PSScriptRoot "sherpa-dll"
    if (Test-Path (Join-Path $localDir "onnxruntime.dll")) {
        return $localDir
    }
    return $null
}

# ── 离线/在线版通用构建参数 ───────────────────────────────────
$GO_TAGS = ""
$SHERPA_DLL_DIR = ""

# KWS 唤醒词无论在在线还是离线模式都使用本地 sherpa-onnx 模型，
# 因此 CGO 必须始终开启。CC 需要指向 MinGW-w64 x86_64 gcc。
$BUILD_CC = Find-MinGWGCC
if (-not $BUILD_CC) {
    Write-Host ""
    Write-Host "ERROR: 编译需要 MinGW-w64 x86_64 gcc（KWS 唤醒词依赖 sherpa-onnx）"
    Write-Host "  下载地址: https://github.com/nixman/mingw-builds-binaries/releases"
    Write-Host "  下载 x86_64-*-release-posix-seh-ucrt-*.7z，解压后将 bin 目录加入 PATH"
    Write-Host "  或者指定 CC 环境变量: `$env:CC = 'D:\path\to\mingw64\bin\x86_64-w64-mingw32-gcc.exe'"
    exit 1
}
$env:CC = $BUILD_CC
$env:CGO_ENABLED = "1"
Write-Host "    CGO enabled, CC=$BUILD_CC"

if ($Variant -eq "offline") {
    $GO_TAGS = "-tags offline"
}

# 找到 sherpa-onnx 的 DLL 目录（用于打包）
$SHERPA_DLL_DIR = Find-SherpaDLLDir
if ($SHERPA_DLL_DIR) {
    Write-Host "    Sherpa DLL dir: $SHERPA_DLL_DIR"
} else {
    Write-Host "    WARNING: sherpa-onnx DLL directory not found — zip will not bundle runtime DLLs"
}

# ── 版本 / 构建时间 ─────────────────────────────────────────
$VERSION = if ($env:VERSION) {
    $env:VERSION
} else {
    $v = & git describe --tags --always --dirty 2>$null
    if ($LASTEXITCODE -eq 0 -and $v) { $v.Trim() } else { "dev" }
}
$BUILD_TIME = (Get-Date).ToUniversalTime().ToString("yyyy-MM-dd_HH:mm:ss_UTC")

Write-Host "╔══════════════════════════════════════════════════╗"
Write-Host "║  Avatar Desktop — Windows Build Script              ║"
Write-Host "╠══════════════════════════════════════════════════╣"
Write-Host ("║  Version:    " + $VERSION)
Write-Host ("║  Build time: " + $BUILD_TIME)
Write-Host ("║  Variant:    " + $Variant + " (CGO=" + $env:CGO_ENABLED + ")")
Write-Host ("║  Sign:       self-signed (" + $CERT_PFX + ")")
Write-Host "╚══════════════════════════════════════════════════╝"

# ── 计时工具（记录各步骤耗时，方便定位慢在哪一步） ─────────────
$BuildStopwatch = [System.Diagnostics.Stopwatch]::StartNew()
$StepStopwatch  = [System.Diagnostics.Stopwatch]::StartNew()
function Get-TimeStamp { (Get-Date).ToString("HH:mm:ss") }
function Start-Step([string]$Title) {
    $StepStopwatch.Restart()
    $total = $BuildStopwatch.Elapsed.TotalSeconds
    Write-Host ""
    Write-Host ("[{0} +{1,7:N1}s] {2}" -f (Get-TimeStamp), $total, $Title)
}
function End-Step {
    $StepStopwatch.Stop()
    Write-Host ("    [{0} step {1,6:N1}s]" -f (Get-TimeStamp), $StepStopwatch.Elapsed.TotalSeconds)
}

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
        -Subject "CN=Avatar Desktop" `
        -FriendlyName "Avatar Desktop Self-Signed" `
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
    if (Test-Path $DIST_DIR) {
        try {
            Remove-Item -Recurse -Force $DIST_DIR -ErrorAction Stop
        } catch [UnauthorizedAccessException] {
            Write-Host "    ERROR: Cannot delete files in dist/ — file may be in use."
            Write-Host "    Make sure $EXE_NAME is not running and try again."
            exit 1
        }
    }
    Write-Host ">>> Done."
    exit 0
}

# ══════════════════════════════════════════════════════════════
# build 模式：清理 → 构建 → 签名 → 验证 → 打包
# ══════════════════════════════════════════════════════════════

# ── Step 1/5: 清理旧产物 ────────────────────────────────────
Start-Step "Step 1/5: Cleaning old dist/..."
if (Test-Path $DIST_DIR) {
    try {
        Remove-Item -Recurse -Force $DIST_DIR -ErrorAction Stop
    } catch [UnauthorizedAccessException] {
        Write-Host "    ERROR: Cannot delete files in dist/ — file may be in use."
        Write-Host "    Make sure $EXE_NAME is not running and try again."
        exit 1
    }
}
New-Item -ItemType Directory -Force -Path $DIST_DIR | Out-Null
Write-Host "    Cleaned."
End-Step

# ── Step 2/5: 构建 exe ────────────────────────────────────────
Start-Step "Step 2/5: Building $EXE_NAME..."

$GO_LDFLAGS = "$LDFLAGS -X main.version=$VERSION -X main.buildTime=$BUILD_TIME"
$exePath = Join-Path $DIST_DIR $EXE_NAME

# 混淆 web/index.html 的内联 JS（构建前）。构建完成后务必恢复原文，
# 否则工作区的 index.html 会一直保持混淆态。
$INDEX_HTML = "web/index.html"
$INDEX_HTML_BAK = "web/index.html.orig"

if (Test-Path "scripts/obfuscate.js") {
    Copy-Item $INDEX_HTML $INDEX_HTML_BAK -Force
    # javascript-obfuscator 全局安装，require 需通过 NODE_PATH 定位
    $env:NODE_PATH = (npm root -g)
    & node scripts/obfuscate.js
    Remove-Item Env:\NODE_PATH -ErrorAction SilentlyContinue
    if ($LASTEXITCODE -ne 0) {
        Move-Item $INDEX_HTML_BAK $INDEX_HTML -Force
        Write-Host "    WARNING: JS obfuscation failed, building with unobfuscated JS"
    } else {
        Write-Host "    JS obfuscated OK"
    }
}

# 生成 exe 图标资源（任务栏/窗口图标）。go-winres 从 winres/icon.png
# 生成 .syso 对象文件，go build 会自动链接它。.syso 文件提交到 git，
# 只要 winres/icon.png 没变就直接复用，无需每次构建重新生成。
$SYSO_FILE = "rsrc_windows_amd64.syso"
$SYSO_NEEDS_REGEN = $false
if (Test-Path $SYSO_FILE) {
    Write-Host "    Icon resource already cached ($SYSO_FILE)"
} elseif (Test-Path "winres/icon.png") {
    $SYSO_NEEDS_REGEN = $true
} else {
    Write-Host "    No winres/icon.png found — building without custom icon"
}
if ($SYSO_NEEDS_REGEN) {
    $WINRES = Get-Command go-winres -ErrorAction SilentlyContinue
    if (-not $WINRES) {
        $gopathBin = Join-Path (go env GOPATH) "bin"
        if (Test-Path (Join-Path $gopathBin "go-winres.exe")) {
            $WINRES = Get-Item (Join-Path $gopathBin "go-winres.exe")
        }
    }
    if (-not $WINRES) {
        Write-Host "    Installing go-winres..."
        & go install github.com/tc-hib/go-winres@latest 2>&1 | Out-Null
        $gopathBin = Join-Path (go env GOPATH) "bin"
        if (Test-Path (Join-Path $gopathBin "go-winres.exe")) {
            $WINRES = Get-Item (Join-Path $gopathBin "go-winres.exe")
        }
    }
    if ($WINRES) {
        Write-Host "    Generating icon resource ($SYSO_FILE)..."
        & $WINRES simply --arch amd64 --manifest gui --product-name "Avatar Desktop" `
            --file-description "Desktop Avatar" --icon winres/icon.png
        if ($LASTEXITCODE -ne 0) { Write-Host "    WARNING: go-winres failed, building without icon" }
    }
}

try {
    # garble on Windows defaults to -trimpath, so we drop it from the explicit flags.
    # garble passes through -tags and -ldflags to go build.
    $env:GOTOOLCHAIN = "local"
    $env:GOGARBLE = "github.com/liuyngchng/avatar-desktop-x64"

    # Use Start-Process -NoNewWindow instead of & to prevent go from
    # spawning a cascade of console windows (go run → garble → go build →
    # compile, link, asm, etc.) on every build.
    $garbleArgs = @("run", "mvdan.cc/garble@v0.14.2", "-literals", "build")
    if ($GO_TAGS) { $garbleArgs += $GO_TAGS }
    $garbleArgs += "-ldflags=""$GO_LDFLAGS""", "-o", $exePath, "."
    $buildOut = Join-Path $env:TEMP "avatar-build-out.txt"
    $buildErr = Join-Path $env:TEMP "avatar-build-err.txt"
    $p = Start-Process -FilePath "go" -ArgumentList $garbleArgs -NoNewWindow -Wait -PassThru `
        -RedirectStandardOutput $buildOut -RedirectStandardError $buildErr
    if ($p.ExitCode -ne 0) {
        Write-Host "`n── BUILD STDERR ─────────────────────────────" -ForegroundColor Red
        if (Test-Path $buildErr) { Get-Content $buildErr | ForEach-Object { Write-Host "  $_" -ForegroundColor Red } }
        Write-Host "── BUILD STDOUT ─────────────────────────────" -ForegroundColor Yellow
        if (Test-Path $buildOut) { Get-Content $buildOut | ForEach-Object { Write-Host "  $_" -ForegroundColor Yellow } }
        Write-Host "──────────────────────────────────────────────`n" -ForegroundColor Red
        throw "go build failed"
    }
} finally {
    Remove-Item Env:\GOTOOLCHAIN -ErrorAction SilentlyContinue
    Remove-Item Env:\GOGARBLE -ErrorAction SilentlyContinue
    Remove-Item Env:\CGO_ENABLED -ErrorAction SilentlyContinue
    # 恢复 index.html 原文（混淆是构建时的临时操作）
    if (Test-Path $INDEX_HTML_BAK) {
        Move-Item $INDEX_HTML_BAK $INDEX_HTML -Force
    }
}

$size = (Get-Item $exePath).Length
Write-Host ("    OK — {0:N1} MB" -f ($size / 1MB))
End-Step

# ── Step 3/5: 签名 ──────────────────────────────────────────
Start-Step "Step 3/5: Signing $EXE_NAME..."
Invoke-Sign $exePath
End-Step

# ── Step 4/5: 验证 ──────────────────────────────────────────
Start-Step "Step 4/5: Verifying..."

# 检查 .exe 存在且 > 1MB
if ($size -lt 1000000) {
    throw "ERROR: binary too small ($size bytes), build may have failed!"
}
Write-Host ("    Binary size: {0:N1} MB" -f ($size / 1MB))

# 验证签名
if ($SIGNTOOL) {
    Write-Host "    Verifying signature..."
    # 自签名证书不在 Windows 受信任根证书列表中，signtool verify 会报
    # "terminated in a root which is not trusted" —— 这是预期行为，不是构建失败。
    $prevEAP = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        $null = & $SIGNTOOL verify /pa /v $exePath 2>&1
    } finally {
        $ErrorActionPreference = $prevEAP
    }
    Write-Host "    Verify done (self-signed cert is untrusted by Windows — expected, not a failure)"
End-Step
}

# ── Step 5/5: 打包 zip ──────────────────────────────────────
Start-Step "Step 5/5: Packaging release archive..."

# Compress-Archive 在 PowerShell 5.1 中有 Write-Progress / out-lineoutput
# 的 IndexOutOfRangeException bug，即使用 SilentlyContinue 也偶尔触发。
# 改用 .NET ZipFile 直接打包，完全绕过 PowerShell cmdlet。
# 需要先显式加载程序集（Compress-Archive 会隐式加载它，绕过 cmdlet 后就得自己加载）。
Add-Type -AssemblyName System.IO.Compression.FileSystem

# 用绝对路径 — .NET ZipFile 不走 PowerShell 的 Set-Location，直接从进程 CWD 解析相对路径。
$zipPath = Join-Path (Join-Path $PSScriptRoot $DIST_DIR) $ZIP_NAME
$tmpPackDir = Join-Path ([System.IO.Path]::GetTempPath()) "avatar-pack-$([System.IO.Path]::GetRandomFileName())"
New-Item -ItemType Directory -Force -Path $tmpPackDir | Out-Null
try {
    # 复制 exe
    Copy-Item $exePath $tmpPackDir

    # 复制额外文件
    foreach ($f in $EXTRA_FILES) {
        if (-not (Test-Path $f.Src)) {
            Write-Host "    WARNING: $($f.Src) not found, skipping"
            continue
        }
        Copy-Item $f.Src (Join-Path $tmpPackDir $f.Dst)
        Write-Host ("    Added: $($f.Dst)")
    }

    # sherpa-onnx 运行时 DLL（KWS 唤醒词依赖，所有模式都需要）
    # DLL（onnxruntime.dll 等，运行时必需）
    if ($SHERPA_DLL_DIR) {
        Get-ChildItem $SHERPA_DLL_DIR -Filter "*.dll" | ForEach-Object {
            Copy-Item $_.FullName (Join-Path $tmpPackDir $_.Name)
            Write-Host ("    Added: " + $_.Name)
        }
    } else {
        Write-Host "    WARNING: sherpa-onnx DLL not found — exe will fail to start without them"
    }

    # KWS 唤醒词模型（所有模式都需要）
    $kwsDir = "models/kws"
    if (Test-Path $kwsDir) {
        $dstKwsDir = Join-Path $tmpPackDir $kwsDir
        New-Item -ItemType Directory -Force -Path $dstKwsDir | Out-Null
        # 只打包 int8 量化模型 + 必需文本文件，跳过 fp32 模型与 test_wavs 子目录。
        # findFile 按字典序扫描，int8 版（*.int8.onnx）会优先命中，fp32 版无需分发。
        Get-ChildItem $kwsDir -File | Where-Object {
            $_.Extension -ne ".onnx" -or $_.Name -like "*.int8.onnx"
        } | ForEach-Object {
            Copy-Item $_.FullName $dstKwsDir
        }
        Write-Host ("    Added: $kwsDir/ (KWS model, int8 only)")
    } else {
        Write-Host "    WARNING: $kwsDir not found — wake word detection will fail"
    }

    # 离线版：额外打包 ASR/TTS 模型文件
    if ($Variant -eq "offline") {
        foreach ($modelDir in @("models/asr", "models/tts")) {
            if (Test-Path $modelDir) {
                $dstModelDir = Join-Path $tmpPackDir $modelDir
                New-Item -ItemType Directory -Force -Path $dstModelDir | Out-Null
                Copy-Item (Join-Path $modelDir "*") $dstModelDir -Recurse -Force
                Write-Host ("    Added: $modelDir/ (models)")
            } else {
                Write-Host "    WARNING: $modelDir not found — offline mode will fail without models"
            }
        }
    }

    # 用 .NET 创建 zip（避免 PowerShell 5.1 Compress-Archive 的 bug）
    if (Test-Path $zipPath) { Remove-Item $zipPath -Force }
    [System.IO.Compression.ZipFile]::CreateFromDirectory($tmpPackDir, $zipPath,
        [System.IO.Compression.CompressionLevel]::Optimal, $false)
} finally {
    Remove-Item -Recurse -Force $tmpPackDir -ErrorAction SilentlyContinue
}

# 列出 zip 内容
$zipSize = (Get-Item $zipPath).Length
Write-Host ("    OK — {0} ({1:N1} MB)" -f $ZIP_NAME, ($zipSize / 1MB))
Write-Host "    Contents:"
$zip = [System.IO.Compression.ZipFile]::OpenRead($zipPath)
try {
    foreach ($entry in $zip.Entries) {
        Write-Host ("      {0}  {1:N0} bytes" -f $entry.FullName, $entry.Length)
    }
} finally {
    $zip.Dispose()
}

End-Step

# ── 完成 ────────────────────────────────────────────────────
$BuildStopwatch.Stop()
Write-Host ""
Write-Host ("  Total build time: {0:N1}s" -f $BuildStopwatch.Elapsed.TotalSeconds)
Write-Host ""
Write-Host "╔══════════════════════════════════════════════════╗"
Write-Host "║  Build complete!                                ║"
Write-Host "╠══════════════════════════════════════════════════╣"
Write-Host ("║  Output:  " + (Join-Path $DIST_DIR $EXE_NAME))
Write-Host ("║  Archive: " + (Join-Path $DIST_DIR $ZIP_NAME))
Write-Host "╚══════════════════════════════════════════════════╝"
Write-Host ""
if ($Variant -eq "offline") {
    Write-Host "  To distribute, send the user:"
    Write-Host "    1. $ZIP_NAME — extract and double-click $EXE_NAME"
    Write-Host "    2. 离线版无需 API Key，模型已随包分发"
    Write-Host "    3. 若需在线对话，编辑 cfg.yml 将 provider 改为 online 并填 API Key"
} else {
    Write-Host "  To distribute, send the user:"
    Write-Host "    1. $ZIP_NAME — extract and double-click $EXE_NAME"
    Write-Host "    2. Edit cfg.yml and fill in WorkspaceId + API key"
    Write-Host "    3. 唤醒词已内置于 KWS 本地模型，无需额外配置"
}