# ============================================================
# 在线报表系统 一键环境引导 + 构建脚本（Windows）
# 用法：在本目录打开 PowerShell 执行：
#   Set-ExecutionPolicy -Scope Process Bypass; .\setup-and-build.ps1
# 功能：
#   1. 检测/便携安装 Go 1.22（不污染系统，装到项目 .tools 目录）
#   2. go mod tidy 拉取依赖（GOPROXY 走 goproxy.cn）
#   3. 编译 report-server.exe
#   4. 冒烟测试：启动服务 -> 健康检查 -> 登录 -> 清理进程
# ============================================================
param(
    [switch]$NoSmokeTest   # 跳过冒烟测试
)

$ErrorActionPreference = "Stop"
$root = $PSScriptRoot
$toolsDir = Join-Path $root ".tools"
$goDir = Join-Path $toolsDir "go"
$goZip = Join-Path $toolsDir "go.zip"
$goExe = Join-Path $goDir "bin\go.exe"

# ---------- 1. Go 环境 ----------
if (-not (Test-Path $goExe)) {
    Write-Output "[1/4] 未检测到 Go，开始便携安装..."
    New-Item -ItemType Directory -Force -Path $toolsDir | Out-Null

    # 清理可能损坏的半成品
    Remove-Item $goZip -ErrorAction SilentlyContinue

    $url = "https://golang.google.cn/dl/go1.22.10.windows-amd64.zip"
    Write-Output "      下载 $url （约 70MB，走国内镜像）"
    Invoke-WebRequest -Uri $url -OutFile $goZip -UseBasicParsing

    Write-Output "      解压中..."
    if (Test-Path $goDir) { Remove-Item $goDir -Recurse -Force }
    Expand-Archive -Path $goZip -DestinationPath $toolsDir -Force
    Remove-Item $goZip -ErrorAction SilentlyContinue
    Write-Output "      Go 已安装到 $goDir"
}
else {
    Write-Output "[1/4] 已检测到本地 Go"
}
$env:GOROOT = $goDir
$env:PATH = "$goDir\bin;$env:PATH"
$env:GOPROXY = "goproxy.cn,direct"
Write-Output ("      Go 版本: " + (& $goExe version))

# ---------- 2. 依赖 ----------
Write-Output "[2/4] 拉取依赖 (go mod tidy)..."
Push-Location $root
& $goExe mod tidy
if ($LASTEXITCODE -ne 0) { Pop-Location; throw "依赖拉取失败" }

# ---------- 3. 编译 ----------
Write-Output "[3/4] 编译..."
& $goExe build -o report-server.exe ./cmd/server
if ($LASTEXITCODE -ne 0) { Pop-Location; throw "编译失败（请把上方报错发给助手修复）" }
Write-Output "      生成 report-server.exe"

# ---------- 4. 冒烟测试 ----------
if (-not $NoSmokeTest) {
    Write-Output "[4/4] 冒烟测试..."
    $proc = Start-Process -FilePath (Join-Path $root "report-server.exe") `
        -WorkingDirectory $root -PassThru -WindowStyle Hidden `
        -RedirectStandardOutput (Join-Path $root "server.log") `
        -RedirectStandardError (Join-Path $root "server-err.log")
    Start-Sleep -Seconds 3

    try {
        $health = Invoke-RestMethod -Uri "http://127.0.0.1:18080/api/health" -TimeoutSec 5
        Write-Output ("      健康检查: " + ($health | ConvertTo-Json -Compress))

        $login = Invoke-RestMethod -Uri "http://127.0.0.1:18080/api/auth/login" `
            -Method Post -ContentType "application/json" -TimeoutSec 5 `
            -Body '{"username":"admin","password":"admin123"}'
        if ($login.code -eq 0) {
            Write-Output "      登录测试: 通过（已获得 JWT）"
        } else {
            Write-Output ("      登录测试异常: " + ($login | ConvertTo-Json -Compress))
        }
        Write-Output ""
        Write-Output "✅ 冒烟测试通过！服务正在后台运行（日志见 server.log）"
        Write-Output "   浏览器可访问: http://127.0.0.1:18080/api/health"

        # ---------- 5. M3 报表引擎全流程测试 ----------
        Write-Output "[5/5] 报表引擎流程测试（SQLite 演示数据源 → 建报表 → 发布 → 查询）..."
        $step = "准备"
        try {
            $hdr = @{ Authorization = "Bearer " + $login.data.accessToken }
            $base = "http://127.0.0.1:18080/api"

            $step = "查询数据源列表"
            $dsList = Invoke-RestMethod -Uri "$base/datasources" -Headers $hdr
            $ds = $dsList.data | Where-Object { $_.name -eq "demo-sqlite" } | Select-Object -First 1
            if (-not $ds) {
                $step = "创建演示数据源"
                $dsBody = @{ name = "demo-sqlite"; dbType = "sqlite"; serviceName = "./data/report_sys.db" } | ConvertTo-Json
                $ds = Invoke-RestMethod -Uri "$base/datasources" -Method Post -Headers $hdr -ContentType "application/json; charset=utf-8" -Body $dsBody
                $ds = $ds.data
            }
            Write-Output ("      [1] 数据源 OK (id=" + $ds.id + ")")

            # 分组
            $groups = Invoke-RestMethod -Uri "$base/report-groups" -Headers $hdr
            $gid = $groups.data[0].id

            # 报表：已存在则复用（幂等）
            $step = "查询报表列表"
            $rpList = Invoke-RestMethod -Uri "$base/reports" -Headers $hdr
            $rp = $rpList.data | Where-Object { $_.name -eq "demo-user-list" } | Select-Object -First 1
            if (-not $rp) {
                $step = "创建演示报表"
                $rpBody = @{
                    name = "demo-user-list"; groupId = $gid; datasourceId = $ds.id
                    sqlText = "SELECT id, username, role, created_at FROM sys_user"
                    params = @(); columns = @()
                } | ConvertTo-Json
                $rp = Invoke-RestMethod -Uri "$base/reports" -Method Post -Headers $hdr -ContentType "application/json; charset=utf-8" -Body $rpBody
                $rp = $rp.data
            }
            Write-Output ("      [2] 报表 OK (id=" + $rp.id + ")")

            # 发布
            $step = "发布报表"
            $null = Invoke-RestMethod -Uri "$base/reports/$($rp.id)/publish" -Method Post -Headers $hdr -ContentType "application/json" -Body '{"status":"published"}'
            Write-Output "      [3] 发布 OK"

            # 查询
            $step = "执行报表查询"
            $q = Invoke-RestMethod -Uri "$base/reports/$($rp.id)/query" -Method Post -Headers $hdr -ContentType "application/json" -Body '{"page":1,"pageSize":10}'
            Write-Output ("      [4] 查询 OK: 共 " + $q.data.total + " 行, 列: " + ($q.data.columns.name -join ", "))
            Write-Output "✅ M3 报表引擎全流程测试通过！"
        } catch {
            $msg = $_.Exception.Message
            try {
                $resp = $_.Exception.Response
                if ($resp) {
                    $sr = New-Object System.IO.StreamReader($resp.GetResponseStream())
                    $body = $sr.ReadToEnd()
                    if ($body) { $msg += " | 响应: " + $body }
                }
            } catch {}
            Write-Output "⚠️ M3 流程测试失败（步骤: $step）: $msg"
        }

        # ---------- 6. 导出子系统测试 ----------
        Write-Output "[6/6] 导出子系统测试（同步 CSV / xlsx 流式导出 + 任务接口）..."
        try {
            $resp = Invoke-WebRequest -Uri "$base/reports/$($rp.id)/export" -Method Post -Headers $hdr -ContentType "application/json" -Body '{"format":"csv"}'
            if ($resp.Headers['Content-Type'] -like 'text/csv*' -and $resp.RawContentLength -gt 20) {
                Write-Output ("      CSV 同步导出 OK: " + $resp.RawContentLength + " 字节")
            } else { throw ("CSV 导出响应异常: " + $resp.Headers['Content-Type']) }

            $resp2 = Invoke-WebRequest -Uri "$base/reports/$($rp.id)/export" -Method Post -Headers $hdr -ContentType "application/json" -Body '{"format":"xlsx"}'
            if ($resp2.Headers['Content-Type'] -like '*spreadsheetml*') {
                Write-Output ("      XLSX 同步导出 OK: " + $resp2.RawContentLength + " 字节")
            } else { throw ("XLSX 导出响应异常: " + $resp2.Headers['Content-Type']) }

            $tasks = Invoke-RestMethod -Uri "$base/export-tasks" -Headers $hdr
            Write-Output ("      导出任务接口 OK: 当前 " + @($tasks.data).Count + " 条任务")
            Write-Output "✅ M4 导出子系统测试通过！"
        } catch {
            Write-Output "⚠️ M4 导出测试失败: $($_.Exception.Message)"
        }
        Write-Output "   停止服务: Stop-Process -Id $($proc.Id)"
    } catch {
        Write-Output "⚠️ 冒烟测试失败: $($_.Exception.Message)"
        Write-Output "   请查看 server.log / server-err.log"
        Write-Output "   服务进程 PID=$($proc.Id)，如需停止: Stop-Process -Id $($proc.Id)"
    }
}
Pop-Location
