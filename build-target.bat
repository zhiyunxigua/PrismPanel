@echo off
setlocal

cd /d "%~dp0"

if "%~1"=="" (
    echo Usage: build-target.bat ^<goos^> ^<goarch^> [executable-suffix]
    exit /b 2
)
if "%~2"=="" (
    echo Usage: build-target.bat ^<goos^> ^<goarch^> [executable-suffix]
    exit /b 2
)
if not defined DIST_DIR (
    echo [ERROR] DIST_DIR is empty.
    exit /b 1
)

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
