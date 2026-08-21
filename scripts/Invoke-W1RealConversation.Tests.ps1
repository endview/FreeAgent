[CmdletBinding()]
param()

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:Runner = Join-Path $PSScriptRoot 'Invoke-W1RealConversation.ps1'
$script:PowerShell = (Get-Process -Id $PID).Path
$script:Utf8NoBom = New-Object Text.UTF8Encoding($false)
$script:Cases = 0
$script:Assertions = 0
$script:Failures = New-Object 'Collections.Generic.List[string]'
$script:EvidenceRoots = New-Object 'Collections.Generic.List[string]'
$script:RuntimeName = 'FREEAGENT_W1_OFFLINE_TEST_CREDENTIAL'
$script:RuntimeValue = 'not-a-secret'

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

function Test-BytesContain {
    param(
        [Parameter(Mandatory = $true)][byte[]]$Haystack,
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

function New-TestSeed {
    param([Parameter(Mandatory = $true)][string]$Path)
    $json = @'
{"schema_version":"freeagent.bootstrap-seed/v1","seed_id":"freeagent.w1.offline","seed_revision":1,"tenant_id":"default","default_assembly":{"workspace_id":"local-chat","agent_id":"assistant","profile_id":"deepseek-chat"},"definitions":{"workspace":{"id":"local-chat","version":"1","body":{"name":"Local Chat"},"budget_policy_alias":"budget"},"agent":{"id":"assistant","version":"1","body":{"name":"Assistant","kind":"GENERAL"}},"profile":{"id":"deepseek-chat","version":"1","body":{"name":"DeepSeek Chat","mode":"PURE_CHAT"},"context_policy_alias":"context","cost_policy_alias":"cost","scheduling_policy_alias":"schedule"}},"model_binding":{"port":{"name":"model.generate","exact_version":"v1"},"instance_id":"model-deepseek","failure_policy":"REQUIRED","config":{"schema_version":"model-binding-config/v1","provider":"deepseek","model":"deepseek-v4-flash","model_build_id":"offline-build","billing_version":"offline-price","price_snapshot_id":"offline-price","parameters":{}}}}
'@
    [IO.File]::WriteAllText($Path, $json, $script:Utf8NoBom)
}

function New-TestFakeExecutable {
    param([Parameter(Mandatory = $true)][string]$OutputPath)
    $source = @'
using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Security.Cryptography;
using System.Text;

public static class FakeW1FreeAgent
{
    private sealed class Record
    {
        public string Run;
        public string Reply;
        public string Deadline;
        public int Turn;
    }

    private sealed class State
    {
        public int Revision;
        public string Head = "";
        public int Calls;
        public string Conversation = "";
        public Dictionary<string, Record> Requests =
            new Dictionary<string, Record>(StringComparer.Ordinal);
    }

    private static Dictionary<string, string> Options(string[] args)
    {
        var result = new Dictionary<string, string>(StringComparer.Ordinal);
        for (int index = 1; index < args.Length; index++)
        {
            if (!args[index].StartsWith("--", StringComparison.Ordinal)) continue;
            if (index + 1 < args.Length &&
                !args[index + 1].StartsWith("--", StringComparison.Ordinal))
                result[args[index]] = args[++index];
            else
                result[args[index]] = "true";
        }
        return result;
    }

    private static string Get(Dictionary<string, string> values, string name)
    {
        string value;
        if (!values.TryGetValue(name, out value))
            throw new InvalidOperationException("missing option " + name);
        return value;
    }

    private static string Optional(
        Dictionary<string, string> values,
        string name,
        string fallback)
    {
        string value;
        return values.TryGetValue(name, out value) ? value : fallback;
    }

    private static string Encode(string value)
    {
        return Convert.ToBase64String(new UTF8Encoding(false).GetBytes(value ?? ""));
    }

    private static string Decode(string value)
    {
        return new UTF8Encoding(false, true).GetString(Convert.FromBase64String(value));
    }

    private static State Load(string path)
    {
        var state = new State();
        foreach (string line in File.ReadAllLines(path, new UTF8Encoding(false, true)))
        {
            if (line.StartsWith("revision=", StringComparison.Ordinal))
                state.Revision = Int32.Parse(line.Substring(9), CultureInfo.InvariantCulture);
            else if (line.StartsWith("head=", StringComparison.Ordinal))
                state.Head = Decode(line.Substring(5));
            else if (line.StartsWith("calls=", StringComparison.Ordinal))
                state.Calls = Int32.Parse(line.Substring(6), CultureInfo.InvariantCulture);
            else if (line.StartsWith("conversation=", StringComparison.Ordinal))
                state.Conversation = Decode(line.Substring(13));
            else if (line.StartsWith("request=", StringComparison.Ordinal))
            {
                string[] parts = line.Substring(8).Split('|');
                if (parts.Length != 5) throw new InvalidDataException("request state");
                state.Requests.Add(Decode(parts[0]), new Record {
                    Run = Decode(parts[1]),
                    Reply = Decode(parts[2]),
                    Deadline = Decode(parts[3]),
                    Turn = Int32.Parse(parts[4], CultureInfo.InvariantCulture)
                });
            }
        }
        return state;
    }

    private static void Save(string path, State state)
    {
        var lines = new List<string>();
        lines.Add("revision=" + state.Revision.ToString(CultureInfo.InvariantCulture));
        lines.Add("head=" + Encode(state.Head));
        lines.Add("calls=" + state.Calls.ToString(CultureInfo.InvariantCulture));
        lines.Add("conversation=" + Encode(state.Conversation));
        var keys = new List<string>(state.Requests.Keys);
        keys.Sort(StringComparer.Ordinal);
        foreach (string key in keys)
        {
            Record record = state.Requests[key];
            lines.Add("request=" + Encode(key) + "|" + Encode(record.Run) + "|" +
                Encode(record.Reply) + "|" + Encode(record.Deadline) + "|" +
                record.Turn.ToString(CultureInfo.InvariantCulture));
        }
        string parent = Path.GetDirectoryName(path);
        if (!String.IsNullOrEmpty(parent)) Directory.CreateDirectory(parent);
        File.WriteAllLines(path, lines.ToArray(), new UTF8Encoding(false));
    }

    private static string Json(string value)
    {
        return (value ?? "").Replace("\\", "\\\\").Replace("\"", "\\\"");
    }

    private static void Out(string value)
    {
        Console.OutputEncoding = new UTF8Encoding(false);
        Console.WriteLine(value);
    }

    private static void Err(string value)
    {
        Console.Error.WriteLine(value);
    }

    private static void Log(string value)
    {
        string path = Environment.GetEnvironmentVariable("W1_FAKE_INVOCATION_LOG");
        if (!String.IsNullOrWhiteSpace(path))
            File.AppendAllText(path, value + "\n", new UTF8Encoding(false));
    }

    private static string Digest(int revision)
    {
        byte[] input = new UTF8Encoding(false).GetBytes(
            "offline-manifest-" + revision.ToString(CultureInfo.InvariantCulture));
        using (SHA256 sha = SHA256.Create())
        {
            return BitConverter.ToString(sha.ComputeHash(input)).
                Replace("-", "").ToLowerInvariant();
        }
    }

    private static string AttemptCounts()
    {
        return "{\"model_pending\":0,\"model_unknown\":0," +
            "\"action_pending\":0,\"action_unknown\":0," +
            "\"channel_ingress_receipts\":0,\"channel_cursor_scopes\":0," +
            "\"channel_send_pending\":0,\"channel_send_unknown\":0}";
    }

    private static void WriteConversation(State state, bool created)
    {
        Out("{\"conversation_id\":\"" + Json(state.Conversation) +
            "\",\"tenant_id\":\"default\",\"principal_id\":\"local-operator\"," +
            "\"workspace_id\":\"local-chat\",\"agent_id\":\"assistant\"," +
            "\"profile_id\":\"deepseek-chat\",\"head_run_id\":\"" +
            Json(state.Head) + "\",\"revision\":" +
            state.Revision.ToString(CultureInfo.InvariantCulture) +
            ",\"created\":" + (created ? "true" : "false") +
            ",\"created_at\":\"2026-01-01T00:00:00Z\"," +
            "\"updated_at\":\"2026-01-01T00:00:00Z\"}");
    }

    private static string Usage(
        int turn,
        bool drift,
        bool priceDrift,
        bool omit)
    {
        if (omit) return "null";
        if (turn % 10 == 0)
        {
            return "{\"input_tokens\":null,\"cached_input_tokens\":null," +
                "\"uncached_input_tokens\":null,\"output_tokens\":null," +
                "\"reasoning_tokens\":null,\"estimated_cost\":null," +
                "\"provider_reported_cost\":null,\"reconciled_cost\":null," +
                "\"status\":\"NO_USAGE_REPORTED\"," +
                "\"price_snapshot_id\":\"offline-price\",\"currency\":\"CNY\"}";
        }
        int input = 100 + turn;
        int cached = turn % 2 == 0 ? 40 : 0;
        if (drift) cached++;
        int uncached = input - (turn % 2 == 0 ? 40 : 0);
        string estimated = ((decimal)turn / 1000000m).ToString(
            "0.000000",
            CultureInfo.InvariantCulture);
        string priceIdentity = priceDrift ? "offline-price-v2" : "offline-price";
        return "{\"input_tokens\":" + input.ToString(CultureInfo.InvariantCulture) +
            ",\"cached_input_tokens\":" + cached.ToString(CultureInfo.InvariantCulture) +
            ",\"uncached_input_tokens\":" + uncached.ToString(CultureInfo.InvariantCulture) +
            ",\"output_tokens\":" + (20 + turn).ToString(CultureInfo.InvariantCulture) +
            ",\"reasoning_tokens\":" + (turn % 7).ToString(CultureInfo.InvariantCulture) +
            ",\"estimated_cost\":\"" + estimated + "\"," +
            "\"provider_reported_cost\":null,\"reconciled_cost\":null," +
            "\"status\":\"PROVIDER_REPORTED\"," +
            "\"price_snapshot_id\":\"" + priceIdentity +
            "\",\"currency\":\"CNY\"}";
    }

    private static void WriteChat(
        State state,
        string request,
        Record record,
        bool drift,
        bool priceDrift,
        bool omitUsage)
    {
        Out("{\"request_id\":\"" + Json(request) +
            "\",\"deadline\":\"" + Json(record.Deadline) +
            "\",\"run_id\":\"" + Json(record.Run) +
            "\",\"conversation_id\":\"" + Json(state.Conversation) +
            "\",\"conversation_revision\":" +
            state.Revision.ToString(CultureInfo.InvariantCulture) +
            ",\"disposition\":\"TERMINATED\",\"reason\":\"MODEL_SUCCEEDED\"," +
            "\"reply\":\"" + Json(record.Reply) + "\",\"failure\":\"\"," +
            "\"usage\":" + Usage(
                record.Turn,
                drift,
                priceDrift,
                omitUsage) + "}");
    }

    public static int Main(string[] args)
    {
        try
        {
            if (args.Length == 0) return 64;
            string command = args[0];
            var options = Options(args);
            string mode = Environment.GetEnvironmentVariable("W1_FAKE_MODE") ?? "success";
            if (command == "init")
            {
                string database = Get(options, "--db");
                string artifacts = Get(options, "--artifact-root");
                Directory.CreateDirectory(artifacts);
                File.WriteAllText(Path.Combine(artifacts, "offline.txt"), "artifact");
                Save(database, new State());
                Log("init");
                Out("{\"phase\":\"init\",\"ok\":true}");
                return 0;
            }
            if (command == "conversation-create")
            {
                string database = Get(options, "--db");
                State state = Load(database);
                if (!String.IsNullOrEmpty(state.Conversation)) return 71;
                state.Conversation = Get(options, "--conversation");
                Save(database, state);
                Log("conversation-create");
                WriteConversation(state, true);
                return 0;
            }
            if (command == "conversation-get")
            {
                State state = Load(Get(options, "--db"));
                Log("conversation-get|revision=" + state.Revision);
                WriteConversation(state, false);
                return 0;
            }
            if (command == "chat")
            {
                string database = Get(options, "--db");
                State state = Load(database);
                string request = Get(options, "--request-id");
                string environmentName = Get(options, "--deepseek-api-key-env");
                string runtimeValue = Environment.GetEnvironmentVariable(environmentName);
                bool runtimePresent = !String.IsNullOrEmpty(runtimeValue);
                Record existing;
                if (state.Requests.TryGetValue(request, out existing))
                {
                    Log("chat-retry|revision=" + state.Revision +
                        "|runtime=" + (runtimePresent ? "1" : "0") +
                        "|calls=" + state.Calls);
                    if (mode == "retry-dispatch-bug" && !runtimePresent) return 78;
                    if (runtimePresent) return 79;
                    WriteChat(
                        state,
                        request,
                        existing,
                        mode == "retry-usage-drift",
                        false,
                        false);
                    return 0;
                }
                int expected = Int32.Parse(
                    Get(options, "--conversation-revision"),
                    CultureInfo.InvariantCulture);
                if (expected != state.Revision) return 72;
                string expectedHead = Optional(options, "--conversation-head-run", "");
                if (!String.Equals(expectedHead, state.Head, StringComparison.Ordinal)) return 73;
                int turn = state.Revision + 1;
                if (mode == "fail-turn-3" && turn == 3)
                {
                    Log("chat-fail|turn=3|calls=" + state.Calls);
                    Err("offline failure");
                    return 9;
                }
                if (mode == "leak-stderr-turn-3" && turn == 3)
                {
                    Log("chat-leak|turn=3|calls=" + state.Calls);
                    Err(runtimeValue ?? "");
                    return 9;
                }
                if (!runtimePresent)
                {
                    Log("chat-missing-runtime|turn=" + turn + "|calls=" + state.Calls);
                    return 77;
                }
                state.Calls++;
                state.Revision = turn;
                state.Head = "offline-run-" + turn.ToString("D2", CultureInfo.InvariantCulture);
                var record = new Record {
                    Run = state.Head,
                    Reply = "offline reply " + turn.ToString("D2", CultureInfo.InvariantCulture),
                    Deadline = Get(options, "--deadline"),
                    Turn = turn
                };
                state.Requests.Add(request, record);
                Save(database, state);
                Log("chat-new|turn=" + turn + "|runtime=1|calls=" + state.Calls);
                WriteChat(
                    state,
                    request,
                    record,
                    false,
                    mode == "price-drift-turn-3" && turn == 3,
                    mode == "missing-usage-turn-3" && turn == 3);
                return 0;
            }
            if (command == "backup")
            {
                string database = Get(options, "--db");
                string bundle = Get(options, "--out");
                State state = Load(database);
                Directory.CreateDirectory(bundle);
                File.Copy(database, Path.Combine(bundle, "database.state"), false);
                string digest = Digest(state.Revision);
                File.WriteAllText(Path.Combine(bundle, "digest.txt"), digest);
                Log("backup|revision=" + state.Revision);
                Out("{\"bundle\":\"" + Json(bundle) + "\",\"manifest\":{" +
                    "\"manifest_digest\":\"" + digest + "\"," +
                    "\"store_identity\":{\"store_instance_id\":\"offline-store\"}," +
                    "\"attempt_counts\":" + AttemptCounts() + "}}");
                return 0;
            }
            if (command == "backup-verify")
            {
                string bundle = Get(options, "--bundle");
                State state = Load(Path.Combine(bundle, "database.state"));
                string digest = File.ReadAllText(Path.Combine(bundle, "digest.txt"));
                if (!String.Equals(digest, Digest(state.Revision), StringComparison.Ordinal)) return 74;
                Log("backup-verify|revision=" + state.Revision);
                Out("{\"bundle\":\"" + Json(bundle) + "\",\"manifest\":{" +
                    "\"manifest_digest\":\"" + digest + "\"," +
                    "\"store_identity\":{\"store_instance_id\":\"offline-store\"}," +
                    "\"attempt_counts\":" + AttemptCounts() + "}}");
                return 0;
            }
            if (command == "restore")
            {
                string bundle = Get(options, "--bundle");
                string database = Get(options, "--db");
                string artifacts = Get(options, "--artifact-root");
                Directory.CreateDirectory(Path.GetDirectoryName(database));
                Directory.CreateDirectory(artifacts);
                File.Copy(Path.Combine(bundle, "database.state"), database, false);
                File.WriteAllText(Path.Combine(artifacts, "offline.txt"), "artifact");
                State state = Load(database);
                Log("restore|revision=" + state.Revision);
                Out("{\"database_path\":\"" + Json(Path.GetFullPath(database)) +
                    "\",\"artifact_root\":\"" + Json(Path.GetFullPath(artifacts)) +
                    "\",\"store_instance_id\":\"offline-store\"}");
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

function Invoke-TestRunner {
    param(
        [Parameter(Mandatory = $true)][string]$FakeExecutable,
        [Parameter(Mandatory = $true)][string]$Seed,
        [Parameter(Mandatory = $true)][string]$Mode,
        [Parameter(Mandatory = $true)][string]$InvocationLog,
        [bool]$RuntimePresent = $true,
        [string]$RuntimeName = $script:RuntimeName
    )
    $arguments = @(
        '-NoLogo', '-NoProfile', '-NonInteractive',
        '-ExecutionPolicy', 'Bypass', '-File', $script:Runner,
        '-FreeAgentPath', $FakeExecutable,
        '-SeedPath', $Seed,
        '-SecretEnvironmentName', $RuntimeName,
        '-CommandTimeoutMilliseconds', '60000'
    )
    $savedRuntime = [Environment]::GetEnvironmentVariable(
        $script:RuntimeName,
        'Process'
    )
    $savedMode = [Environment]::GetEnvironmentVariable('W1_FAKE_MODE', 'Process')
    $savedLog = [Environment]::GetEnvironmentVariable(
        'W1_FAKE_INVOCATION_LOG',
        'Process'
    )
    $process = $null
    try {
        [Environment]::SetEnvironmentVariable(
            $script:RuntimeName,
            $(if ($RuntimePresent) { $script:RuntimeValue } else { $null }),
            'Process'
        )
        [Environment]::SetEnvironmentVariable('W1_FAKE_MODE', $Mode, 'Process')
        [Environment]::SetEnvironmentVariable(
            'W1_FAKE_INVOCATION_LOG',
            $InvocationLog,
            'Process'
        )
        $start = New-Object Diagnostics.ProcessStartInfo
        $start.FileName = $script:PowerShell
        $start.Arguments = Join-TestWindowsArguments -Values $arguments
        $start.UseShellExecute = $false
        $start.CreateNoWindow = $true
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $true
        $start.StandardOutputEncoding = $script:Utf8NoBom
        $start.StandardErrorEncoding = $script:Utf8NoBom
        $process = New-Object Diagnostics.Process
        $process.StartInfo = $start
        if (-not $process.Start()) { throw 'runner process did not start' }
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(300000)) {
            $process.Kill()
            throw 'runner process timed out'
        }
        $stdoutTask.Wait()
        $stderrTask.Wait()
        $stdout = [string]$stdoutTask.Result
        $stderr = [string]$stderrTask.Result
        $summary = $null
        try { $summary = $stdout | ConvertFrom-Json -ErrorAction Stop } catch {}
        if ($null -ne $summary -and
            $null -ne $summary.PSObject.Properties['evidence_path'] -and
            -not [string]::IsNullOrWhiteSpace([string]$summary.evidence_path)) {
            $script:EvidenceRoots.Add([string]$summary.evidence_path)
        }
        return [pscustomobject]@{
            ExitCode = [int]$process.ExitCode
            Stdout = $stdout
            Stderr = $stderr
            Summary = $summary
        }
    } finally {
        if ($null -ne $process) { $process.Dispose() }
        [Environment]::SetEnvironmentVariable(
            $script:RuntimeName,
            $savedRuntime,
            'Process'
        )
        [Environment]::SetEnvironmentVariable('W1_FAKE_MODE', $savedMode, 'Process')
        [Environment]::SetEnvironmentVariable(
            'W1_FAKE_INVOCATION_LOG',
            $savedLog,
            'Process'
        )
    }
}

function Remove-TestTree {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Container)) { return }
    $full = [IO.Path]::GetFullPath($Path)
    $temp = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).
        TrimEnd([char]92, [char]47) + [IO.Path]::DirectorySeparatorChar
    if (-not $full.StartsWith($temp, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'W1_REAL_CONVERSATION_SELFTEST_CLEANUP_BOUNDARY'
    }
    Get-ChildItem -LiteralPath $full -Recurse -Force | ForEach-Object {
        try { $_.Attributes = [IO.FileAttributes]::Normal } catch {}
    }
    Remove-Item -LiteralPath $full -Recurse -Force
}

function Assert-EvidenceContainsNoRuntimeValue {
    param([Parameter(Mandatory = $true)][string]$Path)
    [byte[]]$needle = $script:Utf8NoBom.GetBytes($script:RuntimeValue)
    foreach ($file in @(Get-ChildItem -LiteralPath $Path -File -Recurse -Force)) {
        Assert-True -Name ('runtime value absent from ' + $file.Name) -Condition (
            -not (Test-BytesContain `
                -Haystack ([IO.File]::ReadAllBytes($file.FullName)) `
                -Needle $needle)
        )
    }
}

if (-not (Test-Path -LiteralPath $script:Runner -PathType Leaf)) {
    throw 'W1_REAL_CONVERSATION_SELFTEST_DEPENDENCY_MISSING'
}
$suiteRoot = Join-Path ([IO.Path]::GetTempPath()) (
    'freeagent w1 real conversation tests-' + [Guid]::NewGuid().ToString('N')
)
[void][IO.Directory]::CreateDirectory($suiteRoot)

try {
    $seed = Join-Path $suiteRoot 'seed.json'
    New-TestSeed -Path $seed
    $fake = Join-Path $suiteRoot 'fake-freeagent.exe'
    New-TestFakeExecutable -OutputPath $fake

    Invoke-Case 'runner is ASCII, parses, and never reads a secret value' {
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
        $text = [IO.File]::ReadAllText($script:Runner, [Text.Encoding]::ASCII)
        Assert-True -Name 'runner does not call GetEnvironmentVariable' -Condition (
            -not $text.Contains('GetEnvironmentVariable(')
        )
        Assert-True -Name 'runner has no literal key parameter' -Condition (
            -not $text.Contains('$ApiKey') -and -not $text.Contains('$SecretValue')
        )
        Assert-True -Name 'secretless child removal is present' -Condition (
            $text.Contains('EnvironmentVariables.Remove($SecretEnvironmentName)')
        )
    }

    Invoke-Case 'success performs 50 calls, two stage gate, restore, retries, and cleanup' {
        $log = Join-Path $suiteRoot 'success.log'
        $result = Invoke-TestRunner `
            -FakeExecutable $fake `
            -Seed $seed `
            -Mode 'success' `
            -InvocationLog $log
        Assert-Equal -Name 'success exit' -Expected 0 -Actual $result.ExitCode
        Assert-True -Name 'success summary exists' -Condition ($null -ne $result.Summary)
        $summary = $result.Summary
        Assert-Equal -Name 'success schema' `
            -Expected 'freeagent.w1-real-conversation-summary/v1' `
            -Actual $summary.schema_version
        Assert-Equal -Name 'success status' -Expected 'COMPLETE' -Actual $summary.status
        Assert-Equal -Name 'turn count' -Expected 50 -Actual $summary.turns_succeeded
        Assert-Equal -Name 'unique runs' -Expected 50 -Actual $summary.unique_runs
        Assert-Equal -Name 'final revision' -Expected 50 -Actual $summary.final_revision
        Assert-Equal -Name 'first two gate' -Expected $true `
            -Actual $summary.first_two_turns_verified
        Assert-Equal -Name 'turn 25 backup' -Expected $true `
            -Actual $summary.turn_25_backup_verified
        Assert-Equal -Name 'turn 25 restore' -Expected $true `
            -Actual $summary.turn_25_restore_verified
        Assert-Equal -Name 'retry 25' -Expected $true `
            -Actual $summary.exact_retry_without_runtime_value.turn_25
        Assert-Equal -Name 'retry 50' -Expected $true `
            -Actual $summary.exact_retry_without_runtime_value.turn_50
        Assert-Equal -Name 'final backup' -Expected $true `
            -Actual $summary.final_backup_verified
        Assert-Equal -Name 'cleanup' -Expected 'REMOVED' -Actual $summary.cleanup_status
        Assert-Equal -Name 'no evidence retained' -Expected $false `
            -Actual $summary.evidence_retained
        Assert-Equal -Name 'no evidence path' -Expected $null -Actual $summary.evidence_path
        Assert-Equal -Name 'no automatic retry' -Expected $false `
            -Actual $summary.automatic_retry
        Assert-Equal -Name 'harness does not read value' -Expected $false `
            -Actual $summary.runtime_value_read_by_harness
        Assert-Equal -Name 'harness does not persist value' -Expected $false `
            -Actual $summary.runtime_value_persisted_by_harness
        $usage = $summary.usage
        Assert-Equal -Name 'usage expected attempts' -Expected 50 `
            -Actual $usage.expected_original_attempts
        Assert-Equal -Name 'usage original attempts only' -Expected 50 `
            -Actual $usage.original_attempts_observed
        Assert-Equal -Name 'usage price snapshot frozen' `
            -Expected 'offline-price' `
            -Actual $usage.price_snapshot_id
        Assert-Equal -Name 'provider reported statuses' -Expected 45 `
            -Actual $usage.status_counts.provider_reported
        Assert-Equal -Name 'no report statuses' -Expected 5 `
            -Actual $usage.status_counts.no_usage_reported
        Assert-Equal -Name 'input total' -Expected 5625 `
            -Actual $usage.tokens.input_tokens.total
        Assert-Equal -Name 'cached input total' -Expected 800 `
            -Actual $usage.tokens.cached_input_tokens.total
        Assert-Equal -Name 'uncached input total' -Expected 4825 `
            -Actual $usage.tokens.uncached_input_tokens.total
        Assert-Equal -Name 'output total' -Expected 2025 `
            -Actual $usage.tokens.output_tokens.total
        Assert-Equal -Name 'reasoning total' -Expected 131 `
            -Actual $usage.tokens.reasoning_tokens.total
        Assert-Equal -Name 'known reasoning attempts' -Expected 45 `
            -Actual $usage.tokens.reasoning_tokens.known_attempts
        Assert-Equal -Name 'unknown reasoning attempts' -Expected 5 `
            -Actual $usage.tokens.reasoning_tokens.unknown_attempts
        Assert-True -Name 'weighted cache ratio' -Condition (
            [Math]::Abs(
                [double]$usage.cache.weighted_cache_hit_ratio -
                [double]0.142222222222
            ) -lt 0.000000000001
        )
        Assert-Equal -Name 'weighted cache eligible' -Expected 45 `
            -Actual $usage.cache.weighted_eligible_attempts
        Assert-Equal -Name 'weighted cache unknown' -Expected 5 `
            -Actual $usage.cache.weighted_unknown_attempts
        Assert-Equal -Name 'request cache hits' -Expected 20 `
            -Actual $usage.cache.request_hit_attempts
        Assert-Equal -Name 'request cache misses' -Expected 25 `
            -Actual $usage.cache.request_miss_attempts
        Assert-Equal -Name 'request cache unknown' -Expected 5 `
            -Actual $usage.cache.request_unknown_attempts
        Assert-True -Name 'request cache ratio' -Condition (
            [Math]::Abs(
                [double]$usage.cache.request_hit_ratio -
                [double]0.444444444444
            ) -lt 0.000000000001
        )
        Assert-Equal -Name 'estimated cost total' -Expected '0.001125' `
            -Actual $usage.estimated_cost.total
        Assert-Equal -Name 'estimated cost currency' -Expected 'CNY' `
            -Actual $usage.estimated_cost.currency
        Assert-Equal -Name 'estimated cost known' -Expected 45 `
            -Actual $usage.estimated_cost.known_attempts
        Assert-Equal -Name 'estimated cost unknown' -Expected 5 `
            -Actual $usage.estimated_cost.unknown_attempts
        Assert-Equal -Name 'provider cost stays unknown' -Expected $null `
            -Actual $usage.provider_reported_cost.total
        Assert-Equal -Name 'provider cost unknown coverage' -Expected 50 `
            -Actual $usage.provider_reported_cost.unknown_attempts
        Assert-Equal -Name 'reconciled cost stays unknown' -Expected $null `
            -Actual $usage.reconciled_cost.total
        Assert-Equal -Name 'reconciled cost unknown coverage' -Expected 50 `
            -Actual $usage.reconciled_cost.unknown_attempts
        Assert-Equal -Name 'complete requested usage' -Expected 45 `
            -Actual $usage.unknown_coverage.attempts_with_complete_requested_usage
        Assert-Equal -Name 'requested usage unknown' -Expected 5 `
            -Actual $usage.unknown_coverage.attempts_with_any_requested_unknown
        Assert-Equal -Name 'complete usage facts' -Expected 45 `
            -Actual $usage.unknown_coverage.attempts_with_complete_usage_facts
        Assert-Equal -Name 'no usage facts' -Expected 5 `
            -Actual $usage.unknown_coverage.attempts_without_any_usage_facts
        Assert-True -Name 'runtime absent from stdout' -Condition (
            -not $result.Stdout.Contains($script:RuntimeValue)
        )
        Assert-True -Name 'runtime absent from stderr' -Condition (
            -not $result.Stderr.Contains($script:RuntimeValue)
        )
        $lines = @([IO.File]::ReadAllLines($log, $script:Utf8NoBom))
        $newCalls = @($lines | Where-Object { $_ -like 'chat-new|*' })
        $retries = @($lines | Where-Object { $_ -like 'chat-retry|*' })
        Assert-Equal -Name 'exact new model calls' -Expected 50 -Actual $newCalls.Count
        Assert-Equal -Name 'exact retry count' -Expected 2 -Actual $retries.Count
        Assert-Equal -Name 'retry 25 lacks runtime' -Expected 1 -Actual @(
            $retries | Where-Object { $_ -eq 'chat-retry|revision=25|runtime=0|calls=25' }
        ).Count
        Assert-Equal -Name 'retry 50 lacks runtime' -Expected 1 -Actual @(
            $retries | Where-Object { $_ -eq 'chat-retry|revision=50|runtime=0|calls=50' }
        ).Count
        Assert-Equal -Name 'two backups' -Expected 2 -Actual @(
            $lines | Where-Object { $_ -like 'backup|*' }
        ).Count
        Assert-Equal -Name 'two backup verifies' -Expected 2 -Actual @(
            $lines | Where-Object { $_ -like 'backup-verify|*' }
        ).Count
        Assert-Equal -Name 'one restore' -Expected 1 -Actual @(
            $lines | Where-Object { $_ -eq 'restore|revision=25' }
        ).Count
    }

    Invoke-Case 'turn failure stops without replay and retains safe evidence' {
        $log = Join-Path $suiteRoot 'turn-failure.log'
        $result = Invoke-TestRunner `
            -FakeExecutable $fake `
            -Seed $seed `
            -Mode 'fail-turn-3' `
            -InvocationLog $log
        Assert-True -Name 'turn failure is nonzero' -Condition ($result.ExitCode -ne 0)
        Assert-Equal -Name 'turn failure status' -Expected 'FAILED' `
            -Actual $result.Summary.status
        Assert-Equal -Name 'turn failure code' -Expected 'TURN_03_PROCESS_FAILED' `
            -Actual $result.Summary.failure_code
        Assert-Equal -Name 'two turns retained' -Expected 2 `
            -Actual $result.Summary.turns_succeeded
        Assert-Equal -Name 'stage two passed' -Expected $true `
            -Actual $result.Summary.first_two_turns_verified
        Assert-Equal -Name 'evidence retained' -Expected $true `
            -Actual $result.Summary.evidence_retained
        Assert-True -Name 'evidence path exists' -Condition (
            Test-Path -LiteralPath $result.Summary.evidence_path -PathType Container
        )
        $lines = @([IO.File]::ReadAllLines($log, $script:Utf8NoBom))
        Assert-Equal -Name 'failure made two model calls' -Expected 2 -Actual @(
            $lines | Where-Object { $_ -like 'chat-new|*' }
        ).Count
        Assert-Equal -Name 'failure attempted turn three once' -Expected 1 -Actual @(
            $lines | Where-Object { $_ -eq 'chat-fail|turn=3|calls=2' }
        ).Count
        Assert-EvidenceContainsNoRuntimeValue -Path $result.Summary.evidence_path
    }

    Invoke-Case 'price snapshot identity is frozen across original attempts' {
        $log = Join-Path $suiteRoot 'price-drift.log'
        $result = Invoke-TestRunner `
            -FakeExecutable $fake `
            -Seed $seed `
            -Mode 'price-drift-turn-3' `
            -InvocationLog $log
        Assert-True -Name 'price drift is nonzero' -Condition ($result.ExitCode -ne 0)
        Assert-Equal -Name 'price drift code' `
            -Expected 'USAGE_PRICE_SNAPSHOT_DRIFT' `
            -Actual $result.Summary.failure_code
        Assert-Equal -Name 'price drift keeps first identity' `
            -Expected 'offline-price' `
            -Actual $result.Summary.usage.price_snapshot_id
        Assert-Equal -Name 'price drift observes two valid usages' -Expected 2 `
            -Actual $result.Summary.usage.original_attempts_observed
        $lines = @([IO.File]::ReadAllLines($log, $script:Utf8NoBom))
        Assert-Equal -Name 'price drift made three calls' -Expected 3 -Actual @(
            $lines | Where-Object { $_ -like 'chat-new|*' }
        ).Count
    }

    Invoke-Case 'every successful original turn requires authoritative usage' {
        $log = Join-Path $suiteRoot 'missing-usage.log'
        $result = Invoke-TestRunner `
            -FakeExecutable $fake `
            -Seed $seed `
            -Mode 'missing-usage-turn-3' `
            -InvocationLog $log
        Assert-True -Name 'missing usage is nonzero' -Condition ($result.ExitCode -ne 0)
        Assert-Equal -Name 'missing usage code' `
            -Expected 'TURN_03_USAGE_INVALID' `
            -Actual $result.Summary.failure_code
        Assert-Equal -Name 'missing usage keeps two valid attempts' -Expected 2 `
            -Actual $result.Summary.usage.original_attempts_observed
        $lines = @([IO.File]::ReadAllLines($log, $script:Utf8NoBom))
        Assert-Equal -Name 'missing usage made three calls' -Expected 3 -Actual @(
            $lines | Where-Object { $_ -like 'chat-new|*' }
        ).Count
    }

    Invoke-Case 'exact retry cannot dispatch because the runtime value is removed' {
        $log = Join-Path $suiteRoot 'retry-bug.log'
        $result = Invoke-TestRunner `
            -FakeExecutable $fake `
            -Seed $seed `
            -Mode 'retry-dispatch-bug' `
            -InvocationLog $log
        Assert-True -Name 'retry bug is nonzero' -Condition ($result.ExitCode -ne 0)
        Assert-Equal -Name 'retry bug code' -Expected 'EXACT_RETRY_25_FAILED' `
            -Actual $result.Summary.failure_code
        Assert-Equal -Name 'retry bug stops at 25' -Expected 25 `
            -Actual $result.Summary.turns_succeeded
        Assert-Equal -Name 'backup completed before retry' -Expected $true `
            -Actual $result.Summary.turn_25_backup_verified
        Assert-Equal -Name 'restore completed before retry' -Expected $true `
            -Actual $result.Summary.turn_25_restore_verified
        $lines = @([IO.File]::ReadAllLines($log, $script:Utf8NoBom))
        Assert-Equal -Name 'retry bug has 25 model calls' -Expected 25 -Actual @(
            $lines | Where-Object { $_ -like 'chat-new|*' }
        ).Count
        Assert-Equal -Name 'bug retry had no runtime' -Expected 1 -Actual @(
            $lines | Where-Object { $_ -eq 'chat-retry|revision=25|runtime=0|calls=25' }
        ).Count
    }

    Invoke-Case 'exact retry usage must deep equal and is never recounted' {
        $log = Join-Path $suiteRoot 'retry-usage-drift.log'
        $result = Invoke-TestRunner `
            -FakeExecutable $fake `
            -Seed $seed `
            -Mode 'retry-usage-drift' `
            -InvocationLog $log
        Assert-True -Name 'usage drift is nonzero' -Condition ($result.ExitCode -ne 0)
        Assert-Equal -Name 'usage drift code' `
            -Expected 'EXACT_RETRY_25_CHANGED_RESULT' `
            -Actual $result.Summary.failure_code
        Assert-Equal -Name 'usage drift original attempts only' -Expected 25 `
            -Actual $result.Summary.usage.original_attempts_observed
        Assert-Equal -Name 'usage drift no duplicate cost' -Expected '0.000295' `
            -Actual $result.Summary.usage.estimated_cost.total
        $lines = @([IO.File]::ReadAllLines($log, $script:Utf8NoBom))
        Assert-Equal -Name 'usage drift has 25 calls' -Expected 25 -Actual @(
            $lines | Where-Object { $_ -like 'chat-new|*' }
        ).Count
        Assert-Equal -Name 'usage drift retry had no runtime' -Expected 1 -Actual @(
            $lines | Where-Object {
                $_ -eq 'chat-retry|revision=25|runtime=0|calls=25'
            }
        ).Count
    }

    Invoke-Case 'child stderr cannot leak a runtime value through the summary' {
        $log = Join-Path $suiteRoot 'leak.log'
        $result = Invoke-TestRunner `
            -FakeExecutable $fake `
            -Seed $seed `
            -Mode 'leak-stderr-turn-3' `
            -InvocationLog $log
        Assert-True -Name 'leak fixture is nonzero' -Condition ($result.ExitCode -ne 0)
        Assert-True -Name 'runtime absent from wrapper stdout' -Condition (
            -not $result.Stdout.Contains($script:RuntimeValue)
        )
        Assert-True -Name 'runtime absent from wrapper stderr' -Condition (
            -not $result.Stderr.Contains($script:RuntimeValue)
        )
        Assert-EvidenceContainsNoRuntimeValue -Path $result.Summary.evidence_path
    }

    Invoke-Case 'missing runtime value fails once and retains the initialized store' {
        $log = Join-Path $suiteRoot 'missing-runtime.log'
        $result = Invoke-TestRunner `
            -FakeExecutable $fake `
            -Seed $seed `
            -Mode 'success' `
            -InvocationLog $log `
            -RuntimePresent $false
        Assert-True -Name 'missing runtime is nonzero' -Condition ($result.ExitCode -ne 0)
        Assert-Equal -Name 'missing runtime code' -Expected 'TURN_01_PROCESS_FAILED' `
            -Actual $result.Summary.failure_code
        Assert-Equal -Name 'missing runtime has zero turns' -Expected 0 `
            -Actual $result.Summary.turns_succeeded
        Assert-Equal -Name 'missing runtime evidence retained' -Expected $true `
            -Actual $result.Summary.evidence_retained
        $lines = @([IO.File]::ReadAllLines($log, $script:Utf8NoBom))
        Assert-Equal -Name 'missing runtime attempted one dispatch' -Expected 1 -Actual @(
            $lines | Where-Object { $_ -eq 'chat-missing-runtime|turn=1|calls=0' }
        ).Count
        Assert-Equal -Name 'missing runtime made no model call' -Expected 0 -Actual @(
            $lines | Where-Object { $_ -like 'chat-new|*' }
        ).Count
    }

    Invoke-Case 'invalid environment name fails before creating evidence' {
        $log = Join-Path $suiteRoot 'invalid-name.log'
        $result = Invoke-TestRunner `
            -FakeExecutable $fake `
            -Seed $seed `
            -Mode 'success' `
            -InvocationLog $log `
            -RuntimeName 'INVALID-NAME'
        Assert-True -Name 'invalid name is nonzero' -Condition ($result.ExitCode -ne 0)
        Assert-Equal -Name 'invalid name code' `
            -Expected 'SECRET_ENVIRONMENT_NAME_INVALID' `
            -Actual $result.Summary.failure_code
        Assert-Equal -Name 'invalid name has no evidence' -Expected $false `
            -Actual $result.Summary.evidence_retained
        Assert-Equal -Name 'invalid name path null' -Expected $null `
            -Actual $result.Summary.evidence_path
        Assert-True -Name 'invalid name never invoked fake' -Condition (
            -not (Test-Path -LiteralPath $log)
        )
    }
} finally {
    foreach ($evidence in @($script:EvidenceRoots | Select-Object -Unique)) {
        Remove-TestTree -Path $evidence
    }
    if ($env:FREEAGENT_W1_REAL_TEST_KEEP -ceq '1') {
        Write-Host ('W1_REAL_CONVERSATION_TEST_FIXTURE ' + $suiteRoot)
    } else {
        Remove-TestTree -Path $suiteRoot
    }
}

if ($script:Failures.Count -gt 0) {
    $script:Failures | ForEach-Object { Write-Error $_ -ErrorAction Continue }
    throw (
        'W1_REAL_CONVERSATION_SELFTEST_FAIL ' +
        "cases=$($script:Cases) assertions=$($script:Assertions) " +
        "failures=$($script:Failures.Count)"
    )
}

Write-Host (
    'W1_REAL_CONVERSATION_SELFTEST_PASS ' +
    "cases=$($script:Cases) assertions=$($script:Assertions)"
)
