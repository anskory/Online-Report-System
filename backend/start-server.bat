@echo off
REM ============================================
REM   Online Report System - Start Script (cmd)
REM   Double-click to start the service
REM   NOTE: This file MUST be saved in ANSI/GBK
REM   encoding (no chcp, avoid cmd parse bug)
REM ============================================
title Report System
pushd "%~dp0"

set EXE=report-server.exe

if not exist "%EXE%" goto NO_EXE
if not exist "config.yaml" goto NO_CFG

echo ============================================
echo   在线报表系统正在启动...
echo   端口: 18080
echo   健康检查: http://127.0.0.1:18080/api/health
echo   默认账号: admin / admin123
echo   停止服务: 关闭本窗口 或 Ctrl+C
echo ============================================
echo.

"%EXE%"

echo.
echo [提示] 服务已退出，返回码=%errorlevel%
echo        详细日志见 server.log / server-err.log
echo.
pause
popd
exit /b 0

:NO_EXE
echo [错误] 未找到 %EXE%，请先执行编译：
echo     powershell -ExecutionPolicy Bypass -File .\setup-and-build.ps1
echo.
pause
exit /b 1

:NO_CFG
echo [错误] 未找到 config.yaml 配置文件
echo.
pause
exit /b 1
