[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$scriptPath = Join-Path $PSScriptRoot 'release-gate.ps1'
if (-not (Test-Path -LiteralPath $scriptPath -PathType Leaf)) {
    throw "release gate script is missing: $scriptPath"
}
$workflowPath = Join-Path (Split-Path -Parent $PSScriptRoot) '.github/workflows/release-gate.yml'
if (-not (Test-Path -LiteralPath $workflowPath -PathType Leaf)) {
    throw "release gate workflow is missing: $workflowPath"
}

$testTempRoot = if (-not [string]::IsNullOrWhiteSpace($env:FREEAGENT_RELEASE_GATE_TEST_TEMP_ROOT)) {
    if (-not [IO.Path]::IsPathRooted($env:FREEAGENT_RELEASE_GATE_TEST_TEMP_ROOT) -or
        -not (Test-Path -LiteralPath $env:FREEAGENT_RELEASE_GATE_TEST_TEMP_ROOT -PathType Container)) {
        throw 'FREEAGENT_RELEASE_GATE_TEST_TEMP_ROOT must be an existing absolute directory.'
    }
    [IO.Path]::GetFullPath($env:FREEAGENT_RELEASE_GATE_TEST_TEMP_ROOT)
} else {
    [IO.Path]::GetTempPath()
}

$scriptSource = Get-Content -LiteralPath $scriptPath -Raw -Encoding UTF8
$workflowSource = Get-Content -LiteralPath $workflowPath -Raw -Encoding UTF8

function Get-WorkflowJobSource {
    param([Parameter(Mandatory = $true)][string]$Name)

    $pattern = '(?ms)^  ' + [regex]::Escape($Name) + ':\r?\n.*?(?=^  [A-Za-z0-9_-]+:\r?\n|\z)'
    $matches = [regex]::Matches($workflowSource, $pattern)
    if ($matches.Count -ne 1) {
        throw "workflow job $Name must occur exactly once; actual=$($matches.Count)"
    }
    return $matches[0].Value
}

function Assert-ContractPattern {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Source,
        [Parameter(Mandatory = $true)][string]$Pattern,
        [int]$MinimumCount = 1
    )

    $matches = [regex]::Matches(
        $Source,
        $Pattern,
        [Text.RegularExpressions.RegexOptions]::Multiline
    )
    if ($matches.Count -lt $MinimumCount) {
        throw "CONTRACT FAILED [$Name]: expected at least $MinimumCount match(es), actual=$($matches.Count), pattern=$Pattern"
    }
}

function Assert-ContractNotPattern {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Source,
        [Parameter(Mandatory = $true)][string]$Pattern
    )

    if ([regex]::IsMatch(
        $Source,
        $Pattern,
        [Text.RegularExpressions.RegexOptions]::Multiline
    )) {
        throw "CONTRACT FAILED [$Name]: forbidden pattern matched: $Pattern"
    }
}

# Workflow contract: every project command runs through an authenticated
# controller snapshot and a sealed, external staging tree. The workflow itself
# only coordinates immutable actions, controller invocations, GCC installation,
# verified artifact upload, and the final proof seal.
function Assert-ContractCount {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Source,
        [Parameter(Mandatory = $true)][string]$Pattern,
        [Parameter(Mandatory = $true)][int]$ExpectedCount
    )

    $actual = [regex]::Matches(
        $Source,
        $Pattern,
        [Text.RegularExpressions.RegexOptions]::Multiline
    ).Count
    if ($actual -ne $ExpectedCount) {
        throw "CONTRACT FAILED [$Name]: expected=$ExpectedCount actual=$actual pattern=$Pattern"
    }
}

function Get-WorkflowStepSources {
    param([Parameter(Mandatory = $true)][string]$JobSource)

    return @(
        [regex]::Matches(
            $JobSource,
            '(?ms)^      - name:[^\r\n]*\r?\n.*?(?=^      - name:|\z)'
        ) | ForEach-Object { $_.Value }
    )
}

function Get-WorkflowStepSource {
    param(
        [Parameter(Mandatory = $true)][string]$JobSource,
        [Parameter(Mandatory = $true)][string]$Id
    )

    $matches = @(
        Get-WorkflowStepSources -JobSource $JobSource |
            Where-Object {
                [regex]::IsMatch(
                    $_,
                    '(?m)^        id: ' + [regex]::Escape($Id) + '\s*$'
                )
            }
    )
    if ($matches.Count -ne 1) {
        throw "workflow step id $Id must occur exactly once in its job; actual=$($matches.Count)"
    }
    return $matches[0]
}

function Get-WorkflowNamedStepSource {
    param(
        [Parameter(Mandatory = $true)][string]$JobSource,
        [Parameter(Mandatory = $true)][string]$Name
    )

    $matches = @(
        Get-WorkflowStepSources -JobSource $JobSource |
            Where-Object {
                [regex]::IsMatch(
                    $_,
                    '(?m)^      - name: ' + [regex]::Escape($Name) + '\s*$'
                )
            }
    )
    if ($matches.Count -ne 1) {
        throw "workflow named step $Name must occur exactly once in its job; actual=$($matches.Count)"
    }
    return $matches[0]
}

function Get-WorkflowNeeds {
    param([Parameter(Mandatory = $true)][string]$JobSource)

    $match = [regex]::Match(
        $JobSource,
        '(?ms)^    needs:\r?\n(?<body>(?:      - [A-Za-z0-9_-]+\r?\n)+)'
    )
    if (-not $match.Success) {
        throw 'workflow job must declare an explicit block-list needs contract.'
    }
    return @(
        [regex]::Matches(
            $match.Groups['body'].Value,
            '(?m)^\s+- (?<name>[A-Za-z0-9_-]+)\s*$'
        ) | ForEach-Object { $_.Groups['name'].Value }
    )
}

$expectedJobNames = @(
    'permanent-gates',
    'linux-quality',
    'windows',
    'linux-race',
    'cross-build',
    'release-seal'
)
$jobsMarker = [regex]::Match($workflowSource, '(?m)^jobs:\s*$')
if (-not $jobsMarker.Success) {
    throw 'workflow jobs mapping is missing.'
}
$jobsSource = $workflowSource.Substring($jobsMarker.Index + $jobsMarker.Length)
$actualJobNames = @(
    [regex]::Matches($jobsSource, '(?m)^  (?<name>[a-z][a-z0-9-]*):\s*$') |
        ForEach-Object { $_.Groups['name'].Value }
)
if (($actualJobNames -join ',') -cne ($expectedJobNames -join ',')) {
    throw "workflow jobs mismatch; expected=$($expectedJobNames -join ',') actual=$($actualJobNames -join ',')"
}

$jobSources = [ordered]@{}
foreach ($jobName in $expectedJobNames) {
    $jobSources[$jobName] = Get-WorkflowJobSource -Name $jobName
}
$projectJobNames = @($expectedJobNames | Where-Object { $_ -cne 'release-seal' })
$artifactJobNames = @(
    'linux-quality',
    'windows',
    'linux-race',
    'cross-build'
)
$proofJobNames = @(
    'permanent-gates',
    'linux-quality',
    'windows',
    'linux-race'
)

Assert-ContractCount -Name 'workflow has exactly fifteen immutable action uses' `
    -Source $workflowSource -Pattern '(?m)^\s+uses:\s+[^\r\n]+\s*$' -ExpectedCount 15
$expectedActionPins = [ordered]@{
    'actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0' = 5
    'actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff # v5.6.0' = 5
    'actions/setup-node@820762786026740c76f36085b0efc47a31fe5020 # v7.0.0' = 1
    'actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2' = 4
}

$permanentJobSource = $jobSources['permanent-gates']
$nodeStep = Get-WorkflowStepSource -JobSource $permanentJobSource -Id 'setup-node'
Assert-ContractCount -Name 'permanent job uses one immutable setup-node action' `
    -Source $nodeStep `
    -Pattern '(?m)^        uses: actions/setup-node@820762786026740c76f36085b0efc47a31fe5020 # v7\.0\.0\s*$' `
    -ExpectedCount 1
foreach ($pattern in @(
    '(?m)^          node-version-file: \$\{\{ steps\.prepare\.outputs\.stage \}\}/internal/controlweb/\.node-version\s*$',
    '(?m)^          cache: false\s*$',
    '(?m)^          check-latest: false\s*$',
    '(?m)^          package-manager-cache: false\s*$'
)) {
    Assert-ContractCount -Name 'permanent setup-node is exact and cache-free' `
        -Source $nodeStep -Pattern $pattern -ExpectedCount 1
}
$npmStep = Get-WorkflowStepSource -JobSource $permanentJobSource -Id 'setup-npm'
foreach ($literal in @(
    'https://registry.npmjs.org/npm/-/npm-12.0.2.tgz',
    'uIXokLlBj6FpNUTQX1PmT5pz7BlIN9QlixX+zdaSNHsd0qUXsbDLr50xzY6Sw7cJVr0uzHKDOle0swmPW/p5Qw==',
    "nodeVersion -cne 'v24.19.0'",
    "npmVersion -cne '12.0.2'",
    'FREEAGENT_NPM_CLI=$cli'
)) {
    Assert-ContractCount -Name "permanent npm setup binds $literal" `
        -Source $npmStep -Pattern ([regex]::Escape($literal)) -ExpectedCount 1
}
Assert-ContractNotPattern -Name 'workflow never globally installs npm' `
    -Source $workflowSource -Pattern '(?i)npm\s+(?:install|i)\s+(?:--global|-g)'
$actualActionPins = @(
    [regex]::Matches($workflowSource, '(?m)^\s+uses:\s+(?<pin>[^\r\n]+?)\s*$') |
        ForEach-Object { $_.Groups['pin'].Value.Trim() }
)
foreach ($pin in $expectedActionPins.Keys) {
    $actualPinCount = @($actualActionPins | Where-Object { $_ -ceq $pin }).Count
    if ($actualPinCount -ne $expectedActionPins[$pin]) {
        throw "workflow immutable pin count mismatch for $pin; expected=$($expectedActionPins[$pin]) actual=$actualPinCount"
    }
}
$unexpectedActionPins = @($actualActionPins | Where-Object {
    $expectedActionPins.Keys -cnotcontains $_
})
if ($unexpectedActionPins.Count -ne 0) {
    throw "workflow contains unexpected or mutable action references: $($unexpectedActionPins -join ', ')"
}

Assert-ContractPattern -Name 'workflow permission is read-only contents' `
    -Source $workflowSource `
    -Pattern '(?ms)^permissions:\r?\n  contents: read\s*$'
Assert-ContractPattern -Name 'workflow cancels obsolete concurrent runs' `
    -Source $workflowSource `
    -Pattern '(?ms)^concurrency:\r?\n  group: release-gate-\$\{\{ github\.workflow \}\}-\$\{\{ github\.ref \}\}\r?\n  cancel-in-progress: true\s*$'
Assert-ContractNotPattern -Name 'workflow cannot continue after any failed step' `
    -Source $workflowSource -Pattern '(?m)^\s*continue-on-error\s*:'

$expectedJobTimeouts = [ordered]@{
    'permanent-gates' = 45
    'linux-quality' = 60
    'windows' = 60
    'linux-race' = 75
    'cross-build' = 45
    'release-seal' = 5
}
foreach ($jobName in $expectedJobTimeouts.Keys) {
    Assert-ContractCount -Name "$jobName has its fixed job timeout" `
        -Source $jobSources[$jobName] `
        -Pattern ('(?m)^    timeout-minutes: ' + $expectedJobTimeouts[$jobName] + '\s*$') `
        -ExpectedCount 1
}

$expectedCheckoutInputPatterns = @(
    '(?m)^          ref: \$\{\{ github\.sha \}\}\s*$',
    '(?m)^          fetch-depth: 1\s*$',
    '(?m)^          fetch-tags: false\s*$',
    '(?m)^          persist-credentials: false\s*$',
    '(?m)^          submodules: false\s*$',
    '(?m)^          lfs: false\s*$',
    '(?m)^          clean: true\s*$',
    '(?m)^          set-safe-directory: false\s*$'
)
foreach ($jobName in $projectJobNames) {
    $jobSource = $jobSources[$jobName]
    $checkoutStep = Get-WorkflowNamedStepSource `
        -JobSource $jobSource -Name 'Check out exact workflow revision'
    Assert-ContractCount -Name "$jobName has one immutable checkout" `
        -Source $checkoutStep `
        -Pattern '(?m)^        uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4\.4\.0\s*$' `
        -ExpectedCount 1
    foreach ($inputPattern in $expectedCheckoutInputPatterns) {
        Assert-ContractCount -Name "$jobName fixes every checkout safety input" `
            -Source $checkoutStep -Pattern $inputPattern -ExpectedCount 1
    }

    $setupStep = Get-WorkflowStepSource -JobSource $jobSource -Id 'setup'
    Assert-ContractCount -Name "$jobName has one immutable setup-go action" `
        -Source $setupStep `
        -Pattern '(?m)^        uses: actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff # v5\.6\.0\s*$' `
        -ExpectedCount 1
    Assert-ContractCount -Name "$jobName resolves Go only from sealed stage go.mod" `
        -Source $setupStep `
        -Pattern '(?m)^          go-version-file: \$\{\{ steps\.prepare\.outputs\.stage \}\}/go\.mod\s*$' `
        -ExpectedCount 1
    Assert-ContractCount -Name "$jobName disables setup-go cache" `
        -Source $setupStep -Pattern '(?m)^          cache: false\s*$' -ExpectedCount 1
    Assert-ContractCount -Name "$jobName disables setup-go latest lookup" `
        -Source $setupStep -Pattern '(?m)^          check-latest: false\s*$' -ExpectedCount 1
}

$controlPath = Join-Path $PSScriptRoot 'Invoke-CIWorkflowControl.ps1'
if (-not (Test-Path -LiteralPath $controlPath -PathType Leaf)) {
    throw "workflow control script is missing: $controlPath"
}
$controlHash = (Get-FileHash -LiteralPath $controlPath -Algorithm SHA256).Hash.ToLowerInvariant()
$workflowHashLiterals = @(
    [regex]::Matches($workflowSource, '(?i)(?<![0-9a-f])[0-9a-f]{64}(?![0-9a-f])') |
        ForEach-Object { $_.Value.ToLowerInvariant() }
)
if ($workflowHashLiterals.Count -ne 24) {
    throw "workflow control hash literal count mismatch; expected=24 actual=$($workflowHashLiterals.Count)"
}
$wrongControlHashes = @($workflowHashLiterals | Where-Object { $_ -cne $controlHash })
if ($wrongControlHashes.Count -ne 0) {
    throw "workflow control hash literals do not match the actual controller SHA256 $controlHash"
}

$expectedControlStepIds = [ordered]@{
    'permanent-gates' = @('prepare', 'initialize', 'run', 'finalize')
    'linux-quality' = @('prepare', 'initialize', 'run', 'finalize', 'seal')
    'windows' = @('prepare', 'initialize', 'run', 'finalize', 'seal')
    'linux-race' = @('prepare', 'initialize', 'run', 'finalize', 'seal')
    'cross-build' = @('prepare', 'initialize', 'run', 'finalize', 'seal')
}
$controlStepCount = 0
foreach ($jobName in $expectedControlStepIds.Keys) {
    $jobSource = $jobSources[$jobName]
    foreach ($stepId in $expectedControlStepIds[$jobName]) {
        $stepSource = Get-WorkflowStepSource -JobSource $jobSource -Id $stepId
        $controlStepCount++
        Assert-ContractCount -Name "$jobName/$stepId has the current controller hash literal" `
            -Source $stepSource `
            -Pattern ([regex]::Escape($controlHash)) -ExpectedCount 1
        Assert-ContractPattern -Name "$jobName/$stepId reads exact controller bytes" `
            -Source $stepSource -Pattern '\[IO\.File\]::ReadAllBytes\(\$path\)'
        Assert-ContractPattern -Name "$jobName/$stepId enables strict mode" `
            -Source $stepSource -Pattern 'Set-StrictMode -Version Latest'
        Assert-ContractPattern -Name "$jobName/$stepId performs strict UTF-8 decoding" `
            -Source $stepSource -Pattern 'UTF8Encoding\(\$false,\s*\$true\)'
        Assert-ContractPattern -Name "$jobName/$stepId parses authenticated source" `
            -Source $stepSource `
            -Pattern '\[Management\.Automation\.Language\.Parser\]::ParseInput\(\$source,'
        Assert-ContractPattern -Name "$jobName/$stepId compiles an in-memory script block" `
            -Source $stepSource -Pattern '\[ScriptBlock\]::Create\(\$source\)'
        Assert-ContractNotPattern -Name "$jobName/$stepId never executes controller by file path" `
            -Source $stepSource -Pattern '(?m)(?:^|\s)-File(?:\s|$)'

        if ($stepId -ceq 'prepare') {
            Assert-ContractPattern -Name "$jobName prepare reads only the checked-out controller path" `
                -Source $stepSource `
                -Pattern '\$path = Join-Path \$env:GITHUB_WORKSPACE ''scripts/Invoke-CIWorkflowControl\.ps1'''
            Assert-ContractPattern -Name "$jobName prepare writes a create-new controller snapshot" `
                -Source $stepSource -Pattern '\[IO\.FileMode\]::CreateNew'
            Assert-ContractPattern -Name "$jobName prepare durably flushes the controller snapshot" `
                -Source $stepSource -Pattern '\$stream\.Flush\(\$true\)'
            Assert-ContractPattern -Name "$jobName prepare invokes only authenticated in-memory control" `
                -Source $stepSource `
                -Pattern '(?s)\[ScriptBlock\]::Create\(\$source\).*?\$stream\.Flush\(\$true\).*?& \$control'
            Assert-ContractPattern -Name "$jobName prepare pins the exact GitHub revision" `
                -Source $stepSource `
                -Pattern '(?s)-RepositoryRoot \$env:GITHUB_WORKSPACE.*?-Revision \$env:GITHUB_SHA'
            Assert-ContractNotPattern -Name "$jobName prepare does not load any other checkout file" `
                -Source $stepSource -Pattern '(?m)\b(Get-Content|Copy-Item)\b'
        } else {
            Assert-ContractCount -Name "$jobName/$stepId loads only the prepared controller snapshot" `
                -Source $stepSource `
                -Pattern '(?m)^          CIWC_CONTROL_PATH: \$\{\{ steps\.prepare\.outputs\.control \}\}\s*$' `
                -ExpectedCount 1
            Assert-ContractNotPattern -Name "$jobName/$stepId never returns to the checkout" `
                -Source $stepSource -Pattern 'GITHUB_WORKSPACE|github\.workspace|scripts/'
        }
    }
}
if ($controlStepCount -ne 24) {
    throw "authenticated controller step count mismatch; expected=24 actual=$controlStepCount"
}

$expectedStepIds = [ordered]@{
    'permanent-gates' = @('prepare', 'initialize', 'setup', 'run', 'finalize')
    'linux-quality' = @('prepare', 'initialize', 'setup', 'run', 'finalize', 'upload', 'seal')
    'windows' = @('prepare', 'initialize', 'setup', 'run', 'finalize', 'upload', 'seal')
    'linux-race' = @('prepare', 'initialize', 'setup', 'gcc', 'run', 'finalize', 'upload', 'seal')
    'cross-build' = @('prepare', 'initialize', 'setup', 'run', 'finalize', 'upload', 'seal')
}
foreach ($jobName in $expectedStepIds.Keys) {
    $actualStepIds = @(
        [regex]::Matches($jobSources[$jobName], '(?m)^        id: (?<id>[a-z]+)\s*$') |
            ForEach-Object { $_.Groups['id'].Value }
    )
    if (($actualStepIds -join ',') -cne ($expectedStepIds[$jobName] -join ',')) {
        throw "$jobName step order mismatch; expected=$($expectedStepIds[$jobName] -join ',') actual=$($actualStepIds -join ',')"
    }

    $runStep = Get-WorkflowStepSource -JobSource $jobSources[$jobName] -Id 'run'
    $runConditionPattern = if ($jobName -ceq 'permanent-gates') {
        "(?m)^        if: \$\{\{ !cancelled\(\) && steps\.initialize\.outcome == 'success' && steps\.setup\.outcome == 'success' && steps\.setup-node\.outcome == 'success' && steps\.setup-npm\.outcome == 'success' \}\}\s*$"
    } else {
        "(?m)^        if: \$\{\{ !cancelled\(\) && steps\.initialize\.outcome == 'success' \}\}\s*$"
    }
    Assert-ContractCount -Name "$jobName run requires initialized, non-cancelled state" `
        -Source $runStep `
        -Pattern $runConditionPattern `
        -ExpectedCount 1
    $finalizeStep = Get-WorkflowStepSource -JobSource $jobSources[$jobName] -Id 'finalize'
    Assert-ContractCount -Name "$jobName finalizer handles both real run terminal outcomes" `
        -Source $finalizeStep `
        -Pattern "(?m)^        if: \$\{\{ always\(\) && steps\.prepare\.outcome == 'success' && steps\.initialize\.outcome == 'success' && \(steps\.run\.outcome == 'success' \|\| steps\.run\.outcome == 'failure'\) \}\}\s*$" `
        -ExpectedCount 1
}

foreach ($proofJobName in $proofJobNames) {
    Assert-ContractCount -Name "$proofJobName exports its finalizer proof bit" `
        -Source $jobSources[$proofJobName] `
        -Pattern '(?m)^      proof_complete: \$\{\{ steps\.finalize\.outputs\.run_succeeded \}\}\s*$' `
        -ExpectedCount 1
}
Assert-ContractNotPattern -Name 'permanent gates produce no upload or mutable release artifact' `
    -Source $jobSources['permanent-gates'] -Pattern 'upload-artifact|id: upload|id: seal'

$expectedUploads = [ordered]@{
    'linux-quality' = @(
        'release-evidence-linux-ordinary',
        '${{ steps.prepare.outputs.artifact }}'
    )
    'windows' = @(
        'release-evidence-windows-ordinary',
        '${{ steps.prepare.outputs.artifact }}'
    )
    'linux-race' = @(
        'release-evidence-linux-race',
        '${{ steps.prepare.outputs.artifact }}'
    )
    'cross-build' = @(
        'freeagent-${{ matrix.goos }}-${{ matrix.goarch }}',
        '${{ steps.prepare.outputs.artifact }}'
    )
}
foreach ($jobName in $artifactJobNames) {
    $uploadStep = Get-WorkflowStepSource -JobSource $jobSources[$jobName] -Id 'upload'
    $sealStep = Get-WorkflowStepSource -JobSource $jobSources[$jobName] -Id 'seal'
    Assert-ContractCount -Name "$jobName has one immutable upload action" `
        -Source $uploadStep `
        -Pattern '(?m)^        uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4\.6\.2\s*$' `
        -ExpectedCount 1
    Assert-ContractCount -Name "$jobName uploads only its exact external artifact name" `
        -Source $uploadStep `
        -Pattern ('(?m)^          name: ' + [regex]::Escape($expectedUploads[$jobName][0]) + '\s*$') `
        -ExpectedCount 1
    Assert-ContractCount -Name "$jobName uploads only its exact external artifact path" `
        -Source $uploadStep `
        -Pattern ('(?m)^          path: ' + [regex]::Escape($expectedUploads[$jobName][1]) + '\s*$') `
        -ExpectedCount 1
    Assert-ContractNotPattern -Name "$jobName cannot upload an evidence or binary subtree" `
        -Source $uploadStep `
        -Pattern '(?m)^          path: \$\{\{ steps\.prepare\.outputs\.artifact \}\}[\\/](?:evidence|bin)(?:[\\/]|$)'
    foreach ($uploadInputPattern in @(
        '(?m)^          if-no-files-found: error\s*$',
        '(?m)^          retention-days: 14\s*$',
        '(?m)^          include-hidden-files: false\s*$',
        '(?m)^          overwrite: false\s*$'
    )) {
        Assert-ContractCount -Name "$jobName fixes every upload safety input" `
            -Source $uploadStep -Pattern $uploadInputPattern -ExpectedCount 1
    }
    Assert-ContractPattern -Name "$jobName seal binds the final artifact content set" `
        -Source $sealStep `
        -Pattern '(?s)CIWC_ARTIFACT_SET_SHA256: \$\{\{ steps\.finalize\.outputs\.artifact_set_sha256 \}\}.*?-ExpectedArtifactSetSha256 \$env:CIWC_ARTIFACT_SET_SHA256'

    if ($jobName -ceq 'cross-build') {
        $artifactCondition = "(?m)^        if: \$\{\{ always\(\) && steps\.setup\.outcome == 'success' && steps\.run\.outcome == 'success' && steps\.finalize\.outcome == 'success' \}\}\s*$"
    } else {
        $artifactCondition = "(?m)^        if: \$\{\{ always\(\) && steps\.finalize\.outcome == 'success' \}\}\s*$"
    }
    Assert-ContractCount -Name "$jobName upload follows only successful verification" `
        -Source $uploadStep -Pattern $artifactCondition -ExpectedCount 1
    Assert-ContractCount -Name "$jobName seal follows the same verified upload attempt" `
        -Source $sealStep -Pattern $artifactCondition -ExpectedCount 1
    Assert-ContractCount -Name "$jobName preserves Finalize to Upload to Seal order" `
        -Source $jobSources[$jobName] `
        -Pattern '(?ms)^        id: finalize\s*$.*?^        id: upload\s*$.*?^        id: seal\s*$' `
        -ExpectedCount 1
}
Assert-ContractCount -Name 'all four artifact uploads publish their complete external artifact root' `
    -Source $workflowSource `
    -Pattern '(?m)^          path: \$\{\{ steps\.prepare\.outputs\.artifact \}\}\s*$' `
    -ExpectedCount 4
Assert-ContractNotPattern -Name 'artifact uploads never publish a path below the complete artifact root' `
    -Source $workflowSource `
    -Pattern '(?m)^          path: \$\{\{ steps\.prepare\.outputs\.artifact \}\}[\\/]'

foreach ($raceJobName in @('linux-race')) {
    $gccStep = Get-WorkflowStepSource -JobSource $jobSources[$raceJobName] -Id 'gcc'
    Assert-ContractPattern -Name "$raceJobName installs GCC explicitly" `
        -Source $gccStep `
        -Pattern '(?s)sudo apt-get update.*?sudo apt-get install --yes gcc'
}

$crossBuildJob = $jobSources['cross-build']
$requiredCrossBuildNeeds = @(
    'permanent-gates',
    'linux-quality',
    'windows',
    'linux-race'
)
$actualCrossBuildNeeds = @(Get-WorkflowNeeds -JobSource $crossBuildJob)
if (($actualCrossBuildNeeds -join ',') -cne ($requiredCrossBuildNeeds -join ',')) {
    throw "cross-build needs mismatch; expected=$($requiredCrossBuildNeeds -join ',') actual=$($actualCrossBuildNeeds -join ',')"
}
Assert-ContractPattern -Name 'cross-build matrix does not cancel sibling proofs' `
    -Source $crossBuildJob -Pattern '(?m)^      fail-fast: false\s*$'
$matrixEntries = @(
    [regex]::Matches(
        $crossBuildJob,
        '(?m)^          - goos: (?<goos>[a-z]+)\r?\n            goarch: (?<goarch>[a-z0-9]+)\r?\n            binary: (?<binary>[^\r\n]+)\s*$'
    ) | ForEach-Object {
        "$($_.Groups['goos'].Value)/$($_.Groups['goarch'].Value)/$($_.Groups['binary'].Value)"
    }
)
$expectedMatrixEntries = @(
    'windows/amd64/freeagent-windows-amd64.exe',
    'windows/arm64/freeagent-windows-arm64.exe',
    'linux/amd64/freeagent-linux-amd64',
    'linux/arm64/freeagent-linux-arm64',
    'darwin/amd64/freeagent-darwin-amd64',
    'darwin/arm64/freeagent-darwin-arm64'
)
if (($matrixEntries -join ',') -cne ($expectedMatrixEntries -join ',')) {
    throw "cross-build matrix mismatch; expected=$($expectedMatrixEntries -join ',') actual=$($matrixEntries -join ',')"
}

Assert-ContractNotPattern -Name 'workflow never invokes Go or gofmt directly' `
    -Source $workflowSource `
    -Pattern '(?m)^\s*(?:&\s+)?(?:go|gofmt)\s+(?:test|build|mod|vet|list|fmt|version|env)\b'
Assert-ContractNotPattern -Name 'workflow never invokes a project script outside authenticated control' `
    -Source $workflowSource `
    -Pattern 'scripts/(?!Invoke-CIWorkflowControl\.ps1)[A-Za-z0-9_.-]+\.ps1'
Assert-ContractNotPattern -Name 'workflow never executes any project script by file path' `
    -Source $workflowSource -Pattern '(?m)(?:^|\s)-File(?:\s|$)'
Assert-ContractNotPattern -Name 'workflow never merges raw output streams' `
    -Source $workflowSource -Pattern '2>&1|2>\s*&1|\*>'
Assert-ContractNotPattern -Name 'workflow never tees or cats raw command output' `
    -Source $workflowSource -Pattern '(?im)\bTee-Object\b|^\s*tee\s|^\s*cat\s'

$releaseSealJob = $jobSources['release-seal']
$requiredSealNeeds = @(
    'permanent-gates',
    'linux-quality',
    'windows',
    'linux-race',
    'cross-build'
)
$actualSealNeeds = @(Get-WorkflowNeeds -JobSource $releaseSealJob)
if (($actualSealNeeds -join ',') -cne ($requiredSealNeeds -join ',')) {
    throw "release-seal needs mismatch; expected=$($requiredSealNeeds -join ',') actual=$($actualSealNeeds -join ',')"
}
Assert-ContractCount -Name 'release seal always adjudicates prerequisite results' `
    -Source $releaseSealJob -Pattern '(?m)^    if: \$\{\{ always\(\) \}\}\s*$' `
    -ExpectedCount 1
Assert-ContractNotPattern -Name 'release seal uses no third-party action' `
    -Source $releaseSealJob -Pattern '(?m)^\s+uses:'
foreach ($resultExpression in @(
    '${{ needs.permanent-gates.result }}',
    '${{ needs.linux-quality.result }}',
    '${{ needs.windows.result }}',
    '${{ needs.linux-race.result }}',
    '${{ needs.cross-build.result }}'
)) {
    Assert-ContractCount -Name 'release seal consumes every prerequisite result' `
        -Source $releaseSealJob -Pattern ([regex]::Escape($resultExpression)) `
        -ExpectedCount 1
}
foreach ($proofExpression in @(
    '${{ needs.permanent-gates.outputs.proof_complete }}',
    '${{ needs.linux-quality.outputs.proof_complete }}',
    '${{ needs.windows.outputs.proof_complete }}',
    '${{ needs.linux-race.outputs.proof_complete }}'
)) {
    Assert-ContractCount -Name 'release seal consumes every required proof bit' `
        -Source $releaseSealJob -Pattern ([regex]::Escape($proofExpression)) `
        -ExpectedCount 1
}
Assert-ContractPattern -Name 'release seal fails every non-success job result' `
    -Source $releaseSealJob -Pattern 'if \(\$entry\.Value -cne ''success''\)'
Assert-ContractPattern -Name 'release seal fails every incomplete proof bit' `
    -Source $releaseSealJob -Pattern 'if \(\$entry\.Value -cne ''true''\)'
Assert-ContractPattern -Name 'release seal emits a single closed proof marker' `
    -Source $releaseSealJob -Pattern "Write-Host 'RELEASE_PROOF_SEAL_PASS'"

# Local clean-staging contract. The caller supplies a staging-generator
# ArtifactRoot whose initial layout is exactly the fixed manifest. The same
# fixed pin must verify the stage before any output/Go command and again in
# finally after every success or failure path.
Assert-ContractPattern -Name 'local pre-verification precedes output layout and first Go command' `
    -Source $scriptSource `
    -Pattern '(?s)Invoke-StagingVerification.*?-Phase ''pre''.*?\$preVerificationPassed = \$true.*?Initialize-ArtifactLayout.*?Set-IsolatedGoEnvironment.*?''mod'', ''download'', ''all'''
Assert-ContractPattern -Name 'local bootstrap authenticates raw manifest pin before verifier binding' `
    -Source $scriptSource `
    -Pattern '(?s)function Get-AuthenticatedStagingVerifier.*?ReadAllBytes\(\$ManifestPath\).*?Get-ReleaseGateSha256Hex -Bytes \$manifestBytes.*?\$ManifestPin.*?scripts/Test-PublicStaging\.ps1'
Assert-ContractPattern -Name 'local verifier executes authenticated bytes through BOM-free child stdin' `
    -Source $scriptSource `
    -Pattern '(?s)ToBase64String\(\$authenticated\.Bytes\).*?\$bootstrapBytes = \$utf8\.GetBytes\(\$bootstrap\).*?Unicode\.GetBytes\(\$transportBootstrap\).*?-EncodedCommand .*?RedirectStandardInput = \$true.*?StandardInput\.BaseStream\.Write\(.*?\$bootstrapBytes.*?StandardInput\.BaseStream\.Close\(\)'
Assert-ContractPattern -Name 'local verifier transport strips stdin BOM and strictly decodes UTF-8' `
    -Source $scriptSource `
    -Pattern '(?s)\$bytes\[0\] -eq 0xEF.*?\$offset = 3.*?\$bytes\[0\] -eq 0xFF.*?\$offset = 2.*?UTF8Encoding\(\$false, \$true\).*?ScriptBlock\]::Create\(\$commandText\)'
Assert-ContractNotPattern -Name 'local verifier does not use BOM-emitting stdin text writer' `
    -Source $scriptSource `
    -Pattern 'StandardInput\.Write\('
Assert-ContractPattern -Name 'local verifier child uses trusted absolute temp and working directory' `
    -Source $scriptSource `
    -Pattern '(?s)Resolve-StagingVerifierTempDirectory.*?WorkingDirectory = \$verifierTemp.*?EnvironmentVariables\[\$tempName\] = \$verifierTemp'
Assert-ContractPattern -Name 'local verifier temp uses canonical macOS private tmp' `
    -Source $scriptSource `
    -Pattern '(?s)\$script:IsMacOSPlatform.*?''/private/tmp'''
Assert-ContractNotPattern -Name 'local gate never executes mutable stage verifier by file path' `
    -Source $scriptSource `
    -Pattern '(?s)-File\s+\$(verifier|authenticated\.Path)'
Assert-ContractPattern -Name 'local download all precedes License and the permanent gate sequence' `
    -Source $scriptSource `
    -Pattern '(?s)''mod'', ''download'', ''all''.*?Invoke-PermanentGates'
Assert-ContractPattern -Name 'local permanent gate wrapper fixes the required order' `
    -Source $scriptSource `
    -Pattern '(?s)function Invoke-PermanentGates.*?Name = ''PublicTree''.*?Name = ''License''.*?Name = ''Docs''.*?Invoke-PowerShellGateSequence'
Assert-ContractPattern -Name 'local public-tree gate receives absolute root' `
    -Source $scriptSource `
    -Pattern 'Arguments = @\(''-Root'', \$Root\)'
Assert-ContractPattern -Name 'local license gate receives resolved Go executable' `
    -Source $scriptSource `
    -Pattern 'Arguments = @\(''-Root'', \$Root, ''-GoCommand'', \$GoCommand\)'
Assert-ContractPattern -Name 'local docs gate receives resolved gofmt executable' `
    -Source $scriptSource `
    -Pattern 'Arguments = @\(''-Root'', \$Root, ''-GofmtPath'', \$GofmtCommand\)'
Assert-ContractPattern -Name 'local permanent gates use isolated child processes' `
    -Source $scriptSource `
    -Pattern '(?s)function Invoke-PowerShellGateSequence.*?''-NoProfile''.*?''-NonInteractive''.*?''-File''.*?\$scriptPath.*?if \(\$exitCode -ne 0\).*?throw'
Assert-ContractNotPattern -Name 'local permanent gates expose no bypass switch' `
    -Source $scriptSource `
    -Pattern '\[switch\]\$(Skip|Disable|Bypass)(PublicTree|License|Docs|PermanentGate)'
Assert-ContractPattern -Name 'local ordinary test emits Go JSON' `
    -Source $scriptSource `
    -Pattern "'test', '-json', '-shuffle=on', '-count=1', '-timeout=30m'"
Assert-ContractPattern -Name 'local ordinary test covers all packages' `
    -Source $scriptSource `
    -Pattern "-Packages @\('\./\.\.\.'\)"
Assert-ContractPattern -Name 'local post-verification is guarded by pre success and runs in finally' `
    -Source $scriptSource `
    -Pattern '(?s)finally \{.*?if \(\$preVerificationPassed\).*?Invoke-StagingVerification.*?-Phase ''post'''
Assert-ContractPattern -Name 'local final build artifact gate follows all cross-builds' `
    -Source $scriptSource `
    -Pattern '(?s)foreach \(\$target in \$targets\).*?Invoke-NativeChecked.*?Assert-FinalBuildArtifacts'
Assert-ContractPattern -Name 'local final failure uses pure adjudication' `
    -Source $scriptSource `
    -Pattern 'Resolve-ReleaseGateFailure'
foreach ($isolatedName in @(
    'CGO_ENABLED', 'GOOS', 'GOARCH', 'CC',
    'GOCACHE', 'GOMODCACHE', 'GOTMPDIR',
    'TEMP', 'TMP', 'TMPDIR',
    'GOWORK', 'GOFLAGS', 'GOENV', 'GOTOOLCHAIN', 'GOPATH'
)) {
    Assert-ContractPattern -Name "local gate saves and restores $isolatedName" `
        -Source $scriptSource `
        -Pattern ([regex]::Escape("'$isolatedName'"))
}
Assert-ContractPattern -Name 'local gate disables ambient go env files' `
    -Source $scriptSource `
    -Pattern "Set-ProcessEnvironment -Name 'GOENV' -Value 'off'"
Assert-ContractPattern -Name 'local gate disables automatic toolchain downloads' `
    -Source $scriptSource `
    -Pattern "Set-ProcessEnvironment -Name 'GOTOOLCHAIN' -Value 'local'"
Assert-ContractPattern -Name 'local gate enforces readonly modules' `
    -Source $scriptSource `
    -Pattern "Set-ProcessEnvironment -Name 'GOFLAGS' -Value '-mod=readonly'"
Assert-ContractPattern -Name 'local race test emits Go JSON with timeout' `
    -Source $scriptSource `
    -Pattern "'test', '-json', '-race', '-count=1', '-timeout=30m'"
Assert-ContractPattern -Name 'local evidence records status' `
    -Source $scriptSource `
    -Pattern 'status'
Assert-ContractPattern -Name 'local evidence records command outcome' `
    -Source $scriptSource `
    -Pattern 'command_outcome'
Assert-ContractPattern -Name 'local skip race is unproved and not run' `
    -Source $scriptSource `
    -Pattern '(?s)if \(\$SkipRace\).*?''UNPROVED'''
Assert-ContractPattern -Name 'not-run evidence writes the closed outcome' `
    -Source $scriptSource `
    -Pattern "-CommandOutcome 'NOT_RUN'"
Assert-ContractPattern -Name 'local missing compiler fails closed' `
    -Source $scriptSource `
    -Pattern '(?s)elseif \(\$null -eq \$compiler\).*?throw'
Assert-ContractPattern -Name 'local evidence records compiler version' `
    -Source $scriptSource `
    -Pattern 'compiler_version'
Assert-ContractPattern -Name 'local evidence records Go environment' `
    -Source $scriptSource `
    -Pattern 'go_env'
Assert-ContractPattern -Name 'local evidence records elapsed time' `
    -Source $scriptSource `
    -Pattern 'elapsed_ms'
Assert-ContractPattern -Name 'local evidence records real exit code' `
    -Source $scriptSource `
    -Pattern 'exit_code'
Assert-ContractPattern -Name 'local gate detects PS7 native error preference' `
    -Source $scriptSource `
    -Pattern 'Get-Variable\s+`\r?\n\s+-Name ''PSNativeCommandUseErrorActionPreference'''
Assert-ContractPattern -Name 'local gate freezes PS7 native error preference false' `
    -Source $scriptSource `
    -Pattern '(?s)if \(\$hasNativeErrorPreference\).*?-Name ''PSNativeCommandUseErrorActionPreference''.*?-Value \$false.*?-Scope Script'
Assert-ContractPattern -Name 'local gate restores PS7 native error preference in finally' `
    -Source $scriptSource `
    -Pattern '(?s)finally \{.*?if \(\$hasNativeErrorPreference\).*?-Value \$savedNativeErrorPreference.*?-Scope Script'
Assert-ContractPattern -Name 'local race PASS requires Linux' `
    -Source $scriptSource `
    -Pattern '\$hostGoos -ceq ''linux'''
Assert-ContractPattern -Name 'local race PASS requires GCC proof' `
    -Source $scriptSource `
    -Pattern '\$compilerIsGCC'
Assert-ContractNotPattern -Name 'workflow definition alone cannot claim race PASS' `
    -Source $workflowSource `
    -Pattern "(?m)^\s*status:\s*PASS\s*$"

$tokens = $null
$parseErrors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile(
    $scriptPath,
    [ref]$tokens,
    [ref]$parseErrors
)
if ($parseErrors.Count -ne 0) {
    throw "release gate parser errors: $($parseErrors -join '; ')"
}

$parameterNames = @($ast.ParamBlock.Parameters | ForEach-Object { $_.Name.VariablePath.UserPath })
$expectedParameterNames = @('ArtifactRoot', 'ManifestSha256', 'GoCommand', 'SkipRace')
if ($parameterNames.Count -ne $expectedParameterNames.Count) {
    throw "release gate parameter count mismatch; expected=$($expectedParameterNames.Count) actual=$($parameterNames.Count)"
}
foreach ($parameterName in $expectedParameterNames) {
    if (@($parameterNames | Where-Object { $_ -ceq $parameterName }).Count -ne 1) {
        throw "release gate parameter surface is missing or duplicates $parameterName"
    }
}
foreach ($removedParameterName in @('ReplaceArtifacts', 'EvidenceDirectory')) {
    if ($parameterNames -ccontains $removedParameterName) {
        throw "release gate still exposes removed parameter $removedParameterName"
    }
}
foreach ($mandatoryParameterName in @('ArtifactRoot', 'ManifestSha256', 'GoCommand')) {
    $parameter = @($ast.ParamBlock.Parameters | Where-Object {
        $_.Name.VariablePath.UserPath -ceq $mandatoryParameterName
    })[0]
    $mandatory = @($parameter.Attributes | Where-Object {
        $_.TypeName.Name -ceq 'Parameter' -and
        @($_.NamedArguments | Where-Object {
            $_.ArgumentName -ceq 'Mandatory' -and $_.Argument.Extent.Text -ceq '$true'
        }).Count -eq 1
    }).Count -eq 1
    if (-not $mandatory) {
        throw "release gate parameter $mandatoryParameterName must be mandatory"
    }
}

$gateSequenceFunctions = @($ast.FindAll({
    param($node)
    $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
        $node.Name -ceq 'Invoke-PowerShellGateSequence'
}, $true))
if ($gateSequenceFunctions.Count -ne 1) {
    throw "release gate must contain exactly one Invoke-PowerShellGateSequence function; actual=$($gateSequenceFunctions.Count)"
}
. ([scriptblock]::Create($gateSequenceFunctions[0].Extent.Text))

$failureDecisionFunctions = @($ast.FindAll({
    param($node)
    $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
        $node.Name -ceq 'Resolve-ReleaseGateFailure'
}, $true))
if ($failureDecisionFunctions.Count -ne 1) {
    throw "release gate must contain exactly one Resolve-ReleaseGateFailure function; actual=$($failureDecisionFunctions.Count)"
}
. ([scriptblock]::Create($failureDecisionFunctions[0].Extent.Text))

function Set-GateSequenceFixture {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][int]$ExitCode
    )

    $content = @"
param([Parameter(Mandatory = `$true)][string]`$LogPath)
Add-Content -LiteralPath `$LogPath -Value '$Name' -Encoding utf8
exit $ExitCode
"@
    [IO.File]::WriteAllText($Path, $content, [Text.UTF8Encoding]::new($false))
}

$gateSequenceFixture = Join-Path $testTempRoot ("freeagent-permanent-gates-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $gateSequenceFixture | Out-Null
try {
    $gateLog = Join-Path $gateSequenceFixture 'gate-order.log'
    $gateScripts = [ordered]@{
        PublicTree = Join-Path $gateSequenceFixture 'public-tree.ps1'
        License = Join-Path $gateSequenceFixture 'license.ps1'
        Docs = Join-Path $gateSequenceFixture 'docs.ps1'
    }
    foreach ($name in $gateScripts.Keys) {
        Set-GateSequenceFixture -Path $gateScripts[$name] -Name $name -ExitCode 0
    }
    $gateInvocations = @($gateScripts.Keys | ForEach-Object {
        [pscustomobject]@{
            Name = $_
            ScriptPath = $gateScripts[$_]
            Arguments = @('-LogPath', $gateLog)
        }
    })
    $powerShellCommand = (Get-Process -Id $PID).Path
    Invoke-PowerShellGateSequence `
        -PowerShellCommand $powerShellCommand `
        -GateInvocations $gateInvocations
    $successfulOrder = @((Get-Content -LiteralPath $gateLog -Encoding UTF8))
    if (($successfulOrder -join ',') -cne 'PublicTree,License,Docs') {
        throw "permanent gate success order mismatch: $($successfulOrder -join ',')"
    }

    Remove-Item -LiteralPath $gateLog -Force
    Set-GateSequenceFixture -Path $gateScripts['License'] -Name 'License' -ExitCode 7
    $failureObserved = $false
    try {
        Invoke-PowerShellGateSequence `
            -PowerShellCommand $powerShellCommand `
            -GateInvocations $gateInvocations
    } catch {
        $failureObserved = $_.Exception.Message -match 'Permanent gate License exited with code 7'
    }
    if (-not $failureObserved) {
        throw 'permanent gate sequence did not fail closed on the first failed child.'
    }
    $failedOrder = @((Get-Content -LiteralPath $gateLog -Encoding UTF8))
    if (($failedOrder -join ',') -cne 'PublicTree,License') {
        throw "permanent gate failure did not short-circuit before Docs: $($failedOrder -join ',')"
    }
} finally {
    Remove-Item -LiteralPath $gateSequenceFixture -Recurse -Force
}

$evidenceStateFunctions = @($ast.FindAll({
    param($node)
    $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
        $node.Name -ceq 'Assert-ReleaseEvidenceState'
}, $true))
if ($evidenceStateFunctions.Count -ne 1) {
    throw "release gate must contain exactly one Assert-ReleaseEvidenceState function; actual=$($evidenceStateFunctions.Count)"
}

$evidenceInvokerFunctions = @($ast.FindAll({
    param($node)
    $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
        $node.Name -ceq 'Invoke-GoTestWithEvidence'
}, $true))
if ($evidenceInvokerFunctions.Count -ne 1) {
    throw "release gate must contain exactly one Invoke-GoTestWithEvidence function; actual=$($evidenceInvokerFunctions.Count)"
}

$evidenceWriterFunctions = @($ast.FindAll({
    param($node)
    $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
        $node.Name -ceq 'Write-ReleaseEvidence'
}, $true))
if ($evidenceWriterFunctions.Count -ne 1) {
    throw "release gate must contain exactly one Write-ReleaseEvidence function; actual=$($evidenceWriterFunctions.Count)"
}

$evidenceRecordFunctions = @($ast.FindAll({
    param($node)
    $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
        $node.Name -ceq 'New-ReleaseEvidenceRecord'
}, $true))
if ($evidenceRecordFunctions.Count -ne 1) {
    throw "release gate must contain exactly one New-ReleaseEvidenceRecord function; actual=$($evidenceRecordFunctions.Count)"
}

$notRunEvidenceFunctions = @($ast.FindAll({
    param($node)
    $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
        $node.Name -ceq 'Write-NotRunRaceEvidence'
}, $true))
if ($notRunEvidenceFunctions.Count -ne 1) {
    throw "release gate must contain exactly one Write-NotRunRaceEvidence function; actual=$($notRunEvidenceFunctions.Count)"
}

$evidenceStateFunction = [scriptblock]::Create($evidenceStateFunctions[0].Extent.Text)
. $evidenceStateFunction
$allowedEvidenceStates = @(
    @('PASS', 'PASSED'),
    @('UNPROVED', 'NOT_RUN'),
    @('UNPROVED', 'FAILED'),
    @('UNPROVED', 'PASSED'),
    @('SKIPPED', 'NOT_RUN')
)
foreach ($state in $allowedEvidenceStates) {
    Assert-ReleaseEvidenceState -Status $state[0] -CommandOutcome $state[1]
}

$allStatuses = @('PASS', 'SKIPPED', 'UNPROVED')
$allOutcomes = @('PASSED', 'FAILED', 'NOT_RUN')
foreach ($status in $allStatuses) {
    foreach ($outcome in $allOutcomes) {
        $isAllowed = $allowedEvidenceStates |
            Where-Object { $_[0] -ceq $status -and $_[1] -ceq $outcome }
        if ($null -ne $isAllowed) {
            continue
        }
        $rejected = $false
        try {
            Assert-ReleaseEvidenceState -Status $status -CommandOutcome $outcome
        } catch {
            $rejected = $true
        }
        if (-not $rejected) {
            throw "evidence state matrix accepted forbidden combination $status/$outcome"
        }
    }
}

. ([scriptblock]::Create($evidenceWriterFunctions[0].Extent.Text))
. ([scriptblock]::Create($evidenceRecordFunctions[0].Extent.Text))
. ([scriptblock]::Create($evidenceInvokerFunctions[0].Extent.Text))
. ([scriptblock]::Create($notRunEvidenceFunctions[0].Extent.Text))

function Assert-EvidenceFixture {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)]$Result,
        [Parameter(Mandatory = $true)][int]$ExpectedExitCode,
        [Parameter(Mandatory = $true)][string]$ExpectedStatus,
        [Parameter(Mandatory = $true)][string]$ExpectedOutcome
    )

    if ($Result.ExitCode -ne $ExpectedExitCode) {
        throw "$Name exit mismatch; expected=$ExpectedExitCode actual=$($Result.ExitCode)"
    }
    if (-not (Test-Path -LiteralPath $Result.JSONPath -PathType Leaf)) {
        throw "$Name raw go test JSON is missing: $($Result.JSONPath)"
    }
    $record = Get-Content -LiteralPath $Result.ManifestPath -Raw | ConvertFrom-Json
    if ($record.status -cne $ExpectedStatus -or $record.command_outcome -cne $ExpectedOutcome) {
        throw "$Name state mismatch; expected=$ExpectedStatus/$ExpectedOutcome actual=$($record.status)/$($record.command_outcome)"
    }
    if ($record.exit_code -ne $ExpectedExitCode) {
        throw "$Name manifest exit mismatch; expected=$ExpectedExitCode actual=$($record.exit_code)"
    }
    if ($record.elapsed_ms -lt 0) {
        throw "$Name elapsed_ms cannot be negative."
    }
    if ($record.go_test_json -cne [IO.Path]::GetFileName($Result.JSONPath)) {
        throw "$Name does not bind its raw go test JSON."
    }
    if ($record.command_argv.Count -lt 2 -or
        $record.command_argv[-1] -cne './fixture' -or
        $record.packages.Count -ne 1 -or
        $record.packages[0] -cne './fixture') {
        throw "$Name command argv/package binding is incomplete."
    }
}

$nativeEvidenceFixture = Join-Path $testTempRoot ("freeagent-release-evidence-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $nativeEvidenceFixture | Out-Null
try {
    $fakeCommand = Join-Path $nativeEvidenceFixture 'fake-go.ps1'
    [IO.File]::WriteAllText(
        $fakeCommand,
        @'
param(
    [int]$FixtureExitCode,
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$Remaining
)
Write-Output '{"Action":"pass","Package":"fixture"}'
exit $FixtureExitCode
'@,
        [Text.UTF8Encoding]::new($false)
    )
    $pwshCommand = (Get-Process -Id $PID).Path
    $fixtureGoEnvironment = [ordered]@{
        GOOS = 'fixture-os'
        GOARCH = 'fixture-arch'
        CGO_ENABLED = '1'
    }

    $passedResult = Invoke-GoTestWithEvidence `
        -GoCommand $pwshCommand `
        -Name 'executed-pass' `
        -Arguments @('-NoProfile', '-File', $fakeCommand, '0') `
        -Packages @('./fixture') `
        -EvidenceRoot $nativeEvidenceFixture `
        -GoVersion 'go version fixture' `
        -GoEnvironment $fixtureGoEnvironment `
        -CompilerPath 'fixture-gcc' `
        -CompilerIdentity 'fixture-gcc identity' `
        -CompilerVersion 'fixture-gcc 1.0'
    Assert-EvidenceFixture `
        -Name 'executed passing command' `
        -Result $passedResult `
        -ExpectedExitCode 0 `
        -ExpectedStatus 'PASS' `
        -ExpectedOutcome 'PASSED'

    $failedResult = Invoke-GoTestWithEvidence `
        -GoCommand $pwshCommand `
        -Name 'executed-fail' `
        -Arguments @('-NoProfile', '-File', $fakeCommand, '7') `
        -Packages @('./fixture') `
        -EvidenceRoot $nativeEvidenceFixture `
        -GoVersion 'go version fixture' `
        -GoEnvironment $fixtureGoEnvironment `
        -CompilerPath 'fixture-gcc' `
        -CompilerIdentity 'fixture-gcc identity' `
        -CompilerVersion 'fixture-gcc 1.0'
    Assert-EvidenceFixture `
        -Name 'executed failing command' `
        -Result $failedResult `
        -ExpectedExitCode 7 `
        -ExpectedStatus 'UNPROVED' `
        -ExpectedOutcome 'FAILED'

    $incompleteProofResult = Invoke-GoTestWithEvidence `
        -GoCommand $pwshCommand `
        -Name 'executed-incomplete-proof' `
        -Arguments @('-NoProfile', '-File', $fakeCommand, '0') `
        -Packages @('./fixture') `
        -EvidenceRoot $nativeEvidenceFixture `
        -GoVersion 'go version fixture' `
        -GoEnvironment $fixtureGoEnvironment `
        -CompilerPath 'fixture-gcc' `
        -CompilerIdentity 'fixture-gcc identity' `
        -CompilerVersion '' `
        -ProofComplete $false
    Assert-EvidenceFixture `
        -Name 'executed command with incomplete proof' `
        -Result $incompleteProofResult `
        -ExpectedExitCode 0 `
        -ExpectedStatus 'UNPROVED' `
        -ExpectedOutcome 'PASSED'

    Write-NotRunRaceEvidence `
        -Name 'explicit-skip' `
        -EvidenceRoot $nativeEvidenceFixture `
        -Status 'UNPROVED' `
        -Reason 'fixture explicit skip' `
        -GoVersion 'go version fixture' `
        -GoEnvironment $fixtureGoEnvironment `
        -CompilerPath $null
    $skipRecord = Get-Content -LiteralPath (Join-Path $nativeEvidenceFixture 'explicit-skip.evidence.json') -Raw |
        ConvertFrom-Json
    if ($skipRecord.status -cne 'UNPROVED' -or
        $skipRecord.command_outcome -cne 'NOT_RUN' -or
        $null -ne $skipRecord.exit_code -or
        -not $skipRecord.required) {
        throw "explicit skip evidence mismatch: $($skipRecord | ConvertTo-Json -Compress)"
    }
} finally {
    Remove-Item -LiteralPath $nativeEvidenceFixture -Recurse -Force
}

$formatFunctions = @($ast.FindAll({
    param($node)
    $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
        $node.Name -ceq 'Test-GoFormatting'
}, $true))
if ($formatFunctions.Count -ne 1) {
    throw "release gate must contain exactly one Test-GoFormatting function; actual=$($formatFunctions.Count)"
}

$repositoryRoot = (Resolve-Path -LiteralPath (Split-Path -Parent $PSScriptRoot)).Path
$ownedGofmtToolRoot = $null
$bundledGofmt = Get-ChildItem -LiteralPath (Join-Path $repositoryRoot '.tools') -Recurse -File -ErrorAction SilentlyContinue |
    Where-Object { $_.Name -in @('gofmt.exe', 'gofmt') } |
    Select-Object -First 1
if ($null -ne $bundledGofmt) {
    $gofmtCommand = $bundledGofmt.FullName
} else {
    $gofmt = @(
        Get-Command gofmt -CommandType Application -ErrorAction SilentlyContinue
    ) | Select-Object -First 1
    if ($null -ne $gofmt) {
        $gofmtCommand = $gofmt.Source
    } else {
        $formatPowerShellCommand = (Get-Process -Id $PID).Path
        $ownedGofmtToolRoot = Join-Path $testTempRoot (
            'freeagent-format-tool-' + [Guid]::NewGuid().ToString('N')
        )
        New-Item -ItemType Directory -Path $ownedGofmtToolRoot | Out-Null
        $formatDriver = Join-Path $ownedGofmtToolRoot 'format-driver.ps1'
        [IO.File]::WriteAllText(
            $formatDriver,
            @'
param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
foreach ($path in @($Arguments | Where-Object { $_ -cne '-l' })) {
    $text = [IO.File]::ReadAllText($path)
    if ($text -match '\(\s+\)') {
        Write-Output $path
    }
}
'@,
            [Text.UTF8Encoding]::new($false)
        )
        $gofmtCommand = Join-Path $ownedGofmtToolRoot 'gofmt.cmd'
        [IO.File]::WriteAllText(
            $gofmtCommand,
            @"
@echo off
`"$formatPowerShellCommand`" -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File `"%~dp0format-driver.ps1`" %*
exit /b %ERRORLEVEL%
"@,
            [Text.UTF8Encoding]::new($false)
        )
    }
}
$formatFunctionSource = $formatFunctions[0].Extent.Text

function New-FormattingFixture {
    $path = Join-Path $testTempRoot ("freeagent-formatting-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $path | Out-Null
    return $path
}

function Set-GoFixtureFile {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$RelativePath,
        [Parameter(Mandatory = $true)][string]$Content
    )

    $path = Join-Path $Root $RelativePath
    New-Item -ItemType Directory -Path (Split-Path -Parent $path) -Force | Out-Null
    [IO.File]::WriteAllText($path, $Content, [Text.UTF8Encoding]::new($false))
}

function Invoke-FormattingFixture {
    param([Parameter(Mandatory = $true)][string]$Root)

    $escapedRoot = $Root.Replace("'", "''")
    $escapedGofmt = $gofmtCommand.Replace("'", "''")
    $harness = @"
`$repositoryRoot = '$escapedRoot'
$formatFunctionSource
Test-GoFormatting -GofmtCommand '$escapedGofmt'
"@

    $output = New-Object System.Collections.Generic.List[object]
    try {
        & ([scriptblock]::Create($harness)) 2>&1 | ForEach-Object { $output.Add($_) }
        return [pscustomobject]@{
            Succeeded = $true
            Output = ($output -join [Environment]::NewLine)
        }
    } catch {
        $output.Add($_)
        return [pscustomobject]@{
            Succeeded = $false
            Output = (($output | ForEach-Object { $_.ToString() }) -join [Environment]::NewLine)
        }
    }
}

function Assert-FormattingPassed {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)]$Result
    )

    if (-not $Result.Succeeded) {
        throw "$Name expected success:`n$($Result.Output)"
    }
}

function Assert-FormattingRejected {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)]$Result,
        [Parameter(Mandatory = $true)][string]$ExpectedPath
    )

    if ($Result.Succeeded) {
        throw "$Name expected rejection but passed."
    }
    $normalizedOutput = $Result.Output.Replace([char]92, [char]47)
    $normalizedExpectedPath = $ExpectedPath.Replace([char]92, [char]47)
    if ($normalizedOutput.IndexOf($normalizedExpectedPath, [StringComparison]::OrdinalIgnoreCase) -lt 0) {
        throw "$Name did not report $ExpectedPath`:`n$($Result.Output)"
    }
}

$rootGeneratedCacheFixture = New-FormattingFixture
try {
    Set-GoFixtureFile -Root $rootGeneratedCacheFixture -RelativePath 'source/main.go' `
        -Content "package source`n"
    Set-GoFixtureFile -Root $rootGeneratedCacheFixture -RelativePath '.cache/go-build/00/generated.go' `
        -Content "package generated`nfunc goBuild( ){}`n"
    Set-GoFixtureFile -Root $rootGeneratedCacheFixture -RelativePath '.cache/go-tmp/go-build1/b001/generated.go' `
        -Content "package generated`nfunc goTmp( ){}`n"
    Assert-FormattingPassed -Name 'repository-root Go generated caches are excluded' `
        -Result (Invoke-FormattingFixture -Root $rootGeneratedCacheFixture)
} finally {
    Remove-Item -LiteralPath $rootGeneratedCacheFixture -Recurse -Force
}

$rootOtherCacheFixture = New-FormattingFixture
try {
    Set-GoFixtureFile -Root $rootOtherCacheFixture -RelativePath 'source/main.go' `
        -Content "package source`n"
    Set-GoFixtureFile -Root $rootOtherCacheFixture -RelativePath '.cache/other/source.go' `
        -Content "package other`nfunc source( ){}`n"
    Assert-FormattingRejected -Name 'repository-root .cache outside generated prefixes remains checked' `
        -Result (Invoke-FormattingFixture -Root $rootOtherCacheFixture) `
        -ExpectedPath '.cache/other/source.go'
} finally {
    Remove-Item -LiteralPath $rootOtherCacheFixture -Recurse -Force
}

$nestedGoTmpFixture = New-FormattingFixture
try {
    Set-GoFixtureFile -Root $nestedGoTmpFixture -RelativePath 'source/.cache/go-tmp/source.go' `
        -Content "package nested`nfunc source( ){}`n"
    Assert-FormattingRejected -Name 'nested .cache go-tmp cannot bypass formatting' `
        -Result (Invoke-FormattingFixture -Root $nestedGoTmpFixture) `
        -ExpectedPath 'source/.cache/go-tmp/source.go'
} finally {
    Remove-Item -LiteralPath $nestedGoTmpFixture -Recurse -Force
}

if ($null -ne $ownedGofmtToolRoot -and (Test-Path -LiteralPath $ownedGofmtToolRoot)) {
    Remove-Item -LiteralPath $ownedGofmtToolRoot -Recurse -Force
}

$script:ReleaseGateFixtureCases = 0
$script:ReleaseGateFixtureAssertions = 0
$script:ReleaseGateFixtureFailures = New-Object 'System.Collections.Generic.List[string]'
$script:Utf8NoBom = New-Object Text.UTF8Encoding($false)
$script:IsWindowsPlatform = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
$script:GeneratorPath = Join-Path $PSScriptRoot 'New-PublicStaging.ps1'
$script:VerifierPath = Join-Path $PSScriptRoot 'Test-PublicStaging.ps1'
$script:PowerShellCommand = (Get-Process -Id $PID).Path
$script:ManifestName = 'public-tree-manifest.v1.json'
$script:EnvironmentNames = @(
    'CGO_ENABLED', 'GOOS', 'GOARCH', 'CC',
    'GOCACHE', 'GOMODCACHE', 'GOTMPDIR',
    'TEMP', 'TMP', 'TMPDIR',
    'GOWORK', 'GOFLAGS', 'GOENV', 'GOTOOLCHAIN', 'GOPATH'
)

function Assert-ReleaseGateFixtureTrue {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][bool]$Condition
    )
    $script:ReleaseGateFixtureAssertions++
    if (-not $Condition) {
        throw "ASSERT_FAIL $Name"
    }
}

function Assert-ReleaseGateFixtureEqual {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [AllowNull()]$Expected,
        [AllowNull()]$Actual
    )
    $script:ReleaseGateFixtureAssertions++
    if ($Expected -cne $Actual) {
        throw "ASSERT_FAIL $Name expected=[$Expected] actual=[$Actual]"
    }
}

function Invoke-ReleaseGateFixtureCase {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][scriptblock]$Body
    )
    $script:ReleaseGateFixtureCases++
    try {
        & $Body
        Write-Host "PASS $Name"
    } catch {
        $script:ReleaseGateFixtureFailures.Add("$Name`: $($_.Exception.Message)")
        Write-Host "FAIL $Name`: $($_.Exception.Message)"
    }
}

Invoke-ReleaseGateFixtureCase 'failure adjudication preserves stage-integrity precedence and details' {
    $primaryPostRestore = Resolve-ReleaseGateFailure `
        -PrimaryMessage 'PRIMARY_SENTINEL' `
        -PostMessage 'POST_SENTINEL' `
        -RestoreFailures @('RESTORE_ONE', 'RESTORE_TWO')
    Assert-ReleaseGateFixtureTrue -Name 'primary post restore uses stage-integrity code' `
        -Condition ($primaryPostRestore.IndexOf(
            'RELEASE_GATE_FAIL code=RG_STAGE_INTEGRITY_FAILED',
            [StringComparison]::Ordinal
        ) -eq 0)
    foreach ($sentinel in @('PRIMARY_SENTINEL', 'POST_SENTINEL', 'RESTORE_ONE', 'RESTORE_TWO')) {
        Assert-ReleaseGateFixtureTrue -Name "primary post restore retains $sentinel" `
            -Condition ($primaryPostRestore.IndexOf($sentinel, [StringComparison]::Ordinal) -ge 0)
    }

    $postRestore = Resolve-ReleaseGateFailure `
        -PrimaryMessage $null `
        -PostMessage 'POST_ONLY_SENTINEL' `
        -RestoreFailures @('POST_RESTORE_ONE', 'POST_RESTORE_TWO')
    Assert-ReleaseGateFixtureTrue -Name 'post restore uses stage-integrity code' `
        -Condition ($postRestore.IndexOf(
            'RELEASE_GATE_FAIL code=RG_STAGE_INTEGRITY_FAILED',
            [StringComparison]::Ordinal
        ) -eq 0)
    foreach ($sentinel in @('POST_ONLY_SENTINEL', 'POST_RESTORE_ONE', 'POST_RESTORE_TWO')) {
        Assert-ReleaseGateFixtureTrue -Name "post restore retains $sentinel" `
            -Condition ($postRestore.IndexOf($sentinel, [StringComparison]::Ordinal) -ge 0)
    }

    $primaryRestore = Resolve-ReleaseGateFailure `
        -PrimaryMessage 'PRIMARY_RESTORE_SENTINEL' `
        -PostMessage $null `
        -RestoreFailures @('PRIMARY_RESTORE_ONE', 'PRIMARY_RESTORE_TWO')
    Assert-ReleaseGateFixtureTrue -Name 'primary restore uses restore code' `
        -Condition ($primaryRestore.IndexOf(
            'RELEASE_GATE_FAIL code=RG_RESTORE_FAILED',
            [StringComparison]::Ordinal
        ) -eq 0)
    foreach ($sentinel in @('PRIMARY_RESTORE_SENTINEL', 'PRIMARY_RESTORE_ONE', 'PRIMARY_RESTORE_TWO')) {
        Assert-ReleaseGateFixtureTrue -Name "primary restore retains $sentinel" `
            -Condition ($primaryRestore.IndexOf($sentinel, [StringComparison]::Ordinal) -ge 0)
    }

    $primaryOnly = Resolve-ReleaseGateFailure `
        -PrimaryMessage 'PRIMARY_EXACT_SENTINEL' `
        -PostMessage $null `
        -RestoreFailures @()
    Assert-ReleaseGateFixtureEqual -Name 'primary-only failure remains exact' `
        -Expected 'PRIMARY_EXACT_SENTINEL' -Actual $primaryOnly

    $emptyPrimary = Resolve-ReleaseGateFailure `
        -PrimaryFailed $true `
        -PrimaryMessage '' `
        -PostMessage $null `
        -RestoreFailures @()
    Assert-ReleaseGateFixtureTrue -Name 'empty primary message cannot become success' `
        -Condition ($emptyPrimary.IndexOf(
            'RELEASE_GATE_FAIL code=RG_PRIMARY_FAILED',
            [StringComparison]::Ordinal
        ) -eq 0)

    $emptyPost = Resolve-ReleaseGateFailure `
        -PostFailed $true `
        -PrimaryMessage $null `
        -PostMessage '' `
        -RestoreFailures @()
    Assert-ReleaseGateFixtureTrue -Name 'empty post message retains stage-integrity failure' `
        -Condition ($emptyPost.IndexOf(
            'RELEASE_GATE_FAIL code=RG_STAGE_INTEGRITY_FAILED',
            [StringComparison]::Ordinal
        ) -eq 0)
}

function Write-ReleaseGateFixtureFile {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Content
    )
    $parent = [IO.Path]::GetDirectoryName($Path)
    if (-not [string]::IsNullOrWhiteSpace($parent)) {
        [void][IO.Directory]::CreateDirectory($parent)
    }
    [IO.File]::WriteAllText($Path, $Content, $script:Utf8NoBom)
}

function Get-ReleaseGateFixtureHash {
    param([Parameter(Mandatory = $true)][string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function New-ReleaseGateIntegrationFixture {
    param([Parameter(Mandatory = $true)][string]$Name)

    $root = Join-Path $testTempRoot ("release-gate-integration-$Name-" + [Guid]::NewGuid().ToString('N'))
    $source = Join-Path $root 'source'
    $stage = Join-Path $root 'stage'
    $artifact = Join-Path $root 'artifact'
    $tools = Join-Path $root 'tools'
    $control = Join-Path $root 'control'
    foreach ($directory in @($source, $artifact, $tools, $control)) {
        [void][IO.Directory]::CreateDirectory($directory)
    }

    $sourceScripts = Join-Path $source 'scripts'
    [void][IO.Directory]::CreateDirectory($sourceScripts)
    Copy-Item -LiteralPath $scriptPath -Destination (Join-Path $sourceScripts 'release-gate.ps1')

    $realVerifierBase64 = [Convert]::ToBase64String(
        [IO.File]::ReadAllBytes($script:VerifierPath)
    )
    Write-ReleaseGateFixtureFile -Path (Join-Path $sourceScripts 'Test-PublicStaging.ps1') -Content @"
[CmdletBinding()]
param(
    [Parameter(Mandatory = `$true)][string]`$Root,
    [Parameter(Mandatory = `$true)][string]`$ManifestPath,
    [Parameter(Mandatory = `$true)][string]`$ManifestSha256
)
`$artifact = [IO.Path]::GetDirectoryName(`$ManifestPath)
`$phase = if (Test-Path -LiteralPath (Join-Path `$artifact 'bin') -PathType Container) { 'post' } else { 'pre' }
Add-Content -LiteralPath `$env:FREEAGENT_RELEASE_GATE_FIXTURE_LOG -Value "verify:`$phase" -Encoding UTF8
Add-Content -LiteralPath `$env:FREEAGENT_RELEASE_GATE_FIXTURE_LOG -Value "verify-env:TEMP=`$env:TEMP|TMP=`$env:TMP|TMPDIR=`$env:TMPDIR|CWD=`$([Environment]::CurrentDirectory)" -Encoding UTF8
`$source = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('$realVerifierBase64'))
& ([ScriptBlock]::Create(`$source)) -Root `$Root -ManifestPath `$ManifestPath -ManifestSha256 `$ManifestSha256
exit `$LASTEXITCODE
"@

    $gateStubs = @(
        @{
            Name = 'Test-PublicTree.ps1'
            Content = @'
param([Parameter(Mandatory = $true)][string]$Root)
Add-Content -LiteralPath $env:FREEAGENT_RELEASE_GATE_FIXTURE_LOG -Value 'gate:PublicTree' -Encoding UTF8
'@
        },
        @{
            Name = 'Test-License.ps1'
            Content = @'
param(
    [Parameter(Mandatory = $true)][string]$Root,
    [Parameter(Mandatory = $true)][string]$GoCommand
)
Add-Content -LiteralPath $env:FREEAGENT_RELEASE_GATE_FIXTURE_LOG -Value 'gate:License' -Encoding UTF8
'@
        },
        @{
            Name = 'Test-Docs.ps1'
            Content = @'
param(
    [Parameter(Mandatory = $true)][string]$Root,
    [Parameter(Mandatory = $true)][string]$GofmtPath
)
Add-Content -LiteralPath $env:FREEAGENT_RELEASE_GATE_FIXTURE_LOG -Value 'gate:Docs' -Encoding UTF8
'@
        },
        @{
            Name = 'Test-Branding.ps1'
            Content = @'
param([Parameter(Mandatory = $true)][string]$RepositoryRoot)
Add-Content -LiteralPath $env:FREEAGENT_RELEASE_GATE_FIXTURE_LOG -Value 'gate:Branding' -Encoding UTF8
'@
        }
    )
    foreach ($stub in $gateStubs) {
        Write-ReleaseGateFixtureFile -Path (Join-Path $sourceScripts $stub.Name) -Content $stub.Content
    }

    Write-ReleaseGateFixtureFile -Path (Join-Path $source 'go.mod') -Content "module example.invalid/releasegate`n`ngo 1.22`n"
    Write-ReleaseGateFixtureFile -Path (Join-Path $source 'main.go') -Content "package releasegate`n"
    Write-ReleaseGateFixtureFile -Path (Join-Path $source 'sentinel.dat') -Content 'ABCD'

    $fakeGoDriver = Join-Path $tools 'fake-go-driver.ps1'
    Write-ReleaseGateFixtureFile -Path $fakeGoDriver -Content @'
[CmdletBinding(PositionalBinding = $false)]
param(
    [Alias('o')][string]$BuildOutput,
    [Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments
)
trap {
    Add-Content -LiteralPath $env:FREEAGENT_RELEASE_GATE_FIXTURE_LOG `
        -Value ('fake-go-error|' + $_.Exception.Message) -Encoding UTF8
    Write-Error ('fake-go-error: ' + $_.Exception.Message)
    exit 99
}
$argumentText = if ([string]::IsNullOrWhiteSpace($BuildOutput)) {
    $Arguments -join ' '
} else {
    'build -trimpath -o ' + $BuildOutput + ' ./cmd/freeagent'
}
$record = @(
    'go',
    "CGO_ENABLED=$env:CGO_ENABLED",
    "GOOS=$env:GOOS",
    "GOARCH=$env:GOARCH",
    "GOCACHE=$env:GOCACHE",
    "GOMODCACHE=$env:GOMODCACHE",
    "GOTMPDIR=$env:GOTMPDIR",
    "TEMP=$env:TEMP",
    "TMP=$env:TMP",
    "TMPDIR=$env:TMPDIR",
    "GOWORK=$env:GOWORK",
    "GOFLAGS=$env:GOFLAGS",
    "GOENV=$env:GOENV",
    "GOTOOLCHAIN=$env:GOTOOLCHAIN",
    "GOPATH=$env:GOPATH",
    "ARGS=$argumentText"
) -join '|'
Add-Content -LiteralPath $env:FREEAGENT_RELEASE_GATE_FIXTURE_LOG -Value $record -Encoding UTF8

if ($argumentText -ceq 'mod download all') {
    $sentinel = Join-Path $env:FREEAGENT_RELEASE_GATE_FIXTURE_STAGE 'sentinel.dat'
    switch ($env:FREEAGENT_RELEASE_GATE_FIXTURE_MUTATION) {
        'add' {
            [IO.File]::WriteAllText(
                (Join-Path $env:FREEAGENT_RELEASE_GATE_FIXTURE_STAGE 'added-after-pre.dat'),
                'added',
                (New-Object Text.UTF8Encoding($false))
            )
        }
        'delete' {
            Remove-Item -LiteralPath $sentinel -Force
        }
        'same-size' {
            [IO.File]::WriteAllBytes($sentinel, [Text.Encoding]::ASCII.GetBytes('WXYZ'))
        }
        'mtime' {
            [IO.File]::SetLastWriteTimeUtc($sentinel, [DateTime]::UtcNow.AddHours(-2))
        }
        'verifier' {
            $verifier = Join-Path $env:FREEAGENT_RELEASE_GATE_FIXTURE_STAGE `
                'scripts/Test-PublicStaging.ps1'
            [IO.File]::AppendAllText(
                $verifier,
                "`nexit 0`n",
                (New-Object Text.UTF8Encoding($false))
            )
        }
    }
}

if (-not [string]::IsNullOrWhiteSpace($env:FREEAGENT_RELEASE_GATE_FIXTURE_FAIL_MATCH) -and
    $argumentText -ceq $env:FREEAGENT_RELEASE_GATE_FIXTURE_FAIL_MATCH) {
    exit 7
}

if ($Arguments.Count -gt 0 -and $Arguments[0] -ceq 'version') {
    Write-Output 'go version go1.99.0 windows/amd64'
} elseif ($Arguments.Count -gt 0 -and $Arguments[0] -ceq 'env') {
    $goos = if ([string]::IsNullOrWhiteSpace($env:GOOS)) { 'windows' } else { $env:GOOS }
    $goarch = if ([string]::IsNullOrWhiteSpace($env:GOARCH)) { 'amd64' } else { $env:GOARCH }
    $cgo = if ([string]::IsNullOrWhiteSpace($env:CGO_ENABLED)) { '0' } else { $env:CGO_ENABLED }
    Write-Output $goos
    Write-Output $goarch
    Write-Output $cgo
} elseif ($Arguments.Count -gt 0 -and $Arguments[0] -ceq 'test') {
    Write-Output '{"Action":"pass","Package":"example.invalid/releasegate"}'
} elseif ($Arguments.Count -gt 0 -and $Arguments[0] -ceq 'build') {
    if ([string]::IsNullOrWhiteSpace($BuildOutput)) {
        exit 8
    }
    $output = $BuildOutput
    [void][IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($output))
    switch ($env:FREEAGENT_RELEASE_GATE_FIXTURE_BUILD_MODE) {
        'no-output' {}
        'zero-byte' {
            [IO.File]::WriteAllBytes($output, [byte[]]@())
        }
        'directory' {
            [void][IO.Directory]::CreateDirectory($output)
        }
        default {
            [IO.File]::WriteAllText(
                $output,
                $env:GOOS + '/' + $env:GOARCH,
                (New-Object Text.UTF8Encoding($false))
            )
            if ($env:FREEAGENT_RELEASE_GATE_FIXTURE_BUILD_MODE -ceq 'extra') {
                [IO.File]::WriteAllText(
                    (Join-Path ([IO.Path]::GetDirectoryName($output)) 'unexpected.bin'),
                    'unexpected',
                    (New-Object Text.UTF8Encoding($false))
                )
            }
        }
    }
}
exit 0
'@

    $fakeGofmtDriver = Join-Path $tools 'fake-gofmt-driver.ps1'
    Write-ReleaseGateFixtureFile -Path $fakeGofmtDriver -Content @'
[CmdletBinding()]
param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
Add-Content -LiteralPath $env:FREEAGENT_RELEASE_GATE_FIXTURE_LOG `
    -Value ('gofmt|ARGS=' + ($Arguments -join ' ')) -Encoding UTF8
exit 0
'@

    $powerShellExecutable = $script:PowerShellCommand.Replace('%', '%%')
    Write-ReleaseGateFixtureFile -Path (Join-Path $tools 'go.cmd') -Content @"
@echo off
`"$powerShellExecutable`" -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File `"%~dp0fake-go-driver.ps1`" %*
exit /b %ERRORLEVEL%
"@
    Write-ReleaseGateFixtureFile -Path (Join-Path $tools 'gofmt.cmd') -Content @"
@echo off
`"$powerShellExecutable`" -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File `"%~dp0fake-gofmt-driver.ps1`" %*
exit /b %ERRORLEVEL%
"@

    $generatorOutput = @(& $script:PowerShellCommand -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass `
        -File $script:GeneratorPath -SourceRoot $source -StageRoot $stage -ArtifactRoot $artifact 2>&1)
    $generatorExit = $LASTEXITCODE
    if ($generatorExit -ne 0) {
        throw "release gate fixture staging failed exit=$generatorExit output=$($generatorOutput -join [Environment]::NewLine)"
    }

    $manifest = Join-Path $artifact $script:ManifestName
    return [pscustomobject]@{
        Root = $root
        Source = $source
        Stage = $stage
        Artifact = $artifact
        Manifest = $manifest
        ManifestSha256 = Get-ReleaseGateFixtureHash -Path $manifest
        Gate = Join-Path (Join-Path $stage 'scripts') 'release-gate.ps1'
        GoCommand = Join-Path $tools 'go.cmd'
        GofmtCommand = Join-Path $tools 'gofmt.cmd'
        Tools = $tools
        Control = $control
        Log = Join-Path $control 'events.log'
    }
}

function Remove-ReleaseGateIntegrationFixture {
    param([Parameter(Mandatory = $true)]$Fixture)
    $full = [IO.Path]::GetFullPath([string]$Fixture.Root)
    $allowed = [IO.Path]::GetFullPath($testTempRoot).TrimEnd([char[]]@([char]92, [char]47)) +
        [IO.Path]::DirectorySeparatorChar
    if (-not $full.StartsWith($allowed, [StringComparison]::OrdinalIgnoreCase)) {
        throw "refusing to remove fixture outside test root: $full"
    }
    if (Test-Path -LiteralPath $full) {
        Remove-Item -LiteralPath $full -Recurse -Force
    }
}

function Invoke-ReleaseGateIntegration {
    param(
        [Parameter(Mandatory = $true)]$Fixture,
        [AllowNull()][string]$ArtifactRoot,
        [AllowNull()][string]$ManifestSha256,
        [AllowNull()][string]$GoCommand,
        [AllowNull()][string]$Mutation,
        [AllowNull()][string]$FailMatch,
        [AllowNull()][string]$BuildMode,
        [switch]$SkipRace
    )

    if ([string]::IsNullOrWhiteSpace($ArtifactRoot)) { $ArtifactRoot = $Fixture.Artifact }
    if ([string]::IsNullOrWhiteSpace($ManifestSha256)) { $ManifestSha256 = $Fixture.ManifestSha256 }
    if ([string]::IsNullOrWhiteSpace($GoCommand)) { $GoCommand = $Fixture.GoCommand }

    $fixtureEnvironmentNames = @(
        'FREEAGENT_RELEASE_GATE_FIXTURE_LOG',
        'FREEAGENT_RELEASE_GATE_FIXTURE_STAGE',
        'FREEAGENT_RELEASE_GATE_FIXTURE_MUTATION',
        'FREEAGENT_RELEASE_GATE_FIXTURE_FAIL_MATCH',
        'FREEAGENT_RELEASE_GATE_FIXTURE_BUILD_MODE'
    )
    $savedFixtureEnvironment = @{}
    foreach ($name in $fixtureEnvironmentNames) {
        $savedFixtureEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
    }
    [Environment]::SetEnvironmentVariable('FREEAGENT_RELEASE_GATE_FIXTURE_LOG', $Fixture.Log, 'Process')
    [Environment]::SetEnvironmentVariable('FREEAGENT_RELEASE_GATE_FIXTURE_STAGE', $Fixture.Stage, 'Process')
    [Environment]::SetEnvironmentVariable('FREEAGENT_RELEASE_GATE_FIXTURE_MUTATION', $Mutation, 'Process')
    [Environment]::SetEnvironmentVariable('FREEAGENT_RELEASE_GATE_FIXTURE_FAIL_MATCH', $FailMatch, 'Process')
    [Environment]::SetEnvironmentVariable('FREEAGENT_RELEASE_GATE_FIXTURE_BUILD_MODE', $BuildMode, 'Process')

    $output = New-Object 'System.Collections.Generic.List[string]'
    $succeeded = $false
    try {
        try {
            $arguments = @{
                ArtifactRoot = $ArtifactRoot
                ManifestSha256 = $ManifestSha256
                GoCommand = $GoCommand
            }
            if ($SkipRace) { $arguments['SkipRace'] = $true }
            & $Fixture.Gate @arguments 2>&1 | ForEach-Object { [void]$output.Add([string]$_) }
            $succeeded = $true
        } catch {
            [void]$output.Add($_.Exception.Message)
        }
    } finally {
        foreach ($name in $fixtureEnvironmentNames) {
            [Environment]::SetEnvironmentVariable($name, $savedFixtureEnvironment[$name], 'Process')
        }
    }
    return [pscustomobject]@{
        Succeeded = $succeeded
        Output = ($output -join [Environment]::NewLine)
        Events = if (Test-Path -LiteralPath $Fixture.Log -PathType Leaf) {
            @((Get-Content -LiteralPath $Fixture.Log -Encoding UTF8))
        } else {
            @()
        }
    }
}

function Assert-ReleaseGateFailedWith {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)]$Result,
        [Parameter(Mandatory = $true)][string]$Code
    )
    Assert-ReleaseGateFixtureTrue -Name "$Name failed" -Condition (-not $Result.Succeeded)
    $script:ReleaseGateFixtureAssertions++
    if ($Result.Output.IndexOf("RELEASE_GATE_FAIL code=$Code", [StringComparison]::Ordinal) -lt 0) {
        throw "ASSERT_FAIL $Name expected code=$Code output=[$($Result.Output)]"
    }
}

function Assert-NoReleaseOutputs {
    param(
        [Parameter(Mandatory = $true)]$Fixture,
        [Parameter(Mandatory = $true)][string]$ArtifactRoot
    )
    foreach ($name in @('bin', 'evidence', 'cache', 'tmp')) {
        Assert-ReleaseGateFixtureTrue -Name "no early artifact $name" `
            -Condition (-not (Test-Path -LiteralPath (Join-Path $ArtifactRoot $name)))
    }
    Assert-ReleaseGateFixtureTrue -Name 'no early stage bin' `
        -Condition (-not (Test-Path -LiteralPath (Join-Path $Fixture.Stage 'bin')))
    Assert-ReleaseGateFixtureTrue -Name 'no early stage evidence' `
        -Condition (-not (Test-Path -LiteralPath (Join-Path $Fixture.Stage 'evidence')))
    Assert-ReleaseGateFixtureTrue -Name 'no early stage cache' `
        -Condition (-not (Test-Path -LiteralPath (Join-Path $Fixture.Stage 'cache')))
    Assert-ReleaseGateFixtureTrue -Name 'no early stage tmp' `
        -Condition (-not (Test-Path -LiteralPath (Join-Path $Fixture.Stage 'tmp')))
}

function Get-EventIndex {
    param(
        [Parameter(Mandatory = $true)][string[]]$Events,
        [Parameter(Mandatory = $true)][string]$Pattern
    )
    for ($index = 0; $index -lt $Events.Count; $index++) {
        if ($Events[$index] -match $Pattern) { return $index }
    }
    return -1
}

function Assert-OrderedEventPatterns {
    param(
        [Parameter(Mandatory = $true)][string[]]$Events,
        [Parameter(Mandatory = $true)][string[]]$Patterns
    )
    $previous = -1
    foreach ($pattern in $Patterns) {
        $index = Get-EventIndex -Events $Events -Pattern $pattern
        Assert-ReleaseGateFixtureTrue -Name "event exists: $pattern" -Condition ($index -ge 0)
        Assert-ReleaseGateFixtureTrue -Name "event order: $pattern" -Condition ($index -gt $previous)
        $previous = $index
    }
}

function Invoke-WithEnvironmentSentinels {
    param(
        [Parameter(Mandatory = $true)]$Fixture,
        [AllowNull()][string]$Mutation,
        [AllowNull()][string]$FailMatch
    )
    $saved = @{}
    foreach ($name in $script:EnvironmentNames) {
        $saved[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
        [Environment]::SetEnvironmentVariable($name, "before_$name", 'Process')
    }
    $savedLocation = (Get-Location).Path
    $nativeVariable = Get-Variable -Name 'PSNativeCommandUseErrorActionPreference' -ErrorAction SilentlyContinue
    $hasNativeVariable = $null -ne $nativeVariable
    $savedNativeValue = if ($hasNativeVariable) { [bool]$nativeVariable.Value } else { $null }
    if ($hasNativeVariable) {
        Set-Variable -Name 'PSNativeCommandUseErrorActionPreference' -Value $true -Scope Script
    }
    try {
        Set-Location -LiteralPath $Fixture.Control
        $result = Invoke-ReleaseGateIntegration -Fixture $Fixture -Mutation $Mutation -FailMatch $FailMatch -SkipRace
        foreach ($name in $script:EnvironmentNames) {
            Assert-ReleaseGateFixtureEqual -Name "environment restored $name" `
                -Expected "before_$name" `
                -Actual ([Environment]::GetEnvironmentVariable($name, 'Process'))
        }
        Assert-ReleaseGateFixtureEqual -Name 'location restored' `
            -Expected $Fixture.Control -Actual (Get-Location).Path
        if ($hasNativeVariable) {
            Assert-ReleaseGateFixtureEqual -Name 'native preference restored' `
                -Expected $true `
                -Actual ([bool](Get-Variable -Name 'PSNativeCommandUseErrorActionPreference').Value)
        }
        return $result
    } finally {
        Set-Location -LiteralPath $savedLocation
        foreach ($name in $script:EnvironmentNames) {
            [Environment]::SetEnvironmentVariable($name, $saved[$name], 'Process')
        }
        if ($hasNativeVariable) {
            Set-Variable -Name 'PSNativeCommandUseErrorActionPreference' -Value $savedNativeValue -Scope Script
        }
    }
}

Invoke-ReleaseGateFixtureCase 'parameter and initial artifact failures are side-effect free' {
    $fixture = New-ReleaseGateIntegrationFixture -Name 'parameter-contract'
    try {
        foreach ($invalidPin in @(
            (('0' * 63) -join ''),
            (('0' * 65) -join ''),
            (('g' * 64) -join '')
        )) {
            $result = Invoke-ReleaseGateIntegration -Fixture $fixture -ManifestSha256 $invalidPin -SkipRace
            Assert-ReleaseGateFailedWith -Name 'invalid manifest pin' -Result $result -Code 'RG_MANIFEST_PIN_INVALID'
            Assert-NoReleaseOutputs -Fixture $fixture -ArtifactRoot $fixture.Artifact
        }

        $relativeArtifact = Invoke-ReleaseGateIntegration -Fixture $fixture -ArtifactRoot 'relative-artifact' -SkipRace
        Assert-ReleaseGateFailedWith -Name 'relative artifact root' `
            -Result $relativeArtifact -Code 'RG_PATH_NOT_ABSOLUTE'
        Assert-NoReleaseOutputs -Fixture $fixture -ArtifactRoot $fixture.Artifact

        $missingArtifactPath = Join-Path $fixture.Root 'missing-artifact'
        $missingArtifact = Invoke-ReleaseGateIntegration -Fixture $fixture `
            -ArtifactRoot $missingArtifactPath -SkipRace
        Assert-ReleaseGateFailedWith -Name 'missing artifact root' `
            -Result $missingArtifact -Code 'RG_ARTIFACT_MISSING'
        Assert-ReleaseGateFixtureTrue -Name 'missing artifact root was not created' `
            -Condition (-not (Test-Path -LiteralPath $missingArtifactPath))

        $relativeGo = Invoke-ReleaseGateIntegration -Fixture $fixture -GoCommand 'go.cmd' -SkipRace
        Assert-ReleaseGateFailedWith -Name 'relative Go command' -Result $relativeGo -Code 'RG_PATH_NOT_ABSOLUTE'
        Assert-NoReleaseOutputs -Fixture $fixture -ArtifactRoot $fixture.Artifact

        $stageGo = Invoke-ReleaseGateIntegration -Fixture $fixture -GoCommand $fixture.Gate -SkipRace
        Assert-ReleaseGateFailedWith -Name 'Go command inside stage' `
            -Result $stageGo -Code 'RG_GO_COMMAND_DISALLOWED'
        Assert-NoReleaseOutputs -Fixture $fixture -ArtifactRoot $fixture.Artifact

        $artifactGo = Invoke-ReleaseGateIntegration -Fixture $fixture -GoCommand $fixture.Manifest -SkipRace
        Assert-ReleaseGateFailedWith -Name 'Go command inside artifact' `
            -Result $artifactGo -Code 'RG_GO_COMMAND_DISALLOWED'
        Assert-NoReleaseOutputs -Fixture $fixture -ArtifactRoot $fixture.Artifact

        Write-ReleaseGateFixtureFile -Path (Join-Path $fixture.Artifact 'foreign.txt') -Content 'foreign'
        [void][IO.Directory]::CreateDirectory((Join-Path $fixture.Artifact 'foreign-directory'))
        $foreign = Invoke-ReleaseGateIntegration -Fixture $fixture -SkipRace
        Assert-ReleaseGateFailedWith -Name 'foreign artifact entries' `
            -Result $foreign -Code 'RG_ARTIFACT_INITIAL_LAYOUT'
        Assert-ReleaseGateFixtureEqual -Name 'foreign file preserved' -Expected 'foreign' `
            -Actual ([IO.File]::ReadAllText((Join-Path $fixture.Artifact 'foreign.txt')))
        Assert-ReleaseGateFixtureTrue -Name 'foreign directory preserved' `
            -Condition (Test-Path -LiteralPath (Join-Path $fixture.Artifact 'foreign-directory') -PathType Container)
    } finally {
        Remove-ReleaseGateIntegrationFixture -Fixture $fixture
    }
}

Invoke-ReleaseGateFixtureCase 'artifact roots are disjoint and reparse-free' {
    $fixture = New-ReleaseGateIntegrationFixture -Name 'artifact-boundary'
    $junction = $null
    try {
        $nestedArtifact = Join-Path $fixture.Stage 'nested-artifact'
        [void][IO.Directory]::CreateDirectory($nestedArtifact)
        Copy-Item -LiteralPath $fixture.Manifest -Destination (Join-Path $nestedArtifact $script:ManifestName)
        $nested = Invoke-ReleaseGateIntegration -Fixture $fixture -ArtifactRoot $nestedArtifact -SkipRace
        Assert-ReleaseGateFailedWith -Name 'artifact nested in stage' `
            -Result $nested -Code 'RG_ROOTS_NOT_DISJOINT'
        Assert-ReleaseGateFixtureTrue -Name 'nested manifest preserved' `
            -Condition (Test-Path -LiteralPath (Join-Path $nestedArtifact $script:ManifestName) -PathType Leaf)

        $ancestor = Invoke-ReleaseGateIntegration -Fixture $fixture -ArtifactRoot $fixture.Root -SkipRace
        Assert-ReleaseGateFailedWith -Name 'artifact containing stage' `
            -Result $ancestor -Code 'RG_ROOTS_NOT_DISJOINT'

        if ($script:IsWindowsPlatform) {
            $target = Join-Path $fixture.Root 'artifact-junction-target'
            [void][IO.Directory]::CreateDirectory($target)
            Copy-Item -LiteralPath $fixture.Manifest -Destination (Join-Path $target $script:ManifestName)
            $junction = Join-Path $fixture.Root 'artifact-junction'
            & cmd.exe /d /c "mklink /J `"$junction`" `"$target`"" *> $null
            if ($LASTEXITCODE -eq 0) {
                $reparse = Invoke-ReleaseGateIntegration -Fixture $fixture -ArtifactRoot $junction -SkipRace
                Assert-ReleaseGateFailedWith -Name 'reparse artifact root' `
                    -Result $reparse -Code 'RG_ARTIFACT_REPARSE'
                Assert-ReleaseGateFixtureTrue -Name 'reparse target manifest preserved' `
                    -Condition (Test-Path -LiteralPath (Join-Path $target $script:ManifestName) -PathType Leaf)
            }
        }
    } finally {
        if ($null -ne $junction -and (Test-Path -LiteralPath $junction)) {
            [IO.Directory]::Delete($junction, $false)
        }
        Remove-ReleaseGateIntegrationFixture -Fixture $fixture
    }
}

Invoke-ReleaseGateFixtureCase 'pre-verification rejects wrong or malformed manifest before outputs and Go' {
    $wrongPinFixture = New-ReleaseGateIntegrationFixture -Name 'wrong-pin'
    try {
        $wrongPin = Invoke-ReleaseGateIntegration -Fixture $wrongPinFixture `
            -ManifestSha256 (('0' * 64) -join '') -SkipRace
        Assert-ReleaseGateFailedWith -Name 'wrong manifest pin' `
            -Result $wrongPin -Code 'RG_STAGING_MANIFEST_AUTH_FAILED'
        Assert-NoReleaseOutputs -Fixture $wrongPinFixture -ArtifactRoot $wrongPinFixture.Artifact
        Assert-ReleaseGateFixtureTrue -Name 'wrong pin invokes no verifier' `
            -Condition (@($wrongPin.Events | Where-Object { $_ -like 'verify:*' }).Count -eq 0)
        Assert-ReleaseGateFixtureTrue -Name 'wrong pin invokes no Go command' `
            -Condition (@($wrongPin.Events | Where-Object { $_ -like 'go|*' }).Count -eq 0)
    } finally {
        Remove-ReleaseGateIntegrationFixture -Fixture $wrongPinFixture
    }

    $malformedFixture = New-ReleaseGateIntegrationFixture -Name 'malformed-manifest'
    try {
        Write-ReleaseGateFixtureFile -Path $malformedFixture.Manifest -Content '{'
        $malformedHash = Get-ReleaseGateFixtureHash -Path $malformedFixture.Manifest
        $malformed = Invoke-ReleaseGateIntegration -Fixture $malformedFixture `
            -ManifestSha256 $malformedHash -SkipRace
        Assert-ReleaseGateFailedWith -Name 'malformed manifest' `
            -Result $malformed -Code 'RG_STAGING_MANIFEST_SCHEMA_FAILED'
        Assert-NoReleaseOutputs -Fixture $malformedFixture -ArtifactRoot $malformedFixture.Artifact
        Assert-ReleaseGateFixtureTrue -Name 'malformed manifest invokes no verifier' `
            -Condition (@($malformed.Events | Where-Object { $_ -like 'verify:*' }).Count -eq 0)
        Assert-ReleaseGateFixtureTrue -Name 'malformed manifest invokes no Go command' `
            -Condition (@($malformed.Events | Where-Object { $_ -like 'go|*' }).Count -eq 0)
    } finally {
        Remove-ReleaseGateIntegrationFixture -Fixture $malformedFixture
    }

    $tamperedVerifierFixture = New-ReleaseGateIntegrationFixture -Name 'tampered-verifier-pre'
    try {
        [IO.File]::AppendAllText(
            (Join-Path $tamperedVerifierFixture.Stage 'scripts/Test-PublicStaging.ps1'),
            "`nexit 0`n",
            $script:Utf8NoBom
        )
        $tamperedVerifier = Invoke-ReleaseGateIntegration `
            -Fixture $tamperedVerifierFixture `
            -SkipRace
        Assert-ReleaseGateFailedWith -Name 'tampered verifier before pre' `
            -Result $tamperedVerifier -Code 'RG_STAGING_VERIFIER_AUTH_FAILED'
        Assert-NoReleaseOutputs `
            -Fixture $tamperedVerifierFixture `
            -ArtifactRoot $tamperedVerifierFixture.Artifact
        Assert-ReleaseGateFixtureTrue -Name 'tampered verifier is never executed' `
            -Condition (@($tamperedVerifier.Events | Where-Object { $_ -like 'verify:*' }).Count -eq 0)
        Assert-ReleaseGateFixtureTrue -Name 'tampered verifier invokes no Go command' `
            -Condition (@($tamperedVerifier.Events | Where-Object { $_ -like 'go|*' }).Count -eq 0)
    } finally {
        Remove-ReleaseGateIntegrationFixture -Fixture $tamperedVerifierFixture
    }
}

Invoke-ReleaseGateFixtureCase 'successful gate fixes order, isolation, outputs, builds, and restoration' {
    $fixture = New-ReleaseGateIntegrationFixture -Name 'success'
    try {
        $result = Invoke-WithEnvironmentSentinels -Fixture $fixture
        if (-not $result.Succeeded) {
            throw "ASSERT_FAIL successful fixture expected success output=[$($result.Output)]"
        }
        $script:ReleaseGateFixtureAssertions++
        Assert-OrderedEventPatterns -Events $result.Events -Patterns @(
            '^verify:pre$',
            '\|ARGS=mod download all$',
            '^gate:PublicTree$',
            '^gate:License$',
            '^gate:Docs$',
            '^gate:Branding$',
            '^gofmt\|ARGS=-l ',
            '\|ARGS=mod verify$',
            '\|ARGS=vet \./\.\.\.$',
            '\|ARGS=test -json -shuffle=on -count=1 -timeout=30m \./\.\.\.$',
            '\|ARGS=build -trimpath -o .*freeagent-windows-amd64\.exe \./cmd/freeagent$',
            '\|ARGS=build -trimpath -o .*freeagent-darwin-arm64 \./cmd/freeagent$',
            '^verify:post$'
        )
        Assert-ReleaseGateFixtureEqual -Name 'exactly two staging verifications' -Expected 2 `
            -Actual @($result.Events | Where-Object { $_ -like 'verify:*' }).Count
        $verifierEnvironmentEvents = @(
            $result.Events |
                Where-Object { $_ -like 'verify-env:*' }
        )
        Assert-ReleaseGateFixtureEqual -Name 'exactly two isolated verifier environments' `
            -Expected 2 -Actual $verifierEnvironmentEvents.Count
        foreach ($environmentEvent in $verifierEnvironmentEvents) {
            $match = [regex]::Match(
                $environmentEvent,
                '^verify-env:TEMP=(?<temp>[^|]+)\|TMP=(?<tmp>[^|]+)\|TMPDIR=(?<tmpdir>[^|]+)\|CWD=(?<cwd>.+)$'
            )
            Assert-ReleaseGateFixtureTrue -Name 'verifier environment record is parseable' `
                -Condition $match.Success
            if ($match.Success) {
                $trustedTemp = $match.Groups['temp'].Value
                Assert-ReleaseGateFixtureTrue -Name 'verifier temp is absolute' `
                    -Condition ([IO.Path]::IsPathRooted($trustedTemp))
                foreach ($valueName in @('tmp', 'tmpdir', 'cwd')) {
                    Assert-ReleaseGateFixtureEqual -Name "verifier $valueName equals trusted temp" `
                        -Expected $trustedTemp -Actual $match.Groups[$valueName].Value
                }
                foreach ($forbiddenRoot in @($fixture.Stage, $fixture.Artifact)) {
                    $forbiddenPrefix = [IO.Path]::GetFullPath($forbiddenRoot).
                        TrimEnd([char[]]@([char]92, [char]47)) +
                        [IO.Path]::DirectorySeparatorChar
                    $trustedFull = [IO.Path]::GetFullPath($trustedTemp)
                    Assert-ReleaseGateFixtureTrue -Name 'verifier temp stays outside stage and artifact' `
                        -Condition (-not $trustedFull.StartsWith(
                            $forbiddenPrefix,
                            [StringComparison]::OrdinalIgnoreCase
                        ))
                }
            }
        }

        $expectedTop = @('bin', 'cache', 'evidence', $script:ManifestName, 'tmp')
        $actualTop = @(Get-ChildItem -LiteralPath $fixture.Artifact -Force | ForEach-Object { $_.Name } | Sort-Object)
        Assert-ReleaseGateFixtureEqual -Name 'artifact top-level layout' `
            -Expected ($expectedTop -join ',') -Actual ($actualTop -join ',')
        foreach ($path in @(
            'evidence/local',
            'cache/build',
            'cache/module',
            'cache/go-tmp',
            'cache/gopath'
        )) {
            Assert-ReleaseGateFixtureTrue -Name "external output directory $path exists" `
                -Condition (Test-Path -LiteralPath (Join-Path $fixture.Artifact $path) -PathType Container)
        }
        foreach ($stageOutput in @('bin', 'evidence', 'cache', 'tmp', 'release-evidence')) {
            Assert-ReleaseGateFixtureTrue -Name "stage has no $stageOutput output" `
                -Condition (-not (Test-Path -LiteralPath (Join-Path $fixture.Stage $stageOutput)))
        }

        $expectedBinaries = @(
            'freeagent-darwin-amd64',
            'freeagent-darwin-arm64',
            'freeagent-linux-amd64',
            'freeagent-linux-arm64',
            'freeagent-windows-amd64.exe',
            'freeagent-windows-arm64.exe'
        )
        $actualBinaries = @(Get-ChildItem -LiteralPath (Join-Path $fixture.Artifact 'bin') -File |
            ForEach-Object { $_.Name } | Sort-Object)
        Assert-ReleaseGateFixtureEqual -Name 'six exact cross-build outputs' `
            -Expected ($expectedBinaries -join ',') -Actual ($actualBinaries -join ',')
        foreach ($binary in $expectedBinaries) {
            Assert-ReleaseGateFixtureTrue -Name "cross-build output $binary is non-empty" `
                -Condition ((Get-Item -LiteralPath (Join-Path (Join-Path $fixture.Artifact 'bin') $binary)).Length -gt 0)
        }
        $buildEvents = @($result.Events | Where-Object { $_ -match '\|ARGS=build ' })
        Assert-ReleaseGateFixtureEqual -Name 'six exact build invocations' -Expected 6 -Actual $buildEvents.Count
        foreach ($buildEvent in $buildEvents) {
            Assert-ReleaseGateFixtureTrue -Name 'build forces CGO zero' `
                -Condition ($buildEvent -match '\|CGO_ENABLED=0\|')
            Assert-ReleaseGateFixtureTrue -Name 'build uses trimpath' `
                -Condition ($buildEvent -match '\|ARGS=build -trimpath ')
        }

        $firstGo = @($result.Events | Where-Object { $_ -like 'go|*' })[0]
        foreach ($expectedEnvironment in @(
            "GOCACHE=$(Join-Path $fixture.Artifact 'cache/build')",
            "GOMODCACHE=$(Join-Path $fixture.Artifact 'cache/module')",
            "GOTMPDIR=$(Join-Path $fixture.Artifact 'cache/go-tmp')",
            "TEMP=$(Join-Path $fixture.Artifact 'tmp')",
            "TMP=$(Join-Path $fixture.Artifact 'tmp')",
            "TMPDIR=$(Join-Path $fixture.Artifact 'tmp')",
            'GOWORK=off',
            'GOFLAGS=-mod=readonly',
            'GOENV=off',
            'GOTOOLCHAIN=local',
            "GOPATH=$(Join-Path $fixture.Artifact 'cache/gopath')"
        )) {
            Assert-ReleaseGateFixtureTrue -Name "isolated Go environment $expectedEnvironment" `
                -Condition ($firstGo.IndexOf("|$expectedEnvironment|", [StringComparison]::OrdinalIgnoreCase) -ge 0)
        }

        foreach ($raceName in @('race')) {
            $raceManifest = Join-Path (Join-Path $fixture.Artifact 'evidence/local') "$raceName.evidence.json"
            Assert-ReleaseGateFixtureTrue -Name "$raceName SkipRace evidence is external" `
                -Condition (Test-Path -LiteralPath $raceManifest -PathType Leaf)
            $record = Get-Content -LiteralPath $raceManifest -Raw -Encoding UTF8 | ConvertFrom-Json
            Assert-ReleaseGateFixtureEqual -Name "$raceName SkipRace status" -Expected 'UNPROVED' -Actual $record.status
            Assert-ReleaseGateFixtureEqual -Name "$raceName SkipRace outcome" -Expected 'NOT_RUN' -Actual $record.command_outcome
        }
    } finally {
        Remove-ReleaseGateIntegrationFixture -Fixture $fixture
    }
}

foreach ($buildMode in @('no-output', 'zero-byte', 'extra', 'directory')) {
    Invoke-ReleaseGateFixtureCase "final build artifact gate rejects $buildMode" {
        $fixture = New-ReleaseGateIntegrationFixture -Name "build-artifact-$buildMode"
        try {
            $result = Invoke-ReleaseGateIntegration -Fixture $fixture `
                -BuildMode $buildMode -SkipRace
            Assert-ReleaseGateFailedWith -Name "$buildMode build artifacts" `
                -Result $result -Code 'RG_BUILD_ARTIFACT_INVALID'
            Assert-ReleaseGateFixtureTrue -Name "$buildMode does not emit pass" `
                -Condition ($result.Output.IndexOf('RELEASE_GATE_PASS', [StringComparison]::Ordinal) -lt 0)
            Assert-ReleaseGateFixtureEqual -Name "$buildMode still runs post verification" `
                -Expected 2 -Actual @($result.Events | Where-Object { $_ -like 'verify:*' }).Count
        } finally {
            Remove-ReleaseGateIntegrationFixture -Fixture $fixture
        }
    }
}

foreach ($mutation in @('add', 'delete', 'same-size')) {
    Invoke-ReleaseGateFixtureCase "post-verification rejects $mutation stage mutation" {
        $fixture = New-ReleaseGateIntegrationFixture -Name "post-$mutation"
        try {
        $result = Invoke-ReleaseGateIntegration -Fixture $fixture -Mutation $mutation -SkipRace
        Assert-ReleaseGateFailedWith -Name "$mutation post mutation" `
            -Result $result -Code 'RG_STAGE_INTEGRITY_FAILED'
        Assert-ReleaseGateFixtureTrue -Name "$mutation retains post verification detail" `
            -Condition ($result.Output.IndexOf(
                'RELEASE_GATE_FAIL code=RG_POST_VERIFY_FAILED',
                [StringComparison]::Ordinal
            ) -ge 0)
            Assert-ReleaseGateFixtureEqual -Name "$mutation has pre and post verification" -Expected 2 `
                -Actual @($result.Events | Where-Object { $_ -like 'verify:*' }).Count
        } finally {
            Remove-ReleaseGateIntegrationFixture -Fixture $fixture
        }
    }
}

Invoke-ReleaseGateFixtureCase 'post authentication rejects verifier replacement' {
    $fixture = New-ReleaseGateIntegrationFixture -Name 'post-verifier'
    try {
        $result = Invoke-ReleaseGateIntegration -Fixture $fixture -Mutation 'verifier' -SkipRace
        Assert-ReleaseGateFailedWith -Name 'post verifier replacement' `
            -Result $result -Code 'RG_STAGE_INTEGRITY_FAILED'
        Assert-ReleaseGateFixtureTrue -Name 'post verifier replacement retains auth detail' `
            -Condition ($result.Output.IndexOf(
                'RELEASE_GATE_FAIL code=RG_STAGING_VERIFIER_AUTH_FAILED',
                [StringComparison]::Ordinal
            ) -ge 0)
        Assert-ReleaseGateFixtureTrue -Name 'post verifier replacement does not emit pass' `
            -Condition ($result.Output.IndexOf('RELEASE_GATE_PASS', [StringComparison]::Ordinal) -lt 0)
        Assert-ReleaseGateFixtureEqual -Name 'only authenticated pre verifier executes' -Expected 1 `
            -Actual @($result.Events | Where-Object { $_ -like 'verify:*' }).Count
    } finally {
        Remove-ReleaseGateIntegrationFixture -Fixture $fixture
    }
}

Invoke-ReleaseGateFixtureCase 'mtime-only stage change remains content-equivalent' {
    $fixture = New-ReleaseGateIntegrationFixture -Name 'mtime-only'
    try {
        $result = Invoke-ReleaseGateIntegration -Fixture $fixture -Mutation 'mtime' -SkipRace
        if (-not $result.Succeeded) {
            throw "ASSERT_FAIL mtime-only fixture expected success output=[$($result.Output)]"
        }
        $script:ReleaseGateFixtureAssertions++
        Assert-ReleaseGateFixtureEqual -Name 'mtime-only has pre and post verification' -Expected 2 `
            -Actual @($result.Events | Where-Object { $_ -like 'verify:*' }).Count
    } finally {
        Remove-ReleaseGateIntegrationFixture -Fixture $fixture
    }
}

Invoke-ReleaseGateFixtureCase 'primary and post failures are both retained and environment restores' {
    $fixture = New-ReleaseGateIntegrationFixture -Name 'primary-post-failure'
    try {
        $result = Invoke-WithEnvironmentSentinels -Fixture $fixture `
            -Mutation 'same-size' -FailMatch 'vet ./...'
        Assert-ReleaseGateFixtureTrue -Name 'combined failure fails' -Condition (-not $result.Succeeded)
        Assert-ReleaseGateFixtureTrue -Name 'combined failure uses stage-integrity code' `
            -Condition ($result.Output.IndexOf(
                'RELEASE_GATE_FAIL code=RG_STAGE_INTEGRITY_FAILED',
                [StringComparison]::Ordinal
            ) -ge 0)
        Assert-ReleaseGateFixtureTrue -Name 'combined failure retains primary native failure' `
            -Condition ($result.Output.IndexOf('RELEASE_GATE_FAIL code=RG_NATIVE_COMMAND_FAILED', [StringComparison]::Ordinal) -ge 0)
        Assert-ReleaseGateFixtureTrue -Name 'combined failure retains post failure' `
            -Condition ($result.Output.IndexOf('RELEASE_GATE_FAIL code=RG_POST_VERIFY_FAILED', [StringComparison]::Ordinal) -ge 0)
        Assert-ReleaseGateFixtureEqual -Name 'combined failure still runs post verification' -Expected 2 `
            -Actual @($result.Events | Where-Object { $_ -like 'verify:*' }).Count
    } finally {
        Remove-ReleaseGateIntegrationFixture -Fixture $fixture
    }
}

Invoke-ReleaseGateFixtureCase 'primary failure and post verifier replacement retain both causes' {
    $fixture = New-ReleaseGateIntegrationFixture -Name 'primary-post-verifier'
    try {
        $result = Invoke-WithEnvironmentSentinels -Fixture $fixture `
            -Mutation 'verifier' -FailMatch 'vet ./...'
        Assert-ReleaseGateFixtureTrue -Name 'combined verifier-auth failure fails' `
            -Condition (-not $result.Succeeded)
        foreach ($detail in @(
            'RELEASE_GATE_FAIL code=RG_STAGE_INTEGRITY_FAILED',
            'RELEASE_GATE_FAIL code=RG_NATIVE_COMMAND_FAILED',
            'RELEASE_GATE_FAIL code=RG_STAGING_VERIFIER_AUTH_FAILED'
        )) {
            Assert-ReleaseGateFixtureTrue -Name "combined verifier-auth retains $detail" `
                -Condition ($result.Output.IndexOf($detail, [StringComparison]::Ordinal) -ge 0)
        }
        Assert-ReleaseGateFixtureEqual -Name 'combined verifier-auth only executes pre verifier' `
            -Expected 1 `
            -Actual @($result.Events | Where-Object { $_ -like 'verify:*' }).Count
    } finally {
        Remove-ReleaseGateIntegrationFixture -Fixture $fixture
    }
}

if ($script:ReleaseGateFixtureFailures.Count -gt 0) {
    $script:ReleaseGateFixtureFailures | ForEach-Object { Write-Error $_ -ErrorAction Continue }
    throw "RELEASE_GATE_SELFTEST_FAIL cases=$($script:ReleaseGateFixtureCases) assertions=$($script:ReleaseGateFixtureAssertions) failures=$($script:ReleaseGateFixtureFailures.Count)"
}

Write-Host (
    'RELEASE_GATE_SELFTEST_PASS ' +
    "cases=$($script:ReleaseGateFixtureCases) " +
    "assertions=$($script:ReleaseGateFixtureAssertions)"
)
Write-Host 'release gate go test, formatting, workflow-preservation, and clean-staging contracts passed'
