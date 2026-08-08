@echo off
setlocal

cd /d "%~dp0"

if "%~1"=="" goto :usage

set "APP_VERSION=%~1"
set "RELEASE_DIR=%~dp0release\%APP_VERSION%"
set "DIST_DIR=%RELEASE_DIR%\dist"

powershell -NoProfile -ExecutionPolicy Bypass -Command "if ('%APP_VERSION%' -notmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') { exit 2 }"
if errorlevel 1 (
    echo [ERROR] Version must use MAJOR.MINOR.PATCH, for example 0.0.1.
    exit /b 2
)

call "%~dp0build.bat"
if errorlevel 1 exit /b 1

powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0winapp\scripts\package-release.ps1" -Version "%APP_VERSION%" -ExecutablePath "%DIST_DIR%\windows-amd64\winapp\PrismPanel.exe" -OutputDirectory "%RELEASE_DIR%"
if errorlevel 1 (
    echo [ERROR] WinApp release package failed.
    exit /b 1
)

echo.
echo [OK] Release %APP_VERSION% is ready in "%RELEASE_DIR%".
exit /b 0

:usage
echo Usage: build-release.bat ^<MAJOR.MINOR.PATCH^>
exit /b 2
