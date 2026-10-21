param(
    [string]$Executable = (Join-Path $PSScriptRoot '../bin/freeagent.exe'),
    [string]$LocalCallerExecutable = '',
    [string]$Root = (Join-Path ([IO.Path]::GetTempPath()) ('freeagent-harness-smoke-' + [guid]::NewGuid().ToString('N')))
)

$ErrorActionPreference = 'Stop'
$Executable = (Resolve-Path -LiteralPath $Executable).Path
$Root = [IO.Path]::GetFullPath($Root)
if (Test-Path -LiteralPath $Root) { throw 'Smoke root must be a new directory' }
$workspace = Join-Path $Root 'workspace'
$data = Join-Path $Root 'state'
$backup = Join-Path $Root 'backup'
$restored = Join-Path $Root 'restored'
New-Item -ItemType Directory -Path $workspace | Out-Null
# Key profiles live outside data directories; keep the smoke run's own.
$keysFile = Join-Path $Root 'keys.json'
$previousKeysFile = $env:FREEAGENT_KEYS_FILE
$env:FREEAGENT_KEYS_FILE = $keysFile

function Quote-WindowsArgument([string]$Value) {
    if ($Value -notmatch '[\s"]') {
        return $Value
    }
    return '"' + $Value.Replace('"', '\"') + '"'
}

function Invoke-CliProcess {
    param([string[]]$Arguments)
    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = $Executable
    $start.WorkingDirectory = $Root
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $start.Arguments = (($Arguments | ForEach-Object { Quote-WindowsArgument $_ }) -join ' ')
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $start
    try {
        $null = $process.Start()
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        $process.WaitForExit()
        $stdout = $stdoutTask.Result
        $stderr = $stderrTask.Result
        $code = $process.ExitCode
    } finally {
        $process.Dispose()
    }
    if ($code -ne 0) {
        throw "Harness command failed: $($Arguments -join ' ')`n$stdout`n$stderr"
    }
    return $stdout
}

function Invoke-Harness {
    param([string[]]$Arguments)
    return ((Invoke-CliProcess -Arguments $Arguments) | ConvertFrom-Json)
}

function Set-Utf8NoBomContent {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,
        [Parameter(Mandatory = $true)]
        [string]$Content
    )
    [IO.File]::WriteAllText($Path, $Content, [Text.UTF8Encoding]::new($false))
}

try {
$version = & $Executable --version
if ($LASTEXITCODE -ne 0) { throw 'Version check failed' }
$null = Invoke-Harness -Arguments @('init', '--data-dir', $data, '--workspace', $workspace)
$echo = Invoke-Harness -Arguments @('config', 'model', '--data-dir', $data, '--id', 'default', '--provider', 'echo')
if ($echo.default_model -ne 'default') { throw 'The first model did not become the default' }
$first = Invoke-Harness -Arguments @('run', '--data-dir', $data, '--json', '--request', 'first', 'hello')
if ($first.run.status -ne 'completed' -or $first.answer -ne 'Echo: hello') { throw 'First run did not complete' }
$session = $first.run.conversation_id
$duplicate = Invoke-Harness -Arguments @('run', '--data-dir', $data, '--session', $session, '--request', 'first', '--json', 'hello')
if (-not $duplicate.duplicate -or $duplicate.run.id -ne $first.run.id) { throw 'Request was not deduplicated' }
$key = Invoke-Harness -Arguments @('key', 'add', '--name', 'smoke', '--url', 'https://example.invalid',
    '--key-env', 'FREEAGENT_SMOKE_UNRESOLVED_KEY')
$configured = Invoke-Harness -Arguments @('config', 'model', '--data-dir', $data, '--id', 'smoke', '--key', 'smoke', '--model', 'smoke-model')
$shown = Invoke-Harness -Arguments @('config', 'show', '--data-dir', $data)
if (-not $configured.changed -or $shown.changed -or $shown.network_tested -or $shown.credentials_resolved -or
    -not $key.id -or $shown.models.smoke.key -ne $key.id -or $shown.models.smoke.model -ne 'smoke-model' -or
    $shown.default_model -ne 'default') {
    throw 'Offline model configuration failed'
}
$switchedHistory = @(Invoke-Harness -Arguments @('sessions', '--data-dir', $data, '--show', $session))
if ($switchedHistory.Count -ne 2) { throw 'Model configuration lost conversation history' }
$reset = Invoke-Harness -Arguments @('config', 'model', '--data-dir', $data, '--id', 'smoke', '--remove')
if ($reset.models.PSObject.Properties.Name -contains 'smoke' -or $reset.default_model -ne 'default' -or
    $reset.models.default.provider -ne 'echo') {
    throw 'Removing a model failed or changed the default'
}
$second = Invoke-Harness -Arguments @('run', '--data-dir', $data, '--session', $session, '--request', 'second', '--json', 'again')
if ($second.answer -ne 'Echo: again') { throw 'Second conversation turn failed' }
$sessions = @(Invoke-Harness -Arguments @('sessions', '--data-dir', $data))
$history = @(Invoke-Harness -Arguments @('sessions', '--data-dir', $data, '--show', $session))
if ($sessions.Count -ne 1 -or $history.Count -ne 4) { throw 'Conversation persistence failed' }
$null = Invoke-Harness -Arguments @('config', 'model', '--data-dir', $data, '--provider', 'echo', '--strength', 'strong')
$null = Invoke-Harness -Arguments @('config', 'model', '--data-dir', $data, '--id', 'light', '--provider', 'echo', '--strength', 'light')
$null = Invoke-Harness -Arguments @('config', 'auto', '--data-dir', $data, '--enabled', '--allow-model', 'default', '--allow-model', 'light')
$autoRun = Invoke-Harness -Arguments @('run', '--data-dir', $data, '--model', 'auto', '--json', '--request', 'auto', 'hello')
if ($autoRun.run.model_id -ne 'light' -or $autoRun.run.routing[0].mode -ne 'auto') { throw 'Auto selection failed' }
$manualRun = Invoke-Harness -Arguments @('run', '--data-dir', $data, '--session', $autoRun.run.conversation_id,
    '--model', 'default', '--json', '--request', 'manual', 'hello')
if ($manualRun.run.model_id -ne 'default' -or $manualRun.run.routing[0].mode -ne 'manual') { throw 'Manual selection was overridden' }
$null = Invoke-Harness -Arguments @('config', 'auto', '--data-dir', $data, '--enabled=false')
$memoryRun = Invoke-Harness -Arguments @('run', '--data-dir', $data, '--json', '--request', 'memory-first', 'no automatic memory')
$memorySession = $memoryRun.run.conversation_id
function Invoke-Memory {
    param([string]$Operation, [string[]]$Arguments = @())
    return Invoke-Harness -Arguments (@('memory', $Operation, '--data-dir', $data, '--session', $memorySession) + $Arguments)
}
$emptyMemory = @(Invoke-Memory -Operation 'list')
if ($emptyMemory.Count -ne 0) { throw 'Ordinary chat silently saved retained memory' }
$preview = Invoke-Memory -Operation 'save' -Arguments @('--title', 'Selected note', '--text', 'PRIVATE_SMOKE_V1', '--request', 'private-smoke')
if ($preview.saved -or $preview.preview.body -ne 'PRIVATE_SMOKE_V1') { throw 'Memory preview failed' }
$emptyMemory = @(Invoke-Memory -Operation 'list')
if ($emptyMemory.Count -ne 0) { throw 'A preview persisted memory' }
$private = Invoke-Memory -Operation 'save' -Arguments @('--title', 'Selected note', '--text', 'PRIVATE_SMOKE_V1', '--request', 'private-smoke', '--yes')
$privateDuplicate = Invoke-Memory -Operation 'save' -Arguments @('--title', 'Selected note', '--text', 'PRIVATE_SMOKE_V1', '--request', 'private-smoke', '--yes')
if ($private.revision -ne 1 -or -not $privateDuplicate.duplicate -or $privateDuplicate.entry_id -ne $private.entry_id) {
    throw 'Private memory idempotency failed'
}
$null = Invoke-Memory -Operation 'select' -Arguments @('--entry', $private.entry_id)
$selectedMemoryRun = Invoke-Harness -Arguments @('run', '--data-dir', $data, '--session', $memorySession,
    '--json', '--request', 'memory-selected', 'use selected context')
$sources = @(Invoke-Memory -Operation 'sources' -Arguments @('--run', $selectedMemoryRun.run.id))
if ($sources.Count -ne 1 -or $sources[0].used.entry_id -ne $private.entry_id -or
    $sources[0].used.revision -ne 1 -or $sources[0].current_state -ne 'active') {
    throw 'Actual selected memory source was not recorded'
}
$null = Invoke-Memory -Operation 'update' -Arguments @('--entry', $private.entry_id, '--revision', '1',
    '--text', 'PRIVATE_SMOKE_V2', '--request', 'private-update', '--yes')
$null = Invoke-Memory -Operation 'disable' -Arguments @('--entry', $private.entry_id, '--revision', '2',
    '--request', 'private-disable', '--yes')
$disabledMemoryRun = Invoke-Harness -Arguments @('run', '--data-dir', $data, '--session', $memorySession,
    '--model', 'light', '--json', '--request', 'memory-disabled', 'disabled source status')
$disabledSources = @(Invoke-Memory -Operation 'sources' -Arguments @('--run', $disabledMemoryRun.run.id))
if ($disabledSources.Count -ne 1 -or $disabledSources[0].used.state -ne 'disabled' -or
    $disabledSources[0].current_state -ne 'disabled' -or $disabledSources[0].used.sha256) {
    throw 'Disabled entry was not represented as an inactive source'
}
$null = Invoke-Memory -Operation 'enable' -Arguments @('--entry', $private.entry_id, '--revision', '3',
    '--request', 'private-enable', '--yes')
$privateCurrent = Invoke-Memory -Operation 'read' -Arguments @('--entry', $private.entry_id)
if ($privateCurrent.body -ne 'PRIVATE_SMOKE_V2' -or $privateCurrent.revision -ne 4) { throw 'Private enable lost content' }
$null = Invoke-Harness -Arguments @('config', 'memory', '--data-dir', $data, '--knowledge')
$export = Invoke-Memory -Operation 'copy' -Arguments @('--from-entry', $private.entry_id,
    '--scope', 'knowledge', '--owner', 'explicit-copy', '--request', 'private-export', '--yes')
$exported = Invoke-Memory -Operation 'read' -Arguments @('--scope', 'knowledge', '--owner', 'explicit-copy', '--entry', $export.entry_id)
if ($exported.body -ne 'PRIVATE_SMOKE_V2' -or $exported.sources[0].entry_id -ne $private.entry_id -or
    $exported.sources[0].revision -ne 4) { throw 'Explicit private copy lost its revision source' }
$document = Join-Path $workspace 'selected.md'
Set-Utf8NoBomContent -Path $document -Content "# Selected document`nKNOWLEDGE_SMOKE_V1"
$import = Invoke-Memory -Operation 'import' -Arguments @('--scope', 'knowledge', '--owner', 'project-notes',
    '--file', $document, '--request', 'import-one', '--yes')
$importDuplicate = Invoke-Memory -Operation 'import' -Arguments @('--scope', 'knowledge', '--owner', 'project-notes',
    '--file', $document, '--request', 'import-two', '--yes')
if (-not $importDuplicate.duplicate -or $importDuplicate.entry_id -ne $import.entry_id) { throw 'Repeated file content was not deduplicated' }
$hits = @(Invoke-Memory -Operation 'search' -Arguments @('--scope', 'knowledge', '--owner', 'project-notes', '--query', 'KNOWLEDGE_SMOKE_V1'))
if ($hits.Count -ne 1 -or $hits[0].source.revision -ne 1 -or $hits[0].end_byte -le $hits[0].start_byte) {
    throw 'Knowledge search/source range failed'
}
Set-Utf8NoBomContent -Path $document -Content "# Selected document`nKNOWLEDGE_SMOKE_V2"
$null = Invoke-Memory -Operation 'import' -Arguments @('--scope', 'knowledge', '--owner', 'project-notes',
    '--file', $document, '--update', '--entry', $import.entry_id, '--revision', '1', '--request', 'import-update', '--yes')
$oldHits = @(Invoke-Memory -Operation 'search' -Arguments @('--scope', 'knowledge', '--owner', 'project-notes', '--query', 'KNOWLEDGE_SMOKE_V1'))
$hits = @(Invoke-Memory -Operation 'search' -Arguments @('--scope', 'knowledge', '--owner', 'project-notes', '--query', 'KNOWLEDGE_SMOKE_V2'))
if ($oldHits.Count -ne 0 -or $hits.Count -ne 1 -or $hits[0].source.revision -ne 2) { throw 'Knowledge update retained an obsolete search result' }
$null = Invoke-Memory -Operation 'disable' -Arguments @('--scope', 'knowledge', '--owner', 'project-notes',
    '--entry', $import.entry_id, '--revision', '2', '--request', 'knowledge-disable', '--yes')
$disabledHits = @(Invoke-Memory -Operation 'search' -Arguments @('--scope', 'knowledge', '--owner', 'project-notes', '--query', 'KNOWLEDGE_SMOKE_V2'))
if ($disabledHits.Count -ne 0) { throw 'Disabled knowledge still appeared in search' }
$null = Invoke-Memory -Operation 'enable' -Arguments @('--scope', 'knowledge', '--owner', 'project-notes',
    '--entry', $import.entry_id, '--revision', '3', '--request', 'knowledge-enable', '--yes')
$null = Invoke-Memory -Operation 'delete' -Arguments @('--scope', 'knowledge', '--owner', 'project-notes',
    '--entry', $import.entry_id, '--revision', '4', '--request', 'knowledge-delete', '--yes')
$deletedHits = @(Invoke-Memory -Operation 'search' -Arguments @('--scope', 'knowledge', '--owner', 'project-notes', '--query', 'KNOWLEDGE_SMOKE_V2'))
if ($deletedHits.Count -ne 0) { throw 'Deleted knowledge remained indexed' }
$null = Invoke-Memory -Operation 'clear'
$null = Invoke-Harness -Arguments @('config', 'memory', '--data-dir', $data, '--knowledge=false')
$memoryOffRun = Invoke-Harness -Arguments @('run', '--data-dir', $data, '--session', $memorySession,
    '--json', '--request', 'memory-off', 'plain chat after clearing memory')
$offSources = @(Invoke-Memory -Operation 'sources' -Arguments @('--run', $memoryOffRun.run.id))
if ($memoryOffRun.run.status -ne 'completed' -or $offSources.Count -ne 0) { throw 'Cleared/disabled extensions blocked plain chat' }
$null = Invoke-Harness -Arguments @('config', 'commands', '--data-dir', $data, '--enabled', '--allow', $Executable)
$null = Invoke-Harness -Arguments @('config', 'http', '--data-dir', $data, '--enabled', '--origin', 'http://127.0.0.1:1')
$selected = Invoke-Harness -Arguments @('tools', 'list', '--data-dir', $data)
if ($selected.network_tested -or -not ($selected.builtin.Name -contains 'http_get') -or
    -not ($selected.builtin.Name -contains 'command_run')) { throw 'Offline optional selection failed' }
$null = Invoke-Harness -Arguments @('config', 'http', '--data-dir', $data, '--enabled=false')
$null = Invoke-Harness -Arguments @('config', 'commands', '--data-dir', $data, '--enabled=false')
$removed = Invoke-Harness -Arguments @('tools', 'list', '--data-dir', $data)
if ($removed.builtin.Name -contains 'http_get' -or $removed.builtin.Name -contains 'command_run') {
    throw 'Optional tool removal failed'
}
$callerVerified = $false
if ($LocalCallerExecutable) {
    $LocalCallerExecutable = (Resolve-Path -LiteralPath $LocalCallerExecutable).Path
    $callerOutput = & $LocalCallerExecutable --data-dir $data 'second local caller'
    if ($LASTEXITCODE -ne 0) { throw 'Local caller failed' }
    $caller = ($callerOutput -join "`n") | ConvertFrom-Json
    if ($caller.run.status -ne 'completed' -or $caller.answer -ne 'Echo: second local caller') {
        throw 'Local caller did not reuse the task service'
    }
    $callerVerified = $true
}
$null = Invoke-Harness -Arguments @('config', 'collaboration', '--data-dir', $data, '--mode', 'ask')
$planPath = Join-Path $Root 'plan.json'
$plan = @{
    objective = 'Controlled foreground smoke'
    budget = @{ max_children = 1; max_model_calls = 4; max_tool_calls = 2; max_output_bytes = 32768; max_concurrent_children = 1; max_active_seconds = 300 }
    roles = @{
        reviewer = @{
            instructions = 'Read only explicitly selected facts.'
            tools = @('file_read')
            paths = @('facts.txt')
            output_format = 'json'
            budget = @{ max_steps = 4; max_seconds = 30; max_output_bytes = 16384; max_call_output_bytes = 4096 }
        }
    }
    children = @(@{ name = 'one'; role = 'reviewer'; objective = 'Inspect facts'; context = 'Explicit smoke context' })
    parallel = $false
}
$planJson = $plan | ConvertTo-Json -Depth 8
Set-Utf8NoBomContent -Path $planPath -Content $planJson
$created = Invoke-Harness -Arguments @('collab', 'create', '--data-dir', $data, '--plan', $planPath)
if (-not $created.parent_run -or $created.continued) { throw 'Collaboration dispatched during creation' }
$shared = Invoke-Memory -Operation 'copy' -Arguments @('--from-entry', $private.entry_id,
    '--scope', 'shared', '--owner', $created.parent_run, '--kind', 'finding', '--request', 'shared-export', '--yes')
$sharedEntry = Invoke-Memory -Operation 'read' -Arguments @('--scope', 'shared', '--owner', $created.parent_run, '--entry', $shared.entry_id)
if ($sharedEntry.body -ne 'PRIVATE_SMOKE_V2' -or $sharedEntry.author -ne 'user') { throw 'Task shared copy failed' }
$null = Invoke-Harness -Arguments @('collab', 'control', '--data-dir', $data, '--parent', $created.parent_run, '--child', 'one', '--action', 'pause')
$paused = Invoke-Harness -Arguments @('collab', 'inspect', '--data-dir', $data, '--parent', $created.parent_run)
if ($paused.children[0].control -ne 'paused' -or $paused.usage.model_calls -ne 0) { throw 'Pause did not preserve undispatched work' }
$null = Invoke-Harness -Arguments @('collab', 'control', '--data-dir', $data, '--parent', $created.parent_run, '--action', 'cancel')
$cancelled = Invoke-Harness -Arguments @('collab', 'inspect', '--data-dir', $data, '--parent', $created.parent_run)
if ($cancelled.parent.status -ne 'abandoned' -or $cancelled.children[0].run.status -ne 'abandoned') { throw 'Parent cancellation did not reach child' }
$null = Invoke-Harness -Arguments @('config', 'collaboration', '--data-dir', $data, '--mode', 'off')
$doctor = Invoke-Harness -Arguments @('doctor', '--data-dir', $data)
if ($doctor.keys_file -ne $keysFile -or $null -eq $doctor.encrypted -or @($doctor.keys).Count -ne 1) {
    throw 'Doctor did not report the isolated key file'
}
$keyList = @(Invoke-Harness -Arguments @('key', 'list', '--json'))
if ($keyList.Count -ne 1 -or $keyList[0].name -ne 'smoke' -or $keyList[0].source -ne 'env' -or
    @($keyList | Where-Object { $_.PSObject.Properties.Name -contains 'data' }).Count -ne 0) {
    throw 'Key list was not limited to profile metadata'
}
$backupResult = Invoke-Harness -Arguments @('backup', '--data-dir', $data, '--out', $backup)
$verify = Invoke-Harness -Arguments @('backup-verify', '--source', $backup)
$restoreResult = Invoke-Harness -Arguments @('restore', '--source', $backup, '--data-dir', $restored)
$restoredDoctor = Invoke-Harness -Arguments @('doctor', '--data-dir', $restored)
$restoredHistory = @(Invoke-Harness -Arguments @('sessions', '--data-dir', $restored, '--show', $session))
if (-not ($doctor.ok -and $backupResult.ok -and $verify.ok -and $restoreResult.ok -and $restoredDoctor.ok) -or $restoredHistory.Count -ne 4) {
    throw 'Backup/verify/restore did not preserve the conversation'
}
$restoredPlan = Invoke-Harness -Arguments @('collab', 'inspect', '--data-dir', $restored, '--parent', $created.parent_run)
if ($restoredPlan.parent.status -ne 'abandoned' -or $restoredPlan.usage.model_calls -ne 0) { throw 'Collaboration records were not restored' }
$restoredPrivate = Invoke-Harness -Arguments @('memory', 'read', '--data-dir', $restored, '--session', $memorySession, '--entry', $private.entry_id)
$restoredShared = Invoke-Harness -Arguments @('memory', 'read', '--data-dir', $restored, '--session', $memorySession,
    '--scope', 'shared', '--owner', $created.parent_run, '--entry', $shared.entry_id)
$restoredKnowledge = Invoke-Harness -Arguments @('memory', 'read', '--data-dir', $restored, '--session', $memorySession,
    '--scope', 'knowledge', '--owner', 'explicit-copy', '--entry', $export.entry_id)
$restoredDeleted = @(Invoke-Harness -Arguments @('memory', 'list', '--data-dir', $restored, '--session', $memorySession,
    '--scope', 'knowledge', '--owner', 'project-notes'))
$restoredSources = @(Invoke-Harness -Arguments @('memory', 'sources', '--data-dir', $restored, '--run', $selectedMemoryRun.run.id))
if ($restoredPrivate.body -ne 'PRIVATE_SMOKE_V2' -or $restoredPrivate.revision -ne 4 -or
    $restoredShared.body -ne 'PRIVATE_SMOKE_V2' -or $restoredKnowledge.body -ne 'PRIVATE_SMOKE_V2' -or
    $restoredDeleted.Count -ne 0 -or $restoredSources[0].used.revision -ne 1 -or $restoredSources[0].current_state -ne 'updated') {
    throw 'Backup/restore lost memory revisions, deletion or source history'
}
[pscustomobject]@{
    ok = $true
    version = $version
    root = $Root
    session = $session
    run_status = $first.run.status
    request_deduplicated = $duplicate.duplicate
    model_switch_preserved_history = $true
    configuration_offline = $true
    keys_file_isolated = $true
    auto_manual_precedence_offline = $true
    optional_tools_select_remove_offline = $true
    local_caller_verified = $callerVerified
    collaboration_controls_restore_offline = $true
    memory_private_selection_sources_offline = $true
    memory_explicit_copy_shared_offline = $true
    knowledge_import_dedup_update_delete_offline = $true
    memory_restore_sources_offline = $true
    history_messages = $history.Count
    restored_messages = $restoredHistory.Count
} | ConvertTo-Json
} finally {
    if ($null -eq $previousKeysFile) {
        Remove-Item Env:FREEAGENT_KEYS_FILE -ErrorAction SilentlyContinue
    } else {
        $env:FREEAGENT_KEYS_FILE = $previousKeysFile
    }
}
