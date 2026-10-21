param(
    [string]$Go = '',
    [string]$Endpoint = 'https://api.deepseek.com',
    [string]$Model = 'deepseek-flash',
    # DeepSeek runs server-side web search only with deepseek-v4-pro.
    [string]$ResponsesModel = 'deepseek-v4-pro',
    [string]$Run = 'TestLiveProvider',
    # Optional portable zip; when set, the packaged CLI check reuses this key.
    [string]$Archive = ''
)

$ErrorActionPreference = 'Stop'

if ([string]::IsNullOrWhiteSpace($Go)) {
    $cachedGo = Join-Path $env:USERPROFILE '.cache\codex-go\1.26.5\go\bin\go.exe'
    $Go = if (Test-Path -LiteralPath $cachedGo -PathType Leaf) { $cachedGo } else { 'go' }
}

if ($Go -ne 'go') {
    $Go = (Resolve-Path -LiteralPath $Go).Path
}
if ($Archive -ne '') {
    $Archive = (Resolve-Path -LiteralPath $Archive).Path
}

$keyEnv = 'FREEAGENT_LIVE_DEEPSEEK_KEY'
$secure = $null
$bstr = [IntPtr]::Zero
$plain = $null
$output = @()
$exitCode = 1

Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    $secure = Read-Host 'Enter the newly rotated DeepSeek API key (hidden)' -AsSecureString
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
    $plain = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr)
    if ([string]::IsNullOrWhiteSpace($plain)) {
        throw 'No credential was entered; no request was dispatched.'
    }

    [Environment]::SetEnvironmentVariable($keyEnv, $plain, 'Process')
    $env:FREEAGENT_LIVE_ENDPOINT = $Endpoint
    $env:FREEAGENT_LIVE_MODEL = $Model
    $env:FREEAGENT_LIVE_RESPONSES_MODEL = $ResponsesModel
    $env:FREEAGENT_LIVE_KEY_ENV = $keyEnv
    $env:FREEAGENT_REQUIRE_LIVE = '1'

    $output = @(& $Go test -count=1 -timeout=30m -v ./internal/harness -run $Run 2>&1)
    $exitCode = $LASTEXITCODE
    $text = $output -join [Environment]::NewLine
    $containsCredential = $text.Contains($plain)
    if ($containsCredential) {
        $text = $text.Replace($plain, '[REDACTED]')
        Write-Warning 'The test output contained the active credential; it was redacted from displayed output.'
    }
    $text | Write-Output
    if ($containsCredential) {
        throw 'Live test output contained the active credential.'
    }
    if ($exitCode -ne 0) {
        throw "Live Provider tests failed with exit code $exitCode."
    }
    if ($Archive -ne '') {
        & (Join-Path $PSScriptRoot 'run-packaged-cli-acceptance-secure.ps1') -Archive $Archive -KeyEnv $keyEnv -ResponsesModel $ResponsesModel
        if (-not $?) {
            $exitCode = 1
            throw 'Packaged CLI live acceptance failed.'
        }
    }
}
finally {
    Pop-Location
    if ($bstr -ne [IntPtr]::Zero) {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
    }
    foreach ($name in @(
            $keyEnv,
            'FREEAGENT_LIVE_ENDPOINT',
            'FREEAGENT_LIVE_MODEL',
            'FREEAGENT_LIVE_RESPONSES_MODEL',
            'FREEAGENT_LIVE_KEY_ENV',
            'FREEAGENT_REQUIRE_LIVE')) {
        [Environment]::SetEnvironmentVariable($name, $null, 'Process')
    }
    if ($secure -ne $null) {
        $secure.Dispose()
    }
    $plain = $null
    $text = $null
    $output = @()
}

Write-Output 'Live Provider acceptance passed and process-only credential state was cleared.'
exit $exitCode
