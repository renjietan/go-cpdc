param(
    [string]$ToolVersion = "v1.72.0",
    [string]$PluginVersion = "v1.36.11"
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$binDir = Join-Path $projectRoot ".tools\bin"
New-Item -ItemType Directory -Force -Path $binDir | Out-Null

$previousGobIn = $env:GOBIN
$previousPath = $env:PATH
try {
    $env:GOBIN = $binDir
    $env:PATH = "$binDir;$previousPath"
    $bufExe = Join-Path $binDir "buf.exe"
    if (-not (Test-Path $bufExe)) {
        $downloadUrl = "https://github.com/bufbuild/buf/releases/download/$ToolVersion/buf-Windows-x86_64.exe"
        Invoke-WebRequest -Uri $downloadUrl -OutFile $bufExe
    }

    $pluginExe = Join-Path $binDir "protoc-gen-go.exe"
    if (-not (Test-Path $pluginExe)) {
        go install "google.golang.org/protobuf/cmd/protoc-gen-go@$PluginVersion"
    }
    & $bufExe generate $projectRoot
} finally {
    $env:GOBIN = $previousGobIn
    $env:PATH = $previousPath
}
