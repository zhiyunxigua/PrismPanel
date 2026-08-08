@echo off
setlocal

cd /d "%~dp0"

if not defined APP_VERSION set /p APP_VERSION=<"%~dp0VERSION"
if not defined APP_VERSION (
    echo [ERROR] APP_VERSION is empty.
    exit /b 1
)

if not defined DIST_DIR set "DIST_DIR=%~dp0dist"
for %%I in ("%DIST_DIR%") do set "DIST_DIR=%%~fI"

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

echo.
echo === Building frontend ===
pushd "%~dp0frontend"
call npm run build -- --configLoader runner
if errorlevel 1 (
    popd
    echo [ERROR] Frontend build failed.
    exit /b 1
)
popd
if not exist "%~dp0frontend/dist/index.html" (
    echo [ERROR] Frontend build did not produce dist/index.html.
    exit /b 1
)

call "%~dp0build-target.bat" windows amd64 .exe
if errorlevel 1 exit /b 1

call "%~dp0build-target.bat" linux amd64
if errorlevel 1 exit /b 1

call "%~dp0build-target.bat" linux arm64
if errorlevel 1 exit /b 1

echo.
echo [OK] PrismPanel %APP_VERSION% was built in "%DIST_DIR%".
exit /b 0
