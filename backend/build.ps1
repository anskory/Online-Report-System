# 构建脚本（Windows PowerShell）
# 用法：.\build.ps1            # 标准构建：纯 Go，已含 Oracle 支持（go-ora，无需 gcc/Instant Client）
#       .\build.ps1 -Oracle   # godror 模式（CGO 工具链 + Oracle Instant Client，性能更好、特性更全）
param(
    [switch]$Oracle
)

$ErrorActionPreference = "Stop"
$env:GOPROXY = "goproxy.cn,direct"

# ---------- 前端构建（Vue 3 + Vite）：产物输出到 internal/webui/dist 供 go:embed 使用 ----------
$frontend = Join-Path $PSScriptRoot "..\frontend"
if (Test-Path (Join-Path $frontend "package.json")) {
    Push-Location $frontend
    try {
        if (-not (Test-Path (Join-Path $frontend "node_modules"))) {
            Write-Output "[前端] 首次构建，安装依赖 ..."
            npm install
            if ($LASTEXITCODE -ne 0) { throw "前端依赖安装失败" }
        }
        Write-Output "[前端] vite build ..."
        npm run build
        if ($LASTEXITCODE -ne 0) { throw "前端构建失败" }
    } finally {
        Pop-Location
    }
} else {
    Write-Output "[前端] 未找到 frontend/package.json，跳过前端构建"
}

# 同步依赖（补齐 go.sum：新增依赖如纯 Go Oracle 驱动 go-ora 首次构建时需要）
go mod tidy
if ($LASTEXITCODE -ne 0) { throw "依赖同步失败（go mod tidy）" }

if ($Oracle) {
    if (-not $env:CGO_ENABLED) { $env:CGO_ENABLED = "1" }
    go build -tags oracle -o report-server.exe ./cmd/server
} else {
    $env:CGO_ENABLED = "0"
    go build -o report-server.exe ./cmd/server
}
if ($LASTEXITCODE -ne 0) { throw "构建失败" }
Write-Output "构建完成: report-server.exe"
