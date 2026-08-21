[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$SourceRoot,
    [Parameter(Mandatory = $true)][string]$StageRoot,
    [Parameter(Mandatory = $true)][string]$ArtifactRoot
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:Utf8NoBom = New-Object System.Text.UTF8Encoding($false)
$script:IsWindowsPlatform = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
$script:ManifestName = 'public-tree-manifest.v1.json'

if ($script:IsWindowsPlatform) {
    try {
        Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Runtime.InteropServices;

public static class PublicStagingGeneratorNativeStreams
{
    private const int ErrorHandleEof = 38;
    private static readonly IntPtr InvalidHandleValue = new IntPtr(-1);

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct Win32FindStreamData
    {
        public long StreamSize;

        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 296)]
        public string StreamName;
    }

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr FindFirstStreamW(
        string fileName,
        int infoLevel,
        out Win32FindStreamData findStreamData,
        uint flags);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool FindNextStreamW(
        IntPtr findStream,
        out Win32FindStreamData findStreamData);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool FindClose(IntPtr findFile);

    public static string[] Enumerate(string path)
    {
        Win32FindStreamData data;
        IntPtr handle = FindFirstStreamW(path, 0, out data, 0);
        if (handle == InvalidHandleValue)
        {
            int firstError = Marshal.GetLastWin32Error();
            if (firstError == ErrorHandleEof)
            {
                return new string[0];
            }
            throw new Win32Exception(firstError);
        }

        var names = new List<string>();
        try
        {
            names.Add(data.StreamName);
            while (FindNextStreamW(handle, out data))
            {
                names.Add(data.StreamName);
            }

            int nextError = Marshal.GetLastWin32Error();
            if (nextError != ErrorHandleEof)
            {
                throw new Win32Exception(nextError);
            }
        }
        finally
        {
            FindClose(handle);
        }
        return names.ToArray();
    }
}
'@ -ErrorAction Stop
    } catch {
        Write-Error 'PUBLIC_STAGING_FAIL code=PST_STREAM_ENUMERATION_UNAVAILABLE'
        exit 1
    }
}

function Fail-PublicStaging {
    param([Parameter(Mandatory = $true)][string]$Code)
    throw "PUBLIC_STAGING_FAIL code=$Code"
}

function Get-Sha256Bytes {
    param([Parameter(Mandatory = $true)][AllowEmptyCollection()][byte[]]$Bytes)
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        return [byte[]]$sha.ComputeHash($Bytes)
    } finally {
        $sha.Dispose()
    }
}

function ConvertTo-Hex {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    return ([BitConverter]::ToString($Bytes)).Replace('-', '').ToLowerInvariant()
}

function Get-FileSha256 {
    param([Parameter(Mandatory = $true)][string]$Path)
    $stream = $null
    $sha = $null
    try {
        $stream = New-Object IO.FileStream(
            $Path,
            [IO.FileMode]::Open,
            [IO.FileAccess]::Read,
            [IO.FileShare]::Read,
            1048576,
            [IO.FileOptions]::SequentialScan
        )
        $sha = [Security.Cryptography.SHA256]::Create()
        return ConvertTo-Hex -Bytes ([byte[]]$sha.ComputeHash($stream))
    } catch {
        Fail-PublicStaging -Code 'PST_TREE_SCAN_FAILED'
    } finally {
        if ($null -ne $sha) { $sha.Dispose() }
        if ($null -ne $stream) { $stream.Dispose() }
    }
}

function Get-NormalizedAbsolutePath {
    param([Parameter(Mandatory = $true)][string]$Value)
    $isAbsolute = $false
    if (-not [string]::IsNullOrWhiteSpace($Value)) {
        if ($script:IsWindowsPlatform) {
            $deviceProbe = $Value.Replace([char]47, [char]92)
            $isDevicePath = $deviceProbe.StartsWith('\\?\', [StringComparison]::Ordinal) -or
                $deviceProbe.StartsWith('\\.\', [StringComparison]::Ordinal) -or
                $deviceProbe.StartsWith('\??\', [StringComparison]::Ordinal)
            $isDriveAbsolute = $Value -match '^[A-Za-z]:[\\/]'
            $isUncAbsolute = $Value -match '^[\\/]{2}[^\\/]+[\\/][^\\/]+(?:[\\/].*)?$'
            $isAbsolute = -not $isDevicePath -and ($isDriveAbsolute -or $isUncAbsolute)
        } else {
            $isAbsolute = $Value.StartsWith('/', [StringComparison]::Ordinal)
        }
    }
    if (-not $isAbsolute) {
        Fail-PublicStaging -Code 'PST_ROOT_NOT_ABSOLUTE'
    }
    try {
        $full = [IO.Path]::GetFullPath($Value)
    } catch {
        Fail-PublicStaging -Code 'PST_ROOT_NOT_ABSOLUTE'
    }
    if ($script:IsWindowsPlatform -and (
        $full.StartsWith('\\?\', [StringComparison]::Ordinal) -or
        $full.StartsWith('\\.\', [StringComparison]::Ordinal) -or
        $full.StartsWith('\??\', [StringComparison]::Ordinal)
    )) {
        Fail-PublicStaging -Code 'PST_ROOT_NOT_ABSOLUTE'
    }
    return $full.TrimEnd([char[]]@([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar))
}

function Assert-NotFileSystemRoot {
    param([Parameter(Mandatory = $true)][string]$Path)
    $pathRoot = [IO.Path]::GetPathRoot($Path)
    if ([string]::IsNullOrWhiteSpace($pathRoot)) {
        Fail-PublicStaging -Code 'PST_ROOT_NOT_ABSOLUTE'
    }
    $trimmedPath = $Path.TrimEnd([char[]]@([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar))
    $trimmedRoot = $pathRoot.TrimEnd([char[]]@([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar))
    $comparison = if ($script:IsWindowsPlatform) { [StringComparison]::OrdinalIgnoreCase } else { [StringComparison]::Ordinal }
    if ([string]::Equals($trimmedPath, $trimmedRoot, $comparison)) {
        Fail-PublicStaging -Code 'PST_ROOT_FILESYSTEM'
    }
}

function Assert-NoReparseAncestry {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $cursor = if (Test-Path -LiteralPath $Path) { $Path } else { [IO.Path]::GetDirectoryName($Path) }
    while (-not [string]::IsNullOrWhiteSpace($cursor)) {
        if (Test-Path -LiteralPath $cursor) {
            try {
                $item = Get-Item -LiteralPath $cursor -Force
            } catch {
                Fail-PublicStaging -Code $Code
            }
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                Fail-PublicStaging -Code $Code
            }
        }
        $parent = [IO.Directory]::GetParent($cursor)
        if ($null -eq $parent) { break }
        $next = $parent.FullName
        if ([string]::Equals($next, $cursor, [StringComparison]::OrdinalIgnoreCase)) { break }
        $cursor = $next
    }
}

function Test-PathInside {
    param(
        [Parameter(Mandatory = $true)][string]$Candidate,
        [Parameter(Mandatory = $true)][string]$Container
    )
    $comparison = if ($script:IsWindowsPlatform) { [StringComparison]::OrdinalIgnoreCase } else { [StringComparison]::Ordinal }
    if ([string]::Equals($Candidate, $Container, $comparison)) { return $true }
    return $Candidate.StartsWith($Container + [IO.Path]::DirectorySeparatorChar, $comparison)
}

function Assert-RootsDisjoint {
    param([Parameter(Mandatory = $true)][string[]]$Roots)
    for ($left = 0; $left -lt $Roots.Count; $left++) {
        for ($right = $left + 1; $right -lt $Roots.Count; $right++) {
            if ((Test-PathInside -Candidate $Roots[$left] -Container $Roots[$right]) -or
                (Test-PathInside -Candidate $Roots[$right] -Container $Roots[$left])) {
                Fail-PublicStaging -Code 'PST_ROOTS_NOT_DISJOINT'
            }
        }
    }
}

function Assert-NoAlternateStreams {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (-not $script:IsWindowsPlatform) { return }
    try {
        $streams = @([PublicStagingGeneratorNativeStreams]::Enumerate($Path))
    } catch {
        Fail-PublicStaging -Code 'PST_STREAM_ENUMERATION_FAILED'
    }
    foreach ($stream in $streams) {
        if ($stream -cne '::$DATA') {
            Fail-PublicStaging -Code 'PST_ADS_FORBIDDEN'
        }
    }
}

function Assert-PortableRelativePath {
    param([Parameter(Mandatory = $true)][string]$RelativePath)
    if ([string]::IsNullOrEmpty($RelativePath) -or $RelativePath.StartsWith('/', [StringComparison]::Ordinal) -or
        $RelativePath.Contains([char]92) -or [IO.Path]::IsPathRooted($RelativePath) -or
        $script:Utf8NoBom.GetByteCount($RelativePath) -gt 4096) {
        Fail-PublicStaging -Code 'PST_PATH_INVALID'
    }
    foreach ($segment in $RelativePath.Split([char]47)) {
        if ([string]::IsNullOrEmpty($segment) -or $segment -ceq '.' -or $segment -ceq '..' -or
            $segment.EndsWith('.', [StringComparison]::Ordinal) -or $segment.EndsWith(' ', [StringComparison]::Ordinal) -or
            $script:Utf8NoBom.GetByteCount($segment) -gt 255 -or
            -not [string]::Equals(
                $segment.Normalize([Text.NormalizationForm]::FormC),
                $segment,
                [StringComparison]::Ordinal
            ) -or
            $segment.IndexOfAny([char[]]@('<', '>', ':', '"', '/', [char]92, '|', '?', '*')) -ge 0) {
            Fail-PublicStaging -Code 'PST_PATH_INVALID'
        }
        foreach ($character in $segment.ToCharArray()) {
            $category = [Globalization.CharUnicodeInfo]::GetUnicodeCategory($character)
            if ($category -in @(
                [Globalization.UnicodeCategory]::Control,
                [Globalization.UnicodeCategory]::Format,
                [Globalization.UnicodeCategory]::LineSeparator,
                [Globalization.UnicodeCategory]::ParagraphSeparator,
                [Globalization.UnicodeCategory]::Surrogate
            )) {
                Fail-PublicStaging -Code 'PST_PATH_INVALID'
            }
        }
        $baseName = ($segment -split '\.', 2)[0]
        if ($baseName -match '^(?i:CON|PRN|AUX|NUL|COM[1-9¹²³]|LPT[1-9¹²³])$') {
            Fail-PublicStaging -Code 'PST_PATH_INVALID'
        }
        if ($segment -ieq '.git') {
            Fail-PublicStaging -Code 'PST_SOURCE_GIT_FORBIDDEN'
        }
    }
}

function Add-PortablePathKey {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][Collections.Generic.HashSet[string]]$Exact,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][Collections.Generic.HashSet[string]]$CaseInsensitive,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][Collections.Generic.HashSet[string]]$Nfd
    )
    if (-not $Exact.Add($Path) -or -not $CaseInsensitive.Add($Path) -or
        -not $Nfd.Add($Path.Normalize([Text.NormalizationForm]::FormD))) {
        Fail-PublicStaging -Code 'PST_PATH_COLLISION'
    }
}

function Assert-RegularFile {
    param([Parameter(Mandatory = $true)]$Item)
    if (($Item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        Fail-PublicStaging -Code 'PST_REPARSE_FORBIDDEN'
    }
    if (($Item.Attributes -band [IO.FileAttributes]::SparseFile) -ne 0) {
        Fail-PublicStaging -Code 'PST_SPARSE_FORBIDDEN'
    }
    if (-not ($Item -is [IO.FileInfo])) {
        Fail-PublicStaging -Code 'PST_SPECIAL_FILE'
    }
    if (-not $script:IsWindowsPlatform) {
        $modeProperty = $Item.PSObject.Properties['Mode']
        if ($null -ne $modeProperty) {
            $mode = [string]$modeProperty.Value
            if (-not [string]::IsNullOrEmpty($mode) -and $mode[0] -notin @('-', 'l')) {
                Fail-PublicStaging -Code 'PST_SPECIAL_FILE'
            }
        }
    } else {
        Assert-NoAlternateStreams -Path $Item.FullName
    }
}

function Get-SortedRecords {
    param([Parameter(Mandatory = $true)][AllowEmptyCollection()][object[]]$Records)
    $byKey = @{}
    [string[]]$keys = @($Records | ForEach-Object {
        $key = ConvertTo-Hex -Bytes $script:Utf8NoBom.GetBytes([string]$_.Path)
        if ($byKey.ContainsKey($key)) { Fail-PublicStaging -Code 'PST_PATH_COLLISION' }
        $byKey[$key] = $_
        $key
    })
    [Array]::Sort($keys, [StringComparer]::Ordinal)
    return @($keys | ForEach-Object { $byKey[$_] })
}

function Write-UInt64BigEndian {
    param(
        [Parameter(Mandatory = $true)][IO.Stream]$Stream,
        [Parameter(Mandatory = $true)][uint64]$Value
    )
    [byte[]]$bytes = [BitConverter]::GetBytes($Value)
    if ([BitConverter]::IsLittleEndian) { [Array]::Reverse($bytes) }
    $Stream.Write($bytes, 0, $bytes.Length)
}

function Get-DirectorySeal {
    param([Parameter(Mandatory = $true)][AllowEmptyCollection()][object[]]$Directories)
    $stream = New-Object IO.MemoryStream
    try {
        [byte[]]$domain = $script:Utf8NoBom.GetBytes("freeagent-public-directories-v1`0")
        $stream.Write($domain, 0, $domain.Length)
        Write-UInt64BigEndian -Stream $stream -Value ([uint64]$Directories.Count)
        foreach ($record in $Directories) {
            [byte[]]$pathBytes = $script:Utf8NoBom.GetBytes([string]$record.Path)
            Write-UInt64BigEndian -Stream $stream -Value ([uint64]$pathBytes.Length)
            $stream.Write($pathBytes, 0, $pathBytes.Length)
        }
        return ConvertTo-Hex -Bytes (Get-Sha256Bytes -Bytes $stream.ToArray())
    } finally {
        $stream.Dispose()
    }
}

function Get-FileTreeSeal {
    param([Parameter(Mandatory = $true)][object[]]$Files)
    $stream = New-Object IO.MemoryStream
    try {
        [byte[]]$domain = $script:Utf8NoBom.GetBytes("freeagent-public-files-v1`0")
        $stream.Write($domain, 0, $domain.Length)
        Write-UInt64BigEndian -Stream $stream -Value ([uint64]$Files.Count)
        foreach ($record in $Files) {
            [byte[]]$pathBytes = $script:Utf8NoBom.GetBytes([string]$record.Path)
            Write-UInt64BigEndian -Stream $stream -Value ([uint64]$pathBytes.Length)
            $stream.Write($pathBytes, 0, $pathBytes.Length)
            Write-UInt64BigEndian -Stream $stream -Value ([uint64]$record.Size)
            [byte[]]$hashBytes = New-Object byte[] 32
            for ($index = 0; $index -lt 32; $index++) {
                $hashBytes[$index] = [Convert]::ToByte(([string]$record.Sha256).Substring($index * 2, 2), 16)
            }
            $stream.Write($hashBytes, 0, $hashBytes.Length)
        }
        return ConvertTo-Hex -Bytes (Get-Sha256Bytes -Bytes $stream.ToArray())
    } finally {
        $stream.Dispose()
    }
}

function Get-PublicTreeState {
    param([Parameter(Mandatory = $true)][string]$TreeRoot)
    $directories = New-Object 'System.Collections.Generic.List[object]'
    $files = New-Object 'System.Collections.Generic.List[object]'
    $exact = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    $caseInsensitive = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
    $nfd = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    $scanState = [pscustomobject]@{ TotalBytes = [int64]0 }

    function Visit-PublicDirectory {
        param(
            [Parameter(Mandatory = $true)][string]$FullPath,
            [Parameter(Mandatory = $true)][AllowEmptyString()][string]$RelativePath
        )
        try {
            $directoryItem = Get-Item -LiteralPath $FullPath -Force
            $children = @(Get-ChildItem -LiteralPath $FullPath -Force)
        } catch {
            Fail-PublicStaging -Code 'PST_TREE_SCAN_FAILED'
        }
        if (($directoryItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            Fail-PublicStaging -Code 'PST_REPARSE_FORBIDDEN'
        }
        Assert-NoAlternateStreams -Path $FullPath
        if ($children.Count -eq 0) {
            Fail-PublicStaging -Code 'PST_EMPTY_DIRECTORY'
        }
        foreach ($child in $children) {
            $childRelative = if ([string]::IsNullOrEmpty($RelativePath)) {
                [string]$child.Name
            } else {
                $RelativePath + '/' + [string]$child.Name
            }
            Assert-PortableRelativePath -RelativePath $childRelative
            Add-PortablePathKey -Path $childRelative -Exact $exact -CaseInsensitive $caseInsensitive -Nfd $nfd
            if (($child.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                Fail-PublicStaging -Code 'PST_REPARSE_FORBIDDEN'
            }
            if ($child.PSIsContainer) {
                $directories.Add([pscustomobject]@{ Path = $childRelative })
                Visit-PublicDirectory -FullPath $child.FullName -RelativePath $childRelative
            } else {
                Assert-RegularFile -Item $child
                $sizeBefore = [int64]$child.Length
                $hash = Get-FileSha256 -Path $child.FullName
                try { $after = Get-Item -LiteralPath $child.FullName -Force } catch {
                    Fail-PublicStaging -Code 'PST_TREE_SCAN_FAILED'
                }
                if ([int64]$after.Length -ne $sizeBefore) {
                    Fail-PublicStaging -Code 'PST_ROOT_CHANGED'
                }
                if ([int64]::MaxValue - [int64]$scanState.TotalBytes -lt $sizeBefore) {
                    Fail-PublicStaging -Code 'PST_TREE_TOO_LARGE'
                }
                $scanState.TotalBytes = [int64]$scanState.TotalBytes + $sizeBefore
                $files.Add([pscustomobject]@{ Path = $childRelative; Size = $sizeBefore; Sha256 = $hash })
            }
        }
    }

    Visit-PublicDirectory -FullPath $TreeRoot -RelativePath ''
    [object[]]$sortedDirectories = @(Get-SortedRecords -Records $directories.ToArray())
    [object[]]$sortedFiles = @(Get-SortedRecords -Records $files.ToArray())
    return [pscustomobject]@{
        Directories = $sortedDirectories
        Files = $sortedFiles
        DirectoryCount = [int64]$sortedDirectories.Count
        FileCount = [int64]$sortedFiles.Count
        TotalBytes = [int64]$scanState.TotalBytes
        DirectorySeal = Get-DirectorySeal -Directories $sortedDirectories
        FileSeal = Get-FileTreeSeal -Files $sortedFiles
    }
}

function ConvertTo-JsonString {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value)
    $builder = New-Object Text.StringBuilder
    [void]$builder.Append('"')
    foreach ($character in $Value.ToCharArray()) {
        switch ([int][char]$character) {
            34 { [void]$builder.Append('\"'); break }
            92 { [void]$builder.Append('\\'); break }
            8 { [void]$builder.Append('\b'); break }
            9 { [void]$builder.Append('\t'); break }
            10 { [void]$builder.Append('\n'); break }
            12 { [void]$builder.Append('\f'); break }
            13 { [void]$builder.Append('\r'); break }
            default {
                if ([int][char]$character -lt 32) {
                    [void]$builder.Append(('\u{0:x4}' -f [int][char]$character))
                } else {
                    [void]$builder.Append($character)
                }
            }
        }
    }
    [void]$builder.Append('"')
    return $builder.ToString()
}

function Get-CanonicalManifestBytes {
    param([Parameter(Mandatory = $true)]$State)
    $builder = New-Object Text.StringBuilder
    function Add-Line {
        param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Line)
        [void]$builder.Append($Line)
        [void]$builder.Append([char]10)
    }
    Add-Line '{'
    Add-Line '  "schema_version": 1,'
    Add-Line '  "kind": "freeagent-public-tree-manifest",'
    Add-Line '  "path_contract": "utf8-nfc-portable-slash-relative-v1",'
    Add-Line '  "record_encoding": "length-prefixed-big-endian-v1",'
    Add-Line '  "hash_algorithm": "sha256",'
    Add-Line ('  "directory_count": ' + [string]$State.DirectoryCount + ',')
    Add-Line ('  "file_count": ' + [string]$State.FileCount + ',')
    Add-Line ('  "total_bytes": ' + [string]$State.TotalBytes + ',')
    Add-Line ('  "directory_set_seal_sha256": "' + [string]$State.DirectorySeal + '",')
    Add-Line ('  "file_tree_seal_sha256": "' + [string]$State.FileSeal + '",')
    Add-Line '  "directories": ['
    for ($index = 0; $index -lt $State.Directories.Count; $index++) {
        $suffix = if ($index -lt $State.Directories.Count - 1) { ',' } else { '' }
        Add-Line ('    ' + (ConvertTo-JsonString -Value ([string]$State.Directories[$index].Path)) + $suffix)
    }
    Add-Line '  ],'
    Add-Line '  "files": ['
    for ($index = 0; $index -lt $State.Files.Count; $index++) {
        $file = $State.Files[$index]
        Add-Line '    {'
        Add-Line ('      "path": ' + (ConvertTo-JsonString -Value ([string]$file.Path)) + ',')
        Add-Line ('      "size": ' + [string]$file.Size + ',')
        Add-Line ('      "sha256": "' + [string]$file.Sha256 + '"')
        $suffix = if ($index -lt $State.Files.Count - 1) { ',' } else { '' }
        Add-Line ('    }' + $suffix)
    }
    Add-Line '  ]'
    Add-Line '}'
    return $script:Utf8NoBom.GetBytes($builder.ToString())
}

function Test-StatesEqual {
    param(
        [Parameter(Mandatory = $true)]$Expected,
        [Parameter(Mandatory = $true)]$Actual
    )
    return $Expected.DirectoryCount -eq $Actual.DirectoryCount -and
        $Expected.FileCount -eq $Actual.FileCount -and
        $Expected.TotalBytes -eq $Actual.TotalBytes -and
        $Expected.DirectorySeal -ceq $Actual.DirectorySeal -and
        $Expected.FileSeal -ceq $Actual.FileSeal
}

function Copy-PublicFile {
    param(
        [Parameter(Mandatory = $true)][string]$Source,
        [Parameter(Mandatory = $true)][string]$Destination,
        [Parameter(Mandatory = $true)]$Expected
    )
    $input = $null
    $output = $null
    $sha = $null
    try {
        $input = New-Object IO.FileStream(
            $Source, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read,
            1048576, [IO.FileOptions]::SequentialScan
        )
        $output = New-Object IO.FileStream(
            $Destination, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None,
            1048576, [IO.FileOptions]::SequentialScan
        )
        $sha = [Security.Cryptography.SHA256]::Create()
        [byte[]]$buffer = New-Object byte[] 1048576
        [int64]$written = 0
        while (($read = $input.Read($buffer, 0, $buffer.Length)) -gt 0) {
            $output.Write($buffer, 0, $read)
            [void]$sha.TransformBlock($buffer, 0, $read, $null, 0)
            $written += $read
        }
        [void]$sha.TransformFinalBlock((New-Object byte[] 0), 0, 0)
        $output.Flush($true)
        $hash = ConvertTo-Hex -Bytes ([byte[]]$sha.Hash)
        if ($written -ne [int64]$Expected.Size -or $hash -cne [string]$Expected.Sha256) {
            Fail-PublicStaging -Code 'PST_SOURCE_CHANGED'
        }
    } catch {
        if ($_.Exception.Message.StartsWith('PUBLIC_STAGING_FAIL code=', [StringComparison]::Ordinal)) { throw }
        Fail-PublicStaging -Code 'PST_COPY_FAILED'
    } finally {
        if ($null -ne $sha) { $sha.Dispose() }
        if ($null -ne $output) { $output.Dispose() }
        if ($null -ne $input) { $input.Dispose() }
    }
}

function Assert-DirectoryEmpty {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    try {
        $entries = @(Get-ChildItem -LiteralPath $Path -Force)
    } catch {
        Fail-PublicStaging -Code $Code
    }
    if ($entries.Count -ne 0) { Fail-PublicStaging -Code $Code }
}

function Assert-ArtifactOwnsOnlyTemp {
    param(
        [Parameter(Mandatory = $true)][string]$Artifact,
        [Parameter(Mandatory = $true)][string]$TempPath
    )
    try {
        $entries = @(Get-ChildItem -LiteralPath $Artifact -Force)
    } catch {
        Fail-PublicStaging -Code 'PST_ARTIFACT_RACE'
    }
    if ($entries.Count -ne 1 -or
        -not [string]::Equals($entries[0].FullName, $TempPath, [StringComparison]::OrdinalIgnoreCase) -or
        ($entries[0].Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
        -not ($entries[0] -is [IO.FileInfo])) {
        Fail-PublicStaging -Code 'PST_ARTIFACT_RACE'
    }
    Assert-NoAlternateStreams -Path $Artifact
    Assert-NoAlternateStreams -Path $TempPath
}

try {
    $sourceFull = Get-NormalizedAbsolutePath -Value $SourceRoot
    $stageFull = Get-NormalizedAbsolutePath -Value $StageRoot
    $artifactFull = Get-NormalizedAbsolutePath -Value $ArtifactRoot
    foreach ($root in @($sourceFull, $stageFull, $artifactFull)) { Assert-NotFileSystemRoot -Path $root }
    Assert-RootsDisjoint -Roots @($sourceFull, $stageFull, $artifactFull)

    if (-not (Test-Path -LiteralPath $sourceFull -PathType Container)) {
        Fail-PublicStaging -Code 'PST_SOURCE_MISSING'
    }
    if (-not (Test-Path -LiteralPath $artifactFull -PathType Container)) {
        Fail-PublicStaging -Code 'PST_ARTIFACT_MISSING'
    }
    $stageParent = [IO.Path]::GetDirectoryName($stageFull)
    if ([string]::IsNullOrWhiteSpace($stageParent) -or -not (Test-Path -LiteralPath $stageParent -PathType Container)) {
        Fail-PublicStaging -Code 'PST_STAGE_PARENT_MISSING'
    }

    Assert-NoReparseAncestry -Path $sourceFull -Code 'PST_ROOT_REPARSE'
    Assert-NoReparseAncestry -Path $stageFull -Code 'PST_ROOT_REPARSE'
    Assert-NoReparseAncestry -Path $artifactFull -Code 'PST_ROOT_REPARSE'
    Assert-NoAlternateStreams -Path $sourceFull
    Assert-NoAlternateStreams -Path $artifactFull

    if (Test-Path -LiteralPath $stageFull) {
        if (-not (Test-Path -LiteralPath $stageFull -PathType Container)) {
            Fail-PublicStaging -Code 'PST_STAGE_NOT_DIRECTORY'
        }
        Assert-NoAlternateStreams -Path $stageFull
        Assert-DirectoryEmpty -Path $stageFull -Code 'PST_STAGE_NOT_EMPTY'
    }
    Assert-DirectoryEmpty -Path $artifactFull -Code 'PST_ARTIFACT_NOT_EMPTY'

    $sourcePre = Get-PublicTreeState -TreeRoot $sourceFull
    [byte[]]$manifestBytes = Get-CanonicalManifestBytes -State $sourcePre
    $manifestHash = ConvertTo-Hex -Bytes (Get-Sha256Bytes -Bytes $manifestBytes)

    $nonce = [Guid]::NewGuid().ToString('N')
    $candidate = Join-Path $stageParent ('.freeagent-public-staging-candidate-' + $nonce)
    if (Test-Path -LiteralPath $candidate) {
        Fail-PublicStaging -Code 'PST_CANDIDATE_EXISTS'
    }
    Assert-RootsDisjoint -Roots @($sourceFull, $artifactFull, $candidate)
    try {
        [void][IO.Directory]::CreateDirectory($candidate)
    } catch {
        Fail-PublicStaging -Code 'PST_CANDIDATE_CREATE_FAILED'
    }

    foreach ($directory in $sourcePre.Directories) {
        $relativeNative = ([string]$directory.Path).Replace([char]47, [IO.Path]::DirectorySeparatorChar)
        $destination = Join-Path $candidate $relativeNative
        if (Test-Path -LiteralPath $destination) { Fail-PublicStaging -Code 'PST_CANDIDATE_RACE' }
        try { [void][IO.Directory]::CreateDirectory($destination) } catch {
            Fail-PublicStaging -Code 'PST_COPY_FAILED'
        }
    }
    foreach ($file in $sourcePre.Files) {
        $relativeNative = ([string]$file.Path).Replace([char]47, [IO.Path]::DirectorySeparatorChar)
        Copy-PublicFile -Source (Join-Path $sourceFull $relativeNative) `
            -Destination (Join-Path $candidate $relativeNative) -Expected $file
    }

    $sourcePostCopy = Get-PublicTreeState -TreeRoot $sourceFull
    $candidateState = Get-PublicTreeState -TreeRoot $candidate
    if (-not (Test-StatesEqual -Expected $sourcePre -Actual $sourcePostCopy)) {
        Fail-PublicStaging -Code 'PST_SOURCE_CHANGED'
    }
    if (-not (Test-StatesEqual -Expected $sourcePre -Actual $candidateState)) {
        Fail-PublicStaging -Code 'PST_COPY_MISMATCH'
    }

    Assert-DirectoryEmpty -Path $artifactFull -Code 'PST_ARTIFACT_RACE'
    $manifestFinal = Join-Path $artifactFull $script:ManifestName
    if (Test-Path -LiteralPath $manifestFinal) { Fail-PublicStaging -Code 'PST_MANIFEST_EXISTS' }
    $manifestTemp = Join-Path $artifactFull ('.' + $script:ManifestName + '.' + $nonce + '.tmp')
    $manifestStream = $null
    try {
        $manifestStream = New-Object IO.FileStream(
            $manifestTemp, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None
        )
        $manifestStream.Write($manifestBytes, 0, $manifestBytes.Length)
        $manifestStream.Flush($true)
    } catch {
        Fail-PublicStaging -Code 'PST_MANIFEST_WRITE_FAILED'
    } finally {
        if ($null -ne $manifestStream) { $manifestStream.Dispose() }
    }
    if ((ConvertTo-Hex -Bytes (Get-Sha256Bytes -Bytes ([IO.File]::ReadAllBytes($manifestTemp)))) -cne $manifestHash) {
        Fail-PublicStaging -Code 'PST_MANIFEST_WRITE_FAILED'
    }
    Assert-ArtifactOwnsOnlyTemp -Artifact $artifactFull -TempPath $manifestTemp

    Assert-NoReparseAncestry -Path $stageFull -Code 'PST_STAGE_RACE'
    if (Test-Path -LiteralPath $stageFull) {
        if (-not (Test-Path -LiteralPath $stageFull -PathType Container)) {
            Fail-PublicStaging -Code 'PST_STAGE_RACE'
        }
        Assert-DirectoryEmpty -Path $stageFull -Code 'PST_STAGE_RACE'
        try {
            [IO.Directory]::Delete($stageFull, $false)
        } catch {
            Fail-PublicStaging -Code 'PST_STAGE_RACE'
        }
    }
    try {
        [IO.Directory]::Move($candidate, $stageFull)
    } catch {
        Fail-PublicStaging -Code 'PST_STAGE_RACE'
    }

    $sourceFinal = Get-PublicTreeState -TreeRoot $sourceFull
    $stageFinal = Get-PublicTreeState -TreeRoot $stageFull
    if (-not (Test-StatesEqual -Expected $sourcePre -Actual $sourceFinal)) {
        Fail-PublicStaging -Code 'PST_SOURCE_CHANGED'
    }
    if (-not (Test-StatesEqual -Expected $sourcePre -Actual $stageFinal)) {
        Fail-PublicStaging -Code 'PST_COPY_MISMATCH'
    }

    $verifier = Join-Path $PSScriptRoot 'Test-PublicStaging.ps1'
    if (-not (Test-Path -LiteralPath $verifier -PathType Leaf)) {
        Fail-PublicStaging -Code 'PST_VERIFIER_MISSING'
    }
    $powerShell = (Get-Process -Id $PID).Path
    $verifyOutput = @(& $powerShell -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $verifier `
        -Root $stageFull -ManifestPath $manifestTemp -ManifestSha256 $manifestHash 2>&1)
    if ($LASTEXITCODE -ne 0) {
        Fail-PublicStaging -Code 'PST_VERIFIER_FAILED'
    }

    $sourceBeforeCommit = Get-PublicTreeState -TreeRoot $sourceFull
    if (-not (Test-StatesEqual -Expected $sourcePre -Actual $sourceBeforeCommit)) {
        Fail-PublicStaging -Code 'PST_SOURCE_CHANGED'
    }
    Assert-ArtifactOwnsOnlyTemp -Artifact $artifactFull -TempPath $manifestTemp
    if (Test-Path -LiteralPath $manifestFinal) { Fail-PublicStaging -Code 'PST_MANIFEST_EXISTS' }
    try {
        [IO.File]::Move($manifestTemp, $manifestFinal)
    } catch {
        Fail-PublicStaging -Code 'PST_MANIFEST_COMMIT_FAILED'
    }

    Write-Host (
        'PUBLIC_STAGING_PASS ' +
        "manifest_sha256=$manifestHash " +
        "directories=$($stageFinal.DirectoryCount) " +
        "files=$($stageFinal.FileCount) " +
        "bytes=$($stageFinal.TotalBytes) " +
        "directory_seal=$($stageFinal.DirectorySeal) " +
        "file_seal=$($stageFinal.FileSeal)"
    )
} catch {
    if ($_.Exception.Message.StartsWith('PUBLIC_STAGING_FAIL code=', [StringComparison]::Ordinal)) {
        Write-Error $_.Exception.Message
    } else {
        Write-Error 'PUBLIC_STAGING_FAIL code=PST_INTERNAL'
    }
    exit 1
}
