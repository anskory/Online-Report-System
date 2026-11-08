@echo off
REM ============================================
REM  在线报表系统 - 停止服务脚本 (cmd)
REM  按端口(默认18080)查找 report-server 进程并结束
REM  用法: stop-server.bat [端口]
REM ============================================
setlocal enabledelayedexpansion

set REPORT_PORT=%1
if "%REPORT_PORT%"=="" set REPORT_PORT=18080

echo [1/2] 查找监听端口 %REPORT_PORT% 的进程...

set PID_LIST=
for /f "tokens=5" %%p in ('netstat -ano ^| findstr ":%REPORT_PORT% " ^| findstr "LISTENING"') do (
    echo %%p | findstr /v "0" >nul 2>&1
    set PID_LIST=!PID_LIST! %%p
)

if "%PID_LIST%"=="" (
    echo [提示] 端口 %REPORT_PORT% 无监听进程，服务可能未运行。
    pause
    exit /b 0
)

echo [2/2] 结束进程:%PID_LIST%
for %%p in (%PID_LIST%) do (
    tasklist /fi "PID eq %%p" 2>nul | findstr /i "report-server" >nul 2>&1
    if !errorlevel! equ 0 (
        taskkill /f /pid %%p >nul 2>&1
        if !errorlevel! equ 0 (
            echo       已结束 report-server.exe (PID=%%p)
        ) else (
            echo       结束 PID=%%p 失败，请手动处理
        )
    ) else (
        echo       跳过 PID=%%p（不是 report-server 进程，请自行确认）
    )
)

echo.
echo [完成] 停止操作执行完毕。
pause
