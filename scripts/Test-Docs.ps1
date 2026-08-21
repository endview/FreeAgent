[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$Root,

    [string]$GofmtPath = ''
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if (-not [System.IO.Path]::IsPathRooted($Root)) {
    [Console]::Error.WriteLine('DOC_ROOT_NOT_ABSOLUTE path=.')
    exit 1
}

try {
    $lexicalRoot = [System.IO.Path]::GetFullPath($Root)
    $lexicalPathRoot = [System.IO.Path]::GetPathRoot($lexicalRoot)
    $lexicalCursor = $lexicalPathRoot
    $lexicalRemainder = $lexicalRoot.Substring($lexicalPathRoot.Length)
    foreach ($segment in $lexicalRemainder.Split([char[]]@([char]92, [char]47), [System.StringSplitOptions]::RemoveEmptyEntries)) {
        $lexicalCursor = [System.IO.Path]::Combine($lexicalCursor, $segment)
        $lexicalItem = Get-Item -LiteralPath $lexicalCursor -Force
        if (($lexicalItem.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            [Console]::Error.WriteLine('DOC_ROOT_REPARSE_POINT path=.')
            exit 1
        }
    }
} catch {
    [Console]::Error.WriteLine('DOC_ROOT_NOT_FOUND path=.')
    exit 1
}

if (-not (Test-Path -LiteralPath $Root -PathType Container)) {
    [Console]::Error.WriteLine('DOC_ROOT_NOT_FOUND path=.')
    exit 1
}

$script:IsWindowsPlatform = [System.Environment]::OSVersion.Platform -eq [System.PlatformID]::Win32NT
$script:PathComparison = if ($script:IsWindowsPlatform) {
    [System.StringComparison]::OrdinalIgnoreCase
} else {
    [System.StringComparison]::Ordinal
}
$script:PathComparer = if ($script:IsWindowsPlatform) {
    [System.StringComparer]::OrdinalIgnoreCase
} else {
    [System.StringComparer]::Ordinal
}
$script:Ordinal = [System.StringComparer]::Ordinal
$script:Utf8Strict = New-Object System.Text.UTF8Encoding($false, $true)
$script:Utf8NoBom = New-Object System.Text.UTF8Encoding($false)
$script:Violations = New-Object 'System.Collections.Generic.List[string]'
$script:RequestedGofmtPath = $GofmtPath
$script:ResolvedGofmtPath = $null
$script:GofmtResolutionAttempted = $false
$script:GofmtResolutionRule = $null
$script:AllowedExternalHosts = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::OrdinalIgnoreCase)
foreach ($hostName in @(
    'docs.crewai.com',
    'docs.dify.ai',
    'docs.langchain.com',
    'docs.letta.com',
    'docs.openhands.dev',
    'github.com',
    'gitlab.com',
    'go.googlesource.com',
    'openai.github.io',
    'www.postgresql.org'
)) {
    [void]$script:AllowedExternalHosts.Add($hostName)
}
$script:RawHtmlBlockType1Start = [System.Text.RegularExpressions.Regex]::new(
    '^ {0,3}<(?<tag>script|pre|style|textarea)(?:[ \t]|>|$)',
    [System.Text.RegularExpressions.RegexOptions]::IgnoreCase
)
$script:RawHtmlBlockType6Start = [System.Text.RegularExpressions.Regex]::new(
    '^ {0,3}</?(?:address|article|aside|base|basefont|blockquote|body|caption|center|col|colgroup|dd|details|dialog|dir|div|dl|dt|fieldset|figcaption|figure|footer|form|frame|frameset|h[1-6]|head|header|hr|html|iframe|legend|li|link|main|menu|menuitem|nav|noframes|ol|optgroup|option|p|param|search|section|summary|table|tbody|td|tfoot|th|thead|title|tr|track|ul)(?:[ \t]|/?>|$)',
    [System.Text.RegularExpressions.RegexOptions]::IgnoreCase
)
$script:CommonMarkEmailAutolink = [System.Text.RegularExpressions.Regex]::new(
    '^<[A-Za-z0-9.!#$%&''*+/=?^_`{|}~-]+@[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*>$',
    [System.Text.RegularExpressions.RegexOptions]::CultureInvariant -bor
        [System.Text.RegularExpressions.RegexOptions]::Compiled
)

try {
    $resolvedRoot = Resolve-Path -LiteralPath $Root
    if ($resolvedRoot.Provider.Name -ne 'FileSystem') {
        [Console]::Error.WriteLine('DOC_ROOT_NOT_FILESYSTEM path=.')
        exit 1
    }
    $script:RootFull = [System.IO.Path]::GetFullPath($resolvedRoot.ProviderPath)
    $rootPathPart = [System.IO.Path]::GetPathRoot($script:RootFull)
    if ($script:RootFull.Length -gt $rootPathPart.Length) {
        $script:RootFull = $script:RootFull.TrimEnd([char[]]@([char]92, [char]47))
    }
    $script:RootPrefix = $script:RootFull
    if (-not ($script:RootPrefix.EndsWith([System.IO.Path]::DirectorySeparatorChar.ToString()) -or
              $script:RootPrefix.EndsWith([System.IO.Path]::AltDirectorySeparatorChar.ToString()))) {
        $script:RootPrefix += [System.IO.Path]::DirectorySeparatorChar
    }
} catch {
    [Console]::Error.WriteLine('DOC_ROOT_RESOLUTION_FAILED path=.')
    exit 1
}

function ConvertTo-SafeDiagnosticPath {
    param([AllowEmptyString()][string]$Value)

    if ([string]::IsNullOrEmpty($Value)) {
        return $Value
    }
    $builder = New-Object System.Text.StringBuilder
    foreach ($character in $Value.ToCharArray()) {
        $category = [System.Char]::GetUnicodeCategory($character)
        if ($category -in @(
            [System.Globalization.UnicodeCategory]::Control,
            [System.Globalization.UnicodeCategory]::Format,
            [System.Globalization.UnicodeCategory]::LineSeparator,
            [System.Globalization.UnicodeCategory]::ParagraphSeparator,
            [System.Globalization.UnicodeCategory]::Surrogate
        )) {
            [void]$builder.Append(('\u{0:X4}' -f [int]$character))
        } else {
            [void]$builder.Append($character)
        }
    }
    return $builder.ToString()
}

function Test-ContainsUnsafeUnicode {
    param([AllowEmptyString()][string]$Value)

    if ([string]::IsNullOrEmpty($Value)) {
        return $false
    }
    foreach ($character in $Value.ToCharArray()) {
        if ([System.Char]::GetUnicodeCategory($character) -in @(
            [System.Globalization.UnicodeCategory]::Control,
            [System.Globalization.UnicodeCategory]::Format,
            [System.Globalization.UnicodeCategory]::LineSeparator,
            [System.Globalization.UnicodeCategory]::ParagraphSeparator,
            [System.Globalization.UnicodeCategory]::Surrogate
        )) {
            return $true
        }
    }
    return $false
}

function Add-DocViolation {
    param(
        [Parameter(Mandatory = $true)][string]$Rule,
        [string]$Path = '.',
        [int]$Line = 0,
        [string]$Detail = ''
    )

    $safePath = if ([string]::IsNullOrWhiteSpace($Path)) { '.' } else { ConvertTo-SafeDiagnosticPath -Value $Path }
    $message = "$Rule path=$safePath"
    if ($Line -gt 0) {
        $message += " line=$Line"
    }
    if (-not [string]::IsNullOrWhiteSpace($Detail)) {
        $message += " detail=$Detail"
    }
    $script:Violations.Add($message)
}

function Test-InRepository {
    param([Parameter(Mandatory = $true)][string]$FullPath)

    $candidate = [System.IO.Path]::GetFullPath($FullPath)
    if ([string]::Equals($candidate, $script:RootFull, $script:PathComparison)) {
        return $true
    }
    return $candidate.StartsWith($script:RootPrefix, $script:PathComparison)
}

function Get-RepositoryRelativePath {
    param([Parameter(Mandatory = $true)][string]$FullPath)

    $candidate = [System.IO.Path]::GetFullPath($FullPath)
    if ([string]::Equals($candidate, $script:RootFull, $script:PathComparison)) {
        return '.'
    }
    if (-not $candidate.StartsWith($script:RootPrefix, $script:PathComparison)) {
        return '[outside]'
    }
    return $candidate.Substring($script:RootPrefix.Length).Replace([char]92, [char]47)
}

function Test-IsRootGitPath {
    param([Parameter(Mandatory = $true)][string]$RelativePath)

    return $RelativePath -eq '.git' -or $RelativePath.StartsWith('.git/', [System.StringComparison]::Ordinal)
}

function Test-PathHasReparsePoint {
    param([Parameter(Mandatory = $true)][string]$FullPath)

    if (-not (Test-InRepository -FullPath $FullPath)) {
        return $true
    }

    $cursor = [System.IO.Path]::GetFullPath($FullPath)
    while ($true) {
        if (Test-Path -LiteralPath $cursor) {
            $item = Get-Item -LiteralPath $cursor -Force
            if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
                return $true
            }
        }
        if ([string]::Equals($cursor, $script:RootFull, $script:PathComparison)) {
            break
        }
        $parent = [System.IO.Path]::GetDirectoryName($cursor)
        if ([string]::IsNullOrEmpty($parent) -or [string]::Equals($parent, $cursor, $script:PathComparison)) {
            break
        }
        if (-not (Test-InRepository -FullPath $parent)) {
            break
        }
        $cursor = $parent
    }
    return $false
}

function Get-SafeRepositoryFiles {
    $files = New-Object 'System.Collections.Generic.List[object]'
    $directories = New-Object 'System.Collections.Generic.Queue[string]'
    $directories.Enqueue($script:RootFull)

    while ($directories.Count -gt 0) {
        $directory = $directories.Dequeue()
        try {
            $children = @(Get-ChildItem -LiteralPath $directory -Force | Sort-Object Name)
        } catch {
            Add-DocViolation -Rule 'DOC_ENUMERATION_FAILED' -Path (Get-RepositoryRelativePath -FullPath $directory)
            continue
        }
        foreach ($child in $children) {
            if ([string]::Equals($directory, $script:RootFull, $script:PathComparison) -and $child.Name -ceq '.git') {
                continue
            }
            $relative = Get-RepositoryRelativePath -FullPath $child.FullName
            if (($child.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
                Add-DocViolation -Rule 'DOC_REPARSE_POINT' -Path $relative
                continue
            }
            if ($child.PSIsContainer) {
                $directories.Enqueue($child.FullName)
            } else {
                $files.Add($child)
            }
        }
    }
    return $files.ToArray()
}

function Test-ExactRepositoryCase {
    param([Parameter(Mandatory = $true)][string]$FullPath)

    if (-not (Test-InRepository -FullPath $FullPath)) {
        return $false
    }
    $relative = Get-RepositoryRelativePath -FullPath $FullPath
    if ($relative -eq '.') {
        return $true
    }

    $cursor = $script:RootFull
    foreach ($part in $relative.Split([char]47)) {
        if ([string]::IsNullOrEmpty($part)) {
            return $false
        }
        $exact = Get-ChildItem -LiteralPath $cursor -Force | Where-Object { $_.Name -ceq $part } | Select-Object -First 1
        if ($null -eq $exact) {
            return $false
        }
        $cursor = $exact.FullName
    }
    return $true
}

function Test-SafeFixedPath {
    param(
        [Parameter(Mandatory = $true)][string]$FullPath,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )

    if (-not (Test-InRepository -FullPath $FullPath)) {
        Add-DocViolation -Rule 'DOC_FIXED_PATH_ROOT_ESCAPE' -Path $RelativePath
        return $false
    }
    $cursor = $script:RootFull
    foreach ($segment in $RelativePath.Split([char]47)) {
        if ([string]::IsNullOrEmpty($segment) -or $segment -eq '.' -or $segment -eq '..') {
            Add-DocViolation -Rule 'DOC_FIXED_PATH_INVALID' -Path $RelativePath
            return $false
        }
        $cursor = [System.IO.Path]::Combine($cursor, $segment)
        try {
            $item = Get-Item -LiteralPath $cursor -Force -ErrorAction Stop
        } catch {
            return $true
        }
        if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            Add-DocViolation -Rule 'DOC_FIXED_PATH_REPARSE' -Path $RelativePath
            return $false
        }
    }
    return $true
}

function Get-RepositoryRelativePathState {
    param(
        [Parameter(Mandatory = $true)][string]$RelativePath,
        [Parameter(Mandatory = $true)][ref]$ActualFullPath
    )

    $ActualFullPath.Value = $null
    if (-not (Test-ManifestRelativePath -Path $RelativePath)) { return 'invalid' }
    $cursor = $script:RootFull
    $caseMismatch = $false
    foreach ($segment in $RelativePath.Split([char]47)) {
        try { $children = @(Get-ChildItem -LiteralPath $cursor -Force -ErrorAction Stop) } catch { return 'missing' }
        $exact = @($children | Where-Object { $_.Name -ceq $segment } | Select-Object -First 1)
        $item = if ($exact.Count -gt 0) { $exact[0] } else { $null }
        if ($null -eq $item) {
            $variant = @($children | Where-Object { [string]::Equals($_.Name, $segment, [System.StringComparison]::OrdinalIgnoreCase) } | Select-Object -First 1)
            if ($variant.Count -eq 0) { return 'missing' }
            $item = $variant[0]
            $caseMismatch = $true
        }
        if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) { return 'reparse' }
        $cursor = $item.FullName
    }
    $ActualFullPath.Value = $cursor
    if ($caseMismatch) { return 'case-mismatch' }
    return 'exact'
}

function Resolve-SafeLocalLinkTarget {
    param(
        [Parameter(Mandatory = $true)][string]$TargetRelative,
        [Parameter(Mandatory = $true)][string]$SourceRelative,
        [Parameter(Mandatory = $true)][int]$Line,
        [Parameter(Mandatory = $true)][ref]$TargetItem
    )

    $TargetItem.Value = $null
    if ($TargetRelative -ceq '.') {
        $TargetItem.Value = Get-Item -LiteralPath $script:RootFull -Force
        return $true
    }
    $cursor = $script:RootFull
    $segments = @($TargetRelative.Split([char[]]@([char]47), [System.StringSplitOptions]::RemoveEmptyEntries))
    foreach ($segment in $segments) {
        $children = @()
        try {
            # The cursor is already known to be a real, non-reparse directory.
            # Enumerating only this parent obtains link metadata without ever
            # resolving or traversing the requested child first.
            $children = @(Get-ChildItem -LiteralPath $cursor -Force -ErrorAction Stop)
        } catch {
            Add-DocViolation -Rule 'DOC_LINK_BROKEN' -Path $SourceRelative -Line $Line
            return $false
        }
        $item = @($children | Where-Object { $_.Name -ceq $segment } | Select-Object -First 1)
        $exactItem = if ($item.Count -eq 0) { $null } else { $item[0] }
        $caseItem = $exactItem
        if ($null -eq $caseItem) {
            $variants = @($children | Where-Object { [string]::Equals($_.Name, $segment, [System.StringComparison]::OrdinalIgnoreCase) } | Select-Object -First 1)
            if ($variants.Count -gt 0) { $caseItem = $variants[0] }
        }
        if ($null -eq $caseItem) {
            Add-DocViolation -Rule 'DOC_LINK_BROKEN' -Path $SourceRelative -Line $Line
            return $false
        }
        $item = $caseItem
        if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            Add-DocViolation -Rule 'DOC_LINK_REPARSE_POINT' -Path $SourceRelative -Line $Line
            return $false
        }
        if ($null -eq $exactItem) {
            Add-DocViolation -Rule 'DOC_LINK_CASE_MISMATCH' -Path $SourceRelative -Line $Line
            return $false
        }
        $cursor = $item.FullName
        $TargetItem.Value = $item
    }
    return $true
}

function Test-DirectoryContainsPublicFile {
    param([Parameter(Mandatory = $true)][string]$DirectoryFull)

    # Match the repository traversal used by this gate: root .git metadata and
    # reparse points do not enter the public tree.  A directory link is useful
    # only when at least one regular file remains after those exclusions.
    $directories = New-Object 'System.Collections.Generic.Queue[string]'
    $directories.Enqueue($DirectoryFull)
    while ($directories.Count -gt 0) {
        $directory = $directories.Dequeue()
        try {
            $children = @(Get-ChildItem -LiteralPath $directory -Force -ErrorAction Stop)
        } catch {
            return $false
        }
        foreach ($child in $children) {
            if ([string]::Equals($directory, $script:RootFull, $script:PathComparison) -and $child.Name -ceq '.git') {
                continue
            }
            if (($child.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
                continue
            }
            if ($child.PSIsContainer) {
                $directories.Enqueue($child.FullName)
            } else {
                return $true
            }
        }
    }
    return $false
}

function Split-DocumentLines {
    param([AllowEmptyString()][string]$Text)
    return [System.Text.RegularExpressions.Regex]::Split($Text, "`r`n|`n|`r")
}

function Read-StrictUtf8 {
    param(
        [Parameter(Mandatory = $true)][string]$FullPath,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )

    try {
        $bytes = [System.IO.File]::ReadAllBytes($FullPath)
    } catch {
        Add-DocViolation -Rule 'DOC_READ_FAILED' -Path $RelativePath
        return $null
    }

    $hasBom = $bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF
    if ($hasBom) {
        Add-DocViolation -Rule 'DOC_UTF8_BOM' -Path $RelativePath
    }

    try {
        $text = $script:Utf8Strict.GetString($bytes)
    } catch {
        Add-DocViolation -Rule 'DOC_UTF8_INVALID' -Path $RelativePath
        return $null
    }
    if ($text.Length -gt 0 -and $text[0] -eq [char]0xFEFF) {
        $text = $text.Substring(1)
    }

    return [pscustomobject]@{
        Bytes = $bytes
        Text = $text
        Lines = @(Split-DocumentLines -Text $text)
    }
}

function Get-Sha256Hex {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)

    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        $hash = $sha.ComputeHash($Bytes)
    } finally {
        $sha.Dispose()
    }
    return ([System.BitConverter]::ToString($hash)).Replace('-', '').ToLowerInvariant()
}

function Get-LineSha256 {
    param([AllowEmptyString()][string]$Line)
    return Get-Sha256Hex -Bytes $script:Utf8NoBom.GetBytes($Line)
}

function Test-StrictPercentEncoding {
    param([AllowEmptyString()][string]$Value)
    return -not [System.Text.RegularExpressions.Regex]::IsMatch($Value, '%(?![0-9A-Fa-f]{2})')
}

function Test-ContainsEntityReferenceSyntax {
    param([AllowEmptyString()][string]$Value)

    # CommonMark expands HTML5 entity and numeric references in link
    # destinations. This release gate intentionally rejects that syntax
    # instead of carrying a second, platform-dependent HTML5 entity table.
    return [System.Text.RegularExpressions.Regex]::IsMatch(
        $Value,
        '&(?:[A-Za-z][A-Za-z0-9]{1,31}|#[0-9]{1,7}|#[xX][0-9A-Fa-f]{1,6});'
    )
}

function Test-StrictExternalUri {
    param(
        [AllowEmptyString()][string]$Value,
        [Parameter(Mandatory = $true)][string[]]$AllowedSchemes,
        [Parameter(Mandatory = $true)][ref]$Canonical
    )

    # Transport syntax is checked here. R0-3 host allowlisting is applied
    # separately to every visible HTTP(S) URL so Markdown, HTML, angle
    # autolinks, and bare URLs share the same exact-host policy.
    $Canonical.Value = $null
    if ([string]::IsNullOrWhiteSpace($Value) -or $Value -cne $Value.Trim() -or $Value.Contains([char]92) -or
        (Test-ContainsUnsafeUnicode -Value $Value) -or -not (Test-StrictPercentEncoding -Value $Value) -or $Value -match '\s') {
        return $false
    }
    $uri = $null
    if (-not [System.Uri]::TryCreate($Value, [System.UriKind]::Absolute, [ref]$uri) -or -not $uri.IsAbsoluteUri) {
        return $false
    }
    $scheme = $uri.Scheme.ToLowerInvariant()
    if ($AllowedSchemes -cnotcontains $scheme -or -not [string]::IsNullOrEmpty($uri.UserInfo)) {
        return $false
    }
    if ($scheme -in @('http', 'https')) {
        if ($Value -notmatch ('(?i)^' + [System.Text.RegularExpressions.Regex]::Escape($scheme) + '://') -or
            [string]::IsNullOrWhiteSpace($uri.Host)) {
            return $false
        }
    } elseif ($scheme -ceq 'mailto' -and [string]::IsNullOrWhiteSpace($uri.OriginalString.Substring(7))) {
        return $false
    }
    try {
        $decodedComponents = [System.Uri]::UnescapeDataString($uri.PathAndQuery + $uri.Fragment)
    } catch {
        return $false
    }
    if (Test-ContainsUnsafeUnicode -Value $decodedComponents) {
        return $false
    }
    $Canonical.Value = $uri.AbsoluteUri
    return $true
}

function Test-ConservativeRawHtmlType7Start {
    param([AllowEmptyString()][string]$Line)

    # Deliberately over-classify a tag-shaped full line as raw HTML. The later
    # HTML grammar gate rejects malformed tags; keeping this block state here
    # prevents a pseudo fence after malformed raw markup from hiding content.
    $start = 0
    while ($start -lt $Line.Length -and $Line[$start] -eq [char]32) { $start++ }
    if ($start -gt 3 -or $start -ge $Line.Length -or
        -not [System.Text.RegularExpressions.Regex]::IsMatch($Line.Substring($start), '^</?[A-Za-z][A-Za-z0-9-]*')) {
        return $false
    }
    $end = $Line.Length - 1
    while ($end -ge $start -and $Line[$end] -in @([char]32, [char]9)) { $end-- }
    if ($end -le $start -or $Line[$end] -ne [char]62) {
        return $false
    }

    $quote = [char]0
    $tagEnd = -1
    for ($index = $start + 1; $index -le $end; $index++) {
        $character = $Line[$index]
        if ($quote -ne [char]0) {
            if ($character -eq $quote) { $quote = [char]0 }
            continue
        }
        if ($character -eq [char]34 -or $character -eq [char]39) {
            $quote = $character
        } elseif ($character -eq [char]62) {
            $tagEnd = $index
            break
        }
    }
    return $tagEnd -eq $end
}

function Get-CommonMarkInlineWhitespaceEnd {
    param(
        [Parameter(Mandatory = $true)][string]$Text,
        [Parameter(Mandatory = $true)][int]$Start
    )

    $cursor = $Start
    while ($cursor -lt $Text.Length -and $Text[$cursor] -in @([char]9, [char]32)) { $cursor++ }
    if ($cursor -lt $Text.Length -and $Text[$cursor] -in @([char]10, [char]13)) {
        if ($Text[$cursor] -eq [char]13 -and $cursor + 1 -lt $Text.Length -and
            $Text[$cursor + 1] -eq [char]10) {
            $cursor += 2
        } else {
            $cursor++
        }
        while ($cursor -lt $Text.Length -and $Text[$cursor] -in @([char]9, [char]32)) { $cursor++ }
    }
    return $cursor
}

function Get-CommonMarkInlineRawHtmlTokenLength {
    param(
        [Parameter(Mandatory = $true)][string]$Text,
        [Parameter(Mandatory = $true)][int]$Start
    )

    if ($Start -lt 0 -or $Start + 1 -ge $Text.Length -or $Text[$Start] -ne [char]60) {
        return 0
    }

    if ($Start + 5 -le $Text.Length -and
        $Text.Substring($Start, 5).Equals('<!-->', [System.StringComparison]::Ordinal)) {
        return 5
    }
    if ($Start + 6 -le $Text.Length -and
        $Text.Substring($Start, 6).Equals('<!--->', [System.StringComparison]::Ordinal)) {
        return 6
    }
    if ($Start + 4 -le $Text.Length -and
        $Text.Substring($Start, 4).Equals('<!--', [System.StringComparison]::Ordinal)) {
        $closing = $Text.IndexOf('-->', $Start + 4, [System.StringComparison]::Ordinal)
        if ($closing -lt 0) { return 0 }
        return $closing + 3 - $Start
    }
    if ($Start + 9 -le $Text.Length -and
        $Text.Substring($Start, 9).Equals('<![CDATA[', [System.StringComparison]::Ordinal)) {
        $closing = $Text.IndexOf(']]>', $Start + 9, [System.StringComparison]::Ordinal)
        if ($closing -lt 0) { return 0 }
        return $closing + 3 - $Start
    }
    if ($Text[$Start + 1] -eq [char]63) {
        $closing = $Text.IndexOf('?>', $Start + 2, [System.StringComparison]::Ordinal)
        if ($closing -lt 0) { return 0 }
        return $closing + 2 - $Start
    }
    if ($Text[$Start + 1] -eq [char]33) {
        $cursor = $Start + 2
        if ($cursor -ge $Text.Length -or -not (
            ($Text[$cursor] -ge [char]65 -and $Text[$cursor] -le [char]90) -or
            ($Text[$cursor] -ge [char]97 -and $Text[$cursor] -le [char]122)
        )) {
            return 0
        }
        $closing = $Text.IndexOf([char]62, $cursor + 1)
        if ($closing -lt 0) { return 0 }
        return $closing + 1 - $Start
    }

    if ($Text[$Start + 1] -eq [char]47) {
        $cursor = $Start + 2
        if ($cursor -ge $Text.Length -or -not (
            ($Text[$cursor] -ge [char]65 -and $Text[$cursor] -le [char]90) -or
            ($Text[$cursor] -ge [char]97 -and $Text[$cursor] -le [char]122)
        )) { return 0 }
        $cursor++
        while ($cursor -lt $Text.Length -and (
            ($Text[$cursor] -ge [char]65 -and $Text[$cursor] -le [char]90) -or
            ($Text[$cursor] -ge [char]97 -and $Text[$cursor] -le [char]122) -or
            ($Text[$cursor] -ge [char]48 -and $Text[$cursor] -le [char]57) -or
            $Text[$cursor] -eq [char]45
        )) { $cursor++ }
        $cursor = Get-CommonMarkInlineWhitespaceEnd -Text $Text -Start $cursor
        if ($cursor -lt $Text.Length -and $Text[$cursor] -eq [char]62) {
            return $cursor + 1 - $Start
        }
        return 0
    }

    $cursor = $Start + 1
    $first = $Text[$cursor]
    $firstIsLetter = ($first -ge [char]65 -and $first -le [char]90) -or
        ($first -ge [char]97 -and $first -le [char]122)
    if ($firstIsLetter) {
        $schemeCursor = $cursor + 1
        while ($schemeCursor -lt $Text.Length -and $schemeCursor - $cursor -lt 32 -and (
            ($Text[$schemeCursor] -ge [char]65 -and $Text[$schemeCursor] -le [char]90) -or
            ($Text[$schemeCursor] -ge [char]97 -and $Text[$schemeCursor] -le [char]122) -or
            ($Text[$schemeCursor] -ge [char]48 -and $Text[$schemeCursor] -le [char]57) -or
            $Text[$schemeCursor] -in @([char]43, [char]45, [char]46)
        )) { $schemeCursor++ }
        if ($schemeCursor - $cursor -ge 2 -and $schemeCursor -lt $Text.Length -and
            $Text[$schemeCursor] -eq [char]58) {
            $autolinkCursor = $schemeCursor + 1
            while ($autolinkCursor -lt $Text.Length) {
                $characterCode = [int]$Text[$autolinkCursor]
                if ($Text[$autolinkCursor] -eq [char]62) {
                    return $autolinkCursor + 1 - $Start
                }
                if ($characterCode -le 32 -or $Text[$autolinkCursor] -eq [char]60) { break }
                $autolinkCursor++
            }
        }
    }

    $emailEnd = $Text.IndexOf([char]62, $Start + 1)
    if ($emailEnd -gt $Start + 1 -and
        $Text.IndexOf([char]64, $Start + 1, $emailEnd - $Start - 1) -ge 0) {
        $emailCandidate = $Text.Substring($Start, $emailEnd - $Start + 1)
        if ($script:CommonMarkEmailAutolink.IsMatch($emailCandidate)) {
            return $emailCandidate.Length
        }
    }

    if (-not $firstIsLetter) { return 0 }
    $cursor++
    while ($cursor -lt $Text.Length -and (
        ($Text[$cursor] -ge [char]65 -and $Text[$cursor] -le [char]90) -or
        ($Text[$cursor] -ge [char]97 -and $Text[$cursor] -le [char]122) -or
        ($Text[$cursor] -ge [char]48 -and $Text[$cursor] -le [char]57) -or
        $Text[$cursor] -eq [char]45
    )) { $cursor++ }

    while ($cursor -lt $Text.Length) {
        if ($Text[$cursor] -eq [char]62) { return $cursor + 1 - $Start }
        if ($Text[$cursor] -eq [char]47 -and $cursor + 1 -lt $Text.Length -and
            $Text[$cursor + 1] -eq [char]62) { return $cursor + 2 - $Start }
        $separatorStart = $cursor
        $cursor = Get-CommonMarkInlineWhitespaceEnd -Text $Text -Start $cursor
        if ($cursor -eq $separatorStart) { return 0 }
        if ($cursor -ge $Text.Length) { return 0 }
        if ($Text[$cursor] -eq [char]62) { return $cursor + 1 - $Start }
        if ($Text[$cursor] -eq [char]47 -and $cursor + 1 -lt $Text.Length -and
            $Text[$cursor + 1] -eq [char]62) { return $cursor + 2 - $Start }

        $attributeFirst = $Text[$cursor]
        if (-not (
            ($attributeFirst -ge [char]65 -and $attributeFirst -le [char]90) -or
            ($attributeFirst -ge [char]97 -and $attributeFirst -le [char]122) -or
            $attributeFirst -in @([char]58, [char]95)
        )) { return 0 }
        $cursor++
        while ($cursor -lt $Text.Length -and (
            ($Text[$cursor] -ge [char]65 -and $Text[$cursor] -le [char]90) -or
            ($Text[$cursor] -ge [char]97 -and $Text[$cursor] -le [char]122) -or
            ($Text[$cursor] -ge [char]48 -and $Text[$cursor] -le [char]57) -or
            $Text[$cursor] -in @([char]45, [char]46, [char]58, [char]95)
        )) { $cursor++ }

        $afterName = $cursor
        $cursor = Get-CommonMarkInlineWhitespaceEnd -Text $Text -Start $cursor
        if ($cursor -ge $Text.Length -or $Text[$cursor] -ne [char]61) {
            $cursor = $afterName
            continue
        }
        $cursor++
        $cursor = Get-CommonMarkInlineWhitespaceEnd -Text $Text -Start $cursor
        if ($cursor -ge $Text.Length) { return 0 }
        if ($Text[$cursor] -in @([char]34, [char]39)) {
            $quote = $Text[$cursor]
            $closingQuote = $Text.IndexOf($quote, $cursor + 1)
            if ($closingQuote -lt 0) { return 0 }
            $cursor = $closingQuote + 1
            continue
        }
        $valueStart = $cursor
        while ($cursor -lt $Text.Length -and [int]$Text[$cursor] -notin @(9, 10, 13, 32) -and
            $Text[$cursor] -notin @([char]34, [char]39, [char]60, [char]61, [char]62, [char]96)) {
            $cursor++
        }
        if ($cursor -eq $valueStart) { return 0 }
    }
    return 0
}

function Remove-InlineCode {
    param(
        [AllowEmptyString()][string]$Line,
        [AllowNull()][bool[]]$ProtectedMask = $null
    )

    if ($null -ne $ProtectedMask -and $ProtectedMask.Length -ne $Line.Length) {
        throw 'DOC_INLINE_MASK_LENGTH_MISMATCH'
    }
    $characters = $Line.ToCharArray()
    $index = 0
    while ($index -lt $Line.Length) {
        if ($null -ne $ProtectedMask -and $ProtectedMask[$index]) {
            $index++
            continue
        }
        if ($Line[$index] -eq [char]60) {
            $rawHtmlLexemeLength = Get-CommonMarkInlineRawHtmlTokenLength -Text $Line -Start $index
            if ($rawHtmlLexemeLength -gt 0) {
                $overlapsProtectedBoundary = $false
                if ($null -ne $ProtectedMask) {
                    for ($cursor = $index; $cursor -lt $index + $rawHtmlLexemeLength; $cursor++) {
                        if ($ProtectedMask[$cursor]) {
                            $overlapsProtectedBoundary = $true
                            break
                        }
                    }
                }
                if (-not $overlapsProtectedBoundary) {
                    $index += $rawHtmlLexemeLength
                    continue
                }
            }
        }
        if ($Line[$index] -ne [char]96) {
            $index++
            continue
        }

        $runStart = $index
        while ($index -lt $Line.Length -and $Line[$index] -eq [char]96) { $index++ }
        $runLength = $index - $runStart
        $backslashCount = 0
        $before = $runStart - 1
        while ($before -ge 0 -and $Line[$before] -eq [char]92) {
            $backslashCount++
            $before--
        }
        if (($backslashCount % 2) -eq 1) {
            continue
        }

        $search = $index
        $closingEnd = -1
        while ($search -lt $Line.Length) {
            if ($null -ne $ProtectedMask -and $ProtectedMask[$search]) {
                break
            }
            # This release gate does not carry a full CommonMark block AST.
            # Restrict code-span suppression to one physical line so an opener
            # cannot cross a paragraph, list item, quote, or other block edge
            # and hide content that the renderer exposes.
            if ($Line[$search] -in @([char]10, [char]13)) {
                break
            }
            if ($Line[$search] -ne [char]96) {
                $search++
                continue
            }
            $closingStart = $search
            while ($search -lt $Line.Length -and $Line[$search] -eq [char]96) { $search++ }
            if (($search - $closingStart) -eq $runLength) {
                $closingEnd = $search
                break
            }
        }
        if ($closingEnd -lt 0) {
            continue
        }

        for ($cursor = $runStart; $cursor -lt $closingEnd; $cursor++) {
            if ($characters[$cursor] -notin @([char]10, [char]13)) {
                $characters[$cursor] = [char]32
            }
        }
        $index = $closingEnd
    }
    return [string]::new($characters)
}

function Get-FenceMarker {
    param([AllowEmptyString()][string]$Line)
    $match = [System.Text.RegularExpressions.Regex]::Match($Line, '^ {0,3}(?<mark>`{3,}|~{3,})(?<rest>.*)$')
    if (-not $match.Success) {
        return $null
    }
    $marker = $match.Groups['mark'].Value
    if ($marker[0] -eq [char]96 -and $match.Groups['rest'].Value.Contains([char]96)) {
        return $null
    }
    return $marker
}

function Test-FenceClosing {
    param(
        [AllowEmptyString()][string]$Line,
        [Parameter(Mandatory = $true)][char]$Character,
        [Parameter(Mandatory = $true)][int]$MinimumLength
    )

    $literal = [System.Text.RegularExpressions.Regex]::Escape([string]$Character)
    return [System.Text.RegularExpressions.Regex]::IsMatch(
        $Line,
        ('^ {0,3}' + $literal + '{' + $MinimumLength + ',}[ \t]*$')
    )
}

function Test-ExternalHostAllowlistExemptPath {
    param([Parameter(Mandatory = $true)][string]$RelativePath)

    if ($RelativePath -ceq 'THIRD_PARTY_NOTICES.md') {
        return $true
    }
    return [System.Text.RegularExpressions.Regex]::IsMatch(
        $RelativePath,
        '^third_party/(?:[^/]+/)+LICENSE\.md$',
        [System.Text.RegularExpressions.RegexOptions]::CultureInvariant
    )
}

function Test-CanonicalExternalHostAllowed {
    param(
        [Parameter(Mandatory = $true)][string]$Canonical,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )

    # Generated third-party notices and canonical vendored license documents
    # must preserve upstream URLs verbatim. This path-scoped exception only
    # bypasses the product-document host allowlist; URI syntax, UTF-8, private
    # path, link, and all other Docs checks still apply.
    if (Test-ExternalHostAllowlistExemptPath -RelativePath $RelativePath) {
        return $true
    }

    try {
        $uri = [System.Uri]$Canonical
        if ($uri.Scheme -notin @('http', 'https')) {
            return $true
        }
        return $script:AllowedExternalHosts.Contains($uri.IdnHost)
    } catch {
        return $false
    }
}

function Get-RawHtmlBlockStartState {
    param([AllowEmptyString()][string]$Line)

    $type1 = $script:RawHtmlBlockType1Start.Match($Line)
    if ($type1.Success) {
        return [pscustomobject]@{
            BlankTerminated = $false
            EndPattern = [System.Text.RegularExpressions.Regex]::new(
                '</(?:pre|script|style|textarea)>',
                [System.Text.RegularExpressions.RegexOptions]::IgnoreCase
            )
        }
    }
    if ([System.Text.RegularExpressions.Regex]::IsMatch($Line, '^ {0,3}<!--')) {
        return [pscustomobject]@{ BlankTerminated = $false; EndPattern = [regex]::new('-->') }
    }
    if ([System.Text.RegularExpressions.Regex]::IsMatch($Line, '^ {0,3}<\?')) {
        return [pscustomobject]@{ BlankTerminated = $false; EndPattern = [regex]::new('\?>') }
    }
    if ([System.Text.RegularExpressions.Regex]::IsMatch($Line, '^ {0,3}<![A-Za-z]')) {
        return [pscustomobject]@{ BlankTerminated = $false; EndPattern = [regex]::new('>') }
    }
    if ([System.Text.RegularExpressions.Regex]::IsMatch($Line, '^ {0,3}<!\[CDATA\[')) {
        return [pscustomobject]@{ BlankTerminated = $false; EndPattern = [regex]::new('\]\]>') }
    }
    if ($script:RawHtmlBlockType6Start.IsMatch($Line) -or (Test-ConservativeRawHtmlType7Start -Line $Line)) {
        return [pscustomobject]@{ BlankTerminated = $true; EndPattern = $null }
    }
    return $null
}

function Test-RawHtmlBlockLine {
    param(
        [AllowEmptyString()][string]$Line,
        [Parameter(Mandatory = $true)][ref]$State
    )

    if ($null -ne $State.Value -and $State.Value.BlankTerminated -and
        [System.Text.RegularExpressions.Regex]::IsMatch($Line, '^[ \t]*$')) {
        $State.Value = $null
        return $false
    }
    if ($null -eq $State.Value) {
        $State.Value = Get-RawHtmlBlockStartState -Line $Line
        if ($null -eq $State.Value) {
            return $false
        }
    }

    $current = $State.Value
    if (-not $current.BlankTerminated -and $current.EndPattern.IsMatch($Line)) {
        $State.Value = $null
    }
    return $true
}

function Test-ExternalHostAllowlist {
    param(
        [Parameter(Mandatory = $true)][object]$Document,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )

    $urlPattern = [System.Text.RegularExpressions.Regex]::new(
        '(?i)(?<![A-Za-z0-9+.-])(?<scheme>https?)://(?<authority>\[[^\]\s<>]+\](?::[0-9]+)?|[^\s/?#<>()\[\]\x22\x27\x60]+)'
    )
    $visibleLines = @(Split-DocumentLines -Text (Get-MarkdownHtmlScanText -Document $Document))
    for ($index = 0; $index -lt $visibleLines.Count; $index++) {
        $candidate = $visibleLines[$index]
        if (Test-ContainsEntityReferenceSyntax -Value $candidate) {
            Add-DocViolation -Rule 'DOC_LINK_ENTITY_UNSUPPORTED' -Path $RelativePath -Line ($index + 1)
        }
        foreach ($match in $urlPattern.Matches($candidate)) {
            $external = $match.Groups['scheme'].Value + '://' + $match.Groups['authority'].Value + '/'
            $canonicalExternal = $null
            if (-not (Test-StrictExternalUri -Value $external -AllowedSchemes @('http', 'https') -Canonical ([ref]$canonicalExternal)) -or
                -not (Test-CanonicalExternalHostAllowed -Canonical $canonicalExternal -RelativePath $RelativePath)) {
                Add-DocViolation -Rule 'DOC_LINK_EXTERNAL_HOST_NOT_ALLOWED' -Path $RelativePath -Line ($index + 1)
            }
        }
    }
}

function Test-PrivateAbsoluteContent {
    param(
        [Parameter(Mandatory = $true)][object]$Document,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )

    # Assemble concrete private-path markers at runtime so this scanner does not
    # permanently trip the same repository-content policy that it enforces.
    $forwardSlash = [string][char]47
    $homeSegment = 'ho' + 'me'
    $usersSegment = 'us' + 'ers'
    $rootSegment = 'ro' + 'ot'
    $mountSegment = 'mn' + 't'
    $mediaSegment = 'med' + 'ia'
    $patterns = @(
        '(?i)(?<![A-Za-z0-9+.-])[A-Z]:[\\/]',
        '(?<![:/])(?:\\\\|//)[^\\/\s]+[\\/]',
        '(?i)\bfile\s*:(?:/{1,3}|\\+)',
        ('(?i)(?<![A-Za-z0-9])' + $forwardSlash + '(?:' + $homeSegment + '|' + $usersSegment + ')' + $forwardSlash + '[^/\s]+' + $forwardSlash),
        ('(?i)(?<![A-Za-z0-9])' + $forwardSlash + $rootSegment + '(?:' + $forwardSlash + '|\b)'),
        ('(?i)(?<![A-Za-z0-9])' + $forwardSlash + '(?:' + $mountSegment + '|' + $mediaSegment + ')' + $forwardSlash + '[A-Za-z](?:/|\\)(?:' + $usersSegment + '|' + $homeSegment + ')' + $forwardSlash),
        '(?<![A-Za-z0-9])~[\\/][^\s]'
    )

    for ($index = 0; $index -lt $Document.Lines.Count; $index++) {
        foreach ($pattern in $patterns) {
            if ([System.Text.RegularExpressions.Regex]::IsMatch($Document.Lines[$index], $pattern)) {
                Add-DocViolation -Rule 'DOC_PRIVATE_ABSOLUTE_PATH' -Path $RelativePath -Line ($index + 1)
                break
            }
        }
    }
}

function Get-ExplicitAnchorRecord {
    param([AllowEmptyString()][string]$Line)

    $candidate = Remove-InlineCode -Line $Line
    if (-not [System.Text.RegularExpressions.Regex]::IsMatch($candidate, '(?i)<a\s+id\s*=')) {
        return $null
    }
    $match = [System.Text.RegularExpressions.Regex]::Match(
        $candidate,
        '^\s*<a\s+id\s*=\s*(?:"(?<double>[^"\r\n<>]+)"|''(?<single>[^''\r\n<>]+)'')\s*>\s*</a>\s*$'
    )
    if (-not $match.Success) {
        return [pscustomobject]@{ SyntaxValid = $false; Value = '' }
    }
    $value = if ($match.Groups['double'].Success) { $match.Groups['double'].Value } else { $match.Groups['single'].Value }
    return [pscustomobject]@{
        SyntaxValid = [System.Text.RegularExpressions.Regex]::IsMatch($value, '^[A-Za-z][A-Za-z0-9._:-]*$')
        Value = $value
    }
}

function Get-ExplicitAnchors {
    param(
        [Parameter(Mandatory = $true)][object]$Document,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )

    $anchors = [System.Collections.Generic.Dictionary[string,int]]::new([System.StringComparer]::Ordinal)
    $fenceCharacter = [char]0
    $fenceLength = 0
    $rawHtmlBlockState = $null

    for ($index = 0; $index -lt $Document.Lines.Count; $index++) {
        $line = $Document.Lines[$index]
        if ($fenceLength -gt 0) {
            if (Test-FenceClosing -Line $line -Character $fenceCharacter -MinimumLength $fenceLength) {
                $fenceCharacter = [char]0
                $fenceLength = 0
            }
            continue
        }
        $insideRawHtmlBlock = Test-RawHtmlBlockLine -Line $line -State ([ref]$rawHtmlBlockState)
        if (-not $insideRawHtmlBlock) {
            $marker = Get-FenceMarker -Line $line
            if ($null -ne $marker) {
                $fenceCharacter = $marker[0]
                $fenceLength = $marker.Length
                continue
            }
        }

        $record = Get-ExplicitAnchorRecord -Line $line
        if ($null -eq $record) {
            continue
        }
        if ($record.SyntaxValid) {
            $anchor = [string]$record.Value
            if ($anchors.ContainsKey($anchor)) {
                Add-DocViolation -Rule 'DOC_ANCHOR_DUPLICATE' -Path $RelativePath -Line ($index + 1)
            } else {
                $anchors.Add($anchor, $index + 1)
            }
        } else {
            Add-DocViolation -Rule 'DOC_ANCHOR_SYNTAX' -Path $RelativePath -Line ($index + 1)
        }
    }
    return $anchors
}

function Test-MarkdownLinks {
    param(
        [Parameter(Mandatory = $true)][object]$Document,
        [Parameter(Mandatory = $true)][string]$FullPath,
        [Parameter(Mandatory = $true)][string]$RelativePath,
        [Parameter(Mandatory = $true)][object]$MarkdownByFull
    )

    $linkPattern = [System.Text.RegularExpressions.Regex]::new('(?<image>!)?\[[^\]\r\n]*\]\((?<body><[^>\r\n]+>|[^)\r\n]+)\)')
    $visibleLines = @(Split-DocumentLines -Text (Get-MarkdownHtmlScanText -Document $Document))
    for ($index = 0; $index -lt $visibleLines.Count; $index++) {
        $candidate = $visibleLines[$index]
        $linkMatches = $linkPattern.Matches($candidate)
        $residual = $linkPattern.Replace($candidate, '')
        if ($residual.Contains('](')) {
            Add-DocViolation -Rule 'DOC_LINK_SYNTAX_UNSUPPORTED' -Path $RelativePath -Line ($index + 1)
        }
        if ([System.Text.RegularExpressions.Regex]::IsMatch($residual, '!?\[[^\]\r\n]+\]\s*\[[^\]\r\n]*\]') -or
            [System.Text.RegularExpressions.Regex]::IsMatch($residual, '^\s{0,3}\[[^\]\r\n]+\]:')) {
            Add-DocViolation -Rule 'DOC_LINK_REFERENCE_UNSUPPORTED' -Path $RelativePath -Line ($index + 1)
        }

        foreach ($linkMatch in $linkMatches) {
            $body = $linkMatch.Groups['body'].Value
            $angleWrapped = $body.Length -ge 2 -and $body[0] -eq '<' -and $body[$body.Length - 1] -eq '>'
            if ($angleWrapped) {
                $destination = $body.Substring(1, $body.Length - 2)
            } else {
                $destination = $body.Trim()
                if ($destination -match '\s') {
                    Add-DocViolation -Rule 'DOC_LINK_SPACE_REQUIRES_ANGLE' -Path $RelativePath -Line ($index + 1)
                    continue
                }
            }

            if ([string]::IsNullOrWhiteSpace($destination)) {
                Add-DocViolation -Rule 'DOC_LINK_EMPTY' -Path $RelativePath -Line ($index + 1)
                continue
            }
            if (Test-ContainsUnsafeUnicode -Value $destination) {
                Add-DocViolation -Rule 'DOC_LINK_CONTROL_CHARACTER' -Path $RelativePath -Line ($index + 1)
                continue
            }
            if (Test-ContainsEntityReferenceSyntax -Value $destination) {
                Add-DocViolation -Rule 'DOC_LINK_ENTITY_UNSUPPORTED' -Path $RelativePath -Line ($index + 1)
                continue
            }
            if ($destination.StartsWith('//') -or $destination.StartsWith('\\')) {
                Add-DocViolation -Rule 'DOC_LINK_ABSOLUTE' -Path $RelativePath -Line ($index + 1)
                continue
            }

            $schemeMatch = [System.Text.RegularExpressions.Regex]::Match($destination, '^(?<scheme>[A-Za-z][A-Za-z0-9+.-]*):')
            if ($schemeMatch.Success) {
                $scheme = $schemeMatch.Groups['scheme'].Value.ToLowerInvariant()
                if ($scheme.Length -eq 1 -and $destination.Length -gt 2 -and ($destination[2] -eq [char]47 -or $destination[2] -eq [char]92)) {
                    Add-DocViolation -Rule 'DOC_LINK_ABSOLUTE' -Path $RelativePath -Line ($index + 1)
                } elseif ($scheme -notin @('http', 'https', 'mailto')) {
                    Add-DocViolation -Rule 'DOC_LINK_SCHEME_UNSUPPORTED' -Path $RelativePath -Line ($index + 1)
                } else {
                    $canonicalExternal = $null
                    if (-not (Test-StrictExternalUri -Value $destination -AllowedSchemes @('http', 'https', 'mailto') -Canonical ([ref]$canonicalExternal))) {
                        Add-DocViolation -Rule 'DOC_LINK_EXTERNAL_URI_INVALID' -Path $RelativePath -Line ($index + 1)
                    } elseif ($scheme -in @('http', 'https') -and
                        -not (Test-CanonicalExternalHostAllowed -Canonical $canonicalExternal -RelativePath $RelativePath)) {
                        Add-DocViolation -Rule 'DOC_LINK_EXTERNAL_HOST_NOT_ALLOWED' -Path $RelativePath -Line ($index + 1)
                    }
                }
                continue
            }

            if ($destination.Contains([char]92)) {
                Add-DocViolation -Rule 'DOC_LINK_BACKSLASH' -Path $RelativePath -Line ($index + 1)
                continue
            }

            $fragmentRaw = $null
            $pathRaw = $destination
            $hashIndex = $destination.IndexOf([char]35)
            if ($hashIndex -ge 0) {
                $pathRaw = $destination.Substring(0, $hashIndex)
                $fragmentRaw = $destination.Substring($hashIndex + 1)
            }
            if ($pathRaw.Contains('?')) {
                Add-DocViolation -Rule 'DOC_LINK_QUERY_UNSUPPORTED' -Path $RelativePath -Line ($index + 1)
                continue
            }
            if (-not (Test-StrictPercentEncoding -Value $pathRaw) -or
                ($null -ne $fragmentRaw -and -not (Test-StrictPercentEncoding -Value $fragmentRaw))) {
                Add-DocViolation -Rule 'DOC_LINK_PERCENT_ENCODING' -Path $RelativePath -Line ($index + 1)
                continue
            }

            try {
                $decodedPath = [System.Uri]::UnescapeDataString($pathRaw)
                $decodedFragment = if ($null -eq $fragmentRaw) { $null } else { [System.Uri]::UnescapeDataString($fragmentRaw) }
            } catch {
                Add-DocViolation -Rule 'DOC_LINK_PERCENT_ENCODING' -Path $RelativePath -Line ($index + 1)
                continue
            }
            if ($decodedPath.Contains([char]92) -or $decodedPath.IndexOf([char]0) -ge 0) {
                Add-DocViolation -Rule 'DOC_LINK_BACKSLASH' -Path $RelativePath -Line ($index + 1)
                continue
            }
            if ([System.IO.Path]::IsPathRooted($decodedPath) -or $decodedPath.StartsWith('/')) {
                Add-DocViolation -Rule 'DOC_LINK_ABSOLUTE' -Path $RelativePath -Line ($index + 1)
                continue
            }

            try {
                if ([string]::IsNullOrEmpty($decodedPath)) {
                    $targetFull = $FullPath
                } else {
                    $nativePath = $decodedPath.Replace([char]47, [System.IO.Path]::DirectorySeparatorChar)
                    $targetFull = [System.IO.Path]::GetFullPath([System.IO.Path]::Combine([System.IO.Path]::GetDirectoryName($FullPath), $nativePath))
                }
            } catch {
                Add-DocViolation -Rule 'DOC_LINK_PATH_INVALID' -Path $RelativePath -Line ($index + 1)
                continue
            }

            if (-not (Test-InRepository -FullPath $targetFull)) {
                Add-DocViolation -Rule 'DOC_LINK_ROOT_ESCAPE' -Path $RelativePath -Line ($index + 1)
                continue
            }
            $targetRelative = Get-RepositoryRelativePath -FullPath $targetFull
            if (Test-IsRootGitPath -RelativePath $targetRelative) {
                Add-DocViolation -Rule 'DOC_LINK_GIT_TARGET' -Path $RelativePath -Line ($index + 1)
                continue
            }
            $targetItem = $null
            if (-not (Resolve-SafeLocalLinkTarget -TargetRelative $targetRelative -SourceRelative $RelativePath -Line ($index + 1) -TargetItem ([ref]$targetItem))) {
                continue
            }
            if ($null -ne $targetItem -and $targetItem.PSIsContainer -and
                -not (Test-DirectoryContainsPublicFile -DirectoryFull $targetFull)) {
                Add-DocViolation -Rule 'DOC_LINK_DIRECTORY_NO_PUBLIC_FILE' -Path $RelativePath -Line ($index + 1)
                continue
            }

            if ($null -ne $decodedFragment) {
                if ([string]::IsNullOrEmpty($decodedFragment) -or $decodedFragment -notmatch '^[A-Za-z][A-Za-z0-9._:-]*$') {
                    Add-DocViolation -Rule 'DOC_LINK_ANCHOR_SYNTAX' -Path $RelativePath -Line ($index + 1)
                    continue
                }
                if ($null -eq $targetItem -or $targetItem.PSIsContainer -or
                    [System.IO.Path]::GetExtension($targetFull) -ine '.md' -or
                    -not $MarkdownByFull.ContainsKey($targetFull)) {
                    Add-DocViolation -Rule 'DOC_LINK_ANCHOR_TARGET' -Path $RelativePath -Line ($index + 1)
                    continue
                }
                $targetDocument = $MarkdownByFull[$targetFull]
                if (-not $targetDocument.Anchors.ContainsKey($decodedFragment)) {
                    Add-DocViolation -Rule 'DOC_LINK_ANCHOR_MISSING' -Path $RelativePath -Line ($index + 1)
                }
            }
        }
    }
}

function Get-MarkdownHtmlScanText {
    param([Parameter(Mandatory = $true)][object]$Document)

    $cached = $Document.PSObject.Properties['MarkdownHtmlScanText']
    if ($null -ne $cached) {
        return [string]$cached.Value
    }

    $lines = New-Object 'System.Collections.Generic.List[string]'
    $protectedLines = New-Object 'System.Collections.Generic.List[bool]'
    $fenceCharacter = [char]0
    $fenceLength = 0
    $rawHtmlBlockState = $null
    foreach ($line in $Document.Lines) {
        if ($fenceLength -gt 0) {
            $lines.Add([string]::new([char]32, $line.Length))
            $protectedLines.Add($true)
            if (Test-FenceClosing -Line $line -Character $fenceCharacter -MinimumLength $fenceLength) {
                $fenceCharacter = [char]0
                $fenceLength = 0
            }
            continue
        }
        if (Test-RawHtmlBlockLine -Line $line -State ([ref]$rawHtmlBlockState)) {
            $lines.Add($line)
            $protectedLines.Add($true)
            continue
        }
        $marker = Get-FenceMarker -Line $line
        if ($null -ne $marker) {
            $fenceCharacter = $marker[0]
            $fenceLength = $marker.Length
            $lines.Add([string]::new([char]32, $line.Length))
            $protectedLines.Add($true)
            continue
        }
        $lines.Add($line)
        $protectedLines.Add($false)
    }

    $text = [string]::Join("`n", $lines.ToArray())
    $protectedMask = New-Object 'bool[]' $text.Length
    $offset = 0
    for ($lineIndex = 0; $lineIndex -lt $lines.Count; $lineIndex++) {
        if ($protectedLines[$lineIndex]) {
            for ($cursor = $offset; $cursor -lt $offset + $lines[$lineIndex].Length; $cursor++) {
                $protectedMask[$cursor] = $true
            }
        }
        $offset += $lines[$lineIndex].Length
        if ($lineIndex + 1 -lt $lines.Count) {
            $protectedMask[$offset] = $protectedLines[$lineIndex] -or $protectedLines[$lineIndex + 1]
            $offset++
        }
    }
    $result = Remove-InlineCode -Line $text -ProtectedMask $protectedMask
    Add-Member -InputObject $Document -NotePropertyName 'MarkdownHtmlScanText' -NotePropertyValue $result -Force
    return $result
}

function Get-RawHtmlStartTags {
    param(
        [Parameter(Mandatory = $true)][string]$Text,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )

    $tokens = New-Object 'System.Collections.Generic.List[object]'
    $index = 0
    $line = 1
    while ($index -lt $Text.Length) {
        if ($Text[$index] -ne [char]60) {
            if ($Text[$index] -eq [char]10) { $line++ }
            $index++
            continue
        }
        $start = $index
        $startLine = $line
        $destinationPrefix = $index - 1
        while ($destinationPrefix -ge 0 -and $Text[$destinationPrefix] -in @([char]32, [char]9)) {
            $destinationPrefix--
        }
        if ($destinationPrefix -ge 1 -and $Text[$destinationPrefix] -eq [char]40 -and $Text[$destinationPrefix - 1] -eq [char]93) {
            $endDestination = $Text.IndexOf([char]62, $index + 1)
            if ($endDestination -lt 0) {
                Add-DocViolation -Rule 'DOC_HTML_LINK_SYNTAX' -Path $RelativePath -Line $startLine
                break
            }
            $line += [System.Text.RegularExpressions.Regex]::Matches($Text.Substring($index, $endDestination - $index + 1), "`n").Count
            $index = $endDestination + 1
            continue
        }
        if ($index + 4 -le $Text.Length -and $Text.Substring($index, 4) -ceq '<!--') {
            $endComment = -1
            $commentTerminatorLength = 0
            if ($index + 5 -le $Text.Length -and $Text.Substring($index, 5) -ceq '<!-->') {
                $endComment = $index + 5
            } elseif ($index + 6 -le $Text.Length -and $Text.Substring($index, 6) -ceq '<!--->') {
                $endComment = $index + 6
            } else {
                $standardEnd = $Text.IndexOf('-->', $index + 4, [System.StringComparison]::Ordinal)
                $browserEndBang = $Text.IndexOf('--!>', $index + 4, [System.StringComparison]::Ordinal)
                if ($standardEnd -ge 0 -and ($browserEndBang -lt 0 -or $standardEnd -lt $browserEndBang)) {
                    $endComment = $standardEnd
                    $commentTerminatorLength = 3
                } elseif ($browserEndBang -ge 0) {
                    $endComment = $browserEndBang
                    $commentTerminatorLength = 4
                }
            }
            if ($endComment -lt 0) {
                Add-DocViolation -Rule 'DOC_HTML_LINK_SYNTAX' -Path $RelativePath -Line $startLine
                break
            }
            if ($commentTerminatorLength -gt 0) {
                $endComment += $commentTerminatorLength
            }
            $line += [System.Text.RegularExpressions.Regex]::Matches($Text.Substring($index, $endComment - $index), "`n").Count
            $index = $endComment
            continue
        }
        if ($index + 1 -ge $Text.Length) {
            break
        }
        $next = $Text[$index + 1]
        if ($next -in @([char]33, [char]47, [char]63)) {
            $endSpecial = $Text.IndexOf([char]62, $index + 2)
            if ($endSpecial -lt 0) {
                Add-DocViolation -Rule 'DOC_HTML_LINK_SYNTAX' -Path $RelativePath -Line $startLine
                break
            }
            $line += [System.Text.RegularExpressions.Regex]::Matches($Text.Substring($index, $endSpecial - $index + 1), "`n").Count
            $index = $endSpecial + 1
            continue
        }
        if (-not [System.Char]::IsLetter($next)) {
            $index++
            continue
        }

        $cursor = $index + 1
        while ($cursor -lt $Text.Length -and ($Text[$cursor] -match '[A-Za-z0-9:-]')) { $cursor++ }
        $tag = $Text.Substring($index + 1, $cursor - $index - 1).ToLowerInvariant()
        $endTag = -1
        $state = 'BeforeAttributeName'
        $scan = $cursor
        while ($scan -lt $Text.Length) {
            $character = $Text[$scan]
            $isHtmlWhitespace = [int]$character -in @(9, 10, 12, 13, 32)
            switch ($state) {
                'BeforeAttributeName' {
                    if ($isHtmlWhitespace) { $scan++; continue }
                    if ($character -eq [char]62) { $endTag = $scan; break }
                    if ($character -eq [char]47) { $state = 'SelfClosingStartTag'; $scan++; continue }
                    $state = 'AttributeName'
                    continue
                }
                'AttributeName' {
                    if ($isHtmlWhitespace) { $state = 'AfterAttributeName'; $scan++; continue }
                    if ($character -eq [char]47) { $state = 'SelfClosingStartTag'; $scan++; continue }
                    if ($character -eq [char]61) { $state = 'BeforeAttributeValue'; $scan++; continue }
                    if ($character -eq [char]62) { $endTag = $scan; break }
                    $scan++
                    continue
                }
                'AfterAttributeName' {
                    if ($isHtmlWhitespace) { $scan++; continue }
                    if ($character -eq [char]47) { $state = 'SelfClosingStartTag'; $scan++; continue }
                    if ($character -eq [char]61) { $state = 'BeforeAttributeValue'; $scan++; continue }
                    if ($character -eq [char]62) { $endTag = $scan; break }
                    $state = 'AttributeName'
                    continue
                }
                'BeforeAttributeValue' {
                    if ($isHtmlWhitespace) { $scan++; continue }
                    if ($character -eq [char]34) { $state = 'DoubleQuotedAttributeValue'; $scan++; continue }
                    if ($character -eq [char]39) { $state = 'SingleQuotedAttributeValue'; $scan++; continue }
                    if ($character -eq [char]62) { $endTag = $scan; break }
                    $state = 'UnquotedAttributeValue'
                    continue
                }
                'DoubleQuotedAttributeValue' {
                    if ($character -eq [char]34) { $state = 'AfterQuotedAttributeValue' }
                    $scan++
                    continue
                }
                'SingleQuotedAttributeValue' {
                    if ($character -eq [char]39) { $state = 'AfterQuotedAttributeValue' }
                    $scan++
                    continue
                }
                'AfterQuotedAttributeValue' {
                    if ($isHtmlWhitespace) { $state = 'BeforeAttributeName'; $scan++; continue }
                    if ($character -eq [char]47) { $state = 'SelfClosingStartTag'; $scan++; continue }
                    if ($character -eq [char]62) { $endTag = $scan; break }
                    $state = 'BeforeAttributeName'
                    continue
                }
                'UnquotedAttributeValue' {
                    if ($isHtmlWhitespace) { $state = 'BeforeAttributeName'; $scan++; continue }
                    if ($character -eq [char]62) { $endTag = $scan; break }
                    $scan++
                    continue
                }
                'SelfClosingStartTag' {
                    if ($character -eq [char]62) { $endTag = $scan; break }
                    $state = 'BeforeAttributeName'
                    continue
                }
                default { throw 'DOC_HTML_TOKENIZER_STATE_INVALID' }
            }
            if ($endTag -ge 0) {
                break
            }
        }
        if ($endTag -lt 0) {
            Add-DocViolation -Rule 'DOC_HTML_LINK_SYNTAX' -Path $RelativePath -Line $startLine
            $index++
            continue
        }
        $raw = $Text.Substring($start, $endTag - $start + 1)
        if ($raw -match '^<[A-Za-z][A-Za-z0-9+.-]*://[^<>\s]+>$' -or
            $raw -match '^<[A-Za-z0-9.!#$%&''*+/=?^_`{|}~-]+@[A-Za-z0-9.-]+>$') {
            $line += [System.Text.RegularExpressions.Regex]::Matches($raw, "`n").Count
            $index = $endTag + 1
            continue
        }
        $tokens.Add([pscustomobject]@{
            Tag = $tag
            Attributes = $Text.Substring($cursor, $endTag - $cursor)
            Line = $startLine
        })
        $line += [System.Text.RegularExpressions.Regex]::Matches($raw, "`n").Count
        $index = $endTag + 1
    }
    return $tokens.ToArray()
}

function Get-RawHtmlAttributes {
    param(
        [AllowEmptyString()][string]$Text,
        [Parameter(Mandatory = $true)][ref]$Valid
    )

    $Valid.Value = $true
    $attributes = New-Object 'System.Collections.Generic.List[object]'
    $index = 0
    while ($index -lt $Text.Length) {
        if ($Text[$index] -eq [char]47) {
            $index++
            if ($index -ne $Text.Length) { $Valid.Value = $false }
            break
        }
        $separatorStart = $index
        $index = Get-CommonMarkInlineWhitespaceEnd -Text $Text -Start $index
        if ($index -ge $Text.Length) { break }
        if ($index -eq $separatorStart) {
            $Valid.Value = $false
            break
        }
        if ($Text[$index] -eq [char]47) {
            $index++
            if ($index -ne $Text.Length) { $Valid.Value = $false }
            break
        }
        if (-not ($Text[$index] -match '[A-Za-z_:]')) {
            $Valid.Value = $false
            break
        }
        $nameStart = $index
        $index++
        while ($index -lt $Text.Length -and ($Text[$index] -match '[A-Za-z0-9_:.-]')) { $index++ }
        $name = $Text.Substring($nameStart, $index - $nameStart).ToLowerInvariant()
        $afterName = $index
        $index = Get-CommonMarkInlineWhitespaceEnd -Text $Text -Start $index
        $hasValue = $false
        $quoted = $false
        $value = ''
        if ($index -lt $Text.Length -and $Text[$index] -eq [char]61) {
            $hasValue = $true
            $index++
            $index = Get-CommonMarkInlineWhitespaceEnd -Text $Text -Start $index
            if ($index -ge $Text.Length) {
                $Valid.Value = $false
                break
            }
            if ($Text[$index] -eq [char]34 -or $Text[$index] -eq [char]39) {
                $quoted = $true
                $quote = $Text[$index]
                $index++
                $valueStart = $index
                while ($index -lt $Text.Length -and $Text[$index] -ne $quote) { $index++ }
                if ($index -ge $Text.Length) {
                    $Valid.Value = $false
                    break
                }
                $value = $Text.Substring($valueStart, $index - $valueStart)
                $index++
            } else {
                $valueStart = $index
                while ($index -lt $Text.Length -and [int]$Text[$index] -notin @(9, 10, 13, 32)) {
                    if ($Text[$index] -in @([char]34, [char]39, [char]60, [char]61, [char]62, [char]96) -or
                        [int]$Text[$index] -lt 32 -or [int]$Text[$index] -eq 127) {
                        $Valid.Value = $false
                        break
                    }
                    $index++
                }
                if (-not $Valid.Value -or $index -eq $valueStart) {
                    $Valid.Value = $false
                    break
                }
                $value = $Text.Substring($valueStart, $index - $valueStart)
            }
        } else {
            $index = $afterName
        }
        $attributes.Add([pscustomobject]@{ Name = $name; HasValue = $hasValue; Quoted = $quoted; Value = $value })
    }
    return $attributes.ToArray()
}

function Test-RawHtmlLinks {
    param(
        [Parameter(Mandatory = $true)][object]$Document,
        [Parameter(Mandatory = $true)][string]$FullPath,
        [Parameter(Mandatory = $true)][string]$RelativePath,
        [Parameter(Mandatory = $true)][object]$MarkdownByFull
    )

    $tagAttributeModes = @{
        'a' = @{ 'href' = 'single'; 'ping' = 'space-list'; 'attributionsrc' = 'space-list' }
        'area' = @{ 'href' = 'single'; 'ping' = 'space-list'; 'attributionsrc' = 'space-list' }
        'base' = @{ 'href' = 'single' }; 'link' = @{ 'href' = 'single'; 'imagesrcset' = 'srcset' }
        'img' = @{ 'src' = 'single'; 'srcset' = 'srcset'; 'longdesc' = 'single'; 'usemap' = 'single'; 'attributionsrc' = 'space-list' }
        'source' = @{ 'src' = 'single'; 'srcset' = 'srcset' }; 'video' = @{ 'src' = 'single'; 'poster' = 'single' }
        'audio' = @{ 'src' = 'single' }; 'iframe' = @{ 'src' = 'single'; 'longdesc' = 'single' }
        'script' = @{ 'src' = 'single' }; 'object' = @{ 'data' = 'single'; 'codebase' = 'single'; 'archive' = 'space-list'; 'usemap' = 'single' }
        'embed' = @{ 'src' = 'single' }; 'track' = @{ 'src' = 'single' }; 'form' = @{ 'action' = 'single' }
        'input' = @{ 'src' = 'single'; 'formaction' = 'single' }; 'button' = @{ 'formaction' = 'single' }
        'blockquote' = @{ 'cite' = 'single' }; 'q' = @{ 'cite' = 'single' }; 'del' = @{ 'cite' = 'single' }; 'ins' = @{ 'cite' = 'single' }
        'html' = @{ 'manifest' = 'single' }; 'body' = @{ 'background' = 'single' }; 'table' = @{ 'background' = 'single' }
        'td' = @{ 'background' = 'single' }; 'th' = @{ 'background' = 'single' }
        'use' = @{ 'href' = 'single'; 'xlink:href' = 'single' }; 'image' = @{ 'href' = 'single'; 'xlink:href' = 'single' }
    }
    $urlAttributeNames = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::OrdinalIgnoreCase)
    foreach ($modes in $tagAttributeModes.Values) { foreach ($name in $modes.Keys) { [void]$urlAttributeNames.Add($name) } }
    foreach ($name in @('srcdoc', 'profile', 'imagesrcset', 'attributionsrc')) { [void]$urlAttributeNames.Add($name) }
    $safeNonUrlAttributeNames = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::OrdinalIgnoreCase)
    foreach ($name in @(
        'accept', 'accept-charset', 'alt', 'as', 'autocomplete', 'autoplay', 'charset', 'checked', 'class',
        'colspan', 'content', 'controls', 'crossorigin', 'datetime', 'decoding', 'default', 'dir', 'disabled',
        'download', 'enctype', 'fetchpriority', 'for', 'height', 'hidden', 'http-equiv', 'id', 'imagesizes',
        'integrity', 'kind', 'label', 'lang', 'loading', 'loop', 'max', 'media', 'method', 'min', 'multiple',
        'muted', 'name', 'nonce', 'open', 'pattern', 'placeholder', 'playsinline', 'preload', 'readonly', 'referrerpolicy',
        'rel', 'required', 'role', 'rowspan', 'scope', 'selected', 'sizes', 'slot', 'span', 'srclang', 'start', 'step',
        'tabindex', 'target', 'title', 'translate', 'type', 'value', 'width', 'wrap'
    )) { [void]$safeNonUrlAttributeNames.Add($name) }
    $activeContentTags = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::OrdinalIgnoreCase)
    foreach ($name in @('base', 'embed', 'iframe', 'link', 'meta', 'object', 'script', 'style')) {
        [void]$activeContentTags.Add($name)
    }

    $scanText = Get-MarkdownHtmlScanText -Document $Document
    foreach ($token in @(Get-RawHtmlStartTags -Text $scanText -RelativePath $RelativePath)) {
        if ($activeContentTags.Contains($token.Tag)) {
            # JavaScript and CSS can synthesize network destinations that a
            # static URL allowlist cannot soundly enumerate. Public Markdown
            # must use fenced examples instead of executable raw content.
            Add-DocViolation -Rule 'DOC_HTML_ACTIVE_CONTENT_UNSUPPORTED' -Path $RelativePath -Line $token.Line
        }
        $attributesValid = $false
        $attributes = @(Get-RawHtmlAttributes -Text $token.Attributes -Valid ([ref]$attributesValid))
        if (-not $attributesValid) {
            Add-DocViolation -Rule 'DOC_HTML_LINK_SYNTAX' -Path $RelativePath -Line $token.Line
            continue
        }
        $seenNames = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::OrdinalIgnoreCase)
        $byName = @{}
        foreach ($attribute in $attributes) {
            if (-not $seenNames.Add($attribute.Name)) {
                Add-DocViolation -Rule 'DOC_HTML_LINK_SYNTAX' -Path $RelativePath -Line $token.Line
                continue
            }
            $byName[$attribute.Name] = $attribute
            if ($attribute.HasValue -and -not $attribute.Quoted) {
                Add-DocViolation -Rule 'DOC_HTML_LINK_SYNTAX' -Path $RelativePath -Line $token.Line
                continue
            }
            if ($attribute.Name -match '(?i)^on') {
                Add-DocViolation -Rule 'DOC_HTML_ACTIVE_CONTENT_UNSUPPORTED' -Path $RelativePath -Line $token.Line
                continue
            }
            if ($attribute.Name -in @('style', 'srcdoc')) {
                Add-DocViolation -Rule 'DOC_HTML_URL_ATTRIBUTE_UNSUPPORTED' -Path $RelativePath -Line $token.Line
                continue
            }
            if (-not $urlAttributeNames.Contains($attribute.Name)) {
                if (-not $safeNonUrlAttributeNames.Contains($attribute.Name) -and
                    $attribute.Name -notmatch '(?i)^(?:aria|data)-[A-Za-z0-9_.:-]+$') {
                    Add-DocViolation -Rule 'DOC_HTML_ATTRIBUTE_UNSUPPORTED' -Path $RelativePath -Line $token.Line
                }
                continue
            }
            if (-not $tagAttributeModes.ContainsKey($token.Tag) -or -not $tagAttributeModes[$token.Tag].ContainsKey($attribute.Name)) {
                Add-DocViolation -Rule 'DOC_HTML_URL_ATTRIBUTE_UNSUPPORTED' -Path $RelativePath -Line $token.Line
                continue
            }
            if (-not $attribute.HasValue -or -not $attribute.Quoted) {
                Add-DocViolation -Rule 'DOC_HTML_LINK_SYNTAX' -Path $RelativePath -Line $token.Line
                continue
            }
            # Raw HTML attributes follow the browser HTML parser, which also
            # accepts some legacy or numeric references without a semicolon.
            # Reject ampersands entirely in URL-bearing raw HTML attributes so
            # host validation cannot depend on a partial entity table.
            if ($attribute.Value.Contains('&')) {
                Add-DocViolation -Rule 'DOC_LINK_ENTITY_UNSUPPORTED' -Path $RelativePath -Line $token.Line
                continue
            }
            $attributeValue = [System.Net.WebUtility]::HtmlDecode($attribute.Value)
            if ([string]::IsNullOrWhiteSpace($attributeValue) -or $attributeValue.Contains('<') -or $attributeValue.Contains('>')) {
                Add-DocViolation -Rule 'DOC_HTML_LINK_SYNTAX' -Path $RelativePath -Line $token.Line
                continue
            }
            $destinations = @()
            switch ($tagAttributeModes[$token.Tag][$attribute.Name]) {
                'single' { $destinations = @($attributeValue) }
                'space-list' { $destinations = @($attributeValue -split '\s+' | Where-Object { $_ -ne '' }) }
                'srcset' {
                    foreach ($candidateValue in $attributeValue.Split([char]44)) {
                        $srcsetMatch = [System.Text.RegularExpressions.Regex]::Match($candidateValue.Trim(), '^(?<url>\S+)(?:\s+(?:[0-9]+w|[0-9]+(?:\.[0-9]+)?x))?$')
                        if (-not $srcsetMatch.Success) {
                            Add-DocViolation -Rule 'DOC_HTML_LINK_SYNTAX' -Path $RelativePath -Line $token.Line
                            $destinations = @()
                            break
                        }
                        $destinations += $srcsetMatch.Groups['url'].Value
                    }
                }
                default { Add-DocViolation -Rule 'DOC_HTML_URL_ATTRIBUTE_UNSUPPORTED' -Path $RelativePath -Line $token.Line }
            }
            foreach ($destination in $destinations) {
                $pseudoLines = New-Object 'string[]' $token.Line
                $pseudoLines[$token.Line - 1] = '[html](<' + $destination + '>)'
                Test-MarkdownLinks -Document ([pscustomobject]@{ Lines = $pseudoLines }) -FullPath $FullPath -RelativePath $RelativePath -MarkdownByFull $MarkdownByFull
            }
        }
        if ($token.Tag -eq 'meta') {
            $refresh = $byName.ContainsKey('http-equiv') -and $byName['http-equiv'].Value -match '(?i)^refresh$'
            $contentUrl = $byName.ContainsKey('content') -and $byName['content'].Value -match '(?i)\burl\s*='
            if ($refresh -or $contentUrl) {
                Add-DocViolation -Rule 'DOC_HTML_URL_ATTRIBUTE_UNSUPPORTED' -Path $RelativePath -Line $token.Line
            }
        }
    }
}

function Skip-JsonWhitespace {
    param([Parameter(Mandatory = $true)][object]$State)
    while ($State.Index -lt $State.Text.Length -and $State.Text[$State.Index] -in @([char]32, [char]9, [char]10, [char]13)) {
        $State.Index++
    }
}

function Read-JsonStringStrict {
    param(
        [Parameter(Mandatory = $true)][object]$State,
        [Parameter(Mandatory = $true)][ref]$Value
    )
    if ($State.Index -ge $State.Text.Length -or $State.Text[$State.Index] -ne [char]34) {
        return $false
    }
    $State.Index++
    $builder = New-Object System.Text.StringBuilder
    while ($State.Index -lt $State.Text.Length) {
        $character = $State.Text[$State.Index]
        $State.Index++
        if ($character -eq [char]34) {
            $Value.Value = $builder.ToString()
            return $true
        }
        if ([int]$character -lt 0x20) {
            return $false
        }
        if ($character -ne [char]92) {
            [void]$builder.Append($character)
            continue
        }
        if ($State.Index -ge $State.Text.Length) {
            return $false
        }
        $escape = $State.Text[$State.Index]
        $State.Index++
        switch ($escape) {
            '"' { [void]$builder.Append([char]34) }
            '\' { [void]$builder.Append([char]92) }
            '/' { [void]$builder.Append([char]47) }
            'b' { [void]$builder.Append([char]8) }
            'f' { [void]$builder.Append([char]12) }
            'n' { [void]$builder.Append([char]10) }
            'r' { [void]$builder.Append([char]13) }
            't' { [void]$builder.Append([char]9) }
            'u' {
                if ($State.Index + 4 -gt $State.Text.Length) {
                    return $false
                }
                $hex = $State.Text.Substring($State.Index, 4)
                if ($hex -cnotmatch '^[0-9A-Fa-f]{4}$') {
                    return $false
                }
                [void]$builder.Append([char][System.Convert]::ToInt32($hex, 16))
                $State.Index += 4
            }
            default { return $false }
        }
    }
    return $false
}

function Read-JsonValueStrict {
    param([Parameter(Mandatory = $true)][object]$State)
    Skip-JsonWhitespace -State $State
    if ($State.Index -ge $State.Text.Length) {
        return $false
    }
    $character = $State.Text[$State.Index]
    if ($character -eq [char]34) {
        $unused = ''
        return Read-JsonStringStrict -State $State -Value ([ref]$unused)
    }
    if ($character -eq [char]123) {
        $State.Index++
        $keys = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
        Skip-JsonWhitespace -State $State
        if ($State.Index -lt $State.Text.Length -and $State.Text[$State.Index] -eq [char]125) {
            $State.Index++
            return $true
        }
        while ($State.Index -lt $State.Text.Length) {
            $key = ''
            if (-not (Read-JsonStringStrict -State $State -Value ([ref]$key))) {
                return $false
            }
            if (-not $keys.Add($key)) {
                $State.Duplicate = $true
            }
            Skip-JsonWhitespace -State $State
            if ($State.Index -ge $State.Text.Length -or $State.Text[$State.Index] -ne [char]58) {
                return $false
            }
            $State.Index++
            if (-not (Read-JsonValueStrict -State $State)) {
                return $false
            }
            Skip-JsonWhitespace -State $State
            if ($State.Index -ge $State.Text.Length) {
                return $false
            }
            if ($State.Text[$State.Index] -eq [char]125) {
                $State.Index++
                return $true
            }
            if ($State.Text[$State.Index] -ne [char]44) {
                return $false
            }
            $State.Index++
            Skip-JsonWhitespace -State $State
        }
        return $false
    }
    if ($character -eq [char]91) {
        $State.Index++
        Skip-JsonWhitespace -State $State
        if ($State.Index -lt $State.Text.Length -and $State.Text[$State.Index] -eq [char]93) {
            $State.Index++
            return $true
        }
        while ($State.Index -lt $State.Text.Length) {
            if (-not (Read-JsonValueStrict -State $State)) {
                return $false
            }
            Skip-JsonWhitespace -State $State
            if ($State.Index -ge $State.Text.Length) {
                return $false
            }
            if ($State.Text[$State.Index] -eq [char]93) {
                $State.Index++
                return $true
            }
            if ($State.Text[$State.Index] -ne [char]44) {
                return $false
            }
            $State.Index++
            Skip-JsonWhitespace -State $State
        }
        return $false
    }
    foreach ($literal in @('true', 'false', 'null')) {
        if ($State.Index + $literal.Length -le $State.Text.Length -and
            $State.Text.Substring($State.Index, $literal.Length) -ceq $literal) {
            $State.Index += $literal.Length
            return $true
        }
    }
    $numberMatch = [System.Text.RegularExpressions.Regex]::Match(
        $State.Text.Substring($State.Index),
        '^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?'
    )
    if ($numberMatch.Success) {
        $State.Index += $numberMatch.Length
        return $true
    }
    return $false
}

function Test-StrictJsonDocument {
    param(
        [AllowEmptyString()][string]$Text,
        [Parameter(Mandatory = $true)][ref]$HasDuplicate
    )
    $state = [pscustomobject]@{ Text = $Text; Index = 0; Duplicate = $false }
    $valid = Read-JsonValueStrict -State $state
    Skip-JsonWhitespace -State $state
    $HasDuplicate.Value = $state.Duplicate
    return $valid -and $state.Index -eq $state.Text.Length
}

function Get-JsonPropertyNames {
    param([Parameter(Mandatory = $true)][object]$Object)
    return @($Object.PSObject.Properties | ForEach-Object { $_.Name })
}

function Test-ExactJsonProperties {
    param(
        [Parameter(Mandatory = $true)][object]$Object,
        [Parameter(Mandatory = $true)][string[]]$Expected
    )

    $actual = @(Get-JsonPropertyNames -Object $Object)
    if ($actual.Count -ne $Expected.Count) {
        return $false
    }
    foreach ($name in $Expected) {
        if ($actual -cnotcontains $name) {
            return $false
        }
    }
    return $true
}

function Test-ManifestRelativePath {
    param([AllowEmptyString()][string]$Path)

    if ([string]::IsNullOrWhiteSpace($Path) -or $Path.Contains([char]92) -or $Path.StartsWith('/') -or
        [System.IO.Path]::IsPathRooted($Path)) {
        return $false
    }
    foreach ($segment in $Path.Split([char]47)) {
        if ($segment -eq '' -or $segment -eq '.' -or $segment -eq '..') {
            return $false
        }
    }
    return $true
}

function Get-CanonicalMaturityStatuses {
    param([AllowEmptyString()][string]$Text)
    $statuses = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
    foreach ($status in @('stable', 'experimental', 'unverified', 'planned', 'excluded')) {
        $pattern = '(?i)(?<![A-Za-z0-9_-])' + $status + '(?![A-Za-z0-9_-])'
        if ([System.Text.RegularExpressions.Regex]::IsMatch($Text, $pattern)) {
            [void]$statuses.Add($status)
        }
    }
    if ([System.Text.RegularExpressions.Regex]::IsMatch($Text, '\u5DF2\u7A33\u5B9A|\u7A33\u5B9A\u7248|\u751F\u4EA7\u5C31\u7EEA|\u751F\u4EA7\u53EF\u7528|\u751F\u4EA7\u7EA7')) {
        [void]$statuses.Add('stable')
    }
    if ([System.Text.RegularExpressions.Regex]::IsMatch($Text, '\u5B9E\u9A8C\u6027|\u8BD5\u9A8C\u6027')) {
        [void]$statuses.Add('experimental')
    }
    if ([System.Text.RegularExpressions.Regex]::IsMatch($Text, '\u672A\u9A8C\u8BC1|\u672A\u7ECF\u9A8C\u8BC1')) {
        [void]$statuses.Add('unverified')
    }
    if ([System.Text.RegularExpressions.Regex]::IsMatch($Text, '\u8BA1\u5212\u4E2D|\u89C4\u5212\u4E2D')) {
        [void]$statuses.Add('planned')
    }
    if ([System.Text.RegularExpressions.Regex]::IsMatch($Text, '\u5DF2\u6392\u9664|\u4E0D\u9002\u7528')) {
        [void]$statuses.Add('excluded')
    }
    return ,$statuses
}

function ConvertTo-MaturityScalarText {
    param(
        [AllowEmptyString()][string]$Text,
        [switch]$FormatAsBoundary
    )

    $builder = New-Object System.Text.StringBuilder
    $index = 0
    while ($index -lt $Text.Length) {
        $length = 1
        if ([System.Char]::IsHighSurrogate($Text[$index]) -and ($index + 1) -lt $Text.Length -and
            [System.Char]::IsLowSurrogate($Text[$index + 1])) {
            $length = 2
        }
        try {
            $category = [System.Globalization.CharUnicodeInfo]::GetUnicodeCategory($Text, $index)
        } catch {
            $category = [System.Globalization.UnicodeCategory]::Surrogate
        }
        $isVariationSelector = $false
        if ($category -eq [System.Globalization.UnicodeCategory]::NonSpacingMark) {
            $codePoint = if ($length -eq 2) {
                [System.Char]::ConvertToUtf32($Text, $index)
            } else {
                [int]$Text[$index]
            }
            # Unicode Variation_Selector consists of the Mongolian free variation
            # selectors, the BMP variation selectors, and the supplementary set.
            $isVariationSelector = ($codePoint -ge 0x180B -and $codePoint -le 0x180D) -or
                $codePoint -eq 0x180F -or
                ($codePoint -ge 0xFE00 -and $codePoint -le 0xFE0F) -or
                ($codePoint -ge 0xE0100 -and $codePoint -le 0xE01EF)
        }
        if ($category -eq [System.Globalization.UnicodeCategory]::Format -or
            $isVariationSelector) {
            if ($FormatAsBoundary) { [void]$builder.Append(' ') }
        } else {
            [void]$builder.Append($Text.Substring($index, $length))
        }
        $index += $length
    }
    return $builder.ToString()
}

function ConvertFrom-MaturityHtmlEntities {
    param(
        [AllowEmptyString()][string]$Text,
        [switch]$EntityAsBoundary
    )

    if (-not $EntityAsBoundary) {
        return [System.Net.WebUtility]::HtmlDecode($Text)
    }
    $pattern = [System.Text.RegularExpressions.Regex]::new('&(?:#[0-9]{1,7}|#x[0-9A-Fa-f]{1,6}|[A-Za-z][A-Za-z0-9]{1,31});')
    $builder = New-Object System.Text.StringBuilder
    $cursor = 0
    foreach ($match in $pattern.Matches($Text)) {
        [void]$builder.Append($Text.Substring($cursor, $match.Index - $cursor))
        $decoded = [System.Net.WebUtility]::HtmlDecode($match.Value)
        if ($decoded -ceq $match.Value) {
            [void]$builder.Append($match.Value)
        } else {
            [void]$builder.Append(' ')
            [void]$builder.Append($decoded)
            [void]$builder.Append(' ')
        }
        $cursor = $match.Index + $match.Length
    }
    [void]$builder.Append($Text.Substring($cursor))
    return $builder.ToString()
}

function Remove-MaturityHtmlMarkup {
    param([AllowEmptyString()][string]$Text)

    $visible = New-Object System.Text.StringBuilder
    $boundary = New-Object System.Text.StringBuilder
    $index = 0
    while ($index -lt $Text.Length) {
        if ($Text[$index] -ne [char]60) {
            [void]$visible.Append($Text[$index])
            [void]$boundary.Append($Text[$index])
            $index++
            continue
        }
        if (($index + 4) -le $Text.Length -and $Text.Substring($index, 4) -ceq '<!--') {
            $commentEnd = $Text.IndexOf('-->', $index + 4, [System.StringComparison]::Ordinal)
            if ($commentEnd -ge 0) {
                [void]$boundary.Append(' ')
                $index = $commentEnd + 3
                continue
            }
        }
        $remaining = $Text.Substring($index)
        if ([System.Text.RegularExpressions.Regex]::IsMatch($remaining, '^<[A-Za-z][A-Za-z0-9+.-]*://') -or
            [System.Text.RegularExpressions.Regex]::IsMatch($remaining, '^<[A-Za-z0-9.!#$%&''*+/=?^_`{|}~-]+@[A-Za-z0-9.-]+>')) {
            [void]$visible.Append($Text[$index])
            [void]$boundary.Append($Text[$index])
            $index++
            continue
        }
        $cursor = $index + 1
        if ($cursor -lt $Text.Length -and $Text[$cursor] -eq [char]47) { $cursor++ }
        if ($cursor -ge $Text.Length -or -not [System.Char]::IsLetter($Text[$cursor])) {
            [void]$visible.Append($Text[$index])
            [void]$boundary.Append($Text[$index])
            $index++
            continue
        }
        $cursor++
        while ($cursor -lt $Text.Length -and $Text[$cursor] -match '[A-Za-z0-9:-]') { $cursor++ }
        if ($cursor -lt $Text.Length -and -not ([System.Char]::IsWhiteSpace($Text[$cursor]) -or
            $Text[$cursor] -eq [char]47 -or $Text[$cursor] -eq [char]62)) {
            [void]$visible.Append($Text[$index])
            [void]$boundary.Append($Text[$index])
            $index++
            continue
        }
        $quote = [char]0
        $endTag = -1
        for ($scan = $cursor; $scan -lt $Text.Length; $scan++) {
            $character = $Text[$scan]
            if ($quote -ne [char]0) {
                if ($character -eq $quote) { $quote = [char]0 }
                continue
            }
            if ($character -eq [char]34 -or $character -eq [char]39) {
                $quote = $character
                continue
            }
            if ($character -eq [char]60) { break }
            if ($character -eq [char]62) { $endTag = $scan; break }
        }
        if ($endTag -lt 0 -or $quote -ne [char]0) {
            [void]$visible.Append($Text[$index])
            [void]$boundary.Append($Text[$index])
            $index++
            continue
        }
        [void]$boundary.Append(' ')
        $index = $endTag + 1
    }
    return [pscustomobject]@{ Visible = $visible.ToString(); Boundary = $boundary.ToString() }
}

function Get-MaturitySemanticContent {
    param([AllowEmptyString()][string]$Line)

    $fragments = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
    $withoutCode = [System.Text.RegularExpressions.Regex]::Replace($Line, '`+[^`]*`+', ' ')
    $linkPattern = [System.Text.RegularExpressions.Regex]::new('(?<image>!)?\[(?<label>[^\]\r\n]*)\]\((?<body><[^>\r\n]+>|[^)\r\n]+)\)')
    $markdownBuilder = New-Object System.Text.StringBuilder
    $cursor = 0
    foreach ($match in $linkPattern.Matches($withoutCode)) {
        [void]$markdownBuilder.Append($withoutCode.Substring($cursor, $match.Index - $cursor))
        if ($match.Groups['image'].Success) {
            [void]$markdownBuilder.Append(' ')
        } else {
            [void]$markdownBuilder.Append($match.Groups['label'].Value)
        }
        $body = $match.Groups['body'].Value.Trim()
        $destination = if ($body.StartsWith('<', [System.StringComparison]::Ordinal) -and $body.EndsWith('>', [System.StringComparison]::Ordinal)) {
            $body.Substring(1, $body.Length - 2)
        } else {
            $destinationMatch = [System.Text.RegularExpressions.Regex]::Match($body, '^\S+')
            if ($destinationMatch.Success) { $destinationMatch.Value } else { '' }
        }
        $hashIndex = $destination.IndexOf([char]35)
        if ($hashIndex -ge 0) {
            $baseDestination = $destination.Substring(0, $hashIndex)
            $fragment = $destination.Substring($hashIndex + 1)
            if ($baseDestination -notmatch '^[A-Za-z][A-Za-z0-9+.-]*:' -and $fragment -match '^[a-z][a-z0-9-]*$') {
                [void]$fragments.Add($fragment)
            }
        }
        $cursor = $match.Index + $match.Length
    }
    [void]$markdownBuilder.Append($withoutCode.Substring($cursor))

    $html = Remove-MaturityHtmlMarkup -Text $markdownBuilder.ToString()
    $visible = ConvertFrom-MaturityHtmlEntities -Text $html.Visible
    $boundary = ConvertFrom-MaturityHtmlEntities -Text $html.Boundary -EntityAsBoundary
    $visible = [System.Text.RegularExpressions.Regex]::Replace($visible, '(?i)\b(?:https?://|mailto:)\S+', ' ')
    $boundary = [System.Text.RegularExpressions.Regex]::Replace($boundary, '(?i)\b(?:https?://|mailto:)\S+', ' ')
    $visible = [System.Text.RegularExpressions.Regex]::Replace($visible, '\\([\\`*_{}\[\]()#+.!~-])', '$1')
    $boundary = [System.Text.RegularExpressions.Regex]::Replace($boundary, '\\([\\`*_{}\[\]()#+.!~-])', '$1')
    $visible = $visible.Replace('*', '').Replace('~', '')
    # CommonMark does not treat an underscore inside an alphanumeric word as
    # emphasis. Preserve identifiers such as NO_RAG while still removing
    # formatting delimiters such as _stable_.
    $visible = [System.Text.RegularExpressions.Regex]::Replace(
        $visible,
        '(?<![\p{L}\p{M}\p{Nd}])_|_(?![\p{L}\p{M}\p{Nd}])',
        ''
    )
    $boundary = [System.Text.RegularExpressions.Regex]::Replace($boundary, '[*_~]', ' ')
    $visible = ConvertTo-MaturityScalarText -Text $visible
    $boundary = ConvertTo-MaturityScalarText -Text $boundary -FormatAsBoundary
    return [pscustomobject]@{ VisibleText = $visible; BoundaryText = $boundary; Fragments = @($fragments) }
}

function ConvertTo-MaturityVisibleText {
    param([AllowEmptyString()][string]$Line)
    return (Get-MaturitySemanticContent -Line $Line).VisibleText
}

function Test-MaturityIdentityPresent {
    param(
        [AllowEmptyString()][string]$VisibleText,
        [string[]]$Fragments = @(),
        [Parameter(Mandatory = $true)][object]$Capability
    )

    $idPattern = '(?<![\p{L}\p{M}\p{Nd}_.-])' + [System.Text.RegularExpressions.Regex]::Escape($Capability.Id) + '(?![\p{L}\p{M}\p{Nd}_.-])'
    if ([System.Text.RegularExpressions.Regex]::IsMatch($VisibleText, $idPattern)) { return $true }
    $titlePattern = '(?<![\p{L}\p{M}\p{Nd}_-])' + [System.Text.RegularExpressions.Regex]::Escape($Capability.Title) + '(?![\p{L}\p{M}\p{Nd}_-])'
    if ([System.Text.RegularExpressions.Regex]::IsMatch($VisibleText, $titlePattern)) { return $true }
    return $Fragments -ccontains [string]$Capability.Anchor
}

function Get-GoStructuralText {
    param(
        [AllowEmptyString()][string]$Text,
        [Parameter(Mandatory = $true)][ref]$Valid
    )

    $Valid.Value = $true
    $builder = New-Object System.Text.StringBuilder
    $state = 'code'
    $index = 0
    while ($index -lt $Text.Length) {
        $character = $Text[$index]
        $next = if ($index + 1 -lt $Text.Length) { $Text[$index + 1] } else { [char]0 }
        switch ($state) {
            'code' {
                if ($character -eq [char]47 -and $next -eq [char]47) {
                    [void]$builder.Append('  '); $index += 2; $state = 'line-comment'; continue
                }
                if ($character -eq [char]47 -and $next -eq [char]42) {
                    [void]$builder.Append('  '); $index += 2; $state = 'block-comment'; continue
                }
                if ($character -eq [char]34) { [void]$builder.Append(' '); $index++; $state = 'string'; continue }
                if ($character -eq [char]39) { [void]$builder.Append(' '); $index++; $state = 'rune'; continue }
                if ($character -eq [char]96) { [void]$builder.Append(' '); $index++; $state = 'raw-string'; continue }
                [void]$builder.Append($character); $index++; continue
            }
            'line-comment' {
                if ($character -eq [char]10 -or $character -eq [char]13) {
                    [void]$builder.Append($character); $state = 'code'
                } else { [void]$builder.Append(' ') }
                $index++; continue
            }
            'block-comment' {
                if ($character -eq [char]42 -and $next -eq [char]47) {
                    [void]$builder.Append('  '); $index += 2; $state = 'code'; continue
                }
                if ($character -eq [char]10 -or $character -eq [char]13) { [void]$builder.Append($character) } else { [void]$builder.Append(' ') }
                $index++; continue
            }
            'string' {
                if ($character -eq [char]92 -and $index + 1 -lt $Text.Length) {
                    [void]$builder.Append('  '); $index += 2; continue
                }
                if ($character -eq [char]34) { [void]$builder.Append(' '); $state = 'code' }
                elseif ($character -eq [char]10 -or $character -eq [char]13) { $Valid.Value = $false; return $builder.ToString() }
                else { [void]$builder.Append(' ') }
                $index++; continue
            }
            'rune' {
                if ($character -eq [char]92 -and $index + 1 -lt $Text.Length) {
                    [void]$builder.Append('  '); $index += 2; continue
                }
                if ($character -eq [char]39) { [void]$builder.Append(' '); $state = 'code' }
                elseif ($character -eq [char]10 -or $character -eq [char]13) { $Valid.Value = $false; return $builder.ToString() }
                else { [void]$builder.Append(' ') }
                $index++; continue
            }
            'raw-string' {
                if ($character -eq [char]96) { [void]$builder.Append(' '); $state = 'code' }
                elseif ($character -eq [char]10 -or $character -eq [char]13) { [void]$builder.Append($character) }
                else { [void]$builder.Append(' ') }
                $index++; continue
            }
        }
    }
    if ($state -notin @('code', 'line-comment')) { $Valid.Value = $false }
    return $builder.ToString()
}

function Test-GoTopLevelFunction {
    param(
        [Parameter(Mandatory = $true)][string]$StructuralText,
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][ref]$SignatureSeen
    )

    $SignatureSeen.Value = $false
    $namePattern = [System.Text.RegularExpressions.Regex]::Escape($Name)
    $candidateRegex = [System.Text.RegularExpressions.Regex]::new('(?m)(?<![A-Za-z0-9_])func\s+' + $namePattern + '\b')
    $goIdentifier = '[\p{L}_][\p{L}\p{Nd}_]*'
    $exactRegex = [System.Text.RegularExpressions.Regex]::new('(?m)(?<![A-Za-z0-9_])func\s+' + $namePattern + '\s*\(\s*' + $goIdentifier + '\s+\*\s*testing\s*\.\s*T\s*\)\s*\{')
    $exactStarts = [System.Collections.Generic.HashSet[int]]::new()
    foreach ($match in $exactRegex.Matches($StructuralText)) { [void]$exactStarts.Add($match.Index) }
    foreach ($candidate in $candidateRegex.Matches($StructuralText)) {
        $depth = 0
        $balanced = $true
        for ($index = 0; $index -lt $candidate.Index; $index++) {
            if ($StructuralText[$index] -eq [char]123) { $depth++ }
            elseif ($StructuralText[$index] -eq [char]125) { $depth--; if ($depth -lt 0) { $balanced = $false; break } }
        }
        if (-not $balanced -or $depth -ne 0) { continue }
        $SignatureSeen.Value = $true
        if ($exactStarts.Contains($candidate.Index)) { return $true }
    }
    return $false
}

function Test-ExternalToolReparsePoint {
    param([Parameter(Mandatory = $true)][string]$FullPath)

    $cursor = [System.IO.Path]::GetFullPath($FullPath)
    while (-not [string]::IsNullOrEmpty($cursor)) {
        try {
            $item = Get-Item -LiteralPath $cursor -Force -ErrorAction Stop
            if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) { return $true }
        } catch {
            return $true
        }
        $parent = [System.IO.Path]::GetDirectoryName($cursor.TrimEnd([char[]]@([char]92, [char]47)))
        if ([string]::IsNullOrEmpty($parent) -or [string]::Equals($parent, $cursor, $script:PathComparison)) { break }
        $cursor = $parent
    }
    return $false
}

function Resolve-GofmtExecutable {
    param([Parameter(Mandatory = $true)][string]$MatrixRelative)

    if ($script:GofmtResolutionAttempted) {
        if ($null -eq $script:ResolvedGofmtPath -and -not [string]::IsNullOrEmpty($script:GofmtResolutionRule)) {
            Add-DocViolation -Rule $script:GofmtResolutionRule -Path $MatrixRelative
        }
        return $script:ResolvedGofmtPath
    }
    $script:GofmtResolutionAttempted = $true
    $candidate = $null
    if (-not [string]::IsNullOrWhiteSpace($script:RequestedGofmtPath)) {
        if (-not [System.IO.Path]::IsPathRooted($script:RequestedGofmtPath)) {
            $script:GofmtResolutionRule = 'DOC_MATURITY_MATRIX_GO_FORMATTER_UNSAFE'
            Add-DocViolation -Rule $script:GofmtResolutionRule -Path $MatrixRelative
            return $null
        }
        $candidate = $script:RequestedGofmtPath
    } else {
        try {
            $command = Get-Command -Name 'gofmt' -CommandType Application -ErrorAction Stop | Select-Object -First 1
            if ($null -ne $command) {
                $candidate = if ($command.PSObject.Properties.Name -contains 'Source') { [string]$command.Source } else { [string]$command.Path }
            }
        } catch {}
    }
    if ([string]::IsNullOrWhiteSpace($candidate) -or -not [System.IO.Path]::IsPathRooted($candidate) -or
        -not (Test-Path -LiteralPath $candidate -PathType Leaf)) {
        $script:GofmtResolutionRule = 'DOC_MATURITY_MATRIX_GO_FORMATTER_UNAVAILABLE'
        Add-DocViolation -Rule $script:GofmtResolutionRule -Path $MatrixRelative
        return $null
    }
    try {
        $resolved = [System.IO.Path]::GetFullPath((Get-Item -LiteralPath $candidate -Force -ErrorAction Stop).FullName)
    } catch {
        $script:GofmtResolutionRule = 'DOC_MATURITY_MATRIX_GO_FORMATTER_UNAVAILABLE'
        Add-DocViolation -Rule $script:GofmtResolutionRule -Path $MatrixRelative
        return $null
    }
    $leaf = [System.IO.Path]::GetFileName($resolved)
    $validLeaf = if ($script:IsWindowsPlatform) { $leaf -ieq 'gofmt.exe' -or $leaf -ieq 'gofmt' } else { $leaf -ceq 'gofmt' }
    if (-not $validLeaf -or (Test-InRepository -FullPath $resolved) -or (Test-ExternalToolReparsePoint -FullPath $resolved)) {
        $script:GofmtResolutionRule = 'DOC_MATURITY_MATRIX_GO_FORMATTER_UNSAFE'
        Add-DocViolation -Rule $script:GofmtResolutionRule -Path $MatrixRelative
        return $null
    }
    $script:ResolvedGofmtPath = $resolved
    return $script:ResolvedGofmtPath
}

function Test-GoSourceParsesWithGofmt {
    param(
        [Parameter(Mandatory = $true)][string]$SourceFull,
        [Parameter(Mandatory = $true)][string]$MatrixRelative,
        [Parameter(Mandatory = $true)][ref]$FailureRule
    )

    $FailureRule.Value = $null
    $formatter = Resolve-GofmtExecutable -MatrixRelative $MatrixRelative
    if ($null -eq $formatter) {
        $FailureRule.Value = $script:GofmtResolutionRule
        return $false
    }
    if ($SourceFull.Contains([char]34)) {
        $FailureRule.Value = 'DOC_MATURITY_MATRIX_GO_TEST_SOURCE_INVALID'
        return $false
    }
    $process = New-Object System.Diagnostics.Process
    try {
        $startInfo = New-Object System.Diagnostics.ProcessStartInfo
        $startInfo.FileName = $formatter
        $startInfo.UseShellExecute = $false
        $startInfo.CreateNoWindow = $true
        $startInfo.RedirectStandardOutput = $true
        $startInfo.RedirectStandardError = $true
        if ($startInfo.PSObject.Properties.Name -contains 'ArgumentList' -and $null -ne $startInfo.ArgumentList) {
            [void]$startInfo.ArgumentList.Add('--')
            [void]$startInfo.ArgumentList.Add($SourceFull)
        } else {
            $startInfo.Arguments = '-- "' + $SourceFull + '"'
        }
        $process.StartInfo = $startInfo
        if (-not $process.Start()) {
            $FailureRule.Value = 'DOC_MATURITY_MATRIX_GO_FORMATTER_UNAVAILABLE'
            Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_GO_FORMATTER_UNAVAILABLE' -Path $MatrixRelative
            return $false
        }
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(10000)) {
            try { $process.Kill() } catch {}
            if (-not $process.WaitForExit(5000)) {
                $FailureRule.Value = 'DOC_MATURITY_MATRIX_GO_FORMATTER_TIMEOUT'
                Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_GO_FORMATTER_TIMEOUT' -Path $MatrixRelative
                return $false
            }
            [void]$stdoutTask.Result
            [void]$stderrTask.Result
            $FailureRule.Value = 'DOC_MATURITY_MATRIX_GO_FORMATTER_TIMEOUT'
            Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_GO_FORMATTER_TIMEOUT' -Path $MatrixRelative
            return $false
        }
        $process.WaitForExit()
        [void]$stdoutTask.Result
        [void]$stderrTask.Result
        if ($process.ExitCode -ne 0) {
            $FailureRule.Value = 'DOC_MATURITY_MATRIX_GO_TEST_SOURCE_INVALID'
            return $false
        }
        return $true
    } catch {
        $FailureRule.Value = 'DOC_MATURITY_MATRIX_GO_FORMATTER_UNAVAILABLE'
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_GO_FORMATTER_UNAVAILABLE' -Path $MatrixRelative
        return $false
    } finally {
        $process.Dispose()
    }
}

function Test-AstNodeAtScriptTopLevel {
    param(
        [Parameter(Mandatory = $true)][System.Management.Automation.Language.Ast]$Node,
        [Parameter(Mandatory = $true)][System.Management.Automation.Language.ScriptBlockAst]$RootAst
    )

    if ($Node -is [System.Management.Automation.Language.FunctionDefinitionAst]) {
        return $Node.Parent -is [System.Management.Automation.Language.NamedBlockAst] -and
            $Node.Parent.Parent -eq $RootAst
    }
    if ($Node -is [System.Management.Automation.Language.CommandAst]) {
        return $Node.Parent -is [System.Management.Automation.Language.PipelineAst] -and
            $Node.Parent.Parent -is [System.Management.Automation.Language.NamedBlockAst] -and
            $Node.Parent.Parent.Parent -eq $RootAst
    }
    return $false
}

function Test-AstCommandReachableAtScriptTopLevel {
    param(
        [Parameter(Mandatory = $true)][System.Management.Automation.Language.CommandAst]$Node,
        [Parameter(Mandatory = $true)][System.Management.Automation.Language.ScriptBlockAst]$RootAst
    )

    if (-not (Test-AstNodeAtScriptTopLevel -Node $Node -RootAst $RootAst)) { return $false }
    $pipeline = $Node.Parent
    $namedBlock = $pipeline.Parent
    foreach ($statement in @($namedBlock.Statements)) {
        if ([object]::ReferenceEquals($statement, $pipeline)) { return $true }
        if ($statement -is [System.Management.Automation.Language.ExitStatementAst] -or
            $statement -is [System.Management.Automation.Language.ReturnStatementAst] -or
            $statement -is [System.Management.Automation.Language.ThrowStatementAst]) {
            return $false
        }
    }
    return $false
}

function Test-GoMatrixEvidence {
    param(
        [Parameter(Mandatory = $true)][object]$Test,
        [Parameter(Mandatory = $true)][string]$MatrixRelative,
        [bool]$RequireDeclaration = $false
    )
    if (-not (Test-ExactJsonProperties -Object $Test -Expected @('kind', 'package', 'name')) -or
        -not ($Test.package -is [string]) -or -not ($Test.name -is [string])) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_TEST_SCHEMA' -Path $MatrixRelative
        return $false
    }
    $package = [string]$Test.package
    $name = [string]$Test.name
    if ($package -notmatch '^\./[^\\/].*$' -or $package.Contains([char]92) -or $package -match '(^|/)\.\.(/|$)' -or
        $name -notmatch '^Test[A-Za-z0-9_]+$') {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_TEST_VALUE' -Path $MatrixRelative
        return $false
    }
    if (-not $RequireDeclaration) {
        return $true
    }
    $packageRelative = $package.Substring(2)
    $packageFull = Join-Path $script:RootFull ($packageRelative.Replace([char]47, [System.IO.Path]::DirectorySeparatorChar))
    if (-not (Test-SafeFixedPath -FullPath $packageFull -RelativePath $packageRelative)) {
        return $false
    }
    $actualPackageFull = $null
    $packageState = Get-RepositoryRelativePathState -RelativePath $packageRelative -ActualFullPath ([ref]$actualPackageFull)
    if ($packageState -ceq 'case-mismatch') {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_TEST_TARGET_CASE_MISMATCH' -Path $MatrixRelative
        return $false
    }
    if ($packageState -cne 'exact' -or -not (Test-Path -LiteralPath $actualPackageFull -PathType Container)) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_TEST_TARGET_MISSING' -Path $MatrixRelative
        return $false
    }
    $packageFull = $actualPackageFull
    $sourceInvalid = $false
    $signatureInvalid = $false
    $buildConstrained = $false
    $foundExactDeclaration = $false
    foreach ($file in @(Get-ChildItem -LiteralPath $packageFull -File -Filter '*_test.go' -Force)) {
        $fileRelative = Get-RepositoryRelativePath -FullPath $file.FullName
        if (-not (Test-SafeFixedPath -FullPath $file.FullName -RelativePath $fileRelative)) {
            continue
        }
        if (-not (Test-ExactRepositoryCase -FullPath $file.FullName)) {
            Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_TEST_TARGET_CASE_MISMATCH' -Path $MatrixRelative
            return $false
        }
        try {
            $content = $script:Utf8Strict.GetString([System.IO.File]::ReadAllBytes($file.FullName))
        } catch {
            Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_TEST_READ_FAILED' -Path $MatrixRelative
            return $false
        }
        $gofmtFailureRule = $null
        if (-not (Test-GoSourceParsesWithGofmt -SourceFull $file.FullName -MatrixRelative $MatrixRelative -FailureRule ([ref]$gofmtFailureRule))) {
            if ($gofmtFailureRule -ceq 'DOC_MATURITY_MATRIX_GO_TEST_SOURCE_INVALID') {
                $sourceInvalid = $true
                continue
            }
            return $false
        }
        $structuralValid = $false
        $structural = Get-GoStructuralText -Text $content -Valid ([ref]$structuralValid)
        if (-not $structuralValid) {
            $sourceInvalid = $true
            continue
        }
        $braceDepth = 0
        foreach ($character in $structural.ToCharArray()) {
            if ($character -eq [char]123) { $braceDepth++ }
            elseif ($character -eq [char]125) { $braceDepth--; if ($braceDepth -lt 0) { break } }
        }
        if ($braceDepth -ne 0) {
            $sourceInvalid = $true
            continue
        }
        $candidateSignature = $false
        $exactDeclaration = Test-GoTopLevelFunction -StructuralText $structural -Name $name -SignatureSeen ([ref]$candidateSignature)
        if ($candidateSignature -and -not $exactDeclaration) { $signatureInvalid = $true }
        if (-not $exactDeclaration) { continue }

        $hasExplicitBuildConstraint = [System.Text.RegularExpressions.Regex]::IsMatch(
            $content,
            '(?m)^\s*//\s*(?:go:build|\+build)\b'
        )
        $hasImplicitPlatformConstraint = $file.Name -match '_(?:aix|android|darwin|dragonfly|freebsd|illumos|ios|js|linux|netbsd|openbsd|plan9|solaris|wasip1|windows|386|amd64|arm|arm64|loong64|mips|mips64|mips64le|ppc64|ppc64le|riscv64|s390x|wasm)_test\.go$'
        if ($hasExplicitBuildConstraint -or $hasImplicitPlatformConstraint) {
            $buildConstrained = $true
            continue
        }
        if ($exactDeclaration) { $foundExactDeclaration = $true }
    }
    if ($sourceInvalid) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_GO_TEST_SOURCE_INVALID' -Path $MatrixRelative
        return $false
    }
    if ($foundExactDeclaration) {
        return $true
    }
    if ($buildConstrained) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_GO_TEST_BUILD_CONSTRAINED' -Path $MatrixRelative
        return $false
    }
    if ($signatureInvalid) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_GO_TEST_SIGNATURE_INVALID' -Path $MatrixRelative
        return $false
    }
    Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_TEST_DECLARATION_MISSING' -Path $MatrixRelative
    return $false
}

function Test-PowerShellMatrixEvidence {
    param(
        [Parameter(Mandatory = $true)][object]$Test,
        [Parameter(Mandatory = $true)][string]$MatrixRelative,
        [bool]$RequireDeclaration = $false
    )
    if (-not (Test-ExactJsonProperties -Object $Test -Expected @('kind', 'path', 'name')) -or
        -not ($Test.path -is [string]) -or -not ($Test.name -is [string])) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_TEST_SCHEMA' -Path $MatrixRelative
        return $false
    }
    $relative = [string]$Test.path
    $name = [string]$Test.name
    if (-not (Test-ManifestRelativePath -Path $relative) -or $relative -notmatch '\.Tests\.ps1$' -or
        $name -notmatch '^[A-Za-z][A-Za-z0-9_-]*$') {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_TEST_VALUE' -Path $MatrixRelative
        return $false
    }
    if (-not $RequireDeclaration) {
        return $true
    }
    $full = Join-Path $script:RootFull ($relative.Replace([char]47, [System.IO.Path]::DirectorySeparatorChar))
    if (-not (Test-SafeFixedPath -FullPath $full -RelativePath $relative)) {
        return $false
    }
    $actualFull = $null
    $pathState = Get-RepositoryRelativePathState -RelativePath $relative -ActualFullPath ([ref]$actualFull)
    if ($pathState -ceq 'case-mismatch') {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_TEST_TARGET_CASE_MISMATCH' -Path $MatrixRelative
        return $false
    }
    if ($pathState -cne 'exact' -or -not (Test-Path -LiteralPath $actualFull -PathType Leaf)) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_TEST_TARGET_MISSING' -Path $MatrixRelative
        return $false
    }
    $full = $actualFull
    $tokens = $null
    $errors = $null
    $ast = [System.Management.Automation.Language.Parser]::ParseFile($full, [ref]$tokens, [ref]$errors)
    if (@($errors).Count -ne 0) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_TEST_READ_FAILED' -Path $MatrixRelative
        return $false
    }
    $definitions = @($ast.FindAll({
        param($node)
        $node -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -ceq $name -and
        (Test-AstNodeAtScriptTopLevel -Node $node -RootAst $ast)
    }, $true))
    if ($definitions.Count -eq 0) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_TEST_DECLARATION_MISSING' -Path $MatrixRelative
        return $false
    }
    if ($definitions.Count -ne 1) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_TEST_DECLARATION_AMBIGUOUS' -Path $MatrixRelative
        return $false
    }
    $invocations = @($ast.FindAll({
        param($node)
        if (-not ($node -is [System.Management.Automation.Language.CommandAst]) -or
            -not (Test-AstCommandReachableAtScriptTopLevel -Node $node -RootAst $ast)) {
            return $false
        }
        return $node.GetCommandName() -ceq $name
    }, $true))
    if ($invocations.Count -eq 0) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_TEST_INVOCATION_MISSING' -Path $MatrixRelative
        return $false
    }
    return $true
}

function Test-ExternalMatrixEvidence {
    param(
        [Parameter(Mandatory = $true)][AllowNull()][object]$Evidence,
        [Parameter(Mandatory = $true)][string]$MatrixRelative,
        [Parameter(Mandatory = $true)][string]$CapabilityId,
        [Parameter(Mandatory = $true)][ref]$CanonicalKey
    )
    $CanonicalKey.Value = $null
    if ($null -eq $Evidence -or -not (Test-ExactJsonProperties -Object $Evidence -Expected @('kind', 'receipt_path', 'sha256', 'source_url')) -or
        -not ($Evidence.kind -is [string]) -or -not ($Evidence.receipt_path -is [string]) -or
        -not ($Evidence.sha256 -is [string]) -or -not ($Evidence.source_url -is [string])) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_EVIDENCE_SCHEMA' -Path $MatrixRelative
        return $false
    }
    $kind = [string]$Evidence.kind
    $receiptRelative = [string]$Evidence.receipt_path
    $sha256 = [string]$Evidence.sha256
    $sourceUrl = [string]$Evidence.source_url
    $canonicalSource = $null
    if ($kind -cne 'repository-receipt-v1' -or -not (Test-ManifestRelativePath -Path $receiptRelative) -or
        -not $receiptRelative.StartsWith('testdata/release/external-evidence/', [System.StringComparison]::Ordinal) -or
        $receiptRelative -notmatch '\.receipt\.json$' -or (Test-IsRootGitPath -RelativePath $receiptRelative) -or
        $sha256 -cnotmatch '^[0-9a-f]{64}$' -or $sha256 -match '^([0-9a-f])\1{63}$' -or
        -not (Test-StrictExternalUri -Value $sourceUrl -AllowedSchemes @('https') -Canonical ([ref]$canonicalSource))) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_EVIDENCE_VALUE' -Path $MatrixRelative
        return $false
    }
    $receiptFull = Join-Path $script:RootFull ($receiptRelative.Replace([char]47, [System.IO.Path]::DirectorySeparatorChar))
    if (-not (Test-SafeFixedPath -FullPath $receiptFull -RelativePath $receiptRelative)) {
        return $false
    }
    $actualReceiptFull = $null
    $receiptState = Get-RepositoryRelativePathState -RelativePath $receiptRelative -ActualFullPath ([ref]$actualReceiptFull)
    if ($receiptState -ceq 'case-mismatch') {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_RECEIPT_CASE_MISMATCH' -Path $MatrixRelative
        return $false
    }
    if ($receiptState -ceq 'reparse') {
        Add-DocViolation -Rule 'DOC_FIXED_PATH_REPARSE' -Path $receiptRelative
        return $false
    }
    if ($receiptState -cne 'exact' -or -not (Test-Path -LiteralPath $actualReceiptFull -PathType Leaf)) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_RECEIPT_MISSING' -Path $MatrixRelative
        return $false
    }
    try {
        [byte[]]$receiptBytes = [System.IO.File]::ReadAllBytes($actualReceiptFull)
        $receiptText = $script:Utf8Strict.GetString($receiptBytes)
    } catch {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_RECEIPT_READ_FAILED' -Path $MatrixRelative
        return $false
    }
    $receiptHasDuplicate = $false
    if (-not (Test-StrictJsonDocument -Text $receiptText -HasDuplicate ([ref]$receiptHasDuplicate)) -or $receiptHasDuplicate) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_RECEIPT_JSON' -Path $MatrixRelative
        return $false
    }
    try {
        $receipt = $receiptText | ConvertFrom-Json
    } catch {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_RECEIPT_JSON' -Path $MatrixRelative
        return $false
    }
    if ($null -eq $receipt -or -not (Test-ExactJsonProperties -Object $receipt -Expected @(
        'schema_version', 'capability_id', 'source_url', 'result'
    )) -or -not ($receipt.schema_version -is [string]) -or -not ($receipt.capability_id -is [string]) -or
        -not ($receipt.source_url -is [string]) -or -not ($receipt.result -is [string]) -or
        [string]$receipt.schema_version -cne 'v1') {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_RECEIPT_SCHEMA' -Path $MatrixRelative
        return $false
    }
    if ([string]$receipt.capability_id -cne $CapabilityId -or [string]$receipt.source_url -cne $sourceUrl) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_RECEIPT_BINDING' -Path $MatrixRelative
        return $false
    }
    if ([string]$receipt.result -cne 'pass') {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_RECEIPT_RESULT' -Path $MatrixRelative
        return $false
    }
    $actualHash = Get-Sha256Hex -Bytes $receiptBytes
    if ($actualHash -cne $sha256) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_RECEIPT_HASH_MISMATCH' -Path $MatrixRelative
        return $false
    }
    $CanonicalKey.Value = $kind + [char]0 + $receiptRelative + [char]0 + $sha256 + [char]0 + $canonicalSource
    return $true
}

function Invoke-MaturityChecks {
    param(
        [Parameter(Mandatory = $true)][object]$MarkdownByFull,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][string[]]$P0Exemptions
    )

    $matrixRelative = 'testdata/release/capabilities.v1.json'
    $maturityRelative = 'docs/RELEASE_MATURITY.md'
    $claimsRelative = 'testdata/release/docs-maturity-claims.v1.json'
    $matrixFull = Join-Path $script:RootFull ($matrixRelative.Replace([char]47, [System.IO.Path]::DirectorySeparatorChar))
    $maturityFull = Join-Path $script:RootFull ($maturityRelative.Replace([char]47, [System.IO.Path]::DirectorySeparatorChar))
    $claimsFull = Join-Path $script:RootFull ($claimsRelative.Replace([char]47, [System.IO.Path]::DirectorySeparatorChar))

    $matrixPathSafe = Test-SafeFixedPath -FullPath $matrixFull -RelativePath $matrixRelative
    $maturityPathSafe = Test-SafeFixedPath -FullPath $maturityFull -RelativePath $maturityRelative
    $claimsPathSafe = Test-SafeFixedPath -FullPath $claimsFull -RelativePath $claimsRelative
    $unusedActual = $null
    $matrixPathState = Get-RepositoryRelativePathState -RelativePath $matrixRelative -ActualFullPath ([ref]$unusedActual)
    $maturityPathState = Get-RepositoryRelativePathState -RelativePath $maturityRelative -ActualFullPath ([ref]$unusedActual)
    $claimsPathState = Get-RepositoryRelativePathState -RelativePath $claimsRelative -ActualFullPath ([ref]$unusedActual)
    if ($matrixPathState -ceq 'case-mismatch') {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_CASE_MISMATCH' -Path $matrixRelative
        $matrixPathSafe = $false
    }
    if ($maturityPathState -ceq 'case-mismatch') {
        Add-DocViolation -Rule 'DOC_MATURITY_DOCUMENT_CASE_MISMATCH' -Path $maturityRelative
        $maturityPathSafe = $false
    }
    if ($claimsPathState -ceq 'case-mismatch') {
        Add-DocViolation -Rule 'DOC_MATURITY_CLAIMS_CASE_MISMATCH' -Path $claimsRelative
        $claimsPathSafe = $false
    }
    if ($matrixPathState -ceq 'reparse') {
        Add-DocViolation -Rule 'DOC_FIXED_PATH_REPARSE' -Path $matrixRelative
        $matrixPathSafe = $false
    }
    if ($maturityPathState -ceq 'reparse') {
        Add-DocViolation -Rule 'DOC_FIXED_PATH_REPARSE' -Path $maturityRelative
        $maturityPathSafe = $false
    }
    if ($claimsPathState -ceq 'reparse') {
        Add-DocViolation -Rule 'DOC_FIXED_PATH_REPARSE' -Path $claimsRelative
        $claimsPathSafe = $false
    }
    if (-not $matrixPathSafe -or -not $maturityPathSafe -or -not $claimsPathSafe) {
        return
    }

    if (-not (Test-Path -LiteralPath $matrixFull -PathType Leaf)) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_MISSING' -Path $matrixRelative
        return
    }
    if (-not (Test-Path -LiteralPath $maturityFull -PathType Leaf) -or -not $MarkdownByFull.ContainsKey($maturityFull)) {
        Add-DocViolation -Rule 'DOC_MATURITY_DOCUMENT_MISSING' -Path $maturityRelative
        return
    }

    $matrixDocument = Read-StrictUtf8 -FullPath $matrixFull -RelativePath $matrixRelative
    if ($null -eq $matrixDocument) {
        return
    }
    $matrixHasDuplicate = $false
    if (-not (Test-StrictJsonDocument -Text $matrixDocument.Text -HasDuplicate ([ref]$matrixHasDuplicate))) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_JSON' -Path $matrixRelative
        return
    }
    if ($matrixHasDuplicate) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_DUPLICATE_KEY' -Path $matrixRelative
        return
    }
    try {
        $matrix = $matrixDocument.Text | ConvertFrom-Json
    } catch {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_JSON' -Path $matrixRelative
        return
    }
    if ($null -eq $matrix -or -not (Test-ExactJsonProperties -Object $matrix -Expected @('schema_version', 'items')) -or
        -not ($matrix.schema_version -is [string]) -or [string]$matrix.schema_version -cne 'v1' -or
        -not ($matrix.items -is [System.Array])) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_SCHEMA' -Path $matrixRelative
        return
    }

    $allowedStatuses = @('stable', 'experimental', 'unverified', 'planned', 'excluded')
    $allowedOwners = @('core', 'first-party-module', 'extension-host', 'external-adapter', 'excluded')
    $capabilitiesById = [System.Collections.Generic.Dictionary[string,object]]::new([System.StringComparer]::Ordinal)
    $capabilitiesByAnchor = [System.Collections.Generic.Dictionary[string,object]]::new([System.StringComparer]::Ordinal)
    $capabilitiesByTitle = [System.Collections.Generic.Dictionary[string,object]]::new([System.StringComparer]::Ordinal)
    $matrixItems = @($matrix.items)
    if ($matrixItems.Count -eq 0) {
        Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_SCHEMA' -Path $matrixRelative
        return
    }
    foreach ($item in $matrixItems) {
        if ($null -eq $item -or -not (Test-ExactJsonProperties -Object $item -Expected @(
            'id', 'title', 'status', 'owner', 'tests', 'docs_anchor', 'external_evidence'
        ))) {
            Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_ITEM_SCHEMA' -Path $matrixRelative
            continue
        }
        if (-not ($item.id -is [string]) -or -not ($item.title -is [string]) -or
            -not ($item.status -is [string]) -or -not ($item.owner -is [string]) -or
            -not ($item.docs_anchor -is [string]) -or -not ($item.tests -is [System.Array]) -or
            -not ($item.external_evidence -is [System.Array])) {
            Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_ITEM_TYPE' -Path $matrixRelative
            continue
        }
        $id = [string]$item.id
        $title = [string]$item.title
        $status = [string]$item.status
        $owner = [string]$item.owner
        $anchor = [string]$item.docs_anchor
        $tests = @($item.tests)
        $externalEvidence = @($item.external_evidence)
        if ($id -notmatch '^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)+$' -or
            [string]::IsNullOrWhiteSpace($title) -or $allowedStatuses -cnotcontains $status -or
            $allowedOwners -cnotcontains $owner -or $anchor -notmatch '^[a-z][a-z0-9-]*$') {
            Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_VALUE' -Path $matrixRelative
            continue
        }
        if ($capabilitiesById.ContainsKey($id)) {
            Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_DUPLICATE_ID' -Path $matrixRelative
            continue
        }
        if ($capabilitiesByAnchor.ContainsKey($anchor)) {
            Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_DUPLICATE_ANCHOR' -Path $matrixRelative
            continue
        }
        if ($capabilitiesByTitle.ContainsKey($title)) {
            Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_DUPLICATE_TITLE' -Path $matrixRelative
            continue
        }
        if ($status -ceq 'stable' -and $tests.Count -eq 0) {
            Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_STABLE_WITHOUT_TEST' -Path $matrixRelative
        }
        if (($status -ceq 'excluded') -xor ($owner -ceq 'excluded')) {
            Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_STAGE_ADMISSION' -Path $matrixRelative
        }
        if ($status -ceq 'excluded' -and ($tests.Count -ne 0 -or $externalEvidence.Count -ne 0)) {
            Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_STAGE_ADMISSION' -Path $matrixRelative
        }
        $seenExternalEvidence = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
        $validExternalEvidenceCount = 0
        foreach ($evidence in $externalEvidence) {
            $evidenceKey = $null
            if (Test-ExternalMatrixEvidence -Evidence $evidence -MatrixRelative $matrixRelative -CapabilityId $id -CanonicalKey ([ref]$evidenceKey)) {
                $validExternalEvidenceCount++
                if (-not $seenExternalEvidence.Add($evidenceKey)) {
                    Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_EVIDENCE_DUPLICATE' -Path $matrixRelative
                }
            }
        }
        if ($status -ceq 'stable' -and $owner -ceq 'external-adapter' -and $validExternalEvidenceCount -eq 0) {
            Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_EXTERNAL_EVIDENCE_MISSING' -Path $matrixRelative
        }
        $seenTests = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
        foreach ($test in $tests) {
            if ($null -eq $test -or -not ($test.PSObject.Properties.Name -ccontains 'kind') -or
                -not ($test.kind -is [string])) {
                Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_TEST_SCHEMA' -Path $matrixRelative
                continue
            }
            $testValid = $false
            $testKey = $null
            switch ([string]$test.kind) {
                'go' {
                    $testValid = Test-GoMatrixEvidence -Test $test -MatrixRelative $matrixRelative -RequireDeclaration ($status -ceq 'stable')
                    if ($testValid) {
                        $testKey = 'go' + [char]0 + [string]$test.package + [char]0 + [string]$test.name
                    }
                }
                'powershell' {
                    $testValid = Test-PowerShellMatrixEvidence -Test $test -MatrixRelative $matrixRelative -RequireDeclaration ($status -ceq 'stable')
                    if ($testValid) {
                        $testKey = 'powershell' + [char]0 + [string]$test.path + [char]0 + [string]$test.name
                    }
                }
                default {
                    Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_TEST_KIND' -Path $matrixRelative
                }
            }
            if ($testValid -and -not $seenTests.Add($testKey)) {
                Add-DocViolation -Rule 'DOC_MATURITY_MATRIX_TEST_DUPLICATE' -Path $matrixRelative
            }
        }
        $record = [pscustomobject]@{ Id = $id; Title = $title; Status = $status; Owner = $owner; Anchor = $anchor }
        $capabilitiesById.Add($id, $record)
        $capabilitiesByAnchor.Add($anchor, $record)
        $capabilitiesByTitle.Add($title, $record)
    }

    $maturityDocument = $MarkdownByFull[$maturityFull]
    $seenMaturityAnchors = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
    for ($index = 0; $index -lt $maturityDocument.Lines.Count; $index++) {
        $anchorRecord = Get-ExplicitAnchorRecord -Line $maturityDocument.Lines[$index]
        if ($null -eq $anchorRecord) { continue }
        $anchor = [string]$anchorRecord.Value
        if (-not $anchorRecord.SyntaxValid -or $anchor -cnotmatch '^[a-z][a-z0-9-]*$') {
            Add-DocViolation -Rule 'DOC_MATURITY_ANCHOR_SYNTAX' -Path $maturityRelative -Line ($index + 1)
            continue
        }
        if (-not $capabilitiesByAnchor.ContainsKey($anchor)) {
            Add-DocViolation -Rule 'DOC_MATURITY_ORPHAN_ANCHOR' -Path $maturityRelative -Line ($index + 1)
            continue
        }
        if (-not $seenMaturityAnchors.Add($anchor)) {
            Add-DocViolation -Rule 'DOC_MATURITY_DUPLICATE_ANCHOR' -Path $maturityRelative -Line ($index + 1)
            continue
        }

        $headingIndex = $index + 1
        while ($headingIndex -lt $maturityDocument.Lines.Count -and [string]::IsNullOrWhiteSpace($maturityDocument.Lines[$headingIndex])) {
            $headingIndex++
        }
        if ($headingIndex -ge $maturityDocument.Lines.Count) {
            Add-DocViolation -Rule 'DOC_MATURITY_HEADING_MISSING' -Path $maturityRelative -Line ($index + 1)
            continue
        }
        $headingMatch = [System.Text.RegularExpressions.Regex]::Match(
            $maturityDocument.Lines[$headingIndex],
            '^#{1,6}\s+(?<title>.+?)\s+\[(?<status>stable|experimental|unverified|planned|excluded)\]\s*$'
        )
        if (-not $headingMatch.Success) {
            Add-DocViolation -Rule 'DOC_MATURITY_HEADING_FORMAT' -Path $maturityRelative -Line ($headingIndex + 1)
            continue
        }
        $expected = $capabilitiesByAnchor[$anchor]
        if ($headingMatch.Groups['title'].Value -cne $expected.Title) {
            Add-DocViolation -Rule 'DOC_MATURITY_TITLE_MISMATCH' -Path $maturityRelative -Line ($headingIndex + 1)
        }
        if ($headingMatch.Groups['status'].Value -cne $expected.Status) {
            Add-DocViolation -Rule 'DOC_MATURITY_STATUS_MISMATCH' -Path $maturityRelative -Line ($headingIndex + 1)
        }
    }
    foreach ($record in $capabilitiesById.Values) {
        if (-not $seenMaturityAnchors.Contains($record.Anchor)) {
            Add-DocViolation -Rule 'DOC_MATURITY_ENTRY_MISSING' -Path $maturityRelative
        }
    }

    if (-not (Test-Path -LiteralPath $claimsFull -PathType Leaf)) {
        Add-DocViolation -Rule 'DOC_MATURITY_CLAIMS_MISSING' -Path $claimsRelative
        return
    }
    $claimsDocument = Read-StrictUtf8 -FullPath $claimsFull -RelativePath $claimsRelative
    if ($null -eq $claimsDocument) {
        return
    }
    $claimsHasDuplicate = $false
    if (-not (Test-StrictJsonDocument -Text $claimsDocument.Text -HasDuplicate ([ref]$claimsHasDuplicate))) {
        Add-DocViolation -Rule 'DOC_MATURITY_CLAIMS_JSON' -Path $claimsRelative
        return
    }
    if ($claimsHasDuplicate) {
        Add-DocViolation -Rule 'DOC_MATURITY_CLAIMS_DUPLICATE_KEY' -Path $claimsRelative
        return
    }
    try {
        $manifest = $claimsDocument.Text | ConvertFrom-Json
    } catch {
        Add-DocViolation -Rule 'DOC_MATURITY_CLAIMS_JSON' -Path $claimsRelative
        return
    }
    if ($null -eq $manifest -or -not (Test-ExactJsonProperties -Object $manifest -Expected @('schema_version', 'entries')) -or
        -not ($manifest.schema_version -is [string]) -or [string]$manifest.schema_version -cne 'v1' -or
        -not ($manifest.entries -is [System.Array])) {
        Add-DocViolation -Rule 'DOC_MATURITY_CLAIMS_SCHEMA' -Path $claimsRelative
        return
    }

    $triggerRegex = [System.Text.RegularExpressions.Regex]::new(
        '(?i)(?<![\p{L}\p{M}\p{Nd}_-])(?:stable|experimental|unverified|planned|excluded)(?![\p{L}\p{M}\p{Nd}_-])|production[- ](?:ready|grade)|\u751F\u4EA7\u5C31\u7EEA|\u751F\u4EA7\u53EF\u7528|\u751F\u4EA7\u7EA7|\u5DF2\u7A33\u5B9A|\u7A33\u5B9A\u7248|\u5B9E\u9A8C\u6027|\u8BD5\u9A8C\u6027|\u672A\u9A8C\u8BC1|\u672A\u7ECF\u9A8C\u8BC1|\u8BA1\u5212\u4E2D|\u89C4\u5212\u4E2D|\u5DF2\u6392\u9664|\u4E0D\u9002\u7528'
    )
    $policyNegativeRegex = [System.Text.RegularExpressions.Regex]::new(
        '(?i)^\s*(?:[-*]\s*)?(?:(?:policy|rule)\s*:\s*)?(?:do\s+not\s+(?:mark|call|claim|describe)\s+[^,;.!?]+?\s+(?:as\s+)?(?:stable|production[- ](?:ready|grade))|[^,;.!?]+?\s+(?:is|are|remains?)\s+not\s+(?:stable|production[- ](?:ready|grade)))\s*[.!]?\s*$|^\s*(?:[-*]\s*)?(?:(?:\u7B56\u7565|\u89C4\u5219)\s*[:\uFF1A]\s*)?(?:[^,\uFF0C;\uFF1B\u3002\uFF01\uFF1F]+?(?:\u5C1A\u672A|\u5E76\u975E|\u4E0D\u662F|\u4E0D\u4EE3\u8868)(?:\u5DF2\u7A33\u5B9A|\u7A33\u5B9A\u7248|\u751F\u4EA7\u5C31\u7EEA|\u751F\u4EA7\u53EF\u7528|\u751F\u4EA7\u7EA7)|(?:\u4E0D\u5F97|\u7981\u6B62)(?:\u5C06|\u628A)?[^,\uFF0C;\uFF1B\u3002\uFF01\uFF1F]+?(?:\u6807\u8BB0\u4E3A|\u79F0\u4E3A|\u8BA4\u5B9A\u4E3A)(?:\u5DF2\u7A33\u5B9A|\u7A33\u5B9A\u7248|\u751F\u4EA7\u5C31\u7EEA|\u751F\u4EA7\u53EF\u7528|\u751F\u4EA7\u7EA7))\s*[\u3002\uFF01]?\s*$'
    )
    $policyDefinitionRegex = [System.Text.RegularExpressions.Regex]::new(
        '(?i)^\s*(?:[-*]\s*)?(?:(?:definition|term)\s*:\s*)?(?:status\s+)?(?:stable|experimental|unverified|planned|excluded|production[- ](?:ready|grade))\s+(?:means|is\s+defined\s+as|denotes)\s+[^;.!?]+[.]?\s*$|^\s*(?:[-*]\s*)?(?:(?:\u5B9A\u4E49|\u672F\u8BED)\s*[:\uFF1A]\s*)?(?:\u5DF2\u7A33\u5B9A|\u7A33\u5B9A\u7248|\u751F\u4EA7\u5C31\u7EEA|\u751F\u4EA7\u53EF\u7528|\u751F\u4EA7\u7EA7|\u5B9E\u9A8C\u6027|\u672A\u9A8C\u8BC1|\u8BA1\u5212\u4E2D|\u5DF2\u6392\u9664)(?:\u8868\u793A|\u5B9A\u4E49\u4E3A|\u662F\u6307)[^;\uFF1B\u3002\uFF01\uFF1F]+[\u3002]?\s*$'
    )
    $policyRuleRegex = [System.Text.RegularExpressions.Regex]::new(
        '(?i)^\s*(?:[-*]\s*)?(?:policy|rule)\s*:\s*[^,;.!?]+?\s+(?:(?:must|shall)\s+not|cannot)\s+(?:be\s+)?(?:(?:marked|called|described)\s+as\s+)?(?:stable|production[- ](?:ready|grade))\s*[.]?\s*$|^\s*(?:[-*]\s*)?(?:\u7B56\u7565|\u89C4\u5219)\s*[:\uFF1A]\s*(?:\u4E0D\u5F97|\u7981\u6B62)(?:\u5C06|\u628A)?[^,\uFF0C;\uFF1B\u3002\uFF01\uFF1F]+?(?:\u6807\u8BB0\u4E3A|\u79F0\u4E3A|\u8BA4\u5B9A\u4E3A)(?:\u5DF2\u7A33\u5B9A|\u7A33\u5B9A\u7248|\u751F\u4EA7\u5C31\u7EEA|\u751F\u4EA7\u53EF\u7528|\u751F\u4EA7\u7EA7)\s*[\u3002\uFF01]?\s*$'
    )
    $policyPositiveAssertionRegex = [System.Text.RegularExpressions.Regex]::new(
        '(?i)\b(?:is|are|was|were|remains?|became|becomes?|has\s+become)\s+(?!not\b)(?:fully\s+)?(?:stable|production[- ](?:ready|grade))\b|(?:\u5DF2\u7ECF|\u73B0\u5DF2|\u5F53\u524D|\u76EE\u524D|\u5DF2)(?:\u8FBE\u5230|\u5904\u4E8E|\u6210\u4E3A|\u662F|\u4E3A)?(?:\u7A33\u5B9A\u7248|\u751F\u4EA7\u5C31\u7EEA|\u751F\u4EA7\u53EF\u7528|\u751F\u4EA7\u7EA7)|(?<!\u6807\u8BB0)(?<!\u79F0)(?<!\u8BA4\u5B9A)(?<!\u5B9A\u4E49)(?:\u662F|\u4E3A|\u8FBE\u5230|\u5904\u4E8E|\u6210\u4E3A)(?:\u7A33\u5B9A\u7248|\u751F\u4EA7\u5C31\u7EEA|\u751F\u4EA7\u53EF\u7528|\u751F\u4EA7\u7EA7)|(?<!\u5C1A\u672A)(?<!\u5E76\u975E)(?<!\u4E0D\u662F)(?<!\u4E0D\u4EE3\u8868)(?<!\u6807\u8BB0\u4E3A)(?<!\u79F0\u4E3A)(?<!\u8BA4\u5B9A\u4E3A)(?<!\u672A)(?<!\u4E0D)(?<!\u975E)\u5DF2\u7A33\u5B9A(?!\s*(?:\u8868\u793A|\u5B9A\u4E49\u4E3A|\u662F\u6307))'
    )
    $triggerByKey = [System.Collections.Generic.Dictionary[string,object]]::new([System.StringComparer]::Ordinal)
    foreach ($pair in $MarkdownByFull.GetEnumerator()) {
        $relative = $pair.Value.RelativePath
        if ($P0Exemptions -ccontains $relative) {
            continue
        }
        # This exact root document contains verbatim third-party license text.
        # Exempt it only from product-maturity semantics; all other Docs checks
        # and the independent License gate continue to validate the file.
        if ($relative -ceq 'THIRD_PARTY_NOTICES.md') {
            continue
        }
        for ($index = 0; $index -lt $pair.Value.Lines.Count; $index++) {
            $line = $pair.Value.Lines[$index]
            $semanticLine = Get-MaturitySemanticContent -Line $line
            $visibleLine = $semanticLine.VisibleText
            if (-not $triggerRegex.IsMatch($visibleLine)) {
                continue
            }
            if (-not $triggerRegex.IsMatch($semanticLine.BoundaryText)) {
                Add-DocViolation -Rule 'DOC_MATURITY_TEXT_OBFUSCATED' -Path $relative -Line ($index + 1)
            }
            $hash = Get-LineSha256 -Line $line
            $key = $relative + [char]0 + $hash
            if ($triggerByKey.ContainsKey($key)) {
                Add-DocViolation -Rule 'DOC_MATURITY_TRIGGER_AMBIGUOUS' -Path $relative -Line ($index + 1)
            } else {
                $triggerByKey.Add($key, [pscustomobject]@{
                    Path = $relative
                    LineNumber = $index + 1
                    Text = $visibleLine
                    Fragments = @($semanticLine.Fragments)
                    Hash = $hash
                    Used = $false
                })
            }
        }
    }

    $previousSortKey = $null
    $entryKeys = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
    foreach ($entry in @($manifest.entries)) {
        if ($null -eq $entry -or -not (Test-ExactJsonProperties -Object $entry -Expected @('path', 'line_sha256', 'kind', 'claims', 'policy', 'reason'))) {
            Add-DocViolation -Rule 'DOC_MATURITY_CLAIM_ENTRY_SCHEMA' -Path $claimsRelative
            continue
        }
        if (-not ($entry.path -is [string]) -or -not ($entry.line_sha256 -is [string]) -or
            -not ($entry.kind -is [string]) -or -not ($entry.claims -is [System.Array]) -or
            -not ($entry.reason -is [string])) {
            Add-DocViolation -Rule 'DOC_MATURITY_CLAIM_ENTRY_TYPE' -Path $claimsRelative
            continue
        }
        $entryPath = [string]$entry.path
        $entryHash = [string]$entry.line_sha256
        $kind = [string]$entry.kind
        $reason = [string]$entry.reason
        if (-not (Test-ManifestRelativePath -Path $entryPath) -or $entryHash -cnotmatch '^[0-9a-f]{64}$' -or
            $kind -cnotin @('CAPABILITY', 'POLICY') -or [string]::IsNullOrWhiteSpace($reason)) {
            Add-DocViolation -Rule 'DOC_MATURITY_CLAIM_ENTRY_VALUE' -Path $claimsRelative
            continue
        }

        $sortKey = $entryPath + [char]0 + $entryHash
        if ($null -ne $previousSortKey -and $script:Ordinal.Compare($previousSortKey, $sortKey) -ge 0) {
            Add-DocViolation -Rule 'DOC_MATURITY_CLAIMS_NOT_SORTED' -Path $claimsRelative
        }
        $previousSortKey = $sortKey
        if (-not $entryKeys.Add($sortKey)) {
            Add-DocViolation -Rule 'DOC_MATURITY_CLAIM_DUPLICATE' -Path $claimsRelative
            continue
        }
        if (-not $triggerByKey.ContainsKey($sortKey)) {
            Add-DocViolation -Rule 'DOC_MATURITY_CLAIM_STALE' -Path $entryPath
            continue
        }
        $trigger = $triggerByKey[$sortKey]
        $trigger.Used = $true
        $claims = @($entry.claims)

        if ($kind -ceq 'POLICY') {
            if ($claims.Count -ne 0) {
                Add-DocViolation -Rule 'DOC_MATURITY_POLICY_HAS_CLAIMS' -Path $entryPath -Line $trigger.LineNumber
            }
            if ($null -eq $entry.policy -or -not (Test-ExactJsonProperties -Object $entry.policy -Expected @('syntax', 'subject')) -or
                -not ($entry.policy.syntax -is [string]) -or -not ($entry.policy.subject -is [string]) -or
                [string]$entry.policy.subject -cne 'MATURITY') {
                Add-DocViolation -Rule 'DOC_MATURITY_POLICY_SCHEMA' -Path $entryPath -Line $trigger.LineNumber
                continue
            }
            $policySyntax = [string]$entry.policy.syntax
            $policyMatches = switch ($policySyntax) {
                'NEGATION' { $policyNegativeRegex.IsMatch($trigger.Text) }
                'DEFINITION' { $policyDefinitionRegex.IsMatch($trigger.Text) }
                'RULE' { $policyRuleRegex.IsMatch($trigger.Text) }
                default { $false }
            }
            $policyWithoutTerminal = [System.Text.RegularExpressions.Regex]::Replace(
                $trigger.Text.Trim(),
                '[.!?\u3002\uFF01\uFF1F]\s*$',
                ''
            )
            if ([System.Text.RegularExpressions.Regex]::IsMatch($policyWithoutTerminal, '[;\uFF1B\u3002\uFF01\uFF1F]|[.!?]\s+')) {
                $policyMatches = $false
            }
            if ($policyPositiveAssertionRegex.IsMatch($trigger.Text)) {
                Add-DocViolation -Rule 'DOC_MATURITY_POLICY_POSITIVE_ASSERTION' -Path $entryPath -Line $trigger.LineNumber
                $policyMatches = $false
            }
            if ($policySyntax -ceq 'DEFINITION') {
                foreach ($candidateCapability in $capabilitiesById.Values) {
                    if (Test-MaturityIdentityPresent -VisibleText $trigger.Text -Fragments $trigger.Fragments -Capability $candidateCapability) {
                        $policyMatches = $false
                        break
                    }
                }
            }
            if (-not $policyMatches) {
                Add-DocViolation -Rule 'DOC_MATURITY_POLICY_OVERCLAIM' -Path $entryPath -Line $trigger.LineNumber
            }
            continue
        }

        if ($null -ne $entry.policy) {
            Add-DocViolation -Rule 'DOC_MATURITY_CAPABILITY_POLICY_FORBIDDEN' -Path $entryPath -Line $trigger.LineNumber
        }

        if ($claims.Count -eq 0 -or ($claims.Count -eq 1 -and $null -eq $claims[0])) {
            Add-DocViolation -Rule 'DOC_MATURITY_CAPABILITY_CLAIMS_MISSING' -Path $entryPath -Line $trigger.LineNumber
            continue
        }
        $seenClaimIds = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
        $mentionedCapabilityIds = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
        $canonicalStatuses = Get-CanonicalMaturityStatuses -Text $trigger.Text
        foreach ($candidateCapability in $capabilitiesById.Values) {
            if (Test-MaturityIdentityPresent -VisibleText $trigger.Text -Fragments $trigger.Fragments -Capability $candidateCapability) {
                [void]$mentionedCapabilityIds.Add($candidateCapability.Id)
            }
        }
        foreach ($claim in $claims) {
            if ($null -eq $claim -or -not (Test-ExactJsonProperties -Object $claim -Expected @('capability_id', 'claimed_status'))) {
                Add-DocViolation -Rule 'DOC_MATURITY_CAPABILITY_CLAIM_SCHEMA' -Path $entryPath -Line $trigger.LineNumber
                continue
            }
            if (-not ($claim.capability_id -is [string]) -or -not ($claim.claimed_status -is [string])) {
                Add-DocViolation -Rule 'DOC_MATURITY_CAPABILITY_CLAIM_TYPE' -Path $entryPath -Line $trigger.LineNumber
                continue
            }
            $claimId = [string]$claim.capability_id
            $claimedStatus = [string]$claim.claimed_status
            if (-not $seenClaimIds.Add($claimId)) {
                Add-DocViolation -Rule 'DOC_MATURITY_CAPABILITY_CLAIM_DUPLICATE' -Path $entryPath -Line $trigger.LineNumber
                continue
            }
            if (-not $capabilitiesById.ContainsKey($claimId)) {
                Add-DocViolation -Rule 'DOC_MATURITY_CAPABILITY_UNKNOWN' -Path $entryPath -Line $trigger.LineNumber
                continue
            }
            $capability = $capabilitiesById[$claimId]
            if ($claimedStatus -cne $capability.Status) {
                Add-DocViolation -Rule 'DOC_MATURITY_CAPABILITY_STATUS_INFLATED' -Path $entryPath -Line $trigger.LineNumber
            }
            $identityPresent = Test-MaturityIdentityPresent -VisibleText $trigger.Text -Fragments $trigger.Fragments -Capability $capability
            if (-not $identityPresent) {
                Add-DocViolation -Rule 'DOC_MATURITY_CAPABILITY_IDENTITY_MISSING' -Path $entryPath -Line $trigger.LineNumber
            }
            if (-not $canonicalStatuses.Contains($claimedStatus)) {
                Add-DocViolation -Rule 'DOC_MATURITY_CAPABILITY_STATUS_TEXT_MISMATCH' -Path $entryPath -Line $trigger.LineNumber
            }
            if ($canonicalStatuses.Contains('stable') -and $capability.Status -cne 'stable') {
                Add-DocViolation -Rule 'DOC_MATURITY_CAPABILITY_STATUS_INFLATED' -Path $entryPath -Line $trigger.LineNumber
            }
        }
        $claimClosureValid = $seenClaimIds.Count -eq $mentionedCapabilityIds.Count
        foreach ($mentionedId in $mentionedCapabilityIds) {
            if (-not $seenClaimIds.Contains($mentionedId)) {
                $claimClosureValid = $false
            }
        }
        foreach ($seenId in $seenClaimIds) {
            if (-not $mentionedCapabilityIds.Contains($seenId)) {
                $claimClosureValid = $false
            }
        }
        if (-not $claimClosureValid) {
            Add-DocViolation -Rule 'DOC_MATURITY_CAPABILITY_CLAIMS_INCOMPLETE' -Path $entryPath -Line $trigger.LineNumber
        }
    }

    foreach ($trigger in $triggerByKey.Values) {
        if (-not $trigger.Used) {
            Add-DocViolation -Rule 'DOC_MATURITY_TRIGGER_UNCLAIMED' -Path $trigger.Path -Line $trigger.LineNumber
        }
    }
}

function Invoke-ArchitectureSpecArchiveChecks {
    param([Parameter(Mandatory = $true)][object[]]$RepositoryFiles)

    $manifestRelative = 'docs/architecture/specs-relocation.v1.json'
    $historyPrefix = 'docs/architecture/specs/'
    $indexRelative = 'docs/architecture/specs/README.md'
    $manifestFull = Join-Path $script:RootFull ($manifestRelative.Replace([char]47, [System.IO.Path]::DirectorySeparatorChar))
    $exemptions = New-Object 'System.Collections.Generic.List[string]'
    $currentExpected = @(
        [pscustomobject]@{
            Path = 'docs/specs/CORE_RUNTIME_V1.md'
            Status = 'S1_ACCEPTED_DEVELOPMENT_BASELINE'
            IndexLink = '../../specs/CORE_RUNTIME_V1.md'
        },
        [pscustomobject]@{
            Path = 'docs/specs/CURRENT_STORE_V1.md'
            Status = 'S1_SCHEMA_DRAFT_DEVELOPMENT_BASELINE_ACCEPTED'
            IndexLink = '../../specs/CURRENT_STORE_V1.md'
        },
        [pscustomobject]@{
            Path = 'docs/CUTOVER_ACCEPTANCE.md'
            Status = 'CUTOVER_NOT_APPLICABLE_NEVER_DEPLOYED'
            IndexLink = '../../CUTOVER_ACCEPTANCE.md'
        }
    )
    $historyExpected = @(
        [pscustomobject]@{ Path = 'docs/architecture/specs/2026-07-18-compatibility-remediation-b-design.md'; Status = 'HISTORICAL_NON_NORMATIVE' },
        [pscustomobject]@{ Path = 'docs/architecture/specs/2026-07-19-task3-lossless-persistence-design.md'; Status = 'HISTORICAL_NON_NORMATIVE' },
        [pscustomobject]@{ Path = 'docs/architecture/specs/2026-07-20-locked-mcp-evidence-first-discovery-design.md'; Status = 'S2_SEMANTIC_SOURCE_ONLY' },
        [pscustomobject]@{ Path = 'docs/architecture/specs/2026-07-20-storage-backend-b-design.md'; Status = 'HISTORICAL_NON_NORMATIVE' },
        [pscustomobject]@{ Path = 'docs/architecture/specs/2026-07-21-runtime-catalog-authority-design.md'; Status = 'HISTORICAL_NON_NORMATIVE' },
        [pscustomobject]@{ Path = 'docs/architecture/specs/2026-07-22-governance-rule-wire-design.md'; Status = 'HISTORICAL_NON_NORMATIVE' },
        [pscustomobject]@{ Path = 'docs/architecture/specs/2026-07-22-governance-rule-wire-v1-lock.md'; Status = 'HISTORICAL_NON_NORMATIVE' },
        [pscustomobject]@{ Path = 'docs/architecture/specs/2026-07-22-p0-spec-lock-attestation.md'; Status = 'HISTORICAL_NON_NORMATIVE' },
        [pscustomobject]@{ Path = 'docs/architecture/specs/2026-07-26-stage2-unified-assembly-cutover.md'; Status = 'HISTORICAL_NON_NORMATIVE' },
        [pscustomobject]@{ Path = 'docs/architecture/specs/2026-07-28-stage3-sealed-content-parents.md'; Status = 'S2_SEMANTIC_SOURCE_ONLY' },
        [pscustomobject]@{ Path = 'docs/architecture/specs/2026-07-28-stage3-skill-lifecycle-lock.md'; Status = 'S2_SEMANTIC_SOURCE_ONLY' }
    )

    if (-not (Test-SafeFixedPath -FullPath $manifestFull -RelativePath $manifestRelative)) {
        return $exemptions.ToArray()
    }
    $manifestActualFull = $null
    $manifestState = Get-RepositoryRelativePathState -RelativePath $manifestRelative -ActualFullPath ([ref]$manifestActualFull)
    if ($manifestState -cne 'exact' -or $null -eq $manifestActualFull -or
        -not (Test-Path -LiteralPath $manifestActualFull -PathType Leaf)) {
        Add-DocViolation -Rule 'DOC_SPEC_RELOCATION_MANIFEST_MISSING' -Path $manifestRelative
        return $exemptions.ToArray()
    }

    $manifestDocument = Read-StrictUtf8 -FullPath $manifestActualFull -RelativePath $manifestRelative
    if ($null -eq $manifestDocument) {
        return $exemptions.ToArray()
    }
    $manifestBytes = [byte[]]$manifestDocument.Bytes
    if ([Array]::IndexOf($manifestBytes, [byte]13) -ge 0 -or
        $manifestBytes.Length -eq 0 -or $manifestBytes[$manifestBytes.Length - 1] -ne [byte]10) {
        Add-DocViolation -Rule 'DOC_SPEC_RELOCATION_MANIFEST_JSON' -Path $manifestRelative
    }

    $hasDuplicate = $false
    if (-not (Test-StrictJsonDocument -Text $manifestDocument.Text -HasDuplicate ([ref]$hasDuplicate)) -or $hasDuplicate) {
        Add-DocViolation -Rule 'DOC_SPEC_RELOCATION_MANIFEST_JSON' -Path $manifestRelative
        return $exemptions.ToArray()
    }
    try {
        $manifest = $manifestDocument.Text | ConvertFrom-Json
    } catch {
        Add-DocViolation -Rule 'DOC_SPEC_RELOCATION_MANIFEST_JSON' -Path $manifestRelative
        return $exemptions.ToArray()
    }
    if ($null -eq $manifest -or
        -not (Test-ExactJsonProperties -Object $manifest -Expected @('schema_version', 'kind', 'index', 'current_authorities', 'historical_sources')) -or
        (-not ($manifest.schema_version -is [int]) -and -not ($manifest.schema_version -is [long])) -or
        [int64]$manifest.schema_version -ne 2 -or
        -not ($manifest.kind -is [string]) -or
        [string]$manifest.kind -cne 'freeagent-architecture-spec-authority-index' -or
        $null -eq $manifest.index -or
        -not (Test-ExactJsonProperties -Object $manifest.index -Expected @('path', 'status')) -or
        -not ($manifest.index.path -is [string]) -or
        [string]$manifest.index.path -cne $indexRelative -or
        -not ($manifest.index.status -is [string]) -or
        [string]$manifest.index.status -cne 'HISTORICAL_INDEX_NON_NORMATIVE' -or
        -not ($manifest.current_authorities -is [System.Array]) -or
        $manifest.current_authorities.Count -ne $currentExpected.Count -or
        -not ($manifest.historical_sources -is [System.Array]) -or
        $manifest.historical_sources.Count -ne $historyExpected.Count) {
        Add-DocViolation -Rule 'DOC_SPEC_RELOCATION_MANIFEST_SCHEMA' -Path $manifestRelative
        return $exemptions.ToArray()
    }

    $manifestCurrent = [System.Collections.Generic.Dictionary[string,string]]::new([System.StringComparer]::Ordinal)
    foreach ($entry in $manifest.current_authorities) {
        if ($null -eq $entry -or
            -not (Test-ExactJsonProperties -Object $entry -Expected @('path', 'status')) -or
            -not ($entry.path -is [string]) -or
            -not ($entry.status -is [string]) -or
            -not (Test-ManifestRelativePath -Path ([string]$entry.path)) -or
            $manifestCurrent.ContainsKey([string]$entry.path)) {
            Add-DocViolation -Rule 'DOC_SPEC_RELOCATION_ENTRY_INVALID' -Path $manifestRelative
            continue
        }
        $manifestCurrent.Add([string]$entry.path, [string]$entry.status)
    }

    $manifestHistory = [System.Collections.Generic.Dictionary[string,string]]::new([System.StringComparer]::Ordinal)
    foreach ($entry in $manifest.historical_sources) {
        if ($null -eq $entry -or
            -not (Test-ExactJsonProperties -Object $entry -Expected @('path', 'status')) -or
            -not ($entry.path -is [string]) -or
            -not ($entry.status -is [string]) -or
            -not (Test-ManifestRelativePath -Path ([string]$entry.path)) -or
            $manifestHistory.ContainsKey([string]$entry.path)) {
            Add-DocViolation -Rule 'DOC_SPEC_RELOCATION_ENTRY_INVALID' -Path $manifestRelative
            continue
        }
        $manifestHistory.Add([string]$entry.path, [string]$entry.status)
    }

    $indexActualFull = $null
    $indexState = Get-RepositoryRelativePathState -RelativePath $indexRelative -ActualFullPath ([ref]$indexActualFull)
    if ($indexState -cne 'exact' -or $null -eq $indexActualFull -or
        -not (Test-Path -LiteralPath $indexActualFull -PathType Leaf)) {
        Add-DocViolation -Rule 'DOC_SPEC_AUTHORITY_INDEX_MISSING' -Path $indexRelative
    } else {
        $indexDocument = Read-StrictUtf8 -FullPath $indexActualFull -RelativePath $indexRelative
        if ($null -ne $indexDocument) {
            $indexBytes = [byte[]]$indexDocument.Bytes
            if ([Array]::IndexOf($indexBytes, [byte]13) -ge 0 -or
                $indexBytes.Length -eq 0 -or $indexBytes[$indexBytes.Length - 1] -ne [byte]10) {
                Add-DocViolation -Rule 'DOC_SPEC_LINE_ENDING_NOT_LF' -Path $indexRelative
            }
            if ($indexDocument.Text.IndexOf('`HISTORICAL_INDEX_NON_NORMATIVE`', [System.StringComparison]::Ordinal) -lt 0) {
                Add-DocViolation -Rule 'DOC_SPEC_STATUS_MISMATCH' -Path $indexRelative
            }
            foreach ($expected in $currentExpected) {
                if ($indexDocument.Text.IndexOf($expected.IndexLink, [System.StringComparison]::Ordinal) -lt 0) {
                    Add-DocViolation -Rule 'DOC_SPEC_INDEX_LINK_MISSING' -Path $indexRelative
                }
            }
            foreach ($expected in $historyExpected) {
                $historyName = [System.IO.Path]::GetFileName($expected.Path)
                if ($indexDocument.Text.IndexOf($historyName, [System.StringComparison]::Ordinal) -lt 0 -or
                    $indexDocument.Text.IndexOf(('`' + $expected.Status + '`'), [System.StringComparison]::Ordinal) -lt 0) {
                    Add-DocViolation -Rule 'DOC_SPEC_INDEX_ENTRY_MISSING' -Path $indexRelative
                }
            }
        }
    }
    $exemptions.Add($indexRelative)

    foreach ($expected in $currentExpected) {
        if (-not $manifestCurrent.ContainsKey($expected.Path) -or
            $manifestCurrent[$expected.Path] -cne $expected.Status) {
            Add-DocViolation -Rule 'DOC_SPEC_CURRENT_CANDIDATE_DECLARATION' -Path $expected.Path
        }
        $actualFull = $null
        $state = Get-RepositoryRelativePathState -RelativePath $expected.Path -ActualFullPath ([ref]$actualFull)
        if ($state -cne 'exact' -or $null -eq $actualFull -or
            -not (Test-Path -LiteralPath $actualFull -PathType Leaf)) {
            Add-DocViolation -Rule 'DOC_SPEC_CURRENT_CANDIDATE_MISSING' -Path $expected.Path
            continue
        }
        $document = Read-StrictUtf8 -FullPath $actualFull -RelativePath $expected.Path
        if ($null -ne $document) {
            $bytes = [byte[]]$document.Bytes
            if ([Array]::IndexOf($bytes, [byte]13) -ge 0 -or
                $bytes.Length -eq 0 -or $bytes[$bytes.Length - 1] -ne [byte]10) {
                Add-DocViolation -Rule 'DOC_SPEC_LINE_ENDING_NOT_LF' -Path $expected.Path
            }
            $machineStatusMatches = [System.Text.RegularExpressions.Regex]::Matches(
                $document.Text,
                '(?m)^Machine status: `(?<status>[A-Z0-9_]+)`[ \t]*$'
            )
            if ($machineStatusMatches.Count -ne 1 -or
                $machineStatusMatches[0].Groups['status'].Value -cne $expected.Status) {
                Add-DocViolation -Rule 'DOC_SPEC_STATUS_MISMATCH' -Path $expected.Path
            }
        }
        $exemptions.Add($expected.Path)
    }

    foreach ($expected in $historyExpected) {
        if (-not $manifestHistory.ContainsKey($expected.Path) -or
            $manifestHistory[$expected.Path] -cne $expected.Status) {
            Add-DocViolation -Rule 'DOC_SPEC_HISTORICAL_DECLARATION' -Path $expected.Path
        }
        $actualFull = $null
        $state = Get-RepositoryRelativePathState -RelativePath $expected.Path -ActualFullPath ([ref]$actualFull)
        if ($state -cne 'exact' -or $null -eq $actualFull -or
            -not (Test-Path -LiteralPath $actualFull -PathType Leaf)) {
            Add-DocViolation -Rule 'DOC_SPEC_HISTORICAL_SOURCE_MISSING' -Path $expected.Path
            continue
        }
        $document = Read-StrictUtf8 -FullPath $actualFull -RelativePath $expected.Path
        if ($null -ne $document) {
            $bytes = [byte[]]$document.Bytes
            if ([Array]::IndexOf($bytes, [byte]13) -ge 0 -or
                $bytes.Length -eq 0 -or $bytes[$bytes.Length - 1] -ne [byte]10) {
                Add-DocViolation -Rule 'DOC_SPEC_LINE_ENDING_NOT_LF' -Path $expected.Path
            }
            $marker = '`' + $expected.Status + '`'
            if ($document.Text.IndexOf($marker, [System.StringComparison]::Ordinal) -lt 0) {
                Add-DocViolation -Rule 'DOC_SPEC_STATUS_MISMATCH' -Path $expected.Path
            }
        }
        $exemptions.Add($expected.Path)
    }

    $legacyRootActual = $null
    if ((Get-RepositoryRelativePathState -RelativePath 'docs/superpowers' -ActualFullPath ([ref]$legacyRootActual)) -cne 'missing') {
        Add-DocViolation -Rule 'DOC_SPEC_RELOCATION_OLD_PATH_PRESENT' -Path 'docs/superpowers'
    }

    $expectedHistoryPaths = @($historyExpected | ForEach-Object { $_.Path }) + @($indexRelative)
    [string[]]$expectedHistoryArray = $expectedHistoryPaths
    [Array]::Sort($expectedHistoryArray, [System.StringComparer]::Ordinal)
    $actualHistoryPaths = New-Object 'System.Collections.Generic.List[string]'
    foreach ($file in $RepositoryFiles) {
        $relative = Get-RepositoryRelativePath -FullPath $file.FullName
        if ($relative.StartsWith($historyPrefix, [System.StringComparison]::OrdinalIgnoreCase) -and
            [System.IO.Path]::GetExtension($relative) -ieq '.md') {
            $actualHistoryPaths.Add($relative)
        }
    }
    [string[]]$actualHistoryArray = $actualHistoryPaths.ToArray()
    [Array]::Sort($actualHistoryArray, [System.StringComparer]::Ordinal)
    if (($expectedHistoryArray -join "`n") -cne ($actualHistoryArray -join "`n")) {
        Add-DocViolation -Rule 'DOC_SPEC_HISTORY_SET_MISMATCH' -Path 'docs/architecture/specs'
    }

    return $exemptions.ToArray()
}

function Invoke-DocsGate {
    $repositoryFiles = @(Get-SafeRepositoryFiles)
    $markdownFiles = @($repositoryFiles | Where-Object { [System.IO.Path]::GetExtension($_.Name) -ieq '.md' })
    $markdownFiles = @($markdownFiles | Sort-Object { Get-RepositoryRelativePath -FullPath $_.FullName })
    $markdownByFull = [System.Collections.Generic.Dictionary[string,object]]::new($script:PathComparer)

    foreach ($file in $markdownFiles) {
        $relative = Get-RepositoryRelativePath -FullPath $file.FullName
        if (Test-PathHasReparsePoint -FullPath $file.FullName) {
            Add-DocViolation -Rule 'DOC_MARKDOWN_REPARSE_POINT' -Path $relative
            continue
        }
        $document = Read-StrictUtf8 -FullPath $file.FullName -RelativePath $relative
        if ($null -eq $document) {
            continue
        }
        $anchors = Get-ExplicitAnchors -Document $document -RelativePath $relative
        $markdownByFull.Add($file.FullName, [pscustomobject]@{
            FullPath = $file.FullName
            RelativePath = $relative
            Bytes = $document.Bytes
            Text = $document.Text
            Lines = $document.Lines
            Anchors = $anchors
        })
        Test-PrivateAbsoluteContent -Document $document -RelativePath $relative
        Test-ExternalHostAllowlist -Document $document -RelativePath $relative
    }

    foreach ($record in $markdownByFull.Values) {
        Test-MarkdownLinks -Document $record -FullPath $record.FullPath -RelativePath $record.RelativePath -MarkdownByFull $markdownByFull
        Test-RawHtmlLinks -Document $record -FullPath $record.FullPath -RelativePath $record.RelativePath -MarkdownByFull $markdownByFull
    }

    $architectureSpecExemptions = @(Invoke-ArchitectureSpecArchiveChecks -RepositoryFiles $repositoryFiles)

    $maturityExemptions = @($architectureSpecExemptions | Select-Object -Unique)
    Invoke-MaturityChecks -MarkdownByFull $markdownByFull -P0Exemptions $maturityExemptions

    if ($script:Violations.Count -gt 0) {
        $violations = $script:Violations.ToArray()
        [System.Array]::Sort($violations, [System.StringComparer]::Ordinal)
        foreach ($violation in $violations) {
            [Console]::Error.WriteLine($violation)
        }
        exit 1
    }

    [Console]::Out.WriteLine("DOCS_GATE_OK markdown=$($markdownByFull.Count)")
    exit 0
}

try {
    Invoke-DocsGate
} catch {
    [Console]::Error.WriteLine('DOC_INTERNAL_ERROR path=.')
    exit 1
}
