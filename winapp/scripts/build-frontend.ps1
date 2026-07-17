param(
    [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"

$winapp = Split-Path -Parent $PSScriptRoot
$root = Split-Path -Parent $winapp
$frontend = Join-Path $root "frontend"
$frontendDist = Join-Path $frontend "dist"
$assets = Join-Path $winapp "assets"

if (-not $SkipBuild) {
    Push-Location $frontend
    try {
        npm run build -- --configLoader runner
        if ($LASTEXITCODE -ne 0) {
            throw "frontend build failed with exit code $LASTEXITCODE"
        }
    } finally {
        Pop-Location
    }
}

if (-not (Test-Path -LiteralPath (Join-Path $frontendDist "index.html"))) {
    throw "frontend build artifacts are unavailable: $frontendDist"
}

Get-ChildItem -LiteralPath $assets -Force |
    Where-Object { $_.Name -ne ".gitignore" } |
    Remove-Item -Recurse -Force
Copy-Item -Path (Join-Path $frontendDist "*") -Destination $assets -Recurse -Force