param(
    [Parameter(Mandatory = $true)]
    [string]$Archive,
    [string]$Root = (Join-Path ([IO.Path]::GetTempPath()) ('freeagent-cli-live (spaces) & !-' + [guid]::NewGuid().ToString('N'))),
    [switch]$KeepRoot,
    # Process-only variable already holding the key, set by
    # run-live-acceptance-secure.ps1; otherwise the key is read hidden.
    [string]$KeyEnv = '',
    # DeepSeek runs server-side web search only with deepseek-v4-pro.
    [string]$ResponsesModel = 'deepseek-v4-pro'
)

$ErrorActionPreference = 'Stop'

function Fail([string]$Message) {
    throw $Message
}

function Quote-WindowsArgument([string]$Value) {
    return '"' + $Value.Replace('"', '\"') + '"'
}

function Invoke-Cli {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Executable,
        [Parameter(Mandatory = $true)]
        [string[]]$Arguments,
        [string]$InputText = '',
        [switch]$AllowFailure
    )

    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = $Executable
    $start.Arguments = (($Arguments | ForEach-Object { Quote-WindowsArgument $_ }) -join ' ')
    $start.WorkingDirectory = [IO.Path]::GetDirectoryName($Executable)
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardInput = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $start
    $null = $process.Start()
    try {
        if ($InputText -ne '') {
            $process.StandardInput.Write($InputText)
        }
        $process.StandardInput.Close()
        $stdout = $process.StandardOutput.ReadToEnd()
        $stderr = $process.StandardError.ReadToEnd()
        $process.WaitForExit()
        $result = [pscustomobject]@{
            exit_code = $process.ExitCode
            output = $stdout
            errors = $stderr
            text = ($stdout + "`n" + $stderr).Trim()
        }
        if (-not $AllowFailure -and $result.exit_code -ne 0) {
            Fail ("CLI command failed: {0}`n{1}" -f ($Arguments -join ' '), $result.text)
        }
        return $result
    } finally {
        $process.Dispose()
    }
}

function Invoke-CliJson {
    param(
        [string]$Executable,
        [string[]]$Arguments,
        [string]$InputText = '',
        [switch]$AllowFailure
    )
    $result = Invoke-Cli -Executable $Executable -Arguments $Arguments -InputText $InputText -AllowFailure:$AllowFailure
    try {
        $value = $result.output | ConvertFrom-Json
    } catch {
        Fail ("Expected JSON from CLI command: {0}`n{1}" -f ($Arguments -join ' '), $result.text)
    }
    [pscustomobject]@{
        result = $result
        value = $value
    }
}

function Assert-NoCredential([string]$Text, [string]$Credential, [string]$Label) {
    if ($Text.Contains($Credential)) {
        Fail "$Label contained the active credential."
    }
}

function Read-JsonFile([string]$Path) {
    return (Get-Content -LiteralPath $Path -Raw | ConvertFrom-Json)
}

function Get-RunStatus([object]$Value) {
    return [string]$Value.run.status
}

$Archive = (Resolve-Path -LiteralPath $Archive).Path
$Root = [IO.Path]::GetFullPath($Root)
if (Test-Path -LiteralPath $Root) {
    Fail 'Acceptance root must be new.'
}

$secure = $null
$bstr = [IntPtr]::Zero
$plain = $null
$package = $null
$keep = $false

try {
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    New-Item -ItemType Directory -Path $Root | Out-Null
    [IO.Compression.ZipFile]::ExtractToDirectory($Archive, $Root)
    $directories = @(Get-ChildItem -LiteralPath $Root -Directory)
    if ($directories.Count -ne 1) {
        Fail 'Expected exactly one portable package directory.'
    }
    $package = $directories[0].FullName
    $cli = Join-Path $package 'bin/freeagent.exe'
    $data = Join-Path $package 'data/Harness-v5'
    $workspace = Join-Path $package 'workspace'
    $keysPath = Join-Path $package 'data/keys.json'
    if (-not (Test-Path -LiteralPath $cli -PathType Leaf)) {
        Fail 'Portable CLI executable is missing.'
    }

    New-Item -ItemType Directory -Path $workspace | Out-Null
    if ($KeyEnv -ne '') {
        $plain = [Environment]::GetEnvironmentVariable($KeyEnv, 'Process')
    } else {
        $secure = Read-Host 'Enter the newly rotated DeepSeek API key (hidden; it is sent only to the local CLI stdin pipe)' -AsSecureString
        $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
        $plain = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr)
    }
    if ([string]::IsNullOrWhiteSpace($plain)) {
        Fail 'No credential was entered; no live request was dispatched.'
    }

    $init = Invoke-CliJson -Executable $cli -Arguments @('init', '--workspace', $workspace)
    if ($init.value.data_dir -ne $data -or $init.value.workspace -ne $workspace) {
        Fail 'Portable init selected the wrong data or workspace path.'
    }

    $add = Invoke-CliJson -Executable $cli -Arguments @('key', 'add', '--preset', 'deepseek', '--key-stdin') `
        -InputText ($plain + "`n")
    Assert-NoCredential $add.result.text $plain 'key add output'
    if ($add.value.name -ne 'DeepSeek' -or [string]::IsNullOrWhiteSpace($add.value.id)) {
        Fail 'DeepSeek preset key profile was not created.'
    }

    $test = Invoke-Cli -Executable $cli -Arguments @('key', 'test', 'DeepSeek')
    Assert-NoCredential $test.text $plain 'key test output'
    if ($test.exit_code -ne 0 -or $test.output -notmatch '(?m)^ok:\s+\d+\s+models') {
        Fail "key test did not pass: $($test.text)"
    }

    $models = Invoke-CliJson -Executable $cli -Arguments @('key', 'models', 'DeepSeek', '--json')
    Assert-NoCredential $models.result.text $plain 'key models output'
    $modelNames = @($models.value.models)
    if ($models.value.category -ne 'ok' -or $modelNames -notcontains 'deepseek-flash') {
        Fail 'deepseek-flash was not advertised by the live Provider.'
    }

    $configured = Invoke-CliJson -Executable $cli -Arguments @(
        'config', 'model', '--data-dir', $data, '--id', 'deepseek',
        '--key', 'DeepSeek', '--model', 'deepseek-flash'
    )
    Assert-NoCredential $configured.result.text $plain 'model configuration output'
    $shown = Invoke-CliJson -Executable $cli -Arguments @('config', 'show', '--data-dir', $data)
    if ($shown.value.default_model -ne 'deepseek' -or $shown.value.models.deepseek.model -ne 'deepseek-flash') {
        Fail 'The first live model did not become the default.'
    }

    $source = Join-Path $workspace 'source.md'
    [IO.File]::WriteAllText($source, "# Live CLI source`n`nThis sentence verifies packaged file reading.`n",
        [Text.UTF8Encoding]::new($false))
    $read = Invoke-CliJson -Executable $cli -Arguments @(
        'run', '--data-dir', $data, '--json', '--request', 'packaged-live-read',
        'Read source.md and summarize its content in one short sentence.'
    )
    Assert-NoCredential $read.result.text $plain 'live read output'
    if ((Get-RunStatus $read.value) -ne 'completed' -or [string]::IsNullOrWhiteSpace([string]$read.value.answer)) {
        Fail 'Packaged live file read/summarize did not complete.'
    }
    $readRunID = [string]$read.value.run.id

    $createPrompt = 'Create a file named note.txt in the workspace with exactly this content and no other changes: FREEAGENT_CLI_LIVE_APPROVED'
    $pending = Invoke-CliJson -Executable $cli -Arguments @(
        'run', '--data-dir', $data, '--json', '--request', 'packaged-live-create', $createPrompt
    ) -AllowFailure
    Assert-NoCredential $pending.result.text $plain 'pending create output'
    if ($pending.result.exit_code -ne 3 -or (Get-RunStatus $pending.value) -ne 'waiting_confirmation') {
        Fail 'Packaged file creation did not stop at exact confirmation.'
    }
    $pendingRunID = [string]$pending.value.run.id
    $preview = Invoke-CliJson -Executable $cli -Arguments @(
        'preview', '--data-dir', $data, '--run', $pendingRunID, '--json'
    )
    Assert-NoCredential $preview.result.text $plain 'approval preview output'
    $digest = [string]$preview.value.digest
    if ([string]::IsNullOrWhiteSpace($digest)) {
        Fail 'Approval preview did not return a digest.'
    }
    $approved = Invoke-CliJson -Executable $cli -Arguments @(
        'confirm', '--data-dir', $data, '--run', $pendingRunID,
        '--digest', $digest, '--approve', '--json'
    )
    Assert-NoCredential $approved.result.text $plain 'approval result output'
    $note = Join-Path $workspace 'note.txt'
    if ((Get-RunStatus $approved.value) -ne 'completed' -or
        -not (Test-Path -LiteralPath $note -PathType Leaf) -or
        (Get-Content -LiteralPath $note -Raw) -ne 'FREEAGENT_CLI_LIVE_APPROVED') {
        Fail 'Approved file creation did not produce the exact file.'
    }

    $declinePrompt = 'Create a file named declined.txt in the workspace with exactly this content and no other changes: FREEAGENT_CLI_LIVE_DECLINED'
    $declinedPending = Invoke-CliJson -Executable $cli -Arguments @(
        'run', '--data-dir', $data, '--json', '--request', 'packaged-live-decline', $declinePrompt
    ) -AllowFailure
    Assert-NoCredential $declinedPending.result.text $plain 'pending decline output'
    if ($declinedPending.result.exit_code -ne 3 -or (Get-RunStatus $declinedPending.value) -ne 'waiting_confirmation') {
        Fail 'Declined file creation did not stop at exact confirmation.'
    }
    $declinedRunID = [string]$declinedPending.value.run.id
    $declinedPreview = Invoke-CliJson -Executable $cli -Arguments @(
        'preview', '--data-dir', $data, '--run', $declinedRunID, '--json'
    )
    $declinedDigest = [string]$declinedPreview.value.digest
    $declined = Invoke-CliJson -Executable $cli -Arguments @(
        'confirm', '--data-dir', $data, '--run', $declinedRunID,
        '--digest', $declinedDigest, '--json'
    )
    Assert-NoCredential $declined.result.text $plain 'decline result output'
    if (Test-Path -LiteralPath (Join-Path $workspace 'declined.txt')) {
        Fail 'Declined file was written.'
    }

    # The DeepSeek preset suggests the Responses protocol and server-side
    # search for this model, so no capability flags are passed here.
    if ($modelNames -notcontains $ResponsesModel) {
        Fail "$ResponsesModel was not advertised by the live Provider."
    }
    $searchModel = Invoke-CliJson -Executable $cli -Arguments @(
        'config', 'model', '--data-dir', $data, '--id', 'deepseek-search',
        '--key', 'DeepSeek', '--model', $ResponsesModel
    )
    Assert-NoCredential $searchModel.result.text $plain 'search model configuration output'
    $searchShown = Invoke-CliJson -Executable $cli -Arguments @('config', 'show', '--data-dir', $data)
    $searchCaps = $searchShown.value.model_capabilities.'deepseek-search'
    if ($searchShown.value.default_model -ne 'deepseek' -or $searchCaps.protocol -ne 'responses' -or
        $searchCaps.web_search -ne 'supported') {
        Fail 'The search model did not receive the suggested Responses and web search capabilities.'
    }
    $searchPrompt = 'Use web search now to open the official DeepSeek API documentation site api-docs.deepseek.com and name one model listed on its Models & Pricing page. Answer in one short sentence.'
    $searched = Invoke-CliJson -Executable $cli -Arguments @(
        'run', '--data-dir', $data, '--json', '--model', 'deepseek-search',
        '--request', 'packaged-live-search', $searchPrompt
    )
    Assert-NoCredential $searched.result.text $plain 'live search output'
    if ((Get-RunStatus $searched.value) -ne 'completed' -or [string]::IsNullOrWhiteSpace([string]$searched.value.answer)) {
        Fail 'Packaged live Responses search did not complete.'
    }
    $searchRunID = [string]$searched.value.run.id
    $inspected = Invoke-CliJson -Executable $cli -Arguments @('inspect', '--data-dir', $data, '--run', $searchRunID)
    Assert-NoCredential $inspected.result.text $plain 'search inspect output'
    $searchCalls = @()
    foreach ($attempt in @($inspected.value.attempts)) {
        if ($attempt.kind -ne 'model' -or [string]::IsNullOrWhiteSpace([string]$attempt.response_json)) {
            continue
        }
        $saved = $attempt.response_json | ConvertFrom-Json
        if ($null -ne $saved.Provider -and $saved.Provider.protocol -eq 'responses') {
            $searchCalls += @($saved.Provider.web_search_calls | Where-Object {
                    $_.status -eq 'completed' -and $_.action -and ($_.query -or $_.url)
                })
        }
    }
    if ($searchCalls.Count -eq 0) {
        Fail 'Packaged live search run saved no completed web_search_call metadata.'
    }

    $doctor = Invoke-CliJson -Executable $cli -Arguments @('doctor', '--data-dir', $data)
    Assert-NoCredential $doctor.result.text $plain 'doctor output'
    $deepSeekDoctor = @($doctor.value.keys | Where-Object { $_.name -eq 'DeepSeek' })
    if (-not $doctor.value.ok -or $deepSeekDoctor.Count -ne 1 -or
        $deepSeekDoctor[0].last_check.result -ne 'ok') {
        Fail 'doctor did not report the successful DeepSeek key check.'
    }

    $scanFiles = @(Get-ChildItem -LiteralPath $package -File -Recurse -Force)
    $credentialFiles = @($scanFiles | Select-String -SimpleMatch -Pattern $plain -List)
    if ($credentialFiles.Count -ne 0) {
        Fail 'The active credential was found in the extracted package.'
    }

    # The kept acceptance root must not retain the test key, even encrypted.
    $removed = Invoke-CliJson -Executable $cli -Arguments @('key', 'remove', '--force', 'DeepSeek')
    Assert-NoCredential $removed.result.text $plain 'key remove output'
    $remaining = Invoke-Cli -Executable $cli -Arguments @('key', 'list')
    if ($removed.value.name -ne 'DeepSeek' -or $remaining.output -match 'DeepSeek') {
        Fail 'The test key profile was not removed from the acceptance package.'
    }

    $keep = $true
    [pscustomobject]@{
        ok = $true
        package = $package
        data_dir = $data
        workspace = $workspace
        keys_file = $keysPath
        model = 'deepseek-flash'
        advertised_model = $true
        key_test = $true
        default_model = $true
        live_read_run = $readRunID
        approved_create_run = $pendingRunID
        declined_create_run = $declinedRunID
        responses_model = $ResponsesModel
        search_run = $searchRunID
        completed_web_searches = $searchCalls.Count
        doctor = $true
        credential_scan = $true
        credential_in_output = $false
        key_removed = $true
        keep_root = [bool]$KeepRoot
    } | ConvertTo-Json
} finally {
    if ($bstr -ne [IntPtr]::Zero) {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
    }
    if ($secure -ne $null) {
        $secure.Dispose()
    }
    $plain = $null
    if (-not $KeepRoot -and -not $keep -and (Test-Path -LiteralPath $Root)) {
        Remove-Item -LiteralPath $Root -Recurse -Force -ErrorAction SilentlyContinue
    }
}
