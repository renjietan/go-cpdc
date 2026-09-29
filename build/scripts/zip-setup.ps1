param(
    [string]$BinDirectory = "build\dist\bin",
    [string]$SetupDirectory = "build\dist\bin\cpdc_setup"
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$setupDir = Join-Path $projectRoot $SetupDirectory
$zipPath = Join-Path $projectRoot (Join-Path $BinDirectory "cpdc_setup.zip")

if (-not (Test-Path -LiteralPath $setupDir -PathType Container)) {
    throw "cpdc_setup directory not found: $setupDir"
}

# Remove a stale zip first so Compress-Archive never appends to an old one.
if (Test-Path -LiteralPath $zipPath -PathType Leaf) {
    Remove-Item -LiteralPath $zipPath -Force -ErrorAction Stop
}

$items = @(Get-ChildItem -LiteralPath $setupDir -Force)
if ($items.Count -gt 0) {
    Compress-Archive -Path $items.FullName -DestinationPath $zipPath -CompressionLevel Optimal -ErrorAction Stop
} else {
    # Empty folder: produce an empty zip (keeps the build from failing).
    Compress-Archive -Path $setupDir -DestinationPath $zipPath -CompressionLevel Optimal -ErrorAction Stop
}
Write-Host "zipped $setupDir -> $zipPath"
