param(
    [Parameter(Mandatory = $true)]
    [string]$Archive,
    [string]$Root = (Join-Path ([IO.Path]::GetTempPath()) ('freeagent-portable test (spaces) & !-' + [guid]::NewGuid().ToString('N')))
)

$ErrorActionPreference = 'Stop'
$Archive = (Resolve-Path -LiteralPath $Archive).Path
$Root = [IO.Path]::GetFullPath($Root)
if (Test-Path -LiteralPath $Root) { throw 'Portable test root must be new.' }
$archiveName = [IO.Path]::GetFileName($Archive)
$archiveChecksums = @(Get-Content -LiteralPath (Join-Path ([IO.Path]::GetDirectoryName($Archive)) 'SHA256SUMS.txt'))
$archiveDigest = (Get-FileHash -LiteralPath $Archive -Algorithm SHA256).Hash.ToLowerInvariant()
if ($archiveChecksums -notcontains "$archiveDigest  $archiveName") {
    throw 'Archive checksum mismatch.'
}
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip = [IO.Compression.ZipFile]::OpenRead($Archive)
try {
    foreach ($entry in $zip.Entries) {
        $destination = [IO.Path]::GetFullPath((Join-Path $Root $entry.FullName))
        if (-not $destination.StartsWith($Root.TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar,
                [StringComparison]::OrdinalIgnoreCase)) {
            throw 'Unsafe archive entry.'
        }
    }
} finally {
    $zip.Dispose()
}
[IO.Compression.ZipFile]::ExtractToDirectory($Archive, $Root)
$directories = @(Get-ChildItem -LiteralPath $Root -Directory)
if ($directories.Count -ne 1) { throw 'Expected one portable root.' }
$package = $directories[0].FullName
$manifest = Get-Content -LiteralPath (Join-Path $package 'manifest.json') -Raw | ConvertFrom-Json
if ($manifest.signed -or $manifest.installer -or $manifest.bundled_credentials -or $manifest.bundled_user_data) {
    throw 'Unexpected portable release claim or bundled state.'
}
if ($manifest.data_format -ne 'Harness-v5') { throw 'Portable manifest has the wrong data format.' }
$expected = @('SHA256SUMS.txt', 'manifest.json') + @($manifest.files.path)
$actual = @(Get-ChildItem -LiteralPath $package -File -Recurse | ForEach-Object {
    $_.FullName.Substring($package.Length + 1).Replace('\', '/')
})
if (@(Compare-Object ($expected | Sort-Object) ($actual | Sort-Object)).Count -ne 0) {
    throw 'Archive file set does not match its manifest.'
}
foreach ($file in $manifest.files) {
    $path = Join-Path $package $file.path
    if ((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() -ne $file.sha256 -or
        (Get-Item -LiteralPath $path).Length -ne $file.bytes) {
        throw "Payload integrity failed: $($file.path)"
    }
}
foreach ($line in Get-Content -LiteralPath (Join-Path $package 'SHA256SUMS.txt')) {
    if ($line -notmatch '^([a-f0-9]{64})  (.+)$') { throw 'Invalid checksum entry.' }
    $digest = $Matches[1]
    $path = Join-Path $package $Matches[2]
    if ((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() -ne $digest) {
        throw 'Package checksum mismatch.'
    }
}

$desktop = Join-Path $package 'FreeAgent.exe'
$cli = Join-Path $package 'bin/freeagent.exe'
function Get-PESubsystem([string]$Path) {
    $bytes = [IO.File]::ReadAllBytes($Path)
    $pe = [BitConverter]::ToInt32($bytes, 0x3C)
    return [BitConverter]::ToUInt16($bytes, $pe + 0x5C)
}
if ((Get-PESubsystem $desktop) -ne 2 -or (Get-PESubsystem $cli) -ne 3) {
    throw 'Portable PE subsystems are incorrect.'
}

function Invoke-Cli {
    param([string]$Executable, [string[]]$Arguments, [string]$InputText = $null, [switch]$AllowFailure)
    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = $Executable
    $start.WorkingDirectory = $Root
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardInput = $null -ne $InputText
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $start.Arguments = (($Arguments | ForEach-Object { Quote-WindowsArgument $_ }) -join ' ')
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $start
    try {
        $null = $process.Start()
        if ($null -ne $InputText) {
            $process.StandardInput.Write($InputText)
            $process.StandardInput.Close()
        }
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        $process.WaitForExit()
        $outputText = $stdoutTask.Result
        $errors = $stderrTask.Result
        $code = $process.ExitCode
        $output = if ([string]::IsNullOrEmpty($outputText)) {
            @()
        } else {
            @($outputText -split "`r?`n" | Where-Object { $_ -ne '' })
        }
    } finally {
        $process.Dispose()
    }
    if (-not $AllowFailure -and $code -ne 0) {
        throw "CLI command failed: $($Arguments -join ' ')`n$($output -join "`n")`n$errors"
    }
    [pscustomobject]@{
        exit_code = $code
        output = $output
        errors = $errors
        text = (($output -join "`n") + "`n" + $errors).Trim()
    }
}

function Invoke-CliJson {
    param([string]$Executable, [string[]]$Arguments)
    $result = Invoke-Cli -Executable $Executable -Arguments $Arguments
    return (($result.output -join "`n") | ConvertFrom-Json)
}

function Quote-WindowsArgument([string]$Value) {
    if ($Value -notmatch '[\s"]') {
        return $Value
    }
    return '"' + $Value.Replace('"', '\"') + '"'
}

function New-TestProcess {
    param([string]$FileName, [string[]]$Arguments)
    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = $FileName
    $start.Arguments = (($Arguments | ForEach-Object { Quote-WindowsArgument $_ }) -join ' ')
    $start.WorkingDirectory = $Root
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardInput = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $start
    $null = $process.Start()
    return $process
}

function Stop-TestProcess([Diagnostics.Process]$Process) {
    if ($null -ne $Process -and -not $Process.HasExited) {
        $Process.Kill()
        $null = $Process.WaitForExit(10000)
    }
    if ($null -ne $Process) { $Process.Dispose() }
}

$previousKeysFile = $env:FREEAGENT_KEYS_FILE
try {
    Remove-Item Env:FREEAGENT_KEYS_FILE -ErrorAction SilentlyContinue
    $desktopCheck = & (Join-Path $PSScriptRoot 'test-desktop-startup.ps1') -Package $package
    if (-not $desktopCheck.ok) { throw 'Fresh desktop startup regression failed.' }
    $workspace = Join-Path $package 'workspace'
    $data = [IO.Path]::GetFullPath((Join-Path $package 'data/Harness-v5'))
    $shown = Invoke-CliJson -Executable $cli -Arguments @('config', 'show', '--data-dir', $data)
    if ($shown.workspace -ne $workspace) { throw 'Desktop init did not select the portable workspace.' }
    if (@($shown.models.PSObject.Properties).Count -ne 0) { throw 'Portable init unexpectedly created a model.' }
    $cliData = Join-Path $Root 'explicit-cli-data'
    $init = Invoke-CliJson -Executable $cli -Arguments @('init', '--workspace', $workspace, '--data-dir', $cliData)
    if ($init.data_dir -ne $cliData) { throw 'Explicit CLI init did not select the requested data directory.' }
    $configPath = Join-Path $data 'config.json'
    $before = (Get-FileHash -LiteralPath $configPath -Algorithm SHA256).Hash
    $again = Invoke-Cli -Executable $cli -Arguments @('init', '--workspace', $workspace) -AllowFailure
    if ($again.exit_code -eq 0 -or (Get-FileHash -LiteralPath $configPath -Algorithm SHA256).Hash -ne $before) {
        throw 'Repeated init did not refuse or rewrote configuration.'
    }
    $occupied = Join-Path $Root 'occupied-data'
    New-Item -ItemType Directory -Path $occupied | Out-Null
    [IO.File]::WriteAllText((Join-Path $occupied 'unrelated.txt'), 'must stay unchanged')
    $occupiedResult = Invoke-Cli -Executable $cli -Arguments @('init', '--data-dir', $occupied, '--workspace', $workspace) -AllowFailure
    if ($occupiedResult.exit_code -eq 0 -or @(Get-ChildItem -LiteralPath $occupied -Force).Count -ne 1) {
        throw 'Nonempty data directory was accepted or changed.'
    }

    $sentinel = 'PORTABLE_SENTINEL_KEY'
    $keyResult = Invoke-Cli -Executable $cli -Arguments @(
        'key', 'add', '--name', 'portable', '--url', 'https://example.invalid', '--key-stdin'
    ) -InputText ($sentinel + "`n")
    if ($keyResult.exit_code -ne 0) { throw "Portable key add failed: $($keyResult.text)" }
    $keysPath = Join-Path $package 'data/keys.json'
    if (-not (Test-Path -LiteralPath $keysPath) -or (Get-Content -Raw $keysPath).Contains($sentinel)) {
        throw 'Portable key storage is missing or contains plaintext.'
    }
    $keyList = Invoke-CliJson -Executable $cli -Arguments @('key', 'list', '--json')
    $doctorResult = Invoke-Cli -Executable $cli -Arguments @('doctor')
    $doctor = $doctorResult.text | ConvertFrom-Json
    if (@($keyList).Count -ne 1 -or $keyList[0].name -ne 'portable' -or
        $doctor.keys_file -ne $keysPath -or $doctor.encrypted -ne $true -or
        $doctorResult.text -match [regex]::Escape($sentinel)) {
        throw 'Portable key metadata or encryption check failed.'
    }
    $null = Invoke-Cli -Executable $cli -Arguments @('key', 'remove', 'portable', '--force')

    $badRoot = Join-Path $Root 'bad-manifest'
    New-Item -ItemType Directory -Path (Join-Path $badRoot 'bin') -Force | Out-Null
    Copy-Item -LiteralPath $cli -Destination (Join-Path $badRoot 'bin/freeagent.exe')
    [IO.File]::WriteAllText((Join-Path $badRoot 'manifest.json'), '{"data_format":"Harness-v' + '3"}')
    $bad = Invoke-Cli -Executable (Join-Path $badRoot 'bin/freeagent.exe') -Arguments @('doctor') -AllowFailure
    if ($bad.exit_code -ne 2 -or $bad.text -notmatch 'manifest' -or (Test-Path (Join-Path $badRoot 'data'))) {
        throw 'Bad portable manifest was not refused.'
    }

    $version = Invoke-Cli -Executable $cli -Arguments @('--version')
    $copiedCli = Join-Path $Root 'freeagent.exe'
    Copy-Item -LiteralPath $cli -Destination $copiedCli
    $copiedVersion = Invoke-Cli -Executable $copiedCli -Arguments @('--version')
    $versionPrefix = '^freeagent version=' + [regex]::Escape($manifest.candidate) + '(\+dirty)? commit=[0-9a-f]{40} '
    if ($version.exit_code -ne 0 -or $copiedVersion.exit_code -ne 0 -or
        $version.text -notmatch $versionPrefix -or $copiedVersion.text -notmatch $versionPrefix) {
        throw 'CLI version check failed.'
    }

    $null = Invoke-Cli -Executable $cli -Arguments @('config', 'model', '--data-dir', $data, '--id', 'echo', '--provider', 'echo')
    $process = New-TestProcess -FileName $cli -Arguments @('web', '--data-dir', $data, '--port', '0', '--exit-on-stdin-close')
    try {
        $errors = $process.StandardError.ReadToEndAsync()
        $line = $process.StandardOutput.ReadLineAsync()
        if (-not $line.Wait(10000) -or $null -eq $line.Result) { throw 'Portable web engine did not print its address.' }
        if ($line.Result -notmatch '^http://127\.0\.0\.1:[0-9]+/#key=[a-f0-9]{64}$') {
            throw 'Portable web engine address format was invalid.'
        }
        $address = [uri]$line.Result
        $origin = $address.GetLeftPart([UriPartial]::Authority)
        $headers = @{ Origin = $origin; 'X-FreeAgent-Key' = $address.Fragment.Substring(5) }
        $page = Invoke-WebRequest -Uri "$origin/" -UseBasicParsing -TimeoutSec 5
        $environment = Invoke-RestMethod -Uri "$origin/api/environment" -Headers $headers -TimeoutSec 5
        $chat = Invoke-RestMethod -Uri "$origin/api/chats" -Headers $headers -Method Post `
            -ContentType 'application/json' -Body '{"title":"Portable engine check"}' -TimeoutSec 5
        $path = "$origin/api/chats/$($chat.id)"
        $null = Invoke-RestMethod -Uri "$path/submit" -Headers $headers -Method Post `
            -ContentType 'application/json' -Body '{"prompt":"portable engine reply","request_key":"portable-engine"}' -TimeoutSec 5
        $deadline = [DateTime]::UtcNow.AddSeconds(10)
        $completed = $false
        while ([DateTime]::UtcNow -lt $deadline) {
            $snapshot = Invoke-RestMethod -Uri $path -Headers $headers -TimeoutSec 5
            if (-not $snapshot.busy -and $snapshot.view.turns.Count -eq 1 -and
                $snapshot.view.turns[0].state -eq 'completed') {
                if ($snapshot.view.turns[0].messages[1].raw -ne 'Echo: portable engine reply') {
                    throw 'Portable engine returned the wrong offline answer.'
                }
                $completed = $true
                break
            }
            Start-Sleep -Milliseconds 20
        }
        if ($page.StatusCode -ne 200 -or -not $environment.environment -or -not $completed -or $process.HasExited) {
            throw 'Portable engine Chat acceptance failed.'
        }
        $process.StandardInput.Close()
        if (-not $process.WaitForExit(10000) -or $process.ExitCode -ne 0) {
            throw 'Portable engine did not exit cleanly after stdin close.'
        }
    } finally {
        Stop-TestProcess $process
    }

    $smokeKeys = Join-Path $Root 'smoke/keys.json'
    $env:FREEAGENT_KEYS_FILE = $smokeKeys
    $smoke = & (Join-Path $PSScriptRoot 'smoke.ps1') -Executable $cli -Root (Join-Path $Root 'smoke')
    if ($LASTEXITCODE -ne 0) { throw 'Extracted portable binary smoke failed.' }
    [pscustomobject]@{
        ok = $true
        root = $Root
        integrity = $true
        no_bundled_state = $true
        subsystems = $true
        fresh_desktop_launch = $desktopCheck.fresh_desktop_launch
        desktop_reopen = $desktopCheck.desktop_reopen
        desktop_engine_path = $desktopCheck.desktop_engine_path
        desktop_http = $desktopCheck.desktop_http
        desktop_close = $desktopCheck.desktop_close
        portable_init = $true
        init_refuses_existing = $true
        portable_keys = $true
        bad_manifest_refused = $true
        explicit_cli_version = $true
        engine_web_chat = $true
        engine_exit_on_stdin_close = $true
        extracted_binary_smoke = $true
    } | ConvertTo-Json
} finally {
    if ($null -eq $previousKeysFile) {
        Remove-Item Env:FREEAGENT_KEYS_FILE -ErrorAction SilentlyContinue
    } else {
        $env:FREEAGENT_KEYS_FILE = $previousKeysFile
    }
}
