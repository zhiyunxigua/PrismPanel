@echo off
setlocal

cd /d "%~dp0"

if "%~1"=="" goto :usage

where go >nul 2>nul
if errorlevel 1 (
    echo [ERROR] Go was not found in PATH.
    exit /b 1
)

where powershell >nul 2>nul
if errorlevel 1 (
    echo [ERROR] PowerShell was not found in PATH.
    exit /b 1
)

set "FRONTEND_OPTION="
if /I "%~2"=="--skip-frontend-build" set "FRONTEND_OPTION=-SkipBuild"

echo Preparing WinApp frontend assets...
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\build-frontend.ps1" %FRONTEND_OPTION%
if errorlevel 1 (
    echo [ERROR] WinApp frontend asset preparation failed.
    exit /b 1
)

for %%I in ("%~1") do if not exist "%%~dpI" mkdir "%%~dpI"

set "GOOS=windows"
set "GOARCH=amd64"
set "CGO_ENABLED=0"

echo Building PrismPanel WinApp for windows/amd64...
go build -trimpath -tags "desktop,production" -ldflags "-s -w -H windowsgui" -o "%~1" .
if errorlevel 1 (
    echo [ERROR] PrismPanel WinApp build failed.
    exit /b 1
)

echo [OK] WinApp: %~1
exit /b 0

:usage
echo Usage: build.bat ^<output-file^> [--skip-frontend-build]
exit /b 2
