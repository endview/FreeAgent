[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Root,
    [Parameter(Mandatory = $true)][string]$ManifestPath,
    [Parameter(Mandatory = $true)][string]$ManifestSha256
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:Utf8NoBom = New-Object System.Text.UTF8Encoding($false)
$script:StrictUtf8 = New-Object System.Text.UTF8Encoding($false, $true)
$script:IsWindowsPlatform = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
$script:ManifestName = 'public-tree-manifest.v1.json'
$script:RootSchema = @(
    'schema_version',
    'kind',
    'path_contract',
    'record_encoding',
    'hash_algorithm',
    'directory_count',
    'file_count',
    'total_bytes',
    'directory_set_seal_sha256',
    'file_tree_seal_sha256',
    'directories',
    'files'
)
$script:FileSchema = @('path', 'size', 'sha256')

if ($script:IsWindowsPlatform) {
    try {
        Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Runtime.InteropServices;

public static class PublicStagingVerifierNativeStreams
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

    private static string ToNativePath(string path)
    {
        if (path.StartsWith(@"\\?\", StringComparison.Ordinal))
        {
            return path;
        }
        if (path.StartsWith(@"\\", StringComparison.Ordinal))
        {
            return @"\\?\UNC\" + path.Substring(2);
        }
        return @"\\?\" + path;
    }

    public static string[] Enumerate(string path)
    {
        Win32FindStreamData data;
        IntPtr handle = FindFirstStreamW(ToNativePath(path), 0, out data, 0);
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
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$MissingCode,
        [switch]$RequireDirectory,
        [switch]$RequireFile
    )
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
    if ($RequireDirectory -and -not (Test-Path -LiteralPath $full -PathType Container)) {
        Fail-PublicStaging -Code $MissingCode
    }
    if ($RequireFile -and -not (Test-Path -LiteralPath $full -PathType Leaf)) {
        Fail-PublicStaging -Code $MissingCode
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
    $prefix = $Container + [IO.Path]::DirectorySeparatorChar
    return $Candidate.StartsWith($prefix, $comparison)
}

function Assert-PortableRelativePath {
    param([Parameter(Mandatory = $true)][string]$RelativePath)
    if ([string]::IsNullOrEmpty($RelativePath) -or $RelativePath.StartsWith('/', [StringComparison]::Ordinal) -or
        $RelativePath.Contains([char]92) -or [IO.Path]::IsPathRooted($RelativePath)) {
        Fail-PublicStaging -Code 'PST_PATH_INVALID'
    }
    $utf8Length = $script:Utf8NoBom.GetByteCount($RelativePath)
    if ($utf8Length -gt 4096) {
        Fail-PublicStaging -Code 'PST_PATH_INVALID'
    }
    $segments = $RelativePath.Split([char]47)
    foreach ($segment in $segments) {
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

function Assert-NoAlternateStreams {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (-not $script:IsWindowsPlatform) { return }
    try {
        $streams = @([PublicStagingVerifierNativeStreams]::Enumerate($Path))
    } catch {
        Fail-PublicStaging -Code 'PST_STREAM_ENUMERATION_FAILED'
    }
    foreach ($stream in $streams) {
        if ($stream -cne '::$DATA') {
            Fail-PublicStaging -Code 'PST_ADS_FORBIDDEN'
        }
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
        if ($byKey.ContainsKey($key)) {
            Fail-PublicStaging -Code 'PST_PATH_COLLISION'
        }
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
        } catch {
            Fail-PublicStaging -Code 'PST_TREE_SCAN_FAILED'
        }
        if (($directoryItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            Fail-PublicStaging -Code 'PST_REPARSE_FORBIDDEN'
        }
        Assert-NoAlternateStreams -Path $FullPath
        try {
            $children = @(Get-ChildItem -LiteralPath $FullPath -Force)
        } catch {
            Fail-PublicStaging -Code 'PST_TREE_SCAN_FAILED'
        }
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
                try {
                    $after = Get-Item -LiteralPath $child.FullName -Force
                } catch {
                    Fail-PublicStaging -Code 'PST_TREE_SCAN_FAILED'
                }
                if ([int64]$after.Length -ne $sizeBefore) {
                    Fail-PublicStaging -Code 'PST_ROOT_CHANGED'
                }
                if ([int64]::MaxValue - [int64]$scanState.TotalBytes -lt $sizeBefore) {
                    Fail-PublicStaging -Code 'PST_TREE_TOO_LARGE'
                }
                $scanState.TotalBytes = [int64]$scanState.TotalBytes + $sizeBefore
                $files.Add([pscustomobject]@{
                    Path = $childRelative
                    Size = $sizeBefore
                    Sha256 = $hash
                })
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

function Assert-NoDuplicateJsonKeys {
    param([Parameter(Mandatory = $true)][string]$Text)
    $stack = New-Object 'System.Collections.Generic.Stack[System.Collections.Generic.HashSet[string]]'
    for ($index = 0; $index -lt $Text.Length; $index++) {
        $character = $Text[$index]
        if ($character -eq '{') {
            $stack.Push((New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)))
            continue
        }
        if ($character -eq '}') {
            if ($stack.Count -gt 0) { [void]$stack.Pop() }
            continue
        }
        if ($character -ne '"') { continue }

        $decoded = New-Object Text.StringBuilder
        $index++
        while ($index -lt $Text.Length) {
            $current = $Text[$index]
            if ($current -eq '"') { break }
            if ($current -ne [char]92) {
                [void]$decoded.Append($current)
                $index++
                continue
            }
            $index++
            if ($index -ge $Text.Length) { break }
            $escape = $Text[$index]
            switch ($escape) {
                '"' { [void]$decoded.Append('"') }
                '\' { [void]$decoded.Append([char]92) }
                '/' { [void]$decoded.Append('/') }
                'b' { [void]$decoded.Append([char]8) }
                'f' { [void]$decoded.Append([char]12) }
                'n' { [void]$decoded.Append([char]10) }
                'r' { [void]$decoded.Append([char]13) }
                't' { [void]$decoded.Append([char]9) }
                'u' {
                    if ($index + 4 -ge $Text.Length) { break }
                    $hex = $Text.Substring($index + 1, 4)
                    if ($hex -notmatch '^[0-9A-Fa-f]{4}$') { break }
                    [void]$decoded.Append([char][Convert]::ToUInt16($hex, 16))
                    $index += 4
                }
            }
            $index++
        }
        $lookahead = $index + 1
        while ($lookahead -lt $Text.Length -and [char]::IsWhiteSpace($Text[$lookahead])) { $lookahead++ }
        if ($lookahead -lt $Text.Length -and $Text[$lookahead] -eq ':' -and $stack.Count -gt 0) {
            if (-not $stack.Peek().Add($decoded.ToString())) {
                Fail-PublicStaging -Code 'PST_MANIFEST_JSON_DUPLICATE_KEY'
            }
        }
    }
}

function Assert-ExactProperties {
    param(
        [Parameter(Mandatory = $true)]$Object,
        [Parameter(Mandatory = $true)][string[]]$Expected
    )
    if ($null -eq $Object) { Fail-PublicStaging -Code 'PST_MANIFEST_SCHEMA' }
    [string[]]$actual = @($Object.PSObject.Properties | ForEach-Object { $_.Name })
    [string[]]$expectedCopy = @($Expected)
    [Array]::Sort($actual, [StringComparer]::Ordinal)
    [Array]::Sort($expectedCopy, [StringComparer]::Ordinal)
    if (($actual -join "`n") -cne ($expectedCopy -join "`n")) {
        Fail-PublicStaging -Code 'PST_MANIFEST_SCHEMA'
    }
}

function Test-IntegerValue {
    param($Value)
    return ($Value -is [int] -or $Value -is [long]) -and [int64]$Value -ge 0
}

function Assert-ManifestSchema {
    param([Parameter(Mandatory = $true)]$Manifest)
    Assert-ExactProperties -Object $Manifest -Expected $script:RootSchema
    if (-not (Test-IntegerValue $Manifest.schema_version) -or [int64]$Manifest.schema_version -ne 1 -or
        $Manifest.kind -isnot [string] -or [string]$Manifest.kind -cne 'freeagent-public-tree-manifest' -or
        $Manifest.path_contract -isnot [string] -or [string]$Manifest.path_contract -cne 'utf8-nfc-portable-slash-relative-v1' -or
        $Manifest.record_encoding -isnot [string] -or [string]$Manifest.record_encoding -cne 'length-prefixed-big-endian-v1' -or
        $Manifest.hash_algorithm -isnot [string] -or [string]$Manifest.hash_algorithm -cne 'sha256' -or
        -not (Test-IntegerValue $Manifest.directory_count) -or
        -not (Test-IntegerValue $Manifest.file_count) -or
        -not (Test-IntegerValue $Manifest.total_bytes) -or
        $Manifest.directory_set_seal_sha256 -isnot [string] -or [string]$Manifest.directory_set_seal_sha256 -cnotmatch '^[0-9a-f]{64}$' -or
        $Manifest.file_tree_seal_sha256 -isnot [string] -or [string]$Manifest.file_tree_seal_sha256 -cnotmatch '^[0-9a-f]{64}$' -or
        $Manifest.directories -isnot [System.Array] -or $Manifest.files -isnot [System.Array]) {
        Fail-PublicStaging -Code 'PST_MANIFEST_SCHEMA'
    }
    if ([int64]$Manifest.directory_count -ne $Manifest.directories.Count -or
        [int64]$Manifest.file_count -ne $Manifest.files.Count) {
        Fail-PublicStaging -Code 'PST_MANIFEST_SCHEMA'
    }
    $exact = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    $caseInsensitive = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
    $nfd = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    foreach ($directory in $Manifest.directories) {
        if ($directory -isnot [string]) { Fail-PublicStaging -Code 'PST_MANIFEST_SCHEMA' }
        Assert-PortableRelativePath -RelativePath ([string]$directory)
        Add-PortablePathKey -Path ([string]$directory) -Exact $exact -CaseInsensitive $caseInsensitive -Nfd $nfd
    }
    [int64]$sum = 0
    foreach ($file in $Manifest.files) {
        Assert-ExactProperties -Object $file -Expected $script:FileSchema
        if ($file.path -isnot [string] -or -not (Test-IntegerValue $file.size) -or
            $file.sha256 -isnot [string] -or [string]$file.sha256 -cnotmatch '^[0-9a-f]{64}$') {
            Fail-PublicStaging -Code 'PST_MANIFEST_SCHEMA'
        }
        Assert-PortableRelativePath -RelativePath ([string]$file.path)
        Add-PortablePathKey -Path ([string]$file.path) -Exact $exact -CaseInsensitive $caseInsensitive -Nfd $nfd
        if ([int64]::MaxValue - $sum -lt [int64]$file.size) {
            Fail-PublicStaging -Code 'PST_MANIFEST_SCHEMA'
        }
        $sum += [int64]$file.size
    }
    if ($sum -ne [int64]$Manifest.total_bytes) {
        Fail-PublicStaging -Code 'PST_MANIFEST_SCHEMA'
    }
}

try {
    if ($ManifestSha256 -cnotmatch '^[0-9a-f]{64}$') {
        Fail-PublicStaging -Code 'PST_MANIFEST_HASH_INVALID'
    }
    $rootFull = Get-NormalizedAbsolutePath -Value $Root -MissingCode 'PST_ROOT_MISSING' -RequireDirectory
    $manifestFull = Get-NormalizedAbsolutePath -Value $ManifestPath -MissingCode 'PST_MANIFEST_MISSING' -RequireFile
    Assert-NotFileSystemRoot -Path $rootFull
    Assert-NoReparseAncestry -Path $rootFull -Code 'PST_ROOT_REPARSE'
    Assert-NoReparseAncestry -Path $manifestFull -Code 'PST_MANIFEST_UNSAFE'
    if (Test-PathInside -Candidate $manifestFull -Container $rootFull) {
        Fail-PublicStaging -Code 'PST_MANIFEST_UNSAFE'
    }
    Assert-NoAlternateStreams -Path $manifestFull

    [byte[]]$manifestBytesBegin = [IO.File]::ReadAllBytes($manifestFull)
    $manifestHashBegin = ConvertTo-Hex -Bytes (Get-Sha256Bytes -Bytes $manifestBytesBegin)
    if ($manifestHashBegin -cne $ManifestSha256) {
        Fail-PublicStaging -Code 'PST_MANIFEST_HASH_MISMATCH'
    }
    if ($manifestBytesBegin.Length -ge 3 -and $manifestBytesBegin[0] -eq 0xEF -and
        $manifestBytesBegin[1] -eq 0xBB -and $manifestBytesBegin[2] -eq 0xBF) {
        Fail-PublicStaging -Code 'PST_MANIFEST_UTF8'
    }
    if ($manifestBytesBegin.Length -eq 0 -or $manifestBytesBegin[$manifestBytesBegin.Length - 1] -ne 10 -or
        [Array]::IndexOf($manifestBytesBegin, [byte]13) -ge 0) {
        Fail-PublicStaging -Code 'PST_MANIFEST_FORMAT'
    }
    try {
        $manifestText = $script:StrictUtf8.GetString($manifestBytesBegin)
    } catch {
        Fail-PublicStaging -Code 'PST_MANIFEST_UTF8'
    }

    Assert-NoDuplicateJsonKeys -Text $manifestText
    try {
        $manifest = $manifestText | ConvertFrom-Json
    } catch {
        Fail-PublicStaging -Code 'PST_MANIFEST_JSON'
    }
    Assert-ManifestSchema -Manifest $manifest

    $treeBegin = Get-PublicTreeState -TreeRoot $rootFull
    [byte[]]$expectedCanonical = Get-CanonicalManifestBytes -State $treeBegin
    if ([Convert]::ToBase64String($expectedCanonical) -cne [Convert]::ToBase64String($manifestBytesBegin)) {
        Fail-PublicStaging -Code 'PST_MANIFEST_CONTENT_MISMATCH'
    }

    $treeEnd = Get-PublicTreeState -TreeRoot $rootFull
    if ($treeBegin.DirectorySeal -cne $treeEnd.DirectorySeal -or
        $treeBegin.FileSeal -cne $treeEnd.FileSeal -or
        $treeBegin.DirectoryCount -ne $treeEnd.DirectoryCount -or
        $treeBegin.FileCount -ne $treeEnd.FileCount -or
        $treeBegin.TotalBytes -ne $treeEnd.TotalBytes) {
        Fail-PublicStaging -Code 'PST_ROOT_CHANGED'
    }
    [byte[]]$manifestBytesEnd = [IO.File]::ReadAllBytes($manifestFull)
    $manifestHashEnd = ConvertTo-Hex -Bytes (Get-Sha256Bytes -Bytes $manifestBytesEnd)
    if ($manifestHashEnd -cne $manifestHashBegin -or
        [Convert]::ToBase64String($manifestBytesEnd) -cne [Convert]::ToBase64String($manifestBytesBegin)) {
        Fail-PublicStaging -Code 'PST_MANIFEST_CHANGED'
    }

    Write-Host (
        'PUBLIC_STAGING_VERIFY_PASS ' +
        "manifest_sha256=$manifestHashBegin " +
        "directories=$($treeEnd.DirectoryCount) " +
        "files=$($treeEnd.FileCount) " +
        "bytes=$($treeEnd.TotalBytes) " +
        "directory_seal=$($treeEnd.DirectorySeal) " +
        "file_seal=$($treeEnd.FileSeal)"
    )
} catch {
    if ($_.Exception.Message.StartsWith('PUBLIC_STAGING_FAIL code=', [StringComparison]::Ordinal)) {
        Write-Error $_.Exception.Message
    } else {
        Write-Error 'PUBLIC_STAGING_FAIL code=PST_INTERNAL'
    }
    exit 1
}
