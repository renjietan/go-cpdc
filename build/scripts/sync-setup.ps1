param(
    [string]$BinDirectory = "build\dist\bin",
    [string]$SetupDirectory = "build\dist\bin\cpdc_setup"
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$binDir = Join-Path $projectRoot $BinDirectory
$setupDir = Join-Path $projectRoot $SetupDirectory

if (-not (Test-Path -LiteralPath $setupDir -PathType Container)) {
    throw "cpdc_setup directory not found: $setupDir"
}

$failures = New-Object System.Collections.Generic.List[string]

# 1) Remove stale install binaries (uppercase CPDC*) from every cpdc_setup
#    subfolder, keeping cpdc_config*.json (lowercase cpdc) and ReReadJson.ini.
Get-ChildItem -LiteralPath $setupDir -Directory | ForEach-Object {
    Get-ChildItem -LiteralPath $_.FullName -File |
        Where-Object { $_.Name -clike 'CPDC*' } |
        ForEach-Object {
            $filePath = $_.FullName
            try {
                Remove-Item -LiteralPath $filePath -Force -ErrorAction Stop
                Write-Host "removed stale $filePath"
            } catch {
                $failures.Add("remove failed: $filePath : $($_.Exception.Message)")
                Write-Warning "remove failed: $filePath : $($_.Exception.Message)"
            }
        }
}

# 2) Copy fresh binaries from bin into the matching setup folder.
$mapping = @(
    @{ Dir = 'CCU';      Src = 'CPDC-CCU';               Dst = 'CPDC-CCU' },
    @{ Dir = 'IEC';      Src = 'CPDC-IEC';               Dst = 'CPDC-IEC' },
    @{ Dir = 'CCUAudio'; Src = 'CPDC-CCU-Audio';         Dst = 'CPDC-CCU-Audio' },
    @{ Dir = 'MMR200';   Src = 'CPDC-MultibandRadio';    Dst = 'CPDC-MultibandRadio' },
    @{ Dir = 'MR9360';   Src = 'CPDC-HF';                Dst = 'CPDC-HF' },
    @{ Dir = 'PMR200';   Src = 'CPDC-MultibandHandheld'; Dst = 'CPDC-MultibandHandheld' },
    @{ Dir = 'VehInter'; Src = 'CPDC-VehInte';           Dst = 'CPDC-VehInte' },
    @{ Dir = 'Windows';  Src = 'CPDC.exe';               Dst = 'CPDC.exe' }
)

foreach ($entry in $mapping) {
    $targetDir = Join-Path $setupDir $entry.Dir
    if (-not (Test-Path -LiteralPath $targetDir -PathType Container)) {
        Write-Warning "skip missing setup folder: $targetDir"
        continue
    }
    $source = Join-Path $binDir $entry.Src
    if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
        $failures.Add("missing source binary: $source")
        Write-Warning "missing source binary: $source"
        continue
    }
    $destination = Join-Path $targetDir $entry.Dst
    try {
        Copy-Item -LiteralPath $source -Destination $destination -Force -ErrorAction Stop
        Write-Host "copied $source -> $destination"
    } catch {
        $failures.Add("copy failed: $destination : $($_.Exception.Message)")
        Write-Warning "copy failed: $destination : $($_.Exception.Message)"
    }
}

if ($failures.Count -gt 0) {
    Write-Host ""
    Write-Host "sync-setup finished with $($failures.Count) problem(s):"
    $failures | ForEach-Object { Write-Host "  - $_" }
    exit 1
}

Write-Host "sync-setup completed."
