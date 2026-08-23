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

echo.
echo === Building Prism plugins ===
set "PLUGIN_GRADLE_JAVA_HOME=%PRISM_PLUGIN_JAVA_HOME%"
if not defined PLUGIN_GRADLE_JAVA_HOME if exist "%ProgramFiles%\Java\jdk-17\bin\java.exe" set "PLUGIN_GRADLE_JAVA_HOME=%ProgramFiles%\Java\jdk-17"
if defined PLUGIN_GRADLE_JAVA_HOME if not exist "%PLUGIN_GRADLE_JAVA_HOME%\bin\java.exe" (
    echo [ERROR] Plugin Java runtime was not found in "%PLUGIN_GRADLE_JAVA_HOME%".
    exit /b 1
)

pushd "%~dp0prism-plugin"
if defined PLUGIN_GRADLE_JAVA_HOME (
    call gradlew.bat build "-Dorg.gradle.java.home=%PLUGIN_GRADLE_JAVA_HOME%"
) else (
    call gradlew.bat build
)
if errorlevel 1 (
    popd
    echo [ERROR] Prism plugin build failed.
    exit /b 1
)
popd

set "PLUGIN_RELEASE_DIR=%RELEASE_DIR%\plugin"
if not exist "%PLUGIN_RELEASE_DIR%" mkdir "%PLUGIN_RELEASE_DIR%"
if errorlevel 1 (
    echo [ERROR] Failed to create plugin release directory.
    exit /b 1
)

for %%P in (
    "spigot\build\libs\Prism-Spigot.jar"
    "bungee\build\libs\Prism-Bungee.jar"
    "velocity\build\libs\Prism-Velocity.jar"
) do (
    if not exist "%~dp0prism-plugin\%%~P" (
        echo [ERROR] Plugin build did not produce %%~nxP.
        exit /b 1
    )
    copy /Y "%~dp0prism-plugin\%%~P" "%PLUGIN_RELEASE_DIR%\%%~nxP" >nul
    if errorlevel 1 (
        echo [ERROR] Failed to copy %%~nxP to the release directory.
        exit /b 1
    )
)

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
