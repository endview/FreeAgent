[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$go = (Get-Command go -CommandType Application -ErrorAction Stop | Select-Object -First 1).Source

Push-Location $root
try {
    & $go generate ./...
    if ($LASTEXITCODE -ne 0) { throw 'go generate ./... failed' }
    & git diff --exit-code -- docs/generated
    if ($LASTEXITCODE -ne 0) { throw 'generated runtime facts are stale' }
    $status = @(& git status --porcelain --untracked-files=all -- docs/generated)
    if ($LASTEXITCODE -ne 0) { throw 'git status failed' }
    if ($status.Count -ne 0) {
        throw "generated runtime facts include uncommitted files:`n$($status -join "`n")"
    }
} finally {
    Pop-Location
}

Write-Host 'generated runtime facts are current'
