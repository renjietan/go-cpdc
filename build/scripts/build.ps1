param(
    [string]$OutputDirectory = "build\dist\bin"
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$outputRoot = Join-Path $projectRoot $OutputDirectory
New-Item -ItemType Directory -Force -Path $outputRoot | Out-Null
if (-not $env:GOCACHE) { $env:GOCACHE = Join-Path $projectRoot "build\.gocache" }
New-Item -ItemType Directory -Force -Path $env:GOCACHE | Out-Null

function Build-CPDC {
    param(
        [string]$Name,
        [string]$GOOS = "linux",
        [string]$GOARCH,
        [string]$GOARM = "",
        [string]$GOARM64 = "",
        [string]$Target = "generic"
    )

    $previousGOOS = $env:GOOS
    $previousGOARCH = $env:GOARCH
    $previousGOARM = $env:GOARM
    $previousGOARM64 = $env:GOARM64
    $previousCGO = $env:CGO_ENABLED
    try {
        $env:GOOS = $GOOS
        $env:GOARCH = $GOARCH
        $env:GOARM = $GOARM
        $env:GOARM64 = $GOARM64
        $env:CGO_ENABLED = "0"
        $destination = Join-Path $outputRoot $Name
        go build -trimpath -ldflags "-s -w -X cpdc/internal/buildinfo.Target=$Target" -o $destination ./cmd/cpdc
    } finally {
        $env:GOOS = $previousGOOS
        $env:GOARCH = $previousGOARCH
        $env:GOARM = $previousGOARM
        $env:GOARM64 = $previousGOARM64
        $env:CGO_ENABLED = $previousCGO
    }
}

Push-Location $projectRoot
try {
    Build-CPDC -Name "CPDC.exe" -GOOS "windows" -GOARCH "amd64"
    Build-CPDC -Name "CPDC-CCU" -GOARCH "amd64" -Target "ccu"
    Build-CPDC -Name "CPDC-IEC" -GOARCH "arm64" -GOARM64 "v8.0" -Target "iec"
    Build-CPDC -Name "CPDC-MultibandRadio" -GOARCH "arm64" -GOARM64 "v8.0"
    Build-CPDC -Name "CPDC-MultibandHandheld" -GOARCH "arm" -GOARM "7"
    Build-CPDC -Name "CPDC-VehInte" -GOARCH "arm64"
} finally {
    Pop-Location
}
