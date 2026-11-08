@echo off
REM ============================================
REM   Online Report System - Build Script (cmd)
REM   Usage:
REM     build.bat          standard build - pure Go, Oracle supported via go-ora
REM                        (no gcc / Instant Client needed)
REM     build.bat oracle   godror build (CGO toolchain + Oracle Instant Client)
REM   NOTE: saved in ANSI/GBK + CRLF (cmd requirement)
REM ============================================
title Report System Build
pushd "%~dp0"

set GOEXE=.tools\go\bin\go.exe
set GOPROXY=goproxy.cn,direct

if exist "%GOEXE%" (
    set "GO=%CD%\%GOEXE%"
) else (
    where go >nul 2>&1
    if errorlevel 1 goto NO_GO
    set GO=go
)

REM ---------- frontend build (Vue 3 + Vite) -> ..\frontend -> internal\webui\dist ----------
set "FE=%~dp0..\frontend"
if not exist "%FE%\package.json" goto FE_SKIP
pushd "%FE%"
if not exist "%FE%\node_modules" (
    echo [1/3] npm install ...
    call npm install
    if errorlevel 1 goto FE_FAIL
)
echo [2/3] vite build ...
call npm run build
if errorlevel 1 goto FE_FAIL
popd
goto FE_DONE

:FE_SKIP
echo [1/3] frontend package.json not found, skip frontend build
goto FE_DONE

:FE_FAIL
echo.
echo [error] frontend build FAILED, see messages above.
popd
pause
popd
exit /b 1

:FE_DONE

REM ---- sync deps (fills go.sum entries for newly added modules such as go-ora) ----
echo [deps] go mod tidy ...
"%GO%" mod tidy

if /i "%~1"=="oracle" goto BUILD_ORACLE

:BUILD_STD
echo [1/2] standard build (CGO disabled) ...
set CGO_ENABLED=0
"%GO%" build -o report-server.exe ./cmd/server
if errorlevel 1 goto BUILD_FAIL
goto BUILD_OK

:BUILD_ORACLE
echo [1/2] build with Oracle support (CGO enabled) ...
if not defined CGO_ENABLED set CGO_ENABLED=1
"%GO%" build -tags oracle -o report-server.exe ./cmd/server
if errorlevel 1 goto BUILD_FAIL
goto BUILD_OK

:BUILD_OK
echo [2/2] done: report-server.exe
echo.
echo Next: double-click start-server.bat to run,
echo       or run setup-and-build.ps1 for full smoke test.
echo.
pause
popd
exit /b 0

:BUILD_FAIL
echo.
echo [error] build FAILED, see messages above.
echo.
pause
popd
exit /b 1

:NO_GO
echo [error] Go not found:
echo   - expected local: %CD%\.tools\go\bin\go.exe
echo   - or 'go' in system PATH
echo Run setup-and-build.ps1 first to install portable Go.
echo.
pause
popd
exit /b 1
