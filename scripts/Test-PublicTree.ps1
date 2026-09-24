[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Root
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

trap {
    Write-Output 'PUBLIC_TREE_FAIL rule=PT_INTERNAL path=<root>'
    exit 1
}

$script:Findings = New-Object 'System.Collections.Generic.List[object]'
$script:FindingKeys = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
$script:Files = New-Object 'System.Collections.Generic.List[object]'
$script:IsWindowsPlatform = $env:OS -eq 'Windows_NT'
$script:UnixStatPath = '/usr/bin/stat'
$script:ReviewedJavaScriptCredentialLineHashes = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
$script:SeenReviewedJavaScriptCredentialLineHashes = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
$script:ReviewedUncLineHashes = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
$script:SeenReviewedUncLineHashes = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
foreach ($reviewedHash in @(
    '316a4a29f84cb15774c066cc854005c2aabbe69b960411330f65493752e53d83',
    '4a7e4d66496348382f175a7dbdf618d1aab079fe2d24fc0200f07bc4264b06f5',
    'c0d6bb94fb0df28a7d08ca9a73f9357591f8ff7027859aa3403c8d408bae9c7a',
    'ad3a66985ce657b5c2ef958a4bc989664e43c5e471a84aa065fc666c1e110f2a',
    'f84ea18ee4808274ab510fc56ac430bf0275384ad8f6558a70848cc889d89928',
    '6b7295e18e6d42a4b4e50882254d1c0ee8e694109ceac442f49c9daf602f185e',
    'a657974d6b6827f15908b7353a3e77fb5bd87155556d88782cdc54f31a7eca72',
    'eee4e27f797a8a27789a78f432a8b1d450cc9460e7e3ef77278d8a9304de2e31',
    '22b5d0c49e73b43562473e77bc6d135d218989c07c153c346856a08080e2310d',
    'ed4de0d285843b1fcefbc797a106c377cd16919769c98db321a9e9a35032bebf',
    '326f4260a6b2ffc40ce906ba9700eeff93bb519faa1f25bc10f01bedf9b4ad75',
    'be18ed3b8c19ad0edaa4c5f289e9e20eba3a8d8f482393090ca6b3b0ba149e85',
    'd4f10b5df5139c911935c743406f53c50045b5cfdd4a3f27da01e6483a38c6ff',
    '8312bcff4a357261e04b292ef2448595e6e28d884c1fd6795893795f39dae1a2',
    'a02eded69cde70ec4f2e9c2ff1fa82e54d1a7f7002a5cf77c0d00efb97141c29',
    'fb5abc0912f3af4b38e6500a75064ade369212d6137d743c60a6809db567721b',
    '760ef84ce7ec3434b8e352c37d40a04f370fb5ba505196426870cbb363cc95d4',
    'c2df2898ea73daddac16cbab99e82b3ab3887282a011935048457ffafb71aea8',
    '5ca594c3f2c6c0fa50ee9dbf8c52623d3c6d7b061d96b4385d08c6f88a6c1842',
    '81b7fdc115dd19d54317799bc752262721c3d5e8a182cf7e5e5f4f305c0d4d9b'
)) {
    [void]$script:ReviewedJavaScriptCredentialLineHashes.Add($reviewedHash)
}
foreach ($reviewedHash in @(
    '044f2f6911c657de89683910b47e31c45a9f9864a0649d65f4ee11ff60fdabea',
    '55aaf0de18616c4637287bd58df80927d9958d4186ef786f9d20699b53d1d3cb',
    'd7a003908af754742e1fb99ecb6a5af7fa5f54114876109103dbd2642eed9e91',
    '96b5eb3153b6a91cec2e9f04843ed000be20721b1938a3c5e77d7ee58085976b',
    'f2b01ed20531ee53e4b09646ed4947e17c2e9206c182d362b31364179a334719',
    '64f771a567908ac4696db65f0513029f988a12573a06c524acf209488e971457',
    '56fd1a7f5e8346134f36486050938c4abe45a7257071de180ead860dde55b2f0',
    'b1092e6de9fdc84b075dc82b9f6d090ab9ae9f1c1cf53dd594a4dd8da4f9f256',
    '5a82271842acdc14d86dbce1f35bf5122effb9e7d51691671923cd28b822431d',
    '004394d86ffa01de8ae0de85a2ffebb5e52ed0d7124edd6ada14520fb3b3a559',
    '1f4a6683681421ffaf8cb5cb07a509f398bb373e833c28ad3b659124e9fca54b',
    '2508b74233420e9ae37c0504d7333c1558334fc2a6cd51ff9274e700f32b6037',
    'cc3cfe513e55695f78ff675fe8b256e50fdd02b274c2461ae880d1b9f758f50f',
    'd3d6a7830914c92d129bf53a945458eb9d00a2b2f361133a65810eb6e35b0a4d',
    '2e51510a2e0ffb9ad6378e6277fae7b335ff2a7782314adc2702f085503331d2'
)) {
    [void]$script:ReviewedUncLineHashes.Add($reviewedHash)
}

if ($script:IsWindowsPlatform) {
    # Get-Item -Stream does not consistently enumerate named streams on
    # directories.  Use the Windows stream-enumeration API so the repository
    # root, child directories, and files are checked by the same mechanism.
    Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Runtime.InteropServices;

public static class PublicTreeNativeStreams
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
}

function Add-PublicTreeFinding {
    param(
        [Parameter(Mandatory = $true)][string]$Rule,
        [Parameter(Mandatory = $true)][string]$Path,
        [int]$Line = 0
    )

    if ([string]::IsNullOrWhiteSpace($Path)) {
        $Path = '<root>'
    }
    $key = $Rule + [char]0 + $Path + [char]0 + $Line
    if ($script:FindingKeys.Add($key)) {
        $script:Findings.Add([pscustomobject]@{
            Rule = $Rule
            Path = $Path
            Line = $Line
            Key  = $key
        })
    }
}

function Get-UnixFileKind {
    param([Parameter(Mandatory = $true)][string]$LiteralPath)

    if ($script:IsWindowsPlatform) { return 'NotApplicable' }
    if (-not (Test-Path -LiteralPath $script:UnixStatPath -PathType Leaf)) { return 'Unknown' }

    $process = New-Object Diagnostics.Process
    try {
        $startInfo = New-Object Diagnostics.ProcessStartInfo
        $startInfo.FileName = $script:UnixStatPath
        $startInfo.UseShellExecute = $false
        $startInfo.CreateNoWindow = $true
        $startInfo.RedirectStandardOutput = $true
        $startInfo.RedirectStandardError = $true
        if ($null -eq $startInfo.ArgumentList) { return 'Unknown' }
        [void]$startInfo.ArgumentList.Add('--format=%F')
        [void]$startInfo.ArgumentList.Add('--')
        [void]$startInfo.ArgumentList.Add($LiteralPath)
        $startInfo.EnvironmentVariables['LC_ALL'] = 'C'
        $process.StartInfo = $startInfo
        if (-not $process.Start()) { return 'Unknown' }

        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(2000)) {
            try { $process.Kill() } catch { }
            if (-not $process.WaitForExit(5000)) {
                throw 'Unable to terminate the Unix file-kind probe.'
            }
            [void]$stdoutTask.Result
            [void]$stderrTask.Result
            return 'Unknown'
        }
        $process.WaitForExit()
        $stdout = $stdoutTask.Result
        $stderr = $stderrTask.Result
        if ($process.ExitCode -ne 0 -or -not [string]::IsNullOrEmpty($stderr)) { return 'Unknown' }

        $kind = $stdout.TrimEnd([char[]]@([char]13, [char]10))
        if ($kind -ceq 'regular file' -or $kind -ceq 'regular empty file') { return 'Regular' }
        return 'Special'
    } catch {
        return 'Unknown'
    } finally {
        $process.Dispose()
    }
}

function ConvertTo-SafeDisplayPath {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Rule
    )

    if ($Path -ceq '<root>') { return $Path }
    $segments = @($Path.Split([char]47))
    for ($segmentIndex = 0; $segmentIndex -lt $segments.Count; $segmentIndex++) {
        $segment = $segments[$segmentIndex]
        $lower = $segment.ToLowerInvariant()
        $looksSensitive =
            ($Rule -ceq 'PT_SECRET_FILE' -and $segmentIndex -eq ($segments.Count - 1)) -or
            $lower -eq '.env' -or
            $lower.StartsWith('.env.') -or
            $lower.EndsWith('.pem') -or
            $lower.EndsWith('.key') -or
            $lower.EndsWith('.p12') -or
            $lower.EndsWith('.pfx') -or
            $lower -match '(?:^|[-_.=])(password|passwd|secret|token|api[-_]?key|authorization)(?:[-_.=]|$)' -or
            $segment -match '(?i)(?:sk-(?:proj-)?[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|xox[baprs]-[A-Za-z0-9-]{10,})'
        if (-not $looksSensitive) {
            foreach ($candidate in @(Get-RuleTextForms -Text $segment)) {
                foreach ($sensitiveRule in $contentRules) {
                    if ([regex]::IsMatch($candidate, $sensitiveRule.Pattern)) {
                        $looksSensitive = $true
                        break
                    }
                }
                if ($looksSensitive -or (Test-CredentialAssignmentSecret -Text $candidate)) {
                    $looksSensitive = $true
                    break
                }
            }
        }
        if ($looksSensitive) {
            $segments[$segmentIndex] = '<sensitive>'
            continue
        }

        $builder = New-Object Text.StringBuilder
        $characterIndex = 0
        while ($characterIndex -lt $segment.Length) {
            $character = $segment[$characterIndex]
            $code = [int]$character
            if ([char]::IsHighSurrogate($character)) {
                if (($characterIndex + 1) -ge $segment.Length -or -not [char]::IsLowSurrogate($segment[$characterIndex + 1])) {
                    [void]$builder.Append(('\u{0:x4}' -f $code))
                    $characterIndex++
                    continue
                }
                $category = [Globalization.CharUnicodeInfo]::GetUnicodeCategory($segment, $characterIndex)
                if (@(
                    [Globalization.UnicodeCategory]::Control,
                    [Globalization.UnicodeCategory]::Format,
                    [Globalization.UnicodeCategory]::LineSeparator,
                    [Globalization.UnicodeCategory]::ParagraphSeparator,
                    [Globalization.UnicodeCategory]::Surrogate
                ) -contains $category) {
                    [void]$builder.Append(('\u{0:x4}\u{1:x4}' -f $code, [int]$segment[$characterIndex + 1]))
                } else {
                    [void]$builder.Append($character)
                    [void]$builder.Append($segment[$characterIndex + 1])
                }
                $characterIndex += 2
                continue
            }
            if ([char]::IsLowSurrogate($character)) {
                [void]$builder.Append(('\u{0:x4}' -f $code))
                $characterIndex++
                continue
            }

            $category = [char]::GetUnicodeCategory($character)
            if (@(
                [Globalization.UnicodeCategory]::Control,
                [Globalization.UnicodeCategory]::Format,
                [Globalization.UnicodeCategory]::LineSeparator,
                [Globalization.UnicodeCategory]::ParagraphSeparator,
                [Globalization.UnicodeCategory]::Surrogate
            ) -contains $category) {
                [void]$builder.Append(('\u{0:x4}' -f $code))
            } else {
                [void]$builder.Append($character)
            }
            $characterIndex++
        }
        $segments[$segmentIndex] = $builder.ToString()
    }
    return ($segments -join '/')
}

function Stop-WithRootFinding {
    param([Parameter(Mandatory = $true)][string]$Rule)

    Write-Output "PUBLIC_TREE_FAIL rule=$Rule path=<root>"
    exit 1
}

function Join-CharacterCodes {
    param([Parameter(Mandatory = $true)][int[]]$Codes)

    return -join @($Codes | ForEach-Object { [char]$_ })
}

function Get-RetiredConstructionScriptNames {
    return @(
        (Join-CharacterCodes -Codes @(84, 101, 115, 116, 45, 67, 111, 110, 115, 116, 114, 117, 99, 116, 105, 111, 110, 84, 114, 101, 101, 46, 112, 115, 49)),
        (Join-CharacterCodes -Codes @(84, 101, 115, 116, 45, 67, 111, 110, 115, 116, 114, 117, 99, 116, 105, 111, 110, 84, 114, 101, 101, 46, 84, 101, 115, 116, 115, 46, 112, 115, 49)),
        (Join-CharacterCodes -Codes @(84, 101, 115, 116, 45, 83, 101, 110, 115, 105, 116, 105, 118, 101, 66, 97, 115, 101, 108, 105, 110, 101, 46, 112, 115, 49)),
        (Join-CharacterCodes -Codes @(84, 101, 115, 116, 45, 83, 101, 110, 115, 105, 116, 105, 118, 101, 66, 97, 115, 101, 108, 105, 110, 101, 46, 84, 101, 115, 116, 115, 46, 112, 115, 49)),
        (Join-CharacterCodes -Codes @(98, 97, 116, 99, 104, 45, 98, 45, 114, 101, 108, 101, 97, 115, 101, 45, 103, 97, 116, 101, 46, 112, 115, 49)),
        (Join-CharacterCodes -Codes @(98, 97, 116, 99, 104, 45, 98, 45, 114, 101, 108, 101, 97, 115, 101, 45, 103, 97, 116, 101, 46, 84, 101, 115, 116, 115, 46, 112, 115, 49))
    )
}

function Get-RetiredBatchBArtifactNames {
    return @(
        (Join-CharacterCodes -Codes @(112, 114, 101, 45, 117, 112, 103, 114, 97, 100, 101, 45, 118, 51, 57, 46, 98, 117, 110, 100, 108, 101)),
        (Join-CharacterCodes -Codes @(99, 108, 111, 110, 101, 45, 112, 111, 115, 116, 45, 105, 109, 112, 111, 114, 116, 45, 118, 52, 48, 46, 98, 117, 110, 100, 108, 101)),
        (Join-CharacterCodes -Codes @(114, 111, 108, 108, 98, 97, 99, 107, 45, 112, 114, 111, 111, 102, 45, 118, 51, 57, 46, 98, 117, 110, 100, 108, 101)),
        (Join-CharacterCodes -Codes @(112, 114, 111, 100, 117, 99, 116, 105, 111, 110, 45, 112, 111, 115, 116, 45, 105, 109, 112, 111, 114, 116, 45, 118, 52, 48, 46, 98, 117, 110, 100, 108, 101)),
        (Join-CharacterCodes -Codes @(114, 101, 104, 101, 97, 114, 115, 97, 108, 45, 101, 118, 105, 100, 101, 110, 99, 101, 46, 106, 115, 111, 110)),
        (Join-CharacterCodes -Codes @(108, 101, 103, 97, 99, 121, 45, 99, 117, 114, 115, 111, 114, 45, 115, 116, 97, 116, 117, 115, 46, 116, 120, 116)),
        (Join-CharacterCodes -Codes @(118, 51, 57, 45, 99, 108, 111, 110, 101, 45, 114, 101, 104, 101, 97, 114, 115, 97, 108)),
        (Join-CharacterCodes -Codes @(114, 111, 108, 108, 98, 97, 99, 107, 45, 114, 101, 104, 101, 97, 114, 115, 97, 108))
    )
}

function Get-RuleTextForms {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text)

    Write-Output $Text
    $normalized = $Text.Replace([string][char]92, [string][char]47)
    if ($normalized -cne $Text) {
        Write-Output $normalized
    }
}

function Test-KnownCredentialSafeValue {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value)

    foreach ($knownPlaceholder in @('SensitivePrivateMarker', 'NeverReadMarker')) {
        if ([string]::Equals($Value, $knownPlaceholder, [StringComparison]::Ordinal)) {
            return $true
        }
    }
    if ($Value -match '(?i)^(?:placeholder|redacted|changeme|change-me|replace-me|example-value|dummy-value|not-a-secret|your-(?:password|secret|token|api-key))$') {
        return $true
    }
    if ($Value -match '^\$\{[A-Z][A-Z0-9_]{1,63}\}$' -or $Value -match '^\{\{\s*[A-Z][A-Z0-9_.-]{1,63}\s*\}\}$' -or $Value -match '^<[A-Z][A-Z0-9_.-]{1,63}>$') {
        return $true
    }
    return $false
}

function Test-CredentialVariableName {
    param([Parameter(Mandatory = $true)][string]$Name)

    $componentText = [regex]::Replace($Name, '([A-Z]+)([A-Z][a-z])', '$1_$2')
    $componentText = [regex]::Replace($componentText, '([a-z0-9])([A-Z])', '$1_$2')
    $components = @(
        $componentText.Split([char[]]@([char]95, [char]45), [StringSplitOptions]::RemoveEmptyEntries) |
            ForEach-Object { $_.ToLowerInvariant() }
    )
    $joinedComponents = $components -join '_'
    # Token accounting and parser diagnostics are public operational metadata,
    # not credential containers. Keep this list semantic and closed so a bare
    # token/password/api-key target still follows the fail-closed path below.
    if (
        $joinedComponents -match '^(?:known_)?token_(?:field_coverage|subtotal|report|signal)$' -or
        $joinedComponents -match '^usage_rows_(?:with_any|without)_token_facts?$'
    ) {
        return $false
    }
    if (
        @($components | Where-Object { @('password', 'passwd', 'secret', 'authorization') -ccontains $_ }).Count -gt 0 -or
        $joinedComponents -match '(^|_)(?:api_key|access_token|auth_token|client_secret)(?:_ref|_refs)?($|_)'
    ) {
        return $true
    }
    for ($index = 0; $index -lt ($components.Count - 1); $index++) {
        if (
            $components[$index] -ceq 'token' -and
            @('budget', 'count', 'counts', 'limit', 'limits', 'usage', 'usages') -ccontains $components[$index + 1]
        ) {
            return $false
        }
    }
    if ($components -ccontains 'token') {
        return $true
    }
    $componentPattern = '(?i)(?:^|[_-])(?:password|passwd|secret|token|authorization)(?:$|[_-])|(?:^|[_-])(?:api[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret)(?:$|[_-])'
    return [regex]::IsMatch($Name, $componentPattern)
}

function Test-ReviewedJavaScriptCredentialLine {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$RelativePath
    )

    $extension = [IO.Path]::GetExtension($RelativePath).ToLowerInvariant()
    if (@('.js', '.jsx', '.mjs', '.cjs', '.ts', '.tsx') -notcontains $extension) {
        return $false
    }
    $sha256 = [Security.Cryptography.SHA256]::Create()
    try {
        $bytes = [Text.Encoding]::UTF8.GetBytes($RelativePath + [char]0 + $Text)
        $hash = [BitConverter]::ToString($sha256.ComputeHash($bytes)).Replace('-', '').ToLowerInvariant()
    } finally {
        $sha256.Dispose()
    }
    if (-not $script:ReviewedJavaScriptCredentialLineHashes.Contains($hash)) { return $false }
    return $script:SeenReviewedJavaScriptCredentialLineHashes.Add($hash)
}

function Test-ReviewedUncLine {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$RelativePath,
        [Parameter(Mandatory = $true)][ValidateRange(1, [int]::MaxValue)][int]$Line
    )

    $sha256 = [Security.Cryptography.SHA256]::Create()
    try {
        $lineText = $Line.ToString([Globalization.CultureInfo]::InvariantCulture)
        $bytes = [Text.Encoding]::UTF8.GetBytes(
            $RelativePath + [char]0 + $lineText + [char]0 + $Text
        )
        $hash = [BitConverter]::ToString($sha256.ComputeHash($bytes)).Replace('-', '').ToLowerInvariant()
    } finally {
        $sha256.Dispose()
    }
    if (-not $script:ReviewedUncLineHashes.Contains($hash)) { return $false }
    return $script:SeenReviewedUncLineHashes.Add($hash)
}

function Test-GoProjectedExpressionBalanced {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value)

    $stack = New-Object 'System.Collections.Generic.Stack[char]'
    foreach ($character in $Value.ToCharArray()) {
        if ($character -eq '(' -or $character -eq '[' -or $character -eq '{') {
            $stack.Push($character)
            continue
        }
        if ($character -ne ')' -and $character -ne ']' -and $character -ne '}') {
            continue
        }
        if ($stack.Count -eq 0) { return $false }
        $opening = $stack.Pop()
        if (
            ($character -eq ')' -and $opening -ne '(') -or
            ($character -eq ']' -and $opening -ne '[') -or
            ($character -eq '}' -and $opening -ne '{')
        ) {
            return $false
        }
    }
    return $stack.Count -eq 0
}

function Remove-GoCommentsFromCredentialLine {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text,
        [Parameter(Mandatory = $true)][ref]$LexState,
        [ref]$ContainsRawStringContent
    )

    # Most lines contain no slash and can bypass the lexer entirely.  Lines
    # that can affect comment state use this small state machine; the caller
    # carries block-comment state across lines.
    if (
        $LexState.Value -ceq 'code' -and
        $Text.IndexOf([char]47) -lt 0 -and
        $Text.IndexOf([char]96) -lt 0
    ) {
        return $Text
    }

    $builder = New-Object Text.StringBuilder
    $state = [string]$LexState.Value
    $escaped = $false
    for ($index = 0; $index -lt $Text.Length; $index++) {
        $character = $Text[$index]
        $next = if (($index + 1) -lt $Text.Length) { $Text[$index + 1] } else { [char]0 }

        if ($state -ceq 'block-comment') {
            if ($character -eq '*' -and $next -eq '/') {
                [void]$builder.Append(' ')
                [void]$builder.Append(' ')
                $index++
                $state = 'code'
            } else {
                [void]$builder.Append(' ')
            }
            continue
        }
        if ($state -ceq 'raw-string') {
            if ($character -eq [char]96) {
                [void]$builder.Append(' ')
                $state = 'code'
            } else {
                if ($null -ne $ContainsRawStringContent) {
                    $ContainsRawStringContent.Value = $true
                }
                [void]$builder.Append($character)
            }
            continue
        }
        if ($state -ceq 'quoted-string' -or $state -ceq 'rune') {
            [void]$builder.Append($character)
            if ($escaped) {
                $escaped = $false
            } elseif ($character -eq [char]92) {
                $escaped = $true
            } elseif (
                ($state -ceq 'quoted-string' -and $character -eq [char]34) -or
                ($state -ceq 'rune' -and $character -eq [char]39)
            ) {
                $state = 'code'
            }
            continue
        }

        if ($character -eq '/' -and $next -eq '/') {
            while ($index -lt $Text.Length) {
                [void]$builder.Append(' ')
                $index++
            }
            break
        }
        if ($character -eq '/' -and $next -eq '*') {
            [void]$builder.Append(' ')
            [void]$builder.Append(' ')
            $index++
            $state = 'block-comment'
            continue
        }

        if ($character -eq [char]96) {
            [void]$builder.Append(' ')
            $state = 'raw-string'
        } elseif ($character -eq [char]34) {
            [void]$builder.Append($character)
            $state = 'quoted-string'
            $escaped = $false
        } elseif ($character -eq [char]39) {
            [void]$builder.Append($character)
            $state = 'rune'
            $escaped = $false
        } else {
            [void]$builder.Append($character)
        }
    }
    $LexState.Value = if ($state -ceq 'block-comment' -or $state -ceq 'raw-string') { $state } else { 'code' }
    return $builder.ToString()
}

function ConvertTo-GoCredentialProjection {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text)

    # Replace Go string/rune literals with stable markers.  This lets the
    # assignment grammar distinguish executable syntax from diagnostic prose,
    # while still retaining the exact RHS literal for the fail-closed decision.
    $builder = New-Object Text.StringBuilder
    $literals = @{}
    $index = 0
    $literalNumber = 0
    while ($index -lt $Text.Length) {
        $opening = $Text[$index]
        if ($opening -ne [char]34 -and $opening -ne [char]39 -and $opening -ne [char]96) {
            [void]$builder.Append($opening)
            $index++
            continue
        }

        $start = $index
        $index++
        $escaped = $false
        while ($index -lt $Text.Length) {
            $character = $Text[$index]
            if ($opening -eq [char]96) {
                if ($character -eq [char]96) {
                    $index++
                    break
                }
            } elseif ($escaped) {
                $escaped = $false
            } elseif ($character -eq [char]92) {
                $escaped = $true
            } elseif ($character -eq $opening) {
                $index++
                break
            }
            $index++
        }

        $closed = $index -le $Text.Length -and $index -gt $start -and $Text[$index - 1] -eq $opening
        if (-not $closed) {
            # An unterminated literal is invalid Go.  Preserve a marker whose
            # value is unknown so a sensitive assignment fails closed.
            $index = $Text.Length
        }
        $innerLength = [Math]::Max(0, $index - $start - $(if ($closed) { 2 } else { 1 }))
        $value = if ($innerLength -gt 0) { $Text.Substring($start + 1, $innerLength) } else { '' }
        $marker = '__FA_CREDENTIAL_LITERAL_' + $literalNumber.ToString('D4', [Globalization.CultureInfo]::InvariantCulture) + '__'
        $literalNumber++
        $literals[$marker] = [pscustomobject]@{
            Value = $value
            Closed = $closed
            Quote = [string]$opening
        }

        # A quoted map key still has to participate in the sensitive-name
        # grammar.  Preserve only credential-shaped keys; all other literal
        # contents are hidden behind an opaque marker.
        if ($closed -and $value.Length -gt 0 -and (Test-CredentialVariableName -Name $value)) {
            [void]$builder.Append([char]34)
            [void]$builder.Append($value)
            [void]$builder.Append([char]34)
        } else {
            [void]$builder.Append($marker)
        }
    }
    return [pscustomobject]@{
        Text = $builder.ToString()
        Literals = $literals
    }
}

function Test-CredentialDynamicReference {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value,
        [Parameter(Mandatory = $true)][string]$Kind,
        [Collections.IDictionary]$LiteralValues
    )

    if ($Kind -ceq 'go') {
        if ($Value -match '^(?:range\s+)?&?[A-Za-z_][A-Za-z0-9_]*(?:(?:\.[A-Za-z_][A-Za-z0-9_]*)|(?:\[[^\]\r\n]+\]))*$') {
            return $true
        }
        if (
            $Value -match '^(?:range\s+)?(?:&?[A-Za-z_][A-Za-z0-9_.]*(?:\[[^\]\r\n]+\])?|map\[[^\]\r\n]+\][A-Za-z0-9_.\[\]]+|\[\][A-Za-z0-9_.\[\]]+)\s*\{' -and
            $Value -notmatch '__FA_CREDENTIAL_LITERAL_\d{4}__' -and
            (Test-GoProjectedExpressionBalanced -Value ($Value -replace '^range\s+', ''))
        ) {
            return $true
        }
        if ($Value -match '^(?i:strings\.TrimSpace)\s*\(\s*&?[A-Za-z_][A-Za-z0-9_]*(?:(?:\.[A-Za-z_][A-Za-z0-9_]*)|(?:\[[^\]\r\n]+\]))*\s*\)\s*$') {
            return $true
        }
        $replaceAll = [regex]::Match(
            $Value,
            '^(?i:strings\.ReplaceAll)\s*\(\s*(?<source>&?[A-Za-z_][A-Za-z0-9_]*(?:(?:\.[A-Za-z_][A-Za-z0-9_]*)|(?:\[[^\]\r\n]+\]))*)\s*,\s*(?<old>__FA_CREDENTIAL_LITERAL_\d{4}__)\s*,\s*(?<new>__FA_CREDENTIAL_LITERAL_\d{4}__)\s*\)\s*$'
        )
        if (
            $replaceAll.Success -and
            $null -ne $LiteralValues -and
            $LiteralValues.Contains($replaceAll.Groups['old'].Value) -and
            $LiteralValues.Contains($replaceAll.Groups['new'].Value)
        ) {
            $oldValue = [string]$LiteralValues[$replaceAll.Groups['old'].Value].Value
            $newValue = [string]$LiteralValues[$replaceAll.Groups['new'].Value].Value
            if (
                ($oldValue -ceq '~' -and $newValue -ceq '~0') -or
                ($oldValue -ceq '/' -and $newValue -ceq '~1') -or
                ($oldValue -ceq '~1' -and $newValue -ceq '/') -or
                ($oldValue -ceq '~0' -and $newValue -ceq '~')
            ) {
                return $true
            }
        }
        return $false
    }

    if ($Kind -ceq 'powershell') {
        if ($Value -match '^\$(?:env:)?[A-Za-z_][A-Za-z0-9_:]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*$') { return $true }
        if ($Value -match '^(?i)\[SecretRef\]\s*\$[A-Za-z_][A-Za-z0-9_]*$') { return $true }
        if ($Value -match '^(?i)Resolve-SecretRef\s+-Name\s+\$[A-Za-z_][A-Za-z0-9_]*$') { return $true }
        if ($Value -match '^(?i)\[string\]\s*\$[A-Za-z_][A-Za-z0-9_:]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*$') { return $true }
        if (
            $Value -match '^(?i)(?:\[Environment\]::|Environment\.)GetEnvironmentVariable\(\s*\$?[A-Za-z_][A-Za-z0-9_:]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*\s*(?:,\s*[\x27\x22]Process[\x27\x22])?\s*\)(?:\s*\?\?\s*[\x27\x22]{2})?$'
        ) {
            return $true
        }
        return $false
    }

    if ($Kind -ceq 'sql') {
        $expression = $Value.Trim()
        $expression = [regex]::Replace($expression, '(?i)\s+(?:AND|OR)\s*$', '')
        while ($expression.EndsWith(')', [StringComparison]::Ordinal)) {
            $opening = @($expression.ToCharArray() | Where-Object { $_ -eq '(' }).Count
            $closing = @($expression.ToCharArray() | Where-Object { $_ -eq ')' }).Count
            if ($closing -le $opening) { break }
            $expression = $expression.Substring(0, $expression.Length - 1).TrimEnd()
        }
        $identifier = '[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?'
        if ($expression -match ('^' + $identifier + '(?:\s*[-+]\s*(?:0|[1-9][0-9]*))?$')) {
            return $true
        }
        if ($expression -match ('^(?i:lower|upper|length)\s*\(\s*' + $identifier + '\s*\)$')) {
            return $true
        }
        return $false
    }

    return $false
}

function Split-GoProjectedTopLevelExpressions {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value)

    $parts = New-Object 'System.Collections.Generic.List[string]'
    $roundDepth = 0
    $squareDepth = 0
    $curlyDepth = 0
    $start = 0
    for ($index = 0; $index -lt $Value.Length; $index++) {
        $character = $Value[$index]
        if ($character -eq '(') { $roundDepth++; continue }
        if ($character -eq '[') { $squareDepth++; continue }
        if ($character -eq '{') { $curlyDepth++; continue }
        if ($character -eq ')') {
            if ($roundDepth -gt 0) { $roundDepth-- }
            continue
        }
        if ($character -eq ']') {
            if ($squareDepth -gt 0) { $squareDepth-- }
            continue
        }
        if ($character -eq '}') {
            if ($curlyDepth -gt 0) { $curlyDepth-- }
            continue
        }
        if (
            $character -eq ',' -and
            $roundDepth -eq 0 -and
            $squareDepth -eq 0 -and
            $curlyDepth -eq 0
        ) {
            $parts.Add($Value.Substring($start, $index - $start).Trim())
            $start = $index + 1
        }
    }
    $parts.Add($Value.Substring($start).Trim())
    return $parts.ToArray()
}

function Get-GoCredentialFirstExpression {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value)

    $roundDepth = 0
    $squareDepth = 0
    $curlyDepth = 0
    for ($index = 0; $index -lt $Value.Length; $index++) {
        $character = $Value[$index]
        if ($character -eq ',' -and $roundDepth -eq 0 -and $squareDepth -eq 0 -and $curlyDepth -eq 0) {
            return $Value.Substring(0, $index)
        }
        if ($character -eq '(') { $roundDepth++; continue }
        if ($character -eq '[') { $squareDepth++; continue }
        if ($character -eq '{') { $curlyDepth++; continue }
        if ($character -eq ')') {
            if ($roundDepth -eq 0) { return $Value.Substring(0, $index) }
            $roundDepth--
            continue
        }
        if ($character -eq ']') {
            if ($squareDepth -eq 0) { return $Value.Substring(0, $index) }
            $squareDepth--
            continue
        }
        if ($character -eq '}') {
            if ($curlyDepth -eq 0) { return $Value.Substring(0, $index) }
            $curlyDepth--
        }
    }
    return $Value
}

function Get-GoProjectedDelimiterState {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value)

    $stack = New-Object 'System.Collections.Generic.Stack[char]'
    foreach ($character in $Value.ToCharArray()) {
        if ($character -eq '(' -or $character -eq '[' -or $character -eq '{') {
            $stack.Push($character)
            continue
        }
        if ($character -ne ')' -and $character -ne ']' -and $character -ne '}') {
            continue
        }
        if ($stack.Count -eq 0) { return 'Invalid' }
        $opening = $stack.Pop()
        if (
            ($character -eq ')' -and $opening -ne '(') -or
            ($character -eq ']' -and $opening -ne '[') -or
            ($character -eq '}' -and $opening -ne '{')
        ) {
            return 'Invalid'
        }
    }
    if ($stack.Count -gt 0) { return 'Open' }
    return 'Complete'
}

function Convert-GoTypedDeclarationSegment {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text)

    $targetMatch = [regex]::Match(
        $Text,
        '^(?<leading>\s*)(?<targets>[A-Za-z_][A-Za-z0-9_]*(?:\s*,\s*[A-Za-z_][A-Za-z0-9_]*)*)\s+'
    )
    if (-not $targetMatch.Success) { return $Text }
    if ($targetMatch.Groups['targets'].Value -match '^(?:var|const)$') { return $Text }

    $roundDepth = 0
    $squareDepth = 0
    $curlyDepth = 0
    for ($index = $targetMatch.Length; $index -lt $Text.Length; $index++) {
        $character = $Text[$index]
        if ($character -eq [char]40) {
            $roundDepth++
            continue
        }
        if ($character -eq [char]91) {
            $squareDepth++
            continue
        }
        if ($character -eq [char]123) {
            $curlyDepth++
            continue
        }
        if ($character -eq [char]41) {
            if ($roundDepth -gt 0) { $roundDepth-- }
            continue
        }
        if ($character -eq [char]93) {
            if ($squareDepth -gt 0) { $squareDepth-- }
            continue
        }
        if ($character -eq [char]125) {
            if ($curlyDepth -gt 0) { $curlyDepth-- }
            continue
        }
        if (
            $character -ne [char]61 -or
            $roundDepth -ne 0 -or
            $squareDepth -ne 0 -or
            $curlyDepth -ne 0
        ) {
            continue
        }
        $previous = if ($index -gt 0) { $Text[$index - 1] } else { [char]0 }
        $next = if (($index + 1) -lt $Text.Length) { $Text[$index + 1] } else { [char]0 }
        if (
            $previous -eq [char]58 -or
            $previous -eq [char]33 -or
            $previous -eq [char]60 -or
            $previous -eq [char]62 -or
            $previous -eq [char]61 -or
            $next -eq [char]61
        ) {
            continue
        }
        $typeText = $Text.Substring($targetMatch.Length, $index - $targetMatch.Length).Trim()
        if (
            $typeText.Length -eq 0 -or
            $typeText -notmatch '^(?:[A-Za-z_*[(]|<-chan\b)'
        ) {
            return $Text
        }
        $targetEnd = $targetMatch.Groups['targets'].Index + $targetMatch.Groups['targets'].Length
        return $Text.Substring(0, $targetEnd) + ' =' + $Text.Substring($index + 1)
    }
    return $Text
}

function Convert-GoRawPrefixedTypedDeclarationSegment {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text)

    $prefixMatch = [regex]::Match(
        $Text,
        '^(?<prefix>\s*(?:(?://|/\*+|\*+|#)\s*)*)'
    )
    $prefixLength = $prefixMatch.Groups['prefix'].Length
    $tail = $Text.Substring($prefixLength)
    return $Text.Substring(0, $prefixLength) + (Convert-GoTypedDeclarationSegment -Text $tail)
}

function Convert-GoGroupedDeclarationBody {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text,
        [bool]$RawStringContent = $false
    )

    $builder = New-Object Text.StringBuilder
    $segmentStart = 0
    $roundDepth = 0
    $squareDepth = 0
    $curlyDepth = 0
    for ($index = 0; $index -lt $Text.Length; $index++) {
        $character = $Text[$index]
        if ($character -eq [char]40) {
            $roundDepth++
        } elseif ($character -eq [char]91) {
            $squareDepth++
        } elseif ($character -eq [char]123) {
            $curlyDepth++
        } elseif ($character -eq [char]41) {
            if ($roundDepth -gt 0) { $roundDepth-- }
        } elseif ($character -eq [char]93) {
            if ($squareDepth -gt 0) { $squareDepth-- }
        } elseif ($character -eq [char]125) {
            if ($curlyDepth -gt 0) { $curlyDepth-- }
        }
        if (
            ($character -eq [char]59 -or $character -eq [char]10 -or $character -eq [char]13) -and
            $roundDepth -eq 0 -and
            $squareDepth -eq 0 -and
            $curlyDepth -eq 0
        ) {
            $segment = $Text.Substring($segmentStart, $index - $segmentStart)
            $converted = if ($RawStringContent) {
                Convert-GoRawPrefixedTypedDeclarationSegment -Text $segment
            } else {
                Convert-GoTypedDeclarationSegment -Text $segment
            }
            [void]$builder.Append($converted)
            [void]$builder.Append($character)
            $segmentStart = $index + 1
        }
    }
    if ($segmentStart -lt $Text.Length) {
        $segment = $Text.Substring($segmentStart)
        $converted = if ($RawStringContent) {
            Convert-GoRawPrefixedTypedDeclarationSegment -Text $segment
        } else {
            Convert-GoTypedDeclarationSegment -Text $segment
        }
        [void]$builder.Append($converted)
    }
    return $builder.ToString()
}

function Convert-GoCredentialDeclarationProjection {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text,
        [bool]$GroupedDeclarationLine = $false,
        [bool]$RawStringContent = $false
    )

    $result = [regex]::Replace(
        $Text,
        '\b(?<declaration>var|const)\s+(?<targets>[A-Za-z_][A-Za-z0-9_]*(?:\s*,\s*[A-Za-z_][A-Za-z0-9_]*)*)\s+(?<type>[^:=\r\n]+?)\s*=(?!=)',
        '${declaration} ${targets} ='
    )
    if (-not $GroupedDeclarationLine -and -not $RawStringContent) { return $result }

    $builder = New-Object Text.StringBuilder
    $cursor = 0
    $groupPattern = New-Object Text.RegularExpressions.Regex('\b(?:var|const)\s*\(')
    while ($cursor -lt $result.Length) {
        $match = $groupPattern.Match($result, $cursor)
        if (-not $match.Success) {
            [void]$builder.Append((Convert-GoGroupedDeclarationBody -Text $result.Substring($cursor) -RawStringContent $RawStringContent))
            $cursor = $result.Length
            break
        }
        if ($match.Index -gt $cursor) {
            [void]$builder.Append((Convert-GoGroupedDeclarationBody -Text $result.Substring($cursor, $match.Index - $cursor) -RawStringContent $RawStringContent))
        }
        $openingIndex = $result.IndexOf([char]40, $match.Index)
        [void]$builder.Append($result.Substring($match.Index, $openingIndex - $match.Index + 1))
        $depth = 1
        $closingIndex = -1
        for ($index = $openingIndex + 1; $index -lt $result.Length; $index++) {
            if ($result[$index] -eq [char]40) {
                $depth++
            } elseif ($result[$index] -eq [char]41) {
                $depth--
                if ($depth -eq 0) {
                    $closingIndex = $index
                    break
                }
            }
        }
        $bodyEnd = if ($closingIndex -ge 0) { $closingIndex } else { $result.Length }
        $body = $result.Substring($openingIndex + 1, $bodyEnd - $openingIndex - 1)
        [void]$builder.Append((Convert-GoGroupedDeclarationBody -Text $body -RawStringContent $RawStringContent))
        if ($closingIndex -lt 0) {
            $cursor = $result.Length
            break
        }
        [void]$builder.Append([char]41)
        $cursor = $closingIndex + 1
    }
    if ($cursor -eq 0 -and $result.Length -eq 0) { return '' }
    return $builder.ToString()
}

function Test-GoCredentialAssignmentNeedsContinuation {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text,
        [bool]$GroupedDeclarationLine = $false,
        [bool]$RawStringContent = $false
    )

    $projection = ConvertTo-GoCredentialProjection -Text $Text
    $scanText = Convert-GoCredentialDeclarationProjection -Text $projection.Text -GroupedDeclarationLine $GroupedDeclarationLine -RawStringContent $RawStringContent

    foreach ($tuple in [regex]::Matches(
        $scanText,
        '(?<left>[A-Za-z_][A-Za-z0-9_]*(?:\s*,\s*[A-Za-z_][A-Za-z0-9_]*)+)\s*(?::=|=(?!=))\s*(?<right>[^;\r\n]+)'
    )) {
        $sensitiveTarget = @(
            $tuple.Groups['left'].Value.Split([char]44) |
                ForEach-Object { $_.Trim() } |
                Where-Object { Test-CredentialVariableName -Name $_ }
        ).Count -gt 0
        if (-not $sensitiveTarget) { continue }
        $expression = (Get-GoCredentialFirstExpression -Value $tuple.Groups['right'].Value).Trim()
        if ((Get-GoProjectedDelimiterState -Value $expression) -ceq 'Open') { return $true }
    }

    foreach ($indexed in [regex]::Matches(
        $scanText,
        '[A-Za-z_][A-Za-z0-9_.]*\s*\[\s*(?:"(?<quotedKey>[A-Za-z][A-Za-z0-9_-]*)"|(?<keyMarker>__FA_CREDENTIAL_LITERAL_\d{4}__))\s*\]\s*(?:\+=|:=|=(?!=))\s*(?<right>[^;#\r\n]*)'
    )) {
        $key = if ($indexed.Groups['quotedKey'].Success) {
            $indexed.Groups['quotedKey'].Value
        } elseif ($projection.Literals.Contains($indexed.Groups['keyMarker'].Value)) {
            [string]$projection.Literals[$indexed.Groups['keyMarker'].Value].Value
        } else {
            ''
        }
        if ($key.Length -eq 0 -or -not (Test-CredentialVariableName -Name $key)) { continue }
        $expression = (Get-GoCredentialFirstExpression -Value $indexed.Groups['right'].Value).Trim()
        if ($expression.Length -eq 0 -or (Get-GoProjectedDelimiterState -Value $expression) -ceq 'Open') { return $true }
    }

    $assignmentPattern = '(?i)(?=(?:(?<![A-Za-z0-9_.\-\x27\x22$])(?<nameQuote>[\x27\x22])(?<quotedName>[A-Za-z][A-Za-z0-9_-]*)\k<nameQuote>|(?<![A-Za-z0-9_.\-\x27\x22$])(?<plainTarget>\$?[A-Za-z_][A-Za-z0-9_-]*(?:\.\$?[A-Za-z_][A-Za-z0-9_-]*)*))(?![A-Za-z0-9_-])\s*(?<operator>:=|\+=|=(?!=)|:(?!=))\s*(?:(?<valueQuote>[\x27\x22])(?<quotedValue>[^\r\n]*?)\k<valueQuote>(?=\s*(?:[,;}\]#]|$))|(?<unquotedValue>[^;#\r\n]*)))'
    foreach ($match in [regex]::Matches($scanText, $assignmentPattern)) {
        $name = if ($match.Groups['quotedName'].Success) {
            $match.Groups['quotedName'].Value
        } else {
            @($match.Groups['plainTarget'].Value.TrimStart([char]36).Split([char]46))[-1].TrimStart([char]36)
        }
        if (-not (Test-CredentialVariableName -Name $name)) { continue }
        if (
            $match.Groups['operator'].Value -ceq ':' -and
            $scanText -match '^\s*case\b.*:\s*$'
        ) {
            continue
        }
        if ($match.Groups['valueQuote'].Success) { continue }
        $expression = (Get-GoCredentialFirstExpression -Value $match.Groups['unquotedValue'].Value).Trim()
        if ($expression.Length -eq 0 -or (Get-GoProjectedDelimiterState -Value $expression) -ceq 'Open') { return $true }
    }
    return $false
}

function Test-CredentialAssignmentValueSafe {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$RawValue,
        [Parameter(Mandatory = $true)][string]$Kind,
        [Collections.IDictionary]$LiteralValues,
        [bool]$WasQuoted = $false
    )

    $value = if ($Kind -ceq 'go') {
        (Get-GoCredentialFirstExpression -Value $RawValue).Trim()
    } else {
        $RawValue.Trim()
    }
    if ($Kind -ceq 'go') {
        # The line grammar intentionally stops at commas.  Remove only closing
        # delimiters that belong to an outer call/composite literal; balanced
        # delimiters inside the RHS remain untouched.
        while ($value.Length -gt 0) {
            $last = $value[$value.Length - 1]
            $opening = if ($last -eq ')') { '(' } elseif ($last -eq '}') { '{' } elseif ($last -eq ']') { '[' } else { '' }
            if (-not $opening) { break }
            $openCount = @($value.ToCharArray() | Where-Object { $_ -eq $opening }).Count
            $closeCount = @($value.ToCharArray() | Where-Object { $_ -eq $last }).Count
            if ($closeCount -le $openCount) { break }
            $value = $value.Substring(0, $value.Length - 1).TrimEnd()
        }
    }
    if ($value.Length -eq 0) { return $true }
    if (Test-KnownCredentialSafeValue -Value $value) { return $true }

    if ($WasQuoted) {
        return $false
    }
    if ($null -ne $LiteralValues -and $LiteralValues.Contains($value)) {
        $literal = $LiteralValues[$value]
        if (-not $literal.Closed) { return $false }
        $literalValue = [string]$literal.Value
        return $literalValue.Length -eq 0 -or (Test-KnownCredentialSafeValue -Value $literalValue)
    }

    if ($value -match '^(?i:null|nil)$') { return $true }
    if (Test-CredentialDynamicReference -Value $value -Kind $Kind -LiteralValues $LiteralValues) { return $true }

    # Env interpolation and explicit secret-reference placeholders are safe
    # indirections in config formats.  Anything else, including malformed or
    # partially concatenated literals, is unknown and therefore secret.
    if ($value -match '^\$\{[A-Z][A-Z0-9_]{1,63}\}$') { return $true }
    if ($value -match '^\{\{\s*(?:secretRef|env)\.[A-Za-z0-9_.-]+\s*\}\}$') { return $true }
    if ($value -match '^(?i)!secretref\s+[A-Za-z0-9_.:/-]+$') { return $true }
    return $false
}

function Test-GoExactSecretReferenceExpression {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value,
        [Collections.IDictionary]$LiteralValues
    )

    $compact = [regex]::Replace($Value, '\s+', '')
    if (-not (Test-GoProjectedExpressionBalanced -Value $compact)) { return $false }

    $markerPattern = '__FA_CREDENTIAL_LITERAL_\d{4}__'
    $qualifiedReferenceType = '(?:[A-Za-z_][A-Za-z0-9_]*\.)?SecretRef'
    $referenceSlice = '\[\]' + $qualifiedReferenceType + '(?:\{\}|\(nil\))'
    $referenceSource = '[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*'
    $appendFirst = '(?:' + $referenceSlice + '|' + $referenceSource + ')'
    $appendFollowing = $referenceSource + '\.\.\.'
    $appendPattern = 'append\(' + $appendFirst + '(?:,' + $appendFollowing + ')+\)'
    if ($compact -match ('^' + $appendPattern + '$')) {
        return $true
    }
    $mapPattern = 'map\[string\]any\{(?<body>[^{}]*)\}'
    $mapMatches = @([regex]::Matches($compact, $mapPattern))
    if ($mapMatches.Count -eq 0) { return $false }
    $requiredKeys = @('name', 'provider', 'scope', 'scope_id', 'version')
    foreach ($mapMatch in $mapMatches) {
        $body = $mapMatch.Groups['body'].Value
        $pairPattern = '(?<key>' + $markerPattern + '):(?<value>' + $markerPattern + '|[A-Za-z_][A-Za-z0-9_.]*)'
        $pairs = @([regex]::Matches($body, $pairPattern))
        if ($pairs.Count -ne $requiredKeys.Count) { return $false }
        $remainder = [regex]::Replace($body, $pairPattern, 'PAIR')
        if ($remainder -notmatch '^(?:PAIR,){4}PAIR,?$') { return $false }
        $actualKeys = New-Object 'System.Collections.Generic.List[string]'
        foreach ($pair in $pairs) {
            $marker = $pair.Groups['key'].Value
            if ($null -eq $LiteralValues -or -not $LiteralValues.Contains($marker)) { return $false }
            $literal = $LiteralValues[$marker]
            if (-not $literal.Closed) { return $false }
            $actualKeys.Add([string]$literal.Value)
        }
        $actual = $actualKeys.ToArray()
        [Array]::Sort($actual, [StringComparer]::Ordinal)
        for ($index = 0; $index -lt $requiredKeys.Count; $index++) {
            if ($actual[$index] -cne $requiredKeys[$index]) { return $false }
        }
    }
    $skeleton = [regex]::Replace($compact, $mapPattern, 'REFMAP')
    return $skeleton -match '^(?:REFMAP|\[\]any\{(?:REFMAP,?)+\})$'
}

function Get-GoCredentialDeclaredTypeMap {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text,
        [AllowEmptyString()][string]$GroupedDeclarationKind = ''
    )

    $types = New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal)
    $pattern = '(?m)(?:^|[;(\r\n])\s*(?:(?<declaration>var|const)\s+)?(?<target>[A-Za-z_][A-Za-z0-9_]*)\s+(?<type>[A-Za-z_][A-Za-z0-9_.]*)\s*=(?!=)'
    foreach ($match in [regex]::Matches($Text, $pattern)) {
        $target = $match.Groups['target'].Value
        $declaredType = $match.Groups['type'].Value
        $declarationKind = if ($match.Groups['declaration'].Success) {
            $match.Groups['declaration'].Value
        } else {
            $GroupedDeclarationKind
        }
        if ($types.ContainsKey($target)) {
            if (
                $types[$target].Type -cne $declaredType -or
                $types[$target].Kind -cne $declarationKind
            ) {
                $types[$target] = [pscustomobject]@{ Type = ''; Kind = '' }
            }
        } else {
            $types.Add($target, [pscustomobject]@{ Type = $declaredType; Kind = $declarationKind })
        }
    }
    return ,$types
}

function Get-CredentialSemanticName {
    param([Parameter(Mandatory = $true)][string]$Name)

    $componentText = [regex]::Replace($Name, '([A-Z]+)([A-Z][a-z])', '$1_$2')
    $componentText = [regex]::Replace($componentText, '([a-z0-9])([A-Z])', '$1_$2')
    return $componentText.Replace('-', '_').ToLowerInvariant()
}

function Test-GoNonCredentialSemanticAssignment {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value,
        [Collections.IDictionary]$LiteralValues,
        [bool]$WasQuoted = $false
    )

    $semanticName = Get-CredentialSemanticName -Name $Name
    $literal = $null
    if ($WasQuoted) {
        $literal = $Value
    } elseif ($null -ne $LiteralValues -and $LiteralValues.Contains($Value)) {
        $candidate = $LiteralValues[$Value]
        if ($candidate.Closed) {
            $literal = [string]$candidate.Value
        }
    }

    # These fields are explicit products of the secret-removal boundary. They
    # may contain arbitrary public request/config bytes, but never credential
    # material by contract. Raw SecretValue-like targets remain fail-closed.
    if (
        $semanticName -match '(^|_)non_secret($|_)' -or
        $semanticName -match '(^|_)secret_free($|_)'
    ) {
        return $true
    }

    # Digests and authorization evidence hashes are one-way metadata. Invalid
    # literal values are also safe to keep in negative test fixtures.
    if (
        $semanticName -match 'secret.*(?:hash|digest)$' -or
        $semanticName -match 'authorization.*(?:hash|digest)$'
    ) {
        return $true
    }

    # Credential-shaped revision counters and byte-size declarations are
    # public shape metadata only when the semantic suffix is exact and the RHS
    # is a plain decimal integer.  Do not accept quoted values, expressions, or
    # near-name suffixes: those continue through the fail-closed path below.
    if (
        $null -eq $literal -and
        $semanticName -match '(?:^|_)(?:revision|bytes)(?:_v[1-9][0-9]*)?$' -and
        $Value -match '^(?:0|[1-9][0-9]*)$'
    ) {
        return $true
    }

    # Counts, limits, byte caps, and estimates describe shape/cost only.
    if (
        $semanticName -match '(?:^|_)(?:max_)?(?:exact_)?secret_(?:ref|refs|binding|bindings).*(?:count|limit|bytes|estimate)?$' -and
        $Value -match '^(?:0|[1-9][0-9]*)(?:\s*<<\s*(?:0|[1-9][0-9]*))?$'
    ) {
        return $true
    }
    if (
        $semanticName -match '^(?:max|maximum)_(?:resolved_)?(?:api_key|secret|password|auth_token|access_token)_(?:bytes|count|limit)$' -and
        $Value -match '^(?:0|[1-9][0-9]*)(?:\s*<<\s*(?:0|[1-9][0-9]*))?$'
    ) {
        return $true
    }
    if (
        $semanticName -match '^(?:default_)?[a-z0-9_]*(?:api_key|secret|credential)_environment(?:_name)?$' -and
        $null -ne $literal -and
        $literal -match '^[A-Z_][A-Z0-9_]{2,127}$'
    ) {
        return $true
    }
    if ($semanticName -eq 'api_key_resolver' -and $null -eq $literal) {
        $resolverExpression = [regex]::Replace($Value, '\s+', '')
        if (
            $resolverExpression -match '^(?:deepseekmodel|zhipumodel)\.APIKeyResolverFunc\(func\(' -and
            (Test-GoProjectedExpressionBalanced -Value $resolverExpression)
        ) {
            foreach ($byteLiteral in [regex]::Matches(
                $resolverExpression,
                '\[\]byte\((?<marker>__FA_CREDENTIAL_LITERAL_\d{4}__)\)'
            )) {
                $marker = $byteLiteral.Groups['marker'].Value
                if (
                    $null -eq $LiteralValues -or
                    -not $LiteralValues.Contains($marker) -or
                    -not $LiteralValues[$marker].Closed -or
                    -not (Test-KnownCredentialSafeValue -Value ([string]$LiteralValues[$marker].Value))
                ) {
                    return $false
                }
            }
            return $true
        }
    }
    # The reported cache-hit percentage is usage-accounting metadata, not a
    # credential container. Keep the exemption tied to the exact production
    # syntax: a broad token-name or fmt.Sprintf allowance would weaken the
    # fail-closed scanner.
    if ($semanticName -eq 'token_weighted_cache_hit_rate') {
        if ($null -ne $literal) {
            return [string]::Equals($literal, '0.000000%', [StringComparison]::Ordinal)
        }
        $rateExpression = [regex]::Replace($Value, '\s+', '')
        $rateMatch = [regex]::Match(
            $rateExpression,
            '^fmt\.Sprintf\((?<format>__FA_CREDENTIAL_LITERAL_\d{4}__),100\*float64\(result\.CachedInputTokens\)/float64\(result\.InputTokens\),?\)$'
        )
        if (
            $rateMatch.Success -and
            $null -ne $LiteralValues -and
            $LiteralValues.Contains($rateMatch.Groups['format'].Value)
        ) {
            $formatLiteral = $LiteralValues[$rateMatch.Groups['format'].Value]
            return (
                $formatLiteral.Closed -and
                [string]::Equals([string]$formatLiteral.Value, '%.6f%%', [StringComparison]::Ordinal)
            )
        }
        return $false
    }
    if (
        $semanticName -match '(?:^|_)token_estimate$' -and
        (
            $Value -match '^(?:0|[1-9][0-9]*)$' -or
            $Value -match '^Estimate[A-Za-z0-9_]*Tokens\s*\('
        )
    ) {
        return $true
    }
    if (
        @('secret_binding_count', 'secret_ref_count') -contains $semanticName -and
        $Value -match '^(?:u?int(?:8|16|32|64)?\s*\(|[A-Za-z_][A-Za-z0-9_.]*\.Count\s*\()'
    ) {
        return $true
    }

    # Exact domain separators and public enum values are protocol metadata.
    if (
        @(
            'exact_secret_ref_version_domain',
            'exact_secret_ref_version_set_domain',
            'startup_secret_provider_binding_domain',
            'startup_exact_secret_binding_set_domain'
        ) -contains $semanticName
    ) {
        return $null -ne $literal -and $literal -match '^[A-Za-z0-9._:/-]{1,128}$'
    }
    $publicEnums = New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::Ordinal)
    $publicEnums.Add('startup_secret_version_exact_immutable_v1', 'EXACT_IMMUTABLE_VERSION_V1')
    $publicEnums.Add('unified_model_reported_error_authorization', 'AUTHORIZATION')
    $publicEnums.Add('attachment_kind_secret_ref', 'SECRET_REF')
    if ($publicEnums.ContainsKey($semanticName)) {
        return $null -ne $literal -and $literal -ceq $publicEnums[$semanticName]
    }

    # Reference/binding containers carry only locator metadata. Accept typed
    # constructions, clones, appends, and exact set factories; a direct string
    # assigned to SecretRef/SecretBindings is still rejected.
    $referenceTarget = (
        $semanticName -match 'secret_(?:ref|refs|binding|bindings|provider_binding|provider_bindings|ref_version_set|provider_binding_set)' -or
        @('secret_set', 'secret_bindings', 'exact_secret_bindings') -contains $semanticName
    )
    if (-not $referenceTarget -or $null -ne $literal) {
        return $false
    }
    $compact = [regex]::Replace($Value, '\s+', '')
    while ($compact.Length -gt 0) {
        $last = $compact[$compact.Length - 1]
        $opening = if ($last -eq ')') { '(' } elseif ($last -eq '}') { '{' } elseif ($last -eq ']') { '[' } else { '' }
        if (-not $opening) { break }
        $openCount = @($compact.ToCharArray() | Where-Object { $_ -eq $opening }).Count
        $closeCount = @($compact.ToCharArray() | Where-Object { $_ -eq $last }).Count
        if ($closeCount -le $openCount) { break }
        $compact = $compact.Substring(0, $compact.Length - 1)
    }
    if (-not (Test-GoProjectedExpressionBalanced -Value $compact)) {
        return $false
    }
    return $compact -match '(?i)(?:SecretRef|SecretBinding|secretSet|secretBindings|secretRefs)'
}

function Test-CredentialSemanticMetadataAssignmentSafe {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value,
        [Collections.IDictionary]$LiteralValues,
        [object]$GoDeclaredTypes,
        [bool]$WasQuoted = $false,
        [bool]$IsGo = $false
    )

    if (-not $WasQuoted) {
        if (
            [string]::Equals($Name, 'TokenClass', [StringComparison]::Ordinal) -and
            $Value -cmatch '^INPUT\s*\|\s*CACHE_HIT_INPUT\s*\|\s*CACHE_MISS_INPUT\s*\|\s*OUTPUT\s*\|\s*REASONING$'
        ) {
            return $true
        }
        if (
            [string]::Equals($Name, 'AuthorizationResult', [StringComparison]::Ordinal) -and
            $Value -cmatch '^(?:VALID|INVALID)(?:\s*,\s*ModelUnknownOperatorDecisionObservationHash)?$'
        ) {
            return $true
        }
        if (
            [string]::Equals($Name, 'authorization_result', [StringComparison]::OrdinalIgnoreCase) -and
            ($Value -ieq 'valid' -or $Value -ieq 'invalid')
        ) {
            return $true
        }
    }
    $semanticName = Get-CredentialSemanticName -Name $Name
    if (
        -not $IsGo -and
        $semanticName -eq 'secret_present' -and
        $Value -match '(?i)^\$?(?:true|false)\s*,?$'
    ) {
        return $true
    }
    if (
        -not $IsGo -and
        $WasQuoted -and
        $semanticName -match '^(?:secret|api_key|credential)_(?:environment_)?name$' -and
        $Value -match '^[A-Z_][A-Z0-9_]{2,127}$'
    ) {
        return $true
    }
    if ($IsGo -and (
        Test-GoNonCredentialSemanticAssignment `
            -Name $Name `
            -Value $Value `
            -LiteralValues $LiteralValues `
            -WasQuoted $WasQuoted
    )) {
        return $true
    }
    if (-not $IsGo -or $WasQuoted) { return $false }

    if (
        (
            [string]::Equals($Name, 'MaxSkillMDTokenEstimate', [StringComparison]::Ordinal) -or
            [string]::Equals($Name, 'MaxSecretValueBytes', [StringComparison]::Ordinal)
        ) -and
        $Value -match '^(?:0|[1-9][0-9]*)(?:\s*<<\s*(?:0|[1-9][0-9]*))?$'
    ) {
        return $true
    }

    $errorMetadata = @(
        [pscustomobject]@{ Name = 'ErrAPIKeyResolve'; Message = 'deepseekmodel: API key resolution failed' },
        [pscustomobject]@{ Name = 'ErrAPIKeyResolve'; Message = 'zhipumodel: API key resolution failed' },
        [pscustomobject]@{ Name = 'ErrSecretValueInvalid'; Message = 'moduleapi: invalid SecretValue' },
        [pscustomobject]@{ Name = 'ErrSecretValueDestroyed'; Message = 'moduleapi: SecretValue is destroyed' },
        [pscustomobject]@{ Name = 'ErrSecretValueSerialization'; Message = 'moduleapi: SecretValue serialization is forbidden' },
        [pscustomobject]@{ Name = 'ErrSecretScopeMismatch'; Message = 'moduleapi: secret scope mismatch' }
    )
    $errorConstructor = [regex]::Match(
        $Value,
        '^errors\.New\(\s*(?<literal>__FA_CREDENTIAL_LITERAL_\d{4}__)\s*\)$'
    )
    if ($errorConstructor.Success -and $null -ne $LiteralValues) {
        $marker = $errorConstructor.Groups['literal'].Value
        if ($LiteralValues.Contains($marker) -and $LiteralValues[$marker].Closed) {
            $message = [string]$LiteralValues[$marker].Value
            foreach ($entry in $errorMetadata) {
                if (
                    [string]::Equals($Name, $entry.Name, [StringComparison]::Ordinal) -and
                    [string]::Equals($message, $entry.Message, [StringComparison]::Ordinal)
                ) {
                    return $true
                }
            }
        }
    }

    if ($null -eq $LiteralValues -or $null -eq $GoDeclaredTypes) { return $false }
    $scopeMetadata = @(
        [pscustomobject]@{ Name = 'SecretTenant'; Value = 'TENANT' },
        [pscustomobject]@{ Name = 'SecretWorkspace'; Value = 'WORKSPACE' },
        [pscustomobject]@{ Name = 'SecretAgent'; Value = 'AGENT' },
        [pscustomobject]@{ Name = 'SecretModuleInstallation'; Value = 'MODULE_INSTALLATION' }
    )
    if (
        -not $GoDeclaredTypes.ContainsKey($Name) -or
        $GoDeclaredTypes[$Name].Type -cne 'SecretScope' -or
        $GoDeclaredTypes[$Name].Kind -cne 'const'
    ) {
        return $false
    }
    if (-not $LiteralValues.Contains($Value) -or -not $LiteralValues[$Value].Closed) {
        return $false
    }
    $scopeValue = [string]$LiteralValues[$Value].Value
    foreach ($entry in $scopeMetadata) {
        if (
            [string]::Equals($Name, $entry.Name, [StringComparison]::Ordinal) -and
            [string]::Equals($scopeValue, $entry.Value, [StringComparison]::Ordinal)
        ) {
            return $true
        }
    }
    return $false
}

function Test-StrictStableUriDecoding {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value,
        [Parameter(Mandatory = $true)][ref]$DecodedValue
    )

    $current = $Value
    for ($pass = 0; $pass -lt 8; $pass++) {
        if ($current.IndexOf([char]37) -lt 0) {
            $DecodedValue.Value = $current
            return $true
        }
        if ([regex]::IsMatch($current, '%(?![0-9A-Fa-f]{2})')) {
            return $false
        }
        try {
            $next = [Uri]::UnescapeDataString($current)
        } catch {
            return $false
        }
        if ([string]::Equals($next, $current, [StringComparison]::Ordinal)) {
            return $false
        }
        $current = $next
    }
    if ($current.IndexOf([char]37) -ge 0) {
        return $false
    }
    $DecodedValue.Value = $current
    return $true
}

function Test-CredentialMetadataAssignmentSafe {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$RawValue,
        [Collections.IDictionary]$LiteralValues,
        [object]$GoDeclaredTypes,
        [bool]$WasQuoted = $false,
        [AllowEmptyString()][string]$RelativePath = ''
    )

    $isGo = [IO.Path]::GetExtension($RelativePath).Equals('.go', [StringComparison]::OrdinalIgnoreCase)
    $value = if ($isGo) {
        (Get-GoCredentialFirstExpression -Value $RawValue).Trim()
    } else {
        $RawValue.Trim()
    }
    if (
        Test-CredentialSemanticMetadataAssignmentSafe `
            -Name $Name `
            -Value $value `
            -LiteralValues $LiteralValues `
            -GoDeclaredTypes $GoDeclaredTypes `
            -WasQuoted $WasQuoted `
            -IsGo $isGo
    ) {
        return $true
    }
    if (
        $isGo -and
        (Test-GoExactSecretReferenceExpression -Value $value -LiteralValues $LiteralValues)
    ) {
        return $true
    }
    if (
        $value -match '^(?:\[\s*\]|\{\s*\})\s*(?:,|}|$)'
    ) {
        return $true
    }
    if ($Name -notmatch '(?i)^(?:authorization|token)[_-]?url$') { return $false }
    if ($null -ne $LiteralValues -and $LiteralValues.Contains($value)) {
        $literal = $LiteralValues[$value]
        if (-not $literal.Closed) { return $false }
        $value = [string]$literal.Value
    } elseif (-not $WasQuoted) {
        return $false
    }

    $uri = $null
    if (-not [Uri]::TryCreate($value, [UriKind]::Absolute, [ref]$uri)) { return $false }
    if ($uri.Scheme -cne 'https' -and $uri.Scheme -cne 'http') { return $false }
    if (-not [string]::IsNullOrEmpty($uri.UserInfo)) { return $false }
    $decodedQuery = ''
    if (-not (Test-StrictStableUriDecoding -Value $uri.Query -DecodedValue ([ref]$decodedQuery))) {
        return $false
    }
    if ($decodedQuery -match '(?i)(?:^|[?&])(?:password|passwd|secret|token|authorization|api[_-]?key)=') { return $false }
    return $true
}

function Test-LicenseDeclarationMetadataAssignment {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text,
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value
    )

    if (-not [string]::Equals($Name, 'Token', [StringComparison]::OrdinalIgnoreCase)) { return $false }
    $spdxValues = @(
        'AGPL-3.0-only', 'AGPL-3.0-or-later', 'Apache-2.0',
        'BSD-2-Clause', 'BSD-3-Clause',
        'GPL-2.0-only', 'GPL-2.0-or-later',
        'GPL-3.0-only', 'GPL-3.0-or-later',
        'LGPL-2.1-only', 'LGPL-2.1-or-later',
        'LGPL-3.0-only', 'LGPL-3.0-or-later',
        'MIT', 'MPL-2.0', 'Unlicense'
    )
    if ($spdxValues -cnotcontains $Value) { return $false }

    $pattern = '(?i)^\s*@\{\s*Path\s*=\s*(?<pathQuote>[\x27\x22])[A-Za-z0-9_./-]+\k<pathQuote>\s*;\s*Token\s*=\s*(?<tokenQuote>[\x27\x22])' + [regex]::Escape($Value) + '\k<tokenQuote>\s*;\s*Link\s*=\s*(?:\$null|(?<linkQuote>[\x27\x22])[^\r\n]*?\k<linkQuote>)\s*\}\s*,?\s*$'
    return [regex]::IsMatch($Text, $pattern)
}

function Test-CredentialAssignmentSecret {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text,
        [AllowEmptyString()][string]$RelativePath = '',
        [Collections.IDictionary]$LiteralValues,
        [bool]$GoGroupedDeclarationLine = $false,
        [AllowEmptyString()][string]$GoGroupedDeclarationKind = '',
        [bool]$GoRawStringContent = $false
    )

    if (
        ($Text.IndexOf([char]58) -lt 0 -and $Text.IndexOf([char]61) -lt 0) -or
        -not [regex]::IsMatch(
            $Text,
            '(?i)(?:password|passwd|secret|token|authorization|api[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret)'
        )
    ) {
        return $false
    }
    if (Test-ReviewedJavaScriptCredentialLine -Text $Text -RelativePath $RelativePath) {
        return $false
    }

    $extension = [IO.Path]::GetExtension($RelativePath).ToLowerInvariant()
    $leaf = [IO.Path]::GetFileName($RelativePath)
    $kind = if ($extension -ceq '.go') {
        'go'
    } elseif (@('.ps1', '.psm1', '.psd1') -contains $extension) {
        'powershell'
    } elseif (@('.yaml', '.yml') -contains $extension) {
        'yaml'
    } elseif ($extension -ceq '.json') {
        'json'
    } elseif ($extension -ceq '.sql') {
        'sql'
    } elseif ($leaf -ieq '.env' -or $leaf.StartsWith('.env.', [StringComparison]::OrdinalIgnoreCase) -or $extension -ceq '.env') {
        'env'
    } else {
        'generic'
    }

    $scanText = $Text
    $scanLiterals = $LiteralValues
    $scanDeclaredTypes = $null
    if ($kind -ceq 'go' -and $null -eq $LiteralValues) {
        $projection = ConvertTo-GoCredentialProjection -Text $Text
        $scanDeclaredTypes = Get-GoCredentialDeclaredTypeMap -Text $projection.Text -GroupedDeclarationKind $GoGroupedDeclarationKind
        $scanText = Convert-GoCredentialDeclarationProjection -Text $projection.Text -GroupedDeclarationLine $GoGroupedDeclarationLine -RawStringContent $GoRawStringContent
        $scanLiterals = $projection.Literals

        foreach ($tuple in [regex]::Matches(
            $scanText,
            '(?<left>[A-Za-z_][A-Za-z0-9_]*(?:\s*,\s*[A-Za-z_][A-Za-z0-9_]*)+)\s*(?::=|=(?!=))\s*(?<right>[^;\r\n]+)'
        )) {
            $leftValues = @($tuple.Groups['left'].Value.Split([char]44) | ForEach-Object { $_.Trim() })
            $rightValues = @(Split-GoProjectedTopLevelExpressions -Value $tuple.Groups['right'].Value)
            $rangeValue = $tuple.Groups['right'].Value.Trim()
            for ($tupleIndex = 0; $tupleIndex -lt $leftValues.Count; $tupleIndex++) {
                if (-not (Test-CredentialVariableName -Name $leftValues[$tupleIndex])) { continue }
                if (
                    $rangeValue.StartsWith('range ', [StringComparison]::Ordinal) -and
                    (Test-CredentialAssignmentValueSafe -RawValue $rangeValue -Kind $kind -LiteralValues $scanLiterals)
                ) {
                    continue
                }
                if ($tupleIndex -ge $rightValues.Count) {
                    return $true
                }
                $tupleValue = $rightValues[$tupleIndex]
                if (
                    Test-CredentialMetadataAssignmentSafe `
                        -Name $leftValues[$tupleIndex] `
                        -RawValue $tupleValue `
                        -LiteralValues $scanLiterals `
                        -GoDeclaredTypes $scanDeclaredTypes `
                        -RelativePath $RelativePath
                ) {
                    continue
                }
                if (-not (Test-CredentialAssignmentValueSafe -RawValue $tupleValue -Kind $kind -LiteralValues $scanLiterals)) {
                    return $true
                }
            }
        }

        foreach ($indexed in [regex]::Matches(
            $scanText,
            '[A-Za-z_][A-Za-z0-9_.]*\s*\[\s*(?:"(?<quotedKey>[A-Za-z][A-Za-z0-9_-]*)"|(?<keyMarker>__FA_CREDENTIAL_LITERAL_\d{4}__))\s*\]\s*(?:\+=|:=|=(?!=))\s*(?<right>[^;#\r\n]*)'
        )) {
            $key = if ($indexed.Groups['quotedKey'].Success) {
                $indexed.Groups['quotedKey'].Value
            } elseif ($scanLiterals.Contains($indexed.Groups['keyMarker'].Value)) {
                [string]$scanLiterals[$indexed.Groups['keyMarker'].Value].Value
            } else {
                ''
            }
            if (
                $key.Length -gt 0 -and
                (Test-CredentialVariableName -Name $key) -and
                -not (Test-CredentialMetadataAssignmentSafe -Name $key -RawValue $indexed.Groups['right'].Value -LiteralValues $scanLiterals -GoDeclaredTypes $scanDeclaredTypes -RelativePath $RelativePath) -and
                -not (Test-CredentialAssignmentValueSafe -RawValue $indexed.Groups['right'].Value -Kind $kind -LiteralValues $scanLiterals)
            ) {
                return $true
            }
        }
    }

    # The zero-width lookahead evaluates nested fields separately instead of
    # letting an outer assignment consume the rest of the line.
    $assignmentPattern = '(?i)(?=(?:(?<![A-Za-z0-9_.\-\x27\x22$])(?<nameQuote>[\x27\x22])(?<quotedName>[A-Za-z][A-Za-z0-9_-]*)\k<nameQuote>|(?<![A-Za-z0-9_.\-\x27\x22$])(?<plainTarget>\$?[A-Za-z_][A-Za-z0-9_-]*(?:\.\$?[A-Za-z_][A-Za-z0-9_-]*)*))(?![A-Za-z0-9_-])\s*(?<operator>:=|\+=|=(?!=)|:(?!=))\s*(?:(?<valueQuote>[\x27\x22])(?<quotedValue>[^\r\n]*?)\k<valueQuote>(?=\s*(?:[,;}\]#]|$))|(?<unquotedValue>[^;#\r\n]*)))'
    foreach ($match in [regex]::Matches($scanText, $assignmentPattern)) {
        $name = if ($match.Groups['quotedName'].Success) {
            $match.Groups['quotedName'].Value
        } else {
            $target = $match.Groups['plainTarget'].Value.TrimStart([char]36)
            @($target.Split([char]46))[-1].TrimStart([char]36)
        }
        if (-not (Test-CredentialVariableName -Name $name)) { continue }

        if (
            $kind -ceq 'go' -and
            $match.Groups['operator'].Value -ceq ':' -and
            $scanText -match '^\s*case\b.*:\s*$'
        ) {
            continue
        }

        $rawValue = if ($match.Groups['valueQuote'].Success) {
            $match.Groups['quotedValue'].Value
        } else {
            $match.Groups['unquotedValue'].Value
        }
        $value = $rawValue.Trim()
        if (Test-LicenseDeclarationMetadataAssignment -Text $Text -Name $name -Value $value) { continue }
        if (Test-CredentialMetadataAssignmentSafe -Name $name -RawValue $value -LiteralValues $scanLiterals -GoDeclaredTypes $scanDeclaredTypes -WasQuoted $match.Groups['valueQuote'].Success -RelativePath $RelativePath) { continue }
        if (
            @('go', 'json', 'yaml', 'powershell') -contains $kind -and
            $value.Trim().Length -eq 0
        ) {
            return $true
        }
        if (-not (Test-CredentialAssignmentValueSafe -RawValue $value -Kind $kind -LiteralValues $scanLiterals -WasQuoted $match.Groups['valueQuote'].Success)) {
            return $true
        }
    }
    return $false
}

function Test-PathIsWithinRoot {
    param(
        [Parameter(Mandatory = $true)][string]$Candidate,
        [Parameter(Mandatory = $true)][string]$CanonicalRoot
    )

    $separator = [IO.Path]::DirectorySeparatorChar
    $prefix = $CanonicalRoot.TrimEnd([char[]]@([char]92, [char]47)) + $separator
    $comparison = if ($script:IsWindowsPlatform) {
        [StringComparison]::OrdinalIgnoreCase
    } else {
        [StringComparison]::Ordinal
    }
    return $Candidate.StartsWith($prefix, $comparison)
}

function Test-ConstructionOrRuntimePath {
    param(
        [Parameter(Mandatory = $true)][string]$RelativePath,
        [Parameter(Mandatory = $true)][bool]$IsDirectory
    )

    $segments = @($RelativePath.Split([char]47))
    $leaf = $segments[$segments.Count - 1]

    foreach ($segment in $segments) {
        if ($segment -ieq '.git') {
            Add-PublicTreeFinding -Rule 'PT_CONSTRUCTION' -Path $RelativePath
            break
        }
    }

    foreach ($segment in $segments) {
        if (@('.cache', '.tools', '.construction', '.superpowers') -contains $segment) {
            Add-PublicTreeFinding -Rule 'PT_CONSTRUCTION' -Path $RelativePath
            break
        }
    }

    if (@('bin', 'data', 'logs', 'storage', 'cache', 'evidence', 'backup', 'backups', 'profiles', 'traces') -contains $segments[0] -or $segments -contains 'release-evidence') {
        Add-PublicTreeFinding -Rule 'PT_RUNTIME' -Path $RelativePath
    }

    if ($RelativePath -ieq 'docs/superpowers' -or $RelativePath.StartsWith('docs/superpowers/', [StringComparison]::OrdinalIgnoreCase)) {
        Add-PublicTreeFinding -Rule 'PT_CONSTRUCTION' -Path $RelativePath
    }
    if ($RelativePath -ieq 'docs/design' -or $RelativePath.StartsWith('docs/design/', [StringComparison]::OrdinalIgnoreCase)) {
        Add-PublicTreeFinding -Rule 'PT_CONSTRUCTION' -Path $RelativePath
    }
    if ($RelativePath -ieq 'cmd/public-baseline-gen' -or $RelativePath.StartsWith('cmd/public-baseline-gen/', [StringComparison]::OrdinalIgnoreCase)) {
        Add-PublicTreeFinding -Rule 'PT_CONSTRUCTION' -Path $RelativePath
    }

    foreach ($retiredScriptName in @(Get-RetiredConstructionScriptNames)) {
        if ([string]::Equals($leaf, $retiredScriptName, [StringComparison]::OrdinalIgnoreCase)) {
            Add-PublicTreeFinding -Rule 'PT_CONSTRUCTION' -Path $RelativePath
            break
        }
    }

    foreach ($retiredArtifactName in @(Get-RetiredBatchBArtifactNames)) {
        if ($segments -contains $retiredArtifactName) {
            Add-PublicTreeFinding -Rule 'PT_RUNTIME' -Path $RelativePath
            break
        }
    }

    $privateFiles = @(
        'DEEPSEEK_BENCHMARK_2026-07-15.md',
        'DEEPSEEK_BENCHMARK_RERUN_2026-07-15.md'
    )
    if ($privateFiles -contains $leaf) {
        Add-PublicTreeFinding -Rule 'PT_CONSTRUCTION' -Path $RelativePath
    }

    if (-not $IsDirectory) {
        $lowerLeaf = $leaf.ToLowerInvariant()
        if (
            $lowerLeaf -eq '.env' -or
            ($lowerLeaf.StartsWith('.env.') -and $lowerLeaf -ne '.env.example') -or
            $lowerLeaf.EndsWith('.pem') -or
            $lowerLeaf.EndsWith('.key') -or
            $lowerLeaf.EndsWith('.p12') -or
            $lowerLeaf.EndsWith('.pfx')
        ) {
            Add-PublicTreeFinding -Rule 'PT_SECRET_FILE' -Path $RelativePath
        }

        if (
            $lowerLeaf.EndsWith('.db') -or
            $lowerLeaf.EndsWith('.db-shm') -or
            $lowerLeaf.EndsWith('.db-wal') -or
            $lowerLeaf.EndsWith('.sqlite') -or
            $lowerLeaf.EndsWith('.sqlite3') -or
            $lowerLeaf.EndsWith('.db3') -or
            $lowerLeaf.EndsWith('.sqlite-shm') -or
            $lowerLeaf.EndsWith('.sqlite-wal') -or
            $lowerLeaf.EndsWith('.log') -or
            $lowerLeaf.EndsWith('.bak') -or
            $lowerLeaf.EndsWith('.backup') -or
            $lowerLeaf.EndsWith('.cache') -or
            $lowerLeaf.EndsWith('.evidence') -or
            $lowerLeaf.EndsWith('.profile') -or
            $lowerLeaf.EndsWith('.pprof') -or
            $lowerLeaf.EndsWith('.trace') -or
            $lowerLeaf.EndsWith('.tmp') -or
            $lowerLeaf.EndsWith('.pid') -or
            $lowerLeaf.EndsWith('.sock') -or
            $lowerLeaf.EndsWith('.test') -or
            $lowerLeaf.EndsWith('.out') -or
            $lowerLeaf.StartsWith('coverage.') -or
            $lowerLeaf -eq '$cover' -or
            $lowerLeaf -eq 'local-config' -or
            $lowerLeaf.StartsWith('local-config.') -or
            $lowerLeaf.StartsWith('config.local.')
        ) {
            Add-PublicTreeFinding -Rule 'PT_RUNTIME' -Path $RelativePath
        }

        if ($lowerLeaf.EndsWith('.php')) {
            Add-PublicTreeFinding -Rule 'PT_LEGACY_SOURCE' -Path $RelativePath
        }
    }
}

function Test-TopLevelEntry {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][bool]$IsDirectory
    )

    $allowedDirectories = @(
        '.github',
        'cmd',
        'docs',
        'examples',
        'internal',
        'schemas',
        'scripts',
        'sdk',
        'testdata',
        'third_party',
        'vendor'
    )
    $allowedFiles = @(
        '.gitattributes',
        '.gitignore',
        'CODE_OF_CONDUCT.md',
        'CONTRIBUTING.md',
        'go.mod',
        'go.sum',
        'LICENSE',
        'README.md',
        'README.zh-CN.md',
        'README.zh-TW.md',
        'SECURITY.md',
        'THIRD_PARTY_NOTICES.md',
        'VERSION'
    )

    if ($IsDirectory) {
        if ($allowedDirectories -cnotcontains $Name) {
            Add-PublicTreeFinding -Rule 'PT_TOP_LEVEL_NOT_ALLOWED' -Path $Name
        }
        return
    }
    if ($allowedFiles -cnotcontains $Name) {
        Add-PublicTreeFinding -Rule 'PT_TOP_LEVEL_NOT_ALLOWED' -Path $Name
    }
}

function Test-WindowsAlternateStreams {
    param(
        [Parameter(Mandatory = $true)][string]$LiteralPath,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )

    if (-not $script:IsWindowsPlatform) { return }
    try {
        $streams = @([PublicTreeNativeStreams]::Enumerate($LiteralPath))
        foreach ($stream in $streams) {
            if ($stream -cne '::$DATA') {
                Add-PublicTreeFinding -Rule 'PT_ADS' -Path $RelativePath
            }
        }
    } catch {
        Add-PublicTreeFinding -Rule 'PT_IO_STREAMS' -Path $RelativePath
    }
}

function Visit-PublicTreeDirectory {
    param(
        [Parameter(Mandatory = $true)][string]$DirectoryPath,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$RelativePrefix,
        [Parameter(Mandatory = $true)][string]$CanonicalRoot
    )

    try {
        $children = @(Get-ChildItem -LiteralPath $DirectoryPath -Force -ErrorAction Stop)
    } catch {
        $display = if ($RelativePrefix) { $RelativePrefix } else { '<root>' }
        Add-PublicTreeFinding -Rule 'PT_IO_ENUMERATE' -Path $display
        return
    }

    $caseNames = New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::OrdinalIgnoreCase)
    foreach ($child in $children) {
        $relative = if ($RelativePrefix) {
            $RelativePrefix + '/' + $child.Name
        } else {
            $child.Name
        }

        # VCS metadata is not part of the public payload. Only this exact root
        # entry is excluded; a nested entry with the same name is forbidden.
        if (-not $RelativePrefix -and $child.Name -ceq '.git') {
            continue
        }

        if ($caseNames.ContainsKey($child.Name)) {
            Add-PublicTreeFinding -Rule 'PT_PATH_CASE_COLLISION' -Path $relative
        } else {
            $caseNames.Add($child.Name, $relative)
        }

        try {
            $fullPath = [IO.Path]::GetFullPath($child.FullName)
        } catch {
            Add-PublicTreeFinding -Rule 'PT_PATH_UNSAFE' -Path $relative
            continue
        }
        if (-not (Test-PathIsWithinRoot -Candidate $fullPath -CanonicalRoot $CanonicalRoot)) {
            Add-PublicTreeFinding -Rule 'PT_PATH_UNSAFE' -Path $relative
            continue
        }

        try {
            $isReparse = ($child.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0
        } catch {
            Add-PublicTreeFinding -Rule 'PT_IO_ATTRIBUTES' -Path $relative
            continue
        }
        if ($isReparse) {
            Add-PublicTreeFinding -Rule 'PT_REPARSE' -Path $relative
            continue
        }

        Test-WindowsAlternateStreams -LiteralPath $fullPath -RelativePath $relative

        $isDirectory = [bool]$child.PSIsContainer
        if (-not $RelativePrefix) {
            Test-TopLevelEntry -Name $child.Name -IsDirectory $isDirectory
        }
        Test-ConstructionOrRuntimePath -RelativePath $relative -IsDirectory $isDirectory

        if ($isDirectory) {
            $segments = @($relative.Split([char]47))
            $skipForbiddenDirectory =
                ($segments -contains '.git') -or
                ($segments -contains '.cache') -or
                ($segments -contains '.tools') -or
                ($segments -contains '.construction') -or
                ($segments -contains '.superpowers') -or
                ($segments -contains 'release-evidence') -or
                $relative -ieq 'internal/controlweb/node_modules' -or
                $relative -ieq 'internal/controlweb/.npm-cache' -or
                $relative -ieq 'internal/controlweb/.vite' -or
                (@('bin', 'data', 'logs', 'storage', 'cache', 'evidence', 'backup', 'backups', 'profiles', 'traces') -contains $segments[0]) -or
                $relative -ieq 'docs/superpowers' -or
                $relative -ieq 'docs/design' -or
                $relative -ieq 'cmd/public-baseline-gen'
            if (-not $skipForbiddenDirectory) {
                Visit-PublicTreeDirectory -DirectoryPath $fullPath -RelativePrefix $relative -CanonicalRoot $CanonicalRoot
            }
        } else {
            $canReadFile = $true
            if (-not $script:IsWindowsPlatform) {
                $unixKind = Get-UnixFileKind -LiteralPath $fullPath
                if ($unixKind -ceq 'Special') {
                    Add-PublicTreeFinding -Rule 'PT_FILE_SPECIAL' -Path $relative
                    $canReadFile = $false
                } elseif ($unixKind -cne 'Regular') {
                    Add-PublicTreeFinding -Rule 'PT_IO_FILE_TYPE' -Path $relative
                    $canReadFile = $false
                }
            }
            if (($child.Attributes -band [IO.FileAttributes]::SparseFile) -ne 0) {
                Add-PublicTreeFinding -Rule 'PT_FILE_SPECIAL' -Path $relative
            }
            if ($canReadFile) {
                $script:Files.Add([pscustomobject]@{
                    Path = $relative
                    FullPath = $fullPath
                })
            }
        }
    }
}

function Test-BinaryContent {
    param(
        [Parameter(Mandatory = $true)][byte[]]$Bytes,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )

    $lower = $RelativePath.ToLowerInvariant()
    $binaryExtensions = @(
        '.exe', '.com', '.dll', '.so', '.dylib', '.a', '.lib', '.o', '.obj', '.pdb', '.bin',
        '.msi', '.msix', '.cab', '.deb', '.rpm', '.apk', '.ipa', '.dmg', '.iso', '.img',
        '.wasm', '.class', '.jar', '.war', '.ear', '.whl', '.pyc', '.pyo', '.rlib',
        '.zip', '.7z', '.rar', '.gz', '.tgz', '.bz2', '.xz', '.zst', '.lz4', '.br',
        '.png', '.jpg', '.jpeg', '.gif', '.bmp', '.ico', '.tif', '.tiff', '.webp', '.avif', '.heic',
        '.mp3', '.wav', '.flac', '.ogg', '.mp4', '.mov', '.avi', '.mkv', '.webm',
        '.woff', '.woff2', '.ttf', '.otf', '.eot', '.psd',
        '.pdf', '.doc', '.docx', '.xls', '.xlsx', '.ppt', '.pptx', '.odt', '.ods', '.odp', '.epub'
    )
    foreach ($extension in $binaryExtensions) {
        if ($lower.EndsWith($extension)) {
            return $true
        }
    }
    foreach ($byte in $Bytes) {
        if (($byte -lt 0x20 -and $byte -ne 0x09 -and $byte -ne 0x0a -and $byte -ne 0x0d) -or $byte -eq 0x7f) {
            return $true
        }
    }

    $prefixLength = [Math]::Min($Bytes.Length, 16)
    $prefixBuilder = New-Object Text.StringBuilder
    for ($index = 0; $index -lt $prefixLength; $index++) {
        [void]$prefixBuilder.Append(('{0:x2}' -f $Bytes[$index]))
    }
    $prefix = $prefixBuilder.ToString()
    $binaryMagicPrefixes = @(
        '4d5a', '7f454c46',
        '504b0304', '504b0506', '504b0708',
        'feedface', 'feedfacf', 'cefaedfe', 'cffaedfe', 'cafebabe', '0061736d',
        '89504e470d0a1a0a', 'ffd8ff', '474946383761', '474946383961',
        '424d', '49492a00', '4d4d002a', '00000100',
        '25504446', '1f8b08', '425a68', 'fd377a585a00', '377abcaf271c',
        '526172211a0700', '526172211a070100', '28b52ffd', '213c617263683e0a',
        'd0cf11e0a1b11ae1', '53514c69746520666f726d6174203300',
        '494433', '664c6143', '4f676753'
    )
    foreach ($magicPrefix in $binaryMagicPrefixes) {
        if ($prefix.StartsWith($magicPrefix, [StringComparison]::Ordinal)) {
            return $true
        }
    }
    if ($prefix.StartsWith('52494646', [StringComparison]::Ordinal) -and $prefix.Length -ge 24) {
        $riffType = $prefix.Substring(16, 8)
        if (@('57454250', '57415645', '41564920') -contains $riffType) {
            return $true
        }
    }
    return $false
}

function Get-ActionReference {
    param([Parameter(Mandatory = $true)][string]$Value)

    $candidate = $Value.Trim()
    if ($candidate.StartsWith("'", [StringComparison]::Ordinal)) {
        $match = [regex]::Match($candidate, "^'([^']*)'\s*(?:#.*)?$")
        if (-not $match.Success) { return $null }
        return $match.Groups[1].Value
    }
    if ($candidate.StartsWith('"', [StringComparison]::Ordinal)) {
        $match = [regex]::Match($candidate, '^"([^"\r\n]*)"\s*(?:#.*)?$')
        if (-not $match.Success) { return $null }
        return $match.Groups[1].Value
    }
    $match = [regex]::Match($candidate, '^([^\s#]+)\s*(?:#.*)?$')
    if (-not $match.Success) { return $null }
    return $match.Groups[1].Value
}

function Remove-YamlComment {
    param([Parameter(Mandatory = $true)][string]$Text)

    $single = $false
    $double = $false
    $escaped = $false
    for ($index = 0; $index -lt $Text.Length; $index++) {
        $character = $Text[$index]
        if ($double) {
            if ($escaped) { $escaped = $false; continue }
            if ($character -eq [char]92) { $escaped = $true; continue }
            if ($character -eq '"') { $double = $false }
            continue
        }
        if ($single) {
            if ($character -eq "'") {
                if (($index + 1) -lt $Text.Length -and $Text[$index + 1] -eq "'") { $index++; continue }
                $single = $false
            }
            continue
        }
        if ($character -eq '"') { $double = $true; continue }
        if ($character -eq "'") { $single = $true; continue }
        if ($character -eq '#' -and ($index -eq 0 -or [char]::IsWhiteSpace($Text[$index - 1]))) {
            return [pscustomobject]@{ Valid = $true; Text = $Text.Substring(0, $index) }
        }
    }
    return [pscustomobject]@{ Valid = (-not $single -and -not $double -and -not $escaped); Text = $Text }
}

function Get-YamlKeyValue {
    param([Parameter(Mandatory = $true)][string]$Text)

    $candidate = $Text.Trim()
    if (-not $candidate) { return [pscustomobject]@{ Valid = $true; HasMapping = $false; Key = ''; Value = '' } }
    if ($candidate.StartsWith("'", [StringComparison]::Ordinal)) {
        $builder = New-Object Text.StringBuilder
        $index = 1
        $closed = $false
        while ($index -lt $candidate.Length) {
            if ($candidate[$index] -eq "'") {
                if (($index + 1) -lt $candidate.Length -and $candidate[$index + 1] -eq "'") {
                    [void]$builder.Append("'")
                    $index += 2
                    continue
                }
                $closed = $true
                $index++
                break
            }
            [void]$builder.Append($candidate[$index])
            $index++
        }
        if (-not $closed -or $index -ge $candidate.Length -or $candidate[$index] -ne ':') {
            return [pscustomobject]@{ Valid = $false; HasMapping = $false; Key = ''; Value = '' }
        }
        return [pscustomobject]@{ Valid = $true; HasMapping = $true; Key = $builder.ToString(); Value = $candidate.Substring($index + 1).Trim() }
    }
    if ($candidate.StartsWith('"', [StringComparison]::Ordinal)) {
        $builder = New-Object Text.StringBuilder
        $index = 1
        $closed = $false
        while ($index -lt $candidate.Length) {
            if ($candidate[$index] -eq [char]92) {
                return [pscustomobject]@{ Valid = $false; HasMapping = $false; Key = ''; Value = '' }
            }
            if ($candidate[$index] -eq '"') {
                $closed = $true
                $index++
                break
            }
            [void]$builder.Append($candidate[$index])
            $index++
        }
        if (-not $closed -or $index -ge $candidate.Length -or $candidate[$index] -ne ':') {
            return [pscustomobject]@{ Valid = $false; HasMapping = $false; Key = ''; Value = '' }
        }
        return [pscustomobject]@{ Valid = $true; HasMapping = $true; Key = $builder.ToString(); Value = $candidate.Substring($index + 1).Trim() }
    }
    $match = [regex]::Match($candidate, '^([A-Za-z0-9_.-]+)\s*:(.*)$')
    if ($match.Success) {
        return [pscustomobject]@{ Valid = $true; HasMapping = $true; Key = $match.Groups[1].Value; Value = $match.Groups[2].Value.Trim() }
    }
    if ($candidate.Contains(':') -or $candidate.StartsWith('?', [StringComparison]::Ordinal) -or $candidate.StartsWith('<<', [StringComparison]::Ordinal)) {
        return [pscustomobject]@{ Valid = $false; HasMapping = $false; Key = ''; Value = '' }
    }
    return [pscustomobject]@{ Valid = $true; HasMapping = $false; Key = ''; Value = '' }
}

function Test-YamlFlowMappingSyntax {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text)

    $candidate = $Text.Trim()
    if ($candidate.StartsWith('{', [StringComparison]::Ordinal)) {
        return $true
    }
    $flowStart = $candidate.IndexOf('[', [StringComparison]::Ordinal)
    if ($flowStart -lt 0) {
        return $false
    }
    $flow = $candidate.Substring($flowStart)
    if (-not $flow.EndsWith(']', [StringComparison]::Ordinal)) {
        return $true
    }
    $inner = $flow.Substring(1, $flow.Length - 2)
    if ($inner.IndexOf('{', [StringComparison]::Ordinal) -ge 0 -or $inner.IndexOf('[', [StringComparison]::Ordinal) -ge 0) {
        return $true
    }
    # This restricted scanner accepts only scalar flow sequences. A colon in
    # the sequence may introduce an implicit mapping (including quoted or
    # escaped keys), so reject it rather than attempting partial YAML parsing.
    return $inner.IndexOf(':', [StringComparison]::Ordinal) -ge 0
}

function Get-GitHubYamlUses {
    param(
        [Parameter(Mandatory = $true)][string]$Text,
        [Parameter(Mandatory = $true)][string]$RelativePath,
        [Parameter(Mandatory = $true)][ValidateSet('Workflow','Action')][string]$Kind
    )

    $records = New-Object 'System.Collections.Generic.List[object]'
    $containers = New-Object 'System.Collections.Generic.List[object]'
    $mappingKeys = New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal)
    $sequenceCounters = New-Object 'System.Collections.Generic.Dictionary[string,int]' ([StringComparer]::Ordinal)
    $lines = @([regex]::Split($Text, '\r\n|\n|\r'))
    $blockIndent = -1
    $actionUsing = $null

    for ($lineIndex = 0; $lineIndex -lt $lines.Count; $lineIndex++) {
        $raw = $lines[$lineIndex]
        if (-not $raw.Trim()) { continue }
        $indent = 0
        while ($indent -lt $raw.Length -and $raw[$indent] -eq ' ') { $indent++ }
        if ($blockIndent -ge 0 -and $indent -gt $blockIndent) { continue }
        $blockIndent = -1
        if ($raw.Substring(0, $indent).Contains("`t") -or ($indent -lt $raw.Length -and $raw[$indent] -eq "`t")) {
            Add-PublicTreeFinding -Rule 'PT_ACTION_YAML' -Path $RelativePath -Line ($lineIndex + 1)
            continue
        }
        $commentResult = Remove-YamlComment -Text $raw.Substring($indent)
        if (-not $commentResult.Valid) {
            Add-PublicTreeFinding -Rule 'PT_ACTION_YAML' -Path $RelativePath -Line ($lineIndex + 1)
            continue
        }
        $trimmed = $commentResult.Text.Trim()
        if (-not $trimmed) { continue }
        if ($trimmed -ceq '---' -or $trimmed -ceq '...') {
            Add-PublicTreeFinding -Rule 'PT_ACTION_YAML' -Path $RelativePath -Line ($lineIndex + 1)
            continue
        }

        while ($containers.Count -gt 0 -and $containers[$containers.Count - 1].Indent -ge $indent) {
            $containers.RemoveAt($containers.Count - 1)
        }
        $isSequence = $false
        $mappingIndent = $indent
        if ($trimmed.StartsWith('-', [StringComparison]::Ordinal)) {
            if ($trimmed.Length -gt 1 -and -not [char]::IsWhiteSpace($trimmed[1])) {
                Add-PublicTreeFinding -Rule 'PT_ACTION_YAML' -Path $RelativePath -Line ($lineIndex + 1)
                continue
            }
            $isSequence = $true
            $parentPath = (@($containers | ForEach-Object { $_.Key }) -join '/')
            $counterKey = $parentPath + [char]0 + $indent
            $sequenceNumber = 1
            if ($sequenceCounters.ContainsKey($counterKey)) { $sequenceNumber = $sequenceCounters[$counterKey] + 1 }
            $sequenceCounters[$counterKey] = $sequenceNumber
            $containers.Add([pscustomobject]@{ Indent = $indent; Key = '#' + $sequenceNumber })
            $mappingIndent = $indent + 1
            $trimmed = $trimmed.Substring(1).Trim()
            if (-not $trimmed) { continue }
        }

        if (Test-YamlFlowMappingSyntax -Text $trimmed) {
            Add-PublicTreeFinding -Rule 'PT_ACTION_YAML' -Path $RelativePath -Line ($lineIndex + 1)
            continue
        }
        $mapping = Get-YamlKeyValue -Text $trimmed
        if (-not $mapping.Valid) {
            Add-PublicTreeFinding -Rule 'PT_ACTION_YAML' -Path $RelativePath -Line ($lineIndex + 1)
            continue
        }
        if (-not $mapping.HasMapping) {
            if ($trimmed -match '(?i)[\x22\x27]?uses[\x22\x27]?\s*:') {
                Add-PublicTreeFinding -Rule 'PT_ACTION_YAML' -Path $RelativePath -Line ($lineIndex + 1)
            }
            continue
        }

        $contextParts = @($containers | ForEach-Object { $_.Key })
        $context = $contextParts -join '/'
        if (-not $mappingKeys.ContainsKey($context)) {
            $mappingKeys.Add($context, (New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)))
        }
        if (-not $mappingKeys[$context].Add($mapping.Key)) {
            Add-PublicTreeFinding -Rule 'PT_ACTION_YAML' -Path $RelativePath -Line ($lineIndex + 1)
        }

        $value = $mapping.Value.Trim()
        if (Test-YamlFlowMappingSyntax -Text $value) {
            Add-PublicTreeFinding -Rule 'PT_ACTION_YAML' -Path $RelativePath -Line ($lineIndex + 1)
        }
        if ($mapping.Key -ceq '<<' -or $value -match '^[&*!]') {
            Add-PublicTreeFinding -Rule 'PT_ACTION_YAML' -Path $RelativePath -Line ($lineIndex + 1)
        }

        if ($mapping.Key -ceq 'uses') {
            $usageContext = 'Unknown'
            if ($Kind -ceq 'Workflow' -and $contextParts.Count -eq 2 -and $contextParts[0] -ceq 'jobs') {
                $usageContext = 'Job'
            } elseif ($Kind -ceq 'Workflow' -and $contextParts.Count -eq 4 -and $contextParts[0] -ceq 'jobs' -and $contextParts[2] -ceq 'steps' -and $contextParts[3].StartsWith('#', [StringComparison]::Ordinal)) {
                $usageContext = 'Step'
            } elseif ($Kind -ceq 'Action' -and $contextParts.Count -eq 3 -and $contextParts[0] -ceq 'runs' -and $contextParts[1] -ceq 'steps' -and $contextParts[2].StartsWith('#', [StringComparison]::Ordinal)) {
                $usageContext = 'CompositeStep'
            } else {
                Add-PublicTreeFinding -Rule 'PT_ACTION_YAML' -Path $RelativePath -Line ($lineIndex + 1)
            }
            $records.Add([pscustomobject]@{ Value = $value; Context = $usageContext; Line = $lineIndex + 1 })
        }
        if ($Kind -ceq 'Action' -and $mapping.Key -ceq 'using' -and $contextParts.Count -eq 1 -and $contextParts[0] -ceq 'runs') {
            $actionUsing = Get-ActionReference -Value $value
        }

        if ($value -match '^[|>][+-]?[1-9]?$') {
            if ($mapping.Key -ceq 'uses') { Add-PublicTreeFinding -Rule 'PT_ACTION_REF' -Path $RelativePath -Line ($lineIndex + 1) }
            $blockIndent = $indent
            continue
        }
        if (-not $value) {
            $containers.Add([pscustomobject]@{ Indent = $mappingIndent; Key = $mapping.Key })
        }
    }

    if ($Kind -ceq 'Action') {
        foreach ($record in $records) {
            if ($record.Context -ceq 'CompositeStep' -and $actionUsing -cne 'composite') {
                Add-PublicTreeFinding -Rule 'PT_ACTION_YAML' -Path $RelativePath -Line $record.Line
            }
        }
    }
    $recordArray = New-Object object[] $records.Count
    $records.CopyTo($recordArray)
    return $recordArray
}

function ConvertFrom-GoInterpretedString {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Body)

    $builder = New-Object Text.StringBuilder
    for ($index = 0; $index -lt $Body.Length; $index++) {
        $character = $Body[$index]
        if ($character -ne [char]92) {
            [void]$builder.Append($character)
            continue
        }
        $index++
        if ($index -ge $Body.Length) { return $null }
        $escape = $Body[$index]
        $simple = @{
            'a' = 7; 'b' = 8; 'f' = 12; 'n' = 10; 'r' = 13; 't' = 9; 'v' = 11
            '\' = 92; '"' = 34; "'" = 39
        }
        $escapeText = [string]$escape
        if ($simple.ContainsKey($escapeText)) {
            [void]$builder.Append([char][int]$simple[$escapeText])
            continue
        }
        $digitCount = 0
        $numberBase = 0
        if ($escape -eq 'x') { $digitCount = 2; $numberBase = 16 }
        elseif ($escape -eq 'u') { $digitCount = 4; $numberBase = 16 }
        elseif ($escape -eq 'U') { $digitCount = 8; $numberBase = 16 }
        elseif ($escape -ge '0' -and $escape -le '7') {
            $digitCount = 3
            $numberBase = 8
            $index--
        } else {
            return $null
        }
        if ($index + $digitCount -ge $Body.Length + 1) { return $null }
        $digits = $Body.Substring($index + 1, $digitCount)
        if ($numberBase -eq 8) {
            $digits = $Body.Substring($index, $digitCount)
            if ($digits -notmatch '^[0-7]{3}$') { return $null }
            $index += $digitCount - 1
        } else {
            if ($digits -notmatch ('^[0-9A-Fa-f]{' + $digitCount + '}$')) { return $null }
            $index += $digitCount
        }
        try { $codePoint = [Convert]::ToInt32($digits, $numberBase) } catch { return $null }
        try { [void]$builder.Append([char]::ConvertFromUtf32($codePoint)) } catch { return $null }
    }
    return $builder.ToString()
}

function Get-GoLexResult {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text)

    $tokens = New-Object 'System.Collections.Generic.List[object]'
    $index = 0
    $line = 1
    while ($index -lt $Text.Length) {
        $character = $Text[$index]
        if ([char]::IsWhiteSpace($character)) {
            if ($character -eq "`n") { $line++ }
            $index++
            continue
        }
        if ($character -eq '/' -and ($index + 1) -lt $Text.Length) {
            if ($Text[$index + 1] -eq '/') {
                $index += 2
                while ($index -lt $Text.Length -and $Text[$index] -ne "`n") { $index++ }
                continue
            }
            if ($Text[$index + 1] -eq '*') {
                $index += 2
                $closed = $false
                while (($index + 1) -lt $Text.Length) {
                    if ($Text[$index] -eq "`n") { $line++ }
                    if ($Text[$index] -eq '*' -and $Text[$index + 1] -eq '/') {
                        $index += 2
                        $closed = $true
                        break
                    }
                    $index++
                }
                if (-not $closed) { return [pscustomobject]@{ Valid = $false; Tokens = @() } }
                continue
            }
        }
        if ([char]::IsLetter($character) -or $character -eq '_') {
            $start = $index
            $tokenLine = $line
            $index++
            while ($index -lt $Text.Length -and ([char]::IsLetterOrDigit($Text[$index]) -or $Text[$index] -eq '_')) { $index++ }
            $tokens.Add([pscustomobject]@{ Kind = 'Identifier'; Value = $Text.Substring($start, $index - $start); Line = $tokenLine })
            continue
        }
        if ($character -eq '"') {
            $tokenLine = $line
            $index++
            $body = New-Object Text.StringBuilder
            $closed = $false
            while ($index -lt $Text.Length) {
                $current = $Text[$index]
                if ($current -eq "`n" -or $current -eq "`r") { return [pscustomobject]@{ Valid = $false; Tokens = @() } }
                if ($current -eq '"') {
                    $index++
                    $closed = $true
                    break
                }
                if ($current -eq [char]92) {
                    if (($index + 1) -ge $Text.Length) { return [pscustomobject]@{ Valid = $false; Tokens = @() } }
                    [void]$body.Append($current)
                    $index++
                    [void]$body.Append($Text[$index])
                    $index++
                    continue
                }
                [void]$body.Append($current)
                $index++
            }
            if (-not $closed) { return [pscustomobject]@{ Valid = $false; Tokens = @() } }
            $value = ConvertFrom-GoInterpretedString -Body $body.ToString()
            if ($null -eq $value) { return [pscustomobject]@{ Valid = $false; Tokens = @() } }
            $tokens.Add([pscustomobject]@{ Kind = 'String'; Value = $value; Line = $tokenLine })
            continue
        }
        if ($character -eq [char]96) {
            $tokenLine = $line
            $index++
            $body = New-Object Text.StringBuilder
            $closed = $false
            while ($index -lt $Text.Length) {
                $current = $Text[$index]
                if ($current -eq [char]96) {
                    $index++
                    $closed = $true
                    break
                }
                if ($current -eq "`n") { $line++ }
                if ($current -ne "`r") { [void]$body.Append($current) }
                $index++
            }
            if (-not $closed) { return [pscustomobject]@{ Valid = $false; Tokens = @() } }
            $tokens.Add([pscustomobject]@{ Kind = 'String'; Value = $body.ToString(); Line = $tokenLine })
            continue
        }
        if ($character -eq "'") {
            $index++
            $closed = $false
            while ($index -lt $Text.Length) {
                $current = $Text[$index]
                if ($current -eq "`n" -or $current -eq "`r") { return [pscustomobject]@{ Valid = $false; Tokens = @() } }
                if ($current -eq [char]92) { $index += 2; continue }
                $index++
                if ($current -eq "'") { $closed = $true; break }
            }
            if (-not $closed) { return [pscustomobject]@{ Valid = $false; Tokens = @() } }
            continue
        }
        if (@('(', ')', '.', ';') -contains ([string]$character)) {
            $tokens.Add([pscustomobject]@{ Kind = 'Symbol'; Value = [string]$character; Line = $line })
        }
        $index++
    }
    $lexicalArray = New-Object object[] $tokens.Count
    $tokens.CopyTo($lexicalArray)
    return [pscustomobject]@{ Valid = $true; Tokens = $lexicalArray }
}

function Test-LegacyGoImports {
    param(
        [Parameter(Mandatory = $true)][string]$Text,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )

    $lex = Get-GoLexResult -Text $Text
    if (-not $lex.Valid) {
        Add-PublicTreeFinding -Rule 'PT_GO_PARSE' -Path $RelativePath
        return
    }
    $tokens = @($lex.Tokens)
    for ($index = 0; $index -lt $tokens.Count; $index++) {
        if ($tokens[$index].Kind -cne 'Identifier' -or $tokens[$index].Value -cne 'import') { continue }
        $cursor = $index + 1
        if ($cursor -ge $tokens.Count) {
            Add-PublicTreeFinding -Rule 'PT_GO_PARSE' -Path $RelativePath -Line $tokens[$index].Line
            continue
        }
        if ($tokens[$cursor].Kind -ceq 'Symbol' -and $tokens[$cursor].Value -ceq '(') {
            $cursor++
            $closed = $false
            while ($cursor -lt $tokens.Count) {
                if ($tokens[$cursor].Kind -ceq 'Symbol' -and $tokens[$cursor].Value -ceq ')') {
                    $closed = $true
                    break
                }
                if (
                    $tokens[$cursor].Kind -ceq 'String' -and
                    ($tokens[$cursor].Value -ceq 'freeagent' -or $tokens[$cursor].Value.StartsWith('freeagent/', [StringComparison]::Ordinal))
                ) {
                    Add-PublicTreeFinding -Rule 'PT_LEGACY_SOURCE' -Path $RelativePath -Line $tokens[$cursor].Line
                }
                $cursor++
            }
            if (-not $closed) { Add-PublicTreeFinding -Rule 'PT_GO_PARSE' -Path $RelativePath -Line $tokens[$index].Line }
            $index = $cursor
            continue
        }
        if (
            $cursor -lt $tokens.Count -and
            ($tokens[$cursor].Kind -ceq 'Identifier' -or ($tokens[$cursor].Kind -ceq 'Symbol' -and $tokens[$cursor].Value -ceq '.'))
        ) {
            $cursor++
        }
        if ($cursor -ge $tokens.Count -or $tokens[$cursor].Kind -cne 'String') {
            Add-PublicTreeFinding -Rule 'PT_GO_PARSE' -Path $RelativePath -Line $tokens[$index].Line
            continue
        }
        if ($tokens[$cursor].Value -ceq 'freeagent' -or $tokens[$cursor].Value.StartsWith('freeagent/', [StringComparison]::Ordinal)) {
            Add-PublicTreeFinding -Rule 'PT_LEGACY_SOURCE' -Path $RelativePath -Line $tokens[$cursor].Line
        }
        $index = $cursor
    }
}

if ([string]::IsNullOrWhiteSpace($Root)) {
    Stop-WithRootFinding -Rule 'PT_ROOT_ABSOLUTE'
}

$looksAbsolute = if ($script:IsWindowsPlatform) {
    $Root -match '^(?:[A-Za-z]:[\\/]|[\\/]{2}[^\\/])'
} else {
    $Root.StartsWith('/', [StringComparison]::Ordinal)
}
if (-not $looksAbsolute) {
    Stop-WithRootFinding -Rule 'PT_ROOT_ABSOLUTE'
}

try {
    $rootItem = Get-Item -LiteralPath $Root -Force -ErrorAction Stop
} catch {
    Stop-WithRootFinding -Rule 'PT_ROOT_MISSING'
}
if (-not $rootItem.PSIsContainer -or $rootItem.PSProvider.Name -ne 'FileSystem') {
    Stop-WithRootFinding -Rule 'PT_ROOT_NOT_DIRECTORY'
}

$rootPath = [IO.Path]::GetFullPath($rootItem.FullName)
$filesystemRoot = [IO.Path]::GetPathRoot($rootPath)
$rootComparison = if ($script:IsWindowsPlatform) { [StringComparison]::OrdinalIgnoreCase } else { [StringComparison]::Ordinal }
if ([string]::Equals($rootPath.TrimEnd([char[]]@([char]92, [char]47)), $filesystemRoot.TrimEnd([char[]]@([char]92, [char]47)), $rootComparison)) {
    Stop-WithRootFinding -Rule 'PT_ROOT_FILESYSTEM'
}
$rootPath = $rootPath.TrimEnd([char[]]@([char]92, [char]47))
$rootHasReparseAncestor = $false
$ancestor = $rootItem
while ($null -ne $ancestor) {
    try {
        if (($ancestor.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            $rootHasReparseAncestor = $true
            break
        }
        $ancestor = $ancestor.Parent
    } catch {
        Add-PublicTreeFinding -Rule 'PT_IO_ATTRIBUTES' -Path '<root>'
        $rootHasReparseAncestor = $true
        break
    }
}
if ($rootHasReparseAncestor) {
    Add-PublicTreeFinding -Rule 'PT_REPARSE' -Path '<root>'
} else {
    Test-WindowsAlternateStreams -LiteralPath $rootPath -RelativePath '<root>'
    Visit-PublicTreeDirectory -DirectoryPath $rootPath -RelativePrefix '' -CanonicalRoot $rootPath
}

$retiredTokens = @(
    (Join-CharacterCodes -Codes @(120, 120, 98)),
    (Join-CharacterCodes -Codes @(99, 104, 117, 110, 107, 98, 117, 114, 115, 116))
)
$retiredConstructionScriptNames = @(Get-RetiredConstructionScriptNames)
$privateKeyWords = Join-CharacterCodes -Codes @(80, 82, 73, 86, 65, 84, 69, 32, 75, 69, 89)
$providerPrefix = Join-CharacterCodes -Codes @(115, 107, 45)
$gitHubPrefix = Join-CharacterCodes -Codes @(103, 104)
$slackPrefix = Join-CharacterCodes -Codes @(120, 111, 120)
$awsPrefix = Join-CharacterCodes -Codes @(65, 75, 73, 65)
$asiaPrefix = Join-CharacterCodes -Codes @(65, 83, 73, 65)
$bearerWord = Join-CharacterCodes -Codes @(66, 101, 97, 114, 101, 114)
$windowsUsers = Join-CharacterCodes -Codes @(85, 115, 101, 114, 115)
$posixHome = Join-CharacterCodes -Codes @(47, 104, 111, 109, 101, 47)
$posixUsers = Join-CharacterCodes -Codes @(47, 85, 115, 101, 114, 115, 47)
$posixRoot = Join-CharacterCodes -Codes @(47, 114, 111, 111, 116)
$posixTemp = Join-CharacterCodes -Codes @(47, 116, 109, 112, 47)
$posixVarTemp = Join-CharacterCodes -Codes @(47, 118, 97, 114, 47, 116, 109, 112, 47)
$posixPrivateTemp = Join-CharacterCodes -Codes @(47, 112, 114, 105, 118, 97, 116, 101, 47, 116, 109, 112, 47)
$posixWorkspace = Join-CharacterCodes -Codes @(47, 119, 111, 114, 107, 115, 112, 97, 99, 101, 47)
$posixWorkspaces = Join-CharacterCodes -Codes @(47, 119, 111, 114, 107, 115, 112, 97, 99, 101, 115, 47)
$posixMount = Join-CharacterCodes -Codes @(47, 109, 110, 116, 47)
$fileUriPrefix = Join-CharacterCodes -Codes @(102, 105, 108, 101, 58, 47, 47)
$backslash = [string][char]92
$forwardSlash = [string][char]47
$separatorClass = '[' + [regex]::Escape($backslash) + [regex]::Escape($forwardSlash) + ']'
$escapedBackslash = [regex]::Escape($backslash)
# PublicTree is a repository-byte scanner, not a source-language evaluator.
# These patterns inspect each original physical line exactly as stored. Values
# assembled only through runtime concatenation, interpolation, or decoding are
# outside this lexical rule and remain subject to the runtime path validators.
$uncHostCharacter = '[\p{L}\p{M}\p{N}._$@-]'
$uncHostSegment = $uncHostCharacter + '+'
$uncShareFirstCharacter = '[^\x00-\x20\x22\x27\x60/\\\[\]:|<>\+=;,?*]'
$uncShareTailCharacter = '[^\x00-\x1F\x22\x27\x60/\\\[\]:|<>\+=;,?*]'
$uncShareSegment = $uncShareFirstCharacter + $uncShareTailCharacter + '*'
$uncTailPattern = $uncHostSegment + $separatorClass + '+' + $uncShareSegment
$uncExtendedTailPattern = [regex]::Escape('?') + $separatorClass + '+UNC' + $separatorClass + '+' + $uncTailPattern
$uncBackslashPattern = '(?i)' + $escapedBackslash + '{2,}(?:' + $uncExtendedTailPattern + '|' + $uncTailPattern + ')'
$uncSlashPattern = '(?i)(?<![:/])/{2,}(?:' + $uncExtendedTailPattern + '|' + $uncTailPattern + ')'

$contentRules = @(
    [pscustomobject]@{ Rule = 'PT_SECRET_PRIVATE_KEY'; Pattern = "-----BEGIN (?:[A-Z0-9 ]+ )?$privateKeyWords-----" },
    [pscustomobject]@{ Rule = 'PT_SECRET_PROVIDER_KEY'; Pattern = "\b$([regex]::Escape($providerPrefix))(?:[A-Za-z0-9]{20,}|proj-[A-Za-z0-9_-]{20,})(?![A-Za-z0-9_-])" },
    [pscustomobject]@{ Rule = 'PT_SECRET_GITHUB_TOKEN'; Pattern = "\b(?:$([regex]::Escape($gitHubPrefix))[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b" },
    [pscustomobject]@{ Rule = 'PT_SECRET_SLACK_TOKEN'; Pattern = "\b$([regex]::Escape($slackPrefix))[baprs]-[A-Za-z0-9-]{10,}\b" },
    [pscustomobject]@{ Rule = 'PT_SECRET_AWS_KEY'; Pattern = "\b(?:$awsPrefix|$asiaPrefix)[A-Z0-9]{16}\b" },
    [pscustomobject]@{ Rule = 'PT_SECRET_BEARER'; Pattern = "(?i)\b$bearerWord\s+(?!(?:authentication|redacted|placeholder|example|dummy|token)(?:\b|$))[A-Za-z0-9._~+/=-]{12,}" },
    [pscustomobject]@{ Rule = 'PT_SECRET_URI_USERINFO'; Pattern = '\b[a-zA-Z][a-zA-Z0-9+.-]*://[^/\s:@]+(?::[^/\s@]*)?@[^/\s]+' },
    [pscustomobject]@{ Rule = 'PT_PRIVATE_WINDOWS_PATH'; Pattern = '(?i)(?<![A-Za-z0-9])[A-Za-z]:' + $separatorClass + '[^\s\x22\x27<>]+' },
    [pscustomobject]@{ Rule = 'PT_PRIVATE_WINDOWS_USER_PATH'; Pattern = '(?i)[A-Za-z]:' + $separatorClass + '+' + [regex]::Escape($windowsUsers) + $separatorClass + '+(?!(?:Public|Default|Default User|All Users)(?:' + $separatorClass + '|$))[^\s<>]+' },
    [pscustomobject]@{ Rule = 'PT_PRIVATE_POSIX_HOME_PATH'; Pattern = '(?:' + [regex]::Escape($posixHome) + '|' + [regex]::Escape($posixUsers) + ')[A-Za-z0-9._-]+|(?<![A-Za-z0-9_])' + [regex]::Escape($posixRoot) + '(?:' + [regex]::Escape($forwardSlash) + '|\b)' },
    [pscustomobject]@{ Rule = 'PT_PRIVATE_POSIX_LOCAL_PATH'; Pattern = '(?<![A-Za-z0-9_])(?:' + [regex]::Escape($posixTemp) + '|' + [regex]::Escape($posixVarTemp) + '|' + [regex]::Escape($posixPrivateTemp) + '|' + [regex]::Escape($posixWorkspace) + '|' + [regex]::Escape($posixWorkspaces) + ')[^\s\x22\x27<>]+|(?<![A-Za-z0-9_])' + [regex]::Escape($posixMount) + '[A-Za-z]/Users/[^\s\x22\x27<>]+' },
    [pscustomobject]@{ Rule = 'PT_PRIVATE_FILE_URI'; Pattern = '(?i)\b' + [regex]::Escape($fileUriPrefix) + '[^\s]+' }
)
function Add-TextRuleFindings {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text,
        [Parameter(Mandatory = $true)][string]$RelativePath,
        [int]$Line = 0,
        [switch]$SkipCredentialAssignment
    )

    if (
        $Line -gt 0 -and
        (
            [regex]::IsMatch($Text, $uncBackslashPattern) -or
            [regex]::IsMatch($Text, $uncSlashPattern)
        ) -and
        -not (Test-ReviewedUncLine -Text $Text -RelativePath $RelativePath -Line $Line)
    ) {
        Add-PublicTreeFinding -Rule 'PT_PRIVATE_UNC_PATH' -Path $RelativePath -Line $Line
    }

    foreach ($candidate in @(Get-RuleTextForms -Text $Text)) {
        foreach ($rule in $contentRules) {
            foreach ($contentMatch in [regex]::Matches($candidate, $rule.Pattern)) {
                Add-PublicTreeFinding -Rule $rule.Rule -Path $RelativePath -Line $Line
            }
        }
        if (-not $SkipCredentialAssignment -and (Test-CredentialAssignmentSecret -Text $candidate -RelativePath $RelativePath)) {
            Add-PublicTreeFinding -Rule 'PT_SECRET_ASSIGNMENT' -Path $RelativePath -Line $Line
        }
    }
}

function Add-RetiredConstructionReferenceFindings {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text,
        [Parameter(Mandatory = $true)][string]$RelativePath,
        [int]$Line = 0
    )

    foreach ($candidate in @(Get-RuleTextForms -Text $Text)) {
        foreach ($retiredScriptName in $retiredConstructionScriptNames) {
            $referencePattern = '(?i)(?<![A-Za-z0-9_.-])(?:scripts/)?' + [regex]::Escape($retiredScriptName) + '(?![A-Za-z0-9_.-])'
            if ([regex]::IsMatch($candidate, $referencePattern)) {
                Add-PublicTreeFinding -Rule 'PT_CONSTRUCTION' -Path $RelativePath -Line $Line
                return
            }
        }
    }
}

function Get-ExactJsonProperty {
    param(
        [Parameter(Mandatory = $true)][object]$Object,
        [Parameter(Mandatory = $true)][string]$Name
    )

    if ($Object -isnot [pscustomobject]) { return $null }
    $properties = @($Object.PSObject.Properties | Where-Object { $_.Name -ceq $Name })
    if ($properties.Count -ne 1) { return $null }
    return $properties[0]
}

function Test-SafeJsonSchemaEnum {
    param(
        [Parameter(Mandatory = $true)][object]$Value,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$ContextName
    )

    # The only enum reachable from the public SecretRef graph is its authority
    # scope vocabulary.  It is schema metadata, not credential material.
    if (-not [string]::Equals($ContextName, 'scope', [StringComparison]::Ordinal)) { return $false }
    $actual = @($Value | ForEach-Object { [string]$_ })
    $expected = @('AGENT', 'MODULE_INSTALLATION', 'TENANT', 'WORKSPACE')
    if ($actual.Count -ne $expected.Count) { return $false }
    [Array]::Sort($actual, [StringComparer]::Ordinal)
    for ($index = 0; $index -lt $expected.Count; $index++) {
        if ($actual[$index] -cne $expected[$index]) { return $false }
    }
    return $true
}

function Test-SafeLocalJsonSchemaReference {
    param(
        [Parameter(Mandatory = $true)][object]$Document,
        [Parameter(Mandatory = $true)][string]$Reference,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$ContextName,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][Collections.Generic.HashSet[string]]$Active,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][Collections.Generic.HashSet[string]]$Safe
    )

    $referenceMatch = [regex]::Match($Reference, '^#/\$defs/(?<name>[A-Za-z][A-Za-z0-9_-]*)$')
    if (-not $referenceMatch.Success) { return $false }
    $cacheKey = $Reference + [char]0 + $ContextName
    if ($Safe.Contains($cacheKey)) { return $true }
    if (-not $Active.Add($cacheKey)) { return $false }

    try {
        $definitionsProperty = Get-ExactJsonProperty -Object $Document -Name '$defs'
        if ($null -eq $definitionsProperty) { return $false }
        $targetProperty = Get-ExactJsonProperty -Object $definitionsProperty.Value -Name $referenceMatch.Groups['name'].Value
        if ($null -eq $targetProperty) { return $false }
        if (-not (Test-SafeJsonSchemaNode -Document $Document -Node $targetProperty.Value -ContextName $ContextName -Active $Active -Safe $Safe)) {
            return $false
        }
        [void]$Safe.Add($cacheKey)
        return $true
    } finally {
        [void]$Active.Remove($cacheKey)
    }
}

function Test-SafeJsonSchemaNode {
    param(
        [Parameter(Mandatory = $true)][object]$Document,
        [Parameter(Mandatory = $true)][AllowNull()][object]$Node,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$ContextName,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][Collections.Generic.HashSet[string]]$Active,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][Collections.Generic.HashSet[string]]$Safe
    )

    if ($Node -is [bool]) { return $true }
    if ($Node -isnot [pscustomobject]) { return $false }

    $scalarKeywords = @(
        '$anchor', '$comment', '$id',
        'description', 'exclusiveMaximum', 'exclusiveMinimum', 'format',
        'maxContains', 'maxItems', 'maxLength', 'maxProperties', 'maximum',
        'minContains', 'minItems', 'minLength', 'minProperties', 'minimum',
        'multipleOf', 'pattern', 'readOnly', 'title', 'type', 'uniqueItems', 'writeOnly'
    )
    $schemaKeywords = @(
        'additionalProperties', 'contains', 'else', 'if', 'items', 'not',
        'propertyNames', 'then', 'unevaluatedItems', 'unevaluatedProperties'
    )
    $schemaArrayKeywords = @('allOf', 'anyOf', 'oneOf', 'prefixItems')

    foreach ($property in $Node.PSObject.Properties) {
        $name = [string]$property.Name
        if (@('const', 'default', 'examples') -ccontains $name) { return $false }
        if ($name -ceq 'enum') {
            if (-not (Test-SafeJsonSchemaEnum -Value $property.Value -ContextName $ContextName)) { return $false }
            continue
        }
        if ($name -ceq '$ref') {
            if ($property.Value -isnot [string]) { return $false }
            if (-not (Test-SafeLocalJsonSchemaReference -Document $Document -Reference ([string]$property.Value) -ContextName $ContextName -Active $Active -Safe $Safe)) {
                return $false
            }
            continue
        }
        if ($name -ceq 'properties') {
            if ($property.Value -isnot [pscustomobject]) { return $false }
            foreach ($child in $property.Value.PSObject.Properties) {
                if (-not (Test-SafeJsonSchemaNode -Document $Document -Node $child.Value -ContextName ([string]$child.Name) -Active $Active -Safe $Safe)) {
                    return $false
                }
            }
            continue
        }
        if ($name -ceq 'required') {
            foreach ($requiredName in @($property.Value)) {
                if ($requiredName -isnot [string] -or [string]::IsNullOrWhiteSpace([string]$requiredName)) { return $false }
            }
            continue
        }
        if ($scalarKeywords -ccontains $name) {
            if ($property.Value -is [pscustomobject]) { return $false }
            continue
        }
        if ($schemaKeywords -ccontains $name) {
            if ($property.Value -is [bool]) { continue }
            if (-not (Test-SafeJsonSchemaNode -Document $Document -Node $property.Value -ContextName $ContextName -Active $Active -Safe $Safe)) {
                return $false
            }
            continue
        }
        if ($schemaArrayKeywords -ccontains $name) {
            foreach ($childNode in @($property.Value)) {
                if (-not (Test-SafeJsonSchemaNode -Document $Document -Node $childNode -ContextName $ContextName -Active $Active -Safe $Safe)) {
                    return $false
                }
            }
            continue
        }
        # Unknown keywords can carry arbitrary literal data, so they are not
        # trusted inside a credential reference graph.
        return $false
    }
    return $true
}

function Test-ExactPublicSecretRefsSchema {
    param([Parameter(Mandatory = $true)][object]$Document)

    $definitionsProperty = Get-ExactJsonProperty -Object $Document -Name '$defs'
    if ($null -eq $definitionsProperty) { return $false }
    $expectedDefinitions = @(
        [pscustomobject]@{
            DefinitionCodepoints = @(115, 101, 99, 114, 101, 116, 82, 101, 102, 115)
            CanonicalJSON = '{"type":"array","maxItems":256,"items":{"$ref":"#/$defs/secretRef"}}'
        },
        [pscustomobject]@{
            DefinitionCodepoints = @(115, 101, 99, 114, 101, 116, 82, 101, 102)
            CanonicalJSON = '{"type":"object","additionalProperties":false,"required":["provider","scope","scope_id","name","version"],"properties":{"provider":{"$ref":"#/$defs/dottedIdentifier"},"scope":{"enum":["TENANT","WORKSPACE","AGENT","MODULE_INSTALLATION"]},"scope_id":{"$ref":"#/$defs/secretOpaqueID"},"name":{"$ref":"#/$defs/secretOpaqueID"},"version":{"$ref":"#/$defs/exactVersion"}}}'
        },
        [pscustomobject]@{
            DefinitionCodepoints = @(100, 111, 116, 116, 101, 100, 73, 100, 101, 110, 116, 105, 102, 105, 101, 114)
            CanonicalJSON = '{"type":"string","minLength":1,"maxLength":128,"pattern":"^[a-z][a-z0-9_-]*(\\.[a-z][a-z0-9_-]*)*$"}'
        },
        [pscustomobject]@{
            DefinitionCodepoints = @(115, 101, 99, 114, 101, 116, 79, 112, 97, 113, 117, 101, 73, 68)
            CanonicalJSON = '{"type":"string","minLength":1,"maxLength":256,"pattern":"^(?:[!-~]|[!-~][ -~]*[!-~])$"}'
        },
        [pscustomobject]@{
            DefinitionCodepoints = @(101, 120, 97, 99, 116, 86, 101, 114, 115, 105, 111, 110)
            CanonicalJSON = '{"type":"string","minLength":1,"maxLength":64,"pattern":"^[A-Za-z0-9](?:[A-Za-z0-9._+-]{0,62}[A-Za-z0-9])?$"}'
        }
    )
    foreach ($entry in $expectedDefinitions) {
        $definitionName = Join-CharacterCodes -Codes $entry.DefinitionCodepoints
        $actualProperty = Get-ExactJsonProperty -Object $definitionsProperty.Value -Name $definitionName
        if ($null -eq $actualProperty) { return $false }
        try {
            $actual = $actualProperty.Value | ConvertTo-Json -Depth 32 -Compress
            $expected = ([string]$entry.CanonicalJSON | ConvertFrom-Json -ErrorAction Stop) | ConvertTo-Json -Depth 32 -Compress
        } catch {
            return $false
        }
        if (-not [string]::Equals($actual, $expected, [StringComparison]::Ordinal)) { return $false }
    }
    return $true
}

function Test-JsonObjectPropertyNamesUnique {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text)

    $tokens = New-Object 'System.Collections.Generic.List[object]'
    $index = 0
    while ($index -lt $Text.Length) {
        $character = $Text[$index]
        if ([char]::IsWhiteSpace($character)) {
            $index++
            continue
        }
        if (
            $character -eq [char]123 -or
            $character -eq [char]125 -or
            $character -eq [char]91 -or
            $character -eq [char]93 -or
            $character -eq [char]58 -or
            $character -eq [char]44
        ) {
            $tokens.Add([pscustomobject]@{ Kind = 'punctuation'; Value = [string]$character })
            $index++
            continue
        }
        if ($character -eq [char]34) {
            $builder = New-Object Text.StringBuilder
            $index++
            $closed = $false
            while ($index -lt $Text.Length) {
                $character = $Text[$index]
                if ($character -eq [char]34) {
                    $closed = $true
                    $index++
                    break
                }
                if ([int]$character -lt 32) { return $false }
                if ($character -ne [char]92) {
                    [void]$builder.Append($character)
                    $index++
                    continue
                }
                $index++
                if ($index -ge $Text.Length) { return $false }
                $escapedCharacter = $Text[$index]
                if (
                    $escapedCharacter -eq [char]34 -or
                    $escapedCharacter -eq [char]47 -or
                    $escapedCharacter -eq [char]92
                ) {
                    [void]$builder.Append($escapedCharacter)
                    $index++
                    continue
                }
                $escapedCode = [int]$escapedCharacter
                if ($escapedCode -eq 98) {
                    [void]$builder.Append([char]8)
                } elseif ($escapedCode -eq 102) {
                    [void]$builder.Append([char]12)
                } elseif ($escapedCode -eq 110) {
                    [void]$builder.Append([char]10)
                } elseif ($escapedCode -eq 114) {
                    [void]$builder.Append([char]13)
                } elseif ($escapedCode -eq 116) {
                    [void]$builder.Append([char]9)
                } elseif ($escapedCode -eq 117) {
                    if (($index + 4) -ge $Text.Length) { return $false }
                    $hex = $Text.Substring($index + 1, 4)
                    if ($hex -notmatch '^[0-9A-Fa-f]{4}$') { return $false }
                    [void]$builder.Append([char][Convert]::ToInt32($hex, 16))
                    $index += 4
                } else {
                    return $false
                }
                $index++
            }
            if (-not $closed) { return $false }
            $tokens.Add([pscustomobject]@{ Kind = 'string'; Value = $builder.ToString() })
            continue
        }

        $start = $index
        while ($index -lt $Text.Length) {
            $character = $Text[$index]
            if (
                [char]::IsWhiteSpace($character) -or
                $character -eq [char]123 -or
                $character -eq [char]125 -or
                $character -eq [char]91 -or
                $character -eq [char]93 -or
                $character -eq [char]58 -or
                $character -eq [char]44
            ) {
                break
            }
            $index++
        }
        if ($index -eq $start) { return $false }
        $scalar = $Text.Substring($start, $index - $start)
        if ($scalar -notmatch '^(?:true|false|null|-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)$') {
            return $false
        }
        $tokens.Add([pscustomobject]@{ Kind = 'scalar'; Value = $scalar })
    }

    $frames = New-Object 'System.Collections.Generic.Stack[object]'
    $rootState = 'value'
    foreach ($token in $tokens) {
        $state = if ($frames.Count -gt 0) { [string]$frames.Peek().State } else { $rootState }
        if ($state -ceq 'object-key-or-end') {
            $frame = $frames.Peek()
            if ($token.Kind -ceq 'punctuation' -and $token.Value -ceq '}') {
                [void]$frames.Pop()
                continue
            }
            if ($token.Kind -cne 'string') { return $false }
            if (-not $frame.Names.Add([string]$token.Value)) { return $false }
            $frame.State = 'object-colon'
            continue
        }
        if ($state -ceq 'object-key') {
            $frame = $frames.Peek()
            if ($token.Kind -cne 'string') { return $false }
            if (-not $frame.Names.Add([string]$token.Value)) { return $false }
            $frame.State = 'object-colon'
            continue
        }
        if ($state -ceq 'object-colon') {
            if ($token.Kind -cne 'punctuation' -or $token.Value -cne ':') { return $false }
            $frames.Peek().State = 'object-value'
            continue
        }
        if ($state -ceq 'object-comma-or-end') {
            if ($token.Kind -cne 'punctuation') { return $false }
            if ($token.Value -ceq ',') {
                $frames.Peek().State = 'object-key'
                continue
            }
            if ($token.Value -ceq '}') {
                [void]$frames.Pop()
                continue
            }
            return $false
        }
        if ($state -ceq 'array-comma-or-end') {
            if ($token.Kind -cne 'punctuation') { return $false }
            if ($token.Value -ceq ',') {
                $frames.Peek().State = 'array-value'
                continue
            }
            if ($token.Value -ceq ']') {
                [void]$frames.Pop()
                continue
            }
            return $false
        }

        if ($state -ceq 'array-value-or-end' -and $token.Kind -ceq 'punctuation' -and $token.Value -ceq ']') {
            [void]$frames.Pop()
            continue
        }
        if ($state -ne 'value' -and $state -ne 'object-value' -and $state -ne 'array-value' -and $state -ne 'array-value-or-end') {
            return $false
        }
        if ($frames.Count -gt 0) {
            $frame = $frames.Peek()
            $frame.State = if ($frame.Kind -ceq 'object') { 'object-comma-or-end' } else { 'array-comma-or-end' }
        } else {
            $rootState = 'complete'
        }
        if ($token.Kind -ceq 'punctuation' -and $token.Value -ceq '{') {
            $frames.Push([pscustomobject]@{
                Kind = 'object'
                State = 'object-key-or-end'
                Names = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
            })
            continue
        }
        if ($token.Kind -ceq 'punctuation' -and $token.Value -ceq '[') {
            $frames.Push([pscustomobject]@{
                Kind = 'array'
                State = 'array-value-or-end'
                Names = $null
            })
            continue
        }
        if ($token.Kind -ceq 'string' -or $token.Kind -ceq 'scalar') {
            continue
        }
        return $false
    }
    return $frames.Count -eq 0 -and $rootState -ceq 'complete'
}

function Get-JsonContainerCloseIndex {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text,
        [Parameter(Mandatory = $true)][int]$OpenIndex
    )

    if ($OpenIndex -lt 0 -or $OpenIndex -ge $Text.Length -or $Text[$OpenIndex] -ne [char]123) {
        return -1
    }
    $depth = 1
    $inString = $false
    $escaped = $false
    for ($index = $OpenIndex + 1; $index -lt $Text.Length; $index++) {
        $character = $Text[$index]
        if ($inString) {
            if ($escaped) {
                $escaped = $false
            } elseif ($character -eq [char]92) {
                $escaped = $true
            } elseif ($character -eq [char]34) {
                $inString = $false
            }
            continue
        }
        if ($character -eq [char]34) {
            $inString = $true
        } elseif ($character -eq [char]123 -or $character -eq [char]91) {
            $depth++
        } elseif ($character -eq [char]125 -or $character -eq [char]93) {
            $depth--
            if ($depth -eq 0) { return $index }
            if ($depth -lt 0) { return -1 }
        }
    }
    return -1
}

function Get-JsonContainerDepthAtIndex {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text,
        [Parameter(Mandatory = $true)][int]$OpenIndex,
        [Parameter(Mandatory = $true)][int]$TargetIndex
    )

    if ($OpenIndex -lt 0 -or $TargetIndex -le $OpenIndex -or $TargetIndex -gt $Text.Length) {
        return -1
    }
    $depth = 1
    $inString = $false
    $escaped = $false
    for ($index = $OpenIndex + 1; $index -lt $TargetIndex; $index++) {
        $character = $Text[$index]
        if ($inString) {
            if ($escaped) {
                $escaped = $false
            } elseif ($character -eq [char]92) {
                $escaped = $true
            } elseif ($character -eq [char]34) {
                $inString = $false
            }
            continue
        }
        if ($character -eq [char]34) {
            $inString = $true
        } elseif ($character -eq [char]123 -or $character -eq [char]91) {
            $depth++
        } elseif ($character -eq [char]125 -or $character -eq [char]93) {
            $depth--
            if ($depth -lt 1) { return -1 }
        }
    }
    if ($inString) { return -1 }
    return $depth
}

function Get-SafeJsonSchemaCredentialReferenceLines {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )

    $lines = New-Object 'System.Collections.Generic.HashSet[int]'
    if (-not $RelativePath.EndsWith('.schema.json', [StringComparison]::OrdinalIgnoreCase)) {
        return ,$lines
    }
    if (-not (Test-JsonObjectPropertyNamesUnique -Text $Text)) {
        return ,$lines
    }
    try {
        $document = $Text | ConvertFrom-Json -ErrorAction Stop
    } catch {
        return ,$lines
    }
    $schemaProperty = @($document.PSObject.Properties | Where-Object { $_.Name -ceq '$schema' })
    if (
        $schemaProperty.Count -ne 1 -or
        [string]$schemaProperty[0].Value -cne 'https://json-schema.org/draft/2020-12/schema'
    ) {
        return ,$lines
    }

    $exactReferenceSchema = Test-ExactPublicSecretRefsSchema -Document $document
    $referencePattern = '(?ms)^(?<indent>[ \t]*)"(?<name>[A-Za-z][A-Za-z0-9_-]*)"\s*:\s*\{\s*\r?\n[ \t]*"\$ref"\s*:\s*"(?<reference>#/\$defs/[A-Za-z][A-Za-z0-9_-]*)"\s*\r?\n[ \t]*\}\s*,?'
    foreach ($match in [regex]::Matches($Text, $referencePattern)) {
        $name = $match.Groups['name'].Value
        if (
            -not $exactReferenceSchema -or
            -not [string]::Equals($name, 'secret_refs', [StringComparison]::Ordinal) -or
            -not [string]::Equals($match.Groups['reference'].Value, '#/$defs/secretRefs', [StringComparison]::Ordinal)
        ) {
            continue
        }
        $line = [regex]::Matches($Text.Substring(0, $match.Index), '\n').Count + 1
        [void]$lines.Add($line)
    }

    if ($exactReferenceSchema) {
        $definitionsMatches = @([regex]::Matches(
            $Text,
            '(?m)^[ \t]*"\$defs"[ \t]*:[ \t]*(?<open>\{)[ \t]*$'
        ))
        if ($definitionsMatches.Count -eq 1) {
            $definitionsOpenIndex = $definitionsMatches[0].Groups['open'].Index
            $definitionsCloseIndex = Get-JsonContainerCloseIndex -Text $Text -OpenIndex $definitionsOpenIndex
            if ($definitionsCloseIndex -gt $definitionsOpenIndex) {
                $definitionNamePattern = '(?m)^[ \t]*"(?<name>secretOpaqueID|secretRef|secretRefs)"[ \t]*:[ \t]*\{[ \t]*$'
                foreach ($match in [regex]::Matches($Text, $definitionNamePattern)) {
                    if (
                        $match.Index -le $definitionsOpenIndex -or
                        $match.Index -ge $definitionsCloseIndex -or
                        (Get-JsonContainerDepthAtIndex -Text $Text -OpenIndex $definitionsOpenIndex -TargetIndex $match.Index) -ne 1
                    ) {
                        continue
                    }
                    $line = [regex]::Matches($Text.Substring(0, $match.Index), '\n').Count + 1
                    [void]$lines.Add($line)
                }
            }
        }
    }
    return ,$lines
}

function Get-GoCodeOnlyProjection {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text)

    $builder = New-Object Text.StringBuilder
    $state = 'code'
    $escaped = $false
    for ($index = 0; $index -lt $Text.Length; $index++) {
        $character = $Text[$index]
        $next = if (($index + 1) -lt $Text.Length) { $Text[$index + 1] } else { [char]0 }
        if ($state -ceq 'line-comment') {
            if ($character -eq [char]10 -or $character -eq [char]13) {
                [void]$builder.Append($character)
                $state = 'code'
            } else {
                [void]$builder.Append(' ')
            }
            continue
        }
        if ($state -ceq 'block-comment') {
            if ($character -eq [char]42 -and $next -eq [char]47) {
                [void]$builder.Append(' ')
                [void]$builder.Append(' ')
                $index++
                $state = 'code'
            } elseif ($character -eq [char]10 -or $character -eq [char]13) {
                [void]$builder.Append($character)
            } else {
                [void]$builder.Append(' ')
            }
            continue
        }
        if ($state -ceq 'raw-string') {
            if ($character -eq [char]96) {
                [void]$builder.Append(' ')
                $state = 'code'
            } elseif ($character -eq [char]10 -or $character -eq [char]13) {
                [void]$builder.Append($character)
            } else {
                [void]$builder.Append(' ')
            }
            continue
        }
        if ($state -ceq 'quoted-string' -or $state -ceq 'rune') {
            if ($character -eq [char]10 -or $character -eq [char]13) {
                [void]$builder.Append($character)
                $state = 'code'
                $escaped = $false
                continue
            }
            [void]$builder.Append(' ')
            if ($escaped) {
                $escaped = $false
            } elseif ($character -eq [char]92) {
                $escaped = $true
            } elseif (
                ($state -ceq 'quoted-string' -and $character -eq [char]34) -or
                ($state -ceq 'rune' -and $character -eq [char]39)
            ) {
                $state = 'code'
            }
            continue
        }
        if ($character -eq [char]47 -and $next -eq [char]47) {
            [void]$builder.Append(' ')
            [void]$builder.Append(' ')
            $index++
            $state = 'line-comment'
        } elseif ($character -eq [char]47 -and $next -eq [char]42) {
            [void]$builder.Append(' ')
            [void]$builder.Append(' ')
            $index++
            $state = 'block-comment'
        } elseif ($character -eq [char]96) {
            [void]$builder.Append(' ')
            $state = 'raw-string'
        } elseif ($character -eq [char]34) {
            [void]$builder.Append(' ')
            $state = 'quoted-string'
            $escaped = $false
        } elseif ($character -eq [char]39) {
            [void]$builder.Append(' ')
            $state = 'rune'
            $escaped = $false
        } else {
            [void]$builder.Append($character)
        }
    }
    return $builder.ToString()
}

function Get-GoGroupedDeclarationLineNumbers {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text,
        [ref]$DeclarationKinds
    )

    $lines = New-Object 'System.Collections.Generic.HashSet[int]'
    if ($null -ne $DeclarationKinds) {
        $DeclarationKinds.Value = New-Object 'System.Collections.Generic.Dictionary[int,string]'
    }
    $projection = Get-GoCodeOnlyProjection -Text $Text
    $pattern = New-Object Text.RegularExpressions.Regex('\b(?<kind>var|const)\s*\(')
    $cursor = 0
    while ($cursor -lt $projection.Length) {
        $match = $pattern.Match($projection, $cursor)
        if (-not $match.Success) { break }
        $openingIndex = $projection.IndexOf([char]40, $match.Index)
        $depth = 1
        $closingIndex = -1
        for ($index = $openingIndex + 1; $index -lt $projection.Length; $index++) {
            if ($projection[$index] -eq [char]40) {
                $depth++
            } elseif ($projection[$index] -eq [char]41) {
                $depth--
                if ($depth -eq 0) {
                    $closingIndex = $index
                    break
                }
            }
        }
        $blockEnd = if ($closingIndex -ge 0) { $closingIndex } else { $projection.Length }
        $startLine = [regex]::Matches($projection.Substring(0, $match.Index), '\r\n|\n|\r').Count + 1
        $endLine = [regex]::Matches($projection.Substring(0, $blockEnd), '\r\n|\n|\r').Count + 1
        for ($line = $startLine; $line -le $endLine; $line++) {
            [void]$lines.Add($line)
            if ($null -ne $DeclarationKinds) {
                $kind = $match.Groups['kind'].Value
                if ($DeclarationKinds.Value.ContainsKey($line)) {
                    if ($DeclarationKinds.Value[$line] -cne $kind) {
                        $DeclarationKinds.Value[$line] = ''
                    }
                } else {
                    $DeclarationKinds.Value.Add($line, $kind)
                }
            }
        }
        if ($closingIndex -lt 0) { break }
        $cursor = $closingIndex + 1
    }
    return ,$lines
}

$credentialNameCandidateRegex = New-Object Text.RegularExpressions.Regex(
    '(?:password|passwd|secret|token|authorization|api[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret)',
    ([Text.RegularExpressions.RegexOptions]::IgnoreCase -bor [Text.RegularExpressions.RegexOptions]::CultureInvariant -bor [Text.RegularExpressions.RegexOptions]::Compiled)
)

$texts = New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::Ordinal)
$filePaths = New-Object 'System.Collections.Generic.List[string]'
foreach ($file in $script:Files) { $filePaths.Add($file.Path) }
$filePaths.Sort([StringComparer]::Ordinal)

$fileByPath = New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal)
foreach ($file in $script:Files) { $fileByPath[$file.Path] = $file }

foreach ($relativePath in $filePaths) {
    $file = $fileByPath[$relativePath]
    Add-TextRuleFindings -Text $relativePath -RelativePath $relativePath
    foreach ($token in $retiredTokens) {
        $boundaryPattern = '(?i)(?<![A-Za-z0-9])' + [regex]::Escape($token) + '(?![A-Za-z0-9])'
        if ([regex]::IsMatch($relativePath, $boundaryPattern)) {
            Add-PublicTreeFinding -Rule 'PT_OLD_IDENTITY' -Path $relativePath
        }
    }

    try {
        [byte[]]$bytes = [IO.File]::ReadAllBytes($file.FullPath)
    } catch {
        Add-PublicTreeFinding -Rule 'PT_IO_READ' -Path $relativePath
        continue
    }
    $ascii = [Text.Encoding]::ASCII.GetString($bytes)
    foreach ($token in $retiredTokens) {
        $boundaryPattern = '(?i)(?<![A-Za-z0-9])' + [regex]::Escape($token) + '(?![A-Za-z0-9])'
        if ([regex]::IsMatch($ascii, $boundaryPattern)) {
            Add-PublicTreeFinding -Rule 'PT_OLD_IDENTITY' -Path $relativePath
        }
    }
    if (Test-BinaryContent -Bytes $bytes -RelativePath $relativePath) {
        Add-PublicTreeFinding -Rule 'PT_BINARY' -Path $relativePath
        continue
    }
    try {
        $utf8 = New-Object Text.UTF8Encoding($false, $true)
        $text = $utf8.GetString($bytes)
    } catch {
        Add-PublicTreeFinding -Rule 'PT_BINARY' -Path $relativePath
        continue
    }
    if ([regex]::IsMatch($text, '[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F-\u009F]')) {
        Add-PublicTreeFinding -Rule 'PT_BINARY' -Path $relativePath
        continue
    }
    $texts[$relativePath] = $text

    $lines = @([regex]::Split($text, '\r\n|\n|\r'))
    $safeJsonSchemaCredentialReferenceLines = Get-SafeJsonSchemaCredentialReferenceLines -Text $text -RelativePath $relativePath
    $isGoCredentialFile = $relativePath.EndsWith('.go', [StringComparison]::OrdinalIgnoreCase)
    $goGroupedDeclarationKinds = $null
    $goGroupedDeclarationLines = if ($isGoCredentialFile) {
        Get-GoGroupedDeclarationLineNumbers -Text $text -DeclarationKinds ([ref]$goGroupedDeclarationKinds)
    } else {
        $goGroupedDeclarationKinds = New-Object 'System.Collections.Generic.Dictionary[int,string]'
        New-Object 'System.Collections.Generic.HashSet[int]'
    }
    $goCredentialLexState = 'code'
    $goCredentialBuffer = ''
    $goCredentialBufferStartLine = 0
    $goCredentialBufferLineCount = 0
    $goCredentialBufferGroupedDeclarationLine = $false
    $goCredentialBufferGroupedDeclarationKind = ''
    $goCredentialBufferRawStringContent = $false
    for ($lineIndex = 0; $lineIndex -lt $lines.Count; $lineIndex++) {
        $line = $lines[$lineIndex]
        $goGroupedDeclarationLine = $isGoCredentialFile -and $goGroupedDeclarationLines.Contains($lineIndex + 1)
        $goGroupedDeclarationKind = if (
            $goGroupedDeclarationLine -and
            $goGroupedDeclarationKinds.ContainsKey($lineIndex + 1)
        ) {
            $goGroupedDeclarationKinds[$lineIndex + 1]
        } else {
            ''
        }
        $goRawStringContent = $false
        Add-TextRuleFindings -Text $line -RelativePath $relativePath -Line ($lineIndex + 1) -SkipCredentialAssignment
        $credentialLine = if (
            $isGoCredentialFile -and
            ($goCredentialLexState -cne 'code' -or $line.IndexOf([char]47) -ge 0 -or $line.IndexOf([char]96) -ge 0)
        ) {
            Remove-GoCommentsFromCredentialLine -Text $line -LexState ([ref]$goCredentialLexState) -ContainsRawStringContent ([ref]$goRawStringContent)
        } else {
            $line
        }
        $credentialCandidate = (
            ($credentialLine.IndexOf([char]58) -ge 0 -or $credentialLine.IndexOf([char]61) -ge 0) -and
            $credentialNameCandidateRegex.IsMatch($credentialLine) -and
            -not $safeJsonSchemaCredentialReferenceLines.Contains($lineIndex + 1)
        )
        if ($isGoCredentialFile -and $goCredentialBufferStartLine -gt 0) {
            $goCredentialBuffer += ' ' + $credentialLine.Trim()
            $goCredentialBufferLineCount++
            $goCredentialBufferRawStringContent = $goCredentialBufferRawStringContent -or $goRawStringContent
            if ($goCredentialBuffer.Length -gt 131072 -or $goCredentialBufferLineCount -gt 256) {
                Add-PublicTreeFinding -Rule 'PT_SECRET_ASSIGNMENT' -Path $relativePath -Line $goCredentialBufferStartLine
                $goCredentialBuffer = ''
                $goCredentialBufferStartLine = 0
                $goCredentialBufferLineCount = 0
                $goCredentialBufferGroupedDeclarationLine = $false
                $goCredentialBufferGroupedDeclarationKind = ''
                $goCredentialBufferRawStringContent = $false
            } elseif (-not (Test-GoCredentialAssignmentNeedsContinuation -Text $goCredentialBuffer -GroupedDeclarationLine $goCredentialBufferGroupedDeclarationLine -RawStringContent $goCredentialBufferRawStringContent)) {
                if (Test-CredentialAssignmentSecret -Text $goCredentialBuffer -RelativePath $relativePath -GoGroupedDeclarationLine $goCredentialBufferGroupedDeclarationLine -GoGroupedDeclarationKind $goCredentialBufferGroupedDeclarationKind -GoRawStringContent $goCredentialBufferRawStringContent) {
                    Add-PublicTreeFinding -Rule 'PT_SECRET_ASSIGNMENT' -Path $relativePath -Line $goCredentialBufferStartLine
                }
                $goCredentialBuffer = ''
                $goCredentialBufferStartLine = 0
                $goCredentialBufferLineCount = 0
                $goCredentialBufferGroupedDeclarationLine = $false
                $goCredentialBufferGroupedDeclarationKind = ''
                $goCredentialBufferRawStringContent = $false
            }
        } elseif ($credentialCandidate) {
            if ($isGoCredentialFile -and (Test-GoCredentialAssignmentNeedsContinuation -Text $credentialLine -GroupedDeclarationLine $goGroupedDeclarationLine -RawStringContent $goRawStringContent)) {
                $goCredentialBuffer = $credentialLine.Trim()
                $goCredentialBufferStartLine = $lineIndex + 1
                $goCredentialBufferLineCount = 1
                $goCredentialBufferGroupedDeclarationLine = $goGroupedDeclarationLine
                $goCredentialBufferGroupedDeclarationKind = $goGroupedDeclarationKind
                $goCredentialBufferRawStringContent = $goRawStringContent
            } elseif (Test-CredentialAssignmentSecret -Text $credentialLine -RelativePath $relativePath -GoGroupedDeclarationLine $goGroupedDeclarationLine -GoGroupedDeclarationKind $goGroupedDeclarationKind -GoRawStringContent $goRawStringContent) {
                Add-PublicTreeFinding -Rule 'PT_SECRET_ASSIGNMENT' -Path $relativePath -Line ($lineIndex + 1)
            }
        }
        Add-RetiredConstructionReferenceFindings -Text $line -RelativePath $relativePath -Line ($lineIndex + 1)
        foreach ($token in $retiredTokens) {
            $boundaryPattern = '(?i)(?<![A-Za-z0-9])' + [regex]::Escape($token) + '(?![A-Za-z0-9])'
            if ([regex]::IsMatch($line, $boundaryPattern)) {
                Add-PublicTreeFinding -Rule 'PT_OLD_IDENTITY' -Path $relativePath -Line ($lineIndex + 1)
            }
        }
    }
    if ($goCredentialBufferStartLine -gt 0) {
        Add-PublicTreeFinding -Rule 'PT_SECRET_ASSIGNMENT' -Path $relativePath -Line $goCredentialBufferStartLine
    }
    if ($relativePath.EndsWith('.go', [StringComparison]::OrdinalIgnoreCase)) {
        Test-LegacyGoImports -Text $text -RelativePath $relativePath
    }
}

if (-not $texts.ContainsKey('go.mod')) {
    Add-PublicTreeFinding -Rule 'PT_GO_MOD_MISSING' -Path 'go.mod'
} else {
    $moduleMatches = [regex]::Matches($texts['go.mod'], '(?m)^\s*module\s+(\S+)\s*$')
    if ($moduleMatches.Count -ne 1 -or $moduleMatches[0].Groups[1].Value -cne 'github.com/endview/freeagent') {
        Add-PublicTreeFinding -Rule 'PT_GO_MOD_IDENTITY' -Path 'go.mod'
    }
}

if (-not $texts.ContainsKey('VERSION')) {
    Add-PublicTreeFinding -Rule 'PT_VERSION_MISSING' -Path 'VERSION'
} else {
    $versionPattern = '^v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?\n$'
    if (-not [regex]::IsMatch($texts['VERSION'], $versionPattern)) {
        Add-PublicTreeFinding -Rule 'PT_VERSION_INVALID' -Path 'VERSION'
    }
}

$actionPins = New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::OrdinalIgnoreCase)
foreach ($relativePath in $filePaths) {
    $leaf = [IO.Path]::GetFileName($relativePath)
    $isWorkflow = $relativePath.StartsWith('.github/workflows/', [StringComparison]::Ordinal) -and ($relativePath.EndsWith('.yml', [StringComparison]::OrdinalIgnoreCase) -or $relativePath.EndsWith('.yaml', [StringComparison]::OrdinalIgnoreCase))
    $isActionMetadata = $leaf -ieq 'action.yml' -or $leaf -ieq 'action.yaml'
    if ((-not $isWorkflow -and -not $isActionMetadata) -or -not $texts.ContainsKey($relativePath)) {
        continue
    }

    $yamlKind = if ($isWorkflow) { 'Workflow' } else { 'Action' }
    $usesRecords = @(Get-GitHubYamlUses -Text $texts[$relativePath] -RelativePath $relativePath -Kind $yamlKind)
    foreach ($usesRecord in $usesRecords) {
        $reference = Get-ActionReference -Value $usesRecord.Value
        if ($null -eq $reference) {
            Add-PublicTreeFinding -Rule 'PT_ACTION_REF' -Path $relativePath -Line $usesRecord.Line
            continue
        }
        if (-not $reference -or $reference.Contains('${{') -or $usesRecord.Context -ceq 'Unknown') {
            Add-PublicTreeFinding -Rule 'PT_ACTION_REF' -Path $relativePath -Line $usesRecord.Line
            continue
        }
        if ($reference.StartsWith('./', [StringComparison]::Ordinal)) {
            if ($reference -notmatch '^\./[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*$') {
                Add-PublicTreeFinding -Rule 'PT_ACTION_REF' -Path $relativePath -Line $usesRecord.Line
                continue
            }
            $localSegments = @($reference.Substring(2).Split([char]47))
            if (@($localSegments | Where-Object { -not $_ -or $_ -eq '.' -or $_ -eq '..' }).Count -gt 0) {
                Add-PublicTreeFinding -Rule 'PT_ACTION_REF' -Path $relativePath -Line $usesRecord.Line
                continue
            }
            $localRelative = $reference.Substring(2)
            if ($usesRecord.Context -ceq 'Job') {
                if ($localRelative -notmatch '^\.github/workflows/[A-Za-z0-9_.-]+\.(?:yml|yaml)$' -or -not $fileByPath.ContainsKey($localRelative)) {
                    Add-PublicTreeFinding -Rule 'PT_ACTION_REF' -Path $relativePath -Line $usesRecord.Line
                }
            } elseif ($usesRecord.Context -ceq 'Step' -or $usesRecord.Context -ceq 'CompositeStep') {
                $actionYml = $localRelative + '/action.yml'
                $actionYaml = $localRelative + '/action.yaml'
                $manifestCount = 0
                if ($fileByPath.ContainsKey($actionYml)) { $manifestCount++ }
                if ($fileByPath.ContainsKey($actionYaml)) { $manifestCount++ }
                if ($manifestCount -ne 1) {
                    Add-PublicTreeFinding -Rule 'PT_ACTION_REF' -Path $relativePath -Line $usesRecord.Line
                }
            } else {
                Add-PublicTreeFinding -Rule 'PT_ACTION_REF' -Path $relativePath -Line $usesRecord.Line
            }
            continue
        }

        $remotePattern = if ($usesRecord.Context -ceq 'Job') {
            '^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/\.github/workflows/[A-Za-z0-9_.-]+\.(?:yml|yaml))@([0-9A-Fa-f]{40})$'
        } elseif ($usesRecord.Context -ceq 'Step' -or $usesRecord.Context -ceq 'CompositeStep') {
            '^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*)@([0-9A-Fa-f]{40})$'
        } else {
            '^$'
        }
        $remote = [regex]::Match($reference, $remotePattern)
        if (-not $remote.Success) {
            Add-PublicTreeFinding -Rule 'PT_ACTION_REF' -Path $relativePath -Line $usesRecord.Line
            continue
        }
        $identity = $remote.Groups[1].Value
        $identitySegments = @($identity.Split([char]47))
        if ($identitySegments.Count -lt 2 -or $identity.StartsWith('docker:', [StringComparison]::OrdinalIgnoreCase) -or @($identitySegments | Where-Object { -not $_ -or $_ -eq '.' -or $_ -eq '..' }).Count -gt 0) {
            Add-PublicTreeFinding -Rule 'PT_ACTION_REF' -Path $relativePath -Line $usesRecord.Line
            continue
        }
        $sha = $remote.Groups[2].Value.ToLowerInvariant()
        if ($actionPins.ContainsKey($identity)) {
            if ($actionPins[$identity] -cne $sha) {
                Add-PublicTreeFinding -Rule 'PT_ACTION_DRIFT' -Path $relativePath -Line $usesRecord.Line
            }
        } else {
            $actionPins.Add($identity, $sha)
        }
    }
}

if ($script:Findings.Count -gt 0) {
    $findingKeys = New-Object 'System.Collections.Generic.List[string]'
    $findingByKey = New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal)
    foreach ($finding in $script:Findings) {
        $findingKeys.Add($finding.Key)
        $findingByKey[$finding.Key] = $finding
    }
    $findingKeys.Sort([StringComparer]::Ordinal)
    foreach ($key in $findingKeys) {
        $finding = $findingByKey[$key]
        $displayPath = ConvertTo-SafeDisplayPath -Path $finding.Path -Rule $finding.Rule
        if ($finding.Line -gt 0) {
            Write-Output "PUBLIC_TREE_FAIL rule=$($finding.Rule) path=$displayPath line=$($finding.Line)"
        } else {
            Write-Output "PUBLIC_TREE_FAIL rule=$($finding.Rule) path=$displayPath"
        }
    }
    exit 1
}

Write-Output 'PUBLIC_TREE_PASS'
