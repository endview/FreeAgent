[CmdletBinding(DefaultParameterSetName = 'Initialize')]
param(
    [Parameter(Mandatory = $true, ParameterSetName = 'Initialize')]
    [switch]$Initialize,

    [Parameter(Mandatory = $true, ParameterSetName = 'Run')]
    [switch]$Run,

    [Parameter(Mandatory = $true)]
    [ValidateSet(
        'permanent',
        'linux-quality',
        'windows',
        'linux-race',
        'cross-build'
    )]
    [string]$JobKind,

    [Parameter(Mandatory = $true)][string]$StageRoot,
    [Parameter(Mandatory = $true)][string]$ArtifactRoot,
    [Parameter(Mandatory = $true)][string]$ExecutionTempRoot,

    [Parameter(Mandatory = $true, ParameterSetName = 'Run')]
    [string]$CommandRunnerPath,

    [Parameter(Mandatory = $true, ParameterSetName = 'Run')]
    [string]$ExpectedCommandRunnerSha256,

    [Parameter(ParameterSetName = 'Run')]
    [string]$TargetGoos = '',

    [Parameter(ParameterSetName = 'Run')]
    [string]$TargetGoarch = ''
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
$script:CommandSequence = 0
$script:ExactJobKinds = @(
    'permanent',
    'linux-quality',
    'windows',
    'linux-race',
    'cross-build'
)
$script:MaximumAuthenticatedScriptBytes = 4194304
$script:PublicTreeGateSha256 =
    'bfb1dc5470172642e472cad57cd560d4344922da3f3ace15b7a0d90e615e6d2a'
$script:LicenseGateSha256 =
    '6a4ac7c72ce2db82f17b85ae938022c63931a99bac7cd713dfa3a5236a244e3f'
$script:DocsGateSha256 =
    '137db9d8155b7818ff784576e674961af1853582981e7eb519093961c7e8cf26'
$script:BrandingGateSha256 =
    '244db2cf888f8beb6eadbc0dd850d4c6e9ebf6e084e47cc637f10068a118bb00'
$script:ControlWebGateSha256 =
    'd21a1ab5b861d3a077a9de309b9afda750dd81e22dbfbe3445d5bbefffe089a3'

function Fail-CIWorkflowJob {
    param([Parameter(Mandatory = $true)][string]$Code)
    throw "CI_WORKFLOW_JOB_FAIL code=$Code"
}

function Get-CIWAbsolutePath {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if ([string]::IsNullOrWhiteSpace($Value)) {
        Fail-CIWorkflowJob -Code $Code
    }
    foreach ($character in $Value.ToCharArray()) {
        if ([char]::IsControl($character)) {
            Fail-CIWorkflowJob -Code $Code
        }
    }
    $isAbsolute = $false
    if ($script:IsWindowsPlatform) {
        $deviceProbe = $Value.Replace([char]47, [char]92)
        $isDevicePath =
            $deviceProbe.StartsWith('\\?\', [StringComparison]::Ordinal) -or
            $deviceProbe.StartsWith('\\.\', [StringComparison]::Ordinal) -or
            $deviceProbe.StartsWith('\??\', [StringComparison]::Ordinal)
        $isDriveAbsolute = $Value -match '^[A-Za-z]:[\\/]'
        $isAbsolute = -not $isDevicePath -and $isDriveAbsolute
    } else {
        $isAbsolute = $Value.StartsWith('/', [StringComparison]::Ordinal)
    }
    if (-not $isAbsolute) {
        Fail-CIWorkflowJob -Code $Code
    }
    try {
        $full = [IO.Path]::GetFullPath($Value)
        if ($script:IsWindowsPlatform -and (
            $full.StartsWith('\\?\', [StringComparison]::Ordinal) -or
            $full.StartsWith('\\.\', [StringComparison]::Ordinal) -or
            $full.StartsWith('\??\', [StringComparison]::Ordinal)
        )) {
            Fail-CIWorkflowJob -Code $Code
        }
        $root = [IO.Path]::GetPathRoot($full)
        while ($full.Length -gt $root.Length -and
            ($full.EndsWith(
                [IO.Path]::DirectorySeparatorChar.ToString(),
                [StringComparison]::Ordinal
            ) -or
            $full.EndsWith(
                [IO.Path]::AltDirectorySeparatorChar.ToString(),
                [StringComparison]::Ordinal
            ))) {
            $full = $full.Substring(0, $full.Length - 1)
        }
        return $full
    } catch {
        if ($_.Exception.Message.StartsWith(
            'CI_WORKFLOW_JOB_FAIL code=',
            [StringComparison]::Ordinal
        )) {
            throw
        }
        Fail-CIWorkflowJob -Code $Code
    }
}

function Assert-CIWNotFileSystemRoot {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $root = [IO.Path]::GetPathRoot($Path)
    if ([string]::IsNullOrWhiteSpace($root)) {
        Fail-CIWorkflowJob -Code $Code
    }
    $trimmedPath = $Path.TrimEnd([char[]]@([char]92, [char]47))
    $trimmedRoot = $root.TrimEnd([char[]]@([char]92, [char]47))
    if ([string]::Equals(
        $trimmedPath,
        $trimmedRoot,
        $script:PathComparison
    )) {
        Fail-CIWorkflowJob -Code $Code
    }
}

function Assert-CIWNoReparseAncestry {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $probe = $Path
    while (-not [string]::IsNullOrWhiteSpace($probe)) {
        if (Test-Path -LiteralPath $probe) {
            try {
                $item = Get-Item -LiteralPath $probe -Force
            } catch {
                Fail-CIWorkflowJob -Code $Code
            }
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                Fail-CIWorkflowJob -Code $Code
            }
        }
        $parent = [IO.Path]::GetDirectoryName($probe)
        if ([string]::IsNullOrWhiteSpace($parent) -or
            [string]::Equals($parent, $probe, $script:PathComparison)) {
            break
        }
        $probe = $parent
    }
}

function Test-CIWPathContains {
    param(
        [Parameter(Mandatory = $true)][string]$Parent,
        [Parameter(Mandatory = $true)][string]$Candidate,
        [switch]$AllowEqual
    )
    if ([string]::Equals($Parent, $Candidate, $script:PathComparison)) {
        return [bool]$AllowEqual
    }
    $prefix = $Parent
    if (-not $prefix.EndsWith(
        [IO.Path]::DirectorySeparatorChar.ToString(),
        [StringComparison]::Ordinal
    )) {
        $prefix += [IO.Path]::DirectorySeparatorChar
    }
    return $Candidate.StartsWith($prefix, $script:PathComparison)
}

function Assert-CIWDisjointPaths {
    param(
        [Parameter(Mandatory = $true)][string[]]$Paths,
        [Parameter(Mandatory = $true)][string]$Code
    )
    for ($left = 0; $left -lt $Paths.Count; $left++) {
        for ($right = $left + 1; $right -lt $Paths.Count; $right++) {
            if ((Test-CIWPathContains `
                -Parent $Paths[$left] `
                -Candidate $Paths[$right] `
                -AllowEqual) -or
                (Test-CIWPathContains `
                    -Parent $Paths[$right] `
                    -Candidate $Paths[$left] `
                    -AllowEqual)) {
                Fail-CIWorkflowJob -Code $Code
            }
        }
    }
}

function Read-CIWBoundedFile {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][int]$MaximumBytes,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if ($MaximumBytes -lt 0) {
        Fail-CIWorkflowJob -Code $Code
    }
    Assert-CIWNoReparseAncestry -Path $Path -Code $Code
    $stream = $null
    try {
        $item = Get-Item -LiteralPath $Path -Force
        if (-not ($item -is [IO.FileInfo]) -or
            ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            Fail-CIWorkflowJob -Code $Code
        }
        $stream = New-Object IO.FileStream(
            $Path,
            [IO.FileMode]::Open,
            [IO.FileAccess]::Read,
            [IO.FileShare]::Read
        )
        if ($stream.Length -gt $MaximumBytes) {
            Fail-CIWorkflowJob -Code $Code
        }
        [byte[]]$bytes = New-Object byte[] ([int]$stream.Length)
        $offset = 0
        while ($offset -lt $bytes.Length) {
            $read = $stream.Read($bytes, $offset, $bytes.Length - $offset)
            if ($read -le 0) {
                Fail-CIWorkflowJob -Code $Code
            }
            $offset += $read
        }
        if ($stream.ReadByte() -ne -1) {
            Fail-CIWorkflowJob -Code $Code
        }
        return ,$bytes
    } catch {
        if ($_.Exception.Message.StartsWith(
            'CI_WORKFLOW_JOB_FAIL code=',
            [StringComparison]::Ordinal
        )) {
            throw
        }
        Fail-CIWorkflowJob -Code $Code
    } finally {
        if ($null -ne $stream) {
            $stream.Dispose()
        }
    }
}

function Write-CIWText {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text
    )
    $parent = [IO.Path]::GetDirectoryName($Path)
    if (-not [string]::IsNullOrWhiteSpace($parent)) {
        [void][IO.Directory]::CreateDirectory($parent)
    }
    [IO.File]::WriteAllText($Path, $Text, $script:Utf8NoBom)
}

function Write-CIWJson {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)]$Value
    )
    Write-CIWText `
        -Path $Path `
        -Text (($Value | ConvertTo-Json -Depth 8) + "`n")
}

function Get-CIWEvidenceRelativePath {
    param([Parameter(Mandatory = $true)][string]$Kind)
    switch ($Kind) {
        'linux-quality' { return 'evidence/linux-ordinary' }
        'windows' { return 'evidence/windows-ordinary' }
        'linux-race' { return 'evidence/linux-race' }
        default { return '' }
    }
}

function Get-CIWCommandContract {
    param([Parameter(Mandatory = $true)][string]$Kind)
    switch ($Kind) {
        'linux-quality' {
            return [pscustomobject]@{
                Command = 'go test -json -shuffle=on -count=1 -timeout=30m ./...'
                Argv = @(
                    'go', 'test', '-json', '-shuffle=on',
                    '-count=1', '-timeout=30m', './...'
                )
                Packages = @('./...')
            }
        }
        'windows' {
            return [pscustomobject]@{
                Command = 'go test -json -shuffle=on -count=1 -timeout=30m ./...'
                Argv = @(
                    'go', 'test', '-json', '-shuffle=on',
                    '-count=1', '-timeout=30m', './...'
                )
                Packages = @('./...')
            }
        }
        'linux-race' {
            return [pscustomobject]@{
                Command = 'go test -json -race -count=1 -timeout=30m ./...'
                Argv = @(
                    'go', 'test', '-json', '-race',
                    '-count=1', '-timeout=30m', './...'
                )
                Packages = @('./...')
            }
        }
        default {
            return $null
        }
    }
}

if ($script:ExactJobKinds -cnotcontains $JobKind) {
    Fail-CIWorkflowJob -Code 'CIW_JOB_KIND_INVALID'
}

function Initialize-CIWEvidence {
    param(
        [Parameter(Mandatory = $true)][string]$Kind,
        [Parameter(Mandatory = $true)][string]$Artifact
    )
    $relative = Get-CIWEvidenceRelativePath -Kind $Kind
    if ([string]::IsNullOrWhiteSpace($relative)) { return }
    $directory = Join-Path $Artifact $relative
    [void][IO.Directory]::CreateDirectory($directory)
    $contract = Get-CIWCommandContract -Kind $Kind
    $evidence = [ordered]@{
        schema_version = 2
        name = [IO.Path]::GetFileName($relative)
        required = $true
        status = 'UNPROVED'
        proof_complete = $false
        command_outcome = 'NOT_RUN'
        exit_code = $null
        command = $contract.Command
        command_argv = $contract.Argv
        packages = $contract.Packages
        started_at_utc = $null
        finished_at_utc = $null
        elapsed_ms = 0
        go_version = $null
        runner = [ordered]@{
            name = $env:RUNNER_NAME
            os = $env:RUNNER_OS
            arch = $env:RUNNER_ARCH
        }
        github = [ordered]@{
            repository = $env:GITHUB_REPOSITORY
            workflow = $env:GITHUB_WORKFLOW
            sha = $env:GITHUB_SHA
            ref = $env:GITHUB_REF
            run_id = $env:GITHUB_RUN_ID
            run_attempt = $env:GITHUB_RUN_ATTEMPT
        }
        go_env = $null
        compiler_path = $null
        compiler_identity = $null
        compiler_version = $null
        packages_file = if ($Kind -ceq 'linux-race') {
            'packages.txt'
        } else {
            $null
        }
        go_test_stdout = 'go-test.stdout.jsonl'
        go_test_stderr = 'go-test.stderr.log'
        reason = 'workflow test command has not run'
    }
    Write-CIWJson -Path (Join-Path $directory 'evidence.json') -Value $evidence
    Write-CIWText -Path (Join-Path $directory 'exit-code.txt') -Text "NOT_RUN`n"
    [IO.File]::WriteAllBytes(
        (Join-Path $directory 'go-test.stdout.jsonl'),
        (New-Object byte[] 0)
    )
    [IO.File]::WriteAllBytes(
        (Join-Path $directory 'go-test.stderr.log'),
        (New-Object byte[] 0)
    )
    if ($Kind -ceq 'linux-race') {
        Write-CIWText `
            -Path (Join-Path $directory 'packages.txt') `
            -Text "./...`n"
    }
}

$stage = Get-CIWAbsolutePath -Value $StageRoot -Code 'CIW_STAGE_INVALID'
$artifact = Get-CIWAbsolutePath -Value $ArtifactRoot -Code 'CIW_ARTIFACT_INVALID'
$executionTemp = Get-CIWAbsolutePath `
    -Value $ExecutionTempRoot `
    -Code 'CIW_EXECUTION_TEMP_INVALID'
foreach ($directory in @($stage, $artifact, $executionTemp)) {
    Assert-CIWNotFileSystemRoot -Path $directory -Code 'CIW_LAYOUT_INVALID'
    Assert-CIWNoReparseAncestry -Path $directory -Code 'CIW_LAYOUT_INVALID'
    if (-not (Test-Path -LiteralPath $directory -PathType Container)) {
        Fail-CIWorkflowJob -Code 'CIW_LAYOUT_MISSING'
    }
}
Assert-CIWDisjointPaths `
    -Paths @($stage, $artifact, $executionTemp) `
    -Code 'CIW_LAYOUT_INVALID'

if ($PSCmdlet.ParameterSetName -ceq 'Initialize') {
    Initialize-CIWEvidence -Kind $JobKind -Artifact $artifact
    Write-Host "CI_WORKFLOW_JOB_INITIALIZE_PASS kind=$JobKind"
    return
}

function Get-CIWSha256 {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        return ([BitConverter]::ToString($sha.ComputeHash($Bytes))).
            Replace('-', '').ToLowerInvariant()
    } finally {
        $sha.Dispose()
    }
}

function Assert-CIWGateScript {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$ExpectedSha256,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if ($ExpectedSha256 -cnotmatch '^[0-9a-f]{64}$') {
        Fail-CIWorkflowJob -Code $Code
    }
    $full = Get-CIWAbsolutePath -Value $Path -Code $Code
    [byte[]]$bytes = Read-CIWBoundedFile `
        -Path $full `
        -MaximumBytes $script:MaximumAuthenticatedScriptBytes `
        -Code $Code
    if ((Get-CIWSha256 -Bytes $bytes) -cne $ExpectedSha256) {
        Fail-CIWorkflowJob -Code $Code
    }
    try {
        $source = $script:Utf8Strict.GetString($bytes)
        $tokens = $null
        $errors = $null
        [void][Management.Automation.Language.Parser]::ParseInput(
            $source,
            [ref]$tokens,
            [ref]$errors
        )
        if (@($errors).Count -ne 0) {
            Fail-CIWorkflowJob -Code $Code
        }
    } catch {
        if ($_.Exception.Message.StartsWith(
            'CI_WORKFLOW_JOB_FAIL code=',
            [StringComparison]::Ordinal
        )) {
            throw
        }
        Fail-CIWorkflowJob -Code $Code
    }
    return $full
}

if ($ExpectedCommandRunnerSha256 -cnotmatch '^[0-9a-f]{64}$') {
    Fail-CIWorkflowJob -Code 'CIW_COMMAND_RUNNER_PIN_INVALID'
}
$runnerPath = Get-CIWAbsolutePath `
    -Value $CommandRunnerPath `
    -Code 'CIW_COMMAND_RUNNER_INVALID'
[byte[]]$runnerBytes = Read-CIWBoundedFile `
    -Path $runnerPath `
    -MaximumBytes $script:MaximumAuthenticatedScriptBytes `
    -Code 'CIW_COMMAND_RUNNER_INVALID'
if ((Get-CIWSha256 -Bytes $runnerBytes) -cne $ExpectedCommandRunnerSha256) {
    Fail-CIWorkflowJob -Code 'CIW_COMMAND_RUNNER_AUTH_FAILED'
}
try {
    $runnerSource = $script:Utf8Strict.GetString($runnerBytes)
    $runnerTokens = $null
    $runnerErrors = $null
    [void][Management.Automation.Language.Parser]::ParseInput(
        $runnerSource,
        [ref]$runnerTokens,
        [ref]$runnerErrors
    )
    if ($runnerErrors.Count -ne 0) {
        Fail-CIWorkflowJob -Code 'CIW_COMMAND_RUNNER_AUTH_FAILED'
    }
    $runner = [ScriptBlock]::Create($runnerSource)
} catch {
    if ($_.Exception.Message.StartsWith(
        'CI_WORKFLOW_JOB_FAIL code=',
        [StringComparison]::Ordinal
    )) {
        throw
    }
    Fail-CIWorkflowJob -Code 'CIW_COMMAND_RUNNER_AUTH_FAILED'
}

function Get-CIWApplication {
    param([Parameter(Mandatory = $true)][string]$Name)
    try {
        $command = (@(
            Get-Command $Name -CommandType Application -ErrorAction Stop
        ))[0]
    } catch {
        Fail-CIWorkflowJob -Code "CIW_TOOL_MISSING_$($Name.ToUpperInvariant())"
    }
    if (-not [IO.Path]::IsPathRooted($command.Source)) {
        Fail-CIWorkflowJob -Code 'CIW_TOOL_PATH_INVALID'
    }
    return [IO.Path]::GetFullPath($command.Source)
}

function Resolve-CIWTrustedLinuxSystemExecutable {
    param([Parameter(Mandatory = $true)][string]$Path)
    if ($script:IsWindowsPlatform) {
        Fail-CIWorkflowJob -Code 'CIW_SYSTEM_TOOL_LINK_INVALID'
    }
    $current = Get-CIWAbsolutePath `
        -Value $Path `
        -Code 'CIW_SYSTEM_TOOL_LINK_INVALID'
    [string[]]$linkRoots = @(
        '/usr/bin',
        '/bin',
        '/etc/alternatives'
    )
    [string[]]$finalRoots = @('/usr/bin', '/bin')
    for ($hop = 0; $hop -lt 16; $hop++) {
        $linkRootAllowed = $false
        foreach ($root in $linkRoots) {
            if (Test-CIWPathContains `
                -Parent $root `
                -Candidate $current `
                -AllowEqual) {
                $linkRootAllowed = $true
                break
            }
        }
        if (-not $linkRootAllowed) {
            Fail-CIWorkflowJob -Code 'CIW_SYSTEM_TOOL_LINK_INVALID'
        }
        try {
            $item = Get-Item -LiteralPath $current -Force
        } catch {
            Fail-CIWorkflowJob -Code 'CIW_SYSTEM_TOOL_LINK_INVALID'
        }
        if (-not ($item -is [IO.FileInfo])) {
            Fail-CIWorkflowJob -Code 'CIW_SYSTEM_TOOL_LINK_INVALID'
        }
        if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -eq 0) {
            $finalRootAllowed = $false
            foreach ($root in $finalRoots) {
                if (Test-CIWPathContains `
                    -Parent $root `
                    -Candidate $current `
                    -AllowEqual) {
                    $finalRootAllowed = $true
                    break
                }
            }
            if (-not $finalRootAllowed) {
                Fail-CIWorkflowJob -Code 'CIW_SYSTEM_TOOL_LINK_INVALID'
            }
            Assert-CIWNoReparseAncestry `
                -Path $current `
                -Code 'CIW_SYSTEM_TOOL_LINK_INVALID'
            return $current
        }
        $targetProperty = $item.PSObject.Properties['Target']
        if ($null -eq $targetProperty) {
            Fail-CIWorkflowJob -Code 'CIW_SYSTEM_TOOL_LINK_INVALID'
        }
        [string[]]$targets = @($targetProperty.Value)
        if ($targets.Count -ne 1 -or
            [string]::IsNullOrWhiteSpace($targets[0])) {
            Fail-CIWorkflowJob -Code 'CIW_SYSTEM_TOOL_LINK_INVALID'
        }
        $next = if ([IO.Path]::IsPathRooted($targets[0])) {
            $targets[0]
        } else {
            Join-Path ([IO.Path]::GetDirectoryName($current)) $targets[0]
        }
        $current = Get-CIWAbsolutePath `
            -Value $next `
            -Code 'CIW_SYSTEM_TOOL_LINK_INVALID'
    }
    Fail-CIWorkflowJob -Code 'CIW_SYSTEM_TOOL_LINK_INVALID'
}

function Write-CIWFailureSummary {
    param(
        [Parameter(Mandatory = $true)][string]$Label,
        [Parameter(Mandatory = $true)]$Result,
        [Parameter(Mandatory = $true)][string]$Stdout,
        [Parameter(Mandatory = $true)][string]$Stderr
    )
    $stdoutBytes = [IO.File]::ReadAllBytes($Stdout)
    $stderrBytes = [IO.File]::ReadAllBytes($Stderr)
    Write-Host (
        "CI_COMMAND_FAILURE label=$Label exit=$($Result.ExitCode) " +
        "stdout_bytes=$($Result.StandardOutputBytes) " +
        "stderr_bytes=$($Result.StandardErrorBytes)"
    )
    if ($stdoutBytes.Length -gt 0) {
        $stdoutCount = [Math]::Min(4096, $stdoutBytes.Length)
        [byte[]]$stdoutPrefix = New-Object byte[] $stdoutCount
        [Array]::Copy($stdoutBytes, 0, $stdoutPrefix, 0, $stdoutCount)
        Write-Host (
            'CI_COMMAND_STDOUT_BASE64 ' +
            [Convert]::ToBase64String($stdoutPrefix)
        )
    }
    if ($stderrBytes.Length -gt 0) {
        $stderrCount = [Math]::Min(4096, $stderrBytes.Length)
        [byte[]]$stderrPrefix = New-Object byte[] $stderrCount
        [Array]::Copy($stderrBytes, 0, $stderrPrefix, 0, $stderrCount)
        Write-Host (
            'CI_COMMAND_STDERR_BASE64 ' +
            [Convert]::ToBase64String($stderrPrefix)
        )
    }
}

function Invoke-CIWCommand {
    param(
        [Parameter(Mandatory = $true)][string]$Label,
        [Parameter(Mandatory = $true)][string]$Executable,
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [string[]]$Arguments,
        [int]$TimeoutSeconds = 900,
        [hashtable]$Environment = @{},
        [string]$StdoutPath = '',
        [string]$StderrPath = '',
        [string]$OutputRoot = ''
    )
    $script:CommandSequence++
    $safeLabel = $Label -replace '[^a-zA-Z0-9_.-]', '-'
    if ([string]::IsNullOrWhiteSpace($OutputRoot)) {
        $OutputRoot = $executionTemp
    }
    if ([string]::IsNullOrWhiteSpace($StdoutPath)) {
        $StdoutPath = Join-Path $executionTemp (
            'logs/{0:D3}-{1}.stdout.log' -f $script:CommandSequence, $safeLabel
        )
    }
    if ([string]::IsNullOrWhiteSpace($StderrPath)) {
        $StderrPath = Join-Path $executionTemp (
            'logs/{0:D3}-{1}.stderr.log' -f $script:CommandSequence, $safeLabel
        )
    }
    $parameters = [ordered]@{
        ExecutablePath = $Executable
        ArgumentList = $Arguments
        WorkingDirectory = $stage
        OutputRoot = $OutputRoot
        StandardOutputPath = $StdoutPath
        StandardErrorPath = $StderrPath
        PrivateTempRoot = $executionTemp
        TimeoutSeconds = $TimeoutSeconds
        MaximumStandardOutputBytes = 67108864
        MaximumStandardErrorBytes = 8388608
        EnvironmentOverrides = $Environment
    }
    $objects = @(& $runner @parameters)
    $results = @($objects | Where-Object {
        $null -ne $_.PSObject.Properties['ExitCode']
    })
    if ($results.Count -ne 1) {
        Fail-CIWorkflowJob -Code 'CIW_COMMAND_RESULT_INVALID'
    }
    return [pscustomobject]@{
        Result = $results[0]
        StdoutPath = $StdoutPath
        StderrPath = $StderrPath
    }
}

function Assert-CIWCommandPassed {
    param(
        [Parameter(Mandatory = $true)][string]$Label,
        [Parameter(Mandatory = $true)]$Capture
    )
    if ([int]$Capture.Result.ExitCode -ne 0) {
        Write-CIWFailureSummary `
            -Label $Label `
            -Result $Capture.Result `
            -Stdout $Capture.StdoutPath `
            -Stderr $Capture.StderrPath
        Fail-CIWorkflowJob -Code "CIW_COMMAND_FAILED_$Label"
    }
}

function Read-CIWOutputText {
    param([Parameter(Mandatory = $true)][string]$Path)
    try {
        return $script:Utf8Strict.GetString([IO.File]::ReadAllBytes($Path)).Trim()
    } catch {
        Fail-CIWorkflowJob -Code 'CIW_COMMAND_OUTPUT_INVALID'
    }
}

function Get-CIWGoProof {
    param(
        [Parameter(Mandatory = $true)][string]$Go,
        [hashtable]$Environment = @{}
    )
    $versionCapture = Invoke-CIWCommand `
        -Label 'go-version' `
        -Executable $Go `
        -Arguments @('version') `
        -TimeoutSeconds 120 `
        -Environment $Environment
    Assert-CIWCommandPassed -Label 'GO_VERSION' -Capture $versionCapture
    $envCapture = Invoke-CIWCommand `
        -Label 'go-env' `
        -Executable $Go `
        -Arguments @('env', 'GOOS', 'GOARCH', 'CGO_ENABLED', 'CC') `
        -TimeoutSeconds 120 `
        -Environment $Environment
    Assert-CIWCommandPassed -Label 'GO_ENV' -Capture $envCapture
    $lines = @((
        Read-CIWOutputText -Path $envCapture.StdoutPath
    ) -split '\r?\n')
    if ($lines.Count -ne 4) {
        Fail-CIWorkflowJob -Code 'CIW_GO_ENV_INVALID'
    }
    return [pscustomobject]@{
        Version = Read-CIWOutputText -Path $versionCapture.StdoutPath
        GOOS = $lines[0].Trim()
        GOARCH = $lines[1].Trim()
        CGOEnabled = $lines[2].Trim()
        CC = $lines[3].Trim()
    }
}

$go = Get-CIWApplication -Name 'go'
$moduleDownload = Invoke-CIWCommand `
    -Label 'go-mod-download' `
    -Executable $go `
    -Arguments @('mod', 'download', 'all') `
    -TimeoutSeconds 900
Assert-CIWCommandPassed -Label 'GO_MOD_DOWNLOAD' -Capture $moduleDownload
$moduleVerify = Invoke-CIWCommand `
    -Label 'go-mod-verify' `
    -Executable $go `
    -Arguments @('mod', 'verify') `
    -TimeoutSeconds 300
Assert-CIWCommandPassed -Label 'GO_MOD_VERIFY' -Capture $moduleVerify

if ($JobKind -ceq 'permanent') {
    $powerShell = (Get-Process -Id $PID).Path
    $gofmt = Get-CIWApplication -Name 'gofmt'
    $publicTreeGate = Assert-CIWGateScript `
        -Path (Join-Path $stage 'scripts/Test-PublicTree.ps1') `
        -ExpectedSha256 $script:PublicTreeGateSha256 `
        -Code 'CIW_PUBLIC_TREE_GATE_AUTH_FAILED'
    $licenseGate = Assert-CIWGateScript `
        -Path (Join-Path $stage 'scripts/Test-License.ps1') `
        -ExpectedSha256 $script:LicenseGateSha256 `
        -Code 'CIW_LICENSE_GATE_AUTH_FAILED'
    $docsGate = Assert-CIWGateScript `
        -Path (Join-Path $stage 'scripts/Test-Docs.ps1') `
        -ExpectedSha256 $script:DocsGateSha256 `
        -Code 'CIW_DOCS_GATE_AUTH_FAILED'
    $controlWebGate = Assert-CIWGateScript `
        -Path (Join-Path $stage 'scripts/Test-ControlWeb.ps1') `
        -ExpectedSha256 $script:ControlWebGateSha256 `
        -Code 'CIW_CONTROLWEB_GATE_AUTH_FAILED'
    $node = Get-CIWApplication -Name 'node'
    if ([string]::IsNullOrWhiteSpace($env:FREEAGENT_NPM_CLI)) {
        Fail-CIWorkflowJob -Code 'CIW_NPM_CLI_MISSING'
    }
    $npmCLI = Get-CIWAbsolutePath `
        -Value $env:FREEAGENT_NPM_CLI `
        -Code 'CIW_NPM_CLI_INVALID'
    [void](Read-CIWBoundedFile `
        -Path $npmCLI `
        -MaximumBytes 1048576 `
        -Code 'CIW_NPM_CLI_INVALID')
    if ([IO.Path]::GetExtension($npmCLI) -cne '.js') {
        Fail-CIWorkflowJob -Code 'CIW_NPM_CLI_INVALID'
    }
    $controlWebTemp = Join-Path $executionTemp 'controlweb'
    try {
        [void][IO.Directory]::CreateDirectory($controlWebTemp)
    } catch {
        Fail-CIWorkflowJob -Code 'CIW_CONTROLWEB_TEMP_INVALID'
    }
    Assert-CIWNoReparseAncestry `
        -Path $controlWebTemp `
        -Code 'CIW_CONTROLWEB_TEMP_INVALID'
    foreach ($gate in @(
        @{
            Label = 'PUBLIC_TREE'
            Arguments = @(
                '-NoLogo', '-NoProfile', '-NonInteractive',
                '-ExecutionPolicy', 'Bypass',
                '-File', $publicTreeGate,
                '-Root', $stage
            )
        },
        @{
            Label = 'LICENSE'
            Arguments = @(
                '-NoLogo', '-NoProfile', '-NonInteractive',
                '-ExecutionPolicy', 'Bypass',
                '-File', $licenseGate,
                '-Root', $stage,
                '-GoCommand', $go
            )
        },
        @{
            Label = 'DOCS'
            Arguments = @(
                '-NoLogo', '-NoProfile', '-NonInteractive',
                '-ExecutionPolicy', 'Bypass',
                '-File', $docsGate,
                '-Root', $stage,
                '-GofmtPath', $gofmt
            )
        },
        @{
            Label = 'CONTROLWEB'
            Arguments = @(
                '-NoLogo', '-NoProfile', '-NonInteractive',
                '-ExecutionPolicy', 'Bypass',
                '-File', $controlWebGate,
                '-Root', $stage,
                '-TempRoot', $controlWebTemp,
                '-NodeCommand', $node,
                '-NpmCommand', $npmCLI
            )
        }
    )) {
        $capture = Invoke-CIWCommand `
            -Label ([string]$gate.Label) `
            -Executable $powerShell `
            -Arguments ([string[]]$gate.Arguments) `
            -TimeoutSeconds 900
        Assert-CIWCommandPassed -Label ([string]$gate.Label) -Capture $capture
    }
    Write-Host 'CI_WORKFLOW_JOB_RUN_PASS kind=permanent'
    return
}

if ($JobKind -ceq 'cross-build') {
    if ($TargetGoos -cnotmatch '^(?:windows|linux|darwin)$' -or
        $TargetGoarch -cnotmatch '^(?:amd64|arm64)$') {
        Fail-CIWorkflowJob -Code 'CIW_CROSS_TARGET_INVALID'
    }
    $extension = if ($TargetGoos -ceq 'windows') { '.exe' } else { '' }
    $binDirectory = Join-Path $artifact 'bin'
    [void][IO.Directory]::CreateDirectory($binDirectory)
    $output = Join-Path $binDirectory (
        "freeagent-$TargetGoos-$TargetGoarch$extension"
    )
    $capture = Invoke-CIWCommand `
        -Label "build-$TargetGoos-$TargetGoarch" `
        -Executable $go `
        -Arguments @(
            'build', '-trimpath', '-o', $output, './cmd/freeagent'
        ) `
        -TimeoutSeconds 900 `
        -Environment @{
            CGO_ENABLED = '0'
            GOOS = $TargetGoos
            GOARCH = $TargetGoarch
        }
    Assert-CIWCommandPassed -Label 'CROSS_BUILD' -Capture $capture
    Write-Host (
        "CI_WORKFLOW_JOB_RUN_PASS kind=cross-build target=$TargetGoos/$TargetGoarch"
    )
    return
}

if ($JobKind -ceq 'linux-quality') {
    $gofmt = Get-CIWApplication -Name 'gofmt'
    [string[]]$goFiles = @(Get-ChildItem -LiteralPath $stage -Recurse -File -Filter '*.go' |
        ForEach-Object { $_.FullName })
    [Array]::Sort($goFiles, [StringComparer]::Ordinal)
    if ($goFiles.Count -eq 0) {
        Fail-CIWorkflowJob -Code 'CIW_GO_FILES_MISSING'
    }
    $formatCapture = Invoke-CIWCommand `
        -Label 'gofmt' `
        -Executable $gofmt `
        -Arguments (@('-l') + $goFiles) `
        -TimeoutSeconds 300
    Assert-CIWCommandPassed -Label 'GOFMT' -Capture $formatCapture
    if ((Get-Item -LiteralPath $formatCapture.StdoutPath).Length -ne 0) {
        Write-CIWFailureSummary `
            -Label 'GOFMT_DIFFERENCE' `
            -Result $formatCapture.Result `
            -Stdout $formatCapture.StdoutPath `
            -Stderr $formatCapture.StderrPath
        Fail-CIWorkflowJob -Code 'CIW_GOFORMAT_DIFFERENCE'
    }
    $powerShell = (Get-Process -Id $PID).Path
    $brandingGate = Assert-CIWGateScript `
        -Path (Join-Path $stage 'scripts/Test-Branding.ps1') `
        -ExpectedSha256 $script:BrandingGateSha256 `
        -Code 'CIW_BRANDING_GATE_AUTH_FAILED'
    $brandCapture = Invoke-CIWCommand `
        -Label 'branding' `
        -Executable $powerShell `
        -Arguments @(
            '-NoLogo', '-NoProfile', '-NonInteractive',
            '-ExecutionPolicy', 'Bypass',
            '-File', $brandingGate,
            '-RepositoryRoot', $stage
        ) `
        -TimeoutSeconds 300
    Assert-CIWCommandPassed -Label 'BRANDING' -Capture $brandCapture
    $vetCapture = Invoke-CIWCommand `
        -Label 'go-vet' `
        -Executable $go `
        -Arguments @('vet', './...') `
        -TimeoutSeconds 900
    Assert-CIWCommandPassed -Label 'GO_VET' -Capture $vetCapture
}

$evidenceRelative = Get-CIWEvidenceRelativePath -Kind $JobKind
$evidenceDirectory = Join-Path $artifact $evidenceRelative
$evidencePath = Join-Path $evidenceDirectory 'evidence.json'
$stdoutPath = Join-Path $evidenceDirectory 'go-test.stdout.jsonl'
$stderrPath = Join-Path $evidenceDirectory 'go-test.stderr.log'
$exitPath = Join-Path $evidenceDirectory 'exit-code.txt'
$goEnvironment = @{}
if ($JobKind -ceq 'linux-race') {
    $gcc = Resolve-CIWTrustedLinuxSystemExecutable `
        -Path (Get-CIWApplication -Name 'gcc')
    $goEnvironment.CGO_ENABLED = '1'
    $goEnvironment.CC = $gcc
} else {
    $gcc = $null
}
$goProof = Get-CIWGoProof -Go $go -Environment $goEnvironment

if ($JobKind -ceq 'linux-race') {
    $gccCapture = Invoke-CIWCommand `
        -Label 'gcc-version' `
        -Executable $gcc `
        -Arguments @('--version') `
        -TimeoutSeconds 120 `
        -Environment $goEnvironment
    Assert-CIWCommandPassed -Label 'GCC_VERSION' -Capture $gccCapture
    $gccVersion = Read-CIWOutputText -Path $gccCapture.StdoutPath
    if ([string]::IsNullOrWhiteSpace($gccVersion) -or
        $goProof.GOOS -cne 'linux' -or
        $goProof.CGOEnabled -cne '1' -or
        $goProof.CC -cne $gcc) {
        $evidence = [IO.File]::ReadAllText(
            $evidencePath,
            $script:Utf8Strict
        ) | ConvertFrom-Json
        $evidence.go_version = $goProof.Version
        $evidence.go_env = [ordered]@{
            GOOS = $goProof.GOOS
            GOARCH = $goProof.GOARCH
            CGO_ENABLED = $goProof.CGOEnabled
            CC = $goProof.CC
        }
        $evidence.compiler_path = $gcc
        $evidence.compiler_version = $gccVersion
        $evidence.reason = 'required Go or GCC environment proof is unavailable'
        Write-CIWJson -Path $evidencePath -Value $evidence
        Fail-CIWorkflowJob -Code 'CIW_RACE_ENVIRONMENT_UNPROVED'
    }

    [string[]]$packages = @('./...')
    $testArguments = @(
        'test', '-json', '-race', '-count=1', '-timeout=30m', './...'
    )
    $runnerTimeout = 2400
} else {
    $gccVersion = $null
    [string[]]$packages = @('./...')
    $testArguments = @(
        'test', '-json', '-shuffle=on', '-count=1',
        '-timeout=30m', './...'
    )
    $runnerTimeout = 2400
}

$testCapture = Invoke-CIWCommand `
    -Label 'go-test' `
    -Executable $go `
    -Arguments $testArguments `
    -TimeoutSeconds $runnerTimeout `
    -Environment $goEnvironment `
    -StdoutPath $stdoutPath `
    -StderrPath $stderrPath `
    -OutputRoot $artifact
$testExit = [int]$testCapture.Result.ExitCode
Write-CIWText -Path $exitPath -Text "$testExit`n"

$proofComplete = $testExit -eq 0
if ($JobKind -ceq 'linux-race') {
    $proofComplete = $proofComplete -and
        $goProof.GOOS -ceq 'linux' -and
        $goProof.CGOEnabled -ceq '1' -and
        $goProof.CC -ceq $gcc -and
        -not [string]::IsNullOrWhiteSpace($gcc) -and
        -not [string]::IsNullOrWhiteSpace($gccVersion) -and
        $packages.Count -gt 0
}
$evidence = [IO.File]::ReadAllText(
    $evidencePath,
    $script:Utf8Strict
) | ConvertFrom-Json
$evidence.status = if ($proofComplete) { 'PASS' } else { 'UNPROVED' }
$evidence.proof_complete = $proofComplete
$evidence.command_outcome = if ($testExit -eq 0) { 'PASSED' } else { 'FAILED' }
$evidence.exit_code = $testExit
$evidence.started_at_utc = $testCapture.Result.StartedAtUtc
$evidence.finished_at_utc = $testCapture.Result.FinishedAtUtc
$evidence.elapsed_ms = $testCapture.Result.ElapsedMilliseconds
$evidence.go_version = $goProof.Version
$evidence.go_env = [ordered]@{
    GOOS = $goProof.GOOS
    GOARCH = $goProof.GOARCH
    CGO_ENABLED = $goProof.CGOEnabled
    CC = $goProof.CC
}
$evidence.command_argv = @($go) + @($testArguments)
$evidence.packages = $packages
if ($JobKind -ceq 'linux-race') {
    $evidence.compiler_path = $gcc
    $evidence.compiler_identity = ($gccVersion -split '\r?\n')[0]
    $evidence.compiler_version = $gccVersion
}
$evidence.reason = if ($proofComplete) {
    $null
} elseif ($testExit -ne 0) {
    'test command failed'
} else {
    'required proof is incomplete'
}
Write-CIWJson -Path $evidencePath -Value $evidence

if (-not $proofComplete) {
    Write-CIWFailureSummary `
        -Label 'GO_TEST' `
        -Result $testCapture.Result `
        -Stdout $stdoutPath `
        -Stderr $stderrPath
    Fail-CIWorkflowJob -Code 'CIW_TEST_UNPROVED'
}

if ($JobKind -ceq 'windows') {
    $binDirectory = Join-Path $artifact 'bin'
    [void][IO.Directory]::CreateDirectory($binDirectory)
    $windowsBinary = Join-Path $binDirectory 'freeagent-windows-amd64.exe'
    $buildCapture = Invoke-CIWCommand `
        -Label 'build-windows' `
        -Executable $go `
        -Arguments @(
            'build', '-trimpath', '-o', $windowsBinary, './cmd/freeagent'
        ) `
        -TimeoutSeconds 900 `
        -Environment @{
            CGO_ENABLED = '0'
            GOOS = 'windows'
            GOARCH = 'amd64'
        }
    Assert-CIWCommandPassed -Label 'WINDOWS_BUILD' -Capture $buildCapture
}

Write-Host "CI_WORKFLOW_JOB_RUN_PASS kind=$JobKind"
