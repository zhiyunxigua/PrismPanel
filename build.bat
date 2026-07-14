@echo off
setlocal

cd /d "%~dp0"

set "DIST_DIR=%~dp0dist"

where go >nul 2>nul
if errorlevel 1 (
    echo [ERROR] Go was not found in PATH.
    exit /b 1
)

call :build_target windows amd64 .exe
if errorlevel 1 exit /b 1

call :build_target linux amd64
if errorlevel 1 exit /b 1

call :build_target linux arm64
if errorlevel 1 exit /b 1

echo.
echo [OK] All targets were built in "%DIST_DIR%".
exit /b 0

:build_target
set "TARGET_OS=%~1"
set "TARGET_ARCH=%~2"
set "EXECUTABLE_SUFFIX=%~3"
set "TARGET_DIR=%DIST_DIR%\%TARGET_OS%-%TARGET_ARCH%"

echo.
echo === Building %TARGET_OS%/%TARGET_ARCH% ===

if not exist "%TARGET_DIR%\daemon" mkdir "%TARGET_DIR%\daemon"
if not exist "%TARGET_DIR%\panel" mkdir "%TARGET_DIR%\panel"
if not exist "%TARGET_DIR%\frontend" mkdir "%TARGET_DIR%\frontend"

call "%~dp0daemon\build.bat" "%TARGET_OS%" "%TARGET_ARCH%" "%TARGET_DIR%\daemon\prism-daemon%EXECUTABLE_SUFFIX%"
if errorlevel 1 exit /b 1

call "%~dp0panel\build.bat" "%TARGET_OS%" "%TARGET_ARCH%" "%TARGET_DIR%\panel\prism-panel%EXECUTABLE_SUFFIX%"
if errorlevel 1 exit /b 1

xcopy "%~dp0frontend\*" "%TARGET_DIR%\frontend\" /E /I /Y /Q >nul
if errorlevel 1 (
    echo [ERROR] Failed to copy frontend files for %TARGET_OS%/%TARGET_ARCH%.
    exit /b 1
)

echo [OK] %TARGET_OS%/%TARGET_ARCH%
exit /b 0
