@echo off
setlocal

rem ============================================================
rem Sign built Windows binaries with a self-signed cert,
rem using the built-in PowerShell Set-AuthenticodeSignature.
rem   - Requires Administrator rights (to trust the certificate)
rem Usage: sign-windows.bat [path-to-exe ...]
rem   default: build\windows-amd64\winapp\PrismPanel.exe
rem            build\windows-amd64\panel\prism-panel.exe
rem            build\windows-amd64\daemon\prism-daemon.exe
rem ============================================================

cd /d "%~dp0"

rem ---------- build argument list ----------
set "ARG_COUNT=0"
if "%~1"=="" goto :use_defaults
:collect_args
if "%~1"=="" goto :args_done
set /a ARG_COUNT+=1
set "TARGET_%ARG_COUNT%=%~1"
shift
goto :collect_args
:use_defaults
set "ARG_COUNT=3"
set "TARGET_1=%~dp0build\windows-amd64\winapp\PrismPanel.exe"
set "TARGET_2=%~dp0build\windows-amd64\panel\prism-panel.exe"
set "TARGET_3=%~dp0build\windows-amd64\daemon\prism-daemon.exe"
:args_done

rem ---------- create self-signed cert if missing ----------
set "CERT_FINGERPRINT="
for /f "usebackq delims=" %%F in (`powershell -NoProfile -ExecutionPolicy Bypass -Command "(Get-ChildItem Cert:\LocalMachine\My\ | Where-Object { $_.Subject -eq 'CN=PrismPanel Dev' -and ($_.EnhancedKeyUsageList | Where-Object { $_.FriendlyName -match 'Code' }) } | Select-Object -First 1).Thumbprint"`) do set "CERT_FINGERPRINT=%%F"
if not defined CERT_FINGERPRINT (
    echo Creating self-signed code-signing certificate...
    for /f "usebackq delims=" %%F in (`powershell -NoProfile -ExecutionPolicy Bypass -Command "$c = New-SelfSignedCertificate -Type CodeSigningCert -Subject 'CN=PrismPanel Dev' -CertStoreLocation Cert:\LocalMachine\My -KeyExportPolicy Exportable; $c.Thumbprint"`) do set "CERT_FINGERPRINT=%%F"
)
if not defined CERT_FINGERPRINT (
    echo [ERROR] Failed to create certificate. Run this script as Administrator.
    exit /b 1
)
echo Using certificate thumbprint: %CERT_FINGERPRINT%

rem ---------- trust the cert (Trusted Root + Trusted Publishers) ----------
powershell -NoProfile -ExecutionPolicy Bypass -Command "try { $store = New-Object System.Security.Cryptography.X509Certificates.X509Store('Root','LocalMachine'); $store.Open('ReadWrite'); $cert = Get-Item Cert:\LocalMachine\My\%CERT_FINGERPRINT%; $store.Add($cert); $store.Close() } catch { Write-Host ('trust root failed: ' + $_.Exception.Message) }"
powershell -NoProfile -ExecutionPolicy Bypass -Command "try { $store = New-Object System.Security.Cryptography.X509Certificates.X509Store('TrustedPublisher','LocalMachine'); $store.Open('ReadWrite'); $cert = Get-Item Cert:\LocalMachine\My\%CERT_FINGERPRINT%; $store.Add($cert); $store.Close() } catch { Write-Host ('trust publisher failed: ' + $_.Exception.Message) }"

rem ---------- sign each target ----------
set "FAILED=0"
for /L %%I in (1,1,%ARG_COUNT%) do (
    call :sign_one "%%TARGET_%%I%%"
    if errorlevel 1 set "FAILED=1"
)

if "%FAILED%"=="1" (
    echo.
    echo [ERROR] One or more binaries failed to sign.
    exit /b 1
)
echo.
echo [OK] All binaries signed.
exit /b 0

:sign_one
if not exist "%~1" (
    echo [SKIP] Not found: %~1
    exit /b 0
)
powershell -NoProfile -ExecutionPolicy Bypass -Command "$cert = Get-Item Cert:\LocalMachine\My\%CERT_FINGERPRINT%; Set-AuthenticodeSignature -FilePath '%~1' -Certificate $cert -HashAlgorithm SHA256 -TimestampServer http://timestamp.digicert.com | Out-Null"
if errorlevel 1 (
    powershell -NoProfile -ExecutionPolicy Bypass -Command "$cert = Get-Item Cert:\LocalMachine\My\%CERT_FINGERPRINT%; Set-AuthenticodeSignature -FilePath '%~1' -Certificate $cert -HashAlgorithm SHA256 | Out-Null"
)
if errorlevel 1 (
    echo [ERROR] Failed to sign: %~1
    exit /b 1
)
echo [OK] Signed: %~1
exit /b 0
