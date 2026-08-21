[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$scriptPath = Join-Path $PSScriptRoot 'Test-Branding.ps1'
if (-not (Test-Path -LiteralPath $scriptPath -PathType Leaf)) {
    throw "branding gate script is missing: $scriptPath"
}

$powerShellExe = (Get-Process -Id $PID).Path
$retiredIdentity = ([char[]]@(120, 120, 98) -join '')

function New-BrandingFixture {
    $path = Join-Path ([IO.Path]::GetTempPath()) ("freeagent-branding-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $path | Out-Null
    return $path
}

function Set-ASCIIFile {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$RelativePath,
        [Parameter(Mandatory = $true)][string]$Content
    )

    $path = Join-Path $Root $RelativePath
    New-Item -ItemType Directory -Path (Split-Path -Parent $path) -Force | Out-Null
    [IO.File]::WriteAllBytes($path, [Text.Encoding]::ASCII.GetBytes($Content))
}

function Invoke-BrandingFixture {
    param([Parameter(Mandatory = $true)][string]$Root)

    $savedErrorActionPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        $output = @(& $powerShellExe -NoProfile -NonInteractive -ExecutionPolicy Bypass `
            -File $scriptPath -RepositoryRoot $Root 2>&1)
        $exitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $savedErrorActionPreference
    }
    return [pscustomobject]@{
        ExitCode = $exitCode
        Output = ($output -join [Environment]::NewLine)
    }
}

function Assert-GatePassed {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)]$Result
    )

    if ($Result.ExitCode -ne 0) {
        throw "$Name expected success, exit=$($Result.ExitCode):`n$($Result.Output)"
    }
}

function Assert-GateRejected {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)]$Result,
        [Parameter(Mandatory = $true)][string]$ExpectedPath
    )

    if ($Result.ExitCode -eq 0) {
        throw "$Name expected rejection but passed."
    }
    if ($Result.Output.IndexOf($ExpectedPath, [StringComparison]::OrdinalIgnoreCase) -lt 0) {
        throw "$Name did not report $ExpectedPath`:`n$($Result.Output)"
    }
}

$rootCacheFixture = New-BrandingFixture
try {
    Set-ASCIIFile -Root $rootCacheFixture -RelativePath 'source/main.go' -Content "package source`n"
    Set-ASCIIFile -Root $rootCacheFixture -RelativePath '.cache/go-build/00/cache-entry-d' -Content $retiredIdentity
    Assert-GatePassed -Name 'repository-root .cache/go-build is local generated state' `
        -Result (Invoke-BrandingFixture -Root $rootCacheFixture)
} finally {
    Remove-Item -LiteralPath $rootCacheFixture -Recurse -Force
}

$rootCacheSourceFixture = New-BrandingFixture
try {
    Set-ASCIIFile -Root $rootCacheSourceFixture -RelativePath '.cache/other/source.go' -Content $retiredIdentity
    Assert-GateRejected -Name 'repository-root .cache outside go-build cannot bypass source scanning' `
        -Result (Invoke-BrandingFixture -Root $rootCacheSourceFixture) `
        -ExpectedPath '.cache/other/source.go'
} finally {
    Remove-Item -LiteralPath $rootCacheSourceFixture -Recurse -Force
}

$nestedCacheFixture = New-BrandingFixture
try {
    Set-ASCIIFile -Root $nestedCacheFixture -RelativePath 'source/.cache/go-build/source.go' -Content $retiredIdentity
    Assert-GateRejected -Name 'nested .cache cannot bypass source scanning' `
        -Result (Invoke-BrandingFixture -Root $nestedCacheFixture) `
        -ExpectedPath 'source/.cache/go-build/source.go'
} finally {
    Remove-Item -LiteralPath $nestedCacheFixture -Recurse -Force
}

$ordinarySourceFixture = New-BrandingFixture
try {
    Set-ASCIIFile -Root $ordinarySourceFixture -RelativePath 'source/main.go' -Content $retiredIdentity
    Assert-GateRejected -Name 'ordinary source retains the brand boundary' `
        -Result (Invoke-BrandingFixture -Root $ordinarySourceFixture) `
        -ExpectedPath 'source/main.go'
} finally {
    Remove-Item -LiteralPath $ordinarySourceFixture -Recurse -Force
}

$legacyPHPFixture = New-BrandingFixture
try {
    Set-ASCIIFile -Root $legacyPHPFixture -RelativePath 'legacy/module.php' -Content '<?php echo 1;'
    Assert-GateRejected -Name 'legacy PHP remains rejected' `
        -Result (Invoke-BrandingFixture -Root $legacyPHPFixture) `
        -ExpectedPath 'legacy/module.php'
} finally {
    Remove-Item -LiteralPath $legacyPHPFixture -Recurse -Force
}

Write-Host 'branding gate exclusion contract passed'
