@echo off
REM ============================================
REM   Online Report System - Build & Run (cmd)
REM   One-click: frontend build -> go build -> start
REM   Usage: double-click, or run in cmd.
REM   NOTE: keep this file ASCII-only + CRLF (cmd requirement)
REM ============================================
title Report System - Build and Run
pushd "%~dp0"

REM ---------- locate Go: portable .tools first, then PATH ----------
set "GOEXE=.tools\go\bin\go.exe"
if exist "%GOEXE%" (
    set "GO=%CD%\%GOEXE%"
) else (
    where go >nul 2>&1
    if errorlevel 1 goto NO_GO
    set "GO=go"
)
set GOPROXY=goproxy.cn,direct

REM ---------- [1/3] frontend build (Vue3 + Vite -> internal\webui\dist) ----------
set "FE=%~dp0..\frontend"
if not exist "%FE%\package.json" goto FE_SKIP
pushd "%FE%"
if not exist "node_modules" (
    echo [1/3] npm install ...
    call npm install
    if errorlevel 1 goto FE_FAIL
)
echo [1/3] vite build ...
call npm run build
if errorlevel 1 goto FE_FAIL
popd
goto FE_DONE

:FE_SKIP
echo [1/3] frontend not found (%FE%), skip frontend build
goto FE_DONE

:FE_FAIL
echo.
echo [error] frontend build FAILED, see messages above.
popd
pause
popd
exit /b 1

:FE_DONE

REM ---------- [2/3] backend build (pure Go, Oracle via go-ora) ----------
echo [2/3] go mod tidy ...
"%GO%" mod tidy
echo [2/3] building report-server.exe (CGO disabled) ...
set CGO_ENABLED=0
"%GO%" build -o report-server.exe .\cmd\server
if errorlevel 1 goto BUILD_FAIL

REM ---------- [3/3] start service (foreground, Ctrl+C to stop) ----------
if not exist "config.yaml" goto NO_CFG
echo [3/3] starting report-server.exe ...  (Ctrl+C to stop)
echo.
report-server.exe

echo.
echo [exit] service exited, code=%errorlevel%
echo        more logs: server.log / server-err.log
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

:NO_CFG
echo [error] config.yaml not found in %CD%
echo.
pause
popd
exit /b 1

:NO_GO
echo [error] Go not found:
echo   - expected local: %CD%\.tools\go\bin\go.exe
echo   - or 'go' in system PATH
echo.
pause
popd
exit /b 1
