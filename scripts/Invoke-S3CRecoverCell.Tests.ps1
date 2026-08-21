[CmdletBinding()]
param()

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:Runner = Join-Path $PSScriptRoot 'Invoke-S3CRecoverCell.ps1'
$script:PowerShell = (Get-Process -Id $PID).Path
$script:Utf8 = New-Object Text.UTF8Encoding($false)
$script:Cases = 0
$script:Assertions = 0
$script:Skips = 0
$script:Failures = New-Object 'Collections.Generic.List[string]'
$script:SecretName = 'FREEAGENT_RECOVERY_TEST_KEY'
$script:SecretValue = 'not-a-secret'
$script:Junctions = New-Object 'Collections.Generic.List[string]'

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
        if ($_.Exception.Message.StartsWith(
            'S3C_RECOVERY_SELFTEST_SKIP ',
            [StringComparison]::Ordinal
        )) {
            $script:Skips++
            Write-Host (
                'SKIP ' + $Name + ': ' +
                $_.Exception.Message.Substring(
                    'S3C_RECOVERY_SELFTEST_SKIP '.Length
                )
            )
            return
        }
        $script:Failures.Add("$Name`: $($_.Exception.Message)")
        Write-Host "FAIL $Name`: $($_.Exception.Message)"
    }
}

function Skip-Case {
    param([Parameter(Mandatory = $true)][string]$Reason)
    throw ('S3C_RECOVERY_SELFTEST_SKIP ' + $Reason)
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
        $stream = [IO.File]::Open(
            $Path,
            [IO.FileMode]::Open,
            [IO.FileAccess]::Read,
            [IO.FileShare]::ReadWrite
        )
        return ([BitConverter]::ToString($sha.ComputeHash($stream))).
            Replace('-', '').ToLowerInvariant()
    } finally {
        if ($null -ne $stream) { $stream.Dispose() }
        $sha.Dispose()
    }
}

function Get-TestTreeFingerprint {
    param([Parameter(Mandatory = $true)][string]$Root)
    $lines = New-Object 'Collections.Generic.List[string]'
    $stack = New-Object 'Collections.Generic.Stack[string]'
    $stack.Push($Root)
    while ($stack.Count -gt 0) {
        $directory = $stack.Pop()
        foreach ($item in (New-Object IO.DirectoryInfo($directory)).
            EnumerateFileSystemInfos()) {
            $relative = $item.FullName.Substring($Root.Length).
                TrimStart([char]92, [char]47).Replace('\', '/')
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                $lines.Add('REPARSE:' + $relative)
            } elseif ($item -is [IO.DirectoryInfo]) {
                $stack.Push($item.FullName)
            } elseif ($item -is [IO.FileInfo]) {
                $lines.Add(
                    $relative + ':' + $item.Length + ':' +
                    (Get-TestSha256 -Path $item.FullName)
                )
            }
        }
    }
    return (@($lines | Sort-Object) -join "`n")
}

function Write-TestJson {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)]$Value
    )
    $json = $Value | ConvertTo-Json -Depth 20 -Compress
    [IO.File]::WriteAllText($Path, $json + "`n", $script:Utf8)
}

function New-TestFakeExecutable {
    param([Parameter(Mandatory = $true)][string]$OutputPath)
    $source = @'
using System;
using System.Collections.Generic;
using System.IO;
using System.Text;

public static class FakeRecoveryFreeAgent
{
    private const string SecretName = "FREEAGENT_RECOVERY_TEST_KEY";
    private const string DatabaseSha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
    private const string ManifestSha = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";

    private static Dictionary<string, string> Options(string[] args)
    {
        var result = new Dictionary<string, string>(StringComparer.Ordinal);
        for (int i = 1; i < args.Length; i++)
        {
            if (!args[i].StartsWith("--", StringComparison.Ordinal)) continue;
            if (i + 1 >= args.Length) throw new InvalidOperationException();
            result[args[i]] = args[++i];
        }
        return result;
    }

    private static string Get(Dictionary<string, string> values, string key)
    {
        string value;
        if (!values.TryGetValue(key, out value))
            throw new InvalidOperationException("missing option");
        return value;
    }

    private static string Json(string value)
    {
        return value.Replace("\\", "\\\\").Replace("\"", "\\\"");
    }

    private static void Out(string value)
    {
        byte[] bytes = new UTF8Encoding(false).GetBytes(value + "\n");
        using (Stream output = Console.OpenStandardOutput())
            output.Write(bytes, 0, bytes.Length);
    }

    private static void Log(string command)
    {
        string path = Environment.GetEnvironmentVariable("S3C_RECOVERY_TEST_LOG");
        if (!String.IsNullOrEmpty(path))
        {
            string secret = Environment.GetEnvironmentVariable(SecretName);
            File.AppendAllText(
                path,
                command + "|credential_available=" +
                    (!String.IsNullOrEmpty(secret) ? "true" : "false") + "\n",
                new UTF8Encoding(false));
        }
    }

    private static string Manifest()
    {
        return "{\"format_version\":\"freeagent.current-store-backup/v1\"," +
            "\"created_at\":\"2026-08-05T00:00:00Z\"," +
            "\"tool_version\":\"recovery-test\"," +
            "\"database\":{\"path\":\"database.sqlite\",\"sha256\":\"" +
                DatabaseSha + "\",\"size_bytes\":17}," +
            "\"store_identity\":{\"application_id\":1178682161," +
                "\"user_version\":1,\"schema_identity\":\"test-schema\"," +
                "\"schema_fingerprint\":\"" + DatabaseSha + "\"," +
                "\"generator_id\":\"test-generator\"," +
                "\"store_instance_id\":\"test-store-instance\"}," +
            "\"current\":[],\"artifacts\":[],\"artifact_count\":0," +
            "\"attempt_counts\":{\"model_pending\":0,\"model_unknown\":1," +
                "\"action_pending\":0,\"action_unknown\":0," +
                "\"channel_ingress_receipts\":0,\"channel_cursor_scopes\":0," +
                "\"channel_send_pending\":0,\"channel_send_unknown\":0}," +
            "\"manifest_digest\":\"" + ManifestSha + "\"}";
    }

    public static int Main(string[] args)
    {
        try
        {
            if (args.Length == 0) return 64;
            string command = args[0];
            Log(command);
            var options = Options(args);
            string mode = Environment.GetEnvironmentVariable(
                "S3C_RECOVERY_TEST_MODE") ?? "success";
            if (command == "backup")
            {
                if (mode == "backup-fail") return 7;
                string database = Get(options, "--db");
                string artifacts = Get(options, "--artifact-root");
                string bundle = Get(options, "--out");
                if (!File.Exists(database) || !Directory.Exists(artifacts) ||
                    !File.Exists(database + ".freeagent.owner.lock")) return 72;
                Directory.CreateDirectory(bundle);
                File.WriteAllText(
                    Path.Combine(bundle, "manifest.json"),
                    Manifest(),
                    new UTF8Encoding(false));
                Out("{\"bundle\":\"" + Json(Path.GetFullPath(bundle)) +
                    "\",\"manifest\":" + Manifest() + "}");
                return 0;
            }
            if (command == "backup-verify")
            {
                string bundle = Get(options, "--bundle");
                if (!File.Exists(Path.Combine(bundle, "manifest.json"))) return 8;
                Out("{\"bundle\":\"" + Json(Path.GetFullPath(bundle)) +
                    "\",\"manifest\":" + Manifest() + "}");
                return 0;
            }
            string modelLog = Environment.GetEnvironmentVariable(
                "S3C_RECOVERY_MODEL_LOG");
            if (!String.IsNullOrEmpty(modelLog))
                File.AppendAllText(modelLog, command + "\n");
            return 91;
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

function New-TestSourceCell {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$CellId,
        [Parameter(Mandatory = $true)][string]$FakeExecutable,
        [string]$Status = 'SECRET_LEAK_BLOCKED'
    )
    $cell = Join-Path $Root $CellId
    $binaryRoot = Join-Path $cell 'inputs\bin'
    $artifactRoot = Join-Path $cell 'work\artifacts\nested'
    foreach ($directory in @($cell, $binaryRoot, $artifactRoot)) {
        [void][IO.Directory]::CreateDirectory($directory)
    }
    $frozenBinary = Join-Path $binaryRoot 'fake-freeagent.exe'
    [IO.File]::Copy($FakeExecutable, $frozenBinary, $false)
    [IO.File]::WriteAllText(
        (Join-Path $cell 'work\current.sqlite'),
        'offline-current-store',
        $script:Utf8
    )
    [IO.File]::WriteAllBytes(
        (Join-Path $cell 'work\current.sqlite.freeagent.owner.lock'),
        [byte[]]@()
    )
    [IO.File]::WriteAllText(
        (Join-Path $cell 'work\artifacts\root.txt'),
        'root-artifact',
        $script:Utf8
    )
    [IO.File]::WriteAllText(
        (Join-Path $artifactRoot 'child.txt'),
        'nested-artifact',
        $script:Utf8
    )
    $binaryItem = Get-Item -LiteralPath $frozenBinary
    Write-TestJson -Path (Join-Path $cell 'input-locks.json') -Value ([ordered]@{
        schema_version = 'freeagent.s3c-input-locks/v1'
        binary = [ordered]@{
            relative_path = 'inputs/bin/fake-freeagent.exe'
            size_bytes = $binaryItem.Length
            sha256 = Get-TestSha256 -Path $frozenBinary
        }
        seed = [ordered]@{ relative_path = 'inputs/seed-root/seed.json' }
        scenario = [ordered]@{ relative_path = 'inputs/scenario.json' }
        artifacts = @()
    })
    Write-TestJson -Path (Join-Path $cell 'cell-plan.json') -Value ([ordered]@{
        schema_version = 'freeagent.s3c-cell-plan/v1'
        cell_id = $CellId
        created_at_utc = '2026-08-05T00:00:00.0000000+00:00'
        secret_environment_name = $script:SecretName
        automatic_retry = $false
        paths = [ordered]@{
            database = 'work/current.sqlite'
            artifact_root = 'work/artifacts'
            backup_bundle = 'archive/store.bundle'
        }
    })
    Write-TestJson -Path (Join-Path $cell 'exit.json') -Value ([ordered]@{
        schema_version = 'freeagent.s3c-real-cell-exit/v1'
        cell_id = $CellId
        started_at_utc = '2026-08-05T00:00:00.0000000+00:00'
        finished_at_utc = '2026-08-05T00:01:00.0000000+00:00'
        status = $Status
        report_committed = $false
        archive_verified = $false
        automatic_retry = $false
        manual_review_required = $true
        raw_work_retained = $true
        sensitive_capture_cleanup_failed = $false
    })
    return $cell
}

function Invoke-TestRecovery {
    param(
        [Parameter(Mandatory = $true)][string]$SourceCell,
        [Parameter(Mandatory = $true)][string]$OutputRoot,
        [Parameter(Mandatory = $true)][string]$RecoveryId,
        [string]$Mode = 'success'
    )
    $log = Join-Path $script:SuiteRoot (
        'calls-' + [Guid]::NewGuid().ToString('N') + '.txt'
    )
    $modelLog = Join-Path $script:SuiteRoot (
        'model-' + [Guid]::NewGuid().ToString('N') + '.txt'
    )
    [IO.File]::WriteAllText($log, '', $script:Utf8)
    [IO.File]::WriteAllText($modelLog, '', $script:Utf8)
    $arguments = @(
        '-NoLogo', '-NoProfile', '-NonInteractive',
        '-ExecutionPolicy', 'Bypass', '-File', $script:Runner,
        '-SourceCellPath', $SourceCell,
        '-OutputRoot', $OutputRoot,
        '-RecoveryId', $RecoveryId
    )
    $saved = @{}
    foreach ($name in @(
        'S3C_RECOVERY_TEST_LOG', 'S3C_RECOVERY_MODEL_LOG',
        'S3C_RECOVERY_TEST_MODE', $script:SecretName
    )) {
        $saved[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
    }
    $process = $null
    try {
        [Environment]::SetEnvironmentVariable(
            'S3C_RECOVERY_TEST_LOG', $log, 'Process'
        )
        [Environment]::SetEnvironmentVariable(
            'S3C_RECOVERY_MODEL_LOG', $modelLog, 'Process'
        )
        [Environment]::SetEnvironmentVariable(
            'S3C_RECOVERY_TEST_MODE', $Mode, 'Process'
        )
        [Environment]::SetEnvironmentVariable(
            $script:SecretName, $script:SecretValue, 'Process'
        )
        $start = New-Object Diagnostics.ProcessStartInfo
        $start.FileName = $script:PowerShell
        $start.Arguments = Join-TestWindowsArguments -Values $arguments
        $start.UseShellExecute = $false
        $start.CreateNoWindow = $true
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $true
        $process = New-Object Diagnostics.Process
        $process.StartInfo = $start
        if (-not $process.Start()) { throw 'recovery runner did not start' }
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        $process.WaitForExit()
        $stdoutTask.Wait()
        $stderrTask.Wait()
        return [pscustomobject]@{
            ExitCode = [int]$process.ExitCode
            Stdout = [string]$stdoutTask.Result
            Stderr = [string]$stderrTask.Result
            Calls = @([IO.File]::ReadAllLines($log))
            ModelCalls = @([IO.File]::ReadAllLines($modelLog) | Where-Object {
                -not [string]::IsNullOrWhiteSpace($_)
            })
        }
    } finally {
        if ($null -ne $process) { $process.Dispose() }
        foreach ($name in $saved.Keys) {
            [Environment]::SetEnvironmentVariable(
                [string]$name, $saved[$name], 'Process'
            )
        }
    }
}

if (-not (Test-Path -LiteralPath $script:Runner -PathType Leaf)) {
    throw 'S3C_RECOVERY_SELFTEST_DEPENDENCY_MISSING'
}
$script:SuiteRoot = Join-Path ([IO.Path]::GetTempPath()) (
    'freeagent s3c recovery tests-' + [Guid]::NewGuid().ToString('N')
)
[void][IO.Directory]::CreateDirectory($script:SuiteRoot)

try {
    $fakeExecutable = Join-Path $script:SuiteRoot 'fake-freeagent.exe'
    New-TestFakeExecutable -OutputPath $fakeExecutable
    $sourceRoot = Join-Path $script:SuiteRoot 'source cells'
    $outputRoot = Join-Path $script:SuiteRoot 'recoveries'
    [void][IO.Directory]::CreateDirectory($sourceRoot)
    [void][IO.Directory]::CreateDirectory($outputRoot)

    Invoke-Case 'runner is ASCII and parses in Windows PowerShell' {
        $tokens = $null
        $errors = $null
        [void][Management.Automation.Language.Parser]::ParseFile(
            $script:Runner,
            [ref]$tokens,
            [ref]$errors
        )
        Assert-Equal -Name 'parse errors' -Expected 0 -Actual @($errors).Count
        $bytes = [IO.File]::ReadAllBytes($script:Runner)
        Assert-Equal -Name 'non ASCII bytes' -Expected 0 -Actual @(
            $bytes | Where-Object { $_ -gt 127 }
        ).Count
    }

    Invoke-Case 'non-complete source is cloned and bundle verified' {
        $source = New-TestSourceCell `
            -Root $sourceRoot `
            -CellId 'failed-success-source' `
            -FakeExecutable $fakeExecutable
        $before = Get-TestTreeFingerprint -Root $source
        $result = Invoke-TestRecovery `
            -SourceCell $source `
            -OutputRoot $outputRoot `
            -RecoveryId 'recovery-success'
        Assert-Equal -Name 'success exit' -Expected 0 -Actual $result.ExitCode
        Assert-Equal -Name 'source bytes unchanged' -Expected $before `
            -Actual (Get-TestTreeFingerprint -Root $source)
        Assert-Equal -Name 'only backup and verify called' -Expected 2 `
            -Actual @($result.Calls).Count
        Assert-True -Name 'backup call recorded' -Condition (
            $result.Calls[0].StartsWith('backup|')
        )
        Assert-True -Name 'verify call recorded' -Condition (
            $result.Calls[1].StartsWith('backup-verify|')
        )
        Assert-True -Name 'secret absent from backup child' -Condition (
            $result.Calls[0].EndsWith('credential_available=false')
        )
        Assert-True -Name 'secret absent from verify child' -Condition (
            $result.Calls[1].EndsWith('credential_available=false')
        )
        Assert-Equal -Name 'zero model calls' -Expected 0 `
            -Actual @($result.ModelCalls).Count
        $recovery = Join-Path $outputRoot 'recovery-success'
        $record = Get-Content -LiteralPath (Join-Path $recovery 'recovery.json') `
            -Raw | ConvertFrom-Json
        Assert-Equal -Name 'recovery status' -Expected 'COMPLETE' `
            -Actual $record.status
        Assert-Equal -Name 'semantic replay false' -Expected $false `
            -Actual $record.semantic_replay
        Assert-Equal -Name 'report recovered false' -Expected $false `
            -Actual $record.report_recovered
        Assert-Equal -Name 'source status bound' `
            -Expected 'SECRET_LEAK_BLOCKED' `
            -Actual $record.source_cell.terminal_status
        Assert-Equal -Name 'source unchanged recorded' -Expected $true `
            -Actual $record.source_cell.unchanged
        Assert-Equal -Name 'copy lock count' -Expected 6 `
            -Actual @($record.copy_verification).Count
        Assert-Equal -Name 'clone lock count' -Expected 7 `
            -Actual @($record.clone_locks).Count
        Assert-Equal -Name 'bundle verified' -Expected $true `
            -Actual $record.bundle_verification.verified
        Assert-True -Name 'clone database byte exact' -Condition (
            (Get-TestSha256 -Path (Join-Path $source 'work\current.sqlite')) -ceq
            (Get-TestSha256 -Path (Join-Path $recovery 'raw-clone\current.sqlite'))
        )
        Assert-True -Name 'single terminal record' -Condition (
            @(Get-ChildItem -LiteralPath $recovery -File -Force |
                Where-Object { $_.Name -like '*recovery*.json' }).Count -eq 1
        )
    }

    Invoke-Case 'overlapping output is rejected without writes' {
        $source = New-TestSourceCell `
            -Root $sourceRoot `
            -CellId 'overlap-source' `
            -FakeExecutable $fakeExecutable
        $before = Get-TestTreeFingerprint -Root $source
        $nestedOutput = Join-Path $source 'nested-output'
        $result = Invoke-TestRecovery `
            -SourceCell $source `
            -OutputRoot $nestedOutput `
            -RecoveryId 'overlap-recovery'
        Assert-True -Name 'overlap exit nonzero' -Condition (
            $result.ExitCode -ne 0
        )
        Assert-True -Name 'overlap code visible' -Condition (
            $result.Stderr.Contains('code=SOURCE_OUTPUT_PATH_OVERLAP')
        )
        Assert-True -Name 'overlap output absent' -Condition (
            -not (Test-Path -LiteralPath $nestedOutput)
        )
        Assert-Equal -Name 'overlap source unchanged' -Expected $before `
            -Actual (Get-TestTreeFingerprint -Root $source)
        Assert-Equal -Name 'overlap model calls zero' -Expected 0 `
            -Actual @($result.ModelCalls).Count
    }

    Invoke-Case 'artifact junction is rejected without following it' {
        $source = New-TestSourceCell `
            -Root $sourceRoot `
            -CellId 'reparse-source' `
            -FakeExecutable $fakeExecutable
        $outside = Join-Path $script:SuiteRoot 'junction-target'
        [void][IO.Directory]::CreateDirectory($outside)
        [IO.File]::WriteAllText(
            (Join-Path $outside 'outside.txt'),
            'must-not-copy',
            $script:Utf8
        )
        $junction = Join-Path $source 'work\artifacts\unsafe-junction'
        [void](New-Item -ItemType Junction -Path $junction -Target $outside)
        $script:Junctions.Add($junction)
        $before = Get-TestTreeFingerprint -Root $source
        $result = Invoke-TestRecovery `
            -SourceCell $source `
            -OutputRoot $outputRoot `
            -RecoveryId 'reparse-recovery'
        Assert-True -Name 'reparse exit nonzero' -Condition (
            $result.ExitCode -ne 0
        )
        Assert-True -Name 'reparse target absent' -Condition (
            -not (Test-Path -LiteralPath (
                Join-Path $outputRoot 'reparse-recovery'
            ))
        )
        Assert-Equal -Name 'reparse source unchanged' -Expected $before `
            -Actual (Get-TestTreeFingerprint -Root $source)
        Assert-Equal -Name 'reparse model calls zero' -Expected 0 `
            -Actual @($result.ModelCalls).Count
    }

    Invoke-Case 'NTFS alternate data stream is rejected without copying it' {
        $source = New-TestSourceCell `
            -Root $sourceRoot `
            -CellId 'ads-source' `
            -FakeExecutable $fakeExecutable
        $hostFile = Join-Path $source 'work\artifacts\root.txt'
        $adsName = 'blocked-recovery-stream'
        $adsValue = 'alternate-data-must-not-copy'
        try {
            Set-Content `
                -LiteralPath $hostFile `
                -Stream $adsName `
                -Value $adsValue `
                -Encoding UTF8
            $adsObserved = Get-Content `
                -LiteralPath $hostFile `
                -Stream $adsName `
                -Raw
            if (-not $adsObserved.Contains($adsValue)) {
                throw 'named stream read-back mismatch'
            }
        } catch {
            Skip-Case -Reason (
                'volume does not support a readable named data stream'
            )
        }
        $before = Get-TestTreeFingerprint -Root $source
        $result = Invoke-TestRecovery `
            -SourceCell $source `
            -OutputRoot $outputRoot `
            -RecoveryId 'ads-recovery'
        Assert-True -Name 'ADS exit nonzero' -Condition (
            $result.ExitCode -ne 0
        )
        Assert-True -Name 'ADS rejection code visible' -Condition (
            $result.Stderr.Contains('code=SOURCE_ARTIFACTS_INVALID')
        )
        Assert-True -Name 'ADS recovery target absent' -Condition (
            -not (Test-Path -LiteralPath (
                Join-Path $outputRoot 'ads-recovery'
            ))
        )
        Assert-Equal -Name 'ADS source default streams unchanged' `
            -Expected $before `
            -Actual (Get-TestTreeFingerprint -Root $source)
        Assert-Equal -Name 'ADS retained in sealed source' `
            -Expected $adsObserved `
            -Actual (Get-Content `
                -LiteralPath $hostFile `
                -Stream $adsName `
                -Raw)
        Assert-Equal -Name 'ADS model calls zero' -Expected 0 `
            -Actual @($result.ModelCalls).Count
    }

    Invoke-Case 'SQLite WAL is rejected before recovery creation' {
        $source = New-TestSourceCell `
            -Root $sourceRoot `
            -CellId 'wal-source' `
            -FakeExecutable $fakeExecutable
        [IO.File]::WriteAllText(
            (Join-Path $source 'work\current.sqlite-wal'),
            'active-wal',
            $script:Utf8
        )
        $before = Get-TestTreeFingerprint -Root $source
        $result = Invoke-TestRecovery `
            -SourceCell $source `
            -OutputRoot $outputRoot `
            -RecoveryId 'wal-recovery'
        Assert-True -Name 'WAL exit nonzero' -Condition ($result.ExitCode -ne 0)
        Assert-True -Name 'WAL code visible' -Condition (
            $result.Stderr.Contains('code=SOURCE_SQLITE_SIDECAR_PRESENT')
        )
        Assert-True -Name 'WAL recovery absent' -Condition (
            -not (Test-Path -LiteralPath (
                Join-Path $outputRoot 'wal-recovery'
            ))
        )
        Assert-Equal -Name 'WAL source unchanged' -Expected $before `
            -Actual (Get-TestTreeFingerprint -Root $source)
        Assert-Equal -Name 'WAL model calls zero' -Expected 0 `
            -Actual @($result.ModelCalls).Count
    }

    Invoke-Case 'existing recovery target is never modified' {
        $source = New-TestSourceCell `
            -Root $sourceRoot `
            -CellId 'collision-source' `
            -FakeExecutable $fakeExecutable
        $target = Join-Path $outputRoot 'existing-recovery'
        [void][IO.Directory]::CreateDirectory($target)
        [IO.File]::WriteAllText(
            (Join-Path $target 'sentinel.txt'),
            'keep-existing',
            $script:Utf8
        )
        $sourceBefore = Get-TestTreeFingerprint -Root $source
        $targetBefore = Get-TestTreeFingerprint -Root $target
        $result = Invoke-TestRecovery `
            -SourceCell $source `
            -OutputRoot $outputRoot `
            -RecoveryId 'existing-recovery'
        Assert-True -Name 'collision exit nonzero' -Condition (
            $result.ExitCode -ne 0
        )
        Assert-True -Name 'collision code visible' -Condition (
            $result.Stderr.Contains('code=RECOVERY_COLLISION')
        )
        Assert-Equal -Name 'existing target unchanged' -Expected $targetBefore `
            -Actual (Get-TestTreeFingerprint -Root $target)
        Assert-Equal -Name 'collision source unchanged' -Expected $sourceBefore `
            -Actual (Get-TestTreeFingerprint -Root $source)
        Assert-Equal -Name 'collision model calls zero' -Expected 0 `
            -Actual @($result.ModelCalls).Count
    }

    Invoke-Case 'backup failure records terminal evidence without replay' {
        $source = New-TestSourceCell `
            -Root $sourceRoot `
            -CellId 'backup-failure-source' `
            -FakeExecutable $fakeExecutable
        $before = Get-TestTreeFingerprint -Root $source
        $result = Invoke-TestRecovery `
            -SourceCell $source `
            -OutputRoot $outputRoot `
            -RecoveryId 'backup-failure-recovery' `
            -Mode 'backup-fail'
        Assert-True -Name 'backup failure exit nonzero' -Condition (
            $result.ExitCode -ne 0
        )
        Assert-Equal -Name 'backup invoked once' -Expected 1 `
            -Actual @($result.Calls).Count
        Assert-True -Name 'only backup was invoked' -Condition (
            $result.Calls[0].StartsWith('backup|')
        )
        Assert-Equal -Name 'backup failure model calls zero' -Expected 0 `
            -Actual @($result.ModelCalls).Count
        Assert-Equal -Name 'backup failure source unchanged' -Expected $before `
            -Actual (Get-TestTreeFingerprint -Root $source)
        $record = Get-Content -LiteralPath (
            Join-Path $outputRoot 'backup-failure-recovery\recovery.json'
        ) -Raw | ConvertFrom-Json
        Assert-Equal -Name 'backup failure status' -Expected 'BACKUP_FAILED' `
            -Actual $record.status
        Assert-Equal -Name 'backup exit recorded' -Expected 7 `
            -Actual $record.phases.backup_exit_code
        Assert-Equal -Name 'failed bundle unverified' -Expected $false `
            -Actual $record.bundle_verification.verified
        Assert-Equal -Name 'failed semantic replay false' -Expected $false `
            -Actual $record.semantic_replay
    }

    Invoke-Case 'occupied source owner fence is rejected' {
        $source = New-TestSourceCell `
            -Root $sourceRoot `
            -CellId 'owner-active-source' `
            -FakeExecutable $fakeExecutable
        $lockPath = Join-Path $source 'work\current.sqlite.freeagent.owner.lock'
        $before = Get-TestTreeFingerprint -Root $source
        $holder = New-Object IO.FileStream(
            $lockPath,
            [IO.FileMode]::Open,
            [IO.FileAccess]::ReadWrite,
            [IO.FileShare]::ReadWrite
        )
        try {
            $holder.Lock(0, 1)
            $result = Invoke-TestRecovery `
                -SourceCell $source `
                -OutputRoot $outputRoot `
                -RecoveryId 'owner-active-recovery'
        } finally {
            try { $holder.Unlock(0, 1) } catch {}
            $holder.Dispose()
        }
        Assert-True -Name 'owner active exit nonzero' -Condition (
            $result.ExitCode -ne 0
        )
        Assert-True -Name 'owner active code visible' -Condition (
            $result.Stderr.Contains('code=SOURCE_OWNER_ACTIVE')
        )
        Assert-True -Name 'owner active target absent' -Condition (
            -not (Test-Path -LiteralPath (
                Join-Path $outputRoot 'owner-active-recovery'
            ))
        )
        Assert-Equal -Name 'owner active source unchanged' -Expected $before `
            -Actual (Get-TestTreeFingerprint -Root $source)
        Assert-Equal -Name 'owner active model calls zero' -Expected 0 `
            -Actual @($result.ModelCalls).Count
    }

    Invoke-Case 'COMPLETE source cell is rejected' {
        $source = New-TestSourceCell `
            -Root $sourceRoot `
            -CellId 'complete-source' `
            -FakeExecutable $fakeExecutable `
            -Status 'COMPLETE'
        $before = Get-TestTreeFingerprint -Root $source
        $result = Invoke-TestRecovery `
            -SourceCell $source `
            -OutputRoot $outputRoot `
            -RecoveryId 'complete-recovery'
        Assert-True -Name 'complete source exit nonzero' -Condition (
            $result.ExitCode -ne 0
        )
        Assert-True -Name 'complete source code visible' -Condition (
            $result.Stderr.Contains('code=SOURCE_CELL_COMPLETE')
        )
        Assert-True -Name 'complete recovery absent' -Condition (
            -not (Test-Path -LiteralPath (
                Join-Path $outputRoot 'complete-recovery'
            ))
        )
        Assert-Equal -Name 'complete source unchanged' -Expected $before `
            -Actual (Get-TestTreeFingerprint -Root $source)
        Assert-Equal -Name 'complete source model calls zero' -Expected 0 `
            -Actual @($result.ModelCalls).Count
    }
} finally {
    foreach ($junction in $script:Junctions) {
        if (Test-Path -LiteralPath $junction) {
            [IO.Directory]::Delete($junction)
        }
    }
    if ($env:FREEAGENT_S3C_TEST_KEEP -ceq '1') {
        Write-Host ('S3C_RECOVERY_TEST_FIXTURE ' + $script:SuiteRoot)
    } elseif (Test-Path -LiteralPath $script:SuiteRoot -PathType Container) {
        $resolvedSuite = [IO.Path]::GetFullPath($script:SuiteRoot)
        $resolvedTemp = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).
            TrimEnd([char]92, [char]47) + [IO.Path]::DirectorySeparatorChar
        if (-not $resolvedSuite.StartsWith(
            $resolvedTemp,
            [StringComparison]::OrdinalIgnoreCase
        )) {
            throw 'S3C_RECOVERY_SELFTEST_CLEANUP_BOUNDARY'
        }
        Get-ChildItem -LiteralPath $script:SuiteRoot -Recurse -Force |
            ForEach-Object {
                try { $_.Attributes = [IO.FileAttributes]::Normal } catch {}
            }
        Remove-Item -LiteralPath $script:SuiteRoot -Recurse -Force
    }
}

if ($script:Failures.Count -gt 0) {
    $script:Failures | ForEach-Object { Write-Error $_ -ErrorAction Continue }
    throw (
        'S3C_RECOVERY_SELFTEST_FAIL ' +
        "cases=$($script:Cases) assertions=$($script:Assertions) " +
        "skips=$($script:Skips) failures=$($script:Failures.Count)"
    )
}

Write-Host (
    'S3C_RECOVERY_SELFTEST_PASS ' +
    "cases=$($script:Cases) assertions=$($script:Assertions) " +
    "skips=$($script:Skips)"
)
