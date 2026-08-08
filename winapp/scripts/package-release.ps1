param(
    [Parameter(Mandatory = $true)]
    [string]$Version,
    [Parameter(Mandatory = $true)]
    [string]$ExecutablePath,
    [Parameter(Mandatory = $true)]
    [string]$OutputDirectory
)

$ErrorActionPreference = "Stop"

if ($Version -notmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') {
    throw "Version must use MAJOR.MINOR.PATCH."
}

$executable = Get-Item -LiteralPath $ExecutablePath
New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
$staging = Join-Path $OutputDirectory (".winapp-package-" + [Guid]::NewGuid().ToString("N"))
$archive = Join-Path $OutputDirectory ("PrismPanel-winapp-" + $Version + ".zip")

try {
    New-Item -ItemType Directory -Path $staging | Out-Null
    Copy-Item -LiteralPath $executable.FullName -Destination (Join-Path $staging "PrismPanel.exe")
    $manifest = [ordered]@{
        schema_version = 1
        product = "PrismPanel"
        platform = "windows"
        arch = "amd64"
        version = $Version
        file = "PrismPanel.exe"
        size = $executable.Length
        sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $executable.FullName).Hash.ToLowerInvariant()
        built_at = [DateTime]::UtcNow.ToString("o")
    }
    $manifestPath = Join-Path $staging "manifest.json"
    $manifestJSON = $manifest | ConvertTo-Json
    [IO.File]::WriteAllText($manifestPath, $manifestJSON + [Environment]::NewLine, (New-Object Text.UTF8Encoding($false)))
    if (Test-Path -LiteralPath $archive) {
        Remove-Item -LiteralPath $archive -Force
    }
    Compress-Archive -Path (Join-Path $staging "PrismPanel.exe"), $manifestPath -DestinationPath $archive -CompressionLevel Optimal
} finally {
    if (Test-Path -LiteralPath $staging) {
        Remove-Item -LiteralPath $staging -Recurse -Force
    }
}

Write-Host "[OK] WinApp release bundle: $archive"
