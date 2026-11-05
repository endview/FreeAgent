param([string]$Go = 'go', [string]$OutputDirectory = '', [string]$PostgresRuntime = '', [switch]$AllowDirty)
$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
    $OutputDirectory = Join-Path $PSScriptRoot ('../bin/portable/' + [guid]::NewGuid().ToString('N'))
}
$arguments = @((Join-Path $PSScriptRoot 'release_package.py'), '--target', 'windows-amd64', '--go', $Go, '--output-directory', $OutputDirectory)
if ($AllowDirty) { $arguments += '--allow-dirty' }
if ($PostgresRuntime) { $arguments += @('--postgres-runtime', $PostgresRuntime) }
python @arguments
if ($LASTEXITCODE -ne 0) { throw 'Portable build failed.' }
