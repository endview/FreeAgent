#requires -Version 7.2

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$PackageRoot,

    [ValidateSet('auto', 'windows-amd64', 'linux-amd64')]
    [string]$TargetPlatform = 'auto',

    [string]$ExecutableRelativePath = '',

    [string]$SeedRelativePath = 'config/current-v1.bootstrap.seed.json',

    [ValidateRange(5, 900)]
    [int]$CommandTimeoutSeconds = 60,

    [ValidateRange(5, 300)]
    [int]$ServeStartupTimeoutSeconds = 30,

    [ValidateRange(2, 120)]
    [int]$HttpTimeoutSeconds = 15,

    [ValidateRange(2, 120)]
    [int]$ShutdownTimeoutSeconds = 10,

    [string]$WorkParent = '',

    [switch]$KeepWorkDirectory
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:Schema = 'freeagent.developer-preview-install-validation/v1'
$script:MaximumCapturedCharacters = 1024 * 1024
$script:RequiredStepNames = [string[]]@(
    'package_layout',
    'version',
    'offline_seed',
    'init',
    'conversation_create_get',
    'multiround_chat',
    'exact_retry_attempt_invariance',
    'serve_restart_conversation',
    'backup_verify_restore',
    'restored_conversation_continue',
    'control_enable',
    'control_overview',
    'control_modules',
    'loopback_binding'
)
$script:Steps = [ordered]@{}
foreach ($name in $script:RequiredStepNames) {
    $script:Steps[$name] = [ordered]@{
        name = $name
        status = 'PENDING'
        evidence = ''
    }
}
$script:CurrentStep = 'package_layout'
$script:PackageRootPath = ''
$script:ExecutablePath = ''
$script:ResolvedTargetPlatform = ''
$script:VersionText = ''
$script:WorkRoot = ''
$script:WorkParentPath = ''
$script:WorkDirectoryRemoved = $false
$script:ActiveServers = [Collections.Generic.List[object]]::new()

function Set-StepResult {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)]
        [ValidateSet('PASS', 'BLOCKED', 'FAIL', 'NOT_RUN')]
        [string]$Status,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Evidence
    )

    if (-not $script:Steps.Contains($Name)) {
        throw "unknown Developer Preview validation step: $Name"
    }
    $script:Steps[$Name].status = $Status
    $script:Steps[$Name].evidence = $Evidence
}

function Enter-Step {
    param([Parameter(Mandatory = $true)][string]$Name)

    if (-not $script:Steps.Contains($Name)) {
        throw "unknown Developer Preview validation step: $Name"
    }
    $script:CurrentStep = $Name
}

function Throw-ValidationContract {
    param(
        [Parameter(Mandatory = $true)]
        [ValidateSet('BLOCKED', 'FAIL')]
        [string]$Status,
        [Parameter(Mandatory = $true)][string]$Code,
        [Parameter(Mandatory = $true)][string]$Message
    )

    $exception = [InvalidOperationException]::new($Message)
    $exception.Data['DeveloperPreviewStatus'] = $Status
    $exception.Data['DeveloperPreviewCode'] = $Code
    throw $exception
}

function Throw-Blocked {
    param(
        [Parameter(Mandatory = $true)][string]$Code,
        [Parameter(Mandatory = $true)][string]$Message
    )

    Throw-ValidationContract -Status BLOCKED -Code $Code -Message $Message
}

function Throw-Failed {
    param(
        [Parameter(Mandatory = $true)][string]$Code,
        [Parameter(Mandatory = $true)][string]$Message
    )

    Throw-ValidationContract -Status FAIL -Code $Code -Message $Message
}

function Get-SafeMessage {
    param([AllowNull()][string]$Text)

    if ([string]::IsNullOrEmpty($Text)) {
        return ''
    }
    $singleLine = ($Text -replace '[\r\n\t]+', ' ').Trim()
    if ($singleLine.Length -gt 1024) {
        return $singleLine.Substring(0, 1024) + '...'
    }
    return $singleLine
}

function Get-SHA256Hex {
    param([Parameter(Mandatory = $true)][AllowEmptyCollection()][byte[]]$Bytes)

    return [Convert]::ToHexString(
        [Security.Cryptography.SHA256]::HashData($Bytes)
    ).ToLowerInvariant()
}

function Get-FileSHA256Hex {
    param([Parameter(Mandatory = $true)][string]$Path)

    return Get-SHA256Hex -Bytes ([IO.File]::ReadAllBytes($Path))
}

function Resolve-ContainedFile {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$RelativePath,
        [Parameter(Mandatory = $true)][string]$Label,
        [switch]$AllowMissing
    )

    if ([IO.Path]::IsPathRooted($RelativePath) -or
        [string]::IsNullOrWhiteSpace($RelativePath)) {
        Throw-Failed -Code 'PACKAGE_PATH_INVALID' -Message "$Label must be a non-empty relative path"
    }
    $rootFull = [IO.Path]::GetFullPath($Root)
    $full = [IO.Path]::GetFullPath((Join-Path $rootFull $RelativePath))
    $prefix = $rootFull.TrimEnd([IO.Path]::DirectorySeparatorChar,
        [IO.Path]::AltDirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    if (-not $full.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
        Throw-Failed -Code 'PACKAGE_PATH_ESCAPES_ROOT' -Message "$Label escapes the extracted package root"
    }
    if (-not $AllowMissing -and -not [IO.File]::Exists($full)) {
        Throw-Failed -Code 'PACKAGE_FILE_MISSING' -Message "$Label is missing from the extracted package"
    }
    return $full
}

function Resolve-ContainedDirectory {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$RelativePath,
        [Parameter(Mandatory = $true)][string]$Label
    )

    if ([IO.Path]::IsPathRooted($RelativePath) -or
        [string]::IsNullOrWhiteSpace($RelativePath)) {
        Throw-Failed -Code 'PACKAGE_PATH_INVALID' -Message "$Label must be a non-empty relative path"
    }
    $rootFull = [IO.Path]::GetFullPath($Root)
    $full = [IO.Path]::GetFullPath((Join-Path $rootFull $RelativePath))
    $prefix = $rootFull.TrimEnd([IO.Path]::DirectorySeparatorChar,
        [IO.Path]::AltDirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    if (-not $full.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
        Throw-Failed -Code 'PACKAGE_PATH_ESCAPES_ROOT' -Message "$Label escapes the extracted package root"
    }
    if (-not [IO.Directory]::Exists($full)) {
        Throw-Failed -Code 'PACKAGE_DIRECTORY_MISSING' -Message "$Label is missing from the extracted package"
    }
    return $full
}

function Remove-SensitiveChildEnvironment {
    param([Parameter(Mandatory = $true)][Diagnostics.ProcessStartInfo]$StartInfo)

    $blockedExact = [Collections.Generic.HashSet[string]]::new(
        [StringComparer]::OrdinalIgnoreCase
    )
    foreach ($name in @(
        'FREEAGENT_DEEPSEEK_API_KEY', 'OPENAI_API_KEY', 'ANTHROPIC_API_KEY',
        'HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY', 'NO_PROXY'
    )) {
        $null = $blockedExact.Add($name)
    }

    $names = [string[]]@($StartInfo.Environment.Keys)
    foreach ($name in $names) {
        if ($blockedExact.Contains($name) -or
            $name -match '(?i)(?:SECRET|TOKEN|PASSWORD|API_KEY)$') {
            $null = $StartInfo.Environment.Remove($name)
        }
    }
    $StartInfo.Environment['FREEAGENT_DEVELOPER_PREVIEW_OFFLINE'] = '1'
}

function ConvertTo-WindowsCommandLineArgument {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value)

    if ($Value.Length -gt 0 -and $Value -notmatch '[\s"]') {
        return $Value
    }
    $builder = [Text.StringBuilder]::new()
    $null = $builder.Append('"')
    $slashes = 0
    foreach ($character in $Value.ToCharArray()) {
        if ($character -eq '\') {
            $slashes++
            continue
        }
        if ($character -eq '"') {
            $null = $builder.Append(('\' * (($slashes * 2) + 1)))
            $null = $builder.Append('"')
            $slashes = 0
            continue
        }
        if ($slashes -gt 0) {
            $null = $builder.Append(('\' * $slashes))
            $slashes = 0
        }
        $null = $builder.Append($character)
    }
    if ($slashes -gt 0) {
        $null = $builder.Append(('\' * ($slashes * 2)))
    }
    $null = $builder.Append('"')
    return $builder.ToString()
}

function New-ToolStartInfo {
    param([Parameter(Mandatory = $true)][AllowEmptyCollection()][string[]]$Arguments)

    $startInfo = [Diagnostics.ProcessStartInfo]::new()
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $startInfo.WorkingDirectory = $script:PackageRootPath
    $extension = [IO.Path]::GetExtension($script:ExecutablePath)
    if ($IsWindows -and $extension -in @('.cmd', '.bat')) {
        $startInfo.FileName = $env:ComSpec
        $command = @(
            ConvertTo-WindowsCommandLineArgument -Value $script:ExecutablePath
        ) + @($Arguments | ForEach-Object {
            ConvertTo-WindowsCommandLineArgument -Value $_
        })
        foreach ($argument in @('/d', '/s', '/c', [string]::Join(' ', $command))) {
            $null = $startInfo.ArgumentList.Add($argument)
        }
    } else {
        $startInfo.FileName = $script:ExecutablePath
        foreach ($argument in $Arguments) {
            $null = $startInfo.ArgumentList.Add($argument)
        }
    }
    $startInfo.StandardOutputEncoding = [Text.UTF8Encoding]::new($false, $true)
    $startInfo.StandardErrorEncoding = [Text.UTF8Encoding]::new($false, $true)
    Remove-SensitiveChildEnvironment -StartInfo $startInfo
    return $startInfo
}

function Stop-BoundedProcess {
    param(
        [Parameter(Mandatory = $true)][Diagnostics.Process]$Process,
        [Parameter(Mandatory = $true)][int]$TimeoutSeconds
    )

    if ($Process.HasExited) {
        return
    }
    try {
        $Process.Kill($true)
    } catch {
        try { $Process.Kill() } catch { }
    }
    if (-not $Process.WaitForExit($TimeoutSeconds * 1000)) {
        Throw-Failed -Code 'PROCESS_CONTAINMENT_FAILED' -Message 'child process did not terminate within the containment deadline'
    }
}

function Invoke-ToolCommand {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][string[]]$Arguments,
        [int]$TimeoutSeconds = $CommandTimeoutSeconds,
        [switch]$AllowNonZero
    )

    $process = [Diagnostics.Process]::new()
    $process.StartInfo = New-ToolStartInfo -Arguments $Arguments
    $stopwatch = [Diagnostics.Stopwatch]::StartNew()
    $started = $false
    try {
        if (-not $process.Start()) {
            Throw-Failed -Code 'PROCESS_START_FAILED' -Message 'freeagent process did not start'
        }
        $started = $true
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit($TimeoutSeconds * 1000)) {
            Stop-BoundedProcess -Process $process -TimeoutSeconds $ShutdownTimeoutSeconds
            Throw-Failed -Code 'COMMAND_TIMEOUT' -Message "freeagent command exceeded ${TimeoutSeconds}s"
        }
        if (-not [Threading.Tasks.Task]::WaitAll(
            [Threading.Tasks.Task[]]@($stdoutTask, $stderrTask),
            $ShutdownTimeoutSeconds * 1000
        )) {
            Throw-Failed -Code 'CAPTURE_TIMEOUT' -Message 'freeagent stdout/stderr did not converge'
        }
        $stdout = [string]$stdoutTask.GetAwaiter().GetResult()
        $stderr = [string]$stderrTask.GetAwaiter().GetResult()
        if ($stdout.Length -gt $script:MaximumCapturedCharacters -or
            $stderr.Length -gt $script:MaximumCapturedCharacters) {
            Throw-Failed -Code 'COMMAND_OUTPUT_LIMIT' -Message 'freeagent command exceeded the 1 MiB text capture limit'
        }
        $result = [pscustomobject][ordered]@{
            exit_code = [int]$process.ExitCode
            stdout = $stdout
            stderr = $stderr
            elapsed_ms = [long]$stopwatch.ElapsedMilliseconds
        }
        if (-not $AllowNonZero -and $result.exit_code -ne 0) {
            $summary = Get-SafeMessage -Text $stderr
            Throw-Failed -Code 'COMMAND_EXIT_NONZERO' -Message "freeagent command failed with exit $($result.exit_code): $summary"
        }
        return $result
    } finally {
        $stopwatch.Stop()
        if ($started -and -not $process.HasExited) {
            try { Stop-BoundedProcess -Process $process -TimeoutSeconds $ShutdownTimeoutSeconds } catch { }
        }
        $process.Dispose()
    }
}

function ConvertFrom-CommandJson {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text,
        [Parameter(Mandatory = $true)][string]$Label
    )

    $trimmed = $Text.Trim()
    if ([string]::IsNullOrEmpty($trimmed)) {
        Throw-Failed -Code 'COMMAND_JSON_EMPTY' -Message "$Label returned empty stdout"
    }
    $value = ConvertFrom-DPIStrictJson `
        -Text $trimmed `
        -Code 'COMMAND_JSON_INVALID'
    if ($null -eq $value -or $value -is [Array]) {
        Throw-Failed -Code 'COMMAND_JSON_SHAPE' -Message "$Label did not return one JSON object"
    }
    return ,$value
}

function Get-DPIJsonField {
    param(
        [Parameter(Mandatory = $true)][AllowNull()]$Object,
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Label,
        [switch]$Required
    )

    if ($null -eq $Object) {
        Throw-Failed -Code 'COMMAND_JSON_FIELD_MISSING' `
            -Message "$Label omitted the $Name field"
    }
    $value = $null
    $found = $false
    foreach ($property in @($Object.PSObject.Properties)) {
        if ($property.Name -ceq $Name) {
            $value = $property.Value
            $found = $true
            break
        }
    }
    if (-not $found -and $Required) {
        Throw-Failed -Code 'COMMAND_JSON_FIELD_MISSING' `
            -Message "$Label omitted the $Name field"
    }
    return ,$value
}

function Assert-DPIJsonElement {
    param(
        [Parameter(Mandatory = $true)]$Element,
        [Parameter(Mandatory = $true)][string]$Code,
        [int]$Depth = 0
    )
    if ($Depth -gt 100) {
        Throw-Failed -Code $Code -Message 'JSON nesting exceeded 100 levels'
    }
    if ($Element.ValueKind -ceq 'Object') {
        $names = [Collections.Generic.HashSet[string]]::new(
            [StringComparer]::Ordinal
        )
        foreach ($property in $Element.EnumerateObject()) {
            if (-not $names.Add($property.Name)) {
                Throw-Failed -Code $Code -Message 'JSON object contained a duplicate key'
            }
            Assert-DPIJsonElement -Element $property.Value -Code $Code -Depth ($Depth + 1)
        }
        return
    }
    if ($Element.ValueKind -ceq 'Array') {
        foreach ($item in $Element.EnumerateArray()) {
            Assert-DPIJsonElement -Element $item -Code $Code -Depth ($Depth + 1)
        }
    }
}

function ConvertFrom-DPIStrictJson {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text,
        [Parameter(Mandatory = $true)][string]$Code
    )
    try {
        $options = [Text.Json.JsonDocumentOptions]::new()
        $options.MaxDepth = 100
        $options.AllowTrailingCommas = $false
        $options.CommentHandling = [Text.Json.JsonCommentHandling]::Disallow
        $document = [Text.Json.JsonDocument]::Parse($Text, $options)
        try {
            Assert-DPIJsonElement -Element $document.RootElement -Code $Code
        } finally {
            $document.Dispose()
        }
        return $Text | ConvertFrom-Json -Depth 100
    } catch {
        $caught = $_.Exception
        if ([string]$caught.Data['DeveloperPreviewCode'] -ceq $Code) {
            throw
        }
        Throw-Failed -Code $Code -Message 'input was not valid strict JSON'
    }
}

function Assert-StringEqual {
    param(
        [AllowNull()]$Actual,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Expected,
        [Parameter(Mandatory = $true)][string]$Label
    )

    if ([string]$Actual -cne $Expected) {
        Throw-Failed -Code 'CONTRACT_VALUE_MISMATCH' -Message "$Label did not match the expected value"
    }
}

function Assert-ChatResult {
    param(
        [Parameter(Mandatory = $true)]$Result,
        [Parameter(Mandatory = $true)][string]$Conversation,
        [Parameter(Mandatory = $true)][long]$Revision,
        [Parameter(Mandatory = $true)][string]$Message,
        [AllowEmptyString()][string]$PreviousRun = ''
    )

    $resultConversation = Get-DPIJsonField -Object $Result -Name 'conversation_id' `
        -Required -Label 'chat result'
    $resultReply = Get-DPIJsonField -Object $Result -Name 'reply' `
        -Required -Label 'chat result'
    $resultDisposition = Get-DPIJsonField -Object $Result -Name 'disposition' `
        -Required -Label 'chat result'
    $resultRevision = Get-DPIJsonField -Object $Result -Name 'conversation_revision' `
        -Required -Label 'chat result'
    $resultRunID = Get-DPIJsonField -Object $Result -Name 'run_id' `
        -Required -Label 'chat result'

    Assert-StringEqual -Actual $resultConversation -Expected $Conversation -Label 'chat conversation'
    if ([string]$resultReply -cne $Message) {
        Throw-Failed -Code 'CONTRACT_VALUE_MISMATCH' -Message (
            'offline echo reply did not match the fixed smoke message; ' +
            'actual_length=' + ([string]$resultReply).Length +
            ' expected_length=' + $Message.Length
        )
    }
    Assert-StringEqual -Actual $resultDisposition -Expected 'TERMINATED' -Label 'chat disposition'
    if ([long]$resultRevision -ne $Revision -or
        [string]::IsNullOrWhiteSpace([string]$resultRunID) -or
        (-not [string]::IsNullOrEmpty($PreviousRun) -and
            [string]$resultRunID -ceq $PreviousRun)) {
        Throw-Failed -Code 'CHAT_CONVERSATION_DRIFT' -Message 'chat did not advance the exact Conversation head once'
    }
}

function Get-StorePhysicalSnapshot {
    param([Parameter(Mandatory = $true)][string]$DatabasePath)

    $entries = [Collections.Generic.List[object]]::new()
    foreach ($path in @(
        $DatabasePath,
        $DatabasePath + '-wal',
        $DatabasePath + '-shm',
        $DatabasePath + '-journal'
    )) {
        if ([IO.File]::Exists($path)) {
            $file = Get-Item -LiteralPath $path -Force
            $entries.Add([pscustomobject][ordered]@{
                leaf = [IO.Path]::GetFileName($path)
                bytes = [long]$file.Length
                sha256 = Get-FileSHA256Hex -Path $path
            })
        }
    }
    return [string]::Join("`n", @($entries | ForEach-Object {
        '{0}|{1}|{2}' -f $_.leaf, $_.bytes, $_.sha256
    }))
}

function Test-LoopbackOrigin {
    param(
        [Parameter(Mandatory = $true)][string]$Origin,
        [Parameter(Mandatory = $true)][string]$Label
    )

    $uri = $null
    if (-not [Uri]::TryCreate($Origin, [UriKind]::Absolute, [ref]$uri) -or
        $uri.Scheme -cne 'http' -or -not $uri.IsDefaultPort -and $uri.Port -le 0) {
        Throw-Failed -Code 'LOOPBACK_ORIGIN_INVALID' -Message "$Label is not a valid HTTP origin"
    }
    $address = $null
    if (-not [Net.IPAddress]::TryParse($uri.Host, [ref]$address) -or
        -not [Net.IPAddress]::IsLoopback($address)) {
        Throw-Failed -Code 'NON_LOOPBACK_BINDING' -Message "$Label is not bound to a numeric loopback address"
    }
    return $uri.GetLeftPart([UriPartial]::Authority)
}

function New-LoopbackHttpClient {
    param([switch]$Cookies)

    $handler = [Net.Http.HttpClientHandler]::new()
    $handler.UseProxy = $false
    if ($Cookies) {
        $handler.UseCookies = $true
        $handler.CookieContainer = [Net.CookieContainer]::new()
    }
    $client = [Net.Http.HttpClient]::new($handler, $true)
    $client.Timeout = [TimeSpan]::FromSeconds($HttpTimeoutSeconds)
    return $client
}

function Invoke-LoopbackHttp {
    param(
        [Parameter(Mandatory = $true)][Net.Http.HttpClient]$Client,
        [Parameter(Mandatory = $true)][ValidateSet('GET', 'POST')][string]$Method,
        [Parameter(Mandatory = $true)][string]$Uri,
        [AllowEmptyString()][string]$Body = '',
        [hashtable]$Headers = @{}
    )

    $origin = ([Uri]$Uri).GetLeftPart([UriPartial]::Authority)
    $null = Test-LoopbackOrigin -Origin $origin -Label 'HTTP request origin'
    $request = [Net.Http.HttpRequestMessage]::new(
        [Net.Http.HttpMethod]::new($Method),
        $Uri
    )
    try {
        foreach ($name in $Headers.Keys) {
            if (-not $request.Headers.TryAddWithoutValidation($name, [string]$Headers[$name])) {
                Throw-Failed -Code 'HTTP_HEADER_INVALID' -Message "could not add required HTTP header $name"
            }
        }
        if ($Method -ceq 'POST') {
            $request.Content = [Net.Http.ByteArrayContent]::new(
                [Text.UTF8Encoding]::new($false).GetBytes($Body)
            )
            $null = $request.Content.Headers.TryAddWithoutValidation(
                'Content-Type',
                'application/json'
            )
        }
        $response = $Client.Send($request)
        try {
            $responseBody = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
            if ($responseBody.Length -gt $script:MaximumCapturedCharacters) {
                Throw-Failed -Code 'HTTP_OUTPUT_LIMIT' -Message 'loopback response exceeded the 1 MiB limit'
            }
            return [pscustomobject][ordered]@{
                status_code = [int]$response.StatusCode
                body = [string]$responseBody
                content_type = [string]$response.Content.Headers.ContentType
            }
        } finally {
            $response.Dispose()
        }
    } finally {
        $request.Dispose()
    }
}

function Start-ToolServer {
    param([Parameter(Mandatory = $true)][AllowEmptyCollection()][string[]]$Arguments)

    $process = [Diagnostics.Process]::new()
    $process.StartInfo = New-ToolStartInfo -Arguments $Arguments
    if (-not $process.Start()) {
        Throw-Failed -Code 'SERVE_START_FAILED' -Message 'freeagent serve did not start'
    }
    $stderrTask = $process.StandardError.ReadToEndAsync()
    $readyTask = $process.StandardOutput.ReadLineAsync()
    if (-not $readyTask.Wait($ServeStartupTimeoutSeconds * 1000)) {
        Stop-BoundedProcess -Process $process -TimeoutSeconds $ShutdownTimeoutSeconds
        $process.Dispose()
        Throw-Failed -Code 'SERVE_READY_TIMEOUT' -Message 'freeagent serve did not publish readiness within the deadline'
    }
    $readyLine = [string]$readyTask.GetAwaiter().GetResult()
    if ([string]::IsNullOrWhiteSpace($readyLine)) {
        $earlyError = if ($stderrTask.IsCompleted) {
            Get-SafeMessage -Text ([string]$stderrTask.GetAwaiter().GetResult())
        } else { '' }
        $earlyExit = $process.HasExited
        if (-not $earlyExit) {
            Stop-BoundedProcess -Process $process -TimeoutSeconds $ShutdownTimeoutSeconds
        }
        $process.Dispose()
        if ($earlyError -match '(?i)(unknown|not defined|enable-control|control-handoff)') {
            Throw-Blocked -Code 'CONTROL_OR_SERVE_SURFACE_UNAVAILABLE' -Message 'the packaged CLI does not expose the required serve/control surface'
        }
        Throw-Failed -Code 'SERVE_READY_INVALID' -Message "freeagent serve exited or returned empty readiness: $earlyError"
    }
    $ready = ConvertFrom-CommandJson -Text $readyLine -Label 'serve readiness'
    $server = [pscustomobject][ordered]@{
        process = $process
        stderr_task = $stderrTask
        stdout_task = $process.StandardOutput.ReadToEndAsync()
        ready = $ready
        stopped = $false
    }
    $script:ActiveServers.Add($server)
    return $server
}

function Stop-ToolServer {
    param([Parameter(Mandatory = $true)]$Server)

    if ($Server.stopped) {
        return
    }
    $Server.stopped = $true
    try {
        Stop-BoundedProcess -Process $Server.process -TimeoutSeconds $ShutdownTimeoutSeconds
        $null = [Threading.Tasks.Task]::WaitAll(
            [Threading.Tasks.Task[]]@($Server.stdout_task, $Server.stderr_task),
            $ShutdownTimeoutSeconds * 1000
        )
    } finally {
        $Server.process.Dispose()
    }
}

function Wait-ForFile {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][int]$TimeoutSeconds
    )

    $clock = [Diagnostics.Stopwatch]::StartNew()
    while ($clock.Elapsed.TotalSeconds -lt $TimeoutSeconds) {
        if ([IO.File]::Exists($Path)) {
            return
        }
        Start-Sleep -Milliseconds 25
    }
    Throw-Failed -Code 'EXPECTED_FILE_TIMEOUT' -Message 'the expected owner handoff file did not appear'
}

function Remove-IsolatedWorkRoot {
    if ([string]::IsNullOrEmpty($script:WorkRoot) -or
        -not [IO.Directory]::Exists($script:WorkRoot) -or
        $KeepWorkDirectory) {
        return
    }
    $full = [IO.Path]::GetFullPath($script:WorkRoot)
    $parent = [IO.Path]::GetFullPath($script:WorkParentPath).TrimEnd(
        [IO.Path]::DirectorySeparatorChar,
        [IO.Path]::AltDirectorySeparatorChar
    )
    $expectedPrefix = $parent + [IO.Path]::DirectorySeparatorChar +
        'freeagent-developer-preview-'
    if (-not $full.StartsWith($expectedPrefix, [StringComparison]::OrdinalIgnoreCase) -or
        [IO.Path]::GetDirectoryName($full) -cne $parent) {
        throw 'refusing to remove a work directory outside the isolated validation parent'
    }
    Remove-Item -LiteralPath $full -Recurse -Force
    $script:WorkDirectoryRemoved = -not [IO.Directory]::Exists($full)
}

function Set-IsolatedWorkRootPrivate {
    param([Parameter(Mandatory = $true)][string]$Path)

    if ($IsWindows) {
        try {
            $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
            $current = [Security.Principal.SecurityIdentifier]$identity.User
            $security = [System.Security.AccessControl.DirectorySecurity]::new()
            $security.SetAccessRuleProtection($true, $false)
            $inheritance =
                [System.Security.AccessControl.InheritanceFlags]::ContainerInherit -bor
                [System.Security.AccessControl.InheritanceFlags]::ObjectInherit
            $security.AddAccessRule(
                [System.Security.AccessControl.FileSystemAccessRule]::new(
                    $current,
                    [System.Security.AccessControl.FileSystemRights]::FullControl,
                    $inheritance,
                    [System.Security.AccessControl.PropagationFlags]::None,
                    [System.Security.AccessControl.AccessControlType]::Allow
                )
            )
            [System.IO.FileSystemAclExtensions]::SetAccessControl(
                [IO.DirectoryInfo]::new($Path),
                $security
            )
        } catch {
            Throw-Failed -Code 'WORK_ROOT_PRIVACY_FAILED' `
                -Message 'the isolated validation WorkRoot could not be made private'
        }
        return
    }

    try {
        [IO.File]::SetUnixFileMode(
            $Path,
            [IO.UnixFileMode]::UserRead -bor [IO.UnixFileMode]::UserWrite -bor
                [IO.UnixFileMode]::UserExecute
        )
    } catch {
        Throw-Failed -Code 'WORK_ROOT_PRIVACY_FAILED' `
            -Message 'the isolated validation WorkRoot could not be made private'
    }
}

function New-DefaultWorkParent {
    $userProfile = [Environment]::GetFolderPath('UserProfile')
    if ([string]::IsNullOrWhiteSpace($userProfile) -or
        -not [IO.Directory]::Exists($userProfile)) {
        Throw-Failed -Code 'WORK_PARENT_HOME_MISSING' `
            -Message 'the user profile directory is unavailable for isolated validation state'
    }

    $cacheRoot = Join-Path $userProfile '.cache'
    if (-not [IO.Directory]::Exists($cacheRoot)) {
        $null = [IO.Directory]::CreateDirectory($cacheRoot)
    }
    $workParent = Join-Path (
        Join-Path $cacheRoot 'freeagent'
    ) 'developer-preview-install-validation'
    if (-not [IO.Directory]::Exists($workParent)) {
        $null = [IO.Directory]::CreateDirectory($workParent)
    }
    Set-IsolatedWorkRootPrivate -Path $workParent
    return (Resolve-Path -LiteralPath $workParent).Path
}

$status = 'FAIL'
$exitCode = 1
$failureCode = ''
$failureMessage = ''

try {
    Enter-Step -Name package_layout
    if (-not [IO.Directory]::Exists($PackageRoot)) {
        Throw-Failed -Code 'PACKAGE_ROOT_MISSING' -Message 'the extracted package root does not exist'
    }
    $script:PackageRootPath = (Resolve-Path -LiteralPath $PackageRoot).Path
    $rootItem = Get-Item -LiteralPath $script:PackageRootPath -Force
    if (-not $rootItem.PSIsContainer -or
        (([long]$rootItem.Attributes -band [long][IO.FileAttributes]::ReparsePoint) -ne 0)) {
        Throw-Failed -Code 'PACKAGE_ROOT_UNSAFE' -Message 'the extracted package root must be an ordinary directory'
    }

    $hostPlatform = if ($IsWindows) {
        'windows-amd64'
    } elseif ($IsLinux) {
        'linux-amd64'
    } else {
        ''
    }
    if ([Runtime.InteropServices.RuntimeInformation]::OSArchitecture -ne
        [Runtime.InteropServices.Architecture]::X64) {
        $hostPlatform = ''
    }
    if ($TargetPlatform -ceq 'auto') {
        if ([string]::IsNullOrEmpty($hostPlatform)) {
            Throw-Blocked -Code 'HOST_PLATFORM_UNSUPPORTED' -Message 'this validator supports native Windows amd64 or Linux amd64 hosts only'
        }
        $script:ResolvedTargetPlatform = $hostPlatform
    } else {
        $script:ResolvedTargetPlatform = $TargetPlatform
        if ($TargetPlatform -cne $hostPlatform) {
            Throw-Blocked -Code 'HOST_PLATFORM_MISMATCH' -Message 'the requested package must be tested natively; WSL or emulation is not used'
        }
    }

    if ([string]::IsNullOrEmpty($ExecutableRelativePath)) {
        $ExecutableRelativePath = if ($script:ResolvedTargetPlatform -ceq 'windows-amd64') {
            'bin/freeagent-windows-amd64.exe'
        } else {
            'bin/freeagent-linux-amd64'
        }
    }
    $script:ExecutablePath = Resolve-ContainedFile -Root $script:PackageRootPath `
        -RelativePath $ExecutableRelativePath -Label 'platform executable'

    $requiredLeaves = [string[]]@(
        'VERSION', 'LICENSE', 'THIRD_PARTY_NOTICES.md', 'INSTALL.md',
        'QUICKSTART.md', 'KNOWN_LIMITATIONS.md', 'RELEASE_NOTES.md',
        'VERIFY_CHECKSUMS.md', 'config/README.md', 'data/README.md'
    )
    foreach ($leaf in $requiredLeaves) {
        $null = Resolve-ContainedFile -Root $script:PackageRootPath `
            -RelativePath $leaf -Label $leaf
    }
    $supplyChain = [IO.Path]::GetFullPath((Join-Path $script:PackageRootPath 'supply-chain'))
    if (-not [IO.Directory]::Exists($supplyChain) -or
        @(Get-ChildItem -LiteralPath $supplyChain -File -Force).Count -eq 0) {
        Throw-Failed -Code 'SUPPLY_CHAIN_PAYLOAD_MISSING' -Message 'supply-chain must contain at least one regular file'
    }
    foreach ($artifactRelativePath in @(
        'config/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0',
        'config/bootstrap-artifacts/freeagent.builtin.model.echo/2.0.0'
    )) {
        $artifactDirectory = Resolve-ContainedDirectory -Root $script:PackageRootPath `
            -RelativePath $artifactRelativePath -Label $artifactRelativePath
        if (@(Get-ChildItem -LiteralPath $artifactDirectory -File -Recurse -Force).Count -eq 0) {
            Throw-Failed -Code 'BOOTSTRAP_ARTIFACT_EMPTY' `
                -Message "$artifactRelativePath must contain at least one regular file"
        }
    }
    Set-StepResult -Name package_layout -Status PASS `
        -Evidence 'required package leaves and the native platform executable are present'

    Enter-Step -Name version
    $versionPath = Resolve-ContainedFile -Root $script:PackageRootPath `
        -RelativePath 'VERSION' -Label VERSION
    $expectedVersion = ([IO.File]::ReadAllText($versionPath, [Text.UTF8Encoding]::new($false, $true))).Trim()
    if ([string]::IsNullOrWhiteSpace($expectedVersion) -or
        $expectedVersion -match '[\r\n]' -or $expectedVersion.Length -gt 128) {
        Throw-Failed -Code 'VERSION_FILE_INVALID' -Message 'VERSION must contain one short, non-empty UTF-8 line'
    }
    $versionResult = Invoke-ToolCommand -Arguments @('--version') -AllowNonZero
    if ($versionResult.exit_code -ne 0) {
        Throw-Blocked -Code 'VERSION_COMMAND_UNAVAILABLE' -Message 'the packaged CLI does not yet implement freeagent --version'
    }
    if (-not [string]::IsNullOrWhiteSpace($versionResult.stderr)) {
        Throw-Failed -Code 'VERSION_STDERR_NONEMPTY' -Message 'freeagent --version wrote to stderr'
    }
    $script:VersionText = $versionResult.stdout.Trim()
    if ($script:VersionText.Length -gt 512 -or
        $script:VersionText.IndexOf($expectedVersion, [StringComparison]::Ordinal) -lt 0 -or
        $script:VersionText.IndexOf('freeagent', [StringComparison]::OrdinalIgnoreCase) -lt 0) {
        Throw-Failed -Code 'VERSION_OUTPUT_MISMATCH' -Message 'freeagent --version is not traceable to the packaged VERSION'
    }
    Set-StepResult -Name version -Status PASS -Evidence $script:VersionText

    Enter-Step -Name offline_seed
    $seedPath = Resolve-ContainedFile -Root $script:PackageRootPath `
        -RelativePath $SeedRelativePath -Label 'offline bootstrap seed' -AllowMissing
    if (-not [IO.File]::Exists($seedPath)) {
        Throw-Blocked -Code 'OFFLINE_SEED_NOT_PACKAGED' -Message 'the extracted package does not include the offline Echo bootstrap seed required by init'
    }
    $seedRaw = [IO.File]::ReadAllText($seedPath, [Text.UTF8Encoding]::new($false, $true))
    $seed = ConvertFrom-DPIStrictJson `
        -Text $seedRaw `
        -Code 'OFFLINE_SEED_INVALID'
    $seedSchema = Get-DPIJsonField -Object $seed -Name 'schema_version' `
        -Required -Label 'offline bootstrap seed'
    $seedModelBinding = Get-DPIJsonField -Object $seed -Name 'model_binding' `
        -Required -Label 'offline bootstrap seed'
    $seedModelConfig = Get-DPIJsonField -Object $seedModelBinding -Name 'config' `
        -Required -Label 'offline bootstrap seed model binding'
    $seedProvider = Get-DPIJsonField -Object $seedModelConfig -Name 'provider' `
        -Required -Label 'offline bootstrap seed model config'
    $seedModel = Get-DPIJsonField -Object $seedModelConfig -Name 'model' `
        -Required -Label 'offline bootstrap seed model config'
    $seedModule = Get-DPIJsonField -Object $seed -Name 'module' `
        -Required -Label 'offline bootstrap seed'
    $seedModuleID = Get-DPIJsonField -Object $seedModule -Name 'module_id' `
        -Required -Label 'offline bootstrap seed module'
    if ($seedSchema -cnotin @(
            'freeagent.bootstrap-seed/v1',
            'freeagent.bootstrap-seed/v2'
        ) -or
        $seedProvider -cne 'freeagent.local' -or
        $seedModel -cne 'freeagent-dev-echo' -or
        $seedModuleID -cne 'freeagent.builtin.model.echo' -or
        $seedRaw -match '(?i)https?://|deepseek|api[_-]?key|secret[_-]?ref') {
        Throw-Blocked -Code 'OFFLINE_ECHO_SEED_UNPROVEN' -Message 'the packaged seed is not the deterministic local Echo configuration'
    }
    Set-StepResult -Name offline_seed -Status PASS `
        -Evidence ('deterministic local Echo seed sha256=' + (Get-SHA256Hex -Bytes ([Text.UTF8Encoding]::new($false, $true).GetBytes($seedRaw))))

    Enter-Step -Name init
    if ([string]::IsNullOrWhiteSpace($WorkParent)) {
        $script:WorkParentPath = New-DefaultWorkParent
    } else {
        if (-not [IO.Directory]::Exists($WorkParent)) {
            Throw-Failed -Code 'WORK_PARENT_MISSING' -Message 'the requested work parent does not exist'
        }
        $script:WorkParentPath = (Resolve-Path -LiteralPath $WorkParent).Path
    }
    $script:WorkRoot = Join-Path $script:WorkParentPath `
        ('freeagent-developer-preview-' + [Guid]::NewGuid().ToString('N'))
    $null = New-Item -ItemType Directory -Path $script:WorkRoot
    Set-IsolatedWorkRootPrivate -Path $script:WorkRoot
    $database = Join-Path $script:WorkRoot 'source.sqlite'
    $artifacts = Join-Path $script:WorkRoot 'source-artifacts'
    $bundle = Join-Path $script:WorkRoot 'backup-bundle'
    $restoredDatabase = Join-Path $script:WorkRoot 'restored.sqlite'
    $restoredArtifacts = Join-Path $script:WorkRoot 'restored-artifacts'
    $conversation = 'developer-preview-install-' + [Guid]::NewGuid().ToString('N')
    $deadline = [DateTime]::UtcNow.AddHours(4).ToString('o', [Globalization.CultureInfo]::InvariantCulture)

    $initResult = Invoke-ToolCommand -Arguments @(
        'init', '--db', $database, '--artifact-root', $artifacts,
        '--seed', $seedPath
    )
    $null = ConvertFrom-CommandJson -Text $initResult.stdout -Label init
    if (-not [IO.File]::Exists($database) -or -not [IO.Directory]::Exists($artifacts)) {
        Throw-Failed -Code 'INIT_OUTPUT_MISSING' -Message 'init did not create the isolated Store and artifact root'
    }
    Set-StepResult -Name init -Status PASS -Evidence 'isolated Current Store and artifact root created'

    Enter-Step -Name conversation_create_get
    $createdCommand = Invoke-ToolCommand -Arguments @(
        'conversation-create', '--db', $database, '--conversation', $conversation
    )
    $created = ConvertFrom-CommandJson -Text $createdCommand.stdout -Label conversation_create
    $createdCreated = Get-DPIJsonField -Object $created -Name 'created' `
        -Required -Label 'conversation_create'
    $createdRevision = Get-DPIJsonField -Object $created -Name 'revision' `
        -Required -Label 'conversation_create'
    $createdHeadRun = Get-DPIJsonField -Object $created -Name 'head_run_id' `
        -Label 'conversation_create'
    if (-not [bool]$createdCreated -or [long]$createdRevision -ne 0 -or
        -not [string]::IsNullOrEmpty([string]$createdHeadRun)) {
        Throw-Failed -Code 'CONVERSATION_CREATE_INVALID' -Message 'conversation-create did not return a new revision-zero Conversation'
    }
    $initialGet = ConvertFrom-CommandJson -Text (
        Invoke-ToolCommand -Arguments @(
            'conversation-get', '--db', $database, '--conversation', $conversation
        )
    ).stdout -Label conversation_get
    $initialRevision = Get-DPIJsonField -Object $initialGet -Name 'revision' `
        -Required -Label 'conversation_get'
    $initialCreated = Get-DPIJsonField -Object $initialGet -Name 'created' `
        -Required -Label 'conversation_get'
    if ([long]$initialRevision -ne 0 -or [bool]$initialCreated) {
        Throw-Failed -Code 'CONVERSATION_GET_INVALID' -Message 'conversation-get did not recover the new Conversation'
    }
    Set-StepResult -Name conversation_create_get -Status PASS -Evidence 'revision=0, head empty'

    Enter-Step -Name multiround_chat
    $turn1Message = 'developer-preview-offline-turn-one'
    $turn1Args = [string[]]@(
        'chat', '--db', $database, '--artifact-root', $artifacts,
        '--conversation', $conversation, '--conversation-revision', '0',
        '--message', $turn1Message, '--request-id', 'developer-preview-turn-1',
        '--deadline', $deadline
    )
    $turn1Command = Invoke-ToolCommand -Arguments $turn1Args
    $turn1 = ConvertFrom-CommandJson -Text $turn1Command.stdout -Label chat_turn_1
    Assert-ChatResult -Result $turn1 -Conversation $conversation -Revision 1 `
        -Message $turn1Message

    Enter-Step -Name exact_retry_attempt_invariance
    $beforeRetry = Get-StorePhysicalSnapshot -DatabasePath $database
    $retryCommand = Invoke-ToolCommand -Arguments $turn1Args
    $afterRetry = Get-StorePhysicalSnapshot -DatabasePath $database
    if ($retryCommand.stdout -cne $turn1Command.stdout -or
        $beforeRetry -cne $afterRetry) {
        Throw-Failed -Code 'EXACT_RETRY_MUTATED_STORE' -Message 'exact retry changed the terminal response or durable Store bytes; Attempt invariance is not proven'
    }
    $afterRetryGet = ConvertFrom-CommandJson -Text (
        Invoke-ToolCommand -Arguments @(
            'conversation-get', '--db', $database, '--conversation', $conversation
        )
    ).stdout -Label conversation_get_after_retry
    $afterRetryRevision = Get-DPIJsonField -Object $afterRetryGet -Name 'revision' `
        -Required -Label 'conversation_get_after_retry'
    $afterRetryHeadRun = Get-DPIJsonField -Object $afterRetryGet -Name 'head_run_id' `
        -Required -Label 'conversation_get_after_retry'
    if ([long]$afterRetryRevision -ne 1 -or
        [string]$afterRetryHeadRun -cne [string]$turn1.run_id) {
        Throw-Failed -Code 'EXACT_RETRY_ADVANCED_CONVERSATION' -Message 'exact retry advanced the Conversation revision or head'
    }
    Set-StepResult -Name exact_retry_attempt_invariance -Status PASS `
        -Evidence 'terminal JSON byte-equal; Conversation head and durable Store byte identity unchanged'

    Enter-Step -Name multiround_chat
    $turn2Message = 'developer-preview-offline-turn-two'
    $turn2 = ConvertFrom-CommandJson -Text (
        Invoke-ToolCommand -Arguments @(
            'chat', '--db', $database, '--artifact-root', $artifacts,
            '--conversation', $conversation, '--conversation-revision', '1',
            '--conversation-head-run', [string]$turn1.run_id,
            '--message', $turn2Message, '--request-id', 'developer-preview-turn-2',
            '--deadline', $deadline
        )
    ).stdout -Label chat_turn_2
    Assert-ChatResult -Result $turn2 -Conversation $conversation -Revision 2 `
        -Message $turn2Message -PreviousRun ([string]$turn1.run_id)
    Set-StepResult -Name multiround_chat -Status PASS -Evidence 'two deterministic Echo turns reached revision=2'

    Enter-Step -Name serve_restart_conversation
    $server1 = Start-ToolServer -Arguments @(
        'serve', '--db', $database, '--artifact-root', $artifacts,
        '--listen', '127.0.0.1:0'
    )
    try {
        $server1Status = Get-DPIJsonField -Object $server1.ready -Name 'status' `
            -Required -Label 'serve readiness'
        $server1Listen = Get-DPIJsonField -Object $server1.ready -Name 'listen' `
            -Required -Label 'serve readiness'
        Assert-StringEqual -Actual $server1Status -Expected 'ready' -Label 'serve status'
        $origin1 = Test-LoopbackOrigin -Origin ('http://' + [string]$server1Listen) `
            -Label 'first Chat listener'
        $client1 = New-LoopbackHttpClient
        try {
            $health1 = Invoke-LoopbackHttp -Client $client1 -Method GET -Uri ($origin1 + '/healthz')
            if ($health1.status_code -ne 200) {
                Throw-Failed -Code 'SERVE_HEALTH_FAILED' -Message 'first serve health check did not return 200'
            }
        } finally { $client1.Dispose() }
    } finally { Stop-ToolServer -Server $server1 }

    $server2 = Start-ToolServer -Arguments @(
        'serve', '--db', $database, '--artifact-root', $artifacts,
        '--listen', '127.0.0.1:0'
    )
    try {
        $server2Listen = Get-DPIJsonField -Object $server2.ready -Name 'listen' `
            -Required -Label 'serve readiness'
        $origin2 = Test-LoopbackOrigin -Origin ('http://' + [string]$server2Listen) `
            -Label 'restarted Chat listener'
        $client2 = New-LoopbackHttpClient
        try {
            $turn3Message = 'developer-preview-offline-turn-after-serve-restart'
            $turn3Body = [ordered]@{
                tenant = 'default'; principal = 'local-operator'; workspace = 'local-chat'
                agent = 'assistant'; profile = 'pure-chat'; message = $turn3Message
                request_id = 'developer-preview-turn-3'; deadline = $deadline
                conversation_id = $conversation; conversation_revision = 2L
                conversation_head_run_id = [string]$turn2.run_id
            } | ConvertTo-Json -Compress
            $turn3Response = Invoke-LoopbackHttp -Client $client2 -Method POST `
                -Uri ($origin2 + '/v1/chat') -Body $turn3Body
            if ($turn3Response.status_code -ne 200) {
                Throw-Failed -Code 'SERVE_CHAT_FAILED' -Message 'post-restart loopback chat did not return 200'
            }
            $turn3 = ConvertFrom-CommandJson -Text $turn3Response.body -Label serve_restart_chat
            Assert-ChatResult -Result $turn3 -Conversation $conversation -Revision 3 `
                -Message $turn3Message -PreviousRun ([string]$turn2.run_id)
        } finally { $client2.Dispose() }
    } finally { Stop-ToolServer -Server $server2 }
    Set-StepResult -Name serve_restart_conversation -Status PASS `
        -Evidence 'loopback health passed; restarted serve continued the Conversation at revision=3'

    Enter-Step -Name backup_verify_restore
    $backup = ConvertFrom-CommandJson -Text (
        Invoke-ToolCommand -Arguments @(
            'backup', '--db', $database, '--artifact-root', $artifacts,
            '--out', $bundle
        ) -TimeoutSeconds ([Math]::Max($CommandTimeoutSeconds, 120))
    ).stdout -Label backup
    if (-not [IO.Directory]::Exists($bundle)) {
        Throw-Failed -Code 'BACKUP_BUNDLE_MISSING' -Message 'backup did not create a bundle directory'
    }
    $verified = ConvertFrom-CommandJson -Text (
        Invoke-ToolCommand -Arguments @('backup-verify', '--bundle', $bundle) `
            -TimeoutSeconds ([Math]::Max($CommandTimeoutSeconds, 120))
    ).stdout -Label backup_verify
    $restored = ConvertFrom-CommandJson -Text (
        Invoke-ToolCommand -Arguments @(
            'restore', '--bundle', $bundle, '--db', $restoredDatabase,
            '--artifact-root', $restoredArtifacts
        ) -TimeoutSeconds ([Math]::Max($CommandTimeoutSeconds, 120))
    ).stdout -Label restore
    if (-not [IO.File]::Exists($restoredDatabase) -or
        -not [IO.Directory]::Exists($restoredArtifacts)) {
        Throw-Failed -Code 'RESTORE_OUTPUT_MISSING' -Message 'restore did not create the new Store and artifact root'
    }
    Set-StepResult -Name backup_verify_restore -Status PASS `
        -Evidence 'backup, offline verify, and restore completed into new paths'

    Enter-Step -Name restored_conversation_continue
    $restoredGet = ConvertFrom-CommandJson -Text (
        Invoke-ToolCommand -Arguments @(
            'conversation-get', '--db', $restoredDatabase,
            '--conversation', $conversation
        )
    ).stdout -Label restored_conversation_get
    $restoredRevision = Get-DPIJsonField -Object $restoredGet -Name 'revision' `
        -Required -Label 'restored_conversation_get'
    $restoredHeadRun = Get-DPIJsonField -Object $restoredGet -Name 'head_run_id' `
        -Required -Label 'restored_conversation_get'
    if ([long]$restoredRevision -ne 3 -or
        [string]$restoredHeadRun -cne [string]$turn3.run_id) {
        Throw-Failed -Code 'RESTORED_CONVERSATION_DRIFT' -Message 'restore did not preserve the Conversation revision and head'
    }
    $turn4Message = 'developer-preview-offline-turn-after-restore'
    $turn4 = ConvertFrom-CommandJson -Text (
        Invoke-ToolCommand -Arguments @(
            'chat', '--db', $restoredDatabase, '--artifact-root', $restoredArtifacts,
            '--conversation', $conversation, '--conversation-revision', '3',
            '--conversation-head-run', [string]$turn3.run_id,
            '--message', $turn4Message, '--request-id', 'developer-preview-turn-4',
            '--deadline', $deadline
        )
    ).stdout -Label restored_chat
    Assert-ChatResult -Result $turn4 -Conversation $conversation -Revision 4 `
        -Message $turn4Message -PreviousRun ([string]$turn3.run_id)
    Set-StepResult -Name restored_conversation_continue -Status PASS `
        -Evidence 'restored Conversation continued from revision=3 to revision=4'

    Enter-Step -Name control_enable
    $handoffPath = Join-Path $script:WorkRoot 'control-handoff.json'
    $controlServer = Start-ToolServer -Arguments @(
        'serve', '--db', $restoredDatabase, '--artifact-root', $restoredArtifacts,
        '--listen', '127.0.0.1:0', '--enable-control',
        '--control-handoff-path', $handoffPath
    )
    try {
        $controlReadiness = Get-DPIJsonField -Object $controlServer.ready -Name 'control' `
            -Required -Label 'control-enabled serve readiness'
        $controlListen = Get-DPIJsonField -Object $controlServer.ready -Name 'listen' `
            -Required -Label 'control-enabled serve readiness'
        Assert-StringEqual -Actual $controlReadiness -Expected 'enabled' `
            -Label 'explicit Control readiness'
        $chatOrigin = Test-LoopbackOrigin `
            -Origin ('http://' + [string]$controlListen) `
            -Label 'Control-enabled Chat listener'
        Wait-ForFile -Path $handoffPath -TimeoutSeconds $ServeStartupTimeoutSeconds
        $handoffRaw = [IO.File]::ReadAllText(
            $handoffPath,
            [Text.UTF8Encoding]::new($false, $true)
        )
        if ($handoffRaw.Length -gt 65536) {
            Throw-Failed -Code 'CONTROL_HANDOFF_OVERSIZE' -Message 'Control handoff exceeded 64 KiB'
        }
        $handoff = ConvertFrom-CommandJson -Text $handoffRaw -Label control_handoff
        $handoffSchema = Get-DPIJsonField -Object $handoff -Name 'schema_version' `
            -Required -Label 'control handoff'
        $handoffCapability = Get-DPIJsonField -Object $handoff -Name 'capability' `
            -Required -Label 'control handoff'
        $handoffOrigin = Get-DPIJsonField -Object $handoff -Name 'origin' `
            -Required -Label 'control handoff'
        Assert-StringEqual -Actual $handoffSchema `
            -Expected 'freeagent.control-bootstrap-handoff/v1' -Label 'Control handoff schema'
        if ([string]::IsNullOrWhiteSpace([string]$handoffCapability)) {
            Throw-Failed -Code 'CONTROL_HANDOFF_INCOMPLETE' -Message 'Control handoff omitted its one-time capability'
        }
        $controlOrigin = Test-LoopbackOrigin -Origin ([string]$handoffOrigin) `
            -Label 'Control listener'
        Set-StepResult -Name control_enable -Status PASS `
            -Evidence 'Control explicitly enabled with an owner handoff'

        Enter-Step -Name loopback_binding
        if ($chatOrigin -ceq $controlOrigin) {
            Throw-Failed -Code 'CONTROL_LISTENER_NOT_DISTINCT' -Message 'Chat and Control must use distinct loopback listeners'
        }
        Set-StepResult -Name loopback_binding -Status PASS `
            -Evidence 'Chat and Control origins are distinct numeric loopback listeners'

        $controlClient = New-LoopbackHttpClient -Cookies
        try {
            $ui = Invoke-LoopbackHttp -Client $controlClient -Method GET `
                -Uri ($controlOrigin + '/control/ui/')
            if ($ui.status_code -ne 200 -or
                -not ([string]$ui.content_type).StartsWith(
                    'text/html', [StringComparison]::OrdinalIgnoreCase
                ) -or
                $ui.body.IndexOf('<div id="root"', [StringComparison]::OrdinalIgnoreCase) -lt 0 -or
                $ui.body.IndexOf(
                    '/control/ui/assets/app.js', [StringComparison]::OrdinalIgnoreCase
                ) -lt 0) {
                Throw-Failed -Code 'CONTROL_UI_INCOMPLETE' -Message 'Control Web shell omitted its root element and app bundle reference'
            }
            $uiScript = Invoke-LoopbackHttp -Client $controlClient -Method GET `
                -Uri ($controlOrigin + '/control/ui/assets/app.js')
            if ($uiScript.status_code -ne 200 -or
                -not ([string]$uiScript.content_type).StartsWith(
                    'text/javascript', [StringComparison]::OrdinalIgnoreCase
                ) -or
                $uiScript.body.IndexOf('Overview', [StringComparison]::Ordinal) -lt 0 -or
                $uiScript.body.IndexOf('Modules', [StringComparison]::Ordinal) -lt 0) {
                Throw-Failed -Code 'CONTROL_UI_INCOMPLETE' -Message 'Control Web app bundle did not expose Overview and Modules navigation'
            }

            $bootstrapBody = [ordered]@{
                capability = [string]$handoffCapability
                schema_version = 'control-bootstrap-exchange/v1'
            } | ConvertTo-Json -Compress
            $bootstrapResponse = Invoke-LoopbackHttp -Client $controlClient -Method POST `
                -Uri ($controlOrigin + '/control/bootstrap') -Body $bootstrapBody `
                -Headers @{ Origin = $controlOrigin }
            if ($bootstrapResponse.status_code -ne 200) {
                Throw-Failed -Code 'CONTROL_BOOTSTRAP_FAILED' -Message 'Control bootstrap exchange did not return 200'
            }
            $bootstrap = ConvertFrom-CommandJson -Text $bootstrapResponse.body `
                -Label control_bootstrap
            $bootstrapSchema = Get-DPIJsonField -Object $bootstrap -Name 'schema_version' `
                -Required -Label 'control bootstrap'
            $bootstrapCSRF = Get-DPIJsonField -Object $bootstrap -Name 'csrf_token' `
                -Required -Label 'control bootstrap'
            Assert-StringEqual -Actual $bootstrapSchema `
                -Expected 'control-bootstrap-session/v2' -Label 'Control bootstrap schema'
            if ([string]::IsNullOrWhiteSpace([string]$bootstrapCSRF)) {
                Throw-Failed -Code 'CONTROL_BOOTSTRAP_INCOMPLETE' -Message 'Control bootstrap omitted the process-local CSRF proof'
            }
            $headers = @{
                'X-FreeAgent-CSRF' = [string]$bootstrapCSRF
                'X-FreeAgent-Scope-Kind' = 'TENANT'
                'X-FreeAgent-Tenant-ID' = 'default'
            }

            Enter-Step -Name control_overview
            $overviewResponse = Invoke-LoopbackHttp -Client $controlClient -Method GET `
                -Uri ($controlOrigin + '/control/api/v1/overview') -Headers $headers
            if ($overviewResponse.status_code -ne 200) {
                Throw-Failed -Code 'CONTROL_OVERVIEW_FAILED' -Message 'authorized Control Overview did not return 200'
            }
            $overview = ConvertFrom-CommandJson -Text $overviewResponse.body `
                -Label control_overview
            $overviewSchema = Get-DPIJsonField -Object $overview -Name 'schema_version' `
                -Required -Label 'control overview'
            Assert-StringEqual -Actual $overviewSchema `
                -Expected 'control-http-overview/v1' -Label 'Control Overview schema'
            Set-StepResult -Name control_overview -Status PASS `
                -Evidence 'authorized read-only Overview returned its exact schema'

            Enter-Step -Name control_modules
            $modulesResponse = Invoke-LoopbackHttp -Client $controlClient -Method GET `
                -Uri ($controlOrigin + '/control/api/v1/modules') -Headers $headers
            if ($modulesResponse.status_code -ne 200) {
                Throw-Failed -Code 'CONTROL_MODULES_FAILED' -Message 'authorized Control Modules did not return 200'
            }
            $modules = ConvertFrom-CommandJson -Text $modulesResponse.body `
                -Label control_modules
            $modulesSchema = Get-DPIJsonField -Object $modules -Name 'schema_version' `
                -Required -Label 'control modules'
            $modulesItems = Get-DPIJsonField -Object $modules -Name 'items' `
                -Required -Label 'control modules'
            Assert-StringEqual -Actual $modulesSchema `
                -Expected 'control-http-modules-page/v1' -Label 'Control Modules schema'
            if ($null -eq $modulesItems) {
                Throw-Failed -Code 'CONTROL_MODULES_SHAPE' -Message 'Control Modules omitted its items array'
            }
            Set-StepResult -Name control_modules -Status PASS `
                -Evidence ('authorized Modules page returned items=' + @($modulesItems).Count)

        } finally { $controlClient.Dispose() }
    } finally { Stop-ToolServer -Server $controlServer }

    $status = 'PASS'
    $exitCode = 0
} catch {
    $caught = $_.Exception
    $contractStatus = [string]$caught.Data['DeveloperPreviewStatus']
    $contractCode = [string]$caught.Data['DeveloperPreviewCode']
    if ($contractStatus -notin @('BLOCKED', 'FAIL')) {
        $contractStatus = 'FAIL'
        $contractCode = 'UNEXPECTED_VALIDATOR_FAILURE'
    }
    $status = $contractStatus
    $exitCode = if ($status -ceq 'BLOCKED') { 2 } else { 1 }
    $failureCode = $contractCode
    $failureMessage = Get-SafeMessage -Text $caught.Message
    if ($script:Steps[$script:CurrentStep].status -ceq 'PENDING') {
        Set-StepResult -Name $script:CurrentStep -Status $status `
            -Evidence ($failureCode + ': ' + $failureMessage)
    }
    foreach ($name in $script:RequiredStepNames) {
        if ($script:Steps[$name].status -ceq 'PENDING') {
            Set-StepResult -Name $name `
                -Status $(if ($status -ceq 'BLOCKED') { 'BLOCKED' } else { 'NOT_RUN' }) `
                -Evidence ('not reached after ' + $script:CurrentStep)
        }
    }
} finally {
    foreach ($server in @($script:ActiveServers)) {
        try { Stop-ToolServer -Server $server } catch { }
    }
    try {
        Remove-IsolatedWorkRoot
    } catch {
        if ($status -ceq 'PASS') {
            $status = 'FAIL'
            $exitCode = 1
            $failureCode = 'ISOLATED_CLEANUP_FAILED'
            $failureMessage = Get-SafeMessage -Text $_.Exception.Message
        }
    }
}

if ($status -ceq 'PASS' -and
    @($script:Steps.Values | Where-Object { $_.status -cne 'PASS' }).Count -ne 0) {
    $status = 'FAIL'
    $exitCode = 1
    $failureCode = 'INCOMPLETE_PASS_REPORT'
    $failureMessage = 'one or more required validation steps did not pass'
}

$report = [pscustomobject][ordered]@{
    schema = $script:Schema
    status = $status
    target_platform = $script:ResolvedTargetPlatform
    package_root = $script:PackageRootPath
    executable = $script:ExecutablePath
    version = $script:VersionText
    offline_only = $true
    external_network_used = $false
    secrets_used = $false
    deepseek_run = $false
    loopback_http_only = $true
    work_directory_removed = [bool]$script:WorkDirectoryRemoved
    work_directory_retained = [bool]$KeepWorkDirectory
    steps = [object[]]@($script:Steps.Values | ForEach-Object { [pscustomobject]$_ })
    blocker_or_failure = if ([string]::IsNullOrEmpty($failureCode)) {
        $null
    } else {
        [pscustomobject][ordered]@{
            code = $failureCode
            message = $failureMessage
        }
    }
}

[Console]::Out.WriteLine(($report | ConvertTo-Json -Depth 12 -Compress))
exit $exitCode
