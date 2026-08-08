@echo off
setlocal

cd /d "%~dp0"

if "%~1"=="" goto :usage
if "%~2"=="" goto :usage
if "%~3"=="" goto :usage

where go >nul 2>nul
if errorlevel 1 (
    echo [ERROR] Go was not found in PATH.
    exit /b 1
)

set "GOOS=%~1"
set "GOARCH=%~2"
set "CGO_ENABLED=0"
if not defined APP_VERSION set "APP_VERSION=0.0.1"

echo Building prism-panel %APP_VERSION% for %GOOS%/%GOARCH%...
go build -trimpath -ldflags "-X PrismPanel/internal/buildinfo.Version=%APP_VERSION%" -o "%~3" "./cmd/prism-panel"
if errorlevel 1 (
    echo [ERROR] prism-panel build failed for %GOOS%/%GOARCH%.
    exit /b 1
)

exit /b 0

:usage
echo Usage: build.bat ^<goos^> ^<goarch^> ^<output-file^>
exit /b 2
