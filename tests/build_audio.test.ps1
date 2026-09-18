# Verifies the dedicated CCU-Audio cross-build.
$ErrorActionPreference = "Stop"

$projectRoot = Split-Path -Parent $PSScriptRoot
$buildScript = Join-Path $projectRoot "build\scripts\build-audio.ps1"
$unifiedBuildScript = Join-Path $projectRoot "build\scripts\build.ps1"
$testOutputRoot = Join-Path $projectRoot "build\dist\bin\.build-audio-test"
$artifacts = @(
    (Join-Path $testOutputRoot "CPDC-CCU-Audio"),
    (Join-Path $testOutputRoot "CPDC-HF")
)
$goExe = Join-Path $projectRoot "build\compiler\.go1.23.12\go\bin\go.exe"

if (-not (Test-Path -LiteralPath $buildScript -PathType Leaf)) {
    throw "CCU-Audio build script is missing: $buildScript"
}
if (-not (Test-Path -LiteralPath $unifiedBuildScript -PathType Leaf)) {
    throw "Unified build script is missing: $unifiedBuildScript"
}

$unifiedBuildScriptContent = Get-Content -LiteralPath $unifiedBuildScript -Raw
if ($unifiedBuildScriptContent -match 'Build-CPDC\s+-Name\s+"CPDC-HF"') {
    throw "Unified build script must not build CPDC-HF"
}

$previousWriteTimes = @{}
foreach ($artifact in $artifacts) {
    if (Test-Path -LiteralPath $artifact -PathType Leaf) {
        $previousWriteTimes[$artifact] = (Get-Item -LiteralPath $artifact).LastWriteTimeUtc
    }
}

& $buildScript -OutputDirectory $testOutputRoot

if (-not (Test-Path -LiteralPath $goExe -PathType Leaf)) {
    throw "Go 1.23.12 executable is missing: $goExe"
}

$requiredMetadata = @(
    "go1.23.12",
    "GOOS=linux",
    "GOARCH=arm",
    "GOARM=7",
    "CGO_ENABLED=0"
)
foreach ($artifact in $artifacts) {
    if (-not (Test-Path -LiteralPath $artifact -PathType Leaf)) {
        throw "Dedicated build artifact is missing: $artifact"
    }
    $artifactInfo = Get-Item -LiteralPath $artifact
    if ($previousWriteTimes.ContainsKey($artifact) -and $artifactInfo.LastWriteTimeUtc -le $previousWriteTimes[$artifact]) {
        throw "Dedicated build artifact was not refreshed by this run: $artifact"
    }

    $metadata = (& $goExe version -m $artifact) -join "`n"
    foreach ($expected in $requiredMetadata) {
        if ($metadata -notmatch [regex]::Escape($expected)) {
            throw "Build metadata for $artifact does not contain '$expected':`n$metadata"
        }
    }
    if ($metadata -match "cpdc/internal/buildinfo.Target=ccu") {
        throw "$artifact must not enable the CCU workspace multiplier"
    }

    Write-Host "Verified $artifact"
    Write-Host $metadata
}
