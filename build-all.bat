@echo off
setlocal

rem ============================================================
rem PrismPanel batch build - outputs unified into ./build/
rem   build-all.bat                : windows/amd64 + linux/amd64 + linux/arm64
rem   build-all.bat windows        : only windows/amd64
rem   build-all.bat linux          : only linux/amd64 + linux/arm64
rem ============================================================

cd /d "%~dp0"

set "ONLY=%~1"
if "%ONLY%"=="" set "ONLY=all"

rem Auto-add common install paths in case PATH is not refreshed yet
if not defined GO_BIN if exist "C:\Program Files\Go\bin\go.exe" set "GO_BIN=C:\Program Files\Go\bin"
if not defined NODE_BIN if exist "C:\Program Files\nodejs\npm.cmd" set "NODE_BIN=C:\Program Files\nodejs"
if defined GO_BIN set "PATH=%GO_BIN%;%PATH%"
if defined NODE_BIN set "PATH=%NODE_BIN%;%PATH%"

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

if not defined APP_VERSION set /p APP_VERSION=<"%~dp0VERSION"
if not defined APP_VERSION (
    echo [ERROR] APP_VERSION is empty.
    exit /b 1
)

set "BUILD_DIR=%~dp0build"
for %%I in ("%BUILD_DIR%") do set "BUILD_DIR=%%~fI"
if exist "%BUILD_DIR%" rmdir /S /Q "%BUILD_DIR%"

echo.
echo === [1/4] Building frontend ===
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

set "DO_WINDOWS="
set "DO_LINUX="
if /I "%ONLY%"=="windows" set "DO_WINDOWS=1"
if /I "%ONLY%"=="linux"   set "DO_LINUX=1"
if /I "%ONLY%"=="all"     set "DO_WINDOWS=1" & set "DO_LINUX=1"

if defined DO_WINDOWS call :build_target windows amd64 .exe
if errorlevel 1 exit /b 1

if defined DO_LINUX call :build_target linux amd64
if errorlevel 1 exit /b 1
if defined DO_LINUX call :build_target linux arm64
if errorlevel 1 exit /b 1

echo.
echo [OK] PrismPanel %APP_VERSION% was built in "%BUILD_DIR%".
exit /b 0

:build_target
set "TARGET_OS=%~1"
set "TARGET_ARCH=%~2"
set "EXECUTABLE_SUFFIX=%~3"
set "TARGET_DIR=%BUILD_DIR%\%TARGET_OS%-%TARGET_ARCH%"

echo.
echo === Building %TARGET_OS%/%TARGET_ARCH% ===
mkdir "%TARGET_DIR%\daemon" 2>nul
mkdir "%TARGET_DIR%\panel"  2>nul

set "GOOS=%TARGET_OS%"
set "GOARCH=%TARGET_ARCH%"
set "CGO_ENABLED=0"

echo Building prism-daemon %APP_VERSION% for %TARGET_OS%/%TARGET_ARCH%...
pushd "%~dp0daemon"
go build -trimpath -ldflags "-X PrismPanel-daemon/internal/api.Version=%APP_VERSION%" -o "%TARGET_DIR%\daemon\prism-daemon%EXECUTABLE_SUFFIX%" "./cmd/prism-daemon"
popd
if errorlevel 1 (
    echo [ERROR] prism-daemon build failed.
    exit /b 1
)

echo Building prism-panel %APP_VERSION% for %TARGET_OS%/%TARGET_ARCH%...
pushd "%~dp0panel"
go build -trimpath -ldflags "-X PrismPanel/internal/buildinfo.Version=%APP_VERSION%" -o "%TARGET_DIR%\panel\prism-panel%EXECUTABLE_SUFFIX%" "./cmd/prism-panel"
popd
if errorlevel 1 (
    echo [ERROR] prism-panel build failed.
    exit /b 1
)

echo Copying frontend...
xcopy "%~dp0frontend\dist\*" "%TARGET_DIR%\frontend\dist\" /E /I /Y /Q >nul
if errorlevel 1 (
    echo [ERROR] Failed to copy frontend for %TARGET_OS%/%TARGET_ARCH%.
    exit /b 1
)

if /I "%TARGET_OS%"=="windows" if /I "%TARGET_ARCH%"=="amd64" (
    echo Building WinApp %APP_VERSION% for %TARGET_OS%/%TARGET_ARCH%...
    mkdir "%TARGET_DIR%\winapp" 2>nul
    call "%~dp0winapp\build.bat" "%TARGET_DIR%\winapp\PrismPanel.exe" --skip-frontend-build
    if errorlevel 1 (
        echo [ERROR] WinApp build failed.
        exit /b 1
    )
)

set "GOOS="
set "GOARCH="
set "CGO_ENABLED="
echo [OK] %TARGET_OS%/%TARGET_ARCH%
exit /b 0
