#requires -Version 7.2

[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$validator = Join-Path $PSScriptRoot 'Test-DeveloperPreviewInstall.ps1'
$testRoot = Join-Path ([IO.Path]::GetTempPath()) `
    ('freeagent-developer-preview-tests-' + [Guid]::NewGuid().ToString('N'))
$pwshPath = (Get-Process -Id $PID).Path
$passed = 0
$failed = 0

function Assert-True {
    param(
        [Parameter(Mandatory = $true)][bool]$Condition,
        [Parameter(Mandatory = $true)][string]$Message
    )

    if (-not $Condition) {
        throw $Message
    }
}

function Assert-Equal {
    param(
        [AllowNull()]$Actual,
        [AllowNull()]$Expected,
        [Parameter(Mandatory = $true)][string]$Message
    )

    if ([string]$Actual -cne [string]$Expected) {
        throw "$Message (actual='$Actual', expected='$Expected')"
    }
}

function Write-Utf8File {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Content
    )

    $parent = [IO.Path]::GetDirectoryName($Path)
    if (-not [IO.Directory]::Exists($parent)) {
        $null = [IO.Directory]::CreateDirectory($parent)
    }
    [IO.File]::WriteAllText($Path, $Content, [Text.UTF8Encoding]::new($false))
}

function New-FakePackage {
    param([Parameter(Mandatory = $true)][string]$Name)

    $root = Join-Path $testRoot $Name
    $null = [IO.Directory]::CreateDirectory($root)
    foreach ($leaf in @(
        'LICENSE', 'THIRD_PARTY_NOTICES.md', 'INSTALL.md', 'QUICKSTART.md',
        'KNOWN_LIMITATIONS.md', 'RELEASE_NOTES.md', 'VERIFY_CHECKSUMS.md',
        'config/README.md', 'data/README.md', 'supply-chain/manifest.json'
    )) {
        Write-Utf8File -Path (Join-Path $root $leaf) -Content "$leaf test fixture`n"
    }
    Write-Utf8File -Path (Join-Path $root 'VERSION') -Content "v0.1.0-dev.1`n"
    Write-Utf8File -Path (Join-Path $root `
        'config/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/module.json') `
        -Content '{"module":"context"}'
    Write-Utf8File -Path (Join-Path $root `
        'config/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/module.json') `
        -Content '{"module":"echo"}'
    $seed = [ordered]@{
        schema_version = 'freeagent.bootstrap-seed/v1'
        seed_id = 'freeagent.local.pure-chat'
        model_binding = [ordered]@{
            config = [ordered]@{
                provider = 'freeagent.local'
                model = 'freeagent-dev-echo'
            }
        }
        module = [ordered]@{
            module_id = 'freeagent.builtin.model.echo'
            artifact_relative_path = 'bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0'
        }
    } | ConvertTo-Json -Depth 8 -Compress
    Write-Utf8File -Path (Join-Path $root 'config/current-v1.bootstrap.seed.json') `
        -Content $seed

    $fakeCli = @'
#requires -Version 7.2
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$argv = [string[]]$args
$packageRoot = $PSScriptRoot
$logPath = Join-Path $packageRoot 'fake-invocations.log'
[IO.File]::AppendAllText($logPath, ([string]::Join(' ', $argv) + "`n"), [Text.UTF8Encoding]::new($false))

foreach ($sensitiveName in @('OPENAI_API_KEY', 'ANTHROPIC_API_KEY', 'FREEAGENT_DEEPSEEK_API_KEY')) {
    if (-not [string]::IsNullOrEmpty([Environment]::GetEnvironmentVariable($sensitiveName))) {
        [Console]::Error.WriteLine('sensitive environment reached fake CLI')
        exit 91
    }
}

function Get-Option {
    param([Parameter(Mandatory = $true)][string]$Name)
    for ($index = 1; $index -lt $argv.Count; $index++) {
        if ($argv[$index] -ceq $Name) {
            if ($index + 1 -ge $argv.Count) { throw "missing value for $Name" }
            return $argv[$index + 1]
        }
    }
    return ''
}

function Test-Option {
    param([Parameter(Mandatory = $true)][string]$Name)
    return $argv -ccontains $Name
}

function Read-State {
    param([Parameter(Mandatory = $true)][string]$Path)
    return ([IO.File]::ReadAllText($Path, [Text.UTF8Encoding]::new($false, $true)) |
        ConvertFrom-Json -AsHashtable -Depth 30)
}

function Write-State {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)]$State
    )
    [IO.File]::WriteAllText(
        $Path,
        ($State | ConvertTo-Json -Depth 30 -Compress),
        [Text.UTF8Encoding]::new($false)
    )
}

function Invoke-EchoTurn {
    param(
        [Parameter(Mandatory = $true)][string]$Database,
        [Parameter(Mandatory = $true)][string]$Conversation,
        [Parameter(Mandatory = $true)][long]$Revision,
        [AllowEmptyString()][string]$Head,
        [Parameter(Mandatory = $true)][string]$Message,
        [Parameter(Mandatory = $true)][string]$RequestId
    )

    $state = Read-State -Path $Database
    if ($state.requests.Contains($RequestId)) {
        if ([IO.File]::Exists((Join-Path $packageRoot 'mutate-on-retry'))) {
            $state.retry_mutation = [long]$state.retry_mutation + 1L
            Write-State -Path $Database -State $state
        }
        return [string]$state.requests[$RequestId]
    }
    if ([string]$state.conversation_id -cne $Conversation -or
        [long]$state.revision -ne $Revision -or
        [string]$state.head_run_id -cne $Head) {
        throw 'conversation compare-and-swap mismatch'
    }
    $newRevision = $Revision + 1L
    $runId = 'fake-run-' + $newRevision
    $response = [ordered]@{
        run_id = $runId
        conversation_id = $Conversation
        conversation_revision = $newRevision
        disposition = 'TERMINATED'
        reply = $Message
    } | ConvertTo-Json -Compress
    $state.revision = $newRevision
    $state.head_run_id = $runId
    $state.requests[$RequestId] = $response
    Write-State -Path $Database -State $state
    return $response
}

function Get-FreePort {
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $listener.Start()
    try { return [int]$listener.LocalEndpoint.Port } finally { $listener.Stop() }
}

function Write-HttpResponse {
    param(
        [Parameter(Mandatory = $true)]$Context,
        [Parameter(Mandatory = $true)][int]$Status,
        [Parameter(Mandatory = $true)][string]$ContentType,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Body,
        [string]$SetCookie = ''
    )
    $bytes = [Text.UTF8Encoding]::new($false).GetBytes($Body)
    $Context.Response.StatusCode = $Status
    $Context.Response.ContentType = $ContentType
    $Context.Response.ContentLength64 = $bytes.LongLength
    if (-not [string]::IsNullOrEmpty($SetCookie)) {
        $Context.Response.Headers.Add('Set-Cookie', $SetCookie)
    }
    $Context.Response.OutputStream.Write($bytes, 0, $bytes.Length)
    $Context.Response.Close()
}

function Read-HttpBody {
    param([Parameter(Mandatory = $true)]$Request)
    $reader = [IO.StreamReader]::new($Request.InputStream, $Request.ContentEncoding)
    try { return $reader.ReadToEnd() } finally { $reader.Dispose() }
}

function Invoke-Serve {
    $database = Get-Option '--db'
    $controlEnabled = Test-Option '--enable-control'
    $chatPort = Get-FreePort
    $chatListener = [Net.HttpListener]::new()
    $chatListener.Prefixes.Add("http://127.0.0.1:$chatPort/")
    $chatListener.Start()
    $controlListener = $null
    $controlPort = 0
    if ($controlEnabled) {
        do { $controlPort = Get-FreePort } while ($controlPort -eq $chatPort)
        $controlListener = [Net.HttpListener]::new()
        $controlListener.Prefixes.Add("http://127.0.0.1:$controlPort/")
        $controlListener.Start()
        $handoff = [ordered]@{
            schema_version = 'freeagent.control-bootstrap-handoff/v1'
            origin = "http://127.0.0.1:$controlPort"
            capability = 'fixture-one-time-capability'
        } | ConvertTo-Json -Compress
        [IO.File]::WriteAllText(
            (Get-Option '--control-handoff-path'),
            $handoff,
            [Text.UTF8Encoding]::new($false)
        )
    }
    $ready = [ordered]@{ status = 'ready'; listen = "127.0.0.1:$chatPort" }
    if ($controlEnabled) { $ready.control = 'enabled' }
    [Console]::Out.WriteLine(($ready | ConvertTo-Json -Compress))
    [Console]::Out.Flush()

    $chatTask = $chatListener.GetContextAsync()
    $controlTask = if ($controlEnabled) { $controlListener.GetContextAsync() } else { $null }
    while ($true) {
        $tasks = if ($controlEnabled) {
            [Threading.Tasks.Task[]]@($chatTask, $controlTask)
        } else {
            [Threading.Tasks.Task[]]@($chatTask)
        }
        $which = [Threading.Tasks.Task]::WaitAny($tasks)
        if ($which -eq 0) {
            $context = $chatTask.GetAwaiter().GetResult()
            $chatTask = $chatListener.GetContextAsync()
            if ($context.Request.HttpMethod -ceq 'GET' -and
                $context.Request.Url.AbsolutePath -ceq '/healthz') {
                Write-HttpResponse -Context $context -Status 200 `
                    -ContentType 'text/plain' -Body 'ok'
                continue
            }
            if ($context.Request.HttpMethod -ceq 'POST' -and
                $context.Request.Url.AbsolutePath -ceq '/v1/chat') {
                $body = Read-HttpBody -Request $context.Request | ConvertFrom-Json -Depth 20
                try {
                    $response = Invoke-EchoTurn -Database $database `
                        -Conversation ([string]$body.conversation_id) `
                        -Revision ([long]$body.conversation_revision) `
                        -Head ([string]$body.conversation_head_run_id) `
                        -Message ([string]$body.message) -RequestId ([string]$body.request_id)
                    Write-HttpResponse -Context $context -Status 200 `
                        -ContentType 'application/json' -Body $response
                } catch {
                    Write-HttpResponse -Context $context -Status 409 `
                        -ContentType 'application/json' -Body '{"error":"conflict"}'
                }
                continue
            }
            Write-HttpResponse -Context $context -Status 404 -ContentType 'text/plain' -Body 'not found'
            continue
        }

        $context = $controlTask.GetAwaiter().GetResult()
        $controlTask = $controlListener.GetContextAsync()
        $path = $context.Request.Url.AbsolutePath
        if ($context.Request.HttpMethod -ceq 'GET' -and $path -ceq '/control/ui/') {
            Write-HttpResponse -Context $context -Status 200 -ContentType 'text/html' `
                -Body '<html><nav>Overview Modules</nav></html>'
        } elseif ($context.Request.HttpMethod -ceq 'POST' -and $path -ceq '/control/bootstrap') {
            $null = Read-HttpBody -Request $context.Request
            $response = '{"schema_version":"control-bootstrap-session/v2","csrf_' +
                'token":"fixture-csrf-proof"}'
            Write-HttpResponse -Context $context -Status 200 -ContentType 'application/json' `
                -Body $response -SetCookie 'freeagent_control_session=fixture; Path=/; HttpOnly; SameSite=Strict'
        } elseif ($context.Request.HttpMethod -ceq 'GET' -and
            $path -ceq '/control/api/v1/overview') {
            Write-HttpResponse -Context $context -Status 200 -ContentType 'application/json' `
                -Body '{"schema_version":"control-http-overview/v1"}'
        } elseif ($context.Request.HttpMethod -ceq 'GET' -and
            $path -ceq '/control/api/v1/modules') {
            Write-HttpResponse -Context $context -Status 200 -ContentType 'application/json' `
                -Body '{"schema_version":"control-http-modules-page/v1","items":[]}'
        } else {
            Write-HttpResponse -Context $context -Status 404 -ContentType 'text/plain' -Body 'not found'
        }
    }
}

if ($argv.Count -eq 1 -and $argv[0] -ceq '--version') {
    if ([IO.File]::Exists((Join-Path $packageRoot 'block-version'))) {
        [Console]::Error.WriteLine('unknown command --version')
        exit 64
    }
    [Console]::Out.WriteLine('freeagent v0.1.0-dev.1 fixture')
    exit 0
}
if ($argv.Count -eq 0) { exit 64 }
$command = $argv[0]
switch ($command) {
    'init' {
        $database = Get-Option '--db'
        $artifactRoot = Get-Option '--artifact-root'
        $null = [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($database))
        $null = [IO.Directory]::CreateDirectory($artifactRoot)
        $state = [ordered]@{
            conversation_id = ''
            revision = 0L
            head_run_id = ''
            retry_mutation = 0L
            requests = [ordered]@{}
        }
        Write-State -Path $database -State $state
        if ([IO.File]::Exists((Join-Path $packageRoot 'duplicate-init-json'))) {
            [Console]::Out.WriteLine('{"initialized":true,"initialized":true}')
        } else {
            [Console]::Out.WriteLine('{"initialized":true}')
        }
    }
    'conversation-create' {
        $database = Get-Option '--db'
        $state = Read-State -Path $database
        $state.conversation_id = Get-Option '--conversation'
        Write-State -Path $database -State $state
        [Console]::Out.WriteLine('{"created":true,"revision":0,"head_run_id":""}')
    }
    'conversation-get' {
        $state = Read-State -Path (Get-Option '--db')
        $result = [ordered]@{
            created = $false
            revision = [long]$state.revision
            head_run_id = [string]$state.head_run_id
        } | ConvertTo-Json -Compress
        [Console]::Out.WriteLine($result)
    }
    'chat' {
        $response = Invoke-EchoTurn -Database (Get-Option '--db') `
            -Conversation (Get-Option '--conversation') `
            -Revision ([long](Get-Option '--conversation-revision')) `
            -Head (Get-Option '--conversation-head-run') `
            -Message (Get-Option '--message') -RequestId (Get-Option '--request-id')
        [Console]::Out.WriteLine($response)
    }
    'serve' { Invoke-Serve }
    'backup' {
        $bundle = Get-Option '--out'
        $null = [IO.Directory]::CreateDirectory($bundle)
        [IO.File]::Copy((Get-Option '--db'), (Join-Path $bundle 'store.json'), $false)
        [Console]::Out.WriteLine('{"backup":true}')
    }
    'backup-verify' {
        if (-not [IO.File]::Exists((Join-Path (Get-Option '--bundle') 'store.json'))) {
            throw 'backup store missing'
        }
        [Console]::Out.WriteLine('{"verified":true}')
    }
    'restore' {
        $database = Get-Option '--db'
        $null = [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($database))
        $null = [IO.Directory]::CreateDirectory((Get-Option '--artifact-root'))
        [IO.File]::Copy((Join-Path (Get-Option '--bundle') 'store.json'), $database, $false)
        [Console]::Out.WriteLine('{"restored":true}')
    }
    default {
        [Console]::Error.WriteLine("unknown command $command")
        exit 64
    }
}
'@
    Write-Utf8File -Path (Join-Path $root 'fake-freeagent.ps1') -Content $fakeCli

    if ($IsWindows) {
        $launcher = Join-Path $root 'bin/freeagent-windows-amd64.cmd'
        $escapedPwsh = $pwshPath.Replace('%', '%%')
        Write-Utf8File -Path $launcher `
            -Content ("@echo off`r`n`"$escapedPwsh`" -NoLogo -NoProfile -File `"%~dp0..\fake-freeagent.ps1`" %*`r`n")
        $relativeLauncher = 'bin/freeagent-windows-amd64.cmd'
        $platform = 'windows-amd64'
    } elseif ($IsLinux) {
        $launcher = Join-Path $root 'bin/freeagent-linux-amd64'
        $escapedPwsh = $pwshPath.Replace(
            [string][char]39,
            ([string][char]39 + '"' + [char]39 + '"' + [char]39)
        )
        Write-Utf8File -Path $launcher `
            -Content ("#!/bin/sh`nexec '$escapedPwsh' -NoLogo -NoProfile -File `"`$(dirname `"`$0`")/../fake-freeagent.ps1`" `"`$@`"`n")
        [IO.File]::SetUnixFileMode($launcher,
            [IO.UnixFileMode]::UserRead -bor [IO.UnixFileMode]::UserWrite -bor
            [IO.UnixFileMode]::UserExecute -bor [IO.UnixFileMode]::GroupRead -bor
            [IO.UnixFileMode]::GroupExecute -bor [IO.UnixFileMode]::OtherRead -bor
            [IO.UnixFileMode]::OtherExecute)
        $relativeLauncher = 'bin/freeagent-linux-amd64'
        $platform = 'linux-amd64'
    } else {
        throw 'unit fixture supports Windows or Linux only'
    }
    return [pscustomobject]@{
        root = $root
        launcher = $relativeLauncher
        platform = $platform
    }
}

function Invoke-Validator {
    param(
        [Parameter(Mandatory = $true)]$Package,
        [Parameter(Mandatory = $true)][string]$WorkParent
    )

    $savedOpenAI = [Environment]::GetEnvironmentVariable('OPENAI_API_KEY')
    try {
        [Environment]::SetEnvironmentVariable(
            'OPENAI_API_KEY',
            'fixture-value-that-must-be-removed'
        )
        $output = & $pwshPath -NoLogo -NoProfile -File $validator `
            -PackageRoot $Package.root -TargetPlatform $Package.platform `
            -ExecutableRelativePath $Package.launcher -WorkParent $WorkParent `
            -CommandTimeoutSeconds 30 -ServeStartupTimeoutSeconds 15 `
            -HttpTimeoutSeconds 10 -ShutdownTimeoutSeconds 5 2>&1
        $exitCode = $LASTEXITCODE
    } finally {
        [Environment]::SetEnvironmentVariable('OPENAI_API_KEY', $savedOpenAI)
    }
    $lines = [string[]]@($output | ForEach-Object { [string]$_ })
    $jsonLine = @($lines | Where-Object { $_.TrimStart().StartsWith('{') })[-1]
    Assert-True -Condition (-not [string]::IsNullOrWhiteSpace($jsonLine)) `
        -Message ('validator did not emit a JSON report: ' + [string]::Join(' | ', $lines))
    return [pscustomobject]@{
        exit_code = $exitCode
        report = ($jsonLine | ConvertFrom-Json -Depth 30)
        output = $lines
    }
}

function Invoke-TestCase {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][scriptblock]$Body
    )

    try {
        & $Body
        $script:passed++
        [Console]::Out.WriteLine("PASS $Name")
    } catch {
        $script:failed++
        [Console]::Error.WriteLine("FAIL $($Name): $($_.Exception.Message)")
    }
}

$null = [IO.Directory]::CreateDirectory($testRoot)
try {
    Invoke-TestCase -Name 'parser and target contracts' -Body {
        $tokens = $null
        $errors = $null
        $null = [Management.Automation.Language.Parser]::ParseFile(
            $validator,
            [ref]$tokens,
            [ref]$errors
        )
        Assert-Equal -Actual @($errors).Count -Expected 0 -Message 'validator parser errors'
        $source = [IO.File]::ReadAllText($validator)
        Assert-True -Condition ($source.Contains('bin/freeagent-windows-amd64.exe')) `
            -Message 'Windows amd64 package binary contract missing'
        Assert-True -Condition ($source.Contains('bin/freeagent-linux-amd64')) `
            -Message 'Linux amd64 package binary contract missing'
        Assert-True -Condition ($source.Contains('config/current-v1.bootstrap.seed.json')) `
            -Message 'packaged bootstrap seed contract missing'
        Assert-True -Condition (-not ($source -match '(?i)\bgo\s+run\b')) `
            -Message 'validator must not invoke source through go run'
    }

    Invoke-TestCase -Name 'complete unpacked offline workflow passes' -Body {
        $package = New-FakePackage -Name 'pass-package'
        $workParent = Join-Path $testRoot 'pass-work'
        $null = [IO.Directory]::CreateDirectory($workParent)
        $result = Invoke-Validator -Package $package -WorkParent $workParent
        $diagnostic = ($result.report | ConvertTo-Json -Depth 20 -Compress) +
            ' output=' + [string]::Join(' | ', $result.output)
        Assert-Equal -Actual $result.exit_code -Expected 0 `
            -Message ('PASS workflow exit code report=' + $diagnostic)
        Assert-Equal -Actual $result.report.status -Expected PASS -Message 'PASS workflow status'
        Assert-Equal -Actual @($result.report.steps).Count -Expected 14 -Message 'required step count'
        Assert-Equal -Actual @($result.report.steps | Where-Object status -cne PASS).Count `
            -Expected 0 -Message 'all required steps must pass'
        Assert-True -Condition ([bool]$result.report.offline_only) -Message 'offline marker missing'
        Assert-True -Condition (-not [bool]$result.report.external_network_used) `
            -Message 'external network must remain unused'
        Assert-True -Condition (-not [bool]$result.report.secrets_used) `
            -Message 'secrets must remain unused'
        Assert-Equal -Actual @(Get-ChildItem -LiteralPath $workParent -Force).Count `
            -Expected 0 -Message 'isolated validation directory leaked'
    }

    Invoke-TestCase -Name 'missing version surface is explicit BLOCKED' -Body {
        $package = New-FakePackage -Name 'blocked-package'
        Write-Utf8File -Path (Join-Path $package.root 'block-version') -Content '1'
        $workParent = Join-Path $testRoot 'blocked-work'
        $null = [IO.Directory]::CreateDirectory($workParent)
        $result = Invoke-Validator -Package $package -WorkParent $workParent
        Assert-Equal -Actual $result.exit_code -Expected 2 -Message 'BLOCKED workflow exit code'
        Assert-Equal -Actual $result.report.status -Expected BLOCKED -Message 'BLOCKED workflow status'
        Assert-Equal -Actual $result.report.blocker_or_failure.code `
            -Expected VERSION_COMMAND_UNAVAILABLE -Message 'BLOCKED reason'
        $invocations = [IO.File]::ReadAllLines((Join-Path $package.root 'fake-invocations.log'))
        Assert-Equal -Actual $invocations.Count -Expected 1 -Message 'BLOCKED run executed extra commands'
        Assert-Equal -Actual $invocations[0] -Expected '--version' -Message 'BLOCKED first command'
    }

    Invoke-TestCase -Name 'duplicate command JSON key fails closed' -Body {
        $package = New-FakePackage -Name 'duplicate-json-package'
        Write-Utf8File -Path (Join-Path $package.root 'duplicate-init-json') -Content '1'
        $workParent = Join-Path $testRoot 'duplicate-json-work'
        $null = [IO.Directory]::CreateDirectory($workParent)
        $result = Invoke-Validator -Package $package -WorkParent $workParent
        Assert-Equal -Actual $result.exit_code -Expected 1 -Message 'duplicate JSON exit code'
        Assert-Equal -Actual $result.report.status -Expected FAIL -Message 'duplicate JSON status'
        Assert-Equal -Actual $result.report.blocker_or_failure.code `
            -Expected COMMAND_JSON_INVALID -Message 'duplicate JSON failure reason'
        $initStep = @($result.report.steps | Where-Object name -ceq 'init')[0]
        Assert-Equal -Actual $initStep.status -Expected FAIL -Message 'duplicate JSON step status'
    }

    Invoke-TestCase -Name 'retry mutation fails closed' -Body {
        $package = New-FakePackage -Name 'retry-fail-package'
        Write-Utf8File -Path (Join-Path $package.root 'mutate-on-retry') -Content '1'
        $workParent = Join-Path $testRoot 'retry-fail-work'
        $null = [IO.Directory]::CreateDirectory($workParent)
        $result = Invoke-Validator -Package $package -WorkParent $workParent
        $diagnostic = ($result.report | ConvertTo-Json -Depth 20 -Compress) +
            ' output=' + [string]::Join(' | ', $result.output)
        Assert-Equal -Actual $result.exit_code -Expected 1 -Message 'retry mutation exit code'
        Assert-Equal -Actual $result.report.status -Expected FAIL -Message 'retry mutation status'
        Assert-Equal -Actual $result.report.blocker_or_failure.code `
            -Expected EXACT_RETRY_MUTATED_STORE `
            -Message ('retry mutation failure reason report=' + $diagnostic)
        $retryStep = @($result.report.steps | Where-Object name -ceq 'exact_retry_attempt_invariance')
        Assert-Equal -Actual $retryStep.Count -Expected 1 -Message 'retry step cardinality'
        Assert-Equal -Actual $retryStep[0].status -Expected FAIL -Message 'retry step status'
        $serveStep = @($result.report.steps | Where-Object name -ceq 'serve_restart_conversation')[0]
        Assert-Equal -Actual $serveStep.status -Expected NOT_RUN `
            -Message 'unreached serve step must not silently pass'
    }
} finally {
    if ([IO.Directory]::Exists($testRoot)) {
        Remove-Item -LiteralPath $testRoot -Recurse -Force
    }
}

[Console]::Out.WriteLine("Developer Preview install tests: passed=$passed failed=$failed")
if ($failed -ne 0) { exit 1 }
exit 0
