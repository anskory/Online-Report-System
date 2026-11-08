@echo off
REM ============================================================
REM  Online Report System - One-Click Build
REM  Double-click this file. Real logic lives in build-all.ps1
REM  Output: .\app\windows  .\app\linux  .\app\macos
REM  NOTE: ASCII only - keep it that way (no GBK needed here)
REM ============================================================
setlocal
title Online Report System - Build
pushd "%~dp0"

powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build-all.ps1" %*
set "RC=%ERRORLEVEL%"

popd
echo.
if not "%RC%"=="0" (
    echo [FAILED] build exited with code %RC%  -- see messages above
) else (
    echo [DONE] output folder: %~dp0app
)
echo.
pause
endlocal & exit /b %RC%
