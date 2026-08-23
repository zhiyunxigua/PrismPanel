@echo off
setlocal

rem ============================================================
rem PrismPanel one-click start (local testing)
rem   dev-test.bat         : build frontend, then start daemon + panel
rem   dev-test.bat dev     : use Vite dev server (hot reload) instead
rem ============================================================

cd /d "%~dp0"

set "USE_DEV=%~1"

rem Auto-add common install paths in case PATH is not refreshed yet
if not defined GO_BIN if exist "C:\Program Files\Go\bin\go.exe" set "GO_BIN=C:\Program Files\Go\bin"
if not defined NODE_BIN if exist "C:\Program Files\nodejs\npm.cmd" set "NODE_BIN=C:\Program Files\nodejs"
if defined GO_BIN set "PATH=%GO_BIN%;%PATH%"
if defined NODE_BIN set "PATH=%NODE_BIN%;%PATH%"

rem Use a China mirror when GOPROXY is not explicitly configured
if not defined GOPROXY set "GOPROXY=https://goproxy.cn,direct"

where go >nul 2>nul
if errorlevel 1 (
    echo [ERROR] Go was not found in PATH.
    exit /b 1
)

where npm >nul 2>nul
if errorlevel 1 (
    echo [ERROR] npm was not found in PATH.
    exit /b 1
)

if /i "%USE_DEV%"=="dev" goto :dev

echo.
echo === [1/2] Building frontend ===
pushd "%~dp0frontend"
call npm run build
if errorlevel 1 (
    popd
    echo [ERROR] Frontend build failed.
    exit /b 1
)
popd
if not exist "%~dp0frontend\dist\index.html" (
    echo [ERROR] Frontend build did not produce dist\index.html.
    exit /b 1
)

echo.
echo === [2/2] Starting daemon and panel ===
start "PrismPanel Daemon" cmd /k "cd /d %~dp0daemon && go run ./cmd/prism-daemon"
start "PrismPanel Panel"  cmd /k "cd /d %~dp0panel  && go run ./cmd/prism-panel"
goto :summary

:dev
echo.
echo === [1/2] Starting daemon and panel ===
start "PrismPanel Daemon" cmd /k "cd /d %~dp0daemon && go run ./cmd/prism-daemon"
start "PrismPanel Panel"  cmd /k "cd /d %~dp0panel  && go run ./cmd/prism-panel"

echo.
echo === [2/2] Starting Vite dev server ===
start "PrismPanel Frontend" cmd /k "cd /d %~dp0frontend && npm run dev"
goto :summary

:summary
echo.
echo ============================================================
echo [OK] PrismPanel started for testing.
echo.
echo   Panel (built dist):  http://127.0.0.1:8080
echo   Vite dev server:     http://127.0.0.1:5173  (dev mode, /api proxies to 8080)
echo   Daemon port:         24444  (see daemon\daemon.yaml)
echo.
echo   Node token:          cd daemon && go run ./cmd/prism-daemon -show-secret
echo                        (also stored at daemon\data\secret.json)
echo.
echo   First run:
echo     1. MySQL is optional now: if it is not reachable, the panel falls back
echo        to a local SQLite database (panel.db) automatically.
echo        To use MySQL, create the database first:
echo            CREATE DATABASE prismpanel CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
echo     2. Open the panel; the first login creates the super admin.
echo     3. Add the daemon node: URL http://127.0.0.1:24444 + the node token.
echo     4. Create a server, start it, and manage it through the panel.
echo ============================================================
endlocal
