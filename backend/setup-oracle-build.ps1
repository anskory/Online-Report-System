# ============================================================
# setup-oracle-build.ps1
# One-shot setup + build for godror mode (CGO build tag "oracle").
#   - portable GCC   : w64devkit (no install, no admin) -> .tools\w64devkit
#   - Oracle Instant Client 19 (Basic + SDK)             -> .tools\ic_*
#     (19c is REQUIRED for Oracle 12c; 21c/23ai only support DB 19c+)
#   - then builds report-server.exe with -tags oracle
# NOTE: plain Oracle support needs NONE of this — pure-Go driver (go-ora)
#       is included in the standard build (build.bat / build.ps1).
#       Use this script only if you want the godror/OCI runtime.
# Usage:  powershell -ExecutionPolicy Bypass -File .\setup-oracle-build.ps1
# Re-run safe: already-downloaded components are reused.
# ============================================================
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$Root  = $PSScriptRoot
$Tools = Join-Path $Root '.tools'
$Dl    = Join-Path $Tools 'downloads'
New-Item -ItemType Directory -Force -Path $Tools, $Dl | Out-Null
$env:GOPROXY = 'goproxy.cn,direct'

function Get-File($Url, $Out, $AltUrl) {
    if (Test-Path $Out) { Write-Host "  cached: $(Split-Path $Out -Leaf)"; return }
    try { Invoke-WebRequest -Uri $Url -OutFile $Out -UseBasicParsing }
    catch {
        if (-not $AltUrl) { throw }
        Write-Host "  primary URL failed, trying mirror..."
        Invoke-WebRequest -Uri $AltUrl -OutFile $Out -UseBasicParsing
    }
}

# ---------- [1/7] locate Go ----------
$Go = Join-Path $Tools 'go\bin\go.exe'
if (-not (Test-Path $Go)) {
    $cmd = Get-Command go -ErrorAction SilentlyContinue
    if ($cmd) { $Go = $cmd.Source }
}
if (-not $Go -or -not (Test-Path $Go)) {
    Write-Host '[error] Go not found (.tools\go\bin\go.exe or PATH). Run setup-and-build.ps1 first.'
    exit 1
}
Write-Host "[1/7] Go: $Go"

# ---------- [2/7] frontend build (Vue 3 + Vite -> internal/webui/dist, go:embed) ----------
$Frontend = Join-Path $Root '..\frontend'
if (Test-Path (Join-Path $Frontend 'package.json')) {
    Write-Host '[2/7] building frontend (vite)...'
    Push-Location $Frontend
    try {
        if (-not (Test-Path (Join-Path $Frontend 'node_modules'))) { npm install }
        if ($LASTEXITCODE -ne 0) { Write-Host '[error] npm install failed.'; exit 1 }
        npm run build
        if ($LASTEXITCODE -ne 0) { Write-Host '[error] vite build failed.'; exit 1 }
    } finally { Pop-Location }
} else {
    Write-Host '[2/7] frontend/package.json not found, skip frontend build'
}

# ---------- [2b/7] sync go.sum (go mod tidy) ----------
Write-Host '[2b/7] go mod tidy ...'
& $Go mod tidy
if ($LASTEXITCODE -ne 0) { Write-Host '[error] go mod tidy failed.'; exit 1 }

# ---------- [3/7] GCC (w64devkit, portable) ----------
$Gcc = $null
$cmd = Get-Command gcc -ErrorAction SilentlyContinue
if ($cmd) { $Gcc = $cmd.Source }
if (-not $Gcc) {
    $found = Get-ChildItem -Path $Tools -Recurse -Filter 'gcc.exe' -ErrorAction SilentlyContinue |
             Select-Object -First 1
    if ($found) { $Gcc = $found.FullName }
}
if (-not $Gcc) {
    $Ver = '2.11.0'
    Write-Host "[3/7] downloading portable GCC (w64devkit $Ver, ~66 MB)..."
    $Sfx = Join-Path $Dl "w64devkit-x64-$Ver.7z.exe"
    Get-File "https://github.com/skeeto/w64devkit/releases/download/v$Ver/w64devkit-x64-$Ver.7z.exe" $Sfx `
             "https://sourceforge.net/projects/w64devkit.mirror/files/v$Ver/w64devkit-x64-$Ver.7z.exe/download"
    $Dest = Join-Path $Tools 'w64devkit'
    $cached = Get-ChildItem -Path $Dest -Recurse -Filter 'gcc.exe' -ErrorAction SilentlyContinue |
              Select-Object -First 1
    if (-not $cached) {
        Write-Host '  extracting w64devkit (self-extracting archive)...'
        $proc = Start-Process -FilePath $Sfx -ArgumentList "-y", "-o$Dest" -Wait -PassThru
        if ($proc.ExitCode -ne 0) {
            Write-Host '[error] SFX extraction failed. Extract it manually with 7-Zip into .tools\w64devkit'
            exit 1
        }
        $cached = Get-ChildItem -Path $Dest -Recurse -Filter 'gcc.exe' -ErrorAction SilentlyContinue |
                  Select-Object -First 1
    }
    if (-not $cached) { Write-Host '[error] gcc.exe not found after extraction.'; exit 1 }
    $Gcc = $cached.FullName
}
Write-Host "[3/7] GCC: $Gcc"

# ---------- [4/7] Oracle Instant Client 19 (Basic + SDK) ----------
$IC = Get-ChildItem -Path $Tools -Recurse -Filter 'oci.dll' -ErrorAction SilentlyContinue |
      Where-Object { $_.FullName -notmatch 'downloads' } | Select-Object -First 1
if (-not $IC) {
    $Ver = '19.32.0.0.0dbru'
    $Seg = '1932000'
    Write-Host "[4/7] downloading Oracle Instant Client 19.32 (Basic ~80-160 MB + SDK)..."
    $zips = @()
    foreach ($p in @('basic', 'sdk')) {
        $zip = Join-Path $Dl "instantclient-$p-windows.x64-$Ver.zip"
        Get-File "https://download.oracle.com/otn_software/nt/instantclient/$Seg/instantclient-$p-windows.x64-$Ver.zip" $zip $null
        $zips += ,@($zip, (Join-Path $Tools "ic_$p"))
    }
    $base = $null; $sdkInc = $null
    foreach ($item in $zips) {
        $zipPath = $item[0]; $dest = $item[1]
        Write-Host "  extracting $(Split-Path $zipPath -Leaf) ..."
        Expand-Archive -Path $zipPath -DestinationPath $dest -Force
        if ($zipPath -like '*basic*') {
            $base = Get-ChildItem -Path $dest -Recurse -Filter 'oci.dll' | Select-Object -First 1
        } else {
            $sdkInc = Get-ChildItem -Path $dest -Recurse -Filter 'oci.h' | Select-Object -First 1
        }
    }
    if (-not $base)  { Write-Host '[error] oci.dll not found after extracting Basic package.'; exit 1 }
    if (-not $sdkInc){ Write-Host '[error] sdk/include not found after extracting SDK package.'; exit 1 }
    # merge SDK into the instant client home so that sdk\include sits next to oci.dll
    $sdkDir = Split-Path $sdkInc.FullName           # ...\sdk\include
    $sdkRoot = Split-Path $sdkDir                    # ...\sdk
    $wantSdk = Join-Path $base.DirectoryName 'sdk'
    if (-not (Test-Path $wantSdk)) { Copy-Item $sdkRoot $wantSdk -Recurse }
    $IC = $base
}
$ICHome = $IC.DirectoryName
Write-Host "[4/7] Instant Client: $ICHome"

# ---------- [5/7] build with CGO + oracle tag ----------
Write-Host '[5/7] building report-server.exe (tags=oracle, CGO on)...'
$env:PATH = "$(Split-Path $Gcc);$ICHome;$env:PATH"
$env:CGO_ENABLED = '1'
$env:CGO_CFLAGS  = "-I$(Join-Path $ICHome 'sdk\include')"
$env:CGO_LDFLAGS = "-L$ICHome -loci"
& $Go build -tags oracle -o report-server.exe ./cmd/server
if ($LASTEXITCODE -ne 0) { Write-Host '[error] build FAILED, see messages above.'; exit 1 }

# ---------- [6/7] ship runtime DLLs next to the exe ----------
# Windows loads DLLs from the exe directory first, so copying them makes
# report-server.exe self-contained (no PATH setup needed at runtime).
Write-Host '[6/7] copying Oracle runtime DLLs next to the exe...'
foreach ($name in @('oci.dll', 'oraociei19.dll', 'oraons.dll', 'orasql19.dll')) {
    $src = Join-Path $ICHome $name
    if (Test-Path $src) { Copy-Item $src (Join-Path $Root $name) -Force }
}

Write-Host ''
Write-Host 'OK: report-server.exe built WITH Oracle support.'
Write-Host 'Note: IC 19 needs MS VC++ 2017 Redistributable (already present on most machines).'
Write-Host 'Next: start-server.bat to run, then create an Oracle datasource in the web UI.'
exit 0
