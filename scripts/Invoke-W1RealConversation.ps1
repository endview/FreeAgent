[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$FreeAgentPath,
    [Parameter(Mandatory = $true)][string]$SeedPath,
    [Parameter(Mandatory = $true)][string]$SecretEnvironmentName,
    [ValidateRange(60000, 1800000)]
    [int]$CommandTimeoutMilliseconds = 600000
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:Utf8NoBom = New-Object Text.UTF8Encoding($false, $true)
$script:PathComparison = [StringComparison]::OrdinalIgnoreCase
$script:RootPrefix = 'freeagent-w1-real-conversation-'
$script:Root = $null
$script:TempRoot = $null
$script:FailureCode = 'UNCLASSIFIED'
$script:Phase = 'preflight'
$script:TurnsSucceeded = 0
$script:StageTwoVerified = $false
$script:Turn25BackupVerified = $false
$script:Turn25RestoreVerified = $false
$script:Retry25Verified = $false
$script:Retry50Verified = $false
$script:FinalBackupVerified = $false
$script:FinalRevision = 0
$script:FinalAttemptGuards = $null
$script:MaximumCaptureCharacters = 4194304
$script:UsageAttempts = 0
$script:UsageTotals = [ordered]@{
    input = [uint64]0
    cached_input = [uint64]0
    uncached_input = [uint64]0
    output = [uint64]0
    reasoning = [uint64]0
}
$script:UsageKnownCounts = [ordered]@{
    input = 0
    cached_input = 0
    uncached_input = 0
    output = 0
    reasoning = 0
}
$script:UsageUnknownCounts = [ordered]@{
    input = 0
    cached_input = 0
    uncached_input = 0
    output = 0
    reasoning = 0
}
$script:UsageStatusCounts = [ordered]@{
    pending = 0
    provider_reported = 0
    no_usage_reported = 0
    pending_reconciliation = 0
}
$script:UsageCompleteFactAttempts = 0
$script:UsageNoFactAttempts = 0
$script:CacheWeightedEligibleAttempts = 0
$script:CacheWeightedUnknownAttempts = 0
$script:CacheWeightedHits = [uint64]0
$script:CacheWeightedMisses = [uint64]0
$script:CacheRequestHits = 0
$script:CacheRequestMisses = 0
$script:CacheRequestUnknown = 0

function Fail-W1RealConversation {
    param([Parameter(Mandatory = $true)][string]$Code)
    $script:FailureCode = $Code
    throw 'W1_REAL_CONVERSATION_STOP'
}

function Get-W1FullPath {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if ([string]::IsNullOrWhiteSpace($Value)) {
        Fail-W1RealConversation -Code $Code
    }
    foreach ($character in $Value.ToCharArray()) {
        if ([char]::IsControl($character)) {
            Fail-W1RealConversation -Code $Code
        }
    }
    try {
        return [IO.Path]::GetFullPath($Value)
    } catch {
        Fail-W1RealConversation -Code $Code
    }
}

function Assert-W1NoReparseAncestry {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $cursor = $Path
    while (-not [string]::IsNullOrWhiteSpace($cursor)) {
        if (Test-Path -LiteralPath $cursor) {
            try {
                $item = Get-Item -LiteralPath $cursor -Force
            } catch {
                Fail-W1RealConversation -Code $Code
            }
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                Fail-W1RealConversation -Code $Code
            }
        }
        $parent = [IO.Path]::GetDirectoryName($cursor)
        if ([string]::IsNullOrWhiteSpace($parent) -or
            [string]::Equals($parent, $cursor, $script:PathComparison)) {
            break
        }
        $cursor = $parent
    }
}

function Assert-W1RegularFile {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        Fail-W1RealConversation -Code $Code
    }
    Assert-W1NoReparseAncestry -Path $Path -Code $Code
    try {
        $item = Get-Item -LiteralPath $Path -Force
    } catch {
        Fail-W1RealConversation -Code $Code
    }
    if (-not ($item -is [IO.FileInfo]) -or $item.Length -le 0) {
        Fail-W1RealConversation -Code $Code
    }
}

function Test-W1OpaqueIdentity {
    param([AllowNull()][string]$Value)
    if ([string]::IsNullOrWhiteSpace($Value) -or
        $Value.Length -gt 256 -or $Value -cne $Value.Trim()) {
        return $false
    }
    foreach ($character in $Value.ToCharArray()) {
        if ([char]::IsControl($character)) { return $false }
    }
    return $true
}

function ConvertTo-W1WindowsArgument {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value)
    if ($Value.Length -gt 0 -and $Value -notmatch '[\s"]') {
        return $Value
    }
    $builder = New-Object Text.StringBuilder
    [void]$builder.Append('"')
    $slashes = 0
    foreach ($character in $Value.ToCharArray()) {
        if ($character -eq [char]92) {
            $slashes++
            continue
        }
        if ($character -eq [char]34) {
            [void]$builder.Append(('\' * (($slashes * 2) + 1)))
            [void]$builder.Append('"')
            $slashes = 0
            continue
        }
        if ($slashes -gt 0) {
            [void]$builder.Append(('\' * $slashes))
            $slashes = 0
        }
        [void]$builder.Append($character)
    }
    if ($slashes -gt 0) {
        [void]$builder.Append(('\' * ($slashes * 2)))
    }
    [void]$builder.Append('"')
    return $builder.ToString()
}

function Join-W1WindowsArguments {
    param([Parameter(Mandatory = $true)][string[]]$Values)
    return (($Values | ForEach-Object {
        ConvertTo-W1WindowsArgument -Value ([string]$_)
    }) -join ' ')
}

function Get-W1Sha256 {
    param([Parameter(Mandatory = $true)][string]$Path)
    $stream = $null
    $sha = $null
    try {
        $stream = [IO.File]::Open(
            $Path,
            [IO.FileMode]::Open,
            [IO.FileAccess]::Read,
            [IO.FileShare]::Read
        )
        $sha = [Security.Cryptography.SHA256]::Create()
        $bytes = $sha.ComputeHash($stream)
        return ([BitConverter]::ToString($bytes)).Replace('-', '').ToLowerInvariant()
    } catch {
        Fail-W1RealConversation -Code 'SOURCE_HASH_FAILED'
    } finally {
        if ($null -ne $sha) { $sha.Dispose() }
        if ($null -ne $stream) { $stream.Dispose() }
    }
}

function Read-W1JsonFile {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    try {
        $text = $script:Utf8NoBom.GetString([IO.File]::ReadAllBytes($Path))
        $value = $text | ConvertFrom-Json -ErrorAction Stop
    } catch {
        Fail-W1RealConversation -Code $Code
    }
    if ($null -eq $value -or -not ($value -is [pscustomobject])) {
        Fail-W1RealConversation -Code $Code
    }
    return $value
}

function Get-W1PropertyValue {
    param(
        [Parameter(Mandatory = $true)]$Object,
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if ($null -eq $Object -or -not ($Object -is [pscustomobject])) {
        Fail-W1RealConversation -Code $Code
    }
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) {
        Fail-W1RealConversation -Code $Code
    }
    return $property.Value
}

function Add-W1UInt64Exact {
    param(
        [Parameter(Mandatory = $true)][uint64]$Left,
        [Parameter(Mandatory = $true)][uint64]$Right,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if ($Right -gt ([uint64]::MaxValue - $Left)) {
        Fail-W1RealConversation -Code $Code
    }
    return [uint64]($Left + $Right)
}

function ConvertTo-W1OptionalUsageCount {
    param(
        [Parameter(Mandatory = $true)]$Object,
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $value = Get-W1PropertyValue $Object $Name $Code
    if ($null -eq $value) { return $null }
    if ($value -is [bool]) { Fail-W1RealConversation -Code $Code }
    try {
        $text = [Convert]::ToString(
            $value,
            [Globalization.CultureInfo]::InvariantCulture
        )
    } catch {
        Fail-W1RealConversation -Code $Code
    }
    if ($text -notmatch '^(0|[1-9][0-9]*)$') {
        Fail-W1RealConversation -Code $Code
    }
    [uint64]$parsed = 0
    if (-not [uint64]::TryParse(
            $text,
            [Globalization.NumberStyles]::None,
            [Globalization.CultureInfo]::InvariantCulture,
            [ref]$parsed
        )) {
        Fail-W1RealConversation -Code $Code
    }
    return $parsed
}

function Add-W1OriginalUsage {
    param(
        [Parameter(Mandatory = $true)]$Result,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $usage = Get-W1PropertyValue $Result 'usage' $Code
    if ($null -eq $usage -or -not ($usage -is [pscustomobject])) {
        Fail-W1RealConversation -Code $Code
    }

    $status = [string](Get-W1PropertyValue $usage 'status' $Code)
    $statusSlot = switch -CaseSensitive ($status) {
        'PENDING' { 'pending' }
        'PROVIDER_REPORTED' { 'provider_reported' }
        'NO_USAGE_REPORTED' { 'no_usage_reported' }
        'PENDING_RECONCILIATION' { 'pending_reconciliation' }
        default { Fail-W1RealConversation -Code $Code }
    }

    $fields = @(
        [pscustomobject]@{ Name = 'input_tokens'; Slot = 'input' },
        [pscustomobject]@{ Name = 'cached_input_tokens'; Slot = 'cached_input' },
        [pscustomobject]@{ Name = 'uncached_input_tokens'; Slot = 'uncached_input' },
        [pscustomobject]@{ Name = 'output_tokens'; Slot = 'output' },
        [pscustomobject]@{ Name = 'reasoning_tokens'; Slot = 'reasoning' }
    )
    $values = @{}
    $nextTotals = @{}
    $knownFacts = 0
    foreach ($field in $fields) {
        $number = ConvertTo-W1OptionalUsageCount `
            -Object $usage `
            -Name $field.Name `
            -Code $Code
        $values[$field.Slot] = $number
        if ($null -ne $number) {
            $nextTotals[$field.Slot] = Add-W1UInt64Exact `
                -Left $script:UsageTotals[$field.Slot] `
                -Right $number `
                -Code 'USAGE_TOTAL_OVERFLOW'
            $knownFacts++
        }
    }

    if ($null -ne $values.input -and $null -ne $values.cached_input -and
        $null -ne $values.uncached_input) {
        $partition = Add-W1UInt64Exact `
            -Left $values.cached_input `
            -Right $values.uncached_input `
            -Code 'USAGE_INPUT_PARTITION_OVERFLOW'
        if ($partition -ne $values.input) {
            Fail-W1RealConversation -Code 'USAGE_INPUT_PARTITION_INVALID'
        }
    }

    $nextWeightedHits = $script:CacheWeightedHits
    $nextWeightedMisses = $script:CacheWeightedMisses
    if ($null -ne $values.cached_input -and
        $null -ne $values.uncached_input) {
        $nextWeightedHits = Add-W1UInt64Exact `
            -Left $script:CacheWeightedHits `
            -Right $values.cached_input `
            -Code 'USAGE_CACHE_TOTAL_OVERFLOW'
        $nextWeightedMisses = Add-W1UInt64Exact `
            -Left $script:CacheWeightedMisses `
            -Right $values.uncached_input `
            -Code 'USAGE_CACHE_TOTAL_OVERFLOW'
    }

    $script:UsageStatusCounts[$statusSlot]++
    foreach ($field in $fields) {
        if ($null -eq $values[$field.Slot]) {
            $script:UsageUnknownCounts[$field.Slot]++
        } else {
            $script:UsageTotals[$field.Slot] = $nextTotals[$field.Slot]
            $script:UsageKnownCounts[$field.Slot]++
        }
    }
    if ($knownFacts -eq $fields.Count) {
        $script:UsageCompleteFactAttempts++
    } elseif ($knownFacts -eq 0) {
        $script:UsageNoFactAttempts++
    }
    if ($null -eq $values.cached_input) {
        $script:CacheRequestUnknown++
    } elseif ($values.cached_input -gt 0) {
        $script:CacheRequestHits++
    } else {
        $script:CacheRequestMisses++
    }
    if ($null -ne $values.cached_input -and
        $null -ne $values.uncached_input) {
        $script:CacheWeightedHits = $nextWeightedHits
        $script:CacheWeightedMisses = $nextWeightedMisses
        $script:CacheWeightedEligibleAttempts++
    } else {
        $script:CacheWeightedUnknownAttempts++
    }
    $script:UsageAttempts++
}

function New-W1UsageFieldSummary {
    param([Parameter(Mandatory = $true)][string]$Slot)
    $total = if ($script:UsageKnownCounts[$Slot] -gt 0) {
        $script:UsageTotals[$Slot]
    } else {
        $null
    }
    return [ordered]@{
        total = $total
        known_attempts = $script:UsageKnownCounts[$Slot]
        unknown_attempts = $script:UsageUnknownCounts[$Slot]
    }
}

function New-W1UsageSummary {
    $weightedDenominator = Add-W1UInt64Exact `
        -Left $script:CacheWeightedHits `
        -Right $script:CacheWeightedMisses `
        -Code 'USAGE_CACHE_TOTAL_OVERFLOW'
    $weightedRatio = if ($weightedDenominator -gt 0) {
        [Math]::Round(
            ([double]$script:CacheWeightedHits / [double]$weightedDenominator),
            12
        )
    } else {
        $null
    }
    $knownCacheRequests = $script:CacheRequestHits + $script:CacheRequestMisses
    $requestRatio = if ($knownCacheRequests -gt 0) {
        [Math]::Round(
            ([double]$script:CacheRequestHits / [double]$knownCacheRequests),
            12
        )
    } else {
        $null
    }
    return [ordered]@{
        expected_original_attempts = 50
        original_attempts_observed = $script:UsageAttempts
        status_counts = $script:UsageStatusCounts
        tokens = [ordered]@{
            input_tokens = New-W1UsageFieldSummary -Slot 'input'
            cached_input_tokens = New-W1UsageFieldSummary -Slot 'cached_input'
            uncached_input_tokens = New-W1UsageFieldSummary -Slot 'uncached_input'
            output_tokens = New-W1UsageFieldSummary -Slot 'output'
            reasoning_tokens = New-W1UsageFieldSummary -Slot 'reasoning'
        }
        cache = [ordered]@{
            weighted_cache_hit_ratio = $weightedRatio
            weighted_cached_input = $(if (
                    $script:CacheWeightedEligibleAttempts -gt 0
                ) { $script:CacheWeightedHits } else { $null })
            weighted_uncached_input = $(if (
                    $script:CacheWeightedEligibleAttempts -gt 0
                ) { $script:CacheWeightedMisses } else { $null })
            weighted_eligible_attempts = $script:CacheWeightedEligibleAttempts
            weighted_unknown_attempts = $script:CacheWeightedUnknownAttempts
            request_hit_ratio = $requestRatio
            request_hit_attempts = $script:CacheRequestHits
            request_miss_attempts = $script:CacheRequestMisses
            request_unknown_attempts = $script:CacheRequestUnknown
        }
        unknown_coverage = [ordered]@{
            attempts_with_complete_usage_facts = `
                $script:UsageCompleteFactAttempts
            attempts_without_any_usage_facts = $script:UsageNoFactAttempts
        }
    }
}

function Invoke-W1Process {
    param(
        [Parameter(Mandatory = $true)][string]$Executable,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$WorkingDirectory,
        [Parameter(Mandatory = $true)][int]$TimeoutMilliseconds,
        [switch]$WithoutSecret
    )
    $process = $null
    $jobHandle = [IntPtr]::Zero
    $started = $false
    $timedOut = $false
    $captureFailed = $false
    $stdout = ''
    $stderr = ''
    $exitCode = 125
    try {
        $start = New-Object Diagnostics.ProcessStartInfo
        $start.FileName = $Executable
        $start.Arguments = Join-W1WindowsArguments -Values $Arguments
        $start.WorkingDirectory = $WorkingDirectory
        $start.UseShellExecute = $false
        $start.CreateNoWindow = $true
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $true
        $start.StandardOutputEncoding = $script:Utf8NoBom
        $start.StandardErrorEncoding = $script:Utf8NoBom
        if ($WithoutSecret) {
            [void]$start.EnvironmentVariables.Remove($SecretEnvironmentName)
        }
        $process = New-Object Diagnostics.Process
        $process.StartInfo = $start
        if (-not $process.Start()) { throw 'start failed' }
        $started = $true
        $jobHandle = [W1ProcessJob]::Create($process)
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        $timedOut = -not $process.WaitForExit($TimeoutMilliseconds)
        if ($timedOut) {
            [W1ProcessJob]::Terminate($jobHandle)
            if (-not $process.WaitForExit(30000)) {
                throw 'bounded termination failed'
            }
        }
        [W1ProcessJob]::Close($jobHandle)
        $jobHandle = [IntPtr]::Zero
        if (-not $stdoutTask.Wait(30000) -or -not $stderrTask.Wait(30000)) {
            throw 'bounded capture drain failed'
        }
        $stdout = [string]$stdoutTask.Result
        $stderr = [string]$stderrTask.Result
        if ($stdout.Length -gt $script:MaximumCaptureCharacters -or
            $stderr.Length -gt $script:MaximumCaptureCharacters) {
            $captureFailed = $true
        }
        $exitCode = if ($timedOut) { 124 } else { [int]$process.ExitCode }
    } catch {
        $captureFailed = $true
        if ($jobHandle -ne [IntPtr]::Zero) {
            try { [W1ProcessJob]::Terminate($jobHandle) } catch {}
            try { [W1ProcessJob]::Close($jobHandle) } catch {}
            $jobHandle = [IntPtr]::Zero
        } elseif ($null -ne $process) {
            try {
                if (-not $process.HasExited) { $process.Kill() }
            } catch {}
        }
    } finally {
        if ($jobHandle -ne [IntPtr]::Zero) {
            try { [W1ProcessJob]::Close($jobHandle) } catch {}
        }
        if ($null -ne $process) { $process.Dispose() }
    }
    return [pscustomobject]@{
        Started = [bool]$started
        ExitCode = [int]$exitCode
        TimedOut = [bool]$timedOut
        CaptureFailed = [bool]$captureFailed
        Stdout = $stdout
        Stderr = $stderr
    }
}

function Invoke-W1JsonCommand {
    param(
        [Parameter(Mandatory = $true)][string]$Phase,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$FailureCode,
        [switch]$WithoutSecret
    )
    $script:Phase = $Phase
    $result = Invoke-W1Process `
        -Executable $script:FreeAgent `
        -Arguments $Arguments `
        -WorkingDirectory $script:Root `
        -TimeoutMilliseconds $CommandTimeoutMilliseconds `
        -WithoutSecret:$WithoutSecret
    if (-not $result.Started -or $result.TimedOut -or
        $result.CaptureFailed -or $result.ExitCode -ne 0) {
        $result.Stdout = ''
        $result.Stderr = ''
        Fail-W1RealConversation -Code $FailureCode
    }
    try {
        $value = $result.Stdout | ConvertFrom-Json -ErrorAction Stop
    } catch {
        $result.Stdout = ''
        $result.Stderr = ''
        Fail-W1RealConversation -Code ($FailureCode + '_JSON_INVALID')
    }
    $result.Stdout = ''
    $result.Stderr = ''
    if ($null -eq $value -or -not ($value -is [pscustomobject])) {
        Fail-W1RealConversation -Code ($FailureCode + '_JSON_INVALID')
    }
    return $value
}

function New-W1ChatArguments {
    param(
        [Parameter(Mandatory = $true)][string]$Database,
        [Parameter(Mandatory = $true)][string]$Artifacts,
        [Parameter(Mandatory = $true)][uint64]$ExpectedRevision,
        [AllowEmptyString()][string]$ExpectedHead,
        [Parameter(Mandatory = $true)][string]$Message,
        [Parameter(Mandatory = $true)][string]$RequestId,
        [Parameter(Mandatory = $true)][string]$Deadline
    )
    $values = New-Object 'Collections.Generic.List[string]'
    foreach ($value in @(
        'chat', '--db', $Database, '--artifact-root', $Artifacts,
        '--tenant', $script:TenantId,
        '--principal', 'local-operator',
        '--workspace', $script:WorkspaceId,
        '--agent', $script:AgentId,
        '--profile', $script:ProfileId,
        '--conversation', $script:ConversationId,
        '--conversation-revision', [string]$ExpectedRevision,
        '--message', $Message,
        '--request-id', $RequestId,
        '--deadline', $Deadline,
        '--enable-deepseek',
        '--deepseek-api-key-env', $SecretEnvironmentName
    )) {
        $values.Add([string]$value)
    }
    if (-not [string]::IsNullOrWhiteSpace($ExpectedHead)) {
        $values.Add('--conversation-head-run')
        $values.Add($ExpectedHead)
    }
    return $values.ToArray()
}

function Assert-W1ChatSucceeded {
    param(
        [Parameter(Mandatory = $true)]$Result,
        [Parameter(Mandatory = $true)][int]$Turn,
        [Parameter(Mandatory = $true)][string]$RequestId,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $disposition = [string](Get-W1PropertyValue $Result 'disposition' $Code)
    $reason = [string](Get-W1PropertyValue $Result 'reason' $Code)
    $failure = [string](Get-W1PropertyValue $Result 'failure' $Code)
    if ($reason.IndexOf('UNKNOWN', [StringComparison]::OrdinalIgnoreCase) -ge 0 -or
        $failure.IndexOf('UNKNOWN', [StringComparison]::OrdinalIgnoreCase) -ge 0) {
        Fail-W1RealConversation -Code 'MODEL_UNKNOWN_NO_REPLAY'
    }
    $runId = [string](Get-W1PropertyValue $Result 'run_id' $Code)
    $reply = [string](Get-W1PropertyValue $Result 'reply' $Code)
    if ($disposition -cne 'TERMINATED' -or $reason -cne 'MODEL_SUCCEEDED' -or
        -not [string]::IsNullOrWhiteSpace($failure) -or
        -not (Test-W1OpaqueIdentity -Value $runId) -or
        [string]::IsNullOrWhiteSpace($reply) -or
        [string](Get-W1PropertyValue $Result 'request_id' $Code) -cne $RequestId -or
        [string](Get-W1PropertyValue $Result 'conversation_id' $Code) -cne
            $script:ConversationId -or
        [uint64](Get-W1PropertyValue $Result 'conversation_revision' $Code) -ne
            [uint64]$Turn) {
        Fail-W1RealConversation -Code $Code
    }
}

function Assert-W1ExactRetry {
    param(
        [Parameter(Mandatory = $true)]$Original,
        [Parameter(Mandatory = $true)]$Retry,
        [Parameter(Mandatory = $true)][string]$Code
    )
    foreach ($name in @(
        'request_id', 'deadline', 'run_id', 'conversation_id',
        'conversation_revision', 'disposition', 'reason', 'reply', 'failure'
    )) {
        $left = Get-W1PropertyValue $Original $name $Code
        $right = Get-W1PropertyValue $Retry $name $Code
        if ([string]$left -cne [string]$right) {
            Fail-W1RealConversation -Code $Code
        }
    }
    $leftUsage = Get-W1PropertyValue $Original 'usage' $Code
    $rightUsage = Get-W1PropertyValue $Retry 'usage' $Code
    if ($null -eq $leftUsage -or $null -eq $rightUsage -or
        -not ($leftUsage -is [pscustomobject]) -or
        -not ($rightUsage -is [pscustomobject])) {
        Fail-W1RealConversation -Code $Code
    }
    try {
        $leftCanonical = $leftUsage | ConvertTo-Json -Depth 8 -Compress
        $rightCanonical = $rightUsage | ConvertTo-Json -Depth 8 -Compress
    } catch {
        Fail-W1RealConversation -Code $Code
    }
    if ($leftCanonical -cne $rightCanonical) {
        Fail-W1RealConversation -Code $Code
    }
}

function Invoke-W1BackupVerify {
    param(
        [Parameter(Mandatory = $true)][string]$Database,
        [Parameter(Mandatory = $true)][string]$Artifacts,
        [Parameter(Mandatory = $true)][string]$Bundle,
        [Parameter(Mandatory = $true)][string]$Label
    )
    $backup = Invoke-W1JsonCommand `
        -Phase ($Label + '-backup') `
        -Arguments @(
            'backup', '--db', $Database, '--artifact-root', $Artifacts,
            '--out', $Bundle
        ) `
        -FailureCode ($Label.ToUpperInvariant() + '_BACKUP_FAILED') `
        -WithoutSecret
    $manifest = Get-W1PropertyValue `
        -Object $backup `
        -Name 'manifest' `
        -Code ($Label.ToUpperInvariant() + '_BACKUP_INVALID')
    $digest = [string](Get-W1PropertyValue `
        -Object $manifest `
        -Name 'manifest_digest' `
        -Code ($Label.ToUpperInvariant() + '_BACKUP_INVALID'))
    if ($digest -cnotmatch '^[0-9a-f]{64}$') {
        Fail-W1RealConversation -Code ($Label.ToUpperInvariant() + '_BACKUP_INVALID')
    }
    $verified = Invoke-W1JsonCommand `
        -Phase ($Label + '-backup-verify') `
        -Arguments @('backup-verify', '--bundle', $Bundle) `
        -FailureCode ($Label.ToUpperInvariant() + '_BACKUP_VERIFY_FAILED') `
        -WithoutSecret
    $verifiedManifest = Get-W1PropertyValue `
        -Object $verified `
        -Name 'manifest' `
        -Code ($Label.ToUpperInvariant() + '_BACKUP_VERIFY_INVALID')
    $verifiedDigest = [string](Get-W1PropertyValue `
        -Object $verifiedManifest `
        -Name 'manifest_digest' `
        -Code ($Label.ToUpperInvariant() + '_BACKUP_VERIFY_INVALID'))
    if ($verifiedDigest -cne $digest) {
        Fail-W1RealConversation -Code ($Label.ToUpperInvariant() + '_BACKUP_DIGEST_DRIFT')
    }
    return [pscustomobject]@{
        Backup = $backup
        Manifest = $manifest
        Digest = $digest
    }
}

function Assert-W1ConversationHead {
    param(
        [Parameter(Mandatory = $true)][string]$Database,
        [Parameter(Mandatory = $true)][uint64]$Revision,
        [Parameter(Mandatory = $true)][string]$Head,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $record = Invoke-W1JsonCommand `
        -Phase 'conversation-get' `
        -Arguments @(
            'conversation-get', '--db', $Database,
            '--tenant', $script:TenantId,
            '--conversation', $script:ConversationId
        ) `
        -FailureCode $Code `
        -WithoutSecret
    if ([uint64](Get-W1PropertyValue $record 'revision' $Code) -ne $Revision -or
        [string](Get-W1PropertyValue $record 'head_run_id' $Code) -cne $Head -or
        [string](Get-W1PropertyValue $record 'conversation_id' $Code) -cne
            $script:ConversationId) {
        Fail-W1RealConversation -Code $Code
    }
}

function Get-W1AttemptGuards {
    param([Parameter(Mandatory = $true)]$Manifest)
    $counts = Get-W1PropertyValue `
        -Object $Manifest `
        -Name 'attempt_counts' `
        -Code 'FINAL_ATTEMPT_GUARDS_MISSING'
    $result = [ordered]@{}
    foreach ($name in @(
        'model_pending', 'model_unknown', 'action_pending', 'action_unknown',
        'channel_ingress_receipts', 'channel_cursor_scopes',
        'channel_send_pending', 'channel_send_unknown'
    )) {
        $value = [int64](Get-W1PropertyValue `
            -Object $counts `
            -Name $name `
            -Code 'FINAL_ATTEMPT_GUARDS_MISSING')
        if ($value -ne 0) {
            Fail-W1RealConversation -Code 'FINAL_ATTEMPT_GUARD_NONZERO'
        }
        $result[$name] = $value
    }
    return $result
}

function Test-W1SafeSuccessRoot {
    param([Parameter(Mandatory = $true)][string]$Path)
    try {
        $full = [IO.Path]::GetFullPath($Path)
        $temp = [IO.Path]::GetFullPath($script:TempRoot).
            TrimEnd([char]92, [char]47)
        $parent = [IO.Path]::GetDirectoryName($full)
        $leaf = [IO.Path]::GetFileName($full)
        if (-not [string]::Equals($parent, $temp, $script:PathComparison) -or
            $leaf -cnotmatch '^freeagent-w1-real-conversation-[0-9a-f]{32}$') {
            return $false
        }
        $rootItem = Get-Item -LiteralPath $full -Force
        if (-not ($rootItem -is [IO.DirectoryInfo]) -or
            ($rootItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            return $false
        }
        $stack = New-Object 'Collections.Generic.Stack[string]'
        $items = New-Object 'Collections.Generic.List[IO.FileSystemInfo]'
        $stack.Push($full)
        while ($stack.Count -gt 0) {
            $directory = $stack.Pop()
            foreach ($item in (New-Object IO.DirectoryInfo($directory)).EnumerateFileSystemInfos()) {
                if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                    return $false
                }
                $items.Add($item)
                if ($item -is [IO.DirectoryInfo]) { $stack.Push($item.FullName) }
            }
        }
        foreach ($item in $items) {
            try { $item.Attributes = [IO.FileAttributes]::Normal } catch { return $false }
        }
        $rootItem.Attributes = [IO.FileAttributes]::Normal
        [IO.Directory]::Delete($full, $true)
        return -not [IO.Directory]::Exists($full)
    } catch {
        return $false
    }
}

if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
    [Console]::Error.WriteLine('W1_REAL_CONVERSATION_FAIL code=WINDOWS_REQUIRED')
    exit 1
}

if ($null -eq ('W1ExclusiveDirectory' -as [type])) {
    try {
        Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;

public static class W1ExclusiveDirectory
{
    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CreateDirectory(string path, IntPtr attributes);

    public static int Create(string path)
    {
        if (CreateDirectory(path, IntPtr.Zero)) return 0;
        return Marshal.GetLastWin32Error();
    }
}

public static class W1ProcessJob
{
    private const UInt32 ExtendedLimitInformation = 9;
    private const UInt32 KillOnJobClose = 0x00002000;

    [StructLayout(LayoutKind.Sequential)]
    private struct BasicLimitInformation
    {
        public Int64 PerProcessUserTimeLimit;
        public Int64 PerJobUserTimeLimit;
        public UInt32 LimitFlags;
        public UIntPtr MinimumWorkingSetSize;
        public UIntPtr MaximumWorkingSetSize;
        public UInt32 ActiveProcessLimit;
        public Int64 Affinity;
        public UInt32 PriorityClass;
        public UInt32 SchedulingClass;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct IoCounters
    {
        public UInt64 ReadOperationCount;
        public UInt64 WriteOperationCount;
        public UInt64 OtherOperationCount;
        public UInt64 ReadTransferCount;
        public UInt64 WriteTransferCount;
        public UInt64 OtherTransferCount;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct ExtendedLimitInformationValue
    {
        public BasicLimitInformation BasicLimitInformation;
        public IoCounters IoInfo;
        public UIntPtr ProcessMemoryLimit;
        public UIntPtr JobMemoryLimit;
        public UIntPtr PeakProcessMemoryUsed;
        public UIntPtr PeakJobMemoryUsed;
    }

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr CreateJobObject(IntPtr attributes, string name);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool SetInformationJobObject(
        IntPtr job,
        UInt32 informationClass,
        IntPtr information,
        UInt32 informationLength);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool TerminateJobObject(IntPtr job, UInt32 exitCode);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool CloseHandle(IntPtr handle);

    public static IntPtr Create(Process process)
    {
        IntPtr job = CreateJobObject(IntPtr.Zero, null);
        if (job == IntPtr.Zero)
            throw new Win32Exception(Marshal.GetLastWin32Error());
        IntPtr information = IntPtr.Zero;
        bool assigned = false;
        try
        {
            var value = new ExtendedLimitInformationValue();
            value.BasicLimitInformation.LimitFlags = KillOnJobClose;
            int size = Marshal.SizeOf(value);
            information = Marshal.AllocHGlobal(size);
            Marshal.StructureToPtr(value, information, false);
            if (!SetInformationJobObject(
                job,
                ExtendedLimitInformation,
                information,
                (UInt32)size))
                throw new Win32Exception(Marshal.GetLastWin32Error());
            if (!AssignProcessToJobObject(job, process.Handle))
                throw new Win32Exception(Marshal.GetLastWin32Error());
            assigned = true;
            return job;
        }
        finally
        {
            if (information != IntPtr.Zero) Marshal.FreeHGlobal(information);
            if (!assigned) CloseHandle(job);
        }
    }

    public static void Terminate(IntPtr job)
    {
        if (job != IntPtr.Zero && !TerminateJobObject(job, 124))
            throw new Win32Exception(Marshal.GetLastWin32Error());
    }

    public static void Close(IntPtr job)
    {
        if (job != IntPtr.Zero && !CloseHandle(job))
            throw new Win32Exception(Marshal.GetLastWin32Error());
    }
}
'@ -ErrorAction Stop
    } catch {
        [Console]::Error.WriteLine(
            'W1_REAL_CONVERSATION_FAIL code=PROCESS_RUNTIME_UNAVAILABLE'
        )
        exit 1
    }
}

$startedAt = [DateTimeOffset]::UtcNow
$finishedAt = $startedAt
$status = 'FAILED'
$cleanupStatus = 'NOT_STARTED'
$binarySha256 = $null
$seedSha256 = $null
$uniqueRuns = New-Object 'Collections.Generic.HashSet[string]' (
    [StringComparer]::Ordinal
)

try {
    if ($SecretEnvironmentName -cnotmatch '^[A-Za-z_][A-Za-z0-9_]{0,127}$') {
        Fail-W1RealConversation -Code 'SECRET_ENVIRONMENT_NAME_INVALID'
    }
    $script:FreeAgent = Get-W1FullPath `
        -Value $FreeAgentPath `
        -Code 'FREEAGENT_BINARY_INVALID'
    $seed = Get-W1FullPath -Value $SeedPath -Code 'SEED_INVALID'
    Assert-W1RegularFile `
        -Path $script:FreeAgent `
        -Code 'FREEAGENT_BINARY_INVALID'
    Assert-W1RegularFile -Path $seed -Code 'SEED_INVALID'
    $binarySha256 = Get-W1Sha256 -Path $script:FreeAgent
    $seedSha256 = Get-W1Sha256 -Path $seed

    $seedObject = Read-W1JsonFile -Path $seed -Code 'SEED_INVALID'
    if ([string](Get-W1PropertyValue $seedObject 'schema_version' 'SEED_INVALID') -cne
        'freeagent.bootstrap-seed/v2') {
        Fail-W1RealConversation -Code 'SEED_INVALID'
    }
    $script:TenantId = [string](Get-W1PropertyValue `
        $seedObject 'tenant_id' 'SEED_SCOPE_INVALID')
    $assembly = Get-W1PropertyValue `
        $seedObject 'default_assembly' 'SEED_SCOPE_INVALID'
    $script:WorkspaceId = [string](Get-W1PropertyValue `
        $assembly 'workspace_id' 'SEED_SCOPE_INVALID')
    $script:AgentId = [string](Get-W1PropertyValue `
        $assembly 'agent_id' 'SEED_SCOPE_INVALID')
    $script:ProfileId = [string](Get-W1PropertyValue `
        $assembly 'profile_id' 'SEED_SCOPE_INVALID')
    foreach ($identity in @(
        $script:TenantId, $script:WorkspaceId,
        $script:AgentId, $script:ProfileId
    )) {
        if (-not (Test-W1OpaqueIdentity -Value $identity)) {
            Fail-W1RealConversation -Code 'SEED_SCOPE_INVALID'
        }
    }
    $definitions = Get-W1PropertyValue `
        $seedObject 'definitions' 'SEED_SCOPE_INVALID'
    $profile = Get-W1PropertyValue `
        $definitions 'profile' 'SEED_SCOPE_INVALID'
    $profileBody = Get-W1PropertyValue `
        $profile 'body' 'SEED_SCOPE_INVALID'
    $modelBinding = Get-W1PropertyValue `
        $seedObject 'model_binding' 'SEED_MODEL_INVALID'
    $modelConfig = Get-W1PropertyValue `
        $modelBinding 'config' 'SEED_MODEL_INVALID'
    if ([string](Get-W1PropertyValue $profile 'id' 'SEED_SCOPE_INVALID') -cne
            $script:ProfileId -or
        [string](Get-W1PropertyValue $profileBody 'mode' 'SEED_SCOPE_INVALID') -cne
            'PURE_CHAT' -or
        [string](Get-W1PropertyValue $modelConfig 'provider' 'SEED_MODEL_INVALID') -cne
            'deepseek') {
        Fail-W1RealConversation -Code 'SEED_NOT_DEEPSEEK_PURE_CHAT'
    }

    $script:TempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).
        TrimEnd([char]92, [char]47)
    if (-not (Test-Path -LiteralPath $script:TempRoot -PathType Container)) {
        Fail-W1RealConversation -Code 'TEMP_ROOT_INVALID'
    }
    Assert-W1NoReparseAncestry `
        -Path $script:TempRoot `
        -Code 'TEMP_ROOT_UNSAFE'
    $runNonce = [Guid]::NewGuid().ToString('N')
    $script:Root = Join-Path $script:TempRoot ($script:RootPrefix + $runNonce)
    $createCode = [W1ExclusiveDirectory]::Create($script:Root)
    if ($createCode -ne 0) {
        Fail-W1RealConversation -Code 'TEMP_RUN_CREATE_FAILED'
    }
    $script:ConversationId = 'w1-real-conversation-' + $runNonce
    $database = Join-Path $script:Root 'source.sqlite'
    $artifacts = Join-Path $script:Root 'source-artifacts'

    [void](Invoke-W1JsonCommand `
        -Phase 'init' `
        -Arguments @(
            'init', '--db', $database, '--artifact-root', $artifacts,
            '--seed', $seed
        ) `
        -FailureCode 'INIT_FAILED' `
        -WithoutSecret)

    $created = Invoke-W1JsonCommand `
        -Phase 'conversation-create' `
        -Arguments @(
            'conversation-create', '--db', $database,
            '--tenant', $script:TenantId,
            '--principal', 'local-operator',
            '--workspace', $script:WorkspaceId,
            '--agent', $script:AgentId,
            '--profile', $script:ProfileId,
            '--conversation', $script:ConversationId
        ) `
        -FailureCode 'CONVERSATION_CREATE_FAILED' `
        -WithoutSecret
    if (-not [bool](Get-W1PropertyValue `
            $created 'created' 'CONVERSATION_CREATE_INVALID') -or
        [uint64](Get-W1PropertyValue `
            $created 'revision' 'CONVERSATION_CREATE_INVALID') -ne 0 -or
        [string](Get-W1PropertyValue `
            $created 'conversation_id' 'CONVERSATION_CREATE_INVALID') -cne
            $script:ConversationId) {
        Fail-W1RealConversation -Code 'CONVERSATION_CREATE_INVALID'
    }

    [uint64]$revision = 0
    $head = ''
    $deadlineBase = $startedAt.AddHours(24)
    for ($turn = 1; $turn -le 50; $turn++) {
        $script:Phase = ('turn-{0:D2}' -f $turn)
        [uint64]$expectedRevision = $revision
        $expectedHead = $head
        $message = (
            'W1 real Conversation acceptance turn {0:D2}. ' +
            'Reply with one concise sentence.'
        ) -f $turn
        $requestId = 'w1-real-' + $runNonce + ('-turn-{0:D2}' -f $turn)
        $deadline = $deadlineBase.AddSeconds($turn).UtcDateTime.ToString(
            "yyyy-MM-dd'T'HH:mm:ss.fffffff'Z'",
            [Globalization.CultureInfo]::InvariantCulture
        )
        $chatArguments = New-W1ChatArguments `
            -Database $database `
            -Artifacts $artifacts `
            -ExpectedRevision $expectedRevision `
            -ExpectedHead $expectedHead `
            -Message $message `
            -RequestId $requestId `
            -Deadline $deadline
        $result = Invoke-W1JsonCommand `
            -Phase ('turn-{0:D2}' -f $turn) `
            -Arguments $chatArguments `
            -FailureCode ('TURN_{0:D2}_PROCESS_FAILED' -f $turn)
        Assert-W1ChatSucceeded `
            -Result $result `
            -Turn $turn `
            -RequestId $requestId `
            -Code ('TURN_{0:D2}_INVALID' -f $turn)
        Add-W1OriginalUsage `
            -Result $result `
            -Code ('TURN_{0:D2}_USAGE_INVALID' -f $turn)
        $runId = [string](Get-W1PropertyValue `
            $result 'run_id' ('TURN_{0:D2}_INVALID' -f $turn))
        if (-not $uniqueRuns.Add($runId)) {
            Fail-W1RealConversation -Code 'RUN_ID_REUSED'
        }
        $revision = [uint64]$turn
        $head = $runId
        $script:TurnsSucceeded = $turn
        $script:FinalRevision = $revision
        if ($turn -eq 2) {
            $script:StageTwoVerified = $true
        }

        if ($turn -eq 25) {
            $turn25Bundle = Join-Path $script:Root 'turn-25-bundle'
            $turn25Backup = Invoke-W1BackupVerify `
                -Database $database `
                -Artifacts $artifacts `
                -Bundle $turn25Bundle `
                -Label 'turn-25'
            $script:Turn25BackupVerified = $true
            $restoredDatabase = Join-Path $script:Root 'restored.sqlite'
            $restoredArtifacts = Join-Path $script:Root 'restored-artifacts'
            $restored = Invoke-W1JsonCommand `
                -Phase 'turn-25-restore' `
                -Arguments @(
                    'restore', '--bundle', $turn25Bundle,
                    '--db', $restoredDatabase,
                    '--artifact-root', $restoredArtifacts
                ) `
                -FailureCode 'TURN_25_RESTORE_FAILED' `
                -WithoutSecret
            if (-not [string]::Equals(
                    [IO.Path]::GetFullPath([string](Get-W1PropertyValue `
                        $restored 'database_path' 'TURN_25_RESTORE_INVALID')),
                    [IO.Path]::GetFullPath($restoredDatabase),
                    $script:PathComparison
                ) -or -not [string]::Equals(
                    [IO.Path]::GetFullPath([string](Get-W1PropertyValue `
                        $restored 'artifact_root' 'TURN_25_RESTORE_INVALID')),
                    [IO.Path]::GetFullPath($restoredArtifacts),
                    $script:PathComparison
                )) {
                Fail-W1RealConversation -Code 'TURN_25_RESTORE_INVALID'
            }
            $database = $restoredDatabase
            $artifacts = $restoredArtifacts
            Assert-W1ConversationHead `
                -Database $database `
                -Revision $revision `
                -Head $head `
                -Code 'TURN_25_RESTORED_HEAD_INVALID'
            $script:Turn25RestoreVerified = $true

            $retryArguments = New-W1ChatArguments `
                -Database $database `
                -Artifacts $artifacts `
                -ExpectedRevision $expectedRevision `
                -ExpectedHead $expectedHead `
                -Message $message `
                -RequestId $requestId `
                -Deadline $deadline
            $retry = Invoke-W1JsonCommand `
                -Phase 'turn-25-exact-retry-without-secret' `
                -Arguments $retryArguments `
                -FailureCode 'EXACT_RETRY_25_FAILED' `
                -WithoutSecret
            Assert-W1ExactRetry `
                -Original $result `
                -Retry $retry `
                -Code 'EXACT_RETRY_25_CHANGED_RESULT'
            $script:Retry25Verified = $true
        }

        if ($turn -eq 50) {
            $retry = Invoke-W1JsonCommand `
                -Phase 'turn-50-exact-retry-without-secret' `
                -Arguments $chatArguments `
                -FailureCode 'EXACT_RETRY_50_FAILED' `
                -WithoutSecret
            Assert-W1ExactRetry `
                -Original $result `
                -Retry $retry `
                -Code 'EXACT_RETRY_50_CHANGED_RESULT'
            $script:Retry50Verified = $true
        }
    }

    if ($uniqueRuns.Count -ne 50 -or $revision -ne 50 -or
        -not $script:StageTwoVerified -or
        -not $script:Turn25BackupVerified -or
        -not $script:Turn25RestoreVerified -or
        -not $script:Retry25Verified -or
        -not $script:Retry50Verified) {
        Fail-W1RealConversation -Code 'FIFTY_TURN_CLOSURE_INVALID'
    }
    Assert-W1ConversationHead `
        -Database $database `
        -Revision 50 `
        -Head $head `
        -Code 'FINAL_CONVERSATION_HEAD_INVALID'

    $finalBundle = Join-Path $script:Root 'turn-50-bundle'
    $finalBackup = Invoke-W1BackupVerify `
        -Database $database `
        -Artifacts $artifacts `
        -Bundle $finalBundle `
        -Label 'turn-50'
    $script:FinalAttemptGuards = Get-W1AttemptGuards `
        -Manifest $finalBackup.Manifest
    $script:FinalBackupVerified = $true
    $status = 'COMPLETE'
    $script:FailureCode = ''
} catch {
    if ($script:FailureCode -ceq 'UNCLASSIFIED') {
        $script:FailureCode = 'HARNESS_UNEXPECTED'
    }
    $status = 'FAILED'
}

if ($status -ceq 'COMPLETE') {
    $script:Phase = 'success-cleanup'
    if (Test-W1SafeSuccessRoot -Path $script:Root) {
        $cleanupStatus = 'REMOVED'
    } else {
        $status = 'FAILED'
        $script:FailureCode = 'SUCCESS_CLEANUP_FAILED'
        $cleanupStatus = 'FAILED_RETAINED'
    }
} elseif (-not [string]::IsNullOrWhiteSpace($script:Root) -and
    (Test-Path -LiteralPath $script:Root -PathType Container)) {
    $cleanupStatus = 'RETAINED_FOR_REVIEW'
} else {
    $cleanupStatus = 'NO_EVIDENCE_CREATED'
}

$finishedAt = [DateTimeOffset]::UtcNow
$evidencePath = if ($status -cne 'COMPLETE' -and
    -not [string]::IsNullOrWhiteSpace($script:Root) -and
    (Test-Path -LiteralPath $script:Root -PathType Container)) {
    $script:Root
} else {
    $null
}
$summary = [ordered]@{
    schema_version = 'freeagent.w1-real-conversation-summary/v1'
    status = $status
    failure_code = $script:FailureCode
    failed_phase = $(if ($status -ceq 'COMPLETE') { '' } else { $script:Phase })
    started_at_utc = $startedAt.ToString('O')
    finished_at_utc = $finishedAt.ToString('O')
    elapsed_milliseconds = [int64]($finishedAt - $startedAt).TotalMilliseconds
    turns_requested = 50
    turns_succeeded = $script:TurnsSucceeded
    first_two_turns_verified = [bool]$script:StageTwoVerified
    unique_runs = $uniqueRuns.Count
    final_revision = $script:FinalRevision
    model_dispatches_permitted = $script:TurnsSucceeded
    automatic_retry = $false
    exact_retry_without_runtime_value = [ordered]@{
        turn_25 = [bool]$script:Retry25Verified
        turn_50 = [bool]$script:Retry50Verified
    }
    turn_25_backup_verified = [bool]$script:Turn25BackupVerified
    turn_25_restore_verified = [bool]$script:Turn25RestoreVerified
    final_backup_verified = [bool]$script:FinalBackupVerified
    final_attempt_guards = $script:FinalAttemptGuards
    usage = New-W1UsageSummary
    source_locks = [ordered]@{
        freeagent_sha256 = $binarySha256
        seed_sha256 = $seedSha256
    }
    runtime_value_read_by_harness = $false
    runtime_value_persisted_by_harness = $false
    cleanup_status = $cleanupStatus
    evidence_retained = ($null -ne $evidencePath)
    evidence_path = $evidencePath
}
try {
    $json = $summary | ConvertTo-Json -Depth 12 -Compress
    [Console]::Out.WriteLine($json)
} catch {
    [Console]::Error.WriteLine(
        'W1_REAL_CONVERSATION_FAIL code=SUMMARY_SERIALIZATION_FAILED'
    )
    exit 1
}

if ($status -cne 'COMPLETE') {
    [Console]::Error.WriteLine(
        'W1_REAL_CONVERSATION_REVIEW_REQUIRED code=' + $script:FailureCode
    )
    exit 2
}

exit 0
