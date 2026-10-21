param(
    [string]$Go = 'go',
    [string]$OutputDirectory = ''
)

$ErrorActionPreference = 'Stop'
$repository = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$candidate = (Get-Content -LiteralPath (Join-Path $repository 'VERSION') -Raw).Trim()
if ($candidate -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+[A-Za-z0-9.+-]*$') {
    throw 'VERSION must identify a safe development candidate.'
}
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
    $id = (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + [guid]::NewGuid().ToString('N').Substring(0, 8)
    $OutputDirectory = Join-Path $repository "bin/portable/$id"
}
$OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)
if (Test-Path -LiteralPath $OutputDirectory) { throw 'Portable output must be a new directory.' }
$name = "FreeAgent-$candidate-windows-amd64-portable"
$staging = Join-Path $OutputDirectory 'staging'
$package = Join-Path $staging $name
New-Item -ItemType Directory -Path (Join-Path $package 'bin') -Force | Out-Null
New-Item -ItemType Directory -Path (Join-Path $package 'docs') -Force | Out-Null

$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
$previousCGO = $env:CGO_ENABLED
Push-Location -LiteralPath $repository
try {
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    $sourceBase = (& git rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Source revision could not be recorded.' }
    $sourceDirty = -not [string]::IsNullOrWhiteSpace((& git status --porcelain=v1 | Out-String))
    if ($LASTEXITCODE -ne 0) { throw 'Source status could not be recorded.' }
    # The CLI reports this version and commit; a dirty tree is marked in the
    # version's build metadata.
    $buildVersion = if ($sourceDirty) { "$candidate+dirty" } else { $candidate }
    $buildInfo = 'github.com/endview/freeagent/internal/buildinfo'
    & $Go build -trimpath -buildvcs=false -ldflags '-H=windowsgui' `
        -o (Join-Path $package 'FreeAgent.exe') ./cmd/freeagent-desktop
    if ($LASTEXITCODE -ne 0) { throw 'Portable desktop binary build failed.' }
    & $Go build -trimpath -buildvcs=false -ldflags "-X $buildInfo.Version=$buildVersion -X $buildInfo.Commit=$sourceBase" `
        -o (Join-Path $package 'bin/freeagent.exe') ./cmd/freeagent
    if ($LASTEXITCODE -ne 0) { throw 'Portable CLI binary build failed.' }
    $versionLine = & (Join-Path $package 'bin/freeagent.exe') --version
    if ($LASTEXITCODE -ne 0) { throw 'Built CLI version check failed.' }
} finally {
    Pop-Location
    $env:GOOS = $previousGOOS
    $env:GOARCH = $previousGOARCH
    $env:CGO_ENABLED = $previousCGO
}

$files = [ordered]@{
    'docs/PORTABLE.md' = 'README.md'
    'VERSION' = 'VERSION'
    'LICENSE' = 'LICENSE'
    'THIRD_PARTY_NOTICES.md' = 'THIRD_PARTY_NOTICES.md'
    'docs/CONFIGURATION.md' = 'docs/CONFIGURATION.md'
    'docs/SETTINGS_V7.md' = 'docs/SETTINGS_V7.md'
    'docs/RECOVERY.md' = 'docs/RECOVERY.md'
}
foreach ($entry in $files.GetEnumerator()) {
    Copy-Item -LiteralPath (Join-Path $repository $entry.Key) -Destination (Join-Path $package $entry.Value)
}

$sourceFiles = [Collections.Generic.List[string]]::new()
function Add-SourceFiles {
    param([string]$Directory)
    foreach ($item in Get-ChildItem -LiteralPath $Directory -Force) {
        if ($item.PSIsContainer) {
            if ($item.Name -notin @('node_modules', '.git', '.vite', 'dist')) {
                Add-SourceFiles -Directory $item.FullName
            }
        } else {
            $sourceFiles.Add($item.FullName)
        }
    }
}
foreach ($directory in @('cmd', 'internal', 'scripts', 'docs')) {
    Add-SourceFiles -Directory (Join-Path $repository $directory)
}
foreach ($file in @('go.mod', 'go.sum', 'VERSION')) {
    $sourceFiles.Add((Join-Path $repository $file))
}
$sourceRecords = @($sourceFiles | Sort-Object | ForEach-Object {
    $relative = $_.Substring($repository.Length + 1).Replace('\', '/')
    "$((Get-FileHash -LiteralPath $_ -Algorithm SHA256).Hash.ToLowerInvariant())  $relative"
})
$sha256 = [Security.Cryptography.SHA256]::Create()
try {
    $bytes = [Text.Encoding]::UTF8.GetBytes(($sourceRecords -join "`n") + "`n")
    $sourceDigest = [BitConverter]::ToString($sha256.ComputeHash($bytes)).Replace('-', '').ToLowerInvariant()
} finally {
    $sha256.Dispose()
}
$payload = @(Get-ChildItem -LiteralPath $package -File -Recurse | Sort-Object FullName | ForEach-Object {
    [ordered]@{
        path = $_.FullName.Substring($package.Length + 1).Replace('\', '/')
        bytes = $_.Length
        sha256 = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    }
})
$manifest = [ordered]@{
    candidate = $candidate
    build_info = "$versionLine"
    target = 'windows/amd64'
    source_base_commit = $sourceBase
    source_dirty = $sourceDirty
    source_digest_sha256 = $sourceDigest
    source_file_count = $sourceRecords.Count
    built_at_utc = [DateTime]::UtcNow.ToString('o')
    signed = $false
    installer = $false
    data_format = 'Harness-v5'
    bundled_credentials = $false
    bundled_user_data = $false
    files = $payload
}
$encoding = [Text.UTF8Encoding]::new($false)
[IO.File]::WriteAllText((Join-Path $package 'manifest.json'), ($manifest | ConvertTo-Json -Depth 6) + "`n", $encoding)
[IO.File]::WriteAllText((Join-Path $OutputDirectory 'SOURCE_SHA256SUMS.txt'), ($sourceRecords -join "`n") + "`n", $encoding)
$checksums = @(Get-ChildItem -LiteralPath $package -File -Recurse | Sort-Object FullName | ForEach-Object {
    "$((Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant())  $($_.FullName.Substring($package.Length + 1).Replace('\', '/'))"
})
[IO.File]::WriteAllText((Join-Path $package 'SHA256SUMS.txt'), ($checksums -join "`n") + "`n", $encoding)
Add-Type -AssemblyName System.IO.Compression.FileSystem
$archive = Join-Path $OutputDirectory "$name.zip"
[IO.Compression.ZipFile]::CreateFromDirectory($staging, $archive)
$archiveHash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
[IO.File]::WriteAllText((Join-Path $OutputDirectory 'SHA256SUMS.txt'), "$archiveHash  $name.zip`n", $encoding)
@{ archive = $archive; sha256 = $archiveHash; source_dirty = $sourceDirty; signed = $false } |
    ConvertTo-Json
