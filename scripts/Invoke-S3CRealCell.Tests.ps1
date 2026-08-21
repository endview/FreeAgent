[CmdletBinding()]
param()

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:Runner = Join-Path $PSScriptRoot 'Invoke-S3CRealCell.ps1'
$script:PowerShell = (Get-Process -Id $PID).Path
$script:Utf8NoBom = New-Object Text.UTF8Encoding($false)
$script:Cases = 0
$script:Assertions = 0
$script:Failures = New-Object 'Collections.Generic.List[string]'
$script:SecretName = 'FREEAGENT_S3C_OFFLINE_TEST_KEY'
$script:SecretValue = 'not-a-secret'

function Assert-True {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][bool]$Condition
    )
    $script:Assertions++
    if (-not $Condition) { throw "ASSERT_FAIL $Name" }
}

function Assert-Equal {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [AllowNull()]$Expected,
        [AllowNull()]$Actual
    )
    $script:Assertions++
    if ($Expected -cne $Actual) {
        throw "ASSERT_FAIL $Name expected=[$Expected] actual=[$Actual]"
    }
}

function Invoke-Case {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][scriptblock]$Body
    )
    $script:Cases++
    try {
        & $Body
        Write-Host "PASS $Name"
    } catch {
        $script:Failures.Add("$Name`: $($_.Exception.Message)")
        Write-Host "FAIL $Name`: $($_.Exception.Message)"
    }
}

function ConvertTo-TestWindowsArgument {
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

function Join-TestWindowsArguments {
    param([Parameter(Mandatory = $true)][string[]]$Values)
    return (($Values | ForEach-Object {
        ConvertTo-TestWindowsArgument -Value ([string]$_)
    }) -join ' ')
}

function Get-TestSha256 {
    param([Parameter(Mandatory = $true)][string]$Path)
    $sha = [Security.Cryptography.SHA256]::Create()
    $stream = $null
    try {
        $stream = [IO.File]::OpenRead($Path)
        $bytes = $sha.ComputeHash($stream)
        return ([BitConverter]::ToString($bytes)).Replace('-', '').ToLowerInvariant()
    } finally {
        if ($null -ne $stream) { $stream.Dispose() }
        $sha.Dispose()
    }
}

function Get-TestTreeFingerprint {
    param([Parameter(Mandatory = $true)][string]$Root)
    $lines = New-Object 'Collections.Generic.List[string]'
    foreach ($file in @(Get-ChildItem -LiteralPath $Root -File -Recurse -Force |
        Sort-Object FullName)) {
        $relative = $file.FullName.Substring($Root.Length).
            TrimStart([char]92, [char]47).Replace('\', '/')
        $lines.Add($relative + ':' + (Get-TestSha256 -Path $file.FullName))
    }
    return ($lines -join "`n")
}

function Test-BytesContain {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [byte[]]$Haystack,
        [Parameter(Mandatory = $true)][byte[]]$Needle
    )
    if ($Needle.Length -eq 0 -or $Haystack.Length -lt $Needle.Length) {
        return $false
    }
    for ($offset = 0; $offset -le $Haystack.Length - $Needle.Length; $offset++) {
        $match = $true
        for ($index = 0; $index -lt $Needle.Length; $index++) {
            if ($Haystack[$offset + $index] -ne $Needle[$index]) {
                $match = $false
                break
            }
        }
        if ($match) { return $true }
    }
    return $false
}

function Test-ByteArraysEqual {
    param(
        [Parameter(Mandatory = $true)][byte[]]$Left,
        [Parameter(Mandatory = $true)][byte[]]$Right
    )
    if ($Left.Length -ne $Right.Length) { return $false }
    for ($index = 0; $index -lt $Left.Length; $index++) {
        if ($Left[$index] -ne $Right[$index]) { return $false }
    }
    return $true
}

function New-TestSources {
    param([Parameter(Mandatory = $true)][string]$Root)
    $source = Join-Path $Root 'source'
    $artifactA = Join-Path $source 'artifacts\alpha'
    $artifactB = Join-Path $source 'artifacts\beta'
    foreach ($directory in @($source, $artifactA, $artifactB)) {
        [void][IO.Directory]::CreateDirectory($directory)
    }
    [IO.File]::WriteAllText(
        (Join-Path $artifactA 'module.txt'),
        'alpha-artifact',
        $script:Utf8NoBom
    )
    [IO.File]::WriteAllText(
        (Join-Path $artifactB 'content.txt'),
        'beta-artifact',
        $script:Utf8NoBom
    )
    $seed = Join-Path $source 'seed.json'
    $scenario = Join-Path $source 'scenario.json'
    [IO.File]::WriteAllText(
        $seed,
        '{"schema_version":"freeagent.bootstrap-seed/v1","module":{"artifact_relative_path":"artifacts/alpha"},"nested":[{"artifact_relative_path":"artifacts/beta/content.txt"}]}' + "`n",
        $script:Utf8NoBom
    )
    [IO.File]::WriteAllText(
        $scenario,
        '{"schema_version":"freeagent.s3-eval-scenario/v1","experiment_id":"offline-experiment","agent_id":"offline-agent","profile_id":"offline-profile","tasks":[{"task_id":"one","workspace_id":"ws-one","case_kind":"clean","message":"one"},{"task_id":"two","workspace_id":"ws-two","case_kind":"clean","message":"two"},{"task_id":"three","workspace_id":"ws-three","case_kind":"clean","message":"three"}]}' + "`n",
        $script:Utf8NoBom
    )
    return [pscustomobject]@{
        Root = $source
        Seed = $seed
        Scenario = $scenario
    }
}

function New-TestFakeExecutable {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$OutputPath
    )
    $source = @'
using System;
using System.Collections.Generic;
using System.IO;
using System.Text;

public static class FakeFreeAgent
{
    private static Dictionary<string, string> Options(string[] args)
    {
        var result = new Dictionary<string, string>(StringComparer.Ordinal);
        for (int i = 1; i < args.Length; i++)
        {
            if (!args[i].StartsWith("--", StringComparison.Ordinal)) continue;
            if (i + 1 < args.Length &&
                !args[i + 1].StartsWith("--", StringComparison.Ordinal))
            {
                result[args[i]] = args[++i];
            }
            else
            {
                result[args[i]] = "true";
            }
        }
        return result;
    }

    private static string Get(Dictionary<string, string> values, string name)
    {
        string value;
        if (!values.TryGetValue(name, out value))
            throw new InvalidOperationException("missing option");
        return value;
    }

    private static string Or(
        Dictionary<string, string> values,
        string name,
        string fallback)
    {
        string value;
        return values.TryGetValue(name, out value) ? value : fallback;
    }

    private static string Json(string value)
    {
        if (value == null) return "";
        return value.Replace("\\", "\\\\").Replace("\"", "\\\"");
    }

    private static void Out(string value)
    {
        byte[] bytes = new UTF8Encoding(false).GetBytes(value + "\n");
        using (Stream stream = Console.OpenStandardOutput())
        {
            stream.Write(bytes, 0, bytes.Length);
        }
    }

    private static void Err(string value)
    {
        byte[] bytes = new UTF8Encoding(false).GetBytes(value + "\n");
        using (Stream stream = Console.OpenStandardError())
        {
            stream.Write(bytes, 0, bytes.Length);
        }
    }

    public static int Main(string[] args)
    {
        try
        {
            string invocationLog = Environment.GetEnvironmentVariable(
                "S3C_FAKE_INVOCATION_LOG");
            if (!String.IsNullOrWhiteSpace(invocationLog))
                File.AppendAllText(invocationLog, "invoke\n", new UTF8Encoding(false));
            if (args.Length == 0) return 64;
            string command = args[0];
            var options = Options(args);
            if (command == "init")
            {
                string database = Get(options, "--db");
                string artifacts = Get(options, "--artifact-root");
                Directory.CreateDirectory(Path.GetDirectoryName(database));
                Directory.CreateDirectory(artifacts);
                File.WriteAllText(database, "offline-current-store");
                Out("{\"phase\":\"init\",\"ok\":true}");
                return 0;
            }
            if (command == "s3-eval")
            {
                string database = Get(options, "--db");
                string countPath = database + ".s3-count";
                int count = 0;
                if (File.Exists(countPath))
                    Int32.TryParse(File.ReadAllText(countPath), out count);
                File.WriteAllText(countPath, (count + 1).ToString());
                string mode = Environment.GetEnvironmentVariable("S3C_FAKE_MODE") ?? "success";
                bool scheduler = options.ContainsKey("--enable-fair-scheduler");
                string schedulerJson = "{\"enabled\":" +
                    (scheduler ? "true" : "false") +
                    ",\"global_workers\":" + Or(options, "--scheduler-global-workers", "0") +
                    ",\"workspace_workers\":" + Or(options, "--scheduler-workspace-workers", "0") +
                    ",\"composite_family_workers\":" + Or(options, "--scheduler-family-workers", "0") + "}";
                string reportPrefix =
                    "{\"schema_version\":\"freeagent.s3-eval-report/v1\"," +
                    "\"experiment\":{\"id\":\"offline-experiment\",\"repetitions_requested\":1,\"repetitions_attempted\":1}," +
                    "\"scenario\":{\"schema_version\":\"freeagent.s3-eval-scenario/v1\",\"experiment_id\":\"offline-experiment\",\"agent_id\":\"offline-agent\",\"profile_id\":\"offline-profile\",\"tasks\":[{},{},{}]}," +
                    "\"scheduler\":" + schedulerJson + "," +
                    "\"runtime\":{\"deepseek_enabled\":true},";
                string repetition =
                    "\"repetition_reports\":[{\"repetition\":1,\"report\":{\"families\":[{},{},{}],\"service_order\":[],\"fairness\":{}},\"derived_cost_estimates\":[],";
                if (mode == "partial")
                {
                    Out(reportPrefix + repetition +
                        "\"error\":\"MODEL_UNKNOWN\"}],\"first_error\":\"MODEL_UNKNOWN\"}");
                    return 7;
                }
                if (mode == "leak-exact")
                {
                    string envName = Get(options, "--deepseek-api-key-env");
                    string secret = Environment.GetEnvironmentVariable(envName) ?? "";
                    Out("{\"secret\":\"" + Json(secret) + "\"}");
                    return 9;
                }
                if (mode == "leak-bearer")
                {
                    Out("{\"header\":\"Bearer " + "abcdefghijklmnop1234567890\"}");
                    return 9;
                }
                if (mode == "leak-authorization-stderr")
                {
                    Out("{\"safe\":true}");
                    Err("Authorization: Basic QWxhZGRpbjpvcGVuU2VzYW1l1234");
                    return 9;
                }
                if (mode == "safe-auth-language")
                {
                    Err("Authorization header was present; Bearer redacted");
                    Out(reportPrefix +
                        "\"operator_note\":\"Authorization header was present; Bearer redacted\"," +
                        repetition + "\"error\":\"\"}],\"first_error\":\"\"}");
                    return 0;
                }
                if (mode == "wrong-json")
                {
                    Out("{\"ok\":true}");
                    return 0;
                }
                Out(reportPrefix + repetition +
                    "\"error\":\"\"}],\"first_error\":\"\"}");
                return 0;
            }
            if (command == "backup")
            {
                string bundle = Get(options, "--out");
                Directory.CreateDirectory(bundle);
                File.WriteAllText(Path.Combine(bundle, "manifest.ok"), "verified");
                Out("{\"phase\":\"backup\",\"ok\":true}");
                return 0;
            }
            if (command == "backup-verify")
            {
                string bundle = Get(options, "--bundle");
                if (!File.Exists(Path.Combine(bundle, "manifest.ok"))) return 8;
                Out("{\"phase\":\"backup-verify\",\"ok\":true}");
                return 0;
            }
            return 64;
        }
        catch
        {
            return 70;
        }
    }
}
'@
    Add-Type `
        -TypeDefinition $source `
        -Language CSharp `
        -OutputAssembly $OutputPath `
        -OutputType ConsoleApplication `
        -ErrorAction Stop
}

function Invoke-TestHarness {
    param(
        [Parameter(Mandatory = $true)][string]$FakeExecutable,
        [Parameter(Mandatory = $true)]$Sources,
        [Parameter(Mandatory = $true)][string]$OutputRoot,
        [Parameter(Mandatory = $true)][string]$CellId,
        [Parameter(Mandatory = $true)][string]$Mode,
        [bool]$SecretPresent = $true,
        [ValidateSet('Off', 'On')][string]$SchedulerMode = 'Off',
        [AllowNull()][string]$RunnerPath = $null,
        [AllowNull()][string]$InvocationLogPath = $null
    )
    $selectedRunner = if ([string]::IsNullOrWhiteSpace($RunnerPath)) {
        $script:Runner
    } else {
        $RunnerPath
    }
    $arguments = New-Object 'Collections.Generic.List[string]'
    foreach ($argument in @(
        '-NoLogo', '-NoProfile', '-NonInteractive',
        '-ExecutionPolicy', 'Bypass', '-File', $selectedRunner,
        '-FreeAgentPath', $FakeExecutable,
        '-SeedPath', $Sources.Seed,
        '-ScenarioPath', $Sources.Scenario,
        '-OutputRoot', $OutputRoot,
        '-CellId', $CellId,
        '-SchedulerMode', $SchedulerMode,
        '-Repetitions', '1',
        '-SecretEnvironmentName', $script:SecretName
    )) {
        $arguments.Add([string]$argument)
    }
    if ($SchedulerMode -ieq 'On') {
        foreach ($argument in @(
            '-SchedulerGlobalWorkers', '4',
            '-SchedulerWorkspaceWorkers', '2',
            '-SchedulerFamilyWorkers', '2'
        )) {
            $arguments.Add([string]$argument)
        }
    }
    $savedSecret = [Environment]::GetEnvironmentVariable($script:SecretName, 'Process')
    $savedMode = [Environment]::GetEnvironmentVariable(
        'S3C_FAKE_MODE',
        'Process'
    )
    $savedInvocationLog = [Environment]::GetEnvironmentVariable(
        'S3C_FAKE_INVOCATION_LOG',
        'Process'
    )
    try {
        [Environment]::SetEnvironmentVariable(
            $script:SecretName,
            $(if ($SecretPresent) { $script:SecretValue } else { $null }),
            'Process'
        )
        [Environment]::SetEnvironmentVariable(
            'S3C_FAKE_MODE',
            $Mode,
            'Process'
        )
        [Environment]::SetEnvironmentVariable(
            'S3C_FAKE_INVOCATION_LOG',
            $InvocationLogPath,
            'Process'
        )
        $start = New-Object Diagnostics.ProcessStartInfo
        $start.FileName = $script:PowerShell
        $start.Arguments = Join-TestWindowsArguments -Values $arguments.ToArray()
        $start.UseShellExecute = $false
        $start.CreateNoWindow = $true
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $true
        $process = New-Object Diagnostics.Process
        $process.StartInfo = $start
        if (-not $process.Start()) { throw 'test harness process did not start' }
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        $process.WaitForExit()
        $stdoutTask.Wait()
        $stderrTask.Wait()
        return [pscustomobject]@{
            ExitCode = [int]$process.ExitCode
            Stdout = [string]$stdoutTask.Result
            Stderr = [string]$stderrTask.Result
        }
    } finally {
        if ($null -ne (Get-Variable process -ErrorAction SilentlyContinue) -and
            $null -ne $process) {
            $process.Dispose()
        }
        [Environment]::SetEnvironmentVariable(
            $script:SecretName,
            $savedSecret,
            'Process'
        )
        [Environment]::SetEnvironmentVariable(
            'S3C_FAKE_MODE',
            $savedMode,
            'Process'
        )
        [Environment]::SetEnvironmentVariable(
            'S3C_FAKE_INVOCATION_LOG',
            $savedInvocationLog,
            'Process'
        )
    }
}

if (-not (Test-Path -LiteralPath $script:Runner -PathType Leaf)) {
    throw 'S3C_REAL_CELL_SELFTEST_DEPENDENCY_MISSING'
}
$suiteRoot = Join-Path ([IO.Path]::GetTempPath()) (
    'freeagent s3c real cell tests-' + [Guid]::NewGuid().ToString('N')
)
[void][IO.Directory]::CreateDirectory($suiteRoot)

try {
    $sources = New-TestSources -Root $suiteRoot
    $fakeExecutable = Join-Path $suiteRoot 'fake-freeagent.exe'
    New-TestFakeExecutable -Root $suiteRoot -OutputPath $fakeExecutable
    $outputRoot = Join-Path $suiteRoot 'cells'
    [void][IO.Directory]::CreateDirectory($outputRoot)

    Invoke-Case 'runner is ASCII and parses in Windows PowerShell' {
        $tokens = $null
        $errors = $null
        [void][Management.Automation.Language.Parser]::ParseFile(
            $script:Runner,
            [ref]$tokens,
            [ref]$errors
        )
        Assert-Equal -Name 'parse error count' -Expected 0 -Actual @($errors).Count
        $bytes = [IO.File]::ReadAllBytes($script:Runner)
        Assert-Equal -Name 'non-ASCII byte count' -Expected 0 -Actual (
            @($bytes | Where-Object { $_ -gt 127 }).Count
        )
    }

    Invoke-Case 'successful cell freezes inputs and verifies backup' {
        $result = Invoke-TestHarness `
            -FakeExecutable $fakeExecutable `
            -Sources $sources `
            -OutputRoot $outputRoot `
            -CellId 'success-cell' `
            -Mode 'success' `
            -SchedulerMode 'on'
        Assert-Equal -Name 'success exit' -Expected 0 -Actual $result.ExitCode
        $cell = Join-Path $outputRoot 'success-cell'
        $exit = Get-Content -LiteralPath (Join-Path $cell 'exit.json') -Raw |
            ConvertFrom-Json
        Assert-Equal -Name 'success status' -Expected 'COMPLETE' -Actual $exit.status
        Assert-Equal -Name 'archive verified' -Expected $true `
            -Actual $exit.archive_verified
        Assert-Equal -Name 'automatic retry disabled' -Expected $false `
            -Actual $exit.automatic_retry
        Assert-Equal -Name 'report hash recorded' `
            -Expected (Get-TestSha256 -Path (Join-Path $cell 'report.json')) `
            -Actual $exit.report_sha256
        Assert-Equal -Name 'raw work retained' -Expected $true `
            -Actual $exit.raw_work_retained
        $plan = Get-Content -LiteralPath (Join-Path $cell 'cell-plan.json') -Raw |
            ConvertFrom-Json
        Assert-Equal -Name 'scheduler mode canonical' -Expected 'ON' `
            -Actual $plan.scheduler.mode
        Assert-True -Name 'raw database exists' -Condition (
            Test-Path -LiteralPath (Join-Path $cell 'work\current.sqlite') -PathType Leaf
        )
        Assert-True -Name 'backup marker exists' -Condition (
            Test-Path -LiteralPath (
                Join-Path $cell 'archive\store.bundle\manifest.ok'
            ) -PathType Leaf
        )
        Assert-Equal -Name 'no exit pending remains' -Expected 0 -Actual @(
            Get-ChildItem -LiteralPath $cell -File -Force |
                Where-Object { $_.Name -like '.exit.pending.*' }
        ).Count
        $locks = Get-Content -LiteralPath (Join-Path $cell 'input-locks.json') -Raw |
            ConvertFrom-Json
        Assert-Equal -Name 'binary hash frozen' `
            -Expected (Get-TestSha256 -Path $fakeExecutable) `
            -Actual $locks.binary.sha256
        Assert-Equal -Name 'artifact file count' -Expected 2 -Actual @($locks.artifacts).Count
        Assert-True -Name 'artifact alpha frozen' -Condition (
            Test-Path -LiteralPath (
                Join-Path $cell 'inputs\seed-root\artifacts\alpha\module.txt'
            ) -PathType Leaf
        )
        Assert-True -Name 'artifact beta frozen' -Condition (
            Test-Path -LiteralPath (
                Join-Path $cell 'inputs\seed-root\artifacts\beta\content.txt'
            ) -PathType Leaf
        )
    }

    Invoke-Case 'partial JSON is byte exact and never replayed' {
        $result = Invoke-TestHarness `
            -FakeExecutable $fakeExecutable `
            -Sources $sources `
            -OutputRoot $outputRoot `
            -CellId 'partial-cell' `
            -Mode 'partial'
        Assert-True -Name 'partial wrapper is nonzero' -Condition (
            $result.ExitCode -ne 0
        )
        $cell = Join-Path $outputRoot 'partial-cell'
        $exit = Get-Content -LiteralPath (Join-Path $cell 'exit.json') -Raw |
            ConvertFrom-Json
        Assert-Equal -Name 'partial status' -Expected 'PARTIAL' -Actual $exit.status
        Assert-Equal -Name 'partial phase exit' -Expected 7 `
            -Actual $exit.phases.s3_eval_exit_code
        Assert-Equal -Name 'partial report committed' -Expected $true `
            -Actual $exit.report_committed
        Assert-Equal -Name 'partial archive verified' -Expected $true `
            -Actual $exit.archive_verified
        Assert-Equal -Name 'single model invocation' -Expected '1' -Actual (
            [IO.File]::ReadAllText((Join-Path $cell 'work\current.sqlite.s3-count'))
        )
        $expected = '{"schema_version":"freeagent.s3-eval-report/v1","experiment":{"id":"offline-experiment","repetitions_requested":1,"repetitions_attempted":1},"scenario":{"schema_version":"freeagent.s3-eval-scenario/v1","experiment_id":"offline-experiment","agent_id":"offline-agent","profile_id":"offline-profile","tasks":[{},{},{}]},"scheduler":{"enabled":false,"global_workers":0,"workspace_workers":0,"composite_family_workers":0},"runtime":{"deepseek_enabled":true},"repetition_reports":[{"repetition":1,"report":{"families":[{},{},{}],"service_order":[],"fairness":{}},"derived_cost_estimates":[],"error":"MODEL_UNKNOWN"}],"first_error":"MODEL_UNKNOWN"}' + "`n"
        $actualBytes = [IO.File]::ReadAllBytes((Join-Path $cell 'report.json'))
        $expectedBytes = $script:Utf8NoBom.GetBytes($expected)
        Assert-True -Name 'partial report byte exact' -Condition (
            Test-ByteArraysEqual -Left $expectedBytes -Right $actualBytes
        )
    }

    Invoke-Case 'cell collision changes no existing evidence' {
        $cell = Join-Path $outputRoot 'success-cell'
        $before = Get-TestTreeFingerprint -Root $cell
        $result = Invoke-TestHarness `
            -FakeExecutable $fakeExecutable `
            -Sources $sources `
            -OutputRoot $outputRoot `
            -CellId 'success-cell' `
            -Mode 'partial'
        Assert-True -Name 'collision exit nonzero' -Condition (
            $result.ExitCode -ne 0
        )
        Assert-True -Name 'collision code visible' -Condition (
            $result.Stderr.Contains('code=CELL_COLLISION')
        )
        $after = Get-TestTreeFingerprint -Root $cell
        Assert-Equal -Name 'collision tree unchanged' -Expected $before -Actual $after
        Assert-Equal -Name 'collision did not rerun model' -Expected '1' -Actual (
            [IO.File]::ReadAllText((Join-Path $cell 'work\current.sqlite.s3-count'))
        )
    }

    Invoke-Case 'output inside an artifact tree is rejected before creation' {
        $nestedOutput = Join-Path $sources.Root 'artifacts\alpha\cell-output'
        $result = Invoke-TestHarness `
            -FakeExecutable $fakeExecutable `
            -Sources $sources `
            -OutputRoot $nestedOutput `
            -CellId 'overlap-cell' `
            -Mode 'success'
        Assert-True -Name 'overlap exit nonzero' -Condition (
            $result.ExitCode -ne 0
        )
        Assert-True -Name 'overlap code visible' -Condition (
            $result.Stderr.Contains('code=INPUT_OUTPUT_PATH_OVERLAP')
        )
        Assert-True -Name 'overlap output absent' -Condition (
            -not (Test-Path -LiteralPath $nestedOutput)
        )
    }

    Invoke-Case 'arbitrary JSON cannot become an S3 report' {
        $result = Invoke-TestHarness `
            -FakeExecutable $fakeExecutable `
            -Sources $sources `
            -OutputRoot $outputRoot `
            -CellId 'wrong-json-cell' `
            -Mode 'wrong-json'
        Assert-True -Name 'wrong JSON wrapper nonzero' -Condition (
            $result.ExitCode -ne 0
        )
        $cell = Join-Path $outputRoot 'wrong-json-cell'
        $exit = Get-Content -LiteralPath (Join-Path $cell 'exit.json') -Raw |
            ConvertFrom-Json
        Assert-Equal -Name 'wrong JSON status' -Expected 'NO_VALID_REPORT' `
            -Actual $exit.status
        Assert-Equal -Name 'wrong JSON report uncommitted' -Expected $false `
            -Actual $exit.report_committed
        Assert-True -Name 'wrong JSON report absent' -Condition (
            -not (Test-Path -LiteralPath (Join-Path $cell 'report.json'))
        )
        Assert-True -Name 'wrong JSON raw stdout retained' -Condition (
            Test-Path -LiteralPath (
                Join-Path $cell 'report.stdout.incomplete'
            ) -PathType Leaf
        )
    }

    Invoke-Case 'missing secret fails before cell creation' {
        $result = Invoke-TestHarness `
            -FakeExecutable $fakeExecutable `
            -Sources $sources `
            -OutputRoot $outputRoot `
            -CellId 'missing-secret-cell' `
            -Mode 'success' `
            -SecretPresent $false
        Assert-True -Name 'missing secret exit nonzero' -Condition (
            $result.ExitCode -ne 0
        )
        Assert-True -Name 'missing secret code visible' -Condition (
            $result.Stderr.Contains('code=SECRET_UNAVAILABLE')
        )
        Assert-True -Name 'missing secret cell absent' -Condition (
            -not (Test-Path -LiteralPath (
                Join-Path $outputRoot 'missing-secret-cell'
            ))
        )
    }

    Invoke-Case 'insufficient output space fails before cell and executable' {
        $lowSpaceRunner = Join-Path $suiteRoot 'runner-low-space.ps1'
        $runnerText = [IO.File]::ReadAllText(
            $script:Runner,
            [Text.Encoding]::ASCII
        )
        $availableNeedle = 'return [uint64]$drive.AvailableFreeSpace'
        Assert-Equal -Name 'available space seam appears once' -Expected 1 -Actual (
            ([Text.RegularExpressions.Regex]::Matches(
                $runnerText,
                [Text.RegularExpressions.Regex]::Escape($availableNeedle)
            )).Count
        )
        $lowSpaceText = $runnerText.Replace(
            $availableNeedle,
            'return [uint64]268435456'
        )
        [IO.File]::WriteAllText(
            $lowSpaceRunner,
            $lowSpaceText,
            $script:Utf8NoBom
        )
        $invocationLog = Join-Path $suiteRoot 'low-space-invocations.txt'
        $result = Invoke-TestHarness `
            -FakeExecutable $fakeExecutable `
            -Sources $sources `
            -OutputRoot $outputRoot `
            -CellId 'low-space-cell' `
            -Mode 'success' `
            -RunnerPath $lowSpaceRunner `
            -InvocationLogPath $invocationLog
        Assert-True -Name 'low space exit nonzero' -Condition (
            $result.ExitCode -ne 0
        )
        Assert-True -Name 'low space code visible' -Condition (
            $result.Stderr.Contains('code=OUTPUT_SPACE_INSUFFICIENT')
        )
        Assert-True -Name 'low space cell absent' -Condition (
            -not (Test-Path -LiteralPath (Join-Path $outputRoot 'low-space-cell'))
        )
        Assert-True -Name 'low space executable never invoked' -Condition (
            -not (Test-Path -LiteralPath $invocationLog)
        )
    }

    Invoke-Case 'secret embedded in an input fails before cell creation' {
        $originalScenario = [IO.File]::ReadAllText($sources.Scenario)
        try {
            [IO.File]::WriteAllText(
                $sources.Scenario,
                '{"experiment_id":"offline-experiment","embedded":"' +
                    $script:SecretValue + '"}' + "`n",
                $script:Utf8NoBom
            )
            $result = Invoke-TestHarness `
                -FakeExecutable $fakeExecutable `
                -Sources $sources `
                -OutputRoot $outputRoot `
                -CellId 'input-secret-cell' `
                -Mode 'success'
        } finally {
            [IO.File]::WriteAllText(
                $sources.Scenario,
                $originalScenario,
                $script:Utf8NoBom
            )
        }
        Assert-True -Name 'input secret exit nonzero' -Condition (
            $result.ExitCode -ne 0
        )
        Assert-True -Name 'input secret not printed' -Condition (
            -not $result.Stderr.Contains($script:SecretValue)
        )
        Assert-True -Name 'input secret cell absent' -Condition (
            -not (Test-Path -LiteralPath (Join-Path $outputRoot 'input-secret-cell'))
        )
    }

    Invoke-Case 'exact secret leak blocks capture without printing match' {
        $result = Invoke-TestHarness `
            -FakeExecutable $fakeExecutable `
            -Sources $sources `
            -OutputRoot $outputRoot `
            -CellId 'leak-cell' `
            -Mode 'leak-exact'
        Assert-True -Name 'leak exit nonzero' -Condition ($result.ExitCode -ne 0)
        Assert-True -Name 'secret absent from wrapper stdout' -Condition (
            -not $result.Stdout.Contains($script:SecretValue)
        )
        Assert-True -Name 'secret absent from wrapper stderr' -Condition (
            -not $result.Stderr.Contains($script:SecretValue)
        )
        $cell = Join-Path $outputRoot 'leak-cell'
        $exit = Get-Content -LiteralPath (Join-Path $cell 'exit.json') -Raw |
            ConvertFrom-Json
        Assert-Equal -Name 'leak status' -Expected 'SECRET_LEAK_BLOCKED' `
            -Actual $exit.status
        Assert-True -Name 'leaking report absent' -Condition (
            -not (Test-Path -LiteralPath (Join-Path $cell 'report.json'))
        )
        Assert-Equal -Name 'pending captures absent' -Expected 0 -Actual @(
            Get-ChildItem -LiteralPath $cell -File -Force |
                Where-Object { $_.Name -like '*.pending.*' }
        ).Count
        [byte[]]$fixtureBytes = $script:Utf8NoBom.GetBytes($script:SecretValue)
        foreach ($file in @(Get-ChildItem -LiteralPath $cell -File -Recurse -Force)) {
            Assert-True -Name ('secret absent from ' + $file.Name) -Condition (
                -not (Test-BytesContain `
                    -Haystack ([IO.File]::ReadAllBytes($file.FullName)) `
                    -Needle $fixtureBytes)
            )
        }
        Assert-True -Name 'leak raw database retained' -Condition (
            Test-Path -LiteralPath (Join-Path $cell 'work\current.sqlite') -PathType Leaf
        )
        Assert-Equal -Name 'leak model invocation count' -Expected '1' -Actual (
            [IO.File]::ReadAllText((Join-Path $cell 'work\current.sqlite.s3-count'))
        )
    }

    Invoke-Case 'credential-like Bearer value blocks stdout capture' {
        $result = Invoke-TestHarness `
            -FakeExecutable $fakeExecutable `
            -Sources $sources `
            -OutputRoot $outputRoot `
            -CellId 'bearer-marker-cell' `
            -Mode 'leak-bearer'
        Assert-True -Name 'Bearer credential exit nonzero' -Condition (
            $result.ExitCode -ne 0
        )
        $cell = Join-Path $outputRoot 'bearer-marker-cell'
        $exit = Get-Content -LiteralPath (Join-Path $cell 'exit.json') -Raw |
            ConvertFrom-Json
        Assert-Equal -Name 'Bearer credential status' `
            -Expected 'SECRET_LEAK_BLOCKED' `
            -Actual $exit.status
        Assert-True -Name 'Bearer stdout not committed' -Condition (
            -not (Test-Path -LiteralPath (Join-Path $cell 'report.json'))
        )
        Assert-Equal -Name 'Bearer pending absent' -Expected 0 -Actual @(
            Get-ChildItem -LiteralPath $cell -File -Force |
                Where-Object { $_.Name -like '*.pending.*' }
        ).Count
    }

    Invoke-Case 'credential-like Authorization value blocks stderr capture' {
        $result = Invoke-TestHarness `
            -FakeExecutable $fakeExecutable `
            -Sources $sources `
            -OutputRoot $outputRoot `
            -CellId 'authorization-marker-cell' `
            -Mode 'leak-authorization-stderr'
        Assert-True -Name 'Authorization credential exit nonzero' -Condition (
            $result.ExitCode -ne 0
        )
        $cell = Join-Path $outputRoot 'authorization-marker-cell'
        $exit = Get-Content -LiteralPath (Join-Path $cell 'exit.json') -Raw |
            ConvertFrom-Json
        Assert-Equal -Name 'Authorization credential status' `
            -Expected 'SECRET_LEAK_BLOCKED' `
            -Actual $exit.status
        Assert-True -Name 'Authorization stderr not committed' -Condition (
            -not (Test-Path -LiteralPath (
                Join-Path $cell 's3-eval.stderr.txt'
            ))
        )
        Assert-Equal -Name 'Authorization pending absent' -Expected 0 -Actual @(
            Get-ChildItem -LiteralPath $cell -File -Force |
                Where-Object { $_.Name -like '*.pending.*' }
        ).Count
    }

    Invoke-Case 'safe Authorization and redacted Bearer language is retained' {
        $result = Invoke-TestHarness `
            -FakeExecutable $fakeExecutable `
            -Sources $sources `
            -OutputRoot $outputRoot `
            -CellId 'safe-auth-language-cell' `
            -Mode 'safe-auth-language'
        Assert-Equal -Name 'safe auth language exit' -Expected 0 `
            -Actual $result.ExitCode
        $cell = Join-Path $outputRoot 'safe-auth-language-cell'
        $exit = Get-Content -LiteralPath (Join-Path $cell 'exit.json') -Raw |
            ConvertFrom-Json
        Assert-Equal -Name 'safe auth language status' -Expected 'COMPLETE' `
            -Actual $exit.status
        Assert-True -Name 'safe auth report retained' -Condition (
            Test-Path -LiteralPath (Join-Path $cell 'report.json') -PathType Leaf
        )
        Assert-True -Name 'safe auth stderr retained' -Condition (
            [IO.File]::ReadAllText(
                (Join-Path $cell 's3-eval.stderr.txt')
            ).Contains('Authorization header was present')
        )
        Assert-Equal -Name 'safe auth archive verified' -Expected $true `
            -Actual $exit.archive_verified
    }
} finally {
    if ($env:FREEAGENT_S3C_TEST_KEEP -ceq '1') {
        Write-Host ('S3C_REAL_CELL_TEST_FIXTURE ' + $suiteRoot)
    } elseif (Test-Path -LiteralPath $suiteRoot -PathType Container) {
        $resolvedSuite = [IO.Path]::GetFullPath($suiteRoot)
        $resolvedTemp = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).
            TrimEnd([char]92, [char]47) + [IO.Path]::DirectorySeparatorChar
        if (-not $resolvedSuite.StartsWith(
            $resolvedTemp,
            [StringComparison]::OrdinalIgnoreCase
        )) {
            throw 'S3C_REAL_CELL_SELFTEST_CLEANUP_BOUNDARY'
        }
        Get-ChildItem -LiteralPath $suiteRoot -Recurse -Force |
            ForEach-Object {
                try { $_.Attributes = [IO.FileAttributes]::Normal } catch {}
            }
        Remove-Item -LiteralPath $suiteRoot -Recurse -Force
    }
}

if ($script:Failures.Count -gt 0) {
    $script:Failures | ForEach-Object { Write-Error $_ -ErrorAction Continue }
    throw (
        'S3C_REAL_CELL_SELFTEST_FAIL ' +
        "cases=$($script:Cases) assertions=$($script:Assertions) " +
        "failures=$($script:Failures.Count)"
    )
}

Write-Host (
    'S3C_REAL_CELL_SELFTEST_PASS ' +
    "cases=$($script:Cases) assertions=$($script:Assertions)"
)
