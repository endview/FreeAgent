[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$checker = Join-Path $PSScriptRoot 'Test-CapabilityMatrix.ps1'

function Write-Utf8NoBom([string]$Path, [string]$Content) {
    [IO.File]::WriteAllText($Path, $Content, [Text.UTF8Encoding]::new($false))
}

function New-Fixture {
    $root = Join-Path ([IO.Path]::GetTempPath()) ('capability matrix 测试 ' + [guid]::NewGuid())
    New-Item -ItemType Directory -Path $root | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $root 'docs/specs') -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $root 'testdata/release') -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $root 'internal/example') -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $root 'internal/example/nested') -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $root 'scripts') -Force | Out-Null
    Write-Utf8NoBom (Join-Path $root 'internal/example/example_test.go') @'
package example

func TestExampleEvidence(t *testing.T) {}
'@
    Write-Utf8NoBom (Join-Path $root 'scripts/evidence.Tests.ps1') @'
function Test-ExampleEvidence {}
'@
    Write-Utf8NoBom (Join-Path $root 'internal/example/nested/nested_test.go') @'
package nested

func TestSubpackageEvidence(t *testing.T) {}
'@
    return $root
}

function Write-Maturity([string]$Root, [string]$Content) {
    Write-Utf8NoBom (Join-Path $Root 'docs/RELEASE_MATURITY.md') $Content
}

function Write-ValidMaturity([string]$Root) {
    Write-Maturity $Root @'
<a id="example"></a>
### Example evidence [stable]
'@
}

function Write-Matrix([string]$Root, [string]$Json) {
    Write-Utf8NoBom (Join-Path $Root 'testdata/release/capabilities.v1.json') $Json
}

function Invoke-Checker([string]$Root) {
    & $checker -RepositoryRoot $Root -MatrixPath (Join-Path $Root 'testdata/release/capabilities.v1.json') -MaturityPath (Join-Path $Root 'docs/RELEASE_MATURITY.md')
}

function Assert-Fails([scriptblock]$Action, [string]$ExpectedMessage) {
    try {
        & $Action
    } catch {
        if ($_.Exception.Message -notlike "*$ExpectedMessage*") {
            throw "expected failure containing '$ExpectedMessage', got '$($_.Exception.Message)'"
        }
        return
    }
    throw "expected failure containing '$ExpectedMessage'"
}

function New-StableGoItem([string]$Id = 'core.example', [string]$Anchor = 'example') {
@"
{
  "id": "$Id",
  "title": "Example evidence",
  "status": "stable",
  "owner": "core",
  "tests": [{ "kind": "go", "package": "./internal/example", "name": "TestExampleEvidence" }],
  "docs_anchor": "$Anchor",
  "external_evidence": []
}
"@
}

$root = New-Fixture
try {
    Write-ValidMaturity $root
    $valid = New-StableGoItem
    Write-Matrix $root "{`n  `"schema_version`": `"v1`",`n  `"items`": [$valid]`n}"
    Invoke-Checker $root

    # Windows PowerShell's JavaScriptSerializer and PowerShell Core's
    # ConvertFrom-Json -AsHashtable must retain the same last-key-wins and
    # case-sensitive key lookup semantics used by this gate.
    Write-Matrix $root "{`n  `"schema_version`": `"v1`",`n  `"items`": {},`n  `"items`": [$valid]`n}"
    Invoke-Checker $root

    Write-Matrix $root "{`n  `"schema_version`": `"v1`",`n  `"Items`": {},`n  `"items`": [$valid]`n}"
    Invoke-Checker $root

    Write-Matrix $root '{"schema_version":"v1","items":['
    Assert-Fails { Invoke-Checker $root } 'malformed matrix JSON'

    $duplicateId = New-StableGoItem
    Write-Matrix $root "{`n  `"schema_version`": `"v1`",`n  `"items`": [$valid, $duplicateId]`n}"
    Assert-Fails { Invoke-Checker $root } 'duplicate capability id'

    Write-Matrix $root @'
{"schema_version":"v1","items":[{"id":"core.example","title":"Example evidence","status":"unknown","owner":"core","tests":[],"docs_anchor":"example","external_evidence":[]}]}
'@
    Assert-Fails { Invoke-Checker $root } 'unknown status'

    Write-Matrix $root @'
{"schema_version":"v1","items":[{"id":"core.example","title":"Example evidence","status":"planned","owner":"unknown","tests":[],"docs_anchor":"example","external_evidence":[]}]}
'@
    Assert-Fails { Invoke-Checker $root } 'unknown owner'

    Write-Matrix $root @'
{"schema_version":"v1","items":[{"id":"core.example","title":"Example evidence","status":"stable","owner":"core","tests":[{"kind":"go","package":"./internal/missing","name":"TestExampleEvidence"}],"docs_anchor":"example","external_evidence":[]}]}
'@
    Assert-Fails { Invoke-Checker $root } 'missing Go package'

    Write-Matrix $root @'
{"schema_version":"v1","items":[{"id":"core.example","title":"Example evidence","status":"stable","owner":"core","tests":[{"kind":"go","package":"./internal/example","name":"TestStaleEvidence"}],"docs_anchor":"example","external_evidence":[]}]}
'@
    Assert-Fails { Invoke-Checker $root } 'missing Go test declaration'

    Write-Matrix $root @'
{"schema_version":"v1","items":[{"id":"core.example","title":"Example evidence","status":"stable","owner":"core","tests":[],"docs_anchor":"example","external_evidence":[]}]}
'@
    Assert-Fails { Invoke-Checker $root } 'stable item has no tests'

    Write-Maturity $root @'
<a id="example"></a>
### Example evidence [planned]
'@
    Write-Matrix $root @'
{"schema_version":"v1","items":[{"id":"core.example","title":"Example evidence","status":"planned","owner":"core","tests":[{"kind":"go","package":"./internal/missing","name":"TestExampleEvidence"}],"docs_anchor":"example","external_evidence":[]}]}
'@
    Assert-Fails { Invoke-Checker $root } 'missing Go package'

    Write-Maturity $root @'
<a id="example"></a>
### Example evidence [unverified]
'@
    Write-Matrix $root @'
{"schema_version":"v1","items":[{"id":"core.example","title":"Example evidence","status":"unverified","owner":"core","tests":[{"kind":"go","package":"./internal/example","name":"TestStaleEvidence"}],"docs_anchor":"example","external_evidence":[]}]}
'@
    Assert-Fails { Invoke-Checker $root } 'missing Go test declaration'

    Write-ValidMaturity $root

    Write-Matrix $root "{`n  `"schema_version`": `"v1`",`n  `"items`": [$(New-StableGoItem 'core.missing-anchor' 'missing-anchor')]`n}"
    Assert-Fails { Invoke-Checker $root } 'missing maturity anchor'

    $first = New-StableGoItem 'core.first' 'example'
    $second = New-StableGoItem 'core.second' 'example'
    Write-Matrix $root "{`n  `"schema_version`": `"v1`",`n  `"items`": [$first, $second]`n}"
    Assert-Fails { Invoke-Checker $root } 'duplicate docs_anchor'

    Write-Matrix $root "{`n  `"schema_version`": `"v1`",`n  `"items`": [$valid]`n}"
    Write-Maturity $root @'
<a id="example"></a>
### Example evidence [stable]
<a id="example"></a>
### Example evidence [stable]
'@
    Assert-Fails { Invoke-Checker $root } 'duplicate maturity anchor'

    Write-Maturity $root @'
<a id="example"></a>
### Example evidence [stable]
<a id="orphan"></a>
### Orphan [planned]
'@
    Assert-Fails { Invoke-Checker $root } 'orphan maturity anchor'

    Write-Maturity $root '<a id="example"></a>'
    Assert-Fails { Invoke-Checker $root } 'missing maturity heading'

    Write-Maturity $root @'
<a id="example"></a>
### Wrong title [stable]
'@
    Assert-Fails { Invoke-Checker $root } 'maturity title mismatch'

    Write-Maturity $root @'
<a id="example"></a>
### Example evidence [planned]
'@
    Assert-Fails { Invoke-Checker $root } 'maturity status mismatch'

    Write-ValidMaturity $root
    Write-Matrix $root '{"schema_version":"v1","items":{}}'
    Assert-Fails { Invoke-Checker $root } 'items must be a JSON array'

    Write-Matrix $root @'
{"schema_version":"v1","items":[{"id":"core.example","title":"Example evidence","status":"stable","owner":"core","tests":{},"docs_anchor":"example","external_evidence":[]}]}
'@
    Assert-Fails { Invoke-Checker $root } 'tests must be a JSON array'

    Write-Matrix $root @'
{"schema_version":"v1","items":[{"id":"external.example","title":"Example evidence","status":"stable","owner":"external-adapter","tests":[{"kind":"powershell","path":"scripts/evidence.Tests.ps1","name":"Test-ExampleEvidence"}],"docs_anchor":"example","external_evidence":"evidence"}]}
'@
    Assert-Fails { Invoke-Checker $root } 'external_evidence must be a JSON array'

    Write-Matrix $root @'
{"schema_version":"v1","items":[{"id":"core.example","title":"Example evidence","status":"stable","owner":"core","tests":[{"kind":"go","package":"./internal/example","name":"TestSubpackageEvidence"}],"docs_anchor":"example","external_evidence":[]}]}
'@
    Assert-Fails { Invoke-Checker $root } 'missing Go test declaration'

    Write-Matrix $root @'
{"schema_version":"v1","items":[{"id":"external.example","title":"Example evidence","status":"stable","owner":"external-adapter","tests":[{"kind":"powershell","path":"scripts/evidence.Tests.ps1","name":"Test-ExampleEvidence"}],"docs_anchor":"example","external_evidence":[]}]}
'@
    Assert-Fails { Invoke-Checker $root } 'external stable item has no external_evidence'

    Write-Maturity $root @'
<a id="example"></a>
### Example evidence [excluded]
'@
    Write-Matrix $root @'
{"schema_version":"v1","items":[{"id":"excluded.example","title":"Example evidence","status":"excluded","owner":"excluded","tests":[{"kind":"go","package":"./internal/example","name":"TestExampleEvidence"}],"docs_anchor":"example","external_evidence":[]}]}
'@
    Assert-Fails { Invoke-Checker $root } 'excluded item must not have tests'

    Write-Matrix $root @'
{"schema_version":"v1","items":[{"id":"excluded.example","title":"Example evidence","status":"excluded","owner":"core","tests":[],"docs_anchor":"example","external_evidence":[]}]}
'@
    Assert-Fails { Invoke-Checker $root } 'excluded item must have owner excluded'

    Write-ValidMaturity $root
    Write-Matrix $root @'
{"schema_version":"v1","items":[{"id":"core.example","title":"Example","status":"stable","owner":"core","tests":[{"kind":"powershell","path":"scripts/evidence.Tests.ps1","name":"Test-ExampleEvidence"}],"docs_anchor":"example","external_evidence":[]}]}
'@
    Write-Maturity $root @'
<a id="example"></a>
### Example [stable]
'@
    Invoke-Checker $root
} finally {
    Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Output 'PASS capability-matrix self-tests'
