[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Root,
    [string]$GoCommand
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$expectedProjectModule = 'github.com/endview/freeagent'
$expectedLicenseBytes = 34523
$expectedLicenseSHA256 = '8486a10c4393cee1c25392769ddd3b2d6c242d6ec7928e1414efff7dfb2f07ef'
$dependencyManifestRelative = 'testdata/release/dependency-licenses.v1.json'
$frontendDependencyManifestRelative = 'testdata/release/frontend-dependency-licenses.v1.json'
$assetManifestRelative = 'testdata/release/distributed-assets.v1.json'
$noticesRelative = 'THIRD_PARTY_NOTICES.md'
$projectAssetSource = 'project://github.com/endview/freeagent'
$expectedDependencyManifestSHA256 = 'c0b1816f70e8ba0f2e9313447b9b8d9ef5baa0312950862c7f26bf129587bf37'
$expectedFrontendDependencyManifestSHA256 = '55c35096c30631dc86950841891c8efe3f577bdd64055a63989e904a7bd2ab03'
$dependencyCompatibilityPolicyID = 'freeagent-agpl-3.0-unvendored-source-six-cgo0-binaries'
$dependencyCompatibilityPolicyVersion = 1
$sourceSPDXScanAlgorithm = 'raw-ascii-marker-path-line-v1'
$sourceSPDXMarker = 'SPDX-License-Identifier:'
$approvedThirdPartySPDX = @(
    'Apache-2.0',
    'BSD-2-Clause',
    'BSD-3-Clause',
    'ISC',
    'MIT',
    'Unicode-3.0',
    'Zlib'
)
$approvedDeclaredLicenseAtoms = @(
    'Apache-2.0',
    'BSD-2-Clause',
    'BSD-3-Clause',
    'CC-BY-2.5',
    'CC-BY-4.0',
    'CC0-1.0',
    'GPL-2.0-only',
    'ISC',
    'MIT',
    'MPL-2.0',
    'Unicode-3.0',
    'Zlib'
)
$approvedDetectedSPDXExpressions = @(
    '(BSD-3-Clause AND ISC)',
    '(BSD-4-Clause AND BSD-2-Clause-FreeBSD)',
    'Apache-2.0 WITH LLVM-exception',
    'BSD-2-Clause',
    'BSD-2-Clause-FreeBSD',
    'BSD-2-Clause-NetBSD',
    'BSD-3-Clause',
    'BSD-4-Clause',
    'GPL-2.0 WITH Linux-syscall-note',
    'GPL-2.0+ WITH Linux-syscall-note',
    'GPL-2.0-only WITH Linux-syscall-note',
    'MIT',
    'MPL-2.0'
)
$assetRoots = @('examples', 'schemas', 'testdata', 'third_party', 'vendor', 'docs/assets', 'internal/currentstore/migrations', 'internal/controlweb/dist')
$assetControlFiles = @($dependencyManifestRelative, $frontendDependencyManifestRelative, $assetManifestRelative)
$script:isWindowsPlatform = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
$script:isLinuxPlatform = $false
if (-not $script:isWindowsPlatform) {
    try {
        $script:isLinuxPlatform = [Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([Runtime.InteropServices.OSPlatform]::Linux)
    } catch {
        # Unknown non-Windows runtimes are treated conservatively and must pass the Linux probe.
        $script:isLinuxPlatform = $true
    }
}
$pathComparison = if ($script:isWindowsPlatform) { [StringComparison]::OrdinalIgnoreCase } else { [StringComparison]::Ordinal }

function Fail([string]$Code, [string]$Message) {
    throw "[$Code] $Message"
}

function Get-SHA256Bytes([byte[]]$Bytes) {
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        return ([BitConverter]::ToString($sha.ComputeHash($Bytes))).Replace('-', '').ToLowerInvariant()
    } finally {
        $sha.Dispose()
    }
}

function Assert-LinuxRegularFileForRead([string]$Path, [string]$Code, [string]$Label) {
    if (-not $script:isLinuxPlatform) { return }
    $display = if ([string]::IsNullOrWhiteSpace($Label)) { $Path } else { $Label }
    $process = New-Object Diagnostics.Process
    try {
        $start = New-Object Diagnostics.ProcessStartInfo
        $start.FileName = '/usr/bin/stat'
        $start.UseShellExecute = $false
        $start.CreateNoWindow = $true
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $true
        if ($null -eq $start.ArgumentList) { Fail $Code "$display could not be classified as a regular file" }
        [void]$start.ArgumentList.Add('--format=%F')
        [void]$start.ArgumentList.Add('--')
        [void]$start.ArgumentList.Add($Path)
        $start.EnvironmentVariables['LC_ALL'] = 'C'
        $process.StartInfo = $start
        try {
            if (-not $process.Start()) { Fail $Code "$display could not be classified as a regular file" }
        } catch {
            Fail $Code "$display could not be classified as a regular file"
        }

        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(2000)) {
            try { $process.Kill() } catch { Fail $Code "$display regular-file probe could not be terminated" }
            if (-not $process.WaitForExit(5000)) { Fail $Code "$display regular-file probe could not be terminated" }
            [void]$stdoutTask.Result
            [void]$stderrTask.Result
            Fail $Code "$display regular-file probe timed out"
        }
        $process.WaitForExit()
        $stdout = $stdoutTask.Result
        $stderr = $stderrTask.Result
        if ($process.ExitCode -ne 0 -or -not [string]::IsNullOrEmpty($stderr)) {
            Fail $Code "$display could not be classified as a regular file"
        }
        $kind = $stdout.TrimEnd([char[]]@([char]13, [char]10))
        if ($kind -cne 'regular file' -and $kind -cne 'regular empty file') {
            Fail $Code "$display must be a regular file"
        }
    } catch {
        if ($_.Exception.Message -match '^\[[A-Z0-9_]+\]') { throw }
        Fail $Code "$display could not be classified as a regular file"
    } finally {
        $process.Dispose()
    }
}

function Read-FileBytesChecked([string]$Path, [string]$Code, [string]$Label) {
    $display = if ([string]::IsNullOrWhiteSpace($Label)) { $Path } else { $Label }
    Assert-LinuxRegularFileForRead $Path $Code $display
    try {
        $bytes = [IO.File]::ReadAllBytes($Path)
        return ,$bytes
    } catch {
        Fail $Code "$display could not be read"
    }
}

function Get-SHA256File([string]$Path) {
    return Get-SHA256Bytes (Read-FileBytesChecked $Path 'LICENSE_FILE_TYPE_INVALID' $Path)
}

function Get-SHA256Text([string]$Text) {
    return Get-SHA256Bytes ([Text.Encoding]::UTF8.GetBytes($Text))
}

function Read-Utf8Strict([string]$Path, [string]$Code, [switch]$RequireCanonicalText, [string]$Label) {
    $display = if ([string]::IsNullOrWhiteSpace($Label)) { $Path } else { $Label }
    $bytes = Read-FileBytesChecked $Path $Code $display
    if ($bytes.Length -ge 3 -and $bytes[0] -eq 0xef -and $bytes[1] -eq 0xbb -and $bytes[2] -eq 0xbf) {
        Fail $Code "$display must be UTF-8 without BOM"
    }
    $encoding = New-Object Text.UTF8Encoding($false, $true)
    try {
        $text = $encoding.GetString($bytes)
    } catch {
        Fail $Code "$display is not valid UTF-8"
    }
    if ($RequireCanonicalText) {
        if ($text.IndexOf("`r", [StringComparison]::Ordinal) -ge 0) {
            Fail $Code "$display must use LF line endings"
        }
        if (-not $text.EndsWith("`n", [StringComparison]::Ordinal) -or $text.EndsWith("`n`n", [StringComparison]::Ordinal)) {
            Fail $Code "$display must end in exactly one LF"
        }
    }
    return $text
}

function Convert-ToNoticeLegalText([string]$Text) {
    foreach ($character in $Text.ToCharArray()) {
        $value = [int]$character
        $allowedWhitespace = $value -eq 0x09 -or $value -eq 0x0a -or $value -eq 0x0c -or $value -eq 0x0d
        if (($value -lt 0x20 -and -not $allowedWhitespace) -or $value -eq 0x7f -or ($value -ge 0x80 -and $value -le 0x9f)) {
            Fail 'LICENSE_NOTICE_RENDER_INVALID' 'legal text contains a forbidden control character'
        }
    }
    $normalized = $Text.Replace("`r`n", "`n").Replace("`r", "`n").Replace([string][char]0x0c, "`n").TrimEnd([char]10)
    return $normalized + "`n"
}

function Get-MaximumNoticeFenceRun([string]$Text, [char]$Character) {
    $maximum = 0
    $current = 0
    foreach ($candidate in $Text.ToCharArray()) {
        if ($candidate -eq $Character) {
            $current++
            if ($current -gt $maximum) { $maximum = $current }
        } else {
            $current = 0
        }
    }
    return $maximum
}

function Get-NoticeLegalFence([string]$NormalizedText) {
    $backtick = [char]0x60
    $tilde = [char]0x7e
    $backtickLength = [Math]::Max(3, (Get-MaximumNoticeFenceRun $NormalizedText $backtick) + 1)
    $tildeLength = [Math]::Max(3, (Get-MaximumNoticeFenceRun $NormalizedText $tilde) + 1)
    if ($backtickLength -le $tildeLength) { return ([string]$backtick) * $backtickLength }
    return ([string]$tilde) * $tildeLength
}

function Add-NoticeLegalPayload($Lines, [string]$NormalizedText) {
    $fence = Get-NoticeLegalFence $NormalizedText
    [void]$Lines.Add($fence)
    $withoutFinalLF = $NormalizedText.Substring(0, $NormalizedText.Length - 1)
    foreach ($textLine in @($withoutFinalLF.Split([char]10))) { [void]$Lines.Add($textLine) }
    [void]$Lines.Add($fence)
}

function Test-IsBlankLegalText([string]$Text) {
    if ([string]::IsNullOrEmpty($Text)) { return $true }
    $meaningfulCategories = @(
        [Globalization.UnicodeCategory]::UppercaseLetter,
        [Globalization.UnicodeCategory]::LowercaseLetter,
        [Globalization.UnicodeCategory]::TitlecaseLetter,
        [Globalization.UnicodeCategory]::ModifierLetter,
        [Globalization.UnicodeCategory]::OtherLetter,
        [Globalization.UnicodeCategory]::DecimalDigitNumber,
        [Globalization.UnicodeCategory]::LetterNumber,
        [Globalization.UnicodeCategory]::OtherNumber
    )
    $index = 0
    while ($index -lt $Text.Length) {
        $character = $Text[$index]
        if ([char]::IsHighSurrogate($character)) {
            if ($index + 1 -ge $Text.Length -or -not [char]::IsLowSurrogate($Text[$index + 1])) {
                $index++
                continue
            }
            $category = [Globalization.CharUnicodeInfo]::GetUnicodeCategory($Text, $index)
            $index += 2
        } elseif ([char]::IsLowSurrogate($character)) {
            $index++
            continue
        } else {
            $category = [Globalization.CharUnicodeInfo]::GetUnicodeCategory($Text, $index)
            $index++
        }
        if ($meaningfulCategories -contains $category) { return $false }
    }
    return $true
}

function Get-CanonicalAbsolutePath([string]$Path) {
    $full = [IO.Path]::GetFullPath($Path)
    $filesystemRoot = [IO.Path]::GetPathRoot($full)
    if ($full.Equals($filesystemRoot, $pathComparison)) { return $filesystemRoot }
    return $full.TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
}

function Get-PathWithTrailingSeparator([string]$Path) {
    if ($Path.EndsWith([string][IO.Path]::DirectorySeparatorChar, [StringComparison]::Ordinal) -or
        $Path.EndsWith([string][IO.Path]::AltDirectorySeparatorChar, [StringComparison]::Ordinal)) {
        return $Path
    }
    return $Path + [IO.Path]::DirectorySeparatorChar
}

function Get-RelativeSlashPath([string]$Base, [string]$Path) {
    $baseWithSeparator = Get-PathWithTrailingSeparator $Base
    if (-not $Path.StartsWith($baseWithSeparator, $pathComparison)) {
        Fail 'LICENSE_PATH_ESCAPE' "$Path is outside $Base"
    }
    return $Path.Substring($baseWithSeparator.Length).Replace([char]92, [char]47)
}

function Resolve-Inside([string]$Base, [string]$Relative, [string]$Code) {
    if ([string]::IsNullOrWhiteSpace($Relative) -or [IO.Path]::IsPathRooted($Relative) -or $Relative.Contains([char]92)) {
        Fail $Code "invalid relative slash path: $Relative"
    }
    $segments = @($Relative.Split([char]47))
    if ($segments.Count -eq 0 -or @($segments | Where-Object { $_ -eq '' -or $_ -eq '.' -or $_ -eq '..' }).Count -ne 0) {
        Fail $Code "invalid relative slash path: $Relative"
    }
    try {
        $candidate = [IO.Path]::GetFullPath((Join-Path $Base ($Relative.Replace([char]47, [IO.Path]::DirectorySeparatorChar))))
    } catch {
        Fail $Code "invalid relative slash path: $Relative"
    }
    $baseWithSeparator = Get-PathWithTrailingSeparator $Base
    if (-not $candidate.StartsWith($baseWithSeparator, $pathComparison)) {
        Fail $Code "path escapes base: $Relative"
    }
    return $candidate
}

function Test-Reparse($Item) {
    if (($Item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { return $true }
    if ($null -ne $Item.PSObject.Properties['LinkType'] -and -not [string]::IsNullOrWhiteSpace([string]$Item.LinkType)) { return $true }
    return $false
}

function Assert-NoReparsePath([string]$Base, [string]$Path, [string]$Code, [string]$Label) {
    $display = if ([string]::IsNullOrWhiteSpace($Label)) { $Path } else { $Label }
    $baseFull = Get-CanonicalAbsolutePath $Base
    $current = [IO.Path]::GetFullPath($Path)
    $baseWithSeparator = Get-PathWithTrailingSeparator $baseFull
    if (-not $current.Equals($baseFull, $pathComparison) -and -not $current.StartsWith($baseWithSeparator, $pathComparison)) {
        Fail $Code "$display is outside its permitted base"
    }
    while ($true) {
        $item = Get-Item -Force -LiteralPath $current -ErrorAction Stop
        if (Test-Reparse $item) { Fail $Code "reparse path is forbidden: $display" }
        if ($current.Equals($baseFull, $pathComparison)) { break }
        $parent = Split-Path -Parent $current
        if ([string]::IsNullOrEmpty($parent) -or $parent.Equals($current, $pathComparison)) { Fail $Code "unable to reach base path from $display" }
        $current = $parent
    }
}

function Assert-NoReparseAncestry([string]$Path, [string]$Code) {
    $current = Get-CanonicalAbsolutePath $Path
    while (-not [string]::IsNullOrWhiteSpace($current)) {
        $item = Get-Item -Force -LiteralPath $current -ErrorAction Stop
        if (Test-Reparse $item) { Fail $Code 'path ancestry contains a reparse point' }
        $filesystemRoot = [IO.Path]::GetPathRoot($current)
        if ($current.Equals($filesystemRoot, $pathComparison)) { break }
        $parent = Split-Path -Parent $current
        if ([string]::IsNullOrWhiteSpace($parent) -or $parent.Equals($current, $pathComparison)) { break }
        $current = $parent
    }
}

function Assert-RegularFile([string]$Base, [string]$Path, [string]$Code, [string]$Label) {
    $display = if ([string]::IsNullOrWhiteSpace($Label)) { $Path } else { $Label }
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { Fail $Code "required file is missing: $display" }
    Assert-NoReparsePath $Base $Path $Code $display
    $item = Get-Item -Force -LiteralPath $Path
    if (($item.Attributes -band [IO.FileAttributes]::SparseFile) -ne 0) { Fail $Code "sparse files are forbidden: $display" }
    if ($script:isWindowsPlatform) {
        $extraStreams = @(Get-Item -LiteralPath $Path -Stream * | Where-Object { $_.Stream -ne ':$DATA' })
        if ($extraStreams.Count -ne 0) { Fail $Code "alternate data streams are forbidden: $display" }
    }
    Assert-LinuxRegularFileForRead $Path $Code $display
}

function Assert-ExactProperties($Object, [string[]]$Expected, [string]$Code, [string]$Context) {
    if ($null -eq $Object -or $Object -isnot [psobject]) { Fail $Code "$Context must be a JSON object" }
    $actual = @($Object.PSObject.Properties.Name)
    foreach ($name in $Expected) {
        if (@($actual | Where-Object { $_ -ceq $name }).Count -ne 1) { Fail $Code "$Context is missing property '$name'" }
    }
    foreach ($name in $actual) {
        if (@($Expected | Where-Object { $_ -ceq $name }).Count -ne 1) { Fail $Code "$Context has unknown property '$name'" }
    }
    if ($actual.Count -ne $Expected.Count) { Fail $Code "$Context property set is not exact" }
}

function Assert-JsonArray($Value, [string]$Code, [string]$Context) {
    if ($null -eq $Value -or $Value -isnot [Array]) { Fail $Code "$Context must be a JSON array" }
}

function Assert-JsonString($Value, [string]$Code, [string]$Context, [switch]$AllowEmpty) {
    if ($Value -isnot [string]) { Fail $Code "$Context must be a JSON string" }
    if (-not $AllowEmpty -and [string]::IsNullOrWhiteSpace($Value)) { Fail $Code "$Context must not be empty" }
    foreach ($character in $Value.ToCharArray()) {
        if ([int]$character -lt 0x20 -or [int]$character -eq 0x7f) { Fail $Code "$Context contains a control character" }
    }
}

function Assert-NonNegativeInteger($Value, [string]$Code, [string]$Context) {
    if ($Value -isnot [byte] -and $Value -isnot [int16] -and $Value -isnot [int32] -and $Value -isnot [int64]) {
        Fail $Code "$Context must be an integer"
    }
    if ([int64]$Value -lt 0) { Fail $Code "$Context must not be negative" }
}

function Assert-JsonBoolean($Value, [string]$Code, [string]$Context) {
    if ($Value -isnot [bool]) { Fail $Code "$Context must be a boolean" }
}

function Assert-Hash([string]$Value, [string]$Code, [string]$Context) {
    if ($Value -cnotmatch '^[0-9a-f]{64}$') { Fail $Code "$Context must be lowercase SHA-256" }
}

function Assert-H1([string]$Value, [string]$Code, [string]$Context) {
    if ($Value -cnotmatch '^h1:[A-Za-z0-9+/]{43}=$') { Fail $Code "$Context must be an h1 checksum" }
}

function Assert-HTTPSURL([string]$Value, [string]$Code, [string]$Context) {
    foreach ($character in $Value.ToCharArray()) {
        if ([int]$character -lt 0x20 -or [int]$character -eq 0x7f) { Fail $Code "$Context contains a control character" }
    }
    $uri = $null
    if (-not [Uri]::TryCreate($Value, [UriKind]::Absolute, [ref]$uri) -or $uri.Scheme -cne 'https' -or [string]::IsNullOrWhiteSpace($uri.Host)) {
        Fail $Code "$Context must be an absolute HTTPS URL"
    }
    if (-not [string]::IsNullOrEmpty($uri.UserInfo) -or -not [string]::IsNullOrEmpty($uri.Query) -or -not [string]::IsNullOrEmpty($uri.Fragment)) {
        Fail $Code "$Context must not contain userinfo, query, or fragment"
    }
    if ($Value.IndexOf('|', [StringComparison]::Ordinal) -ge 0 -or $Value.IndexOf("`n", [StringComparison]::Ordinal) -ge 0 -or $Value.IndexOf("`r", [StringComparison]::Ordinal) -ge 0) {
        Fail $Code "$Context contains forbidden characters"
    }
}

function Get-ExpectedNoticeID([string]$Kind, [string]$Identity) {
    return $Kind + '-' + (Get-SHA256Text $Identity).Substring(0, 24)
}

function Get-LegalFileRole([string]$Name) {
    # Legal documents commonly add a descriptive suffix (for example
    # LICENSE-MIT or LICENSE-3RD-PARTY.md), but source files can also begin
    # with these words (for example copyright_test.go). Never treat a
    # source-code filename as a legal document solely because of its prefix.
    if ($Name -match '(?i)\.(?:asm|bash|bat|c|cc|cmd|cpp|cs|cxx|fish|fs|fsx|go|h|hh|hpp|hxx|java|js|jsx|kt|kts|lua|m|mm|php|pl|ps1|py|r|rb|rs|s|scala|sh|sql|swift|ts|tsx|vb|wasm|zsh)$') { return $null }
    if ($Name -match '^(?i:[A-Za-z0-9][A-Za-z0-9._-]*-(?:LICENSE|LICENCE)(?:$|\.(?:txt|md|rst|html?)))$') { return 'license' }
    if ($Name -match '^(?i:LICENSE|LICENCE|COPYING)(?:$|[._-].*)') { return 'license' }
    if ($Name -match '^(?i:NOTICE)(?:$|[._-].*)') { return 'notice' }
    if ($Name -match '^(?i:COPYRIGHT)(?:$|[._-].*)') { return 'copyright' }
    if ($Name -match '^(?i:PATENTS)(?:$|[._-].*)') { return 'patent' }
    if ($Name -match '^(?i:AUTHORS|CONTRIBUTORS|CREDITS)$') { return 'attribution' }
    return $null
}

function Get-NestedLicensesBase([string]$RelativePath) {
    $marker = '/LICENSES/'
    $index = $RelativePath.IndexOf($marker, [StringComparison]::Ordinal)
    if ($index -lt 1 -or $index + $marker.Length -ge $RelativePath.Length) { return $null }
    return $RelativePath.Substring(0, $index)
}

function Test-IsModuleLegalPath([string]$RelativePath) {
    $lastSlash = $RelativePath.LastIndexOf([char]47)
    $baseName = if ($lastSlash -lt 0) { $RelativePath } else { $RelativePath.Substring($lastSlash + 1) }
    if ($null -ne (Get-LegalFileRole $baseName)) { return $true }
    return ($RelativePath.StartsWith('LICENSES/', [StringComparison]::Ordinal) -or
        $RelativePath.IndexOf('/LICENSES/', [StringComparison]::Ordinal) -ge 0)
}

function Add-LegalBundleFile($Bundle, [string]$Relative, [string]$Role, $Item, [string]$Code, [string]$Context) {
    if (@($Bundle.Keys | Where-Object { $_.Equals($Relative, [StringComparison]::OrdinalIgnoreCase) }).Count -ne 0) {
        Fail $Code "$Context has a case-insensitive legal-file collision: $Relative"
    }
    $Bundle[$Relative] = [pscustomobject]@{ Role = $Role; Item = $Item }
}

function Get-ModuleLegalBundle([string]$ModuleDirectory, [string]$ModuleKey) {
    $bundle = @{}
    $queue = New-Object 'Collections.Generic.Queue[object]'
    $queue.Enqueue([pscustomobject]@{ Path = $ModuleDirectory; InLicensesDirectory = $false })
    while ($queue.Count -ne 0) {
        $current = $queue.Dequeue()
        try { $children = @(Get-ChildItem -LiteralPath $current.Path -Force) } catch {
            Fail 'LICENSE_DEPENDENCY_LEGAL_BUNDLE_INVALID' "$ModuleKey legal bundle directory could not be enumerated"
        }
        foreach ($item in $children) {
            $relative = Get-RelativeSlashPath $ModuleDirectory $item.FullName
            $label = "$ModuleKey legal path $relative"
            if (Test-Reparse $item) { Fail 'LICENSE_DEPENDENCY_ARCHIVE_REPARSE_FORBIDDEN' "reparse path is forbidden: $label" }
            if ($item.PSIsContainer) {
                $inLicensesDirectory = [bool]$current.InLicensesDirectory
                if ($item.Name -ieq 'LICENSES') {
                    if ($item.Name -cne 'LICENSES') { Fail 'LICENSE_DEPENDENCY_LEGAL_BUNDLE_INVALID' "$label must use the canonical LICENSES directory name" }
                    $inLicensesDirectory = $true
                }
                $queue.Enqueue([pscustomobject]@{ Path = $item.FullName; InLicensesDirectory = $inLicensesDirectory })
                continue
            }
            $role = if ([bool]$current.InLicensesDirectory) { 'license' } else { Get-LegalFileRole $item.Name }
            if ($null -eq $role) { continue }
            Assert-RegularFile $ModuleDirectory $item.FullName 'LICENSE_DEPENDENCY_LEGAL_BUNDLE_INVALID' $label
            Add-LegalBundleFile $bundle $relative $role $item 'LICENSE_DEPENDENCY_LEGAL_BUNDLE_INVALID' $ModuleKey
        }
    }
    return $bundle
}

function Get-ModuleSourceSPDXScan([string]$ModuleDirectory, [string]$ModuleKey) {
    $files = @{}
    $queue = New-Object 'Collections.Generic.Queue[string]'
    $queue.Enqueue($ModuleDirectory)
    while ($queue.Count -ne 0) {
        $directory = $queue.Dequeue()
        try { $children = @(Get-ChildItem -LiteralPath $directory -Force) } catch {
            Fail 'LICENSE_DEPENDENCY_ARCHIVE_READ_FAILED' "$ModuleKey archive directory could not be enumerated"
        }
        foreach ($item in $children) {
            $relative = Get-RelativeSlashPath $ModuleDirectory $item.FullName
            foreach ($character in $relative.ToCharArray()) {
                if ([int]$character -lt 0x20 -or [int]$character -eq 0x7f) {
                    Fail 'LICENSE_DEPENDENCY_ARCHIVE_PATH_INVALID' "$ModuleKey archive contains a path with control characters"
                }
            }
            if (Test-Reparse $item) {
                Fail 'LICENSE_DEPENDENCY_ARCHIVE_REPARSE_FORBIDDEN' "$ModuleKey archive contains a reparse path: $relative"
            }
            if ($item.PSIsContainer) {
                $queue.Enqueue($item.FullName)
                continue
            }
            Assert-RegularFile $ModuleDirectory $item.FullName 'LICENSE_DEPENDENCY_ARCHIVE_FILE_INVALID' "$ModuleKey archive file $relative"
            $files[$relative] = $item.FullName
        }
    }

    $paths = [string[]]@($files.Keys)
    [Array]::Sort($paths, [StringComparer]::Ordinal)
    $markerBytes = [Text.Encoding]::ASCII.GetBytes($sourceSPDXMarker)
    $strictUTF8 = New-Object Text.UTF8Encoding($false, $true)
    $records = New-Object Text.StringBuilder
    $expressions = New-Object 'Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    [int64]$occurrenceCount = 0

    foreach ($relative in $paths) {
        $label = "$ModuleKey archive file $relative"
        $bytes = Read-FileBytesChecked $files[$relative] 'LICENSE_DEPENDENCY_ARCHIVE_READ_FAILED' $label
        if (-not [FreeAgent.Release.StrictJsonValidator]::ContainsByteSequence($bytes, $markerBytes)) { continue }
        try { $text = $strictUTF8.GetString($bytes) } catch {
            Fail 'LICENSE_DEPENDENCY_SPDX_UTF8_INVALID' "$label contains an SPDX marker but is not strict UTF-8"
        }
        $normalized = $text.Replace("`r`n", "`n").Replace("`r", "`n")
        $lines = $normalized.Split([char]10)
        for ($lineIndex = 0; $lineIndex -lt $lines.Length; $lineIndex++) {
            $line = $lines[$lineIndex]
            $searchOffset = 0
            $occurrenceOnLine = 0
            while ($searchOffset -le $line.Length - $sourceSPDXMarker.Length) {
                $markerOffset = $line.IndexOf($sourceSPDXMarker, $searchOffset, [StringComparison]::Ordinal)
                if ($markerOffset -lt 0) { break }
                $occurrenceOnLine++
                $expressionStart = $markerOffset + $sourceSPDXMarker.Length
                $nextMarker = $line.IndexOf($sourceSPDXMarker, $expressionStart, [StringComparison]::Ordinal)
                $expressionEnd = if ($nextMarker -lt 0) { $line.Length } else { $nextMarker }
                $expression = $line.Substring($expressionStart, $expressionEnd - $expressionStart).Trim([char[]]@([char]0x20, [char]0x09))
                if ($approvedDetectedSPDXExpressions -cnotcontains $expression) {
                    Fail 'LICENSE_DEPENDENCY_SPDX_UNKNOWN' "$ModuleKey has an unknown or noncanonical SPDX marker at ${relative}:$($lineIndex + 1)"
                }
                $occurrenceCount++
                [void]$expressions.Add($expression)
                [void]$records.Append($relative)
                [void]$records.Append([char]0x09)
                [void]$records.Append(($lineIndex + 1).ToString([Globalization.CultureInfo]::InvariantCulture))
                [void]$records.Append([char]0x09)
                [void]$records.Append($occurrenceOnLine.ToString([Globalization.CultureInfo]::InvariantCulture))
                [void]$records.Append([char]0x09)
                [void]$records.Append($expression)
                [void]$records.Append([char]0x0a)
                if ($nextMarker -lt 0) { break }
                $searchOffset = $nextMarker
            }
        }
    }

    $orderedExpressions = [string[]]@($expressions)
    [Array]::Sort($orderedExpressions, [StringComparer]::Ordinal)
    return [pscustomobject]@{
        algorithm = $sourceSPDXScanAlgorithm
        occurrence_count = $occurrenceCount
        sha256 = Get-SHA256Text $records.ToString()
        expressions = @($orderedExpressions)
    }
}

function Assert-DeclaredLicenseExpression([string]$Expression, $LicenseRefMap, [string]$ModuleKey, $UsedLicenseRefs) {
    Assert-JsonString $Expression 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$ModuleKey declared_license_expression"
    $atoms = [string[]]@($Expression.Split([string[]]@(' AND '), [StringSplitOptions]::None))
    if ($atoms.Count -eq 0) { Fail 'LICENSE_UNKNOWN_SPDX' "$ModuleKey has an empty declared license expression" }
    $seen = New-Object 'Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    foreach ($atom in $atoms) {
        if ([string]::IsNullOrWhiteSpace($atom) -or -not $seen.Add($atom)) {
            Fail 'LICENSE_DECLARED_SPDX_NONCANONICAL' "$ModuleKey declared license expression is not canonical"
        }
        if ($approvedDeclaredLicenseAtoms -ccontains $atom) { continue }
        if (-not $LicenseRefMap.ContainsKey($atom)) { Fail 'LICENSE_UNKNOWN_SPDX' "$ModuleKey uses an unknown declared SPDX atom"
        }
        $licenseRef = $LicenseRefMap[$atom]
        if ([string]$licenseRef.applies_to -cne $ModuleKey) { Fail 'LICENSE_LICENSE_REF_SCOPE_INVALID' "$ModuleKey uses a LicenseRef scoped to another module" }
        $UsedLicenseRefs[$atom] = $true
    }
    $sorted = [string[]]@($atoms)
    [Array]::Sort($sorted, [StringComparer]::Ordinal)
    if (($atoms -join ' AND ') -cne ($sorted -join ' AND ')) {
        Fail 'LICENSE_DECLARED_SPDX_NONCANONICAL' "$ModuleKey declared license expression must be an Ordinal-sorted flat AND expression"
    }
}

function Test-IsManagedAssetPath([string]$RelativePath) {
    foreach ($root in $assetRoots) {
        if ($RelativePath -ceq $root -or $RelativePath.StartsWith($root + '/', [StringComparison]::Ordinal)) { return $true }
    }
    return $false
}

function Test-ContainsSplitSPDXMarkerIntent([string]$Content) {
    $splitIntentPattern = '(?is)S[ \t\r\n/*#;-]*P[ \t\r\n/*#;-]*D[ \t\r\n/*#;-]*X[ \t\r\n/*#;-]*L[ \t\r\n/*#;-]*i[ \t\r\n/*#;-]*c[ \t\r\n/*#;-]*e[ \t\r\n/*#;-]*n[ \t\r\n/*#;-]*s[ \t\r\n/*#;-]*e[ \t\r\n/*#;-]*I[ \t\r\n/*#;-]*d[ \t\r\n/*#;-]*e[ \t\r\n/*#;-]*n[ \t\r\n/*#;-]*t[ \t\r\n/*#;-]*i[ \t\r\n/*#;-]*f[ \t\r\n/*#;-]*i[ \t\r\n/*#;-]*e[ \t\r\n/*#;-]*r'
    foreach ($intent in @([regex]::Matches($Content, $splitIntentPattern))) {
        if ($intent.Value.IndexOf("`n", [StringComparison]::Ordinal) -ge 0 -or
            $intent.Value.IndexOf("`r", [StringComparison]::Ordinal) -ge 0) {
            return $true
        }
    }
    return $false
}

function Assert-StrictSPDXMarkers([string]$Content, [string]$ExpectedIdentifier, [string]$Code, [string]$Label) {
    $validPattern = '^[ \t]*//[ \t]*SPDX-License-Identifier:[ \t]*(?<id>[A-Za-z0-9.+-]+)[ \t]*$'
    foreach ($line in @([regex]::Split($Content, '\r\n|\n|\r'))) {
        $containsMarker = [regex]::IsMatch($line, '(?i)(?:SPDX|License[ \t]*-[ \t]*Identifier|License[ \t]+Identifier)')
        if (-not $containsMarker) { continue }
        $match = [regex]::Match($line, $validPattern)
        if (-not $match.Success) { Fail $Code "$Label contains a malformed or non-line SPDX marker" }
        if ($match.Groups['id'].Value -cne $ExpectedIdentifier) { Fail $Code "$Label SPDX must be the single identifier $ExpectedIdentifier" }
    }

    # Catch marker intent split across lines or comment fragments. A canonical marker above
    # never contains a line break, so any folded match containing one is necessarily invalid.
    if (Test-ContainsSplitSPDXMarkerIntent $Content) {
        Fail $Code "$Label contains a split SPDX marker"
    }
}

if (-not ('FreeAgent.Release.StrictJsonValidator' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Globalization;
using System.Text;

namespace FreeAgent.Release {
    public sealed class StrictJsonValidator {
        private readonly string text;
        private int index;

        private StrictJsonValidator(string value) { text = value; }

        public static void Validate(string value) {
            if (value == null) throw new FormatException("JSON is null");
            var parser = new StrictJsonValidator(value);
            parser.SkipWhitespace();
            parser.ParseValue();
            parser.SkipWhitespace();
            if (parser.index != value.Length) throw new FormatException("trailing JSON content");
        }

        public static string[] SplitObjectStream(string value) {
            if (value == null) throw new FormatException("JSON stream is null");
            var parser = new StrictJsonValidator(value);
            var objects = new List<string>();
            while (true) {
                parser.SkipWhitespace();
                if (parser.index == value.Length) break;
                if (value[parser.index] != '{') throw new FormatException("JSON stream entry must be an object");
                int start = parser.index;
                parser.ParseObject();
                objects.Add(value.Substring(start, parser.index - start));
            }
            if (objects.Count == 0) throw new FormatException("JSON object stream is empty");
            return objects.ToArray();
        }

        public static bool ContainsByteSequence(byte[] value, byte[] needle) {
            if (value == null || needle == null || needle.Length == 0 || value.Length < needle.Length) return false;
            int limit = value.Length - needle.Length;
            for (int offset = 0; offset <= limit; offset++) {
                int index = 0;
                while (index < needle.Length && value[offset + index] == needle[index]) index++;
                if (index == needle.Length) return true;
            }
            return false;
        }

        private void ParseValue() {
            if (index >= text.Length) throw new FormatException("missing JSON value");
            char c = text[index];
            if (c == '{') { ParseObject(); return; }
            if (c == '[') { ParseArray(); return; }
            if (c == '"') { ParseString(); return; }
            if (c == 't') { ParseLiteral("true"); return; }
            if (c == 'f') { ParseLiteral("false"); return; }
            if (c == 'n') { ParseLiteral("null"); return; }
            ParseNumber();
        }

        private void ParseObject() {
            index++;
            SkipWhitespace();
            var keys = new HashSet<string>(StringComparer.Ordinal);
            if (Take('}')) return;
            while (true) {
                if (index >= text.Length || text[index] != '"') throw new FormatException("object key must be a string");
                string key = ParseString();
                if (!keys.Add(key)) throw new FormatException("duplicate object key: " + key);
                SkipWhitespace();
                Require(':');
                SkipWhitespace();
                ParseValue();
                SkipWhitespace();
                if (Take('}')) return;
                Require(',');
                SkipWhitespace();
            }
        }

        private void ParseArray() {
            index++;
            SkipWhitespace();
            if (Take(']')) return;
            while (true) {
                ParseValue();
                SkipWhitespace();
                if (Take(']')) return;
                Require(',');
                SkipWhitespace();
            }
        }

        private string ParseString() {
            Require('"');
            var result = new StringBuilder();
            while (index < text.Length) {
                char c = text[index++];
                if (c == '"') return result.ToString();
                if (c < 0x20) throw new FormatException("unescaped control character in string");
                if (c != '\\') { result.Append(c); continue; }
                if (index >= text.Length) throw new FormatException("incomplete string escape");
                char e = text[index++];
                switch (e) {
                    case '"': result.Append('"'); break;
                    case '\\': result.Append('\\'); break;
                    case '/': result.Append('/'); break;
                    case 'b': result.Append('\b'); break;
                    case 'f': result.Append('\f'); break;
                    case 'n': result.Append('\n'); break;
                    case 'r': result.Append('\r'); break;
                    case 't': result.Append('\t'); break;
                    case 'u':
                        if (index + 4 > text.Length) throw new FormatException("incomplete unicode escape");
                        string hex = text.Substring(index, 4);
                        int code;
                        if (!Int32.TryParse(hex, NumberStyles.AllowHexSpecifier, CultureInfo.InvariantCulture, out code)) throw new FormatException("invalid unicode escape");
                        result.Append((char)code);
                        index += 4;
                        break;
                    default: throw new FormatException("invalid string escape");
                }
            }
            throw new FormatException("unterminated string");
        }

        private void ParseNumber() {
            int start = index;
            if (Take('-')) { }
            if (Take('0')) {
                if (index < text.Length && Char.IsDigit(text[index])) throw new FormatException("leading zero in number");
            } else {
                if (index >= text.Length || text[index] < '1' || text[index] > '9') throw new FormatException("invalid number");
                while (index < text.Length && Char.IsDigit(text[index])) index++;
            }
            if (Take('.')) {
                if (index >= text.Length || !Char.IsDigit(text[index])) throw new FormatException("invalid fraction");
                while (index < text.Length && Char.IsDigit(text[index])) index++;
            }
            if (index < text.Length && (text[index] == 'e' || text[index] == 'E')) {
                index++;
                if (index < text.Length && (text[index] == '+' || text[index] == '-')) index++;
                if (index >= text.Length || !Char.IsDigit(text[index])) throw new FormatException("invalid exponent");
                while (index < text.Length && Char.IsDigit(text[index])) index++;
            }
            if (index == start) throw new FormatException("invalid number");
        }

        private void ParseLiteral(string value) {
            if (index + value.Length > text.Length || String.CompareOrdinal(text, index, value, 0, value.Length) != 0) throw new FormatException("invalid literal");
            index += value.Length;
        }

        private void SkipWhitespace() {
            while (index < text.Length) {
                char c = text[index];
                if (c != ' ' && c != '\t' && c != '\r' && c != '\n') break;
                index++;
            }
        }

        private bool Take(char expected) {
            if (index < text.Length && text[index] == expected) { index++; return true; }
            return false;
        }

        private void Require(char expected) {
            if (!Take(expected)) throw new FormatException("expected '" + expected + "'");
        }
    }
}
'@
}

function Read-StrictJson([string]$Path, [string]$Code) {
    $text = Read-Utf8Strict $Path $Code -RequireCanonicalText
    try {
        [FreeAgent.Release.StrictJsonValidator]::Validate($text)
        return $text | ConvertFrom-Json
    } catch {
        Fail $Code "$Path contains invalid or non-strict JSON: $($_.Exception.Message)"
    }
}

function Get-RepositoryGoFiles([string]$RepositoryRoot) {
    $files = New-Object System.Collections.Generic.List[IO.FileInfo]
    $queue = New-Object 'Collections.Generic.Queue[string]'
    $queue.Enqueue($RepositoryRoot)
    while ($queue.Count -ne 0) {
        $directory = $queue.Dequeue()
        foreach ($item in @(Get-ChildItem -LiteralPath $directory -Force)) {
            if (Test-Reparse $item) {
                $relative = Get-RelativeSlashPath $RepositoryRoot $item.FullName
                Fail 'LICENSE_GO_SCAN_REPARSE_FORBIDDEN' "repository scan encountered reparse path: $relative"
            }
            if ($item.PSIsContainer) {
                $queue.Enqueue($item.FullName)
            } elseif ($item.Extension -ceq '.go') {
                $files.Add($item)
            }
        }
    }
    return @($files)
}

function Get-MarkdownFenceOpening([string]$Line) {
    $match = [regex]::Match($Line, '^[ ]{0,3}(?<fence>`{3,}|~{3,})')
    if (-not $match.Success) { return $null }
    $fence = $match.Groups['fence'].Value
    $remainder = $Line.Substring($match.Groups['fence'].Index + $fence.Length)
    # CommonMark forbids a backtick in the info string of a backtick fence.
    if ($fence[0] -ceq [char]0x60 -and $remainder.IndexOf([char]0x60) -ge 0) { return $null }
    return $match
}

function Test-MarkdownParagraphInterrupt([string]$Line) {
    if ([string]::IsNullOrWhiteSpace($Line)) { return $true }
    if ($null -ne (Get-MarkdownFenceOpening $Line)) { return $true }
    if ([regex]::IsMatch($Line, '^[ ]{0,3}(?:#{1,6}(?:[ \t]+|$)|>|(?:[-+*]|[0-9]{1,9}[.)])[ \t]+)')) { return $true }
    if ([regex]::IsMatch($Line, '^[ ]{0,3}<!--')) { return $true }
    if ([regex]::IsMatch($Line, '^(?: {4,}| {0,3}\t)')) { return $true }
    if ([regex]::IsMatch($Line, '^[ ]{0,3}(?:(?:\*[ \t]*){3,}|(?:-[ \t]*){3,}|(?:_[ \t]*){3,})$')) { return $true }
    return $false
}

function Test-MarkdownBacktickRunHasCloser([string[]]$Lines, [int]$LineIndex, [int]$Offset, [int]$RunLength) {
    for ($candidateLineIndex = $LineIndex; $candidateLineIndex -lt $Lines.Count; $candidateLineIndex++) {
        $candidateLine = $Lines[$candidateLineIndex]
        $candidateOffset = if ($candidateLineIndex -eq $LineIndex) { $Offset } else { 0 }
        while ($candidateOffset -lt $candidateLine.Length) {
            if ($candidateLine[$candidateOffset] -cne [char]0x60) {
                $candidateOffset++
                continue
            }
            $runStart = $candidateOffset
            while ($candidateOffset -lt $candidateLine.Length -and $candidateLine[$candidateOffset] -ceq [char]0x60) { $candidateOffset++ }
            if ($candidateOffset - $runStart -ne $RunLength) { continue }

            # A canonical declaration's own inline-code delimiters must not
            # retroactively close an otherwise-unmatched run before a comment.
            if ($candidateLine.IndexOf('AGPL-3.0-only', [StringComparison]::Ordinal) -ge 0) { continue }
            return $true
        }
    }
    return $false
}

function Get-MarkdownProseLines([string]$Content) {
    $insideFence = $false
    $fenceCharacter = [char]0
    $minimumFenceLength = 0
    $insideHtmlComment = $false
    $insideHtmlBlockComment = $false
    $insideIndentedCode = $false
    $inlineCodeDelimiterLength = 0
    $paragraphOpen = $false
    $lazyBlockQuoteParagraph = $false

    $markdownLines = @([regex]::Split($Content, '\r\n|\n|\r'))
    for ($lineIndex = 0; $lineIndex -lt $markdownLines.Count; $lineIndex++) {
        $rawLine = $markdownLines[$lineIndex]
        if ($insideFence) {
            $closingPattern = '^[ ]{0,3}' + [regex]::Escape([string]$fenceCharacter) + '{' + $minimumFenceLength + ',}[ \t]*$'
            if ([regex]::IsMatch($rawLine, $closingPattern)) {
                $insideFence = $false
                $fenceCharacter = [char]0
                $minimumFenceLength = 0
                $paragraphOpen = $false
            }
            continue
        }

        if ($insideHtmlBlockComment) {
            if ($rawLine.IndexOf('-->', [StringComparison]::Ordinal) -ge 0) {
                $insideHtmlBlockComment = $false
                $paragraphOpen = $false
            }
            continue
        }

        $rawLineIsBlank = [string]::IsNullOrWhiteSpace($rawLine)
        $rawLineIsIndented = [regex]::IsMatch($rawLine, '^(?: {4,}| {0,3}\t)')
        if ($insideIndentedCode) {
            if ($rawLineIsBlank -or $rawLineIsIndented) { continue }
            $insideIndentedCode = $false
        }
        # Block classification precedes inline parsing. In particular, <!-- inside
        # an indented code block is literal code and must not open an HTML comment.
        if ($rawLineIsIndented -and -not $paragraphOpen -and
            -not $insideHtmlComment -and $inlineCodeDelimiterLength -eq 0) {
            $insideIndentedCode = $true
            continue
        }

        if (-not $insideHtmlComment) {
            # Block quotes are excluded policy text, so their inline Markdown state
            # must not leak into following top-level prose.
            $rawBlockQuote = [regex]::Match($rawLine, '^[ ]{0,3}(?:>[ \t]*)+(?<body>.*)$')
            if ($rawBlockQuote.Success) {
                $body = $rawBlockQuote.Groups['body'].Value
                $lazyBlockQuoteParagraph = -not [string]::IsNullOrWhiteSpace($body) -and -not (Test-MarkdownParagraphInterrupt $body)
                $insideIndentedCode = $false
                $inlineCodeDelimiterLength = 0
                $paragraphOpen = $false
                continue
            }

            # Fence recognition is a block operation and therefore precedes inline
            # HTML parsing; <!-- in a valid info string is literal fence metadata.
            $rawOpeningFence = Get-MarkdownFenceOpening $rawLine
            if ($null -ne $rawOpeningFence) {
                $insideFence = $true
                $fenceCharacter = $rawOpeningFence.Groups['fence'].Value[0]
                $minimumFenceLength = $rawOpeningFence.Groups['fence'].Value.Length
                $insideIndentedCode = $false
                $lazyBlockQuoteParagraph = $false
                $paragraphOpen = $false
                continue
            }

            # A block HTML comment interrupts a paragraph. A confirmed multiline
            # code span is the only inline context that can make this opener literal.
            if ($inlineCodeDelimiterLength -eq 0 -and [regex]::IsMatch($rawLine, '^[ ]{0,3}<!--')) {
                $insideHtmlBlockComment = $rawLine.IndexOf('-->', [StringComparison]::Ordinal) -lt 0
                $lazyBlockQuoteParagraph = $false
                $paragraphOpen = $false
                continue
            }
        }

        # Markdown comments are not rendered prose. Remove every visible comment span
        # before checking for a project-license declaration. Code-span state persists
        # across soft line breaks, so a literal <!-- inside code never starts a comment.
        $lineStartedInsideCodeSpan = $inlineCodeDelimiterLength -ne 0
        $visible = New-Object Text.StringBuilder
        # Preserve ordinary single-line code spans so a canonical declaration may
        # format only its identifier as code. Content in a span that crosses a soft
        # line break is omitted: such a span is code, not declaration prose.
        $inlineCodeBuffer = $null
        $offset = 0
        while ($offset -lt $rawLine.Length) {
            if ($insideHtmlComment) {
                $commentEnd = $rawLine.IndexOf('-->', $offset, [StringComparison]::Ordinal)
                if ($commentEnd -lt 0) {
                    $offset = $rawLine.Length
                    continue
                }
                $insideHtmlComment = $false
                $offset = $commentEnd + 3
                continue
            }

            if ($rawLine[$offset] -ceq [char]0x60) {
                $runStart = $offset
                while ($offset -lt $rawLine.Length -and $rawLine[$offset] -ceq [char]0x60) { $offset++ }
                $runLength = $offset - $runStart
                if ($inlineCodeDelimiterLength -eq 0) {
                    $backslashCount = 0
                    $backslashIndex = $runStart - 1
                    while ($backslashIndex -ge 0 -and $rawLine[$backslashIndex] -ceq [char]0x5c) {
                        $backslashCount++
                        $backslashIndex--
                    }
                    if ($backslashCount % 2 -eq 0 -and
                        (Test-MarkdownBacktickRunHasCloser $markdownLines $lineIndex $offset $runLength)) {
                        $inlineCodeDelimiterLength = $runLength
                        $inlineCodeBuffer = New-Object Text.StringBuilder
                        [void]$inlineCodeBuffer.Append($rawLine.Substring($runStart, $runLength))
                    } else {
                        [void]$visible.Append($rawLine.Substring($runStart, $runLength))
                    }
                } else {
                    if ($null -ne $inlineCodeBuffer) {
                        [void]$inlineCodeBuffer.Append($rawLine.Substring($runStart, $runLength))
                    }
                    if ($runLength -eq $inlineCodeDelimiterLength) {
                        $inlineCodeDelimiterLength = 0
                        if ($null -ne $inlineCodeBuffer) {
                            [void]$visible.Append($inlineCodeBuffer.ToString())
                            $inlineCodeBuffer = $null
                        }
                    }
                }
                continue
            }

            if ($inlineCodeDelimiterLength -eq 0 -and
                $offset + 4 -le $rawLine.Length -and
                $rawLine.Substring($offset, 4) -ceq '<!--') {
                $insideHtmlComment = $true
                $offset += 4
                continue
            }
            if ($inlineCodeDelimiterLength -eq 0) {
                [void]$visible.Append($rawLine[$offset])
            } elseif ($null -ne $inlineCodeBuffer) {
                [void]$inlineCodeBuffer.Append($rawLine[$offset])
            }
            $offset++
        }

        $line = $visible.ToString()
        $lineIsBlank = [string]::IsNullOrWhiteSpace($line)

        if ($lazyBlockQuoteParagraph) {
            if ($lineIsBlank) {
                $lazyBlockQuoteParagraph = $false
                $inlineCodeDelimiterLength = 0
            } elseif (-not [regex]::IsMatch($line, '^[ ]{0,3}>') -and
                -not (Test-MarkdownParagraphInterrupt $line)) {
                continue
            } elseif (-not [regex]::IsMatch($line, '^[ ]{0,3}>')) {
                $lazyBlockQuoteParagraph = $false
                $inlineCodeDelimiterLength = 0
            }
        }

        $openingFence = if (-not $lineStartedInsideCodeSpan -and -not $insideHtmlComment) { Get-MarkdownFenceOpening $line } else { $null }
        if ($null -ne $openingFence) {
            $insideFence = $true
            $fenceCharacter = $openingFence.Groups['fence'].Value[0]
            $minimumFenceLength = $openingFence.Groups['fence'].Value.Length
            $insideIndentedCode = $false
            $inlineCodeDelimiterLength = 0
            $paragraphOpen = $false
            continue
        }

        # A block quote is quoted material, not a project declaration. Ignoring the
        # whole block also prevents quoted fenced examples from becoming conflicts.
        $blockQuote = [regex]::Match($line, '^[ ]{0,3}(?:>[ \t]*)+(?<body>.*)$')
        if ($blockQuote.Success) {
            $body = $blockQuote.Groups['body'].Value
            $lazyBlockQuoteParagraph = -not [string]::IsNullOrWhiteSpace($body) -and -not (Test-MarkdownParagraphInterrupt $body)
            $insideIndentedCode = $false
            $paragraphOpen = $false
            continue
        }

        $lineIsIndented = [regex]::IsMatch($line, '^(?: {4,}| {0,3}\t)')
        # Indentation cannot interrupt an active CommonMark paragraph. It starts a
        # code block after headings and other block boundaries as well as blank lines.
        if ($lineIsIndented -and -not $paragraphOpen) {
            $insideIndentedCode = $true
            continue
        }
        Write-Output $line
        if ($lineIsBlank) {
            if ($inlineCodeDelimiterLength -ne 0 -and -not $rawLineIsBlank) {
                # The rendered line is empty only because its nonblank source is
                # inside a multiline code span; the surrounding paragraph and
                # delimiter state continue across this soft line break.
                $paragraphOpen = $true
            } else {
                $paragraphOpen = $false
                $inlineCodeDelimiterLength = 0
            }
        } elseif ($lineIsIndented -and $paragraphOpen) {
            $paragraphOpen = $true
        } else {
            $paragraphOpen = -not (Test-MarkdownParagraphInterrupt $line)
        }
    }
}

function Assert-ProjectLicense([string]$RepositoryRoot) {
    $licensePath = Join-Path $RepositoryRoot 'LICENSE'
    Assert-RegularFile $RepositoryRoot $licensePath 'LICENSE_ROOT_MISSING' 'LICENSE'
    $bytes = Read-FileBytesChecked $licensePath 'LICENSE_ROOT_HASH_MISMATCH' 'LICENSE'
    if ($bytes.Length -ne $expectedLicenseBytes) { Fail 'LICENSE_ROOT_HASH_MISMATCH' "LICENSE size is $($bytes.Length), expected $expectedLicenseBytes" }
    [void](Read-Utf8Strict $licensePath 'LICENSE_ROOT_HASH_MISMATCH' -RequireCanonicalText)
    $hash = Get-SHA256Bytes $bytes
    if ($hash -cne $expectedLicenseSHA256) { Fail 'LICENSE_ROOT_HASH_MISMATCH' "LICENSE SHA-256 is $hash" }

    foreach ($rootFile in @(Get-ChildItem -LiteralPath $RepositoryRoot -File -Force)) {
        if ($rootFile.Name -ieq 'LICENSE') { continue }
        if ($rootFile.Name -match '^(?i:LICENSE|LICENCE|COPYING|NOTICE|UNLICENSE)') {
            Fail 'LICENSE_ROOT_ALTERNATE_FORBIDDEN' "unexpected root-level alternate license file: $($rootFile.Name)"
        }
    }

    $declarations = @(
        @{
            Path = 'README.md'
            LicenseIdentifier = 'AGPL-3.0-only'
            Link = '](LICENSE)'
            CanonicalPatterns = @(
                '^[ \t]*Licensed as[ \t]+`AGPL-3\.0-only`;[ \t]+see[ \t]+\[LICENSE\]\(LICENSE\)\.[ \t]*$',
                '^[ \t]*This project is licensed under[ \t]+`?AGPL-3\.0-only`?;[ \t]+see[ \t]+\[LICENSE\]\(LICENSE\)\.[ \t]*$',
                '^[ \t]*\u672c\u9879\u76ee\u91c7\u7528[ \t]+`?AGPL-3\.0-only`?[ \t]+\u8bb8\u53ef\u8bc1\uff1b\u8be6\u89c1[ \t]+\[LICENSE\]\(LICENSE\)\u3002[ \t]*$',
                '^FreeAgent \u4f7f\u7528 \[GNU Affero General Public License v3\.0 only\]\(LICENSE\)\uff0cSPDX \u6807\u8bc6\u4e3a `AGPL-3\.0-only`\u3002$'
            )
        },
        @{
            Path = 'README.zh-CN.md'
            LicenseIdentifier = 'AGPL-3.0-only'
            Link = '](LICENSE)'
            CanonicalPatterns = @(
                '^[ \t]*Licensed as[ \t]+`AGPL-3\.0-only`;[ \t]+see[ \t]+\[LICENSE\]\(LICENSE\)\.[ \t]*$',
                '^[ \t]*This project is licensed under[ \t]+`?AGPL-3\.0-only`?;[ \t]+see[ \t]+\[LICENSE\]\(LICENSE\)\.[ \t]*$',
                '^[ \t]*\u672c\u9879\u76ee\u91c7\u7528[ \t]+`?AGPL-3\.0-only`?[ \t]+\u8bb8\u53ef\u8bc1\uff1b\u8be6\u89c1[ \t]+\[LICENSE\]\(LICENSE\)\u3002[ \t]*$'
            )
        },
        @{
            Path = 'README.zh-TW.md'
            LicenseIdentifier = 'AGPL-3.0-only'
            Link = '](LICENSE)'
            CanonicalPatterns = @(
                '^[ \t]*Licensed as[ \t]+`AGPL-3\.0-only`;[ \t]+see[ \t]+\[LICENSE\]\(LICENSE\)\.[ \t]*$',
                '^[ \t]*This project is licensed under[ \t]+`?AGPL-3\.0-only`?;[ \t]+see[ \t]+\[LICENSE\]\(LICENSE\)\.[ \t]*$',
                '^[ \t]*\u672c\u9879\u76ee\u91c7\u7528[ \t]+`?AGPL-3\.0-only`?[ \t]+\u8bb8\u53ef\u8bc1\uff1b\u8be6\u89c1[ \t]+\[LICENSE\]\(LICENSE\)\u3002[ \t]*$'
            )
        },
        @{
            Path = 'CONTRIBUTING.md'
            LicenseIdentifier = 'AGPL-3.0-only'
            Link = $null
            CanonicalPatterns = @(
                '^[ \t]*Contributions use[ \t]+`?AGPL-3\.0-only`?\.[ \t]*$',
                '^[ \t]*Contributions are licensed under[ \t]+`?AGPL-3\.0-only`?\.[ \t]*$',
                '^[ \t]*\u8d21\u732e\u5185\u5bb9\u91c7\u7528[ \t]+`?AGPL-3\.0-only`?[ \t]+\u8bb8\u53ef\u8bc1\u3002[ \t]*$',
                '^\u63d0\u4ea4\u5230\u672c\u9879\u76ee\u7684\u4ee3\u7801\u548c\u6587\u6863\u5c06\u6309\u4ed3\u5e93\u7684 `AGPL-3\.0-only` \u8bb8\u53ef\u8bc1\u53d1\u5e03\u3002\u8bf7\u53ea\u63d0\u4ea4\u4f60\u6709\u6743\u63d0\u4f9b\u7684\u5185\u5bb9\uff0c\u5e76\u4fdd\u7559\u7b2c\u4e09\u65b9\u6750\u6599\u6240\u8981\u6c42\u7684\u8bb8\u53ef\u548c\u901a\u77e5\u3002$'
            )
        },
        @{
            Path = 'docs/PRD.md'
            LicenseIdentifier = 'AGPL-3.0-only'
            Link = $null
            CanonicalPatterns = @(
                '^[ \t]*License:[ \t]*`?AGPL-3\.0-only`?[ \t]*$',
                '^[ \t]*\u8bb8\u53ef\u8bc1\uff1a[ \t]*`?AGPL-3\.0-only`?[ \t]*$'
            )
        }
    )
    foreach ($declaration in $declarations) {
        $path = Resolve-Inside $RepositoryRoot $declaration.Path 'LICENSE_DECLARATION_MISSING'
        Assert-RegularFile $RepositoryRoot $path 'LICENSE_DECLARATION_MISSING'
        $content = Read-Utf8Strict $path 'LICENSE_DECLARATION_MISSING'
        $proseLines = @(Get-MarkdownProseLines $content)
        $proseContent = $proseLines -join "`n"
        $identifierPattern = '(?<![A-Za-z0-9-])' + [regex]::Escape([string]$declaration.LicenseIdentifier) + '(?![A-Za-z0-9-])'
        $decoratedIdentifierPattern = '[*_~]*`?' + $identifierPattern + '`?[*_~]*'
        $semanticWordGapPattern = '(?=[*_~]*[ \t\r\n])[*_~ \t\r\n]+'
        $semanticOptionalGapPattern = '[*_~ \t\r\n]*'
        if (-not [regex]::IsMatch($proseContent, $identifierPattern)) {
            Fail 'LICENSE_DECLARATION_MISSING' "$($declaration.Path) must declare AGPL-3.0-only"
        }
        $canonicalDeclarationCount = 0
        foreach ($line in $proseLines) {
            foreach ($pattern in @($declaration.CanonicalPatterns)) {
                if ([regex]::IsMatch($line, [string]$pattern)) {
                    $canonicalDeclarationCount++
                    break
                }
            }
        }
        if ($canonicalDeclarationCount -ne 1) {
            Fail 'LICENSE_DECLARATION_MISSING' "$($declaration.Path) must contain exactly one canonical affirmative project-license declaration"
        }
        $sameLicenseConflictPatterns = @(
            ('(?is)\bnot' + $semanticWordGapPattern + '(?:licensed' + $semanticWordGapPattern + 'under|governed' + $semanticWordGapPattern + 'by|covered' + $semanticWordGapPattern + 'by)' + $semanticWordGapPattern + '(?:the' + $semanticWordGapPattern + ')?' + $decoratedIdentifierPattern),
            ('(?is)\brejects?' + $semanticWordGapPattern + '(?:the' + $semanticWordGapPattern + ')?(?:use' + $semanticWordGapPattern + 'of' + $semanticWordGapPattern + ')?' + $decoratedIdentifierPattern),
            ('(?is)' + $decoratedIdentifierPattern + $semanticWordGapPattern + '(?:does' + $semanticWordGapPattern + 'not' + $semanticWordGapPattern + 'apply|is' + $semanticWordGapPattern + 'not' + $semanticWordGapPattern + '(?:the' + $semanticWordGapPattern + ')?(?:applicable|license))\b'),
            ('(?:\u4e0d\u91c7\u7528|\u672a\u91c7\u7528|\u6ca1\u6709\u91c7\u7528|\u62d2\u7edd(?:\u91c7\u7528|\u4f7f\u7528)?|\u4e0d\u53d7)' + $semanticOptionalGapPattern + '(?:\u8bb8\u53ef\u8bc1?' + $semanticOptionalGapPattern + ')?' + $decoratedIdentifierPattern),
            ($decoratedIdentifierPattern + $semanticOptionalGapPattern + '(?:\u4e0d\u9002\u7528|\u4e0d\u751f\u6548|\u672a\u751f\u6548)')
        )
        foreach ($pattern in $sameLicenseConflictPatterns) {
            if ([regex]::IsMatch($proseContent, $pattern)) {
                Fail 'LICENSE_DECLARATION_CONFLICT' "$($declaration.Path) contains a contradictory AGPL-3.0-only declaration"
            }
        }
        if ($null -ne $declaration.Link -and $content.IndexOf($declaration.Link, [StringComparison]::Ordinal) -lt 0) {
            Fail 'LICENSE_DECLARATION_MISSING' "$($declaration.Path) must link the root LICENSE"
        }
        if ($content.IndexOf('AGPL-3.0-or-later', [StringComparison]::Ordinal) -ge 0) {
            Fail 'LICENSE_DECLARATION_CONFLICT' "$($declaration.Path) contains AGPL-3.0-or-later"
        }
        $spdxHeaders = [regex]::Matches($content, '(?im)^\s*(?://|#|;|<!--)?\s*SPDX-License-Identifier:\s*(?<id>[^\r\n<]+)')
        foreach ($spdxHeader in $spdxHeaders) {
            if ($spdxHeader.Groups['id'].Value.Trim() -cne 'AGPL-3.0-only') {
                Fail 'LICENSE_DECLARATION_CONFLICT' "$($declaration.Path) has a conflicting SPDX declaration"
            }
        }
        if (Test-ContainsSplitSPDXMarkerIntent $content) {
            Fail 'LICENSE_DECLARATION_CONFLICT' "$($declaration.Path) contains a split SPDX declaration"
        }
        $alternateIdentifier = '(?:AGPL-(?:1\.0|2\.0)(?:-only|-or-later)?|AGPL-3\.0-or-later|(?:GPL|LGPL)-(?:1\.0|2\.0|2\.1|3\.0)(?:-only|-or-later)?|MIT|Apache-2\.0|BSD-[234]-Clause|MPL-2\.0|ISC|CC0-1\.0|Unlicense)'
        $conflictingDeclarationPatterns = @(
            ('(?i)^[ \t]*(?:License|Licence):[ \t]*`?' + $alternateIdentifier + '`?[ \t]*$'),
            ('(?i)^[ \t]*(?:This project is licensed under|Licensed as|Contributions use|Contributions are licensed under)[ \t]+`?' + $alternateIdentifier + '`?[ \t]*[.;]?[ \t]*$'),
            ('^[ \t]*\u8bb8\u53ef\u8bc1[\uff1a:][ \t]*`?' + $alternateIdentifier + '`?[ \t]*$'),
            ('^[ \t]*(?:\u672c\u9879\u76ee|\u8d21\u732e\u5185\u5bb9)\u91c7\u7528[ \t]+`?' + $alternateIdentifier + '`?[ \t]+\u8bb8\u53ef\u8bc1\u3002[ \t]*$')
        )
        foreach ($line in $proseLines) {
            foreach ($pattern in $conflictingDeclarationPatterns) {
                if ([regex]::IsMatch($line, $pattern)) {
                    Fail 'LICENSE_DECLARATION_CONFLICT' "$($declaration.Path) has a conflicting project-license declaration"
                }
            }
        }
    }

    $goFiles = @(Get-RepositoryGoFiles $RepositoryRoot)
    foreach ($file in $goFiles) {
        $relativeGoPath = Get-RelativeSlashPath $RepositoryRoot $file.FullName
        if (Test-IsManagedAssetPath $relativeGoPath) { continue }
        $content = Read-Utf8Strict $file.FullName 'LICENSE_GO_SPDX_CONFLICT'
        Assert-StrictSPDXMarkers $content 'AGPL-3.0-only' 'LICENSE_GO_SPDX_CONFLICT' $relativeGoPath
    }
}

function Assert-R0ArtifactsExist([string]$RepositoryRoot) {
    $required = @(
        @{ Relative = $dependencyManifestRelative; Code = 'LICENSE_DEPENDENCY_MANIFEST_MISSING' },
        @{ Relative = $assetManifestRelative; Code = 'LICENSE_ASSET_MANIFEST_MISSING' },
        @{ Relative = $noticesRelative; Code = 'LICENSE_NOTICE_MISSING' }
    )
    $missing = New-Object System.Collections.Generic.List[string]
    foreach ($item in $required) {
        $path = Resolve-Inside $RepositoryRoot $item.Relative $item.Code
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            $missing.Add("[$($item.Code)] $($item.Relative) is missing")
        }
    }
    if ($missing.Count -ne 0) { throw ($missing -join [Environment]::NewLine) }
}

function Resolve-Go([string]$Requested) {
    if ([string]::IsNullOrWhiteSpace($Requested)) {
        $command = Get-Command go -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($null -eq $command) { Fail 'LICENSE_GO_COMMAND_MISSING' 'go is not available on PATH' }
        return $command.Source
    }
    if (-not [IO.Path]::IsPathRooted($Requested)) { Fail 'LICENSE_GO_COMMAND_INVALID' '-GoCommand must be an absolute path' }
    $full = [IO.Path]::GetFullPath($Requested)
    if (-not (Test-Path -LiteralPath $full -PathType Leaf)) { Fail 'LICENSE_GO_COMMAND_MISSING' "Go command does not exist: $full" }
    return $full
}

function Invoke-GoChecked([string]$Command, [string[]]$Arguments) {
    try {
        if ([IO.Path]::GetExtension($Command) -ieq '.ps1') {
            return @(& $Command @Arguments 2>&1 | ForEach-Object { [string]$_ })
        }
        $output = @(& $Command @Arguments 2>&1 | ForEach-Object { [string]$_ })
        $exitCode = $LASTEXITCODE
        if ($exitCode -ne 0) { Fail 'LICENSE_GO_COMMAND_FAILED' "$Command exited with code ${exitCode}; subprocess output omitted" }
        return $output
    } catch {
        if ($_.Exception.Message.StartsWith('[LICENSE_GO_COMMAND_FAILED]', [StringComparison]::Ordinal)) { throw }
        Fail 'LICENSE_GO_COMMAND_FAILED' "$Command failed; subprocess diagnostics omitted"
    }
}

function Invoke-GoModuleVerify([string]$RepositoryRoot, [string]$Command, [string]$Phase) {
    $goModPath = Join-Path $RepositoryRoot 'go.mod'
    $goSumPath = Join-Path $RepositoryRoot 'go.sum'
    $beforeMod = Get-SHA256File $goModPath
    $beforeSum = Get-SHA256File $goSumPath
    $oldLocation = Get-Location
    $oldGoWork = $env:GOWORK
    $oldGoProxy = $env:GOPROXY
    try {
        Set-Location -LiteralPath $RepositoryRoot
        $env:GOWORK = 'off'
        $env:GOPROXY = 'off'
        try {
            [void](Invoke-GoChecked $Command @('mod', 'verify'))
        } catch {
            $code = if ($Phase -ceq 'before') { 'LICENSE_GO_VERIFY_BEFORE_FAILED' } else { 'LICENSE_GO_VERIFY_AFTER_FAILED' }
            Fail $code "go mod verify failed ${Phase} the dependency archive scan; subprocess diagnostics omitted"
        }
    } finally {
        Set-Location -LiteralPath $oldLocation
        [Environment]::SetEnvironmentVariable('GOWORK', $oldGoWork, 'Process')
        [Environment]::SetEnvironmentVariable('GOPROXY', $oldGoProxy, 'Process')
        if ((Get-SHA256File $goModPath) -cne $beforeMod -or (Get-SHA256File $goSumPath) -cne $beforeSum) {
            Fail 'LICENSE_GO_SOURCE_MUTATED' "go mod verify changed go.mod or go.sum ${Phase} the dependency archive scan"
        }
    }
}

function Get-SelectedModules([string]$RepositoryRoot, [string]$Command) {
    $goModPath = Join-Path $RepositoryRoot 'go.mod'
    $goSumPath = Join-Path $RepositoryRoot 'go.sum'
    Assert-RegularFile $RepositoryRoot $goModPath 'LICENSE_GO_MOD_MISSING'
    Assert-RegularFile $RepositoryRoot $goSumPath 'LICENSE_GO_SUM_MISSING'
    $beforeMod = Get-SHA256File $goModPath
    $beforeSum = Get-SHA256File $goSumPath
    $oldLocation = Get-Location
    $oldGoWork = $env:GOWORK
    $oldGoProxy = $env:GOPROXY
    try {
        Set-Location -LiteralPath $RepositoryRoot
        $env:GOWORK = 'off'
        $env:GOPROXY = 'off'
        $reportedGoMod = ((Invoke-GoChecked $Command @('env', 'GOMOD')) -join "`n").Trim()
        try { $reportedGoModFull = [IO.Path]::GetFullPath($reportedGoMod) } catch {
            Fail 'LICENSE_GO_MODULE_ROOT_MISMATCH' 'go env GOMOD returned an invalid path; reported value omitted'
        }
        $expectedGoModFull = [IO.Path]::GetFullPath($goModPath)
        if (-not $reportedGoModFull.Equals($expectedGoModFull, $pathComparison)) {
            Fail 'LICENSE_GO_MODULE_ROOT_MISMATCH' 'go env GOMOD did not match the repository go.mod; reported value omitted'
        }
        $stream = (Invoke-GoChecked $Command @('list', '-mod=readonly', '-m', '-json', 'all')) -join "`n"
        try {
            $objects = [FreeAgent.Release.StrictJsonValidator]::SplitObjectStream($stream)
            $modules = @()
            foreach ($object in $objects) {
                $modules += ($object | ConvertFrom-Json -ErrorAction Stop)
            }
        } catch {
            Fail 'LICENSE_GO_GRAPH_INVALID' 'go returned an invalid strict JSON object stream; payload omitted'
        }
        return ,$modules
    } finally {
        Set-Location -LiteralPath $oldLocation
        [Environment]::SetEnvironmentVariable('GOWORK', $oldGoWork, 'Process')
        [Environment]::SetEnvironmentVariable('GOPROXY', $oldGoProxy, 'Process')
        if ((Get-SHA256File $goModPath) -cne $beforeMod -or (Get-SHA256File $goSumPath) -cne $beforeSum) {
            Fail 'LICENSE_GO_SOURCE_MUTATED' 'license verification changed go.mod or go.sum'
        }
    }
}

function Assert-DependencyManifest([string]$RepositoryRoot, $Manifest, $SelectedModules) {
    Assert-ExactProperties $Manifest @('schema_version', 'kind', 'main_module', 'compatibility_policy', 'license_refs', 'modules') 'LICENSE_DEPENDENCY_SCHEMA_INVALID' 'dependency manifest'
    Assert-NonNegativeInteger $Manifest.schema_version 'LICENSE_DEPENDENCY_SCHEMA_INVALID' 'dependency manifest schema_version'
    Assert-JsonString $Manifest.kind 'LICENSE_DEPENDENCY_SCHEMA_INVALID' 'dependency manifest kind'
    Assert-JsonString $Manifest.main_module 'LICENSE_DEPENDENCY_SCHEMA_INVALID' 'dependency manifest main_module'
    if ([int64]$Manifest.schema_version -ne 1 -or $Manifest.kind -cne 'freeagent-go-dependency-licenses') { Fail 'LICENSE_DEPENDENCY_SCHEMA_INVALID' 'dependency manifest header is invalid' }
    if ($Manifest.main_module -cne $expectedProjectModule) { Fail 'LICENSE_DEPENDENCY_SCHEMA_INVALID' 'dependency manifest main_module is invalid' }
    Assert-ExactProperties $Manifest.compatibility_policy @('id', 'version', 'project_license', 'source_dependency_mode', 'binary_targets', 'cgo_enabled', 'go_version') 'LICENSE_DEPENDENCY_SCHEMA_INVALID' 'compatibility_policy'
    foreach ($property in @('id', 'project_license', 'source_dependency_mode', 'go_version')) {
        Assert-JsonString $Manifest.compatibility_policy.$property 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "compatibility_policy $property"
    }
    Assert-NonNegativeInteger $Manifest.compatibility_policy.version 'LICENSE_DEPENDENCY_SCHEMA_INVALID' 'compatibility_policy version'
    Assert-JsonArray $Manifest.compatibility_policy.binary_targets 'LICENSE_DEPENDENCY_SCHEMA_INVALID' 'compatibility_policy binary_targets'
    Assert-JsonBoolean $Manifest.compatibility_policy.cgo_enabled 'LICENSE_DEPENDENCY_SCHEMA_INVALID' 'compatibility_policy cgo_enabled'
    $expectedTargets = @('darwin/amd64', 'darwin/arm64', 'linux/amd64', 'linux/arm64', 'windows/amd64', 'windows/arm64')
    foreach ($target in $Manifest.compatibility_policy.binary_targets) { Assert-JsonString $target 'LICENSE_DEPENDENCY_SCHEMA_INVALID' 'compatibility_policy binary target' }
    if ([string]$Manifest.compatibility_policy.id -cne $dependencyCompatibilityPolicyID -or
        [int64]$Manifest.compatibility_policy.version -ne $dependencyCompatibilityPolicyVersion -or
        [string]$Manifest.compatibility_policy.project_license -cne 'AGPL-3.0-only' -or
        [string]$Manifest.compatibility_policy.source_dependency_mode -cne 'module-reference-only' -or
        (@($Manifest.compatibility_policy.binary_targets) -join "`n") -cne ($expectedTargets -join "`n") -or
        [bool]$Manifest.compatibility_policy.cgo_enabled -ne $false -or
        [string]$Manifest.compatibility_policy.go_version -cne 'go1.26.5') {
        Fail 'LICENSE_DEPENDENCY_COMPATIBILITY_INVALID' 'compatibility_policy is not the exact reviewed release policy'
    }
    Assert-JsonArray $Manifest.license_refs 'LICENSE_DEPENDENCY_SCHEMA_INVALID' 'dependency manifest license_refs'
    Assert-JsonArray $Manifest.modules 'LICENSE_DEPENDENCY_SCHEMA_INVALID' 'dependency manifest modules'

    $licenseRefMap = @{}
    $licenseRefBindings = @{}
    $orderedLicenseRefIDs = New-Object System.Collections.Generic.List[string]
    foreach ($licenseRef in $Manifest.license_refs) {
        Assert-ExactProperties $licenseRef @('id', 'name', 'source_url', 'text_sha256', 'applies_to', 'required_file') 'LICENSE_DEPENDENCY_SCHEMA_INVALID' 'license_refs entry'
        foreach ($property in @('id', 'name', 'source_url', 'text_sha256', 'applies_to', 'required_file')) {
            Assert-JsonString $licenseRef.$property 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "license_refs entry $property"
        }
        $id = [string]$licenseRef.id
        if ($id -cnotmatch '^LicenseRef-[A-Za-z0-9.-]+$') { Fail 'LICENSE_LICENSE_REF_ID_INVALID' 'license_refs id is not canonical' }
        if ($licenseRefMap.ContainsKey($id)) { Fail 'LICENSE_LICENSE_REF_DUPLICATE' "duplicate LicenseRef id: $id" }
        Assert-HTTPSURL ([string]$licenseRef.source_url) 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$id source_url"
        Assert-Hash ([string]$licenseRef.text_sha256) 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$id text_sha256"
        if ([string]$licenseRef.applies_to -match '[|\r\n\t]' -or [string]$licenseRef.required_file -match '[|\r\n\t]') {
            Fail 'LICENSE_LICENSE_REF_SCOPE_INVALID' "$id contains an invalid identity scope or required file"
        }
        $bindingKey = [string]$licenseRef.applies_to + "`t" + [string]$licenseRef.required_file
        if ($licenseRefBindings.ContainsKey($bindingKey)) { Fail 'LICENSE_LICENSE_REF_BINDING_INVALID' "$id reuses another LicenseRef module/file binding" }
        $licenseRefBindings[$bindingKey] = $id
        $licenseRefMap[$id] = $licenseRef
        $orderedLicenseRefIDs.Add($id)
    }
    $sortedLicenseRefIDs = [string[]]@($orderedLicenseRefIDs)
    [Array]::Sort($sortedLicenseRefIDs, [StringComparer]::Ordinal)
    if (($orderedLicenseRefIDs -join "`n") -cne ($sortedLicenseRefIDs -join "`n")) { Fail 'LICENSE_LICENSE_REF_ORDER_INVALID' 'license_refs must be Ordinal sorted by id' }

    $main = @($SelectedModules | Where-Object { $null -ne $_.PSObject.Properties['Main'] -and [bool]$_.Main })
    if ($main.Count -ne 1 -or [string]$main[0].Path -cne $expectedProjectModule) { Fail 'LICENSE_GO_GRAPH_INVALID' 'selected graph must contain exactly one expected main module' }
    $selected = @($SelectedModules | Where-Object { $null -eq $_.PSObject.Properties['Main'] -or -not [bool]$_.Main })
    $selectedMap = @{}
    foreach ($module in $selected) {
        if ($null -ne $module.PSObject.Properties['Replace'] -and $null -ne $module.Replace) { Fail 'LICENSE_GO_REPLACE_FORBIDDEN' 'module replacement is forbidden; selected-module payload omitted' }
        foreach ($property in @('Path', 'Version', 'Dir', 'Sum', 'GoModSum')) {
            if ($null -eq $module.PSObject.Properties[$property] -or [string]::IsNullOrWhiteSpace([string]$module.$property)) {
                Fail 'LICENSE_MODULE_NOT_DOWNLOADED' "selected module lacks ${property}; selected-module payload omitted"
            }
        }
        $key = [string]$module.Path + '@' + [string]$module.Version
        if ($selectedMap.ContainsKey($key)) { Fail 'LICENSE_GO_GRAPH_INVALID' 'duplicate selected module; selected-module payload omitted' }
        $selectedMap[$key] = $module
    }

    $usedLicenseRefs = @{}
    $manifestMap = @{}
    $orderedKeys = New-Object System.Collections.Generic.List[string]
    foreach ($entry in $Manifest.modules) {
        Assert-ExactProperties $entry @('path', 'version', 'module_sum', 'go_mod_sum', 'declared_license_expression', 'source_url', 'notice_id', 'compatibility_conclusion', 'source_spdx_scan', 'required_files') 'LICENSE_DEPENDENCY_SCHEMA_INVALID' 'dependency entry'
        foreach ($property in @('path', 'version', 'module_sum', 'go_mod_sum', 'declared_license_expression', 'source_url', 'notice_id')) {
            Assert-JsonString $entry.$property 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "dependency entry $property"
        }
        $path = [string]$entry.path
        $version = [string]$entry.version
        if ([string]::IsNullOrWhiteSpace($path) -or [string]::IsNullOrWhiteSpace($version) -or $path.IndexOf('|') -ge 0 -or $version.IndexOf('|') -ge 0) { Fail 'LICENSE_DEPENDENCY_SCHEMA_INVALID' 'dependency identity is invalid' }
        $key = $path + '@' + $version
        if ($manifestMap.ContainsKey($key)) { Fail 'LICENSE_DEPENDENCY_DUPLICATE' "duplicate dependency: $key" }
        $manifestMap[$key] = $entry
        $orderedKeys.Add($key)
        Assert-H1 ([string]$entry.module_sum) 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key module_sum"
        Assert-H1 ([string]$entry.go_mod_sum) 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key go_mod_sum"
        Assert-DeclaredLicenseExpression ([string]$entry.declared_license_expression) $licenseRefMap $key $usedLicenseRefs
        Assert-HTTPSURL ([string]$entry.source_url) 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key source_url"
        $expectedNotice = Get-ExpectedNoticeID 'go' $key
        if ([string]$entry.notice_id -cne $expectedNotice) { Fail 'LICENSE_NOTICE_ID_INVALID' "$key notice_id must be $expectedNotice" }

        Assert-ExactProperties $entry.compatibility_conclusion @('status', 'policy_id', 'policy_version') 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key compatibility_conclusion"
        Assert-JsonString $entry.compatibility_conclusion.status 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key compatibility status"
        Assert-JsonString $entry.compatibility_conclusion.policy_id 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key compatibility policy_id"
        Assert-NonNegativeInteger $entry.compatibility_conclusion.policy_version 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key compatibility policy_version"
        if ([string]$entry.compatibility_conclusion.status -cne 'reviewed-compatible-for-policy' -or
            [string]$entry.compatibility_conclusion.policy_id -cne $dependencyCompatibilityPolicyID -or
            [int64]$entry.compatibility_conclusion.policy_version -ne $dependencyCompatibilityPolicyVersion) {
            Fail 'LICENSE_DEPENDENCY_COMPATIBILITY_INVALID' "$key compatibility conclusion is not the exact reviewed policy conclusion"
        }

        Assert-ExactProperties $entry.source_spdx_scan @('algorithm', 'occurrence_count', 'sha256', 'expressions') 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key source_spdx_scan"
        Assert-JsonString $entry.source_spdx_scan.algorithm 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key source_spdx_scan algorithm"
        Assert-NonNegativeInteger $entry.source_spdx_scan.occurrence_count 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key source_spdx_scan occurrence_count"
        Assert-JsonString $entry.source_spdx_scan.sha256 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key source_spdx_scan sha256"
        Assert-Hash ([string]$entry.source_spdx_scan.sha256) 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key source_spdx_scan sha256"
        Assert-JsonArray $entry.source_spdx_scan.expressions 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key source_spdx_scan expressions"
        if ([string]$entry.source_spdx_scan.algorithm -cne $sourceSPDXScanAlgorithm) { Fail 'LICENSE_DEPENDENCY_SPDX_SCAN_SCHEMA_INVALID' "$key uses an unsupported source SPDX scan algorithm" }
        $reportedExpressions = New-Object System.Collections.Generic.List[string]
        $seenExpressions = New-Object 'Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
        foreach ($expression in $entry.source_spdx_scan.expressions) {
            Assert-JsonString $expression 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key source_spdx_scan expression"
            if ($approvedDetectedSPDXExpressions -cnotcontains [string]$expression) { Fail 'LICENSE_DEPENDENCY_SPDX_UNKNOWN' "$key reports an unknown detected SPDX expression" }
            if (-not ($seenExpressions.Add([string]$expression))) { Fail 'LICENSE_DEPENDENCY_SPDX_SCAN_SCHEMA_INVALID' "$key source_spdx_scan expressions contain a duplicate" }
            $reportedExpressions.Add([string]$expression)
        }
        $sortedExpressions = [string[]]@($reportedExpressions)
        [Array]::Sort($sortedExpressions, [StringComparer]::Ordinal)
        if (($reportedExpressions -join "`n") -cne ($sortedExpressions -join "`n")) { Fail 'LICENSE_DEPENDENCY_SPDX_SCAN_SCHEMA_INVALID' "$key source_spdx_scan expressions must be Ordinal sorted" }

        Assert-JsonArray $entry.required_files 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key required_files"
        if ($entry.required_files.Count -eq 0) { Fail 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key required_files must not be empty" }
    }
    $sortedKeys = [string[]]@($orderedKeys)
    [Array]::Sort($sortedKeys, [StringComparer]::Ordinal)
    if (($orderedKeys -join "`n") -cne ($sortedKeys -join "`n")) { Fail 'LICENSE_DEPENDENCY_ORDER_INVALID' 'dependency entries must be Ordinal sorted by path@version' }

    foreach ($id in $licenseRefMap.Keys) {
        if (-not $manifestMap.ContainsKey([string]$licenseRefMap[$id].applies_to)) { Fail 'LICENSE_LICENSE_REF_SCOPE_INVALID' "$id does not identify an exact selected module"
        }
    }
    $selectedKeys = [string[]]@($selectedMap.Keys)
    $manifestKeys = [string[]]@($manifestMap.Keys)
    [Array]::Sort($selectedKeys, [StringComparer]::Ordinal)
    [Array]::Sort($manifestKeys, [StringComparer]::Ordinal)
    if (($selectedKeys -join "`n") -cne ($manifestKeys -join "`n")) {
        Fail 'LICENSE_DEPENDENCY_GRAPH_MISMATCH' 'dependency manifest does not exactly match the selected Go module graph'
    }

    foreach ($key in $manifestKeys) {
        $entry = $manifestMap[$key]
        $module = $selectedMap[$key]
        if ([string]$entry.module_sum -cne [string]$module.Sum -or [string]$entry.go_mod_sum -cne [string]$module.GoModSum) {
            Fail 'LICENSE_DEPENDENCY_CHECKSUM_MISMATCH' "$key checksum differs from the selected module"
        }
        if (-not [IO.Path]::IsPathRooted([string]$module.Dir)) { Fail 'LICENSE_MODULE_NOT_DOWNLOADED' "$key directory path is not absolute" }
        try { $moduleDir = [IO.Path]::GetFullPath([string]$module.Dir) } catch { Fail 'LICENSE_MODULE_NOT_DOWNLOADED' "$key directory path is invalid" }
        if (-not (Test-Path -LiteralPath $moduleDir -PathType Container)) { Fail 'LICENSE_MODULE_NOT_DOWNLOADED' "$key directory is unavailable" }
        Assert-NoReparseAncestry $moduleDir 'LICENSE_MODULE_REPARSE_FORBIDDEN'
        $derivedLegalBundle = Get-ModuleLegalBundle $moduleDir $key
        $fileKeys = New-Object System.Collections.Generic.List[string]
        $reportedFiles = @{}
        foreach ($file in $entry.required_files) {
            Assert-ExactProperties $file @('path', 'role', 'size', 'sha256') 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key required file"
            Assert-JsonString $file.path 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key required-file path"
            Assert-JsonString $file.role 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key required-file role"
            Assert-JsonString $file.sha256 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key required-file sha256"
            $relative = [string]$file.path
            $role = [string]$file.role
            if ($relative.IndexOf('|', [StringComparison]::Ordinal) -ge 0) { Fail 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key required-file path contains a Markdown table delimiter" }
            if (@('license', 'notice', 'copyright', 'patent', 'attribution') -cnotcontains $role) { Fail 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key has invalid required-file role '$role'" }
            if (-not (Test-IsModuleLegalPath $relative)) { Fail 'LICENSE_DEPENDENCY_LEGAL_SET_MISMATCH' "$key required file is not part of the legal bundle: $relative" }
            [void](Resolve-Inside $moduleDir $relative 'LICENSE_DEPENDENCY_SCHEMA_INVALID')
            if (@($reportedFiles.Keys | Where-Object { $_.Equals($relative, [StringComparison]::OrdinalIgnoreCase) }).Count -ne 0) { Fail 'LICENSE_DEPENDENCY_DUPLICATE' "$key has duplicate required file $relative" }
            $reportedFiles[$relative] = $file
            $fileKeys.Add($relative)
            Assert-NonNegativeInteger $file.size 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key required-file size"
            Assert-Hash ([string]$file.sha256) 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key required-file sha256"
        }
        $derivedFileNames = [string[]]@($derivedLegalBundle.Keys)
        $reportedFileNames = [string[]]@($reportedFiles.Keys)
        [Array]::Sort($derivedFileNames, [StringComparer]::Ordinal)
        [Array]::Sort($reportedFileNames, [StringComparer]::Ordinal)
        if (($derivedFileNames -join "`n") -cne ($reportedFileNames -join "`n")) {
            Fail 'LICENSE_DEPENDENCY_LEGAL_SET_MISMATCH' "$key required_files do not exactly match the legal-file bundle"
        }
        $licenseCount = 0
        foreach ($relative in $reportedFileNames) {
            $file = $reportedFiles[$relative]
            $derivedFile = $derivedLegalBundle[$relative]
            if ([string]$file.role -cne [string]$derivedFile.Role) { Fail 'LICENSE_DEPENDENCY_LEGAL_ROLE_MISMATCH' "$key derives a different role for $relative" }
            if ([string]$derivedFile.Role -ceq 'license') { $licenseCount++ }
            $fullPath = $derivedFile.Item.FullName
            $logicalFileLabel = "$key required file $relative"
            try {
                $actual = Get-Item -Force -LiteralPath $fullPath
                $actualHash = Get-SHA256File $fullPath
            } catch {
                Fail 'LICENSE_DEPENDENCY_FILE_INVALID' "$logicalFileLabel could not be inspected"
            }
            if ([int64]$actual.Length -ne [int64]$file.size -or $actualHash -cne [string]$file.sha256) {
                Fail 'LICENSE_DEPENDENCY_FILE_HASH_MISMATCH' "$key required file differs: $relative"
            }
            $legalText = Read-Utf8Strict $fullPath 'LICENSE_DEPENDENCY_TEXT_INVALID' -Label $logicalFileLabel
            if (Test-IsBlankLegalText $legalText) { Fail 'LICENSE_DEPENDENCY_TEXT_INVALID' "$logicalFileLabel must not be blank" }
        }
        if ($licenseCount -eq 0) { Fail 'LICENSE_DEPENDENCY_SCHEMA_INVALID' "$key must include at least one license file" }
        $sortedFileKeys = [string[]]@($fileKeys)
        [Array]::Sort($sortedFileKeys, [StringComparer]::Ordinal)
        if (($fileKeys -join "`n") -cne ($sortedFileKeys -join "`n")) { Fail 'LICENSE_DEPENDENCY_ORDER_INVALID' "$key required_files must be Ordinal sorted" }

        foreach ($id in $licenseRefMap.Keys) {
            $licenseRef = $licenseRefMap[$id]
            if ([string]$licenseRef.applies_to -cne $key) { continue }
            $requiredPath = [string]$licenseRef.required_file
            if (-not $reportedFiles.ContainsKey($requiredPath) -or [string]$reportedFiles[$requiredPath].role -cne 'license' -or
                [string]$reportedFiles[$requiredPath].sha256 -cne [string]$licenseRef.text_sha256) {
                Fail 'LICENSE_LICENSE_REF_BINDING_INVALID' "$id is not bound to its exact required license file and hash"
            }
            if (-not $usedLicenseRefs.ContainsKey($id)) { Fail 'LICENSE_LICENSE_REF_UNUSED' "$id is declared but unused by its scoped module" }
        }

        $actualScan = Get-ModuleSourceSPDXScan $moduleDir $key
        $reportedScan = $entry.source_spdx_scan
        if ([int64]$reportedScan.occurrence_count -ne [int64]$actualScan.occurrence_count -or
            [string]$reportedScan.sha256 -cne [string]$actualScan.sha256 -or
            (@($reportedScan.expressions) -join "`n") -cne (@($actualScan.expressions) -join "`n")) {
            Fail 'LICENSE_DEPENDENCY_SPDX_SCAN_MISMATCH' "$key source SPDX scan attestation differs from the complete module archive"
        }
    }
    return $selectedMap
}

function Get-DerivedAssets([string]$RepositoryRoot) {
    $derived = @{}
    foreach ($relativeRoot in $assetRoots) {
        $fullRoot = Resolve-Inside $RepositoryRoot $relativeRoot 'LICENSE_ASSET_PATH_INVALID'
        if (-not (Test-Path -LiteralPath $fullRoot)) { continue }
        if (-not (Test-Path -LiteralPath $fullRoot -PathType Container)) { Fail 'LICENSE_ASSET_PATH_INVALID' "$relativeRoot must be a directory" }
        Assert-NoReparsePath $RepositoryRoot $fullRoot 'LICENSE_ASSET_REPARSE_FORBIDDEN'
        $queue = New-Object 'Collections.Generic.Queue[string]'
        $queue.Enqueue($fullRoot)
        while ($queue.Count -ne 0) {
            $directory = $queue.Dequeue()
            foreach ($item in @(Get-ChildItem -LiteralPath $directory -Force)) {
                if (Test-Reparse $item) { Fail 'LICENSE_ASSET_REPARSE_FORBIDDEN' "reparse asset path is forbidden: $($item.FullName)" }
                if ($item.PSIsContainer) {
                    $queue.Enqueue($item.FullName)
                    continue
                }
                Assert-RegularFile $RepositoryRoot $item.FullName 'LICENSE_ASSET_PATH_INVALID'
                $relative = Get-RelativeSlashPath $RepositoryRoot $item.FullName
                if ($assetControlFiles -ccontains $relative) { continue }
                $folded = $relative.ToLowerInvariant()
                if (@($derived.Keys | Where-Object { $_.ToLowerInvariant() -ceq $folded }).Count -ne 0) {
                    Fail 'LICENSE_ASSET_DUPLICATE' "case-insensitive asset collision: $relative"
                }
                $derived[$relative] = [pscustomobject]@{ Size = [int64]$item.Length; SHA256 = Get-SHA256File $item.FullName }
            }
        }
    }
    return $derived
}

function Get-AssetLegalBaseDirectory([string]$RequiredPath) {
    $nestedBase = Get-NestedLicensesBase $RequiredPath
    if ($null -ne $nestedBase) { return $nestedBase }
    $slash = $RequiredPath.LastIndexOf([char]47)
    if ($slash -lt 1) { return $null }
    $baseName = $RequiredPath.Substring($slash + 1)
    if ($null -eq (Get-LegalFileRole $baseName)) { return $null }
    return $RequiredPath.Substring(0, $slash)
}

function Get-AssetLegalBundle([string]$RepositoryRoot, $RequiredFiles, [string]$AssetPath) {
    $directories = @{}
    foreach ($file in $RequiredFiles) {
        $requiredPath = [string]$file.path
        $relativeDirectory = Get-AssetLegalBaseDirectory $requiredPath
        if ($null -eq $relativeDirectory) { Fail 'LICENSE_ASSET_LEGAL_SET_MISMATCH' "$AssetPath required file is not part of a legal-directory bundle: $requiredPath" }
        $directories[$relativeDirectory] = $true
    }
    $bundle = @{}
    $orderedDirectories = [string[]]@($directories.Keys)
    [Array]::Sort($orderedDirectories, [StringComparer]::Ordinal)
    foreach ($relativeDirectory in $orderedDirectories) {
        $fullDirectory = Resolve-Inside $RepositoryRoot $relativeDirectory 'LICENSE_ASSET_LEGAL_PATH_INVALID'
        if (-not (Test-Path -LiteralPath $fullDirectory -PathType Container)) { Fail 'LICENSE_ASSET_LEGAL_PATH_INVALID' "$AssetPath legal directory is missing: $relativeDirectory" }
        Assert-NoReparsePath $RepositoryRoot $fullDirectory 'LICENSE_ASSET_LEGAL_PATH_INVALID' "$AssetPath legal directory $relativeDirectory"
        foreach ($item in @(Get-ChildItem -LiteralPath $fullDirectory -Force)) {
            if ($item.Name -ieq 'LICENSES') {
                $directoryLabel = "$AssetPath legal directory $relativeDirectory/LICENSES"
                if ($item.Name -cne 'LICENSES') { Fail 'LICENSE_ASSET_LEGAL_BUNDLE_INVALID' "$directoryLabel must use the canonical directory name" }
                if (-not $item.PSIsContainer -or (Test-Reparse $item)) { Fail 'LICENSE_ASSET_LEGAL_BUNDLE_INVALID' "$directoryLabel must be a regular directory" }
                Assert-NoReparsePath $RepositoryRoot $item.FullName 'LICENSE_ASSET_LEGAL_BUNDLE_INVALID' $directoryLabel
                $queue = New-Object 'Collections.Generic.Queue[string]'
                $queue.Enqueue($item.FullName)
                while ($queue.Count -ne 0) {
                    $directory = $queue.Dequeue()
                    foreach ($nested in @(Get-ChildItem -LiteralPath $directory -Force)) {
                        $relative = Get-RelativeSlashPath $RepositoryRoot $nested.FullName
                        $nestedLabel = "$AssetPath legal file $relative"
                        if (Test-Reparse $nested) { Fail 'LICENSE_ASSET_LEGAL_BUNDLE_INVALID' "reparse path is forbidden: $nestedLabel" }
                        if ($nested.PSIsContainer) {
                            $queue.Enqueue($nested.FullName)
                        } else {
                            Assert-RegularFile $RepositoryRoot $nested.FullName 'LICENSE_ASSET_LEGAL_BUNDLE_INVALID' $nestedLabel
                            Add-LegalBundleFile $bundle $relative 'license' $nested 'LICENSE_ASSET_LEGAL_BUNDLE_INVALID' $AssetPath
                        }
                    }
                }
                continue
            }
            $role = Get-LegalFileRole $item.Name
            if ($null -eq $role) { continue }
            $relative = $relativeDirectory + '/' + $item.Name
            $label = "$AssetPath legal file $relative"
            if ($item.PSIsContainer -or (Test-Reparse $item)) { Fail 'LICENSE_ASSET_LEGAL_BUNDLE_INVALID' "$label must be a regular file" }
            Assert-RegularFile $RepositoryRoot $item.FullName 'LICENSE_ASSET_LEGAL_BUNDLE_INVALID' $label
            Add-LegalBundleFile $bundle $relative $role $item 'LICENSE_ASSET_LEGAL_BUNDLE_INVALID' $AssetPath
        }
    }
    return $bundle
}

function Assert-AssetGoSPDX([string]$RepositoryRoot, $Entry) {
    $path = [string]$Entry.path
    if (-not $path.EndsWith('.go', [StringComparison]::Ordinal)) { return }
    $fullPath = Resolve-Inside $RepositoryRoot $path 'LICENSE_ASSET_PATH_INVALID'
    $content = Read-Utf8Strict $fullPath 'LICENSE_ASSET_GO_SPDX_CONFLICT' -Label $path
    $expected = if ([string]$Entry.origin -ceq 'project-owned') { 'AGPL-3.0-only' } else { [string]$Entry.spdx_expression }
    Assert-StrictSPDXMarkers $content $expected 'LICENSE_ASSET_GO_SPDX_CONFLICT' $path
}

function Assert-AssetManifest([string]$RepositoryRoot, $Manifest) {
    Assert-ExactProperties $Manifest @('schema_version', 'kind', 'assets') 'LICENSE_ASSET_SCHEMA_INVALID' 'distributed-assets manifest'
    Assert-NonNegativeInteger $Manifest.schema_version 'LICENSE_ASSET_SCHEMA_INVALID' 'distributed-assets schema_version'
    Assert-JsonString $Manifest.kind 'LICENSE_ASSET_SCHEMA_INVALID' 'distributed-assets kind'
    if ([int64]$Manifest.schema_version -ne 1 -or $Manifest.kind -cne 'freeagent-distributed-assets') { Fail 'LICENSE_ASSET_SCHEMA_INVALID' 'distributed-assets manifest header is invalid' }
    Assert-JsonArray $Manifest.assets 'LICENSE_ASSET_SCHEMA_INVALID' 'distributed-assets assets'
    $derived = Get-DerivedAssets $RepositoryRoot
    $manifestMap = @{}
    $ordered = New-Object System.Collections.Generic.List[string]
    foreach ($entry in $Manifest.assets) {
        Assert-ExactProperties $entry @('path', 'size', 'sha256', 'origin', 'source_url', 'spdx_expression', 'notice_id', 'required_files') 'LICENSE_ASSET_SCHEMA_INVALID' 'distributed asset entry'
        foreach ($property in @('path', 'sha256', 'origin', 'source_url', 'spdx_expression')) {
            Assert-JsonString $entry.$property 'LICENSE_ASSET_SCHEMA_INVALID' "distributed asset $property"
        }
        Assert-JsonString $entry.notice_id 'LICENSE_ASSET_SCHEMA_INVALID' 'distributed asset notice_id' -AllowEmpty
        Assert-JsonArray $entry.required_files 'LICENSE_ASSET_SCHEMA_INVALID' 'distributed asset required_files'
        $path = $entry.path
        if ($path.IndexOf('|', [StringComparison]::Ordinal) -ge 0) { Fail 'LICENSE_ASSET_SCHEMA_INVALID' 'distributed asset path contains a Markdown table delimiter' }
        [void](Resolve-Inside $RepositoryRoot $path 'LICENSE_ASSET_PATH_INVALID')
        if ($manifestMap.ContainsKey($path) -or @($manifestMap.Keys | Where-Object { $_.ToLowerInvariant() -ceq $path.ToLowerInvariant() }).Count -ne 0) {
            Fail 'LICENSE_ASSET_DUPLICATE' "duplicate distributed asset: $path"
        }
        $manifestMap[$path] = $entry
        $ordered.Add($path)
        Assert-NonNegativeInteger $entry.size 'LICENSE_ASSET_SCHEMA_INVALID' "$path size"
        Assert-Hash ([string]$entry.sha256) 'LICENSE_ASSET_SCHEMA_INVALID' "$path sha256"
        $origin = [string]$entry.origin
        if ($origin -ceq 'project-owned') {
            if ([string]$entry.source_url -cne $projectAssetSource -or [string]$entry.spdx_expression -cne 'AGPL-3.0-only' -or [string]$entry.notice_id -cne '') {
                Fail 'LICENSE_ASSET_SCHEMA_INVALID' "$path project-owned provenance is invalid"
            }
            if ($entry.required_files.Count -ne 0) { Fail 'LICENSE_ASSET_SCHEMA_INVALID' "$path project-owned required_files must be empty" }
        } elseif ($origin -ceq 'third-party') {
            Assert-HTTPSURL ([string]$entry.source_url) 'LICENSE_ASSET_SCHEMA_INVALID' "$path source_url"
            if ($approvedThirdPartySPDX -cnotcontains [string]$entry.spdx_expression) { Fail 'LICENSE_UNKNOWN_SPDX' "$path uses unapproved SPDX expression '$($entry.spdx_expression)'" }
            $expectedNotice = Get-ExpectedNoticeID 'asset' $path
            if ([string]$entry.notice_id -cne $expectedNotice) { Fail 'LICENSE_NOTICE_ID_INVALID' "$path notice_id must be $expectedNotice" }
            if ($entry.required_files.Count -eq 0) { Fail 'LICENSE_ASSET_LEGAL_REQUIRED' "$path must include legal required_files" }
            $requiredKeys = New-Object System.Collections.Generic.List[string]
            $reportedRequired = @{}
            foreach ($file in $entry.required_files) {
                Assert-ExactProperties $file @('path', 'role', 'size', 'sha256') 'LICENSE_ASSET_SCHEMA_INVALID' "$path required file"
                Assert-JsonString $file.path 'LICENSE_ASSET_SCHEMA_INVALID' "$path required-file path"
                Assert-JsonString $file.role 'LICENSE_ASSET_SCHEMA_INVALID' "$path required-file role"
                Assert-JsonString $file.sha256 'LICENSE_ASSET_SCHEMA_INVALID' "$path required-file sha256"
                $requiredPath = [string]$file.path
                $role = [string]$file.role
                if ($requiredPath.IndexOf('|', [StringComparison]::Ordinal) -ge 0) { Fail 'LICENSE_ASSET_SCHEMA_INVALID' "$path required-file path contains a Markdown table delimiter" }
                if (-not ($requiredPath.StartsWith('third_party/', [StringComparison]::Ordinal) -or
                    $requiredPath.StartsWith('vendor/', [StringComparison]::Ordinal) -or
                    $requiredPath.StartsWith('docs/assets/', [StringComparison]::Ordinal))) {
                    Fail 'LICENSE_ASSET_LEGAL_PATH_INVALID' "$path legal required file must be under third_party, vendor, or docs/assets"
                }
                [void](Resolve-Inside $RepositoryRoot $requiredPath 'LICENSE_ASSET_LEGAL_PATH_INVALID')
                if (@('license', 'notice', 'copyright', 'patent', 'attribution') -cnotcontains $role) { Fail 'LICENSE_ASSET_SCHEMA_INVALID' "$path has invalid required-file role '$role'" }
                if ($null -eq (Get-AssetLegalBaseDirectory $requiredPath)) { Fail 'LICENSE_ASSET_LEGAL_SET_MISMATCH' "$path required file is not part of a legal-directory bundle: $requiredPath" }
                $requiredKey = $requiredPath
                if (@($reportedRequired.Keys | Where-Object { $_.Equals($requiredPath, [StringComparison]::OrdinalIgnoreCase) }).Count -ne 0) { Fail 'LICENSE_ASSET_DUPLICATE' "$path has duplicate required file $requiredPath" }
                $reportedRequired[$requiredPath] = $file
                $requiredKeys.Add($requiredKey)
                Assert-NonNegativeInteger $file.size 'LICENSE_ASSET_SCHEMA_INVALID' "$path required-file size"
                Assert-Hash ([string]$file.sha256) 'LICENSE_ASSET_SCHEMA_INVALID' "$path required-file sha256"
            }
            $derivedLegalBundle = Get-AssetLegalBundle $RepositoryRoot $entry.required_files $path
            $derivedRequiredPaths = [string[]]@($derivedLegalBundle.Keys)
            $reportedRequiredPaths = [string[]]@($reportedRequired.Keys)
            [Array]::Sort($derivedRequiredPaths, [StringComparer]::Ordinal)
            [Array]::Sort($reportedRequiredPaths, [StringComparer]::Ordinal)
            if (($derivedRequiredPaths -join "`n") -cne ($reportedRequiredPaths -join "`n")) {
                Fail 'LICENSE_ASSET_LEGAL_SET_MISMATCH' "$path required_files do not exactly match their legal-directory bundles"
            }
            $licenseCount = 0
            foreach ($requiredPath in $reportedRequiredPaths) {
                $file = $reportedRequired[$requiredPath]
                $derivedFile = $derivedLegalBundle[$requiredPath]
                if ([string]$file.role -cne [string]$derivedFile.Role) { Fail 'LICENSE_ASSET_LEGAL_ROLE_MISMATCH' "$path derives a different role for $requiredPath" }
                if ([string]$derivedFile.Role -ceq 'license') { $licenseCount++ }
                $requiredItem = $derivedFile.Item
                if ([int64]$requiredItem.Length -ne [int64]$file.size -or (Get-SHA256File $requiredItem.FullName) -cne [string]$file.sha256) {
                    Fail 'LICENSE_ASSET_LEGAL_HASH_MISMATCH' "$path legal required file differs: $requiredPath"
                }
                $legalText = Read-Utf8Strict $requiredItem.FullName 'LICENSE_ASSET_LEGAL_TEXT_INVALID' -Label "$path legal file $requiredPath"
                if (Test-IsBlankLegalText $legalText) { Fail 'LICENSE_ASSET_LEGAL_TEXT_INVALID' "$path legal file $requiredPath must not be blank" }
            }
            if ($licenseCount -eq 0) { Fail 'LICENSE_ASSET_LEGAL_REQUIRED' "$path must include at least one license file" }
            $sortedRequiredKeys = [string[]]@($requiredKeys)
            [Array]::Sort($sortedRequiredKeys, [StringComparer]::Ordinal)
            if (($requiredKeys -join "`n") -cne ($sortedRequiredKeys -join "`n")) { Fail 'LICENSE_ASSET_ORDER_INVALID' "$path required_files must be Ordinal sorted" }
        } else {
            Fail 'LICENSE_ASSET_SCHEMA_INVALID' "$path has invalid origin '$origin'"
        }
    }
    $sorted = [string[]]@($ordered)
    [Array]::Sort($sorted, [StringComparer]::Ordinal)
    if (($ordered -join "`n") -cne ($sorted -join "`n")) { Fail 'LICENSE_ASSET_ORDER_INVALID' 'distributed assets must be Ordinal sorted by path' }
    $derivedKeys = [string[]]@($derived.Keys)
    $manifestKeys = [string[]]@($manifestMap.Keys)
    [Array]::Sort($derivedKeys, [StringComparer]::Ordinal)
    [Array]::Sort($manifestKeys, [StringComparer]::Ordinal)
    if (($derivedKeys -join "`n") -cne ($manifestKeys -join "`n")) { Fail 'LICENSE_ASSET_SET_MISMATCH' 'distributed-assets manifest does not exactly match the derived asset set' }
    foreach ($path in $manifestKeys) {
        if ([int64]$manifestMap[$path].size -ne [int64]$derived[$path].Size -or [string]$manifestMap[$path].sha256 -cne [string]$derived[$path].SHA256) {
            Fail 'LICENSE_ASSET_HASH_MISMATCH' "distributed asset differs: $path"
        }
        Assert-AssetGoSPDX $RepositoryRoot $manifestMap[$path]
    }
    foreach ($entry in $Manifest.assets) {
        if ([string]$entry.origin -cne 'third-party') { continue }
        foreach ($file in $entry.required_files) {
            $requiredPath = [string]$file.path
            $exactRequiredMatches = @($manifestMap.Keys | Where-Object { $_ -ceq $requiredPath })
            if ($exactRequiredMatches.Count -ne 1 -or [string]$manifestMap[$exactRequiredMatches[0]].origin -cne 'third-party') {
                Fail 'LICENSE_ASSET_LEGAL_PATH_INVALID' "$($entry.path) legal required file must itself be a declared third-party distributed asset"
            }
        }
    }
    return $manifestMap
}

function Assert-FrontendDependencyManifest([string]$RepositoryRoot, $Manifest) {
    $code = 'LICENSE_FRONTEND_DEPENDENCY_SCHEMA_INVALID'
    Assert-ExactProperties $Manifest @('schema_version', 'kind', 'package_lock', 'build_environment', 'packages', 'distributed_chunks') $code 'frontend dependency manifest'
    Assert-NonNegativeInteger $Manifest.schema_version $code 'frontend dependency schema_version'
    Assert-JsonString $Manifest.kind $code 'frontend dependency kind'
    if ([int64]$Manifest.schema_version -ne 1 -or $Manifest.kind -cne 'freeagent-npm-dependency-licenses') {
        Fail $code 'frontend dependency manifest header is invalid'
    }

    Assert-ExactProperties $Manifest.package_lock @('path', 'sha256', 'lockfile_version') $code 'frontend package_lock'
    Assert-JsonString $Manifest.package_lock.path $code 'frontend package-lock path'
    Assert-JsonString $Manifest.package_lock.sha256 $code 'frontend package-lock sha256'
    Assert-Hash ([string]$Manifest.package_lock.sha256) $code 'frontend package-lock sha256'
    Assert-NonNegativeInteger $Manifest.package_lock.lockfile_version $code 'frontend lockfile_version'
    if ([string]$Manifest.package_lock.path -cne 'internal/controlweb/package-lock.json' -or [int64]$Manifest.package_lock.lockfile_version -ne 3) {
        Fail $code 'frontend package-lock metadata is invalid'
    }
    $packageLockPath = Resolve-Inside $RepositoryRoot ([string]$Manifest.package_lock.path) 'LICENSE_FRONTEND_DEPENDENCY_LOCK_INVALID'
    Assert-RegularFile $RepositoryRoot $packageLockPath 'LICENSE_FRONTEND_DEPENDENCY_LOCK_INVALID' 'frontend package-lock'
    if ((Get-SHA256File $packageLockPath) -cne [string]$Manifest.package_lock.sha256) {
        Fail 'LICENSE_FRONTEND_DEPENDENCY_LOCK_MISMATCH' 'frontend package-lock differs from the license manifest binding'
    }

    Assert-ExactProperties $Manifest.build_environment @('node_version', 'npm_version', 'registry', 'install_scripts') $code 'frontend build_environment'
    foreach ($property in @('node_version', 'npm_version', 'registry', 'install_scripts')) {
        Assert-JsonString $Manifest.build_environment.$property $code "frontend build_environment $property"
    }
    if ([string]$Manifest.build_environment.node_version -cne '24.19.0' -or
        [string]$Manifest.build_environment.npm_version -cne '12.0.2' -or
        [string]$Manifest.build_environment.registry -cne 'https://registry.npmjs.org/' -or
        [string]$Manifest.build_environment.install_scripts -cne 'disabled') {
        Fail $code 'frontend build environment is invalid'
    }

    Assert-JsonArray $Manifest.packages $code 'frontend packages'
    $packageMap = @{}
    $orderedKeys = New-Object System.Collections.Generic.List[string]
    foreach ($entry in $Manifest.packages) {
        Assert-ExactProperties $entry @('name', 'version', 'dependency_kind', 'optional', 'resolved', 'integrity', 'declared_license_expression', 'source_url', 'notice_id', 'required_files') $code 'frontend package'
        foreach ($property in @('name', 'version', 'dependency_kind', 'resolved', 'integrity', 'declared_license_expression', 'source_url', 'notice_id')) {
            Assert-JsonString $entry.$property $code "frontend package $property"
        }
        Assert-JsonBoolean $entry.optional $code 'frontend package optional'
        Assert-JsonArray $entry.required_files $code 'frontend package required_files'
        $name = [string]$entry.name
        $version = [string]$entry.version
        if ($name -cnotmatch '^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$' -or $version -cnotmatch '^[0-9]+\.[0-9]+\.[0-9]+$') {
            Fail $code "frontend package identity is invalid: $name@$version"
        }
        $key = $name + '@' + $version
        if ($packageMap.ContainsKey($key)) { Fail 'LICENSE_FRONTEND_DEPENDENCY_DUPLICATE' "duplicate frontend package: $key" }
        $packageMap[$key] = $entry
        $orderedKeys.Add($key)
        if (@('runtime', 'build') -cnotcontains [string]$entry.dependency_kind) { Fail $code "$key dependency_kind is invalid" }
        Assert-HTTPSURL ([string]$entry.resolved) $code "$key resolved"
        Assert-HTTPSURL ([string]$entry.source_url) $code "$key source_url"
        if ([string]$entry.source_url -cne [string]$entry.resolved -or [string]$entry.integrity -cnotmatch '^sha512-[A-Za-z0-9+/]+={0,2}$') {
            Fail $code "$key registry provenance is invalid"
        }
        if ($approvedThirdPartySPDX -cnotcontains [string]$entry.declared_license_expression) {
            Fail 'LICENSE_UNKNOWN_SPDX' "$key uses unapproved SPDX expression '$($entry.declared_license_expression)'"
        }
        $expectedNotice = Get-ExpectedNoticeID 'npm' $key
        if ([string]$entry.notice_id -cne $expectedNotice) { Fail 'LICENSE_NOTICE_ID_INVALID' "$key notice_id must be $expectedNotice" }
        if ($entry.required_files.Count -ne 1) { Fail 'LICENSE_FRONTEND_DEPENDENCY_LEGAL_REQUIRED' "$key must bind exactly one legal file" }
        $file = $entry.required_files[0]
        Assert-ExactProperties $file @('path', 'role', 'size', 'sha256') $code "$key required file"
        Assert-JsonString $file.path $code "$key required-file path"
        Assert-JsonString $file.role $code "$key required-file role"
        Assert-JsonString $file.sha256 $code "$key required-file sha256"
        Assert-NonNegativeInteger $file.size $code "$key required-file size"
        Assert-Hash ([string]$file.sha256) $code "$key required-file sha256"
        $relative = [string]$file.path
        if (-not $relative.StartsWith('third_party/npm/', [StringComparison]::Ordinal) -or [string]$file.role -cne 'license') {
            Fail 'LICENSE_FRONTEND_DEPENDENCY_LEGAL_PATH_INVALID' "$key legal file is invalid"
        }
        $full = Resolve-Inside $RepositoryRoot $relative 'LICENSE_FRONTEND_DEPENDENCY_LEGAL_PATH_INVALID'
        Assert-RegularFile $RepositoryRoot $full 'LICENSE_FRONTEND_DEPENDENCY_LEGAL_PATH_INVALID' "$key legal file"
        $bundle = Get-AssetLegalBundle $RepositoryRoot @($file) $key
        if ($bundle.Count -ne 1 -or -not $bundle.ContainsKey($relative)) {
            Fail 'LICENSE_FRONTEND_DEPENDENCY_LEGAL_SET_MISMATCH' "$key legal-directory bundle is not exact"
        }
        if ((Get-Item -LiteralPath $full).Length -ne [int64]$file.size -or (Get-SHA256File $full) -cne [string]$file.sha256) {
            Fail 'LICENSE_FRONTEND_DEPENDENCY_LEGAL_HASH_MISMATCH' "$key legal file differs"
        }
        $legalText = Read-Utf8Strict $full 'LICENSE_FRONTEND_DEPENDENCY_LEGAL_TEXT_INVALID' -Label "$key legal file"
        if (Test-IsBlankLegalText $legalText) { Fail 'LICENSE_FRONTEND_DEPENDENCY_LEGAL_TEXT_INVALID' "$key legal file is blank" }
    }
    $sortedKeys = [string[]]@($orderedKeys)
    [Array]::Sort($sortedKeys, [StringComparer]::Ordinal)
    if (($orderedKeys -join "`n") -cne ($sortedKeys -join "`n")) { Fail 'LICENSE_FRONTEND_DEPENDENCY_ORDER_INVALID' 'frontend packages must be Ordinal sorted by identity' }

    $expectedRuntime = @(
        '@tanstack/query-core@5.101.4',
        '@tanstack/react-query@5.101.4',
        'react-dom@19.2.8',
        'react@19.2.8',
        'scheduler@0.27.0'
    )
    [Array]::Sort($expectedRuntime, [StringComparer]::Ordinal)
    [string[]]$actualRuntime = @($Manifest.packages | Where-Object { [string]$_.dependency_kind -ceq 'runtime' } | ForEach-Object { [string]$_.name + '@' + [string]$_.version })
    [Array]::Sort($actualRuntime, [StringComparer]::Ordinal)
    if (($actualRuntime -join "`n") -cne ($expectedRuntime -join "`n")) {
        Fail 'LICENSE_FRONTEND_RUNTIME_CLOSURE_INVALID' 'frontend runtime package closure is not exact'
    }

    Assert-JsonArray $Manifest.distributed_chunks $code 'frontend distributed_chunks'
    $expectedChunks = @{
        'internal/controlweb/dist/assets/react.js' = @('react-dom@19.2.8', 'react@19.2.8', 'scheduler@0.27.0')
        'internal/controlweb/dist/assets/tanstack-query.js' = @('@tanstack/query-core@5.101.4', '@tanstack/react-query@5.101.4')
    }
    $chunkPaths = New-Object System.Collections.Generic.List[string]
    foreach ($chunk in $Manifest.distributed_chunks) {
        Assert-ExactProperties $chunk @('path', 'sha256', 'packages') $code 'frontend distributed chunk'
        Assert-JsonString $chunk.path $code 'frontend chunk path'
        Assert-JsonString $chunk.sha256 $code 'frontend chunk sha256'
        Assert-Hash ([string]$chunk.sha256) $code 'frontend chunk sha256'
        Assert-JsonArray $chunk.packages $code 'frontend chunk packages'
        $path = [string]$chunk.path
        if (-not $expectedChunks.ContainsKey($path) -or $chunkPaths.Contains($path)) { Fail $code "frontend chunk path is invalid: $path" }
        $chunkPaths.Add($path)
        $expectedMembers = @($expectedChunks[$path])
        if ((@($chunk.packages) -join "`n") -cne ($expectedMembers -join "`n")) { Fail 'LICENSE_FRONTEND_CHUNK_MEMBERSHIP_INVALID' "$path package membership differs" }
        foreach ($member in @($chunk.packages)) {
            Assert-JsonString $member $code "$path package member"
            if (-not $packageMap.ContainsKey([string]$member) -or [string]$packageMap[[string]$member].dependency_kind -cne 'runtime') {
                Fail 'LICENSE_FRONTEND_CHUNK_MEMBERSHIP_INVALID' "$path contains a non-runtime package: $member"
            }
        }
        $full = Resolve-Inside $RepositoryRoot $path 'LICENSE_FRONTEND_CHUNK_PATH_INVALID'
        Assert-RegularFile $RepositoryRoot $full 'LICENSE_FRONTEND_CHUNK_PATH_INVALID' $path
        if ((Get-SHA256File $full) -cne [string]$chunk.sha256) { Fail 'LICENSE_FRONTEND_CHUNK_HASH_MISMATCH' "$path differs" }
    }
    $reportedChunkPaths = [string[]]@($chunkPaths)
    [Array]::Sort($reportedChunkPaths, [StringComparer]::Ordinal)
    [string[]]$expectedChunkPaths = @($expectedChunks.Keys)
    [Array]::Sort($expectedChunkPaths, [StringComparer]::Ordinal)
    if (($reportedChunkPaths -join "`n") -cne ($expectedChunkPaths -join "`n")) { Fail 'LICENSE_FRONTEND_CHUNK_SET_MISMATCH' 'frontend third-party chunk set is not exact' }
    return $packageMap
}

function New-ExpectedNotices([string]$DependencyManifestSHA, [string]$FrontendManifestSHA, [string]$AssetManifestSHA, $DependencyManifest, $FrontendManifest, $AssetManifest, $SelectedModuleMap, [string]$RepositoryRoot) {
    $rows = New-Object System.Collections.Generic.List[string]
    foreach ($entry in $DependencyManifest.modules) {
        foreach ($value in @([string]$entry.notice_id, [string]$entry.path, [string]$entry.version, [string]$entry.declared_license_expression, [string]$entry.source_url)) {
            if ($value.IndexOf('|', [StringComparison]::Ordinal) -ge 0 -or $value.IndexOf("`n", [StringComparison]::Ordinal) -ge 0 -or $value.IndexOf("`r", [StringComparison]::Ordinal) -ge 0) {
                Fail 'LICENSE_NOTICE_RENDER_INVALID' 'notice field contains a forbidden delimiter'
            }
        }
        $rows.Add("| $($entry.notice_id) | go-module | $($entry.path) | $($entry.version) | $($entry.declared_license_expression) | $($entry.source_url) |")
    }
    if ($null -ne $FrontendManifest) {
        foreach ($entry in $FrontendManifest.packages) {
            foreach ($value in @([string]$entry.notice_id, [string]$entry.name, [string]$entry.version, [string]$entry.declared_license_expression, [string]$entry.source_url)) {
                if ($value.IndexOf('|', [StringComparison]::Ordinal) -ge 0 -or $value.IndexOf("`n", [StringComparison]::Ordinal) -ge 0 -or $value.IndexOf("`r", [StringComparison]::Ordinal) -ge 0) {
                    Fail 'LICENSE_NOTICE_RENDER_INVALID' 'frontend notice field contains a forbidden delimiter'
                }
            }
            $kind = 'npm-' + [string]$entry.dependency_kind
            $rows.Add("| $($entry.notice_id) | $kind | $($entry.name) | $($entry.version) | $($entry.declared_license_expression) | $($entry.source_url) |")
        }
    }
    foreach ($entry in $AssetManifest.assets) {
        if ([string]$entry.origin -cne 'third-party') { continue }
        foreach ($value in @([string]$entry.notice_id, [string]$entry.path, [string]$entry.spdx_expression, [string]$entry.source_url)) {
            if ($value.IndexOf('|', [StringComparison]::Ordinal) -ge 0 -or $value.IndexOf("`n", [StringComparison]::Ordinal) -ge 0 -or $value.IndexOf("`r", [StringComparison]::Ordinal) -ge 0) {
                Fail 'LICENSE_NOTICE_RENDER_INVALID' 'notice field contains a forbidden delimiter'
            }
        }
        $rows.Add("| $($entry.notice_id) | distributed-asset | $($entry.path) | - | $($entry.spdx_expression) | $($entry.source_url) |")
    }
    $noticeIDs = @{}
    foreach ($row in $rows) {
        $id = ($row -split '\|')[1].Trim()
        if ($noticeIDs.ContainsKey($id)) { Fail 'LICENSE_NOTICE_DUPLICATE' "duplicate notice id: $id" }
        $noticeIDs[$id] = $true
    }
    $lines = New-Object System.Collections.Generic.List[string]
    $headerLines = New-Object System.Collections.Generic.List[string]
    foreach ($line in @(
        '# Third-Party Notices',
        '',
        'This file is validated by `scripts/Test-License.ps1`.',
        '',
        '<!-- freeagent-license-notices-v1 -->',
        "dependency-manifest-sha256: $DependencyManifestSHA"
    )) { $headerLines.Add($line) }
    if ($null -ne $FrontendManifest) { $headerLines.Add("frontend-dependency-manifest-sha256: $FrontendManifestSHA") }
    foreach ($line in @(
        "distributed-assets-manifest-sha256: $AssetManifestSHA",
        "notice-count: $($rows.Count)",
        '',
        '| Notice ID | Kind | Component | Version | Declared SPDX | Source |',
        '| --- | --- | --- | --- | --- | --- |'
    )) { $headerLines.Add($line) }
    foreach ($line in $headerLines) { $lines.Add($line) }
    foreach ($row in $rows) { $lines.Add($row) }
    $lines.Add('')
    $lines.Add('## Dependency License Texts')
    $lines.Add('')
    foreach ($entry in $DependencyManifest.modules) {
        $key = [string]$entry.path + '@' + [string]$entry.version
        if (-not $SelectedModuleMap.ContainsKey($key)) { Fail 'LICENSE_NOTICE_RENDER_INVALID' "selected module is unavailable for $key" }
        $moduleDirectory = [IO.Path]::GetFullPath([string]$SelectedModuleMap[$key].Dir)
        $lines.Add("### $($entry.notice_id)")
        $lines.Add('')
        $lines.Add("Component: $key")
        $lines.Add('')
        $lines.Add("Declared SPDX: $($entry.declared_license_expression)")
        $lines.Add('')
        $lines.Add("Compatibility: $($entry.compatibility_conclusion.status) ($($entry.compatibility_conclusion.policy_id)@$($entry.compatibility_conclusion.policy_version))")
        $lines.Add('')
        $detectedExpressions = if ($entry.source_spdx_scan.expressions.Count -eq 0) { '(none)' } else { @($entry.source_spdx_scan.expressions) -join ', ' }
        $lines.Add("Detected SPDX expressions: $detectedExpressions")
        $lines.Add('')
        $lines.Add("Detected SPDX occurrence count: $($entry.source_spdx_scan.occurrence_count)")
        $lines.Add('')
        $lines.Add("Detected SPDX digest: $($entry.source_spdx_scan.sha256)")
        $lines.Add('')
        foreach ($file in $entry.required_files) {
            $relative = [string]$file.path
            $fullPath = Resolve-Inside $moduleDirectory $relative 'LICENSE_NOTICE_RENDER_INVALID'
            $text = Convert-ToNoticeLegalText (Read-Utf8Strict $fullPath 'LICENSE_DEPENDENCY_TEXT_INVALID' -Label "$key required file $relative")
            $lines.Add("#### $($file.role): $relative")
            $lines.Add('')
            $lines.Add("SHA-256: $($file.sha256)")
            $lines.Add('')
            Add-NoticeLegalPayload $lines $text
            $lines.Add('')
        }
    }
    if ($null -ne $FrontendManifest) {
        $lines.Add('## NPM Dependency License Texts')
        $lines.Add('')
        $legalFiles = @{}
        foreach ($entry in $FrontendManifest.packages) {
            $key = [string]$entry.name + '@' + [string]$entry.version
            foreach ($file in $entry.required_files) {
                $relative = [string]$file.path
                if (-not $legalFiles.ContainsKey($relative)) {
                    $legalFiles[$relative] = [pscustomobject]@{
                        File = $file
                        Components = New-Object System.Collections.Generic.List[string]
                    }
                }
                $legalFiles[$relative].Components.Add($key)
            }
        }
        [string[]]$legalPaths = @($legalFiles.Keys)
        [Array]::Sort($legalPaths, [StringComparer]::Ordinal)
        foreach ($relative in $legalPaths) {
            $binding = $legalFiles[$relative]
            $components = [string[]]@($binding.Components)
            [Array]::Sort($components, [StringComparer]::Ordinal)
            $file = $binding.File
            $fullPath = Resolve-Inside $RepositoryRoot $relative 'LICENSE_NOTICE_RENDER_INVALID'
            $text = Convert-ToNoticeLegalText (Read-Utf8Strict $fullPath 'LICENSE_FRONTEND_DEPENDENCY_LEGAL_TEXT_INVALID')
            $lines.Add("### $relative")
            $lines.Add('')
            $lines.Add("Components: $($components -join ', ')")
            $lines.Add('')
            $lines.Add("SHA-256: $($file.sha256)")
            $lines.Add('')
            Add-NoticeLegalPayload $lines $text
            $lines.Add('')
        }
    }
    $lines.Add('## Distributed-Asset License Texts')
    $lines.Add('')
    foreach ($entry in $AssetManifest.assets) {
        if ([string]$entry.origin -cne 'third-party') { continue }
        $lines.Add("### $($entry.notice_id)")
        $lines.Add('')
        $lines.Add("Component: $($entry.path)")
        $lines.Add('')
        foreach ($file in $entry.required_files) {
            $relative = [string]$file.path
            $fullPath = Resolve-Inside $RepositoryRoot $relative 'LICENSE_NOTICE_RENDER_INVALID'
            $text = Convert-ToNoticeLegalText (Read-Utf8Strict $fullPath 'LICENSE_ASSET_LEGAL_TEXT_INVALID')
            $lines.Add("#### $($file.role): $relative")
            $lines.Add('')
            $lines.Add("SHA-256: $($file.sha256)")
            $lines.Add('')
            Add-NoticeLegalPayload $lines $text
            $lines.Add('')
        }
    }
    return @($lines) -join "`n"
}

function Invoke-LicenseGate([string]$RepositoryRoot, [string]$RequestedGoCommand) {
    Assert-ProjectLicense $RepositoryRoot
    Assert-R0ArtifactsExist $RepositoryRoot

    $dependencyPath = Resolve-Inside $RepositoryRoot $dependencyManifestRelative 'LICENSE_DEPENDENCY_MANIFEST_MISSING'
    $frontendDependencyPath = Resolve-Inside $RepositoryRoot $frontendDependencyManifestRelative 'LICENSE_FRONTEND_DEPENDENCY_MANIFEST_MISSING'
    $hasFrontendDependencyManifest = Test-Path -LiteralPath $frontendDependencyPath -PathType Leaf
    $controlWebPackagePath = Resolve-Inside $RepositoryRoot 'internal/controlweb/package.json' 'LICENSE_FRONTEND_DEPENDENCY_MANIFEST_MISSING'
    $hasControlWebPackage = Test-Path -LiteralPath $controlWebPackagePath -PathType Leaf
    if ($hasControlWebPackage -and -not $hasFrontendDependencyManifest) {
        Fail 'LICENSE_FRONTEND_DEPENDENCY_MANIFEST_MISSING' 'control-web package requires its pinned frontend dependency license manifest'
    }
    if (-not $hasControlWebPackage -and $hasFrontendDependencyManifest) {
        Fail 'LICENSE_FRONTEND_DEPENDENCY_MANIFEST_UNSCOPED' 'frontend dependency license manifest exists without the control-web package'
    }
    $assetPath = Resolve-Inside $RepositoryRoot $assetManifestRelative 'LICENSE_ASSET_MANIFEST_MISSING'
    $noticesPath = Resolve-Inside $RepositoryRoot $noticesRelative 'LICENSE_NOTICE_MISSING'
    Assert-RegularFile $RepositoryRoot $dependencyPath 'LICENSE_DEPENDENCY_MANIFEST_INVALID'
    if ($hasFrontendDependencyManifest) { Assert-RegularFile $RepositoryRoot $frontendDependencyPath 'LICENSE_FRONTEND_DEPENDENCY_MANIFEST_INVALID' }
    Assert-RegularFile $RepositoryRoot $assetPath 'LICENSE_ASSET_MANIFEST_INVALID'
    Assert-RegularFile $RepositoryRoot $noticesPath 'LICENSE_NOTICE_INVALID'
    $dependencyManifest = Read-StrictJson $dependencyPath 'LICENSE_DEPENDENCY_JSON_INVALID'
    $frontendDependencyManifest = if ($hasFrontendDependencyManifest) { Read-StrictJson $frontendDependencyPath 'LICENSE_FRONTEND_DEPENDENCY_JSON_INVALID' } else { $null }
    $assetManifest = Read-StrictJson $assetPath 'LICENSE_ASSET_JSON_INVALID'
    $dependencyManifestSHA = Get-SHA256File $dependencyPath
    if ($dependencyManifestSHA -cne $expectedDependencyManifestSHA256) {
        Fail 'LICENSE_DEPENDENCY_MANIFEST_PIN_MISMATCH' 'dependency license manifest SHA-256 does not match the release-gate pin'
    }
    $frontendDependencyManifestSHA = if ($hasFrontendDependencyManifest) { Get-SHA256File $frontendDependencyPath } else { '' }
    if ($hasFrontendDependencyManifest -and $frontendDependencyManifestSHA -cne $expectedFrontendDependencyManifestSHA256) {
        Fail 'LICENSE_FRONTEND_DEPENDENCY_MANIFEST_PIN_MISMATCH' 'frontend dependency license manifest SHA-256 does not match the release-gate pin'
    }

    $resolvedGo = Resolve-Go $RequestedGoCommand
    $selectedModules = Get-SelectedModules $RepositoryRoot $resolvedGo
    Invoke-GoModuleVerify $RepositoryRoot $resolvedGo 'before'
    $selectedModuleMap = Assert-DependencyManifest $RepositoryRoot $dependencyManifest $selectedModules
    Invoke-GoModuleVerify $RepositoryRoot $resolvedGo 'after'
    if ($hasFrontendDependencyManifest) { [void](Assert-FrontendDependencyManifest $RepositoryRoot $frontendDependencyManifest) }
    [void](Assert-AssetManifest $RepositoryRoot $assetManifest)

    $actualNotices = Read-Utf8Strict $noticesPath 'LICENSE_NOTICE_INVALID' -RequireCanonicalText
    $expectedNotices = New-ExpectedNotices $dependencyManifestSHA $frontendDependencyManifestSHA (Get-SHA256File $assetPath) $dependencyManifest $frontendDependencyManifest $assetManifest $selectedModuleMap $RepositoryRoot
    if ($actualNotices -cne $expectedNotices) { Fail 'LICENSE_NOTICE_MISMATCH' 'THIRD_PARTY_NOTICES.md is not the exact deterministic rendering of the manifests' }

    $frontendDependencyCount = if ($hasFrontendDependencyManifest) { $frontendDependencyManifest.packages.Count } else { 0 }
    Write-Output "PASS Test-License: $($dependencyManifest.modules.Count) Go dependencies; $frontendDependencyCount npm dependencies; $($assetManifest.assets.Count) distributed assets"
}

$repositoryRoot = $null
try {
    if ([string]::IsNullOrWhiteSpace($Root) -or -not [IO.Path]::IsPathRooted($Root)) {
        Fail 'LICENSE_ROOT_INVALID' '-Root is mandatory and must be an absolute path'
    }
    try {
        $repositoryRoot = Get-CanonicalAbsolutePath $Root
    } catch {
        Fail 'LICENSE_ROOT_INVALID' '-Root is not a valid absolute path'
    }
    if (-not (Test-Path -LiteralPath $repositoryRoot -PathType Container)) { Fail 'LICENSE_ROOT_INVALID' "repository root does not exist: $repositoryRoot" }
    Assert-NoReparseAncestry $repositoryRoot 'LICENSE_ROOT_REPARSE_FORBIDDEN'
    Assert-NoReparsePath $repositoryRoot $repositoryRoot 'LICENSE_ROOT_REPARSE_FORBIDDEN'
    Invoke-LicenseGate $repositoryRoot $GoCommand
    exit 0
} catch {
    $publicMessage = [string]$_.Exception.Message
    $redactions = @(
        @{ Value = $repositoryRoot; Replacement = '<root>' },
        @{ Value = $Root; Replacement = '<root>' },
        @{ Value = $GoCommand; Replacement = '<go-command>' }
        @{ Value = (Get-CanonicalAbsolutePath ([IO.Path]::GetTempPath())); Replacement = '<temp>' },
        @{ Value = [Environment]::GetFolderPath([Environment+SpecialFolder]::UserProfile); Replacement = '<home>' }
    )
    $regexOptions = if ($script:isWindowsPlatform) { [Text.RegularExpressions.RegexOptions]::IgnoreCase } else { [Text.RegularExpressions.RegexOptions]::None }
    foreach ($redaction in $redactions) {
        $value = [string]$redaction.Value
        $isFilesystemRoot = $false
        if ([string]$redaction.Replacement -ceq '<root>' -and -not [string]::IsNullOrWhiteSpace($value) -and [IO.Path]::IsPathRooted($value)) {
            try {
                $fullValue = [IO.Path]::GetFullPath($value)
                $isFilesystemRoot = $fullValue.Equals([IO.Path]::GetPathRoot($fullValue), $pathComparison)
            } catch { $isFilesystemRoot = $false }
        }
        if (-not $isFilesystemRoot -and -not [string]::IsNullOrWhiteSpace($value) -and ($value.Length -ge 3 -or [string]$redaction.Replacement -ceq '<root>')) {
            $publicMessage = [regex]::Replace($publicMessage, [regex]::Escape($value), [string]$redaction.Replacement, $regexOptions)
        }
    }
    $pathFragmentTail = '[^\s"''<>|]*'
    $publicMessage = [regex]::Replace($publicMessage, '(?im)(?<![A-Za-z0-9._@+/-])file:[\\/]+' + $pathFragmentTail, '<absolute-path>')
    $publicMessage = [regex]::Replace($publicMessage, '(?m)\\\\' + $pathFragmentTail, '<absolute-path>')
    $publicMessage = [regex]::Replace($publicMessage, '(?im)(?<![A-Za-z0-9._@+/-])[A-Z]:[\\/]' + $pathFragmentTail, '<absolute-path>')
    $publicMessage = [regex]::Replace($publicMessage, '(?m)(?<!:)//' + $pathFragmentTail, '<absolute-path>')
    $publicMessage = [regex]::Replace($publicMessage, '(?m)(?<![A-Za-z0-9._@+:/-])/(?!/)' + $pathFragmentTail, '<absolute-path>')
    [Console]::Error.WriteLine($publicMessage)
    exit 1
}
