[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$Root
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# P0 money-budget removal gate.
#
# FAC2 has no money semantics: no BudgetPolicy, no CostPolicy, no PriceSnapshot,
# no estimated/reported/reconciled cost, no billing version and no currency.
# This gate keeps those identifiers from flowing back into the tree.
#
# Non-money capacity limits (context window budget, result-size budget, run
# duration/step budget, path-name byte budget) are deliberately NOT banned.
# Only identifiers with unambiguous money or retired-port semantics appear
# below.

$script:BannedTokens = @(
    'PriceSnapshot',
    'price_snapshot',
    'model-price-snapshot',
    'BudgetPolicy',
    'budget_policy',
    'CostPolicy',
    'cost_policy',
    'estimated_cost',
    'provider_reported_cost',
    'reconciled_cost',
    'max_cost_micros',
    'max_run_cost_microunits',
    'run-cost-budget',
    'microunits',
    'billing_version',
    'BillingVersion',
    'pricing_mode',
    'FROZEN_SNAPSHOT',
    'deepseekcost',
    'ActionBudgetDecision',
    'ChannelBudgetDecision',
    'BudgetStateRef',
    'budget_state_ref',
    'BUDGET_UNKNOWN',
    'model.generate/v1',
    'model-binding-config/v1'
)

# Every exemption is an exact repository-relative path with a stated reason and
# a ceiling on how many lines may still carry a banned token. Directory-wide or
# glob exemptions are intentionally impossible.
$script:Exemptions = @(
    [pscustomobject]@{
        Path = 'scripts/Test-MoneyBanList.ps1'
        MaxLines = 28
        Reason = 'This gate declares the ban list itself.'
    },
    [pscustomobject]@{
        Path = 'internal/currentstore/migrations/0001_current.sql'
        MaxLines = 24
        Reason = 'Frozen FAC1 migration; byte-identical history, never modified.'
    },
    [pscustomobject]@{
        Path = 'examples/bootstrap-artifacts/freeagent.builtin.model.deepseek/1.0.0/schemas/config.schema.json'
        MaxLines = 1
        Reason = 'Frozen FAC1 artifact; digest is pinned by historical acceptance evidence.'
    },
    [pscustomobject]@{
        Path = 'examples/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/schemas/config.schema.json'
        MaxLines = 1
        Reason = 'Frozen FAC1 artifact; digest is pinned by historical acceptance evidence.'
    },
    [pscustomobject]@{
        Path = 'examples/bootstrap-artifacts/freeagent.builtin.model.echo/1.0.0/README.md'
        MaxLines = 1
        Reason = 'Frozen FAC1 artifact; digest is pinned by historical acceptance evidence.'
    },
    [pscustomobject]@{
        Path = 'sdk/moduleapi/model_v1_test.go'
        MaxLines = 2
        Reason = 'Asserts the retired model.generate/v1 port is rejected; reverse-flow protection.'
    },
    [pscustomobject]@{
        Path = 'sdk/moduleapi/model_binding_config_v1_test.go'
        MaxLines = 3
        Reason = 'Asserts retired money fields and the v1 config schema are rejected; reverse-flow protection.'
    },
    [pscustomobject]@{
        Path = 'internal/s3audit/audit_test.go'
        MaxLines = 1
        Reason = 'Asserts audit output never leaks retired cost fields; reverse-flow protection.'
    },
    [pscustomobject]@{
        Path = 'docs/CURRENT_CAPABILITIES.md'
        MaxLines = 4
        Reason = 'Carries the money-boundary declaration plus frozen FAC1 acceptance evidence.'
    },
    [pscustomobject]@{
        Path = 'docs/PRD.md'
        MaxLines = 5
        Reason = 'Carries the money-boundary declaration plus frozen FAC1 acceptance evidence.'
    },
    [pscustomobject]@{
        Path = 'docs/PROJECT_STATUS.md'
        MaxLines = 3
        Reason = 'Carries the money-boundary declaration plus frozen FAC1 acceptance evidence.'
    },
    [pscustomobject]@{
        Path = 'docs/specs/CORE_RUNTIME_V1.md'
        MaxLines = 2
        Reason = 'Carries the money-boundary declaration plus frozen FAC1 acceptance evidence.'
    },
    [pscustomobject]@{
        Path = 'docs/specs/CURRENT_STORE_V1.md'
        MaxLines = 5
        Reason = 'Carries the money-boundary declaration plus frozen FAC1 acceptance evidence.'
    },
    [pscustomobject]@{
        Path = 'docs/CUTOVER_ACCEPTANCE.md'
        MaxLines = 19
        Reason = 'One-time historical acceptance evidence; not rewritten.'
    },
    [pscustomobject]@{
        Path = 'docs/S3C_REAL_EXPERIMENT.md'
        MaxLines = 1
        Reason = 'One-time historical experiment evidence; not rewritten.'
    },
    [pscustomobject]@{
        Path = 'docs/architecture/specs/2026-07-19-task3-lossless-persistence-design.md'
        MaxLines = 2
        Reason = 'Archived non-normative design; HISTORICAL_NON_NORMATIVE.'
    },
    [pscustomobject]@{
        Path = 'docs/architecture/specs/2026-07-21-runtime-catalog-authority-design.md'
        MaxLines = 65
        Reason = 'Archived non-normative design; HISTORICAL_NON_NORMATIVE.'
    },
    [pscustomobject]@{
        Path = 'docs/architecture/specs/2026-07-22-governance-rule-wire-design.md'
        MaxLines = 5
        Reason = 'Archived non-normative design; HISTORICAL_NON_NORMATIVE.'
    },
    [pscustomobject]@{
        Path = 'docs/architecture/specs/2026-07-22-governance-rule-wire-v1-lock.md'
        MaxLines = 11
        Reason = 'Archived non-normative design; HISTORICAL_NON_NORMATIVE.'
    }
)

$script:Violations = New-Object 'System.Collections.Generic.List[string]'

function Add-MoneyViolation {
    param(
        [Parameter(Mandatory = $true)][string]$Rule,
        [string]$Path = '.',
        [int]$Line = 0,
        [string]$Detail = ''
    )

    $message = "$Rule path=$Path"
    if ($Line -gt 0) {
        $message += " line=$Line"
    }
    if (-not [string]::IsNullOrWhiteSpace($Detail)) {
        $message += " detail=$Detail"
    }
    $script:Violations.Add($message)
}

if (-not [System.IO.Path]::IsPathRooted($Root)) {
    [Console]::Error.WriteLine('MONEY_BAN_ROOT_NOT_ABSOLUTE path=.')
    exit 1
}
if (-not (Test-Path -LiteralPath $Root -PathType Container)) {
    [Console]::Error.WriteLine('MONEY_BAN_ROOT_NOT_FOUND path=.')
    exit 1
}

$script:RootFull = [System.IO.Path]::GetFullPath((Resolve-Path -LiteralPath $Root).ProviderPath).TrimEnd([char[]]@([char]92, [char]47))
$script:RootPrefix = $script:RootFull + [System.IO.Path]::DirectorySeparatorChar
$script:PathComparison = if ([System.Environment]::OSVersion.Platform -eq [System.PlatformID]::Win32NT) {
    [System.StringComparison]::OrdinalIgnoreCase
} else {
    [System.StringComparison]::Ordinal
}

function Get-RelativePath {
    param([Parameter(Mandatory = $true)][string]$FullPath)

    $candidate = [System.IO.Path]::GetFullPath($FullPath)
    if (-not $candidate.StartsWith($script:RootPrefix, $script:PathComparison)) {
        return '[outside]'
    }
    return $candidate.Substring($script:RootPrefix.Length).Replace([char]92, [char]47)
}

function Get-ScannableFiles {
    $files = New-Object 'System.Collections.Generic.List[object]'
    $directories = New-Object 'System.Collections.Generic.Queue[string]'
    $directories.Enqueue($script:RootFull)
    while ($directories.Count -gt 0) {
        $directory = $directories.Dequeue()
        $children = @()
        try {
            $children = @(Get-ChildItem -LiteralPath $directory -Force -ErrorAction Stop | Sort-Object Name)
        } catch {
            Add-MoneyViolation -Rule 'MONEY_BAN_ENUMERATION_FAILED' -Path (Get-RelativePath -FullPath $directory)
            continue
        }
        foreach ($child in $children) {
            if (($child.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
                continue
            }
            $relative = Get-RelativePath -FullPath $child.FullName
            if ($relative -eq '[outside]') {
                continue
            }
            if ($relative -eq '.git' -or $relative.StartsWith('.git/', [System.StringComparison]::Ordinal)) {
                continue
            }
            if ($child.PSIsContainer) {
                $directories.Enqueue($child.FullName)
            } else {
                $files.Add($child)
            }
        }
    }
    return $files.ToArray()
}

function Get-BannedLineNumbers {
    param([Parameter(Mandatory = $true)][string]$FullPath)

    $hits = New-Object 'System.Collections.Generic.List[object]'
    $bytes = [System.IO.File]::ReadAllBytes($FullPath)
    if ($bytes.Length -eq 0 -or [Array]::IndexOf($bytes, [byte]0) -ge 0) {
        return $hits
    }
    $text = $null
    try {
        $text = (New-Object System.Text.UTF8Encoding($false, $true)).GetString($bytes)
    } catch {
        return $hits
    }
    $lines = [System.Text.RegularExpressions.Regex]::Split($text, "`r`n|`n|`r")
    for ($index = 0; $index -lt $lines.Length; $index++) {
        foreach ($token in $script:BannedTokens) {
            if ($lines[$index].IndexOf($token, [System.StringComparison]::OrdinalIgnoreCase) -ge 0) {
                $hits.Add([pscustomobject]@{ Line = $index + 1; BannedIdentifier = $token })
                break
            }
        }
    }
    return $hits
}

function Invoke-MoneyBanGate {
    $exemptionByPath = [System.Collections.Generic.Dictionary[string,object]]::new([System.StringComparer]::Ordinal)
    foreach ($exemption in $script:Exemptions) {
        if ([string]::IsNullOrWhiteSpace($exemption.Reason)) {
            Add-MoneyViolation -Rule 'MONEY_BAN_EXEMPTION_REASON_MISSING' -Path $exemption.Path
            continue
        }
        if ($exemptionByPath.ContainsKey($exemption.Path)) {
            Add-MoneyViolation -Rule 'MONEY_BAN_EXEMPTION_DUPLICATE' -Path $exemption.Path
            continue
        }
        $exemptionByPath.Add($exemption.Path, $exemption)
    }

    $scanned = 0
    $usedExemptions = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
    foreach ($file in (Get-ScannableFiles)) {
        $relative = Get-RelativePath -FullPath $file.FullName
        $scanned++
        $hits = @(Get-BannedLineNumbers -FullPath $file.FullName)
        if ($hits.Count -eq 0) {
            continue
        }
        if (-not $exemptionByPath.ContainsKey($relative)) {
            foreach ($hit in $hits) {
                Add-MoneyViolation -Rule 'MONEY_BAN_TOKEN' -Path $relative -Line $hit.Line -Detail $hit.BannedIdentifier
            }
            continue
        }
        [void]$usedExemptions.Add($relative)
        $exemption = $exemptionByPath[$relative]
        if ($hits.Count -gt $exemption.MaxLines) {
            Add-MoneyViolation -Rule 'MONEY_BAN_EXEMPTION_EXCEEDED' -Path $relative `
                -Detail "lines=$($hits.Count) max=$($exemption.MaxLines)"
        }
    }

    foreach ($path in $exemptionByPath.Keys) {
        if (-not $usedExemptions.Contains($path)) {
            Add-MoneyViolation -Rule 'MONEY_BAN_EXEMPTION_UNUSED' -Path $path
        }
    }

    if ($script:Violations.Count -gt 0) {
        $violations = $script:Violations.ToArray()
        [System.Array]::Sort($violations, [System.StringComparer]::Ordinal)
        foreach ($violation in $violations) {
            [Console]::Error.WriteLine($violation)
        }
        exit 1
    }

    [Console]::Out.WriteLine("MONEY_BAN_GATE_OK files=$scanned tokens=$($script:BannedTokens.Count) exemptions=$($exemptionByPath.Count)")
    exit 0
}

try {
    Invoke-MoneyBanGate
} catch {
    [Console]::Error.WriteLine('MONEY_BAN_INTERNAL_ERROR path=.')
    exit 1
}
