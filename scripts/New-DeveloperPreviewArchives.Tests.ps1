[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:Tool = Join-Path $PSScriptRoot 'New-DeveloperPreviewArchives.ps1'
$script:Utf8NoBom = New-Object Text.UTF8Encoding($false)
$script:Version = 'v0.1.0-dev.1'
$script:Commit = '0123456789abcdef0123456789abcdef01234567'
$script:Cases = 0
$script:Assertions = 0
$script:Failures = New-Object 'Collections.Generic.List[string]'

function Assert-True {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][bool]$Condition
    )
    $script:Assertions++
    if (-not $Condition) { throw "ASSERT_FAIL $Name" }
}

function Assert-Equal {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [AllowNull()]$Expected,
        [AllowNull()]$Actual
    )
    $script:Assertions++
    if ($Expected -cne $Actual) {
        throw "ASSERT_FAIL $Name expected=[$Expected] actual=[$Actual]"
    }
}

function Invoke-Case {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][scriptblock]$Body
    )
    $script:Cases++
    try {
        & $Body
        Write-Host "PASS $Name"
    } catch {
        $script:Failures.Add("$Name`: $($_.Exception.Message)")
        Write-Host "FAIL $Name`: $($_.Exception.Message)"
    }
}

function Assert-ThrowsCode {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Code,
        [Parameter(Mandatory = $true)][scriptblock]$Body
    )
    $message = ''
    try {
        & $Body
    } catch {
        $message = $_.Exception.Message
    }
    Assert-True -Name "$Name throws" -Condition (
        -not [string]::IsNullOrWhiteSpace($message)
    )
    Assert-True -Name "$Name exact code" -Condition (
        $message.StartsWith(
            "DEVELOPER_PREVIEW_ARCHIVE_FAIL code=$Code",
            [StringComparison]::Ordinal
        )
    )
}

function Write-TestBytes {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][byte[]]$Bytes
    )
    $parent = [IO.Path]::GetDirectoryName($Path)
    [void][IO.Directory]::CreateDirectory($parent)
    [IO.File]::WriteAllBytes($Path, $Bytes)
}

function Write-TestText {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Text
    )
    Write-TestBytes -Path $Path -Bytes $script:Utf8NoBom.GetBytes($Text)
}

function Get-TestRelativePath {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$Path
    )
    return $Path.Substring($Root.Length + 1).Replace([char]92, [char]47)
}

function Get-TestFileRecords {
    param([Parameter(Mandatory = $true)][string]$Root)
    $records = New-Object 'Collections.Generic.Dictionary[string,object]' (
        [StringComparer]::Ordinal
    )
    foreach ($file in @(Get-ChildItem -LiteralPath $Root -Recurse -File -Force)) {
        [byte[]]$bytes = [IO.File]::ReadAllBytes($file.FullName)
        $sha = [Security.Cryptography.SHA256]::Create()
        try {
            [byte[]]$hash = $sha.ComputeHash($bytes)
        } finally {
            $sha.Dispose()
        }
        $records.Add(
            (Get-TestRelativePath -Root $Root -Path $file.FullName),
            [pscustomobject]@{
                Length = [int64]$bytes.Length
                Sha256Bytes = $hash
                Sha256 = ([BitConverter]::ToString($hash)).Replace('-', '').ToLowerInvariant()
            }
        )
    }
    return $records
}

function Get-TestArtifactSetSha256 {
    param([Parameter(Mandatory = $true)][string]$Root)
    $records = Get-TestFileRecords -Root $Root
    [string[]]$paths = @($records.Keys)
    [Array]::Sort($paths, [StringComparer]::Ordinal)
    $memory = New-Object IO.MemoryStream
    $sha = $null
    try {
        [byte[]]$prefix = $script:Utf8NoBom.GetBytes(
            "freeagent-artifact-set-v1`0"
        )
        $memory.Write($prefix, 0, $prefix.Length)
        [int64]$total = 0
        foreach ($path in $paths) {
            $record = $records[$path]
            [byte[]]$pathBytes = $script:Utf8NoBom.GetBytes($path)
            [byte[]]$pathLength = [BitConverter]::GetBytes(
                [uint32]$pathBytes.Length
            )
            [byte[]]$fileLength = [BitConverter]::GetBytes(
                [int64]$record.Length
            )
            if ([BitConverter]::IsLittleEndian) {
                [Array]::Reverse($pathLength)
                [Array]::Reverse($fileLength)
            }
            $memory.Write($pathLength, 0, $pathLength.Length)
            $memory.Write($pathBytes, 0, $pathBytes.Length)
            $memory.Write($fileLength, 0, $fileLength.Length)
            $memory.Write($record.Sha256Bytes, 0, $record.Sha256Bytes.Length)
            $total += $record.Length
        }
        [byte[]]$totalBytes = [BitConverter]::GetBytes($total)
        if ([BitConverter]::IsLittleEndian) { [Array]::Reverse($totalBytes) }
        $memory.Write($totalBytes, 0, $totalBytes.Length)
        $memory.Position = 0
        $sha = [Security.Cryptography.SHA256]::Create()
        return ([BitConverter]::ToString($sha.ComputeHash($memory))).
            Replace('-', '').ToLowerInvariant()
    } finally {
        if ($null -ne $sha) { $sha.Dispose() }
        $memory.Dispose()
    }
}

function Write-TestChecksums {
    param([Parameter(Mandatory = $true)][string]$Root)
    $records = Get-TestFileRecords -Root $Root
    [string[]]$paths = @($records.Keys)
    [Array]::Sort($paths, [StringComparer]::Ordinal)
    $builder = New-Object Text.StringBuilder
    foreach ($path in $paths) {
        if ($path -ceq 'supply-chain/checksums.sha256') { continue }
        [void]$builder.Append($records[$path].Sha256)
        [void]$builder.Append('  ')
        [void]$builder.Append($path)
        [void]$builder.Append("`n")
    }
    Write-TestText `
        -Path (Join-Path $Root 'supply-chain/checksums.sha256') `
        -Text $builder.ToString()
}

function Write-TestJson {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)]$Value
    )
    Write-TestText -Path $Path -Text (
        ($Value | ConvertTo-Json -Depth 30 -Compress) + "`n"
    )
}

function New-TestArtifact {
    param(
        [Parameter(Mandatory = $true)][string]$Container,
        [Parameter(Mandatory = $true)][string]$Goos,
        [Parameter(Mandatory = $true)][string]$Goarch,
        [string]$Revision = $script:Commit,
        [string]$Version = $script:Version
    )
    $target = "$Goos-$Goarch"
    $root = Join-Path $Container $target
    [void][IO.Directory]::CreateDirectory($root)
    $extension = if ($Goos -ceq 'windows') { '.exe' } else { '' }
    $binary = "bin/freeagent-$target$extension"
    $textFiles = [ordered]@{
        'VERSION' = "$Version`n"
        'LICENSE' = "fixture license`n"
        'THIRD_PARTY_NOTICES.md' = "# Fixture notices`n"
        'INSTALL.md' = "# Install $target`n"
        'QUICKSTART.md' = "# Quickstart $target`n"
        'KNOWN_LIMITATIONS.md' = "# Known limitations`n"
        'RELEASE_NOTES.md' = "# Release notes $Version`n"
        'VERIFY_CHECKSUMS.md' = "# Verify checksums`n"
        'config/README.md' = "# Config`n"
        'data/README.md' = "# Data`n"
        'config/current-v1.bootstrap.seed.json' =
            "{`"schema`":`"fixture-v1`",`"target`":`"$target`"}`n"
        'config/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/manifest.json' =
            "{`"name`":`"context.basic`"}`n"
        'config/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/module.json' =
            "{`"version`":`"1.0.0`"}`n"
        'config/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/README.md' =
            "context fixture`n"
        'config/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/config.json' =
            "{}`n"
        'config/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/manifest.json' =
            "{`"name`":`"model.echo`"}`n"
        'config/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/module.json' =
            "{`"version`":`"1.0.0`"}`n"
        'config/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/README.md' =
            "model fixture`n"
        'config/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/config.json' =
            "{}`n"
        'config/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/prompt.txt' =
            "echo fixture`n"
    }
    foreach ($entry in $textFiles.GetEnumerator()) {
        Write-TestText `
            -Path (Join-Path $root $entry.Key) `
            -Text ([string]$entry.Value)
    }
    Write-TestText `
        -Path (Join-Path $root $binary) `
        -Text "fixture-binary-$target`n"

    $baseRecords = Get-TestFileRecords -Root $root
    [string[]]$basePaths = @($baseRecords.Keys)
    [Array]::Sort($basePaths, [StringComparer]::Ordinal)
    $sbom = [ordered]@{
        SPDXID = 'SPDXRef-DOCUMENT'
        spdxVersion = 'SPDX-2.3'
        packages = @(
            [ordered]@{
                SPDXID = 'SPDXRef-Package-FreeAgent'
                name = 'FreeAgent'
                versionInfo = $Version
            }
        )
    }
    $sbomPath = Join-Path $root 'supply-chain/sbom.spdx.json'
    Write-TestJson -Path $sbomPath -Value $sbom
    $withSbom = Get-TestFileRecords -Root $root
    $subjects = New-Object 'Collections.Generic.List[object]'
    foreach ($path in $basePaths) {
        $subjects.Add([ordered]@{
            name = $path
            digest = [ordered]@{ sha256 = $baseRecords[$path].Sha256 }
        })
    }
    $subjects.Add([ordered]@{
        name = 'supply-chain/sbom.spdx.json'
        digest = [ordered]@{
            sha256 = $withSbom['supply-chain/sbom.spdx.json'].Sha256
        }
    })
    $predicate = [ordered]@{
        buildDefinition = [ordered]@{
            externalParameters = [ordered]@{
                jobKind = 'cross-build'
                releaseVersion = $Version
                revision = $Revision
                targetGoos = $Goos
                targetGoarch = $Goarch
            }
        }
    }
    $predicate['https://github.com/endview/freeagent/provenance/metadata/v1'] =
        [ordered]@{ runSucceeded = $true }
    $provenance = [ordered]@{
        _type = 'https://in-toto.io/Statement/v1'
        predicate = $predicate
        predicateType = 'https://slsa.dev/provenance/v1'
        subject = $subjects.ToArray()
    }
    Write-TestJson `
        -Path (Join-Path $root 'supply-chain/provenance.unsigned.v1.json') `
        -Value $provenance

    $checksumRecords = Get-TestFileRecords -Root $root
    [string[]]$checksumPaths = @($checksumRecords.Keys)
    [Array]::Sort($checksumPaths, [StringComparer]::Ordinal)
    $builder = New-Object Text.StringBuilder
    foreach ($path in $checksumPaths) {
        [void]$builder.Append($checksumRecords[$path].Sha256)
        [void]$builder.Append('  ')
        [void]$builder.Append($path)
        [void]$builder.Append("`n")
    }
    Write-TestText `
        -Path (Join-Path $root 'supply-chain/checksums.sha256') `
        -Text $builder.ToString()
    return [pscustomobject]@{
        Root = $root
        Target = $target
        Binary = $binary
        ArtifactSetSha256 = Get-TestArtifactSetSha256 -Root $root
    }
}

function New-TestFixture {
    param(
        [Parameter(Mandatory = $true)][string]$Container,
        [Parameter(Mandatory = $true)][string]$Name
    )
    $root = Join-Path $Container $Name
    $artifactsRoot = Join-Path $root 'artifacts'
    [void][IO.Directory]::CreateDirectory($artifactsRoot)
    $artifacts = New-Object 'Collections.Generic.List[object]'
    foreach ($target in @(
        @('windows','amd64'),
        @('windows','arm64'),
        @('linux','amd64'),
        @('linux','arm64'),
        @('darwin','amd64'),
        @('darwin','arm64')
    )) {
        $artifacts.Add(
            (New-TestArtifact `
                -Container $artifactsRoot `
                -Goos $target[0] `
                -Goarch $target[1])
        )
    }
    return [pscustomobject]@{
        Root = $root
        ArtifactRoot = [string[]]@($artifacts | ForEach-Object { $_.Root })
        ArtifactSetSha256 = [string[]]@(
            $artifacts | ForEach-Object { $_.ArtifactSetSha256 }
        )
        Artifacts = $artifacts.ToArray()
    }
}

function Invoke-TestTool {
    param(
        [Parameter(Mandatory = $true)]$Fixture,
        [Parameter(Mandatory = $true)][string]$OutputRoot,
        [string]$Version = $script:Version,
        [string]$Commit = $script:Commit,
        [switch]$BuildOnly
    )
    $parameters = @{
        ArtifactRoot = $Fixture.ArtifactRoot
        ExpectedArtifactSetSha256 = $Fixture.ArtifactSetSha256
        Version = $Version
        Commit = $Commit
        OutputRoot = $OutputRoot
    }
    if ($BuildOnly) { $parameters.BuildOnly = $true }
    return @(& $script:Tool @parameters 6>$null)
}

function Get-TestTreeState {
    param([Parameter(Mandatory = $true)][string[]]$Roots)
    $builder = New-Object Text.StringBuilder
    foreach ($root in @($Roots | Sort-Object -CaseSensitive)) {
        $records = Get-TestFileRecords -Root $root
        [string[]]$paths = @($records.Keys)
        [Array]::Sort($paths, [StringComparer]::Ordinal)
        foreach ($path in $paths) {
            [void]$builder.Append($root)
            [void]$builder.Append([char]0)
            [void]$builder.Append($path)
            [void]$builder.Append([char]0)
            [void]$builder.Append($records[$path].Length)
            [void]$builder.Append([char]0)
            [void]$builder.Append($records[$path].Sha256)
            [void]$builder.Append("`n")
        }
    }
    return $builder.ToString()
}

function Get-TestOutputHashes {
    param([Parameter(Mandatory = $true)][string]$Root)
    $records = Get-TestFileRecords -Root $Root
    [string[]]$paths = @($records.Keys)
    [Array]::Sort($paths, [StringComparer]::Ordinal)
    return ($paths | ForEach-Object {
        "$_=$($records[$_].Sha256)"
    }) -join "`n"
}

function Read-TestZipEntries {
    param([Parameter(Mandatory = $true)][string]$Path)
    Add-Type -AssemblyName System.IO.Compression -ErrorAction Stop
    $stream = $null
    $zip = $null
    try {
        $stream = [IO.File]::OpenRead($Path)
        $zip = New-Object IO.Compression.ZipArchive(
            $stream,
            [IO.Compression.ZipArchiveMode]::Read,
            $false
        )
        $entries = [ordered]@{}
        foreach ($entry in $zip.Entries) {
            $input = $entry.Open()
            $memory = New-Object IO.MemoryStream
            try {
                $input.CopyTo($memory)
                $entries[$entry.FullName] = $memory.ToArray()
            } finally {
                $memory.Dispose()
                $input.Dispose()
            }
        }
        return $entries
    } finally {
        if ($null -ne $zip) { $zip.Dispose() }
        if ($null -ne $stream) { $stream.Dispose() }
    }
}

function Get-TestTarString {
    param(
        [Parameter(Mandatory = $true)][byte[]]$Bytes,
        [Parameter(Mandatory = $true)][int]$Offset,
        [Parameter(Mandatory = $true)][int]$Length
    )
    $count = 0
    while ($count -lt $Length -and $Bytes[$Offset + $count] -ne 0) {
        $count++
    }
    return [Text.Encoding]::ASCII.GetString($Bytes, $Offset, $count).Trim()
}

function Read-TestTarGzipEntries {
    param([Parameter(Mandatory = $true)][string]$Path)
    Add-Type -AssemblyName System.IO.Compression -ErrorAction Stop
    $input = $null
    $gzip = $null
    $memory = New-Object IO.MemoryStream
    try {
        $input = [IO.File]::OpenRead($Path)
        $gzip = New-Object IO.Compression.GZipStream(
            $input,
            [IO.Compression.CompressionMode]::Decompress,
            $false
        )
        $gzip.CopyTo($memory)
        [byte[]]$tar = $memory.ToArray()
    } finally {
        if ($null -ne $gzip) { $gzip.Dispose() }
        if ($null -ne $input) { $input.Dispose() }
        $memory.Dispose()
    }
    $entries = [ordered]@{}
    $offset = 0
    while ($offset + 512 -le $tar.Length) {
        $zero = $true
        for ($index = 0; $index -lt 512; $index++) {
            if ($tar[$offset + $index] -ne 0) { $zero = $false; break }
        }
        if ($zero) { break }
        $name = Get-TestTarString $tar $offset 100
        $prefix = Get-TestTarString $tar ($offset + 345) 155
        if (-not [string]::IsNullOrEmpty($prefix)) { $name = "$prefix/$name" }
        $sizeText = Get-TestTarString $tar ($offset + 124) 12
        $size = [Convert]::ToInt64($sizeText, 8)
        $mtime = [Convert]::ToInt64(
            (Get-TestTarString $tar ($offset + 136) 12),
            8
        )
        Assert-Equal -Name "tar fixed mtime $name" `
            -Expected ([int64]946684800) `
            -Actual $mtime
        $offset += 512
        [byte[]]$content = New-Object byte[] $size
        if ($size -gt 0) { [Array]::Copy($tar, $offset, $content, 0, $size) }
        $entries[$name] = $content
        $offset += [int](($size + 511) -band (-bnot 511))
    }
    return $entries
}

$suiteRoot = Join-Path ([IO.Path]::GetTempPath()) (
    'freeagent-developer-preview-archive-tests-' + [Guid]::NewGuid().ToString('N')
)
[void][IO.Directory]::CreateDirectory($suiteRoot)
try {
    Invoke-Case 'six archives are deterministic complete and leave sealed roots unchanged' {
        $fixture = New-TestFixture -Container $suiteRoot -Name 'deterministic'
        $before = Get-TestTreeState -Roots $fixture.ArtifactRoot
        $outputOne = Join-Path $fixture.Root 'output-one'
        $outputTwo = Join-Path $fixture.Root 'output-two'
        $resultOne = Invoke-TestTool `
            -Fixture $fixture `
            -OutputRoot $outputOne `
            -BuildOnly
        $resultTwo = Invoke-TestTool `
            -Fixture $fixture `
            -OutputRoot $outputTwo `
            -BuildOnly
        Assert-Equal -Name 'single result object one' -Expected 1 -Actual @($resultOne).Count
        Assert-Equal -Name 'archive count result' -Expected 6 -Actual $resultOne[0].ArchiveCount
        Assert-Equal -Name 'deterministic output hashes' `
            -Expected (Get-TestOutputHashes -Root $outputOne) `
            -Actual (Get-TestOutputHashes -Root $outputTwo)
        Assert-Equal -Name 'sealed roots unchanged' `
            -Expected $before `
            -Actual (Get-TestTreeState -Roots $fixture.ArtifactRoot)

        [string[]]$outputNames = @(Get-ChildItem -LiteralPath $outputOne -File |
            ForEach-Object { $_.Name })
        [Array]::Sort($outputNames, [StringComparer]::Ordinal)
        Assert-Equal -Name 'exact output files' -Expected (
            @(
                'SHA256SUMS',
                'freeagent-v0.1.0-dev.1-darwin-amd64-build-only.tar.gz',
                'freeagent-v0.1.0-dev.1-darwin-arm64-build-only.tar.gz',
                'freeagent-v0.1.0-dev.1-linux-amd64-build-only.tar.gz',
                'freeagent-v0.1.0-dev.1-linux-arm64-build-only.tar.gz',
                'freeagent-v0.1.0-dev.1-windows-amd64-build-only.zip',
                'freeagent-v0.1.0-dev.1-windows-arm64-build-only.zip'
            ) -join "`n"
        ) -Actual ($outputNames -join "`n")

        $manifestPath = Join-Path $outputOne 'SHA256SUMS'
        [byte[]]$manifestBytes = [IO.File]::ReadAllBytes($manifestPath)
        $manifest = $script:Utf8NoBom.GetString($manifestBytes)
        Assert-True -Name 'manifest LF only' -Condition (
            $manifest.EndsWith("`n") -and -not $manifest.Contains("`r")
        )
        $previous = ''
        foreach ($line in $manifest.TrimEnd("`n").Split("`n")) {
            Assert-True -Name 'manifest strict line' -Condition (
                $line -cmatch '^([0-9a-f]{64})  ([A-Za-z0-9._-]+\.(?:zip|tar\.gz))$'
            )
            $hash = $Matches[1]
            $name = $Matches[2]
            Assert-True -Name "manifest sorted $name" -Condition (
                [string]::IsNullOrEmpty($previous) -or
                [StringComparer]::Ordinal.Compare($previous, $name) -lt 0
            )
            $previous = $name
            Assert-Equal -Name "manifest hash $name" `
                -Expected $hash `
                -Actual (Get-TestFileRecords -Root $outputOne)[$name].Sha256
        }

        $zipName = 'freeagent-v0.1.0-dev.1-windows-amd64-build-only.zip'
        $zipEntries = Read-TestZipEntries -Path (Join-Path $outputOne $zipName)
        $zipPrefix = 'freeagent-v0.1.0-dev.1-windows-amd64-build-only/'
        foreach ($relative in @(
            'VERSION',
            'LICENSE',
            'INSTALL.md',
            'VERIFY_CHECKSUMS.md',
            'config/current-v1.bootstrap.seed.json',
            'config/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/prompt.txt',
            'supply-chain/checksums.sha256',
            'bin/freeagent-windows-amd64.exe'
        )) {
            Assert-True -Name "zip contains $relative" -Condition (
                $zipEntries.Contains($zipPrefix + $relative)
            )
        }
        $sourceCount = (Get-TestFileRecords -Root $fixture.Artifacts[0].Root).Count
        Assert-Equal -Name 'zip contains full sealed allowlist' `
            -Expected $sourceCount `
            -Actual $zipEntries.Count

        $tarName = 'freeagent-v0.1.0-dev.1-linux-amd64-build-only.tar.gz'
        $tarEntries = Read-TestTarGzipEntries -Path (Join-Path $outputOne $tarName)
        $tarPrefix = 'freeagent-v0.1.0-dev.1-linux-amd64-build-only/'
        foreach ($relative in @(
            'VERSION',
            'RELEASE_NOTES.md',
            'data/README.md',
            'config/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/manifest.json',
            'supply-chain/provenance.unsigned.v1.json',
            'bin/freeagent-linux-amd64'
        )) {
            Assert-True -Name "tar contains $relative" -Condition (
                $tarEntries.Contains($tarPrefix + $relative)
            )
        }
        $linux = @($fixture.Artifacts | Where-Object Target -ceq 'linux-amd64')[0]
        Assert-Equal -Name 'tar contains full sealed allowlist' `
            -Expected (Get-TestFileRecords -Root $linux.Root).Count `
            -Actual $tarEntries.Count
    }

    Invoke-Case 'native package names are installable and Darwin remains build-only' {
        $fixture = New-TestFixture -Container $suiteRoot -Name 'native-names'
        $output = Join-Path $fixture.Root 'output'
        [void](Invoke-TestTool -Fixture $fixture -OutputRoot $output)
        [string[]]$outputNames = @(Get-ChildItem -LiteralPath $output -File |
            ForEach-Object { $_.Name })
        [Array]::Sort($outputNames, [StringComparer]::Ordinal)
        Assert-Equal -Name 'native and build-only output names' -Expected (
            @(
                'SHA256SUMS',
                'freeagent-v0.1.0-dev.1-darwin-amd64-build-only.tar.gz',
                'freeagent-v0.1.0-dev.1-darwin-arm64-build-only.tar.gz',
                'freeagent-v0.1.0-dev.1-linux-amd64.tar.gz',
                'freeagent-v0.1.0-dev.1-linux-arm64.tar.gz',
                'freeagent-v0.1.0-dev.1-windows-amd64.zip',
                'freeagent-v0.1.0-dev.1-windows-arm64.zip'
            ) -join ([char]10)
        ) -Actual ($outputNames -join ([char]10))
    }

    Invoke-Case 'tampered sealed payload fails closed before output publication' {
        $fixture = New-TestFixture -Container $suiteRoot -Name 'tamper'
        $binary = Join-Path $fixture.Artifacts[0].Root $fixture.Artifacts[0].Binary
        [IO.File]::AppendAllText($binary, "tampered`n", $script:Utf8NoBom)
        $output = Join-Path $fixture.Root 'output'
        Assert-ThrowsCode -Name 'tampered binary' -Code 'DPA_ARTIFACT_TAMPERED' -Body {
            [void](Invoke-TestTool -Fixture $fixture -OutputRoot $output)
        }
        Assert-True -Name 'tamper output absent' -Condition (
            -not (Test-Path -LiteralPath $output)
        )
    }

    Invoke-Case 'malicious checksum path fails closed even with a recomputed set seal' {
        $fixture = New-TestFixture -Container $suiteRoot -Name 'path'
        $root = $fixture.Artifacts[0].Root
        $checksums = Join-Path $root 'supply-chain/checksums.sha256'
        $text = [IO.File]::ReadAllText($checksums)
        $firstLineEnd = $text.IndexOf("`n")
        $firstLine = $text.Substring(0, $firstLineEnd)
        $hash = $firstLine.Substring(0, 64)
        $text = "$hash  ../escape`n" + $text.Substring($firstLineEnd + 1)
        Write-TestText -Path $checksums -Text $text
        $fixture.ArtifactSetSha256[0] = Get-TestArtifactSetSha256 -Root $root
        $output = Join-Path $fixture.Root 'output'
        Assert-ThrowsCode -Name 'checksum traversal' -Code 'DPA_CHECKSUMS_INVALID' -Body {
            [void](Invoke-TestTool -Fixture $fixture -OutputRoot $output)
        }
        Assert-True -Name 'path output absent' -Condition (
            -not (Test-Path -LiteralPath $output)
        )
    }

    Invoke-Case 'duplicate provenance JSON keys fail closed' {
        foreach ($scenario in @('ordinary', 'escaped')) {
            $fixture = New-TestFixture `
                -Container $suiteRoot `
                -Name "duplicate-$scenario"
            $root = $fixture.Artifacts[0].Root
            $provenance = Join-Path $root 'supply-chain/provenance.unsigned.v1.json'
            $text = if ($scenario -ceq 'ordinary') {
                '{"duplicate":"one","duplicate":"two"}'
            } else {
                '{"duplicate":"one","dup\u006cicate":"two"}'
            }
            Write-TestText -Path $provenance -Text $text
            Write-TestChecksums -Root $root
            $fixture.ArtifactSetSha256[0] = Get-TestArtifactSetSha256 -Root $root
            $output = Join-Path $fixture.Root 'output'
            Assert-ThrowsCode `
                -Name "$scenario duplicate key" `
                -Code 'DPA_PROVENANCE_INVALID' `
                -Body {
                    [void](Invoke-TestTool -Fixture $fixture -OutputRoot $output)
                }
            Assert-True -Name "$scenario output absent" -Condition (
                -not (Test-Path -LiteralPath $output)
            )
        }
    }

    Invoke-Case 'input collection counts are validated before artifact mapping' {
        $fixture = New-TestFixture `
            -Container $suiteRoot `
            -Name 'input-counts'
        $emptyOutput = Join-Path $fixture.Root 'empty-output'
        Assert-ThrowsCode `
            -Name 'empty artifact collections' `
            -Code 'DPA_INPUT_COUNT_INVALID' `
            -Body {
                [void](& $script:Tool `
                    -ArtifactRoot @() `
                    -ExpectedArtifactSetSha256 @() `
                    -Version $script:Version `
                    -Commit $script:Commit `
                    -OutputRoot $emptyOutput)
            }
        Assert-True -Name 'empty output absent' -Condition (
            -not (Test-Path -LiteralPath $emptyOutput)
        )

        $mismatchOutput = Join-Path $fixture.Root 'mismatch-output'
        Assert-ThrowsCode `
            -Name 'hash count mismatch' `
            -Code 'DPA_INPUT_COUNT_INVALID' `
            -Body {
                [void](& $script:Tool `
                    -ArtifactRoot $fixture.ArtifactRoot `
                    -ExpectedArtifactSetSha256 @($fixture.ArtifactSetSha256[0..4]) `
                    -Version $script:Version `
                    -Commit $script:Commit `
                    -OutputRoot $mismatchOutput)
            }
        Assert-True -Name 'mismatch output absent' -Condition (
            -not (Test-Path -LiteralPath $mismatchOutput)
        )
    }

    Invoke-Case 'duplicate target and overlapping output are rejected' {
        $fixture = New-TestFixture -Container $suiteRoot -Name 'collision'
        $duplicate = Join-Path $fixture.Root 'duplicate-windows-amd64'
        Copy-Item -LiteralPath $fixture.Artifacts[0].Root -Destination $duplicate -Recurse
        $fixture.ArtifactRoot[1] = $duplicate
        $fixture.ArtifactSetSha256[1] = Get-TestArtifactSetSha256 -Root $duplicate
        $output = Join-Path $fixture.Root 'output'
        Assert-ThrowsCode -Name 'duplicate target' -Code 'DPA_TARGET_COLLISION' -Body {
            [void](Invoke-TestTool -Fixture $fixture -OutputRoot $output)
        }
        Assert-True -Name 'collision output absent' -Condition (
            -not (Test-Path -LiteralPath $output)
        )

        $clean = New-TestFixture -Container $suiteRoot -Name 'overlap'
        $nestedOutput = Join-Path $clean.ArtifactRoot[0] 'archives'
        Assert-ThrowsCode -Name 'nested output' -Code 'DPA_ROOTS_OVERLAP' -Body {
            [void](Invoke-TestTool -Fixture $clean -OutputRoot $nestedOutput)
        }
        Assert-True -Name 'overlap output absent' -Condition (
            -not (Test-Path -LiteralPath $nestedOutput)
        )
    }

    Invoke-Case 'existing output and version mismatch fail without replacement' {
        $fixture = New-TestFixture -Container $suiteRoot -Name 'existing'
        $output = Join-Path $fixture.Root 'output'
        [void][IO.Directory]::CreateDirectory($output)
        $sentinel = Join-Path $output 'sentinel.txt'
        Write-TestText -Path $sentinel -Text "keep`n"
        Assert-ThrowsCode -Name 'existing output' -Code 'DPA_OUTPUT_EXISTS' -Body {
            [void](Invoke-TestTool -Fixture $fixture -OutputRoot $output)
        }
        Assert-Equal -Name 'existing output unchanged' -Expected "keep`n" -Actual (
            [IO.File]::ReadAllText($sentinel)
        )

        $versionOutput = Join-Path $fixture.Root 'version-output'
        Assert-ThrowsCode `
            -Name 'sealed VERSION mismatch' `
            -Code 'DPA_VERSION_FILE_MISMATCH' `
            -Body {
                [void](Invoke-TestTool `
                    -Fixture $fixture `
                    -OutputRoot $versionOutput `
                    -Version 'v0.1.0-dev.2')
            }
        Assert-True -Name 'version output absent' -Condition (
            -not (Test-Path -LiteralPath $versionOutput)
        )
    }

    Invoke-Case 'reparse artifact root is rejected when junctions are available' {
        $fixture = New-TestFixture -Container $suiteRoot -Name 'reparse'
        $junction = Join-Path $fixture.Root 'artifact-junction'
        $created = $false
        try {
            [void](New-Item `
                -ItemType Junction `
                -Path $junction `
                -Target $fixture.ArtifactRoot[0] `
                -ErrorAction Stop)
            $created = $true
        } catch {
            Write-Host 'SKIP junction creation unavailable'
        }
        if ($created) {
            try {
                $fixture.ArtifactRoot[0] = $junction
                $output = Join-Path $fixture.Root 'output'
                Assert-ThrowsCode -Name 'junction artifact root' -Code 'DPA_ARTIFACT_REPARSE' -Body {
                    [void](Invoke-TestTool -Fixture $fixture -OutputRoot $output)
                }
                Assert-True -Name 'reparse output absent' -Condition (
                    -not (Test-Path -LiteralPath $output)
                )
            } finally {
                if (Test-Path -LiteralPath $junction) {
                    [IO.Directory]::Delete($junction)
                }
            }
        }
    }
} finally {
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd(
        [IO.Path]::DirectorySeparatorChar
    )
    $suiteFull = [IO.Path]::GetFullPath($suiteRoot)
    if (-not $suiteFull.StartsWith(
        $tempRoot + [IO.Path]::DirectorySeparatorChar,
        [StringComparison]::OrdinalIgnoreCase
    ) -or
        -not ([IO.Path]::GetFileName($suiteFull)).StartsWith(
            'freeagent-developer-preview-archive-tests-',
            [StringComparison]::Ordinal
        )) {
        throw 'TEST_CLEANUP_PATH_INVALID'
    }
    if (Test-Path -LiteralPath $suiteFull) {
        [IO.Directory]::Delete($suiteFull, $true)
    }
}

if ($script:Failures.Count -gt 0) {
    foreach ($failure in $script:Failures) { Write-Error $failure }
    throw "DEVELOPER_PREVIEW_ARCHIVE_TEST_FAIL failures=$($script:Failures.Count)"
}
Write-Host (
    'DEVELOPER_PREVIEW_ARCHIVE_TEST_PASS ' +
    "cases=$($script:Cases) assertions=$($script:Assertions)"
)
