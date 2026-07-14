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

echo Building prism-daemon for %GOOS%/%GOARCH%...
go build -trimpath -o "%~3" "./cmd/prism-daemon"
if errorlevel 1 (
    echo [ERROR] prism-daemon build failed for %GOOS%/%GOARCH%.
    exit /b 1
)

exit /b 0

:usage
echo Usage: build.bat ^<goos^> ^<goarch^> ^<output-file^>
exit /b 2
