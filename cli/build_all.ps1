# Cross-compile seer-cli for all install.sh targets.
# Output: cli/builds/seer-<os>-<arch>[.exe]

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$Out = Join-Path $Root "builds"
New-Item -ItemType Directory -Force -Path $Out | Out-Null

$Version = if ($env:SEER_CLI_VERSION) { $env:SEER_CLI_VERSION } else { "0.2.4" }
Write-Host "Building seer-cli $Version -> $Out"

function Build-Target([string]$Os, [string]$Arch) {
    $ext = ""
    if ($Os -eq "windows") { $ext = ".exe" }
    $name = "seer-${Os}-${Arch}${ext}"
    Write-Host "  -> $name"
    $env:GOOS = $Os
    $env:GOARCH = $Arch
    $env:CGO_ENABLED = "0"
    Push-Location $Root
    try {
        go build -ldflags "-s -w" -o (Join-Path $Out $name) .
        if ($LASTEXITCODE -ne 0) { throw "build failed for $name" }
    } finally {
        Pop-Location
        Remove-Item Env:GOOS -ErrorAction SilentlyContinue
        Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
        Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
    }
}

Build-Target darwin amd64
Build-Target darwin arm64
Build-Target linux amd64
Build-Target linux arm64
Build-Target windows amd64

Copy-Item (Join-Path $Out "seer-windows-amd64.exe") (Join-Path $Root "seer.exe") -Force
Copy-Item (Join-Path $Out "seer-windows-amd64.exe") (Join-Path $Out "seer.exe") -Force

Write-Host "All binaries built:"
Get-ChildItem $Out | Format-Table Name, Length, LastWriteTime
