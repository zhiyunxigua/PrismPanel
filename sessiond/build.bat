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

echo Building prism-sessiond %APP_VERSION% for %GOOS%/%GOARCH%...
go build -trimpath -o "%~3" "./cmd/prism-sessiond"
if errorlevel 1 (
    echo [ERROR] prism-sessiond build failed for %GOOS%/%GOARCH%.
    exit /b 1
)

for %%I in ("%~3") do set "SESSIOND_DIR=%%~dpI"
for %%I in ("%~3") do set "SESSION_OUTPUT=%SESSIOND_DIR%prism-session%%~xI"
echo Building prism-session %APP_VERSION% for %GOOS%/%GOARCH%...
go build -trimpath -o "%SESSION_OUTPUT%" "./cmd/prism-session"
if errorlevel 1 (
    echo [ERROR] prism-session build failed for %GOOS%/%GOARCH%.
    exit /b 1
)

exit /b 0

:usage
echo Usage: build.bat ^<goos^> ^<goarch^> ^<output-file^>
exit /b 2
