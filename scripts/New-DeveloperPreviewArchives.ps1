[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [AllowEmptyCollection()]
    [string[]]$ArtifactRoot,
    [Parameter(Mandatory = $true)]
    [AllowEmptyCollection()]
    [string[]]$ExpectedArtifactSetSha256,
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$Commit,
    [Parameter(Mandatory = $true)][string]$OutputRoot,
    [switch]$BuildOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:Utf8NoBom = New-Object Text.UTF8Encoding($false)
$script:Utf8Strict = New-Object Text.UTF8Encoding($false, $true)
$script:IsWindowsPlatform =
    [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
$script:PathComparison = if ($script:IsWindowsPlatform) {
    [StringComparison]::OrdinalIgnoreCase
} else {
    [StringComparison]::Ordinal
}
$script:FixedUnixTime = [int64]946684800 # 2000-01-01T00:00:00Z
$script:MaximumFileBytes = [int64]4GB - 1
$script:ExpectedTargets = @(
    'darwin-amd64',
    'darwin-arm64',
    'linux-amd64',
    'linux-arm64',
    'windows-amd64',
    'windows-arm64'
)

if ($null -eq ('DeveloperPreviewArchiveCrc32' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.IO;

public static class DeveloperPreviewArchiveCrc32
{
    private static readonly uint[] Table = BuildTable();

    private static uint[] BuildTable()
    {
        var table = new uint[256];
        for (uint i = 0; i < table.Length; i++)
        {
            uint value = i;
            for (int bit = 0; bit < 8; bit++)
            {
                value = (value & 1) != 0
                    ? 0xedb88320U ^ (value >> 1)
                    : value >> 1;
            }
            table[i] = value;
        }
        return table;
    }

    public static uint Update(uint state, byte[] buffer, int offset, int count)
    {
        uint value = state;
        int limit = checked(offset + count);
        for (int i = offset; i < limit; i++)
        {
            value = Table[(value ^ buffer[i]) & 0xff] ^ (value >> 8);
        }
        return value;
    }
}
'@ -ErrorAction Stop
}

function Fail-DeveloperPreviewArchive {
    param([Parameter(Mandatory = $true)][string]$Code)
    throw "DEVELOPER_PREVIEW_ARCHIVE_FAIL code=$Code"
}

if ($ArtifactRoot.Count -ne 6 -or $ExpectedArtifactSetSha256.Count -ne 6) {
    Fail-DeveloperPreviewArchive -Code 'DPA_INPUT_COUNT_INVALID'
}

function ConvertTo-DPALowerHex {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    return ([BitConverter]::ToString($Bytes)).Replace('-', '').ToLowerInvariant()
}

function Get-DPASha256Bytes {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        return ,([byte[]]$sha.ComputeHash($Bytes))
    } finally {
        $sha.Dispose()
    }
}

function Get-DPANormalizedAbsolutePath {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if ([string]::IsNullOrWhiteSpace($Value) -or
        -not [IO.Path]::IsPathRooted($Value)) {
        Fail-DeveloperPreviewArchive -Code $Code
    }
    try {
        $full = [IO.Path]::GetFullPath($Value)
    } catch {
        Fail-DeveloperPreviewArchive -Code $Code
    }
    $root = [IO.Path]::GetPathRoot($full)
    if ([string]::IsNullOrWhiteSpace($root) -or
        [string]::Equals(
            $full.TrimEnd([IO.Path]::DirectorySeparatorChar),
            $root.TrimEnd([IO.Path]::DirectorySeparatorChar),
            $script:PathComparison
        )) {
        Fail-DeveloperPreviewArchive -Code $Code
    }
    return $full.TrimEnd(
        [IO.Path]::DirectorySeparatorChar,
        [IO.Path]::AltDirectorySeparatorChar
    )
}

function Assert-DPANoReparseAncestry {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $cursor = $Path
    while (-not (Test-Path -LiteralPath $cursor)) {
        $parent = [IO.Path]::GetDirectoryName($cursor)
        if ([string]::IsNullOrWhiteSpace($parent) -or $parent -ceq $cursor) {
            Fail-DeveloperPreviewArchive -Code $Code
        }
        $cursor = $parent
    }
    while (-not [string]::IsNullOrWhiteSpace($cursor)) {
        try {
            $item = Get-Item -LiteralPath $cursor -Force
        } catch {
            Fail-DeveloperPreviewArchive -Code $Code
        }
        if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            Fail-DeveloperPreviewArchive -Code $Code
        }
        $parent = [IO.Path]::GetDirectoryName($cursor)
        if ([string]::IsNullOrWhiteSpace($parent) -or $parent -ceq $cursor) {
            break
        }
        $cursor = $parent
    }
}

function Test-DPAPathContains {
    param(
        [Parameter(Mandatory = $true)][string]$Parent,
        [Parameter(Mandatory = $true)][string]$Candidate
    )
    return [string]::Equals($Parent, $Candidate, $script:PathComparison) -or
        $Candidate.StartsWith(
            $Parent + [IO.Path]::DirectorySeparatorChar,
            $script:PathComparison
        )
}

function Assert-DPADisjointOutput {
    param(
        [Parameter(Mandatory = $true)][string]$Output,
        [Parameter(Mandatory = $true)][string[]]$Inputs
    )
    foreach ($inputRoot in $Inputs) {
        if ((Test-DPAPathContains -Parent $Output -Candidate $inputRoot) -or
            (Test-DPAPathContains -Parent $inputRoot -Candidate $Output)) {
            Fail-DeveloperPreviewArchive -Code 'DPA_ROOTS_OVERLAP'
        }
    }
}

function Assert-DPAPortableRelativePath {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if ([string]::IsNullOrWhiteSpace($Value) -or
        $Value.Length -gt 240 -or
        $Value.StartsWith('/') -or
        $Value.EndsWith('/') -or
        $Value.Contains([char]92) -or
        $Value.Contains([char]0) -or
        $Value.Contains(':') -or
        $Value -cnotmatch '^[A-Za-z0-9._/-]+$') {
        Fail-DeveloperPreviewArchive -Code $Code
    }
    foreach ($segment in $Value.Split([char]47)) {
        if ([string]::IsNullOrEmpty($segment) -or
            $segment -ceq '.' -or
            $segment -ceq '..' -or
            $segment.EndsWith('.') -or
            $segment.EndsWith(' ')) {
            Fail-DeveloperPreviewArchive -Code $Code
        }
        $base = ($segment -split '\.', 2)[0]
        if ($base -match '^(?i:CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])$' -or
            $segment -ieq '.git') {
            Fail-DeveloperPreviewArchive -Code $Code
        }
    }
}

function Get-DPARelativePath {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$Path
    )
    if (-not $Path.StartsWith(
        $Root + [IO.Path]::DirectorySeparatorChar,
        $script:PathComparison
    )) {
        Fail-DeveloperPreviewArchive -Code 'DPA_ARTIFACT_LAYOUT_INVALID'
    }
    $relative = $Path.Substring($Root.Length + 1)
    if ($script:IsWindowsPlatform) {
        $relative = $relative.Replace([char]92, [char]47)
    }
    Assert-DPAPortableRelativePath `
        -Value $relative `
        -Code 'DPA_ARTIFACT_LAYOUT_INVALID'
    return $relative
}

function Get-DPAFileRecord {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )
    Assert-DPAPortableRelativePath `
        -Value $RelativePath `
        -Code 'DPA_SOURCE_PATH_INVALID'
    Assert-DPANoReparseAncestry -Path $Path -Code 'DPA_SOURCE_REPARSE'
    try {
        $item = Get-Item -LiteralPath $Path -Force
    } catch {
        Fail-DeveloperPreviewArchive -Code 'DPA_SOURCE_INVALID'
    }
    if (-not ($item -is [IO.FileInfo]) -or
        ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
        $item.Length -lt 0 -or
        $item.Length -gt $script:MaximumFileBytes) {
        Fail-DeveloperPreviewArchive -Code 'DPA_SOURCE_INVALID'
    }
    $stream = $null
    $sha = $null
    try {
        $stream = New-Object IO.FileStream(
            $item.FullName,
            [IO.FileMode]::Open,
            [IO.FileAccess]::Read,
            [IO.FileShare]::Read,
            1048576,
            [IO.FileOptions]::SequentialScan
        )
        $sha = [Security.Cryptography.SHA256]::Create()
        [uint32]$crcState = [uint32]::MaxValue
        [byte[]]$buffer = New-Object byte[] 1048576
        [int64]$count = 0
        while (($read = $stream.Read($buffer, 0, $buffer.Length)) -gt 0) {
            [void]$sha.TransformBlock($buffer, 0, $read, $null, 0)
            $crcState = [DeveloperPreviewArchiveCrc32]::Update(
                $crcState, $buffer, 0, $read
            )
            $count += $read
        }
        [void]$sha.TransformFinalBlock((New-Object byte[] 0), 0, 0)
        if ($count -ne $item.Length -or $stream.Length -ne $item.Length) {
            Fail-DeveloperPreviewArchive -Code 'DPA_SOURCE_CHANGED'
        }
        return [pscustomobject]@{
            RelativePath = $RelativePath
            SourcePath = $item.FullName
            Bytes = $null
            Length = [int64]$count
            Sha256Bytes = [byte[]]$sha.Hash
            Sha256 = ConvertTo-DPALowerHex -Bytes ([byte[]]$sha.Hash)
            Crc32 = [uint32]($crcState -bxor [uint32]::MaxValue)
        }
    } catch {
        if ($_.Exception.Message.StartsWith(
            'DEVELOPER_PREVIEW_ARCHIVE_FAIL code=',
            [StringComparison]::Ordinal
        )) { throw }
        Fail-DeveloperPreviewArchive -Code 'DPA_SOURCE_READ_FAILED'
    } finally {
        if ($null -ne $sha) { $sha.Dispose() }
        if ($null -ne $stream) { $stream.Dispose() }
    }
}

function Get-DPAArtifactSetSha256 {
    param([Parameter(Mandatory = $true)]$Records)
    [string[]]$paths = @($Records.Keys)
    [Array]::Sort($paths, [StringComparer]::Ordinal)
    $memory = New-Object IO.MemoryStream
    $sha = $null
    try {
        [byte[]]$prefix = $script:Utf8NoBom.GetBytes(
            "freeagent-artifact-set-v1`0"
        )
        $memory.Write($prefix, 0, $prefix.Length)
        [int64]$totalBytes = 0
        foreach ($path in $paths) {
            $record = $Records[$path]
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
            $memory.Write(
                [byte[]]$record.Sha256Bytes,
                0,
                ([byte[]]$record.Sha256Bytes).Length
            )
            $totalBytes += [int64]$record.Length
        }
        [byte[]]$total = [BitConverter]::GetBytes($totalBytes)
        if ([BitConverter]::IsLittleEndian) { [Array]::Reverse($total) }
        $memory.Write($total, 0, $total.Length)
        $memory.Position = 0
        $sha = [Security.Cryptography.SHA256]::Create()
        return ConvertTo-DPALowerHex -Bytes ([byte[]]$sha.ComputeHash($memory))
    } finally {
        if ($null -ne $sha) { $sha.Dispose() }
        $memory.Dispose()
    }
}

function Read-DPAStrictUtf8 {
    param(
        [Parameter(Mandatory = $true)]$Record,
        [Parameter(Mandatory = $true)][string]$Code
    )
    try {
        [byte[]]$bytes = [IO.File]::ReadAllBytes($Record.SourcePath)
        if ($bytes.Length -ne $Record.Length -or
            (ConvertTo-DPALowerHex -Bytes (Get-DPASha256Bytes -Bytes $bytes)) -cne
                $Record.Sha256 -or
            ($bytes.Length -ge 3 -and
                $bytes[0] -eq 0xef -and
                $bytes[1] -eq 0xbb -and
                $bytes[2] -eq 0xbf)) {
            Fail-DeveloperPreviewArchive -Code $Code
        }
        return $script:Utf8Strict.GetString($bytes)
    } catch {
        if ($_.Exception.Message.StartsWith(
            'DEVELOPER_PREVIEW_ARCHIVE_FAIL code=',
            [StringComparison]::Ordinal
        )) { throw }
        Fail-DeveloperPreviewArchive -Code $Code
    }
}

function Assert-DPAJsonProperty {
    param(
        [Parameter(Mandatory = $true)]$Value,
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if ($null -eq $Value) { Fail-DeveloperPreviewArchive -Code $Code }
    $property = $Value.PSObject.Properties[$Name]
    if ($null -eq $property) { Fail-DeveloperPreviewArchive -Code $Code }
    return , $property.Value
}

function Assert-DPAJsonElement {
    param(
        [Parameter(Mandatory = $true)]$Element,
        [Parameter(Mandatory = $true)][string]$Code,
        [int]$Depth = 0
    )
    if ($Depth -gt 100) {
        Fail-DeveloperPreviewArchive -Code $Code
    }
    if ($Element.ValueKind -ceq 'Object') {
        $names = New-Object 'Collections.Generic.HashSet[string]' (
            [StringComparer]::Ordinal
        )
        foreach ($property in $Element.EnumerateObject()) {
            if (-not $names.Add($property.Name)) {
                Fail-DeveloperPreviewArchive -Code $Code
            }
            Assert-DPAJsonElement `
                -Element $property.Value `
                -Code $Code `
                -Depth ($Depth + 1)
        }
        return
    }
    if ($Element.ValueKind -ceq 'Array') {
        foreach ($item in $Element.EnumerateArray()) {
            Assert-DPAJsonElement `
                -Element $item `
                -Code $Code `
                -Depth ($Depth + 1)
        }
    }
}

function ConvertFrom-DPAStrictJson {
    param(
        [Parameter(Mandatory = $true)][string]$Text,
        [Parameter(Mandatory = $true)][string]$Code
    )
    try {
        $options = [Text.Json.JsonDocumentOptions]::new()
        $options.MaxDepth = 100
        $options.AllowTrailingCommas = $false
        $options.CommentHandling = [Text.Json.JsonCommentHandling]::Disallow
        $document = [Text.Json.JsonDocument]::Parse($Text, $options)
        try {
            Assert-DPAJsonElement -Element $document.RootElement -Code $Code
        } finally {
            $document.Dispose()
        }
        return $Text | ConvertFrom-Json -Depth 100
    } catch {
        if ($_.Exception.Message.StartsWith(
            'DEVELOPER_PREVIEW_ARCHIVE_FAIL code=',
            [StringComparison]::Ordinal
        )) { throw }
        Fail-DeveloperPreviewArchive -Code $Code
    }
}

function Get-DPAArtifactState {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$ExpectedSetSha256,
        [Parameter(Mandatory = $true)][string]$Revision
    )
    if ($ExpectedSetSha256 -cnotmatch '^[0-9a-f]{64}$') {
        Fail-DeveloperPreviewArchive -Code 'DPA_SEAL_INVALID'
    }
    $full = Get-DPANormalizedAbsolutePath `
        -Value $Root `
        -Code 'DPA_ARTIFACT_ROOT_INVALID'
    Assert-DPANoReparseAncestry `
        -Path $full `
        -Code 'DPA_ARTIFACT_REPARSE'
    if (-not (Test-Path -LiteralPath $full -PathType Container)) {
        Fail-DeveloperPreviewArchive -Code 'DPA_ARTIFACT_ROOT_INVALID'
    }

    $files = New-Object 'Collections.Generic.Dictionary[string,object]' (
        [StringComparer]::Ordinal
    )
    $portableNames = New-Object 'Collections.Generic.HashSet[string]' (
        [StringComparer]::OrdinalIgnoreCase
    )
    $pending = New-Object 'Collections.Generic.Stack[string]'
    $pending.Push($full)
    while ($pending.Count -gt 0) {
        $directory = $pending.Pop()
        foreach ($entry in @(Get-ChildItem -LiteralPath $directory -Force)) {
            if (($entry.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                Fail-DeveloperPreviewArchive -Code 'DPA_ARTIFACT_REPARSE'
            }
            if ($entry -is [IO.DirectoryInfo]) {
                $pending.Push($entry.FullName)
                continue
            }
            if (-not ($entry -is [IO.FileInfo])) {
                Fail-DeveloperPreviewArchive -Code 'DPA_ARTIFACT_LAYOUT_INVALID'
            }
            $relative = Get-DPARelativePath -Root $full -Path $entry.FullName
            if (-not $portableNames.Add($relative)) {
                Fail-DeveloperPreviewArchive -Code 'DPA_PATH_COLLISION'
            }
            $files.Add(
                $relative,
                (Get-DPAFileRecord -Path $entry.FullName -RelativePath $relative)
            )
        }
    }

    [string[]]$binaryNames = @($files.Keys | Where-Object {
        $_ -cmatch '^bin/freeagent-(windows|linux|darwin)-(amd64|arm64)(?:\.exe)?$'
    })
    if ($binaryNames.Count -ne 1 -or
        $binaryNames[0] -cnotmatch
            '^bin/freeagent-(windows|linux|darwin)-(amd64|arm64)(\.exe)?$') {
        Fail-DeveloperPreviewArchive -Code 'DPA_TARGET_INVALID'
    }
    $goos = $Matches[1]
    $goarch = $Matches[2]
    $extension = [string]$Matches[3]
    if (($goos -ceq 'windows' -and $extension -cne '.exe') -or
        ($goos -cne 'windows' -and -not [string]::IsNullOrEmpty($extension))) {
        Fail-DeveloperPreviewArchive -Code 'DPA_TARGET_INVALID'
    }
    $target = "$goos-$goarch"
    $binary = "bin/freeagent-$target$extension"
    [string[]]$requiredFiles = @(
        $binary,
        'INSTALL.md',
        'KNOWN_LIMITATIONS.md',
        'LICENSE',
        'QUICKSTART.md',
        'RELEASE_NOTES.md',
        'THIRD_PARTY_NOTICES.md',
        'VERIFY_CHECKSUMS.md',
        'VERSION',
        'config/current-v1.bootstrap.seed.json',
        'config/README.md',
        'data/README.md',
        'supply-chain/checksums.sha256',
        'supply-chain/provenance.unsigned.v1.json',
        'supply-chain/sbom.spdx.json'
    )
    [string[]]$actualFiles = @($files.Keys)
    [Array]::Sort($actualFiles, [StringComparer]::Ordinal)
    foreach ($requiredFile in $requiredFiles) {
        if (-not $files.ContainsKey($requiredFile)) {
            Fail-DeveloperPreviewArchive -Code 'DPA_ARTIFACT_LAYOUT_INVALID'
        }
    }

    $checksumRecord = $files['supply-chain/checksums.sha256']
    $checksumText = Read-DPAStrictUtf8 `
        -Record $checksumRecord `
        -Code 'DPA_CHECKSUMS_INVALID'
    if ($checksumText.Contains("`r") -or
        -not $checksumText.EndsWith("`n", [StringComparison]::Ordinal)) {
        Fail-DeveloperPreviewArchive -Code 'DPA_CHECKSUMS_INVALID'
    }
    $declared = New-Object 'Collections.Generic.Dictionary[string,string]' (
        [StringComparer]::Ordinal
    )
    $declaredPortable = New-Object 'Collections.Generic.HashSet[string]' (
        [StringComparer]::OrdinalIgnoreCase
    )
    foreach ($line in $checksumText.Substring(0, $checksumText.Length - 1).Split("`n")) {
        if ($line -cnotmatch '^([0-9a-f]{64})  ([A-Za-z0-9._/-]+)$') {
            Fail-DeveloperPreviewArchive -Code 'DPA_CHECKSUMS_INVALID'
        }
        $hash = $Matches[1]
        $path = $Matches[2]
        Assert-DPAPortableRelativePath `
            -Value $path `
            -Code 'DPA_CHECKSUMS_INVALID'
        if ($path -ceq 'supply-chain/checksums.sha256' -or
            -not $declaredPortable.Add($path) -or
            $declared.ContainsKey($path)) {
            Fail-DeveloperPreviewArchive -Code 'DPA_CHECKSUMS_INVALID'
        }
        $declared.Add($path, $hash)
    }
    [string[]]$expectedDeclared = @($actualFiles | Where-Object {
        $_ -cne 'supply-chain/checksums.sha256'
    })
    [string[]]$actualDeclared = @($declared.Keys)
    [Array]::Sort($expectedDeclared, [StringComparer]::Ordinal)
    [Array]::Sort($actualDeclared, [StringComparer]::Ordinal)
    if (($expectedDeclared -join "`n") -cne ($actualDeclared -join "`n")) {
        Fail-DeveloperPreviewArchive -Code 'DPA_CHECKSUMS_INVALID'
    }
    foreach ($path in $expectedDeclared) {
        if ($declared[$path] -cne [string]$files[$path].Sha256) {
            Fail-DeveloperPreviewArchive -Code 'DPA_ARTIFACT_TAMPERED'
        }
    }

    $versionText = Read-DPAStrictUtf8 `
        -Record $files['VERSION'] `
        -Code 'DPA_VERSION_FILE_INVALID'
    if ($versionText -cne "$Version`n") {
        Fail-DeveloperPreviewArchive -Code 'DPA_VERSION_FILE_MISMATCH'
    }

    $actualSetSha256 = Get-DPAArtifactSetSha256 -Records $files
    if ($actualSetSha256 -cne $ExpectedSetSha256) {
        Fail-DeveloperPreviewArchive -Code 'DPA_SEAL_MISMATCH'
    }

    $provenanceText = Read-DPAStrictUtf8 `
        -Record $files['supply-chain/provenance.unsigned.v1.json'] `
        -Code 'DPA_PROVENANCE_INVALID'
    try {
        $provenance = ConvertFrom-DPAStrictJson `
            -Text $provenanceText `
            -Code 'DPA_PROVENANCE_INVALID'
    } catch {
        Fail-DeveloperPreviewArchive -Code 'DPA_PROVENANCE_INVALID'
    }
    if ([string](Assert-DPAJsonProperty $provenance '_type' 'DPA_PROVENANCE_INVALID') -cne
            'https://in-toto.io/Statement/v1' -or
        [string](Assert-DPAJsonProperty $provenance 'predicateType' 'DPA_PROVENANCE_INVALID') -cne
            'https://slsa.dev/provenance/v1') {
        Fail-DeveloperPreviewArchive -Code 'DPA_PROVENANCE_INVALID'
    }
    $predicate = Assert-DPAJsonProperty $provenance 'predicate' 'DPA_PROVENANCE_INVALID'
    $definition = Assert-DPAJsonProperty $predicate 'buildDefinition' 'DPA_PROVENANCE_INVALID'
    $external = Assert-DPAJsonProperty $definition 'externalParameters' 'DPA_PROVENANCE_INVALID'
    if ([string](Assert-DPAJsonProperty $external 'jobKind' 'DPA_PROVENANCE_INVALID') -cne
            'cross-build' -or
        [string](Assert-DPAJsonProperty $external 'releaseVersion' 'DPA_PROVENANCE_INVALID') -cne
            $Version -or
        [string](Assert-DPAJsonProperty $external 'revision' 'DPA_PROVENANCE_INVALID') -cne
            $Revision -or
        [string](Assert-DPAJsonProperty $external 'targetGoos' 'DPA_PROVENANCE_INVALID') -cne
            $goos -or
        [string](Assert-DPAJsonProperty $external 'targetGoarch' 'DPA_PROVENANCE_INVALID') -cne
            $goarch) {
        Fail-DeveloperPreviewArchive -Code 'DPA_PROVENANCE_MISMATCH'
    }
    $metadata = Assert-DPAJsonProperty `
        $predicate `
        'https://github.com/endview/freeagent/provenance/metadata/v1' `
        'DPA_PROVENANCE_INVALID'
    if ((Assert-DPAJsonProperty $metadata 'runSucceeded' 'DPA_PROVENANCE_INVALID') -isnot [bool] -or
        -not [bool](Assert-DPAJsonProperty $metadata 'runSucceeded' 'DPA_PROVENANCE_INVALID')) {
        Fail-DeveloperPreviewArchive -Code 'DPA_PROVENANCE_MISMATCH'
    }
    $subjectValue = Assert-DPAJsonProperty `
        $provenance 'subject' 'DPA_PROVENANCE_INVALID'
    if ($subjectValue -isnot [Array]) {
        Fail-DeveloperPreviewArchive -Code 'DPA_PROVENANCE_INVALID'
    }
    [object[]]$subjects = @($subjectValue)
    $subjectHashes = New-Object 'Collections.Generic.Dictionary[string,string]' (
        [StringComparer]::Ordinal
    )
    foreach ($subject in $subjects) {
        $name = [string](Assert-DPAJsonProperty $subject 'name' 'DPA_PROVENANCE_INVALID')
        Assert-DPAPortableRelativePath `
            -Value $name `
            -Code 'DPA_PROVENANCE_INVALID'
        $digest = Assert-DPAJsonProperty $subject 'digest' 'DPA_PROVENANCE_INVALID'
        $hash = [string](Assert-DPAJsonProperty $digest 'sha256' 'DPA_PROVENANCE_INVALID')
        if ($hash -cnotmatch '^[0-9a-f]{64}$' -or
            $subjectHashes.ContainsKey($name)) {
            Fail-DeveloperPreviewArchive -Code 'DPA_PROVENANCE_INVALID'
        }
        $subjectHashes.Add($name, $hash)
    }
    [string[]]$expectedSubjects = @($actualFiles | Where-Object {
        $_ -cne 'supply-chain/checksums.sha256' -and
        $_ -cne 'supply-chain/provenance.unsigned.v1.json'
    })
    [string[]]$actualSubjects = @($subjectHashes.Keys)
    [Array]::Sort($expectedSubjects, [StringComparer]::Ordinal)
    [Array]::Sort($actualSubjects, [StringComparer]::Ordinal)
    if (($expectedSubjects -join "`n") -cne ($actualSubjects -join "`n")) {
        Fail-DeveloperPreviewArchive -Code 'DPA_PROVENANCE_INVALID'
    }
    foreach ($path in $expectedSubjects) {
        if ($subjectHashes[$path] -cne [string]$files[$path].Sha256) {
            Fail-DeveloperPreviewArchive -Code 'DPA_PROVENANCE_MISMATCH'
        }
    }

    $sbomText = Read-DPAStrictUtf8 `
        -Record $files['supply-chain/sbom.spdx.json'] `
        -Code 'DPA_SBOM_INVALID'
    try {
        $sbom = ConvertFrom-DPAStrictJson `
            -Text $sbomText `
            -Code 'DPA_SBOM_INVALID'
    } catch {
        Fail-DeveloperPreviewArchive -Code 'DPA_SBOM_INVALID'
    }
    if ([string](Assert-DPAJsonProperty $sbom 'spdxVersion' 'DPA_SBOM_INVALID') -cne
            'SPDX-2.3') {
        Fail-DeveloperPreviewArchive -Code 'DPA_SBOM_INVALID'
    }
    $packageValue = Assert-DPAJsonProperty $sbom 'packages' 'DPA_SBOM_INVALID'
    if ($packageValue -isnot [Array]) {
        Fail-DeveloperPreviewArchive -Code 'DPA_SBOM_INVALID'
    }
    [object[]]$packages = @($packageValue)
    [object[]]$freeAgentPackages = @($packages | Where-Object {
        [string](Assert-DPAJsonProperty $_ 'SPDXID' 'DPA_SBOM_INVALID') -ceq
            'SPDXRef-Package-FreeAgent'
    })
    if ($freeAgentPackages.Count -ne 1 -or
        [string](Assert-DPAJsonProperty `
            $freeAgentPackages[0] 'versionInfo' 'DPA_SBOM_INVALID') -cne
            $Version) {
        Fail-DeveloperPreviewArchive -Code 'DPA_SBOM_MISMATCH'
    }

    return [pscustomobject]@{
        Root = $full
        Target = $target
        Goos = $goos
        Goarch = $goarch
        BinaryRelativePath = $binary
        ArtifactSetSha256 = $actualSetSha256
        Files = $files
    }
}

function Copy-DPARecordToStream {
    param(
        [Parameter(Mandatory = $true)]$Record,
        [Parameter(Mandatory = $true)][IO.Stream]$Output
    )
    if ($null -ne $Record.Bytes) {
        if ($Record.Bytes.Length -ne $Record.Length) {
            Fail-DeveloperPreviewArchive -Code 'DPA_SOURCE_CHANGED'
        }
        $Output.Write($Record.Bytes, 0, $Record.Bytes.Length)
        return
    }
    Assert-DPANoReparseAncestry `
        -Path $Record.SourcePath `
        -Code 'DPA_SOURCE_REPARSE'
    $input = $null
    $sha = $null
    try {
        $input = New-Object IO.FileStream(
            $Record.SourcePath,
            [IO.FileMode]::Open,
            [IO.FileAccess]::Read,
            [IO.FileShare]::Read,
            1048576,
            [IO.FileOptions]::SequentialScan
        )
        $sha = [Security.Cryptography.SHA256]::Create()
        [uint32]$crcState = [uint32]::MaxValue
        [byte[]]$buffer = New-Object byte[] 1048576
        [int64]$written = 0
        while (($read = $input.Read($buffer, 0, $buffer.Length)) -gt 0) {
            $Output.Write($buffer, 0, $read)
            [void]$sha.TransformBlock($buffer, 0, $read, $null, 0)
            $crcState = [DeveloperPreviewArchiveCrc32]::Update(
                $crcState, $buffer, 0, $read
            )
            $written += $read
        }
        [void]$sha.TransformFinalBlock((New-Object byte[] 0), 0, 0)
        $actualCrc = [uint32]($crcState -bxor [uint32]::MaxValue)
        $actualHash = ConvertTo-DPALowerHex -Bytes ([byte[]]$sha.Hash)
        if ($written -ne $Record.Length -or
            $input.Length -ne $Record.Length -or
            $actualHash -cne $Record.Sha256 -or
            $actualCrc -ne [uint32]$Record.Crc32) {
            Fail-DeveloperPreviewArchive -Code 'DPA_SOURCE_CHANGED'
        }
    } catch {
        if ($_.Exception.Message.StartsWith(
            'DEVELOPER_PREVIEW_ARCHIVE_FAIL code=',
            [StringComparison]::Ordinal
        )) { throw }
        Fail-DeveloperPreviewArchive -Code 'DPA_SOURCE_READ_FAILED'
    } finally {
        if ($null -ne $sha) { $sha.Dispose() }
        if ($null -ne $input) { $input.Dispose() }
    }
}

function Assert-DPAArchiveEntries {
    param([Parameter(Mandatory = $true)][object[]]$Entries)
    $ordinal = New-Object 'Collections.Generic.HashSet[string]' (
        [StringComparer]::Ordinal
    )
    $portable = New-Object 'Collections.Generic.HashSet[string]' (
        [StringComparer]::OrdinalIgnoreCase
    )
    $previous = ''
    foreach ($entry in $Entries) {
        Assert-DPAPortableRelativePath `
            -Value ([string]$entry.ArchivePath) `
            -Code 'DPA_ARCHIVE_PATH_INVALID'
        if (-not $ordinal.Add([string]$entry.ArchivePath) -or
            -not $portable.Add([string]$entry.ArchivePath) -or
            (-not [string]::IsNullOrEmpty($previous) -and
                [StringComparer]::Ordinal.Compare(
                    $previous,
                    [string]$entry.ArchivePath
                ) -ge 0)) {
            Fail-DeveloperPreviewArchive -Code 'DPA_PATH_COLLISION'
        }
        $previous = [string]$entry.ArchivePath
    }
}

function Write-DPAZipArchive {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][object[]]$Entries
    )
    Assert-DPAArchiveEntries -Entries $Entries
    $stream = $null
    $writer = $null
    try {
        $stream = New-Object IO.FileStream(
            $Path,
            [IO.FileMode]::CreateNew,
            [IO.FileAccess]::ReadWrite,
            [IO.FileShare]::None
        )
        $writer = New-Object IO.BinaryWriter($stream, $script:Utf8NoBom, $true)
        $central = New-Object 'Collections.Generic.List[object]'
        foreach ($entry in $Entries) {
            [byte[]]$name = $script:Utf8NoBom.GetBytes($entry.ArchivePath)
            if ($name.Length -gt [uint16]::MaxValue -or
                $entry.Record.Length -gt [uint32]::MaxValue -or
                $stream.Position -gt [uint32]::MaxValue) {
                Fail-DeveloperPreviewArchive -Code 'DPA_ARCHIVE_TOO_LARGE'
            }
            [uint32]$offset = [uint32]$stream.Position
            $writer.Write([uint32]0x04034b50)
            $writer.Write([uint16]20)
            $writer.Write([uint16]0x0800)
            $writer.Write([uint16]0)
            $writer.Write([uint16]0)
            $writer.Write([uint16]10273)
            $writer.Write([uint32]$entry.Record.Crc32)
            $writer.Write([uint32]$entry.Record.Length)
            $writer.Write([uint32]$entry.Record.Length)
            $writer.Write([uint16]$name.Length)
            $writer.Write([uint16]0)
            $writer.Write($name)
            Copy-DPARecordToStream -Record $entry.Record -Output $stream
            $central.Add([pscustomobject]@{
                Name = $name
                Entry = $entry
                Offset = $offset
            })
        }
        [uint32]$centralOffset = [uint32]$stream.Position
        foreach ($item in $central) {
            $entry = $item.Entry
            $writer.Write([uint32]0x02014b50)
            $writer.Write([uint16]0x0314)
            $writer.Write([uint16]20)
            $writer.Write([uint16]0x0800)
            $writer.Write([uint16]0)
            $writer.Write([uint16]0)
            $writer.Write([uint16]10273)
            $writer.Write([uint32]$entry.Record.Crc32)
            $writer.Write([uint32]$entry.Record.Length)
            $writer.Write([uint32]$entry.Record.Length)
            $writer.Write([uint16]$item.Name.Length)
            $writer.Write([uint16]0)
            $writer.Write([uint16]0)
            $writer.Write([uint16]0)
            $writer.Write([uint16]0)
            [uint32]$externalAttributes = 2175008768
            if ($entry.Executable) { $externalAttributes = 2179792896 }
            $writer.Write($externalAttributes)
            $writer.Write([uint32]$item.Offset)
            $writer.Write([byte[]]$item.Name)
        }
        [uint32]$centralSize = [uint32]($stream.Position - $centralOffset)
        if ($central.Count -gt [uint16]::MaxValue) {
            Fail-DeveloperPreviewArchive -Code 'DPA_ARCHIVE_TOO_LARGE'
        }
        $writer.Write([uint32]0x06054b50)
        $writer.Write([uint16]0)
        $writer.Write([uint16]0)
        $writer.Write([uint16]$central.Count)
        $writer.Write([uint16]$central.Count)
        $writer.Write($centralSize)
        $writer.Write($centralOffset)
        $writer.Write([uint16]0)
        $writer.Flush()
        $stream.Flush($true)
    } catch {
        if ($_.Exception.Message.StartsWith(
            'DEVELOPER_PREVIEW_ARCHIVE_FAIL code=',
            [StringComparison]::Ordinal
        )) { throw }
        Write-Verbose (
            'DPA zip writer exception: ' + $_.Exception.GetType().FullName +
            ': ' + $_.Exception.Message
        )
        Fail-DeveloperPreviewArchive -Code 'DPA_ARCHIVE_WRITE_FAILED'
    } finally {
        if ($null -ne $writer) { $writer.Dispose() }
        if ($null -ne $stream) { $stream.Dispose() }
    }
}

function Set-DPATarAscii {
    param(
        [Parameter(Mandatory = $true)][byte[]]$Header,
        [Parameter(Mandatory = $true)][int]$Offset,
        [Parameter(Mandatory = $true)][int]$Length,
        [Parameter(Mandatory = $true)][string]$Value
    )
    [byte[]]$bytes = [Text.Encoding]::ASCII.GetBytes($Value)
    if ($bytes.Length -gt $Length) {
        Fail-DeveloperPreviewArchive -Code 'DPA_ARCHIVE_PATH_INVALID'
    }
    [Array]::Copy($bytes, 0, $Header, $Offset, $bytes.Length)
}

function Set-DPATarOctal {
    param(
        [Parameter(Mandatory = $true)][byte[]]$Header,
        [Parameter(Mandatory = $true)][int]$Offset,
        [Parameter(Mandatory = $true)][int]$Length,
        [Parameter(Mandatory = $true)][int64]$Value
    )
    $digits = [Convert]::ToString($Value, 8)
    if ($digits.Length -gt $Length - 1) {
        Fail-DeveloperPreviewArchive -Code 'DPA_ARCHIVE_TOO_LARGE'
    }
    $text = $digits.PadLeft($Length - 1, '0') + [char]0
    Set-DPATarAscii -Header $Header -Offset $Offset -Length $Length -Value $text
}

function New-DPATarHeader {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][int64]$Length,
        [Parameter(Mandatory = $true)][bool]$Executable
    )
    [byte[]]$header = New-Object byte[] 512
    $namePart = $Name
    $prefixPart = ''
    if ([Text.Encoding]::ASCII.GetByteCount($namePart) -gt 100) {
        $separator = $Name.LastIndexOf('/')
        while ($separator -gt 0) {
            $prefix = $Name.Substring(0, $separator)
            $leaf = $Name.Substring($separator + 1)
            if ([Text.Encoding]::ASCII.GetByteCount($prefix) -le 155 -and
                [Text.Encoding]::ASCII.GetByteCount($leaf) -le 100) {
                $prefixPart = $prefix
                $namePart = $leaf
                break
            }
            $separator = $Name.LastIndexOf('/', $separator - 1)
        }
        if ([string]::IsNullOrEmpty($prefixPart)) {
            Fail-DeveloperPreviewArchive -Code 'DPA_ARCHIVE_PATH_INVALID'
        }
    }
    Set-DPATarAscii $header 0 100 $namePart
    [int64]$mode = 420
    if ($Executable) { $mode = 493 }
    Set-DPATarOctal $header 100 8 $mode
    Set-DPATarOctal $header 108 8 0
    Set-DPATarOctal $header 116 8 0
    Set-DPATarOctal $header 124 12 $Length
    Set-DPATarOctal $header 136 12 $script:FixedUnixTime
    for ($index = 148; $index -lt 156; $index++) { $header[$index] = 32 }
    $header[156] = [byte][char]'0'
    Set-DPATarAscii $header 257 6 ("ustar" + [char]0)
    Set-DPATarAscii $header 263 2 '00'
    Set-DPATarAscii $header 265 32 'root'
    Set-DPATarAscii $header 297 32 'root'
    if (-not [string]::IsNullOrEmpty($prefixPart)) {
        Set-DPATarAscii $header 345 155 $prefixPart
    }
    [int64]$sum = 0
    foreach ($value in $header) { $sum += $value }
    $checksum = [Convert]::ToString($sum, 8).PadLeft(6, '0')
    Set-DPATarAscii $header 148 6 $checksum
    $header[154] = 0
    $header[155] = 32
    return ,$header
}

function Write-DPATarFile {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][object[]]$Entries
    )
    Assert-DPAArchiveEntries -Entries $Entries
    $stream = $null
    try {
        $stream = New-Object IO.FileStream(
            $Path,
            [IO.FileMode]::CreateNew,
            [IO.FileAccess]::Write,
            [IO.FileShare]::None
        )
        [byte[]]$padding = New-Object byte[] 512
        foreach ($entry in $Entries) {
            [byte[]]$header = New-DPATarHeader `
                -Name $entry.ArchivePath `
                -Length $entry.Record.Length `
                -Executable ([bool]$entry.Executable)
            $stream.Write($header, 0, $header.Length)
            Copy-DPARecordToStream -Record $entry.Record -Output $stream
            $remainder = [int]($entry.Record.Length % 512)
            if ($remainder -ne 0) {
                $stream.Write($padding, 0, 512 - $remainder)
            }
        }
        $stream.Write($padding, 0, $padding.Length)
        $stream.Write($padding, 0, $padding.Length)
        $stream.Flush($true)
    } catch {
        if ($_.Exception.Message.StartsWith(
            'DEVELOPER_PREVIEW_ARCHIVE_FAIL code=',
            [StringComparison]::Ordinal
        )) { throw }
        Write-Verbose (
            'DPA tar writer exception: ' + $_.Exception.GetType().FullName +
            ': ' + $_.Exception.Message
        )
        Fail-DeveloperPreviewArchive -Code 'DPA_ARCHIVE_WRITE_FAILED'
    } finally {
        if ($null -ne $stream) { $stream.Dispose() }
    }
}

function Write-DPADeterministicGzip {
    param(
        [Parameter(Mandatory = $true)][string]$InputPath,
        [Parameter(Mandatory = $true)][string]$OutputPath
    )
    $record = Get-DPAFileRecord -Path $InputPath -RelativePath 'payload.tar'
    if ($record.Length -gt [uint32]::MaxValue) {
        Fail-DeveloperPreviewArchive -Code 'DPA_ARCHIVE_TOO_LARGE'
    }
    $input = $null
    $output = $null
    $writer = $null
    try {
        $input = New-Object IO.FileStream(
            $InputPath, [IO.FileMode]::Open, [IO.FileAccess]::Read,
            [IO.FileShare]::Read, 65535, [IO.FileOptions]::SequentialScan
        )
        $output = New-Object IO.FileStream(
            $OutputPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write,
            [IO.FileShare]::None
        )
        $writer = New-Object IO.BinaryWriter($output, $script:Utf8NoBom, $true)
        $writer.Write([byte[]]@(0x1f,0x8b,0x08,0x00,0x00,0x00,0x00,0x00,0x00,0xff))
        [byte[]]$buffer = New-Object byte[] 65535
        [int64]$remaining = $record.Length
        while ($remaining -gt 0) {
            $wanted = [int][Math]::Min([int64]$buffer.Length, $remaining)
            $offset = 0
            while ($offset -lt $wanted) {
                $read = $input.Read($buffer, $offset, $wanted - $offset)
                if ($read -le 0) {
                    Fail-DeveloperPreviewArchive -Code 'DPA_SOURCE_CHANGED'
                }
                $offset += $read
            }
            $remaining -= $wanted
            [byte]$finalBlock = 0
            if ($remaining -eq 0) { $finalBlock = 1 }
            $writer.Write($finalBlock)
            $writer.Write([uint16]$wanted)
            $writer.Write([uint16]([uint16]$wanted -bxor [uint16]::MaxValue))
            $writer.Write($buffer, 0, $wanted)
        }
        $writer.Write([uint32]$record.Crc32)
        $writer.Write([uint32]$record.Length)
        $writer.Flush()
        $output.Flush($true)
    } catch {
        if ($_.Exception.Message.StartsWith(
            'DEVELOPER_PREVIEW_ARCHIVE_FAIL code=',
            [StringComparison]::Ordinal
        )) { throw }
        Write-Verbose (
            'DPA gzip writer exception: ' + $_.Exception.GetType().FullName +
            ': ' + $_.Exception.Message
        )
        Fail-DeveloperPreviewArchive -Code 'DPA_ARCHIVE_WRITE_FAILED'
    } finally {
        if ($null -ne $writer) { $writer.Dispose() }
        if ($null -ne $output) { $output.Dispose() }
        if ($null -ne $input) { $input.Dispose() }
    }
}

$candidate = ''
try {
    if ($ArtifactRoot.Count -ne 6 -or
        $ExpectedArtifactSetSha256.Count -ne 6) {
        Fail-DeveloperPreviewArchive -Code 'DPA_INPUT_COUNT_INVALID'
    }
    if ($Version -cnotmatch
            '^v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$') {
        Fail-DeveloperPreviewArchive -Code 'DPA_VERSION_INVALID'
    }
    if ($Commit -cnotmatch '^[0-9a-f]{40}$') {
        Fail-DeveloperPreviewArchive -Code 'DPA_COMMIT_INVALID'
    }

    $output = Get-DPANormalizedAbsolutePath `
        -Value $OutputRoot `
        -Code 'DPA_OUTPUT_INVALID'
    Assert-DPANoReparseAncestry `
        -Path $output `
        -Code 'DPA_OUTPUT_REPARSE'
    if (Test-Path -LiteralPath $output) {
        Fail-DeveloperPreviewArchive -Code 'DPA_OUTPUT_EXISTS'
    }
    $outputParent = [IO.Path]::GetDirectoryName($output)
    if ([string]::IsNullOrWhiteSpace($outputParent) -or
        -not (Test-Path -LiteralPath $outputParent -PathType Container)) {
        Fail-DeveloperPreviewArchive -Code 'DPA_OUTPUT_PARENT_INVALID'
    }
    Assert-DPANoReparseAncestry `
        -Path $outputParent `
        -Code 'DPA_OUTPUT_REPARSE'

    $normalizedRoots = New-Object 'Collections.Generic.List[string]'
    $rootSet = New-Object 'Collections.Generic.HashSet[string]' (
        [StringComparer]::OrdinalIgnoreCase
    )
    foreach ($root in $ArtifactRoot) {
        $normalized = Get-DPANormalizedAbsolutePath `
            -Value $root `
            -Code 'DPA_ARTIFACT_ROOT_INVALID'
        if (-not $rootSet.Add($normalized)) {
            Fail-DeveloperPreviewArchive -Code 'DPA_ARTIFACT_ROOT_COLLISION'
        }
        $normalizedRoots.Add($normalized)
    }
    Assert-DPADisjointOutput -Output $output -Inputs $normalizedRoots.ToArray()

    $artifacts = New-Object 'Collections.Generic.Dictionary[string,object]' (
        [StringComparer]::Ordinal
    )
    for ($index = 0; $index -lt 6; $index++) {
        $artifact = Get-DPAArtifactState `
            -Root $normalizedRoots[$index] `
            -ExpectedSetSha256 $ExpectedArtifactSetSha256[$index] `
            -Revision $Commit
        if ($artifacts.ContainsKey($artifact.Target)) {
            Fail-DeveloperPreviewArchive -Code 'DPA_TARGET_COLLISION'
        }
        $artifacts.Add($artifact.Target, $artifact)
    }
    [string[]]$actualTargets = @($artifacts.Keys)
    [Array]::Sort($actualTargets, [StringComparer]::Ordinal)
    if (($actualTargets -join "`n") -cne
            ($script:ExpectedTargets -join "`n")) {
        Fail-DeveloperPreviewArchive -Code 'DPA_TARGET_SET_INVALID'
    }

    $candidate = Join-Path $outputParent (
        '.freeagent-developer-preview-' + [Guid]::NewGuid().ToString('N')
    )
    if (Test-Path -LiteralPath $candidate) {
        Fail-DeveloperPreviewArchive -Code 'DPA_CANDIDATE_EXISTS'
    }
    [void][IO.Directory]::CreateDirectory($candidate)
    Assert-DPANoReparseAncestry `
        -Path $candidate `
        -Code 'DPA_OUTPUT_REPARSE'

    $archiveRecords = New-Object 'Collections.Generic.List[object]'
    foreach ($target in $script:ExpectedTargets) {
        $artifact = $artifacts[$target]
        $packageName = "freeagent-$Version-$target"
        if ($BuildOnly -or $artifact.Goos -ceq 'darwin') {
            $packageName += '-build-only'
        }
        $entries = New-Object 'Collections.Generic.List[object]'
        [string[]]$artifactPaths = @($artifact.Files.Keys)
        [Array]::Sort($artifactPaths, [StringComparer]::Ordinal)
        foreach ($relative in $artifactPaths) {
            $entries.Add([pscustomobject]@{
                ArchivePath = "$packageName/$relative"
                Record = $artifact.Files[$relative]
                Executable = $relative -ceq $artifact.BinaryRelativePath
            })
        }
        [object[]]$sortedEntries = $entries.ToArray()

        if ($artifact.Goos -ceq 'windows') {
            $archiveName = "$packageName.zip"
            $archivePath = Join-Path $candidate $archiveName
            Write-DPAZipArchive -Path $archivePath -Entries $sortedEntries
        } else {
            $archiveName = "$packageName.tar.gz"
            $archivePath = Join-Path $candidate $archiveName
            $temporaryTar = Join-Path $candidate ('.' + $archiveName + '.tar.tmp')
            Write-DPATarFile -Path $temporaryTar -Entries $sortedEntries
            Write-DPADeterministicGzip `
                -InputPath $temporaryTar `
                -OutputPath $archivePath
            [IO.File]::Delete($temporaryTar)
        }
        $archiveRecords.Add(
            (Get-DPAFileRecord -Path $archivePath -RelativePath $archiveName)
        )
    }

    [object[]]$sortedArchives = $archiveRecords.ToArray()
    $manifestBuilder = New-Object Text.StringBuilder
    foreach ($record in $sortedArchives) {
        [void]$manifestBuilder.Append($record.Sha256)
        [void]$manifestBuilder.Append('  ')
        [void]$manifestBuilder.Append($record.RelativePath)
        [void]$manifestBuilder.Append("`n")
    }
    [byte[]]$manifestBytes = $script:Utf8NoBom.GetBytes(
        $manifestBuilder.ToString()
    )
    $manifestPath = Join-Path $candidate 'SHA256SUMS'
    [IO.File]::WriteAllBytes($manifestPath, $manifestBytes)

    if (Test-Path -LiteralPath $output) {
        Fail-DeveloperPreviewArchive -Code 'DPA_OUTPUT_RACE'
    }
    [IO.Directory]::Move($candidate, $output)
    $candidate = ''
    Write-Host (
        'DEVELOPER_PREVIEW_ARCHIVE_PASS ' +
        "version=$Version commit=$Commit archives=6 build_only=" +
        ([bool]$BuildOnly).ToString().ToLowerInvariant()
    )
    [pscustomobject]@{
        OutputRoot = $output
        ManifestPath = Join-Path $output 'SHA256SUMS'
        ArchiveCount = 6
        BuildOnly = [bool]$BuildOnly
    }
} catch {
    if (-not [string]::IsNullOrEmpty($candidate) -and
        (Test-Path -LiteralPath $candidate)) {
        $safeParent = [IO.Path]::GetDirectoryName($candidate)
        if (-not [string]::Equals(
            $safeParent,
            [IO.Path]::GetDirectoryName($output),
            $script:PathComparison
        ) -or
            -not ([IO.Path]::GetFileName($candidate)).StartsWith(
                '.freeagent-developer-preview-',
                [StringComparison]::Ordinal
            )) {
            throw
        }
        [IO.Directory]::Delete($candidate, $true)
    }
    if ($_.Exception.Message.StartsWith(
        'DEVELOPER_PREVIEW_ARCHIVE_FAIL code=',
        [StringComparison]::Ordinal
    )) { throw }
    Fail-DeveloperPreviewArchive -Code 'DPA_UNEXPECTED_FAILURE'
}
