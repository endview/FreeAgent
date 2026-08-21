[CmdletBinding()]
param(
    [string]$MatrixPath,
    [string]$MaturityPath,
    [string]$RepositoryRoot
)

$ErrorActionPreference = 'Stop'

function Resolve-RepositoryPath([string]$Root, [string]$Path, [string]$Label) {
    if ([string]::IsNullOrWhiteSpace($Path)) { throw "$Label is required" }
    $fullPath = [IO.Path]::GetFullPath($Path)
    $rootWithSeparator = $Root.TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    if ($fullPath -ne $Root -and -not $fullPath.StartsWith($rootWithSeparator, [StringComparison]::OrdinalIgnoreCase)) {
        throw "$Label must be inside the repository"
    }
    return $fullPath
}

function Get-RepositoryRelativePath([string]$Root, [string]$Path) {
    $trimmedRoot = $Root.TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
    if ($Path -eq $trimmedRoot) { return '.' }
    $relative = $Path.Substring($trimmedRoot.Length).TrimStart([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
    return $relative.Replace('/', '\')
}

function Require-Property($Object, [string]$Name, [string]$ItemId) {
    if ($Object -is [Collections.IDictionary]) {
        if ($Object.Keys -notcontains $Name) { throw "item '$ItemId' is missing $Name" }
        return ,$Object[$Name]
    }
    if ($null -eq $Object.PSObject.Properties[$Name]) { throw "item '$ItemId' is missing $Name" }
    return ,$Object.$Name
}

function Require-JsonArray($Object, [string]$Name, [string]$ItemId) {
    $value = Require-Property $Object $Name $ItemId
    if ($value -isnot [object[]]) { throw "$Name must be a JSON array for '$ItemId'" }
    return ,$value
}

function Get-MaturitySections([string]$Content) {
    $matches = [regex]::Matches($Content, '(?m)^\s*<a\s+id="(?<anchor>[a-z][a-z0-9-]*)"\s*></a>\s*$')
    $sections = @{}
    foreach ($match in $matches) {
        $anchor = $match.Groups['anchor'].Value
        if ($sections.ContainsKey($anchor)) { throw "duplicate maturity anchor: $anchor" }
        $remaining = $Content.Substring($match.Index + $match.Length)
        $line = ($remaining -split "`r?`n" | Where-Object { -not [string]::IsNullOrWhiteSpace($_) } | Select-Object -First 1)
        if ($null -eq $line -or $line -notmatch '^#{1,6}\s+(?<title>.+?)\s+(?:\[(?<status>stable|experimental|unverified|planned|excluded)\]|[^\w\s]+(?<status>stable|experimental|unverified|planned|excluded))\s*$') {
            throw "missing maturity heading for anchor: $anchor"
        }
        $sections[$anchor] = [pscustomobject]@{ Title = $Matches['title']; Status = $Matches['status'] }
    }
    return $sections
}

function Test-GoEvidence([string]$Root, $Test, [string]$ItemId) {
    $package = [string](Require-Property $Test 'package' $ItemId)
    $name = [string](Require-Property $Test 'name' $ItemId)
    if ($package -notmatch '^\./[^\\/].*$' -or $package -match '(^|[\\/])\.\.([\\/]|$)') { throw "invalid Go package for '$ItemId': $package" }
    if ($name -notmatch '^Test[A-Za-z0-9_]+$') { throw "invalid Go test name for '$ItemId': $name" }
    $packagePath = Resolve-RepositoryPath $Root (Join-Path $Root ($package.Substring(2))) 'Go package'
    if (-not (Test-Path -LiteralPath $packagePath -PathType Container)) { throw "missing Go package for '$ItemId': $package" }
    $declaration = '(?m)^\s*func\s+' + [regex]::Escape($name) + '\s*\('
    $found = Get-ChildItem -LiteralPath $packagePath -Filter '*_test.go' -File | Select-String -Pattern $declaration -Quiet
    if (-not $found) { throw "missing Go test declaration for '$ItemId': $package $name" }
}

function Test-PowerShellEvidence([string]$Root, $Test, [string]$ItemId) {
    $path = [string](Require-Property $Test 'path' $ItemId)
    $name = [string](Require-Property $Test 'name' $ItemId)
    if ($path -notmatch '\.Tests\.ps1$' -or $path -match '^[A-Za-z]:' -or $path -match '(^|[\\/])\.\.([\\/]|$)') { throw "invalid PowerShell test path for '$ItemId': $path" }
    $testPath = Resolve-RepositoryPath $Root (Join-Path $Root $path) 'PowerShell test'
    if (-not (Test-Path -LiteralPath $testPath -PathType Leaf)) { throw "missing PowerShell test file for '$ItemId': $path" }
    $tokens = $null
    $errors = $null
    $ast = [Management.Automation.Language.Parser]::ParseFile($testPath, [ref]$tokens, [ref]$errors)
    if ($errors.Count -ne 0) { throw "invalid PowerShell test file for '$ItemId': $path" }
    $definition = $ast.FindAll({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -ceq $name }, $true)
    if ($definition.Count -ne 1) { throw "missing PowerShell test declaration for '$ItemId': $path $name" }
}

if ([string]::IsNullOrWhiteSpace($RepositoryRoot)) { $RepositoryRoot = Split-Path -Parent $PSScriptRoot }
$root = [IO.Path]::GetFullPath($RepositoryRoot)
if (-not (Test-Path -LiteralPath $root -PathType Container)) { throw 'repository root does not exist' }
if ([string]::IsNullOrWhiteSpace($MatrixPath)) { $MatrixPath = Join-Path $root 'testdata/release/capabilities.v1.json' }
if ([string]::IsNullOrWhiteSpace($MaturityPath)) { $MaturityPath = Join-Path $root 'docs/RELEASE_MATURITY.md' }
$matrixFile = Resolve-RepositoryPath $root $MatrixPath 'matrix path'
$maturityFile = Resolve-RepositoryPath $root $MaturityPath 'maturity path'
if (-not (Test-Path -LiteralPath $matrixFile -PathType Leaf)) { throw 'matrix file is missing' }
if (-not (Test-Path -LiteralPath $maturityFile -PathType Leaf)) { throw 'maturity file is missing' }

try {
    $matrixText = Get-Content -LiteralPath $matrixFile -Raw
    if ($PSVersionTable.PSEdition -ceq 'Core') {
        $matrix = $matrixText | ConvertFrom-Json -AsHashtable -ErrorAction Stop
    } else {
        Add-Type -AssemblyName System.Web.Extensions -ErrorAction Stop
        $matrix = (New-Object System.Web.Script.Serialization.JavaScriptSerializer).DeserializeObject($matrixText)
    }
} catch { throw "malformed matrix JSON: $($_.Exception.Message)" }
if ($null -eq $matrix -or $matrix -isnot [Collections.IDictionary] -or $matrix.Keys -notcontains 'schema_version' -or [string]::IsNullOrWhiteSpace([string]$matrix['schema_version'])) { throw 'matrix must include schema_version' }
if ($matrix.Keys -notcontains 'items' -or $null -eq $matrix['items']) { throw 'matrix must include items array' }
if ($matrix['items'] -isnot [object[]]) { throw 'items must be a JSON array' }
$items = $matrix['items']
if ($items.Count -eq 0) { throw 'matrix items array must not be empty' }

$allowedStatuses = @('stable', 'experimental', 'unverified', 'planned', 'excluded')
$allowedOwners = @('core', 'first-party-module', 'extension-host', 'external-adapter', 'excluded')
$maturity = Get-Content -LiteralPath $maturityFile -Raw
$maturitySections = Get-MaturitySections $maturity
$ids = @{}
$anchors = @{}
$stableCount = 0

foreach ($item in $items) {
    if ($null -eq $item) { throw 'matrix contains a null item' }
    $id = [string](Require-Property $item 'id' '<unknown>')
    if ($id -notmatch '^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)+$') { throw "invalid namespaced id: $id" }
    if ($ids.ContainsKey($id)) { throw "duplicate capability id: $id" }
    $ids[$id] = $true
    $title = [string](Require-Property $item 'title' $id)
    $status = [string](Require-Property $item 'status' $id)
    $owner = [string](Require-Property $item 'owner' $id)
    $tests = Require-JsonArray $item 'tests' $id
    $anchor = [string](Require-Property $item 'docs_anchor' $id)
    $evidence = Require-JsonArray $item 'external_evidence' $id
    if ($allowedStatuses -notcontains $status) { throw "unknown status for '$id': $status" }
    if ($allowedOwners -notcontains $owner) { throw "unknown owner for '$id': $owner" }
    if ($anchor -notmatch '^[a-z][a-z0-9-]*$') { throw "invalid docs_anchor for '$id': $anchor" }
    if ($anchors.ContainsKey($anchor)) { throw "duplicate docs_anchor: $anchor" }
    $anchors[$anchor] = $true
    if (-not $maturitySections.ContainsKey($anchor)) { throw "missing maturity anchor for '$id': $anchor" }
    $section = $maturitySections[$anchor]
    if ($section.Title -cne $title) { throw "maturity title mismatch for '$id': $anchor" }
    if ($section.Status -cne $status) { throw "maturity status mismatch for '$id': $anchor" }
    if ($status -eq 'stable' -and $tests.Count -eq 0) { throw "stable item has no tests: $id" }
    if ($status -eq 'stable' -and $owner -eq 'external-adapter' -and $evidence.Count -eq 0) { throw "external stable item has no external_evidence: $id" }
    if ($status -eq 'excluded') {
        if ($owner -ne 'excluded') { throw "excluded item must have owner excluded: $id" }
        if ($tests.Count -ne 0) { throw "excluded item must not have tests: $id" }
        continue
    }
    foreach ($test in $tests) {
        $kind = [string](Require-Property $test 'kind' $id)
        switch ($kind) {
            # A test reference is release metadata even before a capability is
            # stable.  Validate every declared Go reference so planned and
            # unverified entries cannot retain packages or test names that no
            # longer exist.  Stable items keep the stronger non-empty evidence
            # requirement above.
            'go' { Test-GoEvidence $root $test $id }
            'powershell' { if ($status -eq 'stable') { Test-PowerShellEvidence $root $test $id } }
            default { throw "unknown test kind for '$id': $kind" }
        }
    }
    if ($status -eq 'stable') { $stableCount++ }
}

foreach ($anchor in $maturitySections.Keys) {
    if (-not $anchors.ContainsKey($anchor)) { throw "orphan maturity anchor: $anchor" }
}

Write-Output "PASS capability matrix: $($items.Count) items; $stableCount stable; matrix $(Get-RepositoryRelativePath $root $matrixFile)"
