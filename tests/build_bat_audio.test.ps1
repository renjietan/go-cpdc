$ErrorActionPreference = "Stop"

$projectRoot = Split-Path -Parent $PSScriptRoot
$buildBat = Join-Path $projectRoot "build\scripts\build.bat"
$audioArtifact = Join-Path $projectRoot "build\dist\bin\CPDC-CCU-Audio"

if (-not (Test-Path -LiteralPath $audioArtifact -PathType Leaf)) {
    throw "Audio artifact must exist before testing build.bat: $audioArtifact"
}

$before = (Get-Item -LiteralPath $audioArtifact).LastWriteTimeUtc
Start-Sleep -Milliseconds 1100
cmd.exe /d /c "echo.|call `"$buildBat`""
if ($LASTEXITCODE -ne 0) {
    throw "build.bat failed with exit code $LASTEXITCODE"
}

$after = (Get-Item -LiteralPath $audioArtifact).LastWriteTimeUtc
if ($after -le $before) {
    throw "build.bat did not rebuild CPDC-CCU-Audio"
}

Write-Host "Verified build.bat rebuilt CPDC-CCU-Audio"
