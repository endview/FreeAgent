param([string]$Go = 'go', [string]$OutputDirectory = '')
$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
    $OutputDirectory = Join-Path $PSScriptRoot ('../bin/portable/' + [guid]::NewGuid().ToString('N'))
}
python (Join-Path $PSScriptRoot 'release_package.py') --target windows-amd64 --go $Go --output-directory $OutputDirectory
if ($LASTEXITCODE -ne 0) { throw 'Portable build failed.' }
