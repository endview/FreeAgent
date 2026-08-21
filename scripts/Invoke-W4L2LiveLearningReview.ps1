[CmdletBinding()]
param(
    [string]$GoCommand = '',
    [string]$CredentialEnvironment = 'FREEAGENT_DEEPSEEK_API_KEY'
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot

if ($CredentialEnvironment -ne 'FREEAGENT_DEEPSEEK_API_KEY') {
    throw 'W4-L2 live review only accepts the frozen credential environment name'
}

if ([string]::IsNullOrWhiteSpace($GoCommand)) {
    $go = Get-Command go -ErrorAction SilentlyContinue
    if ($null -ne $go) {
        $GoCommand = $go.Source
    } else {
        $bundled = Join-Path $env:USERPROFILE '.cache\codex-go\1.26.5\go\bin\go.exe'
        if (Test-Path -LiteralPath $bundled -PathType Leaf) {
            $GoCommand = $bundled
        } else {
            throw 'Go executable was not found; pass -GoCommand explicitly'
        }
    }
}
if (-not (Test-Path -LiteralPath $GoCommand -PathType Leaf)) {
    throw 'the selected Go executable does not exist'
}

$previousLiveGate = [Environment]::GetEnvironmentVariable(
    'FREEAGENT_W4_L2_LIVE',
    [EnvironmentVariableTarget]::Process
)
try {
    [Environment]::SetEnvironmentVariable(
        'FREEAGENT_W4_L2_LIVE',
        '1',
        [EnvironmentVariableTarget]::Process
    )
    Push-Location $repoRoot
    try {
        & $GoCommand test -count=1 -v -timeout=6m `
            -run '^TestW4L2LiveDeepSeekLearningReview$' ./cmd/freeagent
        if ($LASTEXITCODE -ne 0) {
            throw "W4-L2 live review failed with exit code $LASTEXITCODE"
        }
    } finally {
        Pop-Location
    }
} finally {
    [Environment]::SetEnvironmentVariable(
        'FREEAGENT_W4_L2_LIVE',
        $previousLiveGate,
        [EnvironmentVariableTarget]::Process
    )
}
