@echo off
setlocal

cd /d "%~dp0"

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

call :build_frontend
if errorlevel 1 exit /b 1

call :build_target windows amd64 .exe
if errorlevel 1 exit /b 1

call :build_target linux amd64
if errorlevel 1 exit /b 1

call :build_target linux arm64
if errorlevel 1 exit /b 1

echo.
echo [OK] All targets were built in "%DIST_DIR%".
exit /b 0

:build_frontend
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
exit /b 0

:build_target
set "TARGET_OS=%~1"
set "TARGET_ARCH=%~2"
set "EXECUTABLE_SUFFIX=%~3"
set "TARGET_DIR=%DIST_DIR%/%TARGET_OS%-%TARGET_ARCH%"

echo.
echo === Building %TARGET_OS%/%TARGET_ARCH% ===

if not exist "%TARGET_DIR%/daemon" mkdir "%TARGET_DIR%/daemon"
if not exist "%TARGET_DIR%/panel" mkdir "%TARGET_DIR%/panel"
if exist "%TARGET_DIR%/frontend" rmdir /S /Q "%TARGET_DIR%/frontend"
mkdir "%TARGET_DIR%/frontend"

call "%~dp0daemon/build.bat" "%TARGET_OS%" "%TARGET_ARCH%" "%TARGET_DIR%/daemon/prism-daemon%EXECUTABLE_SUFFIX%"
if errorlevel 1 exit /b 1

call "%~dp0panel/build.bat" "%TARGET_OS%" "%TARGET_ARCH%" "%TARGET_DIR%/panel/prism-panel%EXECUTABLE_SUFFIX%"
if errorlevel 1 exit /b 1

xcopy "%~dp0frontend\dist\*" "%TARGET_DIR%\frontend\" /E /I /Y /Q >nul
if errorlevel 1 (
    echo [ERROR] Failed to copy frontend build artifacts for %TARGET_OS%/%TARGET_ARCH%.
    exit /b 1
)

if /I "%TARGET_OS%"=="windows" if /I "%TARGET_ARCH%"=="amd64" (
    if not exist "%TARGET_DIR%/winapp" mkdir "%TARGET_DIR%/winapp"
    call "%~dp0winapp/build.bat" "%TARGET_DIR%/winapp/PrismPanel.exe" --skip-frontend-build
    if errorlevel 1 exit /b 1
)

echo [OK] %TARGET_OS%/%TARGET_ARCH%
exit /b 0