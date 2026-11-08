<#
  build-all.ps1 — 在线报表系统 一键编译（由 一键编译.bat 调用）

  做四件事：
    1. frontend 执行 vite build，产物落到 backend/internal/webui/dist（go:embed 嵌入）
    2. 同步 Go 依赖（含纯 Go Oracle 驱动 go-ora）
    3. 交叉编译 5 个目标：
         windows/amd64  —— 带 Oracle（优先 CGO+godror，环境不全时自动降级纯 Go）
         linux/amd64、linux/arm64、darwin/amd64、darwin/arm64 —— 纯 Go，同样支持 Oracle
    4. 组装到 app\{windows,linux,macos}，附 config.yaml、data/、一键启动脚本

  用法：双击 一键编译.bat
        或 powershell -ExecutionPolicy Bypass -File .\build-all.ps1 [-SkipFrontend] [-ForceConfig] [-Targets windows,linux]

  -SkipFrontend  跳过 npm 打包（复用已有 dist）
  -ForceConfig   用 backend\config.yaml 覆盖 app 下已存在的配置
  -Targets       只编译指定平台（默认 windows,linux,macos）
#>
[CmdletBinding()]
param(
    [switch]$SkipFrontend,
    [switch]$ForceConfig,
    [string[]]$Targets = @('windows', 'linux', 'macos')
)

$ErrorActionPreference = 'Stop'

# ---------- 路径与环境 ----------
$Root     = $PSScriptRoot
$Backend  = Join-Path $Root 'backend'
$Frontend = Join-Path $Root 'frontend'
$AppRoot  = Join-Path $Root 'app'
$DistDir  = Join-Path $Backend 'internal\webui\dist'
$Tools    = Join-Path $Backend '.tools'
$StartAt  = Get-Date
$OrigPath = $env:PATH

$env:GOPROXY = 'goproxy.cn,direct'
$env:CGO_ENABLED = '0'

# ---------- 输出小工具 ----------
function Write-Step([string]$Text) { Write-Host ''; Write-Host ("=== " + $Text) -ForegroundColor Cyan }
function Write-Ok([string]$Text)   { Write-Host ("    [OK] " + $Text) -ForegroundColor Green }
function Write-Note([string]$Text) { Write-Host ("    [..] " + $Text) -ForegroundColor Yellow }
function Write-Bad([string]$Text)  { Write-Host ("    [!!] " + $Text) -ForegroundColor Red }
function Die([string]$Text) {
    Write-Bad $Text
    Write-Host ''
    Write-Host ('编译中断，耗时 ' + [math]::Round(((Get-Date) - $StartAt).TotalSeconds, 1) + ' 秒') -ForegroundColor Red
    exit 1
}
# 统一换行符：bat 必须 CRLF
function ConvertTo-Crlf([string]$Text) { return ($Text -replace "`r`n", "`n") -replace "`n", "`r`n" }
# 按指定编码落盘（bat -> GBK 936，sh -> UTF-8 无 BOM）
function Save-Text([string]$Path, [string]$Text, [int]$CodePage) {
    $enc = [System.Text.Encoding]::GetEncoding($CodePage)
    [System.IO.File]::WriteAllText($Path, $Text, $enc)
}
function Save-Utf8NoBom([string]$Path, [string]$Text) {
    [System.IO.File]::WriteAllText($Path, $Text, (New-Object System.Text.UTF8Encoding($false)))
}
function Save-Utf8Bom([string]$Path, [string]$Text) {
    [System.IO.File]::WriteAllText($Path, $Text, (New-Object System.Text.UTF8Encoding($true)))
}

Write-Host ''
Write-Host '在线报表系统 · 一键编译' -ForegroundColor White
Write-Host '前端打包进后端 → 三平台交叉编译 → 输出到 app\windows | app\linux | app\macos' -ForegroundColor DarkGray

# ============================================================
# 1/6 定位工具链
# ============================================================
Write-Step '1/6 定位工具链'

$Go = Join-Path $Tools 'go\bin\go.exe'
if (-not (Test-Path $Go)) {
    $cmd = Get-Command go -ErrorAction SilentlyContinue
    if ($cmd) { $Go = $cmd.Source }
}
if (-not (Test-Path $Go)) {
    Die '未找到 Go：请确认 backend\.tools\go\bin\go.exe 存在，或把 go 加入 PATH（可先运行 backend\setup-and-build.ps1）'
}
$GoVersion = (& $Go version)
Write-Ok ("Go  " + $GoVersion)

# npm 优先用 npm.cmd：npm 常被解析成 npm.ps1，在「禁止运行脚本」的策略下会直接报错
$NpmExe = $null
$cand = Get-Command npm.cmd -ErrorAction SilentlyContinue
if (-not $cand) { $cand = Get-Command npm -ErrorAction SilentlyContinue }
if ($cand) {
    $NpmExe = $cand.Source
    if ($NpmExe -like '*.ps1') {
        $alt = [System.IO.Path]::ChangeExtension($NpmExe, '.cmd')
        if (Test-Path $alt) { $NpmExe = $alt }
    }
}
if (-not $SkipFrontend -and -not $NpmExe) {
    Die '未找到 npm：请安装 Node.js 18+ 并加入 PATH（前端打包需要）'
}
if ($NpmExe) { Write-Ok ("npm " + (& $NpmExe -v)) }

if (-not (Test-Path $Backend)) { Die ('后端目录不存在: ' + $Backend) }

# ============================================================
# 2/6 前端打包
# ============================================================
if ($SkipFrontend) {
    Write-Step '2/6 跳过前端打包（-SkipFrontend）'
    if (-not (Test-Path (Join-Path $DistDir 'index.html'))) {
        Write-Note '前端产物不存在，后端二进制将不带界面'
    }
} else {
    Write-Step '2/6 前端打包（frontend → backend/internal/webui/dist）'
    if (-not (Test-Path (Join-Path $Frontend 'package.json'))) {
        Die ('前端目录异常，未找到 package.json: ' + $Frontend)
    }
    Push-Location $Frontend
    try {
        if (-not (Test-Path (Join-Path $Frontend 'node_modules'))) {
            Write-Note '首次编译，正在执行 npm install（几分钟，请耐心等待）...'
            & $NpmExe install --no-audit --no-fund
            if ($LASTEXITCODE -ne 0) { Die 'npm install 失败' }
        } else {
            Write-Note 'node_modules 已存在，跳过 npm install'
        }
        Write-Note 'vite build ...'
        & $NpmExe run build
        if ($LASTEXITCODE -ne 0) { Die 'npm run build 失败（详见上方报错）' }
    } finally {
        Pop-Location
    }
    if (-not (Test-Path (Join-Path $DistDir 'index.html'))) {
        Die ('前端产物缺失: ' + $DistDir + '\index.html')
    }
    $jsCount = (Get-ChildItem -Path (Join-Path $DistDir 'assets') -Filter '*.js' -ErrorAction SilentlyContinue | Measure-Object).Count
    Write-Ok ("前端产物就绪（assets 下 " + $jsCount + " 个 js）")
}

# ============================================================
# 3/6 同步 Go 依赖
# ============================================================
Write-Step '3/6 同步 Go 依赖（含纯 Go Oracle 驱动 go-ora）'
Push-Location $Backend
try {
    & $Go mod tidy
    if ($LASTEXITCODE -ne 0) {
        Write-Note 'go mod tidy 失败，关闭校验和数据库后重试...'
        $env:GOSUMDB = 'off'
        $env:GOFLAGS = '-mod=mod'
        & $Go mod tidy
        if ($LASTEXITCODE -ne 0) {
            Die '依赖同步失败：请检查网络与 GOPROXY（当前 goproxy.cn）。若离线，需先联网跑通一次 go mod tidy'
        }
        $env:GOSUMDB = ''
        $env:GOFLAGS = ''
    }
} finally {
    Pop-Location
}
Write-Ok '依赖已就绪'

# ============================================================
# 4/6 探测 Windows 版 Oracle 编译环境（CGO + Instant Client）
# ============================================================
Write-Step '4/6 探测 Windows 版 Oracle 编译环境'

# Instant Client 目录：需同时有 oci.dll 与 sdk\include\oci.h
$IcHome = $null
foreach ($cand in @((Join-Path $Tools 'ic_basic\instantclient_19_32'), $Backend)) {
    if ($IcHome) { break }
    if ($cand -and (Test-Path (Join-Path $cand 'oci.dll')) -and (Test-Path (Join-Path $cand 'sdk\include\oci.h'))) {
        $IcHome = $cand
    }
}
if (-not $IcHome -and (Test-Path $Tools)) {
    $hit = Get-ChildItem -Path $Tools -Recurse -Filter 'oci.h' -ErrorAction SilentlyContinue |
           Where-Object { $_.FullName -like '*\sdk\include\oci.h' } | Select-Object -First 1
    if ($hit) { $IcHome = Split-Path (Split-Path (Split-Path $hit.FullName)) }
}

# C 编译器（gcc / w64devkit）
$Gcc = $null
$cmd = Get-Command gcc -ErrorAction SilentlyContinue
if ($cmd) { $Gcc = $cmd.Source }
if (-not $Gcc -and (Test-Path $Tools)) {
    $g = Get-ChildItem -Path $Tools -Recurse -Filter 'gcc.exe' -ErrorAction SilentlyContinue |
         Where-Object { $_.FullName -notlike '*downloads*' } | Select-Object -First 1
    if ($g) { $Gcc = $g.FullName }
}

$WinCgo = $false
if ($Gcc -and $IcHome) {
    $WinCgo = $true
    Write-Ok ('gcc            ' + $Gcc)
    Write-Ok ('Instant Client ' + $IcHome)
    Write-Note 'Windows 版将使用 godror（CGO）编译，Oracle 支持最完整'
    if ($Root -match ' ') {
        Write-Note '注意：项目路径含空格，CGO 编译可能因参数被拆分而失败；失败会自动降级为纯 Go 版本'
    }
} else {
    if (-not $Gcc)  { Write-Note '未找到 gcc（w64devkit）' }
    if (-not $IcHome) { Write-Note '未找到 Oracle Instant Client（含 sdk\include\oci.h）' }
    Write-Note 'Windows 版自动降级为纯 Go Oracle 驱动：功能可用，无需 CGO 与客户端'
}

# ============================================================
# 5/6 交叉编译
# ============================================================
Write-Step '5/6 编译各平台二进制'

$DirWin   = Join-Path $AppRoot 'windows'
$DirLinux = Join-Path $AppRoot 'linux'
$DirMac   = Join-Path $AppRoot 'macos'
foreach ($d in @($DirWin, $DirLinux, $DirMac)) {
    New-Item -ItemType Directory -Force -Path $d | Out-Null
    # 只清理旧主程序；data/（SQLite 与导出文件）、logs/、改过的 config.yaml 一律保留
    Get-ChildItem -Path $d -Filter 'report-server*' -File -ErrorAction SilentlyContinue | ForEach-Object {
        try { [System.IO.File]::Delete($_.FullName) }
        catch { Die ('无法覆盖 ' + $_.FullName + ' —— 服务可能正在运行，请先停止后重试') }
    }
}

$Results = @()

function Invoke-Build {
    param(
        [string]$GoOs,
        [string]$GoArch,
        [string]$Tag,
        [string]$OutFile,
        [string]$Label
    )
    $env:GOOS = $GoOs
    $env:GOARCH = $GoArch
    if ($Tag) {
        $goArgs = @('build', '-trimpath', '-tags', $Tag, '-ldflags', '-s -w', '-o', $OutFile, './cmd/server')
    } else {
        $goArgs = @('build', '-trimpath', '-ldflags', '-s -w', '-o', $OutFile, './cmd/server')
    }
    Push-Location $Backend
    try {
        # $null = 丢弃 go 的 stdout，保证函数只返回布尔值（否则成功/失败判断会被输出污染）
        $null = & $Go @goArgs
        $code = $LASTEXITCODE
    } finally {
        Pop-Location
    }
    if ($code -ne 0) { return $false }
    if (-not (Test-Path $OutFile)) { return $false }
    $sizeMb = [math]::Round(((Get-Item $OutFile).Length / 1MB), 1)
    Write-Ok ($Label + '  ' + $sizeMb + ' MB')
    return $true
}

$doWin   = $Targets -contains 'windows'
$doLinux = $Targets -contains 'linux'
$doMac   = $Targets -contains 'macos'

# ---- Windows / amd64 ----
if ($doWin) {
    $out = Join-Path $DirWin 'report-server.exe'
    $ok = $false
    if ($WinCgo) {
        $env:CGO_ENABLED  = '1'
        $env:CC           = $Gcc
        $env:CGO_CFLAGS   = '-I' + (Join-Path $IcHome 'sdk\include')
        $env:CGO_LDFLAGS  = '-L' + $IcHome + ' -loci'
        $env:PATH         = (Split-Path $Gcc) + ';' + $IcHome + ';' + $OrigPath
        $ok = Invoke-Build -GoOs 'windows' -GoArch 'amd64' -Tag 'oracle' -OutFile $out -Label 'windows/amd64 (godror/CGO)'
        if (-not $ok) {
            Write-Note 'CGO 编译失败，回退纯 Go 版本重试...'
        }
    }
    if (-not $ok) {
        $env:CGO_ENABLED = '0'
        $env:CGO_CFLAGS  = $null
        $env:CGO_LDFLAGS = $null
        $env:PATH        = $OrigPath
        $ok = Invoke-Build -GoOs 'windows' -GoArch 'amd64' -Tag '' -OutFile $out -Label 'windows/amd64 (纯 Go)'
        $WinCgo = $false
    }
    if (-not $ok) { Die 'windows/amd64 编译失败（详见上方 Go 报错）' }

    # 仅 CGO 版本需要运行时 DLL
    if ($WinCgo) {
        foreach ($name in @('oci.dll', 'oraociei19.dll', 'oraons.dll', 'orasql19.dll')) {
            $src = Join-Path $IcHome $name
            if (Test-Path $src) { Copy-Item -LiteralPath $src -Destination (Join-Path $DirWin $name) -Force }
        }
        Write-Ok 'Oracle 运行时 DLL 已随包分发'
    }
    $Results += 'windows/amd64'
}

# ---- Linux / macOS：纯 Go，静态可执行 ----
$env:CGO_ENABLED = '0'
$env:CGO_CFLAGS  = $null
$env:CGO_LDFLAGS = $null
$env:CC          = $null
$env:PATH        = $OrigPath

if ($doLinux) {
    if (-not (Invoke-Build -GoOs 'linux' -GoArch 'amd64' -Tag '' -OutFile (Join-Path $DirLinux 'report-server-amd64') -Label 'linux/amd64   (纯 Go)')) { Die 'linux/amd64 编译失败' }
    if (-not (Invoke-Build -GoOs 'linux' -GoArch 'arm64' -Tag '' -OutFile (Join-Path $DirLinux 'report-server-arm64') -Label 'linux/arm64   (纯 Go)')) { Die 'linux/arm64 编译失败' }
    $Results += 'linux/amd64', 'linux/arm64'
}

if ($doMac) {
    if (-not (Invoke-Build -GoOs 'darwin' -GoArch 'amd64' -Tag '' -OutFile (Join-Path $DirMac 'report-server-amd64') -Label 'darwin/amd64  (纯 Go)')) { Die 'darwin/amd64 编译失败' }
    if (-not (Invoke-Build -GoOs 'darwin' -GoArch 'arm64' -Tag '' -OutFile (Join-Path $DirMac 'report-server-arm64') -Label 'darwin/arm64  (纯 Go)')) { Die 'darwin/arm64 编译失败' }
    $Results += 'darwin/amd64', 'darwin/arm64'
}

# 复位环境
$env:GOOS = ''
$env:GOARCH = ''
$env:CGO_ENABLED = '0'

# ============================================================
# 6/6 组装运行目录
# ============================================================
Write-Step '6/6 组装 app 目录（配置、数据目录、一键启动脚本）'

$CfgSrc = Join-Path $Backend 'config.yaml'
if (-not (Test-Path $CfgSrc)) {
    # 仓库只提交示例配置，首次编译时回退到 config.yaml.example
    $CfgSrc = Join-Path $Backend 'config.yaml.example'
    Write-Note '未找到 backend\config.yaml，使用 config.yaml.example（生产环境请自行替换密钥）'
}
if (-not (Test-Path $CfgSrc)) { Die ('未找到配置文件: ' + $CfgSrc) }

foreach ($d in @($DirWin, $DirLinux, $DirMac)) {
    $cfgDst = Join-Path $d 'config.yaml'
    if ((Test-Path $cfgDst) -and -not $ForceConfig) {
        Write-Note ('保留已有配置（不覆盖，需覆盖加 -ForceConfig）: ' + $cfgDst)
    } else {
        Copy-Item -LiteralPath $CfgSrc -Destination $cfgDst -Force
    }
    New-Item -ItemType Directory -Force -Path (Join-Path $d 'data') | Out-Null
}
Write-Ok 'config.yaml / data/ 已就位（data 目录不清理，历史数据自动保留）'

# ---------- Windows 启动脚本（GBK + CRLF，cmd 才能正确显示中文） ----------
$winStart = @'
@echo off
title 在线报表系统
pushd "%~dp0"
if not exist "report-server.exe" goto NOEXE
if not exist "config.yaml" goto NOCFG
if not exist "data" mkdir data
echo ============================================
echo   在线报表系统 正在启动...
echo   访问地址: http://127.0.0.1:18080
echo   默认账号: admin / admin123
echo   停止服务: 关闭本窗口 或 Ctrl+C
echo ============================================
echo.
"report-server.exe"
set RC=%errorlevel%
echo.
echo [提示] 服务已退出，退出码=%RC%
pause
popd
exit /b %RC%

:NOEXE
echo [错误] 未找到 report-server.exe，请确认文件完整
pause
popd
exit /b 1

:NOCFG
echo [错误] 未找到 config.yaml，请确认文件完整
pause
popd
exit /b 1
'@

$winStartBg = @'
@echo off
title 在线报表系统（后台）
pushd "%~dp0"
if not exist "report-server.exe" goto NOEXE
if not exist "logs" mkdir logs
start "report-server" /min cmd /c "report-server.exe >> logs\server.log 2>&1"
echo [完成] 已在后台启动（最小化窗口）
echo        访问 http://127.0.0.1:18080，默认账号 admin / admin123
echo        日志: logs\server.log
echo        停止: 双击 停止服务.bat
timeout /t 3 >nul
exit /b 0

:NOEXE
echo [错误] 未找到 report-server.exe
pause
exit /b 1
'@

$winStop = @'
@echo off
title 停止 在线报表系统
taskkill /IM report-server.exe /F >nul 2>&1
if errorlevel 1 (
    echo [提示] 未发现正在运行的 report-server.exe
) else (
    echo [完成] 已停止 report-server
)
timeout /t 2 >nul
exit /b 0
'@

Save-Text (Join-Path $DirWin '启动服务.bat')       (ConvertTo-Crlf $winStart)   936
Save-Text (Join-Path $DirWin '后台启动.bat')       (ConvertTo-Crlf $winStartBg) 936
Save-Text (Join-Path $DirWin '停止服务.bat')       (ConvertTo-Crlf $winStop)    936

# ---------- Linux / macOS 启动脚本（UTF-8 无 BOM + LF） ----------
$shStart = @'
#!/bin/sh
# 在线报表系统 - 一键启动（Linux / macOS）
#   ./start.sh       前台运行，Ctrl+C 停止（直接看日志）
#   ./start.sh -d    后台运行，日志 logs/server.log，停止用 ./stop.sh
# 提示：从 Windows 拷过来后如提示 Permission denied，先执行 chmod +x start.sh stop.sh
set -e
cd "$(dirname "$0")"

case "$(uname -m)" in
  x86_64|amd64)  BIN=./report-server-amd64 ;;
  aarch64|arm64) BIN=./report-server-arm64 ;;
  *) echo "[错误] 不支持的 CPU 架构: $(uname -m)"; exit 1 ;;
esac

[ -f "$BIN" ]        || { echo "[错误] 未找到 $BIN"; exit 1; }
[ -f config.yaml ]   || { echo "[错误] 未找到 config.yaml"; exit 1; }

mkdir -p data data/exports logs
chmod +x "$BIN" 2>/dev/null || true

if [ "$1" = "-d" ]; then
  nohup "$BIN" >> logs/server.log 2>&1 &
  echo $! > server.pid
  echo "[完成] 已后台启动 PID=$(cat server.pid)"
  echo "       访问 http://127.0.0.1:18080  默认账号 admin / admin123"
  echo "       日志 logs/server.log  停止 ./stop.sh"
else
  echo "[启动] http://127.0.0.1:18080  默认账号 admin / admin123  （Ctrl+C 停止）"
  exec "$BIN"
fi
'@

$shStop = @'
#!/bin/sh
# 在线报表系统 - 停止后台进程
cd "$(dirname "$0")"
if [ -f server.pid ]; then
  PID=$(cat server.pid)
  if kill "$PID" 2>/dev/null; then
    echo "[完成] 已停止 PID=$PID"
  else
    echo "[提示] PID=$PID 不存在或已退出"
  fi
  rm -f server.pid
else
  pkill -f report-server-amd64 >/dev/null 2>&1 || true
  pkill -f report-server-arm64 >/dev/null 2>&1 || true
  echo "[完成] 已尝试停止 report-server"
fi
'@

$shStartN = ($shStart -replace "`r`n", "`n")
$shStopN  = ($shStop  -replace "`r`n", "`n")
foreach ($d in @($DirLinux, $DirMac)) {
    Save-Utf8NoBom (Join-Path $d 'start.sh') $shStartN
    Save-Utf8NoBom (Join-Path $d 'stop.sh')  $shStopN
}
Write-Ok 'linux / macos 启动脚本已生成（start.sh / stop.sh）'

# ---------- 总说明（UTF-8 带 BOM：Windows 记事本与 Linux cat 都正常） ----------
$elapsed = [math]::Round(((Get-Date) - $StartAt).TotalSeconds, 1)
$readme = @"
在线报表系统 · 部署包
生成时间: $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')
构建耗时: $elapsed 秒
Go 版本 : $GoVersion

一、目录说明
  windows/   Windows x64（Oracle 运行时 DLL 已随包：oci.dll、oraociei19.dll 等）
  linux/     Linux amd64 + arm64（单个二进制，Oracle 走纯 Go 驱动，无外部依赖）
  macos/     macOS amd64 + arm64（同上；Apple 芯片选 arm64）
  每个目录都含: 主程序、config.yaml、data/（运行期自动写入 SQLite 与导出文件）、一键启动脚本

二、启动
  Windows : 双击 启动服务.bat（前台，看日志）；或 后台启动.bat + 停止服务.bat
  Linux   : ./start.sh       前台运行
            ./start.sh -d    后台运行（日志 logs/server.log，停止 ./stop.sh）
            macOS 首次运行如被 Gatekeeper 拦截：
              xattr -dr com.apple.quarantine .
              chmod +x start.sh stop.sh report-server-*
  访问地址: http://127.0.0.1:18080   默认账号 admin / admin123（首次登录后请改密码）
  健康检查: http://127.0.0.1:18080/api/health

三、Oracle 支持
  - 所有平台都内置 Oracle 能力，在「数据源」里选 Oracle 即可（主机/端口/服务名/账号/密码）。
  - Windows 包若由 godror(CGO) 编译：运行时依赖同目录的 oci.dll / oraociei19.dll，
    请勿删除；若缺少 VC++ 运行库，安装 Microsoft VC++ 2015-2022 Redistributable (x64)。
  - Linux / macOS 包使用纯 Go 驱动：不需要 Instant Client，也不需要装任何 Oracle 客户端。
  - 纯 Go 驱动不支持 TNS 描述符直连串；需要 TNS/RAC 场景请在 Windows 用 godror 版本，
    或直接填「主机 + 端口 + 服务名」。

四、改配置
  端口、SQLite 路径、JWT/AES 密钥、导出目录、超时等都在 config.yaml；
  生产环境务必修改 security.jwt_secret 与 security.aes_key（或用环境变量 JWT_SECRET / AES_KEY 覆盖）。

五、重新编译
  在项目根目录双击 一键编译.bat：重新打包前端 → 交叉编译 → 覆盖 app 下主程序与启动脚本。
  注意：本目录的 data/（SQLite 库与导出文件）与已存在的 config.yaml 不会被删除，
        想强制用项目里的配置覆盖，运行时加参数 -ForceConfig。
"@
Save-Utf8Bom (Join-Path $AppRoot '使用说明.txt') ($readme -replace "`r`n", "`n" -replace "`n", "`r`n")
Save-Utf8NoBom (Join-Path $AppRoot 'build-info.txt') ((@(
    'build time : ' + (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'),
    'duration   : ' + $elapsed + ' s',
    'go         : ' + $GoVersion,
    'targets    : ' + ($Results -join ', ')
) -join "`n") + "`n")

# ============================================================
# 汇总
# ============================================================
Write-Host ''
Write-Host '编译完成' -ForegroundColor Green
Write-Host ('  耗时 ' + $elapsed + ' 秒    输出 ' + $AppRoot) -ForegroundColor Gray
foreach ($d in @($DirWin, $DirLinux, $DirMac)) {
    $files = Get-ChildItem -Path $d -File -ErrorAction SilentlyContinue
    $mb = [math]::Round((($files | Measure-Object -Property Length -Sum).Sum / 1MB), 1)
    Write-Host ('  ' + (Split-Path $d -Leaf).PadRight(9) + $mb + ' MB   ' + $files.Count + ' 个文件') -ForegroundColor Gray
}
Write-Host ''
Write-Host ('  目标平台: ' + ($Results -join ', ')) -ForegroundColor Gray
Write-Host '  下一步: 进入 app\windows 双击 启动服务.bat 验证，再分发到服务器' -ForegroundColor Gray
Write-Host ''
exit 0
