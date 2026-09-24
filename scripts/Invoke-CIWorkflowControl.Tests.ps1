[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:Control = Join-Path $PSScriptRoot 'Invoke-CIWorkflowControl.ps1'
$script:Controller = Join-Path $PSScriptRoot 'Invoke-CIReleaseJob.ps1'
$script:Bootstrap = Join-Path $PSScriptRoot 'New-CIReleaseWorkspace.ps1'
$script:Generator = Join-Path $PSScriptRoot 'New-PublicStaging.ps1'
$script:Verifier = Join-Path $PSScriptRoot 'Test-PublicStaging.ps1'
$script:CommandRunner = Join-Path $PSScriptRoot 'Invoke-CIReleaseCommand.ps1'
$script:WorkflowJob = Join-Path $PSScriptRoot 'Invoke-CIWorkflowJob.ps1'
$script:Git = (@(
    Get-Command git -CommandType Application -ErrorAction Stop
))[0].Source
$script:Utf8NoBom = New-Object Text.UTF8Encoding($false)
$script:Cases = 0
$script:Assertions = 0
$script:Failures = New-Object 'Collections.Generic.List[string]'
$script:Lifecycle = $null
$script:SupplyChainRelativePaths = @(
    'supply-chain/sbom.spdx.json',
    'supply-chain/provenance.unsigned.v1.json',
    'supply-chain/checksums.sha256'
)
$script:ReleaseVersion = 'v0.1.1'
$script:CrossBuildPayloadMap = [ordered]@{
    'VERSION' = 'VERSION'
    'LICENSE' = 'LICENSE'
    'THIRD_PARTY_NOTICES.md' = 'THIRD_PARTY_NOTICES.md'
    'docs/INSTALL.md' = 'INSTALL.md'
    'docs/QUICKSTART.md' = 'QUICKSTART.md'
    'docs/KNOWN_LIMITATIONS.md' = 'KNOWN_LIMITATIONS.md'
    'docs/RELEASE_NOTES_v0.1.1.md' = 'RELEASE_NOTES.md'
    'docs/CHECKSUMS.md' = 'VERIFY_CHECKSUMS.md'
    'docs/PACKAGE_CONFIG.md' = 'config/README.md'
    'docs/PACKAGE_DATA.md' = 'data/README.md'
    'examples/current-v1.bootstrap.seed.json' = 'config/current-v1.bootstrap.seed.json'
    'examples/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/LICENSE' = 'config/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/LICENSE'
    'examples/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/module.yaml' = 'config/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/module.yaml'
    'examples/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/README.md' = 'config/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/README.md'
    'examples/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/content/context.json' = 'config/bootstrap-artifacts/freeagent.builtin.context.basic/1.0.0/content/context.json'
    'examples/bootstrap-artifacts/freeagent.builtin.model.echo/2.0.0/LICENSE' = 'config/bootstrap-artifacts/freeagent.builtin.model.echo/2.0.0/LICENSE'
    'examples/bootstrap-artifacts/freeagent.builtin.model.echo/2.0.0/module.yaml' = 'config/bootstrap-artifacts/freeagent.builtin.model.echo/2.0.0/module.yaml'
    'examples/bootstrap-artifacts/freeagent.builtin.model.echo/2.0.0/README.md' = 'config/bootstrap-artifacts/freeagent.builtin.model.echo/2.0.0/README.md'
    'examples/bootstrap-artifacts/freeagent.builtin.model.echo/2.0.0/implementation/adapter.json' = 'config/bootstrap-artifacts/freeagent.builtin.model.echo/2.0.0/implementation/adapter.json'
    'examples/bootstrap-artifacts/freeagent.builtin.model.echo/2.0.0/schemas/config.schema.json' = 'config/bootstrap-artifacts/freeagent.builtin.model.echo/2.0.0/schemas/config.schema.json'
}

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

function Assert-Matches {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Pattern
    )
    $script:Assertions++
    if ($Value -cnotmatch $Pattern) {
        throw "ASSERT_FAIL $Name value=[$Value] pattern=[$Pattern]"
    }
}

function Assert-ThrowsPrefix {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][scriptblock]$Body,
        [Parameter(Mandatory = $true)][string]$Prefix
    )
    $message = $null
    try {
        & $Body
    } catch {
        $message = $_.Exception.Message
    }
    Assert-True -Name "$Name throws" -Condition (
        -not [string]::IsNullOrWhiteSpace($message)
    )
    $script:Assertions++
    if (-not $message.StartsWith($Prefix, [StringComparison]::Ordinal)) {
        throw (
            "ASSERT_FAIL $Name exact prefix expected=[$Prefix] " +
            "actual=[$message]"
        )
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

function Write-Utf8NoBom {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text
    )
    $parent = [IO.Path]::GetDirectoryName($Path)
    if (-not [string]::IsNullOrWhiteSpace($parent)) {
        [void][IO.Directory]::CreateDirectory($parent)
    }
    [IO.File]::WriteAllText($Path, $Text, $script:Utf8NoBom)
}

function Write-CrossBuildSourceFixture {
    param([Parameter(Mandatory = $true)][string]$Repository)
    foreach ($relative in $script:CrossBuildPayloadMap.Keys) {
        $text = if ($relative -ceq 'VERSION') {
            "$($script:ReleaseVersion)`n"
        } elseif ($relative.EndsWith('.json', [StringComparison]::Ordinal)) {
            "{}`n"
        } elseif ($relative.EndsWith('.yaml', [StringComparison]::Ordinal)) {
            "kind: fixture`n"
        } else {
            "fixture release payload $relative`n"
        }
        Write-Utf8NoBom `
            -Path (Join-Path $Repository (
                $relative.Replace(
                    [char]47,
                    [IO.Path]::DirectorySeparatorChar
                )
            )) `
            -Text $text
    }
}

function Get-CrossBuildArtifactRelativePaths {
    param(
        [Parameter(Mandatory = $true)][string]$Goos,
        [Parameter(Mandatory = $true)][string]$Goarch
    )
    $relativePaths = [System.Collections.Generic.List[string]]::new()
    [void]$relativePaths.Add('public-tree-manifest.v1.json')
    foreach ($relativePath in $script:CrossBuildPayloadMap.Values) {
        [void]$relativePaths.Add($relativePath)
    }
    $extension = if ($Goos -ceq 'windows') { '.exe' } else { '' }
    [void]$relativePaths.Add("bin/freeagent-$Goos-$Goarch$extension")
    foreach ($relativePath in $script:SupplyChainRelativePaths) {
        [void]$relativePaths.Add($relativePath)
    }
    return $relativePaths.ToArray()
}

function Get-LowerSha256 {
    param([Parameter(Mandatory = $true)][string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).
        Hash.ToLowerInvariant()
}

function Assert-Utf8NoBomLfText {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Expected
    )
    [byte[]]$actualBytes = [IO.File]::ReadAllBytes($Path)
    [byte[]]$expectedBytes = $script:Utf8NoBom.GetBytes($Expected)
    Assert-Equal -Name "$Name byte length" `
        -Expected $expectedBytes.Length `
        -Actual $actualBytes.Length
    Assert-Equal -Name "$Name sha256" `
        -Expected (
            [BitConverter]::ToString(
                [Security.Cryptography.SHA256]::Create().
                    ComputeHash($expectedBytes)
            ).Replace('-', '').ToLowerInvariant()
        ) `
        -Actual (Get-LowerSha256 -Path $Path)
    Assert-True -Name "$Name has no carriage returns" -Condition (
        @($actualBytes | Where-Object { $_ -eq 13 }).Count -eq 0
    )
    Assert-True -Name "$Name has no UTF-8 BOM" -Condition (
        $actualBytes.Length -lt 3 -or
        -not (
            $actualBytes[0] -eq 0xEF -and
            $actualBytes[1] -eq 0xBB -and
            $actualBytes[2] -eq 0xBF
        )
    )
}

function Read-JsonFile {
    param([Parameter(Mandatory = $true)][string]$Path)
    try {
        return [IO.File]::ReadAllText(
            $Path,
            (New-Object Text.UTF8Encoding($false, $true))
        ) | ConvertFrom-Json -ErrorAction Stop
    } catch {
        throw "ASSERT_FAIL invalid JSON path=[$Path]"
    }
}

function Write-JsonFixture {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)]$Value
    )
    $text = $Value | ConvertTo-Json -Depth 100
    Write-Utf8NoBom -Path $Path -Text ($text + "`n")
}

function Get-StringSha256Base64 {
    param([Parameter(Mandatory = $true)][string]$Value)
    $hasher = [Security.Cryptography.SHA256]::Create()
    try {
        $digest = $hasher.ComputeHash($script:Utf8NoBom.GetBytes($Value))
    } finally {
        $hasher.Dispose()
    }
    return 'h1:' + [Convert]::ToBase64String($digest)
}

function Get-StringSha256Hex {
    param([Parameter(Mandatory = $true)][string]$Value)
    $hasher = [Security.Cryptography.SHA256]::Create()
    try {
        $digest = $hasher.ComputeHash($script:Utf8NoBom.GetBytes($Value))
    } finally {
        $hasher.Dispose()
    }
    return [BitConverter]::ToString($digest).
        Replace('-', '').
        ToLowerInvariant()
}

function Get-StringSha512Base64 {
    param([Parameter(Mandatory = $true)][string]$Value)
    $hasher = [Security.Cryptography.SHA512]::Create()
    try {
        $digest = $hasher.ComputeHash($script:Utf8NoBom.GetBytes($Value))
    } finally {
        $hasher.Dispose()
    }
    return [Convert]::ToBase64String($digest)
}

function Assert-SupplyChainArtifacts {
    param(
        [Parameter(Mandatory = $true)][string]$ArtifactRoot,
        [Parameter(Mandatory = $true)][string]$Revision,
        [Parameter(Mandatory = $true)][bool]$ExpectedRunSucceeded
    )
    foreach ($relative in $script:SupplyChainRelativePaths) {
        Assert-True -Name "supply-chain artifact exists $relative" -Condition (
            Test-Path `
                -LiteralPath (
                    Join-Path $ArtifactRoot (
                        $relative.Replace(
                            [char]47,
                            [IO.Path]::DirectorySeparatorChar
                        )
                    )
                ) `
                -PathType Leaf
        )
    }

    $sbomPath = Join-Path $ArtifactRoot (
        'supply-chain/sbom.spdx.json'.Replace(
            [char]47,
            [IO.Path]::DirectorySeparatorChar
        )
    )
    $sbom = Read-JsonFile -Path $sbomPath
    Assert-Equal -Name 'SPDX version' `
        -Expected 'SPDX-2.3' `
        -Actual $sbom.spdxVersion
    Assert-Equal -Name 'SPDX data license' `
        -Expected 'CC0-1.0' `
        -Actual $sbom.dataLicense
    Assert-True -Name 'SPDX namespace binds revision' -Condition (
        ([string]$sbom.documentNamespace).IndexOf(
            $Revision,
            [StringComparison]::Ordinal
        ) -ge 0
    )
    Assert-True -Name 'SPDX namespace binds release version' -Condition (
        ([string]$sbom.documentNamespace).IndexOf(
            "/$($script:ReleaseVersion)/$Revision/",
            [StringComparison]::Ordinal
        ) -ge 0
    )
    $mainPackages = @($sbom.packages | Where-Object {
        [string]$_.SPDXID -ceq 'SPDXRef-Package-FreeAgent'
    })
    Assert-Equal -Name 'SPDX main package count' `
        -Expected 1 `
        -Actual $mainPackages.Count
    Assert-Equal -Name 'SPDX main package release version' `
        -Expected $script:ReleaseVersion `
        -Actual ([string]$mainPackages[0].versionInfo)

    $provenancePath = Join-Path $ArtifactRoot (
        'supply-chain/provenance.unsigned.v1.json'.Replace(
            [char]47,
            [IO.Path]::DirectorySeparatorChar
        )
    )
    $provenance = Read-JsonFile -Path $provenancePath
    Assert-Equal -Name 'provenance statement type' `
        -Expected 'https://in-toto.io/Statement/v1' `
        -Actual $provenance._type
    Assert-Equal -Name 'provenance predicate type' `
        -Expected 'https://slsa.dev/provenance/v1' `
        -Actual $provenance.predicateType
    $extensionName =
        'https://github.com/endview/freeagent/provenance/metadata/v1'
    $extensionProperty = $provenance.predicate.PSObject.
        Properties[$extensionName]
    Assert-True -Name 'provenance metadata extension exists' -Condition (
        $null -ne $extensionProperty
    )
    Assert-Equal -Name 'provenance authentication is absent' `
        -Expected 'none' `
        -Actual $extensionProperty.Value.authentication
    Assert-Equal -Name 'provenance envelope is absent' `
        -Expected 'absent' `
        -Actual $extensionProperty.Value.envelope
    Assert-Equal -Name 'provenance trust is informational only' `
        -Expected 'informational-only' `
        -Actual $extensionProperty.Value.securityClaim
    Assert-True -Name 'provenance Run result is Boolean' -Condition (
        $extensionProperty.Value.runSucceeded -is [bool]
    )
    Assert-Equal -Name 'provenance records Run result' `
        -Expected $ExpectedRunSucceeded `
        -Actual $extensionProperty.Value.runSucceeded
    $external = $provenance.predicate.buildDefinition.externalParameters
    Assert-Equal -Name 'provenance release version' `
        -Expected $script:ReleaseVersion `
        -Actual ([string]$external.releaseVersion)
    Assert-Equal -Name 'provenance revision' `
        -Expected $Revision `
        -Actual ([string]$external.revision)
    $versionDependencies = @(
        $provenance.predicate.buildDefinition.resolvedDependencies |
            Where-Object { [string]$_.uri -ceq 'file:VERSION' }
    )
    Assert-Equal -Name 'provenance VERSION dependency count' `
        -Expected 1 `
        -Actual $versionDependencies.Count
    Assert-Equal -Name 'provenance VERSION digest' `
        -Expected (Get-StringSha256Hex -Value ($script:ReleaseVersion + [char]10)) `
        -Actual ([string]$versionDependencies[0].digest.sha256)

    $checksumsPath = Join-Path $ArtifactRoot (
        'supply-chain/checksums.sha256'.Replace(
            [char]47,
            [IO.Path]::DirectorySeparatorChar
        )
    )
    $checksumText = [IO.File]::ReadAllText(
        $checksumsPath,
        (New-Object Text.UTF8Encoding($false, $true))
    )
    Assert-True -Name 'checksums use LF only' -Condition (
        $checksumText.Contains("`n") -and
        -not $checksumText.Contains("`r") -and
        $checksumText.EndsWith("`n", [StringComparison]::Ordinal)
    )
    $checksumLines = @(
        $checksumText.TrimEnd([char]10).Split([char]10)
    )
    $expectedRelativePaths = New-Object 'Collections.Generic.List[string]'
    foreach ($file in Get-ChildItem `
        -LiteralPath $ArtifactRoot `
        -File `
        -Recurse `
        -Force) {
        $relative = $file.FullName.Substring($ArtifactRoot.Length).
            TrimStart([char]92, [char]47).
            Replace([char]92, [char]47)
        if ($relative -cne 'supply-chain/checksums.sha256') {
            [void]$expectedRelativePaths.Add($relative)
        }
    }
    [string[]]$sortedExpected = $expectedRelativePaths.ToArray()
    [Array]::Sort($sortedExpected, [StringComparer]::Ordinal)
    Assert-Equal -Name 'checksum entry count' `
        -Expected $sortedExpected.Count `
        -Actual $checksumLines.Count
    for ($index = 0; $index -lt $sortedExpected.Count; $index++) {
        $relative = $sortedExpected[$index]
        $absolute = Join-Path $ArtifactRoot (
            $relative.Replace([char]47, [IO.Path]::DirectorySeparatorChar)
        )
        Assert-Equal -Name "checksum entry $index" `
            -Expected (
                (Get-LowerSha256 -Path $absolute) + '  ' + $relative
            ) `
            -Actual $checksumLines[$index]
    }
}

function Get-ControlPin {
    param(
        [Parameter(Mandatory = $true)][string]$Source,
        [Parameter(Mandatory = $true)][string]$Name
    )
    $pattern = '(?m)^\$script:' + [Regex]::Escape($Name) +
        "\s*=\s*'([0-9a-f]{64})'"
    $matches = [Regex]::Matches($Source, $pattern)
    Assert-Equal -Name "$Name assignment count" -Expected 1 -Actual $matches.Count
    return $matches[0].Groups[1].Value
}

function Invoke-Git {
    param(
        [Parameter(Mandatory = $true)][string]$Repository,
        [Parameter(Mandatory = $true)][string[]]$Arguments
    )
    $output = New-Object 'Collections.Generic.List[string]'
    $savedPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        & $script:Git -C $Repository @Arguments 2>&1 |
            ForEach-Object { [void]$output.Add([string]$_) }
        $exitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $savedPreference
    }
    if ($exitCode -ne 0) {
        throw "fixture git failed exit=$exitCode output=$($output -join ' | ')"
    }
    return ($output -join "`n").Trim()
}

function Commit-All {
    param(
        [Parameter(Mandatory = $true)][string]$Repository,
        [Parameter(Mandatory = $true)][string]$Message
    )
    [void](Invoke-Git -Repository $Repository -Arguments @('add', '--all'))
    [void](Invoke-Git -Repository $Repository -Arguments @(
        '-c', 'commit.gpgsign=false',
        'commit', '--quiet', '--no-verify', '-m', $Message
    ))
    return Invoke-Git -Repository $Repository -Arguments @('rev-parse', 'HEAD')
}

function New-ControlFixture {
    param(
        [Parameter(Mandatory = $true)][string]$Container,
        [Parameter(Mandatory = $true)][string]$Name
    )
    $root = Join-Path $Container $Name
    $repository = Join-Path $root 'repository with space'
    [void][IO.Directory]::CreateDirectory(
        (Join-Path $repository 'scripts')
    )
    [void](Invoke-Git -Repository $repository -Arguments @('init', '--quiet'))
    [void](Invoke-Git -Repository $repository -Arguments @(
        'config', 'user.name', 'FreeAgent CI Test'
    ))
    [void](Invoke-Git -Repository $repository -Arguments @(
        'config', 'user.email', 'freeagent-ci-test@example.invalid'
    ))
    [void](Invoke-Git -Repository $repository -Arguments @(
        'config', 'core.autocrlf', 'false'
    ))
    foreach ($source in @(
        $script:Controller,
        $script:Bootstrap,
        $script:Generator,
        $script:Verifier,
        $script:CommandRunner,
        $script:WorkflowJob
    )) {
        [IO.File]::Copy(
            $source,
            (Join-Path $repository ('scripts/' + [IO.Path]::GetFileName($source))),
            $false
        )
    }
    $modulePath = 'example.invalid/mod'
    $moduleVersion = 'v1.0.0'
    $moduleSum = Get-StringSha256Base64 -Value 'fixture module tree'
    $goModSum = Get-StringSha256Base64 -Value 'fixture dependency go.mod'
    $moduleLicenseText = "MIT License fixture.`n"
    Write-Utf8NoBom -Path (Join-Path $repository 'go.mod') -Text (
        "module fixture.invalid/freeagent`n`n" +
        "go 1.26.5`n`n" +
        "require $modulePath $moduleVersion`n"
    )
    Write-Utf8NoBom -Path (Join-Path $repository 'go.sum') -Text (
        "$modulePath $moduleVersion $moduleSum`n" +
        "$modulePath $moduleVersion/go.mod $goModSum`n"
    )
    [IO.File]::Copy(
        (Join-Path ([IO.Path]::GetDirectoryName($PSScriptRoot)) 'LICENSE'),
        (Join-Path $repository 'LICENSE'),
        $false
    )
    Write-CrossBuildSourceFixture -Repository $repository
    $assetPath = Join-Path $repository 'assets/banner.txt'
    $assetText = "fixture asset`n"
    Write-Utf8NoBom -Path $assetPath -Text $assetText
    $reactVersion = '19.2.8'
    $viteVersion = '7.3.6'
    $reactResolved =
        'https://registry.npmjs.org/react/-/react-19.2.8.tgz'
    $viteResolved =
        'https://registry.npmjs.org/vite/-/vite-7.3.6.tgz'
    $reactIntegrity = 'sha512-' +
        (Get-StringSha512Base64 -Value 'workflow fixture react tarball')
    $viteIntegrity = 'sha512-' +
        (Get-StringSha512Base64 -Value 'workflow fixture vite tarball')
    $reactLicenseRelative = 'third_party/npm/react/19.2.8/LICENSE'
    $viteLicenseRelative = 'third_party/npm/vite/7.3.6/LICENSE'
    $reactLicensePath = Join-Path $repository (
        $reactLicenseRelative.Replace(
            [char]47,
            [IO.Path]::DirectorySeparatorChar
        )
    )
    $viteLicensePath = Join-Path $repository (
        $viteLicenseRelative.Replace(
            [char]47,
            [IO.Path]::DirectorySeparatorChar
        )
    )
    $reactLicenseText = "React MIT workflow fixture license.`n"
    $viteLicenseText = "Vite MIT workflow fixture license.`n"
    Write-Utf8NoBom -Path $reactLicensePath -Text $reactLicenseText
    Write-Utf8NoBom -Path $viteLicensePath -Text $viteLicenseText
    $reactRequiredFile = [ordered]@{
        path = $reactLicenseRelative
        role = 'license'
        size = $script:Utf8NoBom.GetByteCount($reactLicenseText)
        sha256 = Get-LowerSha256 -Path $reactLicensePath
    }
    $viteRequiredFile = [ordered]@{
        path = $viteLicenseRelative
        role = 'license'
        size = $script:Utf8NoBom.GetByteCount($viteLicenseText)
        sha256 = Get-LowerSha256 -Path $viteLicensePath
    }
    $chunkRelative = 'internal/controlweb/dist/assets/react.js'
    $chunkPath = Join-Path $repository (
        $chunkRelative.Replace([char]47, [IO.Path]::DirectorySeparatorChar)
    )
    $chunkText = "/* React workflow fixture runtime */`n"
    Write-Utf8NoBom -Path $chunkPath -Text $chunkText
    $packageLock = [ordered]@{
        name = '@freeagent/controlweb'
        version = '0.0.0'
        lockfileVersion = 3
        requires = $true
        packages = [ordered]@{
            '' = [ordered]@{
                name = '@freeagent/controlweb'
                version = '0.0.0'
                dependencies = [ordered]@{ react = $reactVersion }
                devDependencies = [ordered]@{ vite = $viteVersion }
            }
            'node_modules/react' = [ordered]@{
                version = $reactVersion
                resolved = $reactResolved
                integrity = $reactIntegrity
                license = 'MIT'
            }
            'node_modules/vite' = [ordered]@{
                version = $viteVersion
                resolved = $viteResolved
                integrity = $viteIntegrity
                license = 'MIT'
                dev = $true
            }
        }
    }
    $packageLockPath =
        Join-Path $repository 'internal/controlweb/package-lock.json'
    Write-JsonFixture -Path $packageLockPath -Value $packageLock
    $frontendManifest = [ordered]@{
        schema_version = 1
        kind = 'freeagent-npm-dependency-licenses'
        package_lock = [ordered]@{
            path = 'internal/controlweb/package-lock.json'
            sha256 = Get-LowerSha256 -Path $packageLockPath
            lockfile_version = 3
        }
        build_environment = [ordered]@{
            node_version = '24.19.0'
            npm_version = '12.0.2'
            registry = 'https://registry.npmjs.org/'
            install_scripts = 'disabled'
        }
        packages = @(
            [ordered]@{
                name = 'react'
                version = $reactVersion
                dependency_kind = 'runtime'
                optional = $false
                resolved = $reactResolved
                integrity = $reactIntegrity
                declared_license_expression = 'MIT'
                source_url = $reactResolved
                notice_id = 'npm-111111111111111111111111'
                required_files = @($reactRequiredFile)
            },
            [ordered]@{
                name = 'vite'
                version = $viteVersion
                dependency_kind = 'build'
                optional = $false
                resolved = $viteResolved
                integrity = $viteIntegrity
                declared_license_expression = 'MIT'
                source_url = $viteResolved
                notice_id = 'npm-222222222222222222222222'
                required_files = @($viteRequiredFile)
            }
        )
        distributed_chunks = @(
            [ordered]@{
                path = $chunkRelative
                sha256 = Get-LowerSha256 -Path $chunkPath
                packages = @("react@$reactVersion")
            }
        )
    }
    Write-JsonFixture `
        -Path (
            Join-Path $repository (
                'testdata/release/frontend-dependency-licenses.v1.json'
            )
        ) `
        -Value $frontendManifest
    $dependencyManifest = [ordered]@{
        schema_version = 1
        kind = 'freeagent-go-dependency-licenses'
        main_module = 'fixture.invalid/freeagent'
        compatibility_policy = [ordered]@{
            id = 'freeagent-agpl-3.0-test-policy'
            version = 1
            project_license = 'AGPL-3.0-only'
            source_dependency_mode = 'module-reference-only'
            binary_targets = @(
                'darwin/amd64',
                'darwin/arm64',
                'linux/amd64',
                'linux/arm64',
                'windows/amd64',
                'windows/arm64'
            )
            cgo_enabled = $false
            go_version = 'go1.26.5'
        }
        license_refs = @()
        modules = @(
            [ordered]@{
                path = $modulePath
                version = $moduleVersion
                module_sum = $moduleSum
                go_mod_sum = $goModSum
                declared_license_expression = 'MIT'
                source_url = 'https://example.invalid/mod'
                notice_id = 'go-0123456789abcdef01234567'
                compatibility_conclusion = [ordered]@{
                    status = 'reviewed-compatible-for-policy'
                    policy_id = 'freeagent-agpl-3.0-test-policy'
                    policy_version = 1
                }
                source_spdx_scan = [ordered]@{
                    algorithm = 'raw-ascii-marker-path-line-v1'
                    occurrence_count = 0
                    sha256 =
                        'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'
                    expressions = @()
                }
                required_files = @(
                    [ordered]@{
                        path = 'LICENSE'
                        role = 'license'
                        size = $script:Utf8NoBom.GetByteCount(
                            $moduleLicenseText
                        )
                        sha256 = Get-StringSha256Hex `
                            -Value $moduleLicenseText
                    }
                )
            }
        )
    }
    Write-JsonFixture `
        -Path (
            Join-Path $repository (
                'testdata/release/dependency-licenses.v1.json'
            )
        ) `
        -Value $dependencyManifest
    $assetManifest = [ordered]@{
        schema_version = 1
        kind = 'freeagent-distributed-assets'
        assets = @(
            [ordered]@{
                path = 'assets/banner.txt'
                size = $script:Utf8NoBom.GetByteCount($assetText)
                sha256 = Get-LowerSha256 -Path $assetPath
                origin = 'project-owned'
                source_url = 'project://github.com/endview/freeagent'
                spdx_expression = 'AGPL-3.0-only'
                notice_id = ''
                required_files = @()
            },
            [ordered]@{
                path = $chunkRelative
                size = $script:Utf8NoBom.GetByteCount($chunkText)
                sha256 = Get-LowerSha256 -Path $chunkPath
                origin = 'third-party'
                source_url = $reactResolved
                spdx_expression = 'MIT'
                notice_id = 'asset-111111111111111111111111'
                required_files = @($reactRequiredFile)
            },
            [ordered]@{
                path = $reactLicenseRelative
                size = $script:Utf8NoBom.GetByteCount($reactLicenseText)
                sha256 = Get-LowerSha256 -Path $reactLicensePath
                origin = 'third-party'
                source_url = $reactResolved
                spdx_expression = 'MIT'
                notice_id = 'asset-222222222222222222222222'
                required_files = @($reactRequiredFile)
            },
            [ordered]@{
                path = $viteLicenseRelative
                size = $script:Utf8NoBom.GetByteCount($viteLicenseText)
                sha256 = Get-LowerSha256 -Path $viteLicensePath
                origin = 'third-party'
                source_url = $viteResolved
                spdx_expression = 'MIT'
                notice_id = 'asset-333333333333333333333333'
                required_files = @($viteRequiredFile)
            }
        )
    }
    Write-JsonFixture `
        -Path (
            Join-Path $repository (
                'testdata/release/distributed-assets.v1.json'
            )
        ) `
        -Value $assetManifest
    Write-Utf8NoBom -Path (Join-Path $repository 'README.md') -Text (
        "# workflow control fixture`n"
    )
    $revision = Commit-All -Repository $repository -Message 'fixture'
    return [pscustomobject]@{
        Root = $root
        Repository = $repository
        Revision = $revision
        ModulePath = $modulePath
        ModuleVersion = $moduleVersion
        ModuleLicenseText = $moduleLicenseText
    }
}

function New-ControlBase {
    param(
        [Parameter(Mandatory = $true)]$Fixture,
        [Parameter(Mandatory = $true)][string]$Name,
        [string]$ControlSource = $script:Control
    )
    $base = Join-Path $Fixture.Root $Name
    $controlDirectory = Join-Path $base 'control'
    [void][IO.Directory]::CreateDirectory($controlDirectory)
    $snapshot = Join-Path $controlDirectory 'workflow-control.ps1'
    [IO.File]::Copy($ControlSource, $snapshot, $false)
    return [pscustomobject]@{
        Base = $base
        Control = $snapshot
        Output = Join-Path $Fixture.Root "$Name-output.txt"
        Environment = Join-Path $Fixture.Root "$Name-environment.txt"
    }
}

function Read-KeyValueFile {
    param([Parameter(Mandatory = $true)][string]$Path)
    $values = [ordered]@{}
    foreach ($line in [IO.File]::ReadAllLines($Path, $script:Utf8NoBom)) {
        $separator = $line.IndexOf('=')
        if ($separator -le 0) {
            throw "ASSERT_FAIL invalid key-value line=[$line]"
        }
        $name = $line.Substring(0, $separator)
        if ($values.Contains($name)) {
            throw "ASSERT_FAIL duplicate key=[$name]"
        }
        $values[$name] = $line.Substring($separator + 1)
    }
    return $values
}

function Invoke-ControlPrepare {
    param(
        [Parameter(Mandatory = $true)]$Fixture,
        [Parameter(Mandatory = $true)]$BaseState,
        [string]$JobKind = 'permanent',
        [string]$TargetGoos = '',
        [string]$TargetGoarch = ''
    )
    $parameters = [ordered]@{
        Prepare = $true
        JobKind = $JobKind
        BaseRoot = $BaseState.Base
        RepositoryRoot = $Fixture.Repository
        Revision = $Fixture.Revision
        GitHubOutputPath = $BaseState.Output
        GitHubEnvironmentPath = $BaseState.Environment
    }
    if (-not [string]::IsNullOrEmpty($TargetGoos)) {
        $parameters.TargetGoos = $TargetGoos
    }
    if (-not [string]::IsNullOrEmpty($TargetGoarch)) {
        $parameters.TargetGoarch = $TargetGoarch
    }
    & $BaseState.Control @parameters 6>$null
    $moduleDirectory = Join-Path (
        Join-Path $BaseState.Base 'trusted/work/cache/module'
    ) (
        (
            $Fixture.ModulePath + '@' + $Fixture.ModuleVersion
        ).Replace([char]47, [IO.Path]::DirectorySeparatorChar)
    )
    Write-Utf8NoBom `
        -Path (Join-Path $moduleDirectory 'LICENSE') `
        -Text $Fixture.ModuleLicenseText
    return [pscustomobject]@{
        Output = Read-KeyValueFile -Path $BaseState.Output
        Environment = Read-KeyValueFile -Path $BaseState.Environment
    }
}

foreach ($dependency in @(
    $script:Control,
    $script:Controller,
    $script:Bootstrap,
    $script:Generator,
    $script:Verifier,
    $script:CommandRunner,
    $script:WorkflowJob
)) {
    if (-not (Test-Path -LiteralPath $dependency -PathType Leaf)) {
        throw "CI_WORKFLOW_CONTROL_SELFTEST_DEPENDENCY_MISSING path=$dependency"
    }
}

$testTempRoot = if (-not [string]::IsNullOrWhiteSpace(
    $env:FREEAGENT_CI_WORKFLOW_CONTROL_TEST_TEMP_ROOT
)) {
    [IO.Path]::GetFullPath(
        $env:FREEAGENT_CI_WORKFLOW_CONTROL_TEST_TEMP_ROOT
    )
} else {
    [IO.Path]::GetTempPath()
}
if (-not (Test-Path -LiteralPath $testTempRoot -PathType Container)) {
    throw (
        'FREEAGENT_CI_WORKFLOW_CONTROL_TEST_TEMP_ROOT must be an ' +
        'existing directory.'
    )
}
$suiteRoot = Join-Path $testTempRoot (
    'fawc-' + [Guid]::NewGuid().ToString('N').Substring(0, 16)
)
[void][IO.Directory]::CreateDirectory($suiteRoot)

try {
    Invoke-Case 'workflow control is ASCII and parses in Windows PowerShell' {
        $tokens = $null
        $errors = $null
        [void][Management.Automation.Language.Parser]::ParseFile(
            $script:Control,
            [ref]$tokens,
            [ref]$errors
        )
        Assert-Equal -Name 'parse error count' -Expected 0 -Actual @($errors).Count
        $bytes = [IO.File]::ReadAllBytes($script:Control)
        Assert-Equal -Name 'non-ASCII byte count' -Expected 0 -Actual (
            @($bytes | Where-Object { $_ -gt 127 }).Count
        )
    }

    Invoke-Case 'fixed dependency pins match the current script bytes' {
        $source = [IO.File]::ReadAllText($script:Control, $script:Utf8NoBom)
        $dependencies = [ordered]@{
            ControllerSha256 = $script:Controller
            BootstrapSha256 = $script:Bootstrap
            GeneratorSha256 = $script:Generator
            VerifierSha256 = $script:Verifier
            CommandRunnerSha256 = $script:CommandRunner
            WorkflowJobSha256 = $script:WorkflowJob
        }
        foreach ($name in $dependencies.Keys) {
            $pin = Get-ControlPin -Source $source -Name $name
            Assert-Equal -Name "$name matches current bytes" `
                -Expected (Get-LowerSha256 -Path $dependencies[$name]) `
                -Actual $pin
        }
    }

    Invoke-Case 'Windows workflow tests use a private user-profile home' {
        $source = [IO.File]::ReadAllText(
            $script:WorkflowJob,
            $script:Utf8NoBom
        )
        Assert-True -Name 'workflow job defines private test-home creation' `
            -Condition $source.Contains('function New-CIWWindowsPrivateTestHome')
        Assert-True -Name 'private test home uses a protected DACL' `
            -Condition $source.Contains('SetAccessRuleProtection($true, $false)')
        Assert-True -Name 'private test home sets the current-user owner' `
            -Condition $source.Contains('$security.SetOwner($current)')
        Assert-True -Name 'Windows test environment uses the private home' `
            -Condition $source.Contains('$goEnvironment.USERPROFILE = $windowsTestHome')
        Assert-True -Name 'private test home remains outside runner temp' `
            -Condition (-not $source.Contains('$goEnvironment.USERPROFILE = $executionTemp'))
    }

    Invoke-Case 'dispatch selectors and seal pins require exact canonical text' {
        Assert-ThrowsPrefix `
            -Name 'case-variant job kind' `
            -Prefix (
                'CI_WORKFLOW_CONTROL_FAIL ' +
                'code=CIWC_JOB_KIND_INVALID'
            ) `
            -Body {
                & $script:Control `
                    -Initialize `
                    -JobKind WINDOWS `
                    -BaseRoot $suiteRoot 6>$null
            }
        Assert-ThrowsPrefix `
            -Name 'blank artifact set pin' `
            -Prefix (
                'CI_WORKFLOW_CONTROL_FAIL ' +
                'code=CIWC_ARTIFACT_SET_PIN_INVALID'
            ) `
            -Body {
                & $script:Control `
                    -Seal `
                    -JobKind permanent `
                    -BaseRoot $suiteRoot `
                    -ManifestSha256 ('0' * 64) `
                    -ExpectedArtifactSetSha256 ' ' 6>$null
            }
        Assert-ThrowsPrefix `
            -Name 'noncanonical manifest pin' `
            -Prefix (
                'CI_WORKFLOW_CONTROL_FAIL ' +
                'code=CIWC_MANIFEST_PIN_INVALID'
            ) `
            -Body {
                & $script:Control `
                    -Finalize `
                    -JobKind permanent `
                    -BaseRoot $suiteRoot `
                    -ManifestSha256 ('A' * 64) `
                    -GitHubOutputPath (
                        Join-Path $suiteRoot 'invalid-pin-output.txt'
                    ) 6>$null
            }
        Assert-ThrowsPrefix `
            -Name 'non-cross target' `
            -Prefix (
                'CI_WORKFLOW_CONTROL_FAIL ' +
                'code=CIWC_CROSS_TARGET_INVALID'
            ) `
            -Body {
                & $script:Control `
                    -Initialize `
                    -JobKind permanent `
                    -BaseRoot $suiteRoot `
                    -TargetGoos linux `
                    -TargetGoarch amd64 6>$null
            }
    }

    Invoke-Case 'every job artifact contract requires supply-chain metadata' {
        $tokens = $null
        $errors = $null
        $ast = [Management.Automation.Language.Parser]::ParseFile(
            $script:Control,
            [ref]$tokens,
            [ref]$errors
        )
        Assert-Equal -Name 'artifact contract parse errors' `
            -Expected 0 `
            -Actual @($errors).Count
        $functions = @($ast.FindAll(
            {
                param($node)
                $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
                $node.Name -ceq 'Get-CIWCArtifactContract'
            },
            $true
        ))
        Assert-Equal -Name 'artifact contract function count' `
            -Expected 1 `
            -Actual $functions.Count
        foreach ($kind in @(
            'permanent',
            'linux-quality',
            'windows',
            'linux-race',
            'cross-build'
        )) {
            $goos = if ($kind -ceq 'cross-build') { 'linux' } else { '' }
            $goarch = if ($kind -ceq 'cross-build') { 'amd64' } else { '' }
            $invocation =
                $functions[0].Extent.Text +
                "`nGet-CIWCArtifactContract " +
                "-Kind '$kind' -Goos '$goos' -Goarch '$goarch'"
            $contract = & ([ScriptBlock]::Create($invocation))
            foreach ($relative in $script:SupplyChainRelativePaths) {
                Assert-Equal `
                    -Name "$kind requires $relative exactly once" `
                    -Expected 1 `
                    -Actual @(
                        $contract.Required |
                            Where-Object { $_ -ceq $relative }
                    ).Count
            }
            $payloadInvocation = $invocation + ' -PayloadOnly'
            $payloadContract = & ([ScriptBlock]::Create(
                $payloadInvocation
            ))
            foreach ($relative in $script:SupplyChainRelativePaths) {
                Assert-Equal `
                    -Name "$kind base contract excludes $relative" `
                    -Expected 0 `
                    -Actual @(
                        $payloadContract.Required |
                            Where-Object { $_ -ceq $relative }
                    ).Count
            }
        }
    }

    Invoke-Case 'real Git fixture binds contract and completes through finalize' {
        $fixture = New-ControlFixture `
            -Container $suiteRoot `
            -Name 'lifecycle fixture'
        $baseState = New-ControlBase `
            -Fixture $fixture `
            -Name 'lifecycle base'
        $prepare = Invoke-ControlPrepare `
            -Fixture $fixture `
            -BaseState $baseState `
            -JobKind cross-build `
            -TargetGoos windows `
            -TargetGoarch amd64

        Assert-Equal -Name 'prepare output key count' `
            -Expected 9 `
            -Actual $prepare.Output.Count
        Assert-Equal -Name 'prepare source path' `
            -Expected (Join-Path $baseState.Base 'trusted/work/source') `
            -Actual $prepare.Output['source']
        Assert-Equal -Name 'prepare stage path' `
            -Expected (Join-Path $baseState.Base 'trusted/work/stage') `
            -Actual $prepare.Output['stage']
        Assert-Equal -Name 'prepare artifact path' `
            -Expected (Join-Path $baseState.Base 'trusted/work/artifact') `
            -Actual $prepare.Output['artifact']
        Assert-Equal -Name 'prepare control snapshot path' `
            -Expected $baseState.Control `
            -Actual $prepare.Output['control']
        Assert-Matches -Name 'prepare manifest hash' `
            -Value $prepare.Output['manifest_sha256'] `
            -Pattern '^[0-9a-f]{64}$'
        Assert-Equal -Name 'environment GOWORK' `
            -Expected 'off' `
            -Actual $prepare.Environment['GOWORK']
        Assert-Equal -Name 'environment cache root' `
            -Expected (
                Join-Path $prepare.Output['cache'] 'build'
            ) `
            -Actual $prepare.Environment['GOCACHE']

        $buildContextPath = Join-Path (
            Join-Path $baseState.Base 'control'
        ) 'build-context.v1'
        $buildContext = Read-KeyValueFile -Path $buildContextPath
        Assert-Equal -Name 'build context key count' `
            -Expected 3 `
            -Actual $buildContext.Count
        Assert-Equal -Name 'build context schema' `
            -Expected '1' `
            -Actual $buildContext['schema']
        Assert-Equal -Name 'build context revision' `
            -Expected $fixture.Revision `
            -Actual $buildContext['revision']
        Assert-Matches -Name 'build context created UTC' `
            -Value $buildContext['created_utc'] `
            -Pattern '^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$'
        $parsedCreated = [DateTimeOffset]::ParseExact(
            $buildContext['created_utc'],
            'yyyy-MM-ddTHH:mm:ssZ',
            [Globalization.CultureInfo]::InvariantCulture,
            [Globalization.DateTimeStyles]::AssumeUniversal
        )
        Assert-Equal -Name 'build context created UTC offset' `
            -Expected ([TimeSpan]::Zero) `
            -Actual $parsedCreated.Offset
        $expectedBuildContext =
            "schema=1`n" +
            "revision=$($fixture.Revision)`n" +
            "created_utc=$($buildContext['created_utc'])`n"
        Assert-Utf8NoBomLfText `
            -Name 'build context canonical bytes' `
            -Path $buildContextPath `
            -Expected $expectedBuildContext
        $buildContextSha256 = Get-LowerSha256 -Path $buildContextPath

        $jobContractPath = Join-Path (
            Join-Path $baseState.Base 'control'
        ) 'job-contract.v1'
        $jobContract = Read-KeyValueFile -Path $jobContractPath
        Assert-Equal -Name 'job contract key count' `
            -Expected 5 `
            -Actual $jobContract.Count
        Assert-Equal -Name 'job contract schema' `
            -Expected '2' `
            -Actual $jobContract['schema']
        Assert-Equal -Name 'job contract build context binding' `
            -Expected $buildContextSha256 `
            -Actual $jobContract['build_context_sha256']
        Assert-Utf8NoBomLfText `
            -Name 'job contract canonical bytes' `
            -Path $jobContractPath `
            -Expected (
                "schema=2`n" +
                "job_kind=cross-build`n" +
                "target_goos=windows`n" +
                "target_goarch=amd64`n" +
                "build_context_sha256=$buildContextSha256`n"
            )

        Assert-ThrowsPrefix `
            -Name 'cross target splice' `
            -Prefix (
                'CI_WORKFLOW_CONTROL_FAIL ' +
                'code=CIWC_JOB_CONTRACT_MISMATCH'
            ) `
            -Body {
                & $baseState.Control `
                    -Initialize `
                    -JobKind cross-build `
                    -BaseRoot $baseState.Base `
                    -TargetGoos windows `
                    -TargetGoarch arm64 6>$null
            }
        & $baseState.Control `
            -Initialize `
            -JobKind cross-build `
            -BaseRoot $baseState.Base `
            -TargetGoos windows `
            -TargetGoarch amd64 6>$null
        Assert-ThrowsPrefix `
            -Name 'repeat initialize' `
            -Prefix (
                'CI_WORKFLOW_CONTROL_FAIL ' +
                'code=CIWC_PHASE_INVALID'
            ) `
            -Body {
                & $baseState.Control `
                    -Initialize `
                    -JobKind cross-build `
                    -BaseRoot $baseState.Base `
                    -TargetGoos windows `
                    -TargetGoarch amd64 6>$null
            }
        [string[]]$artifactNames = @(
            Get-ChildItem `
                -LiteralPath $prepare.Output['artifact'] `
                -Force |
                ForEach-Object { $_.Name }
        )
        Assert-Equal -Name 'cross initialize artifact closure' `
            -Expected 'public-tree-manifest.v1.json' `
            -Actual ($artifactNames -join "`n")

        Assert-ThrowsPrefix `
            -Name 'finalize before run' `
            -Prefix (
                'CI_WORKFLOW_CONTROL_FAIL ' +
                'code=CIWC_PHASE_INVALID'
            ) `
            -Body {
                & $baseState.Control `
                    -Finalize `
                    -JobKind cross-build `
                    -BaseRoot $baseState.Base `
                    -TargetGoos windows `
                    -TargetGoarch amd64 `
                    -ManifestSha256 (
                        $prepare.Output['manifest_sha256']
                    ) `
                    -GitHubOutputPath (
                        Join-Path $fixture.Root 'premature-finalize.txt'
                    ) 6>$null
            }

        $toolDirectory = Join-Path $fixture.Root 'tools'
        [void][IO.Directory]::CreateDirectory($toolDirectory)
        $fakeGo = Join-Path $toolDirectory 'go.cmd'
        Write-Utf8NoBom -Path $fakeGo -Text @'
@echo off
if "%~1"=="mod" exit /b 0
if "%~1"=="build" goto build
exit /b 3
:build
shift
:loop
if "%~1"=="" exit /b 4
if "%~1"=="-o" goto output
shift
goto loop
:output
shift
>"%~1" echo fake-binary
exit /b 0
'@
        $savedPath = $env:PATH
        try {
            $env:PATH =
                $toolDirectory + [IO.Path]::PathSeparator + $savedPath
            & $baseState.Control `
                -Run `
                -JobKind cross-build `
                -BaseRoot $baseState.Base `
                -TargetGoos windows `
                -TargetGoarch amd64 6>$null
        } finally {
            $env:PATH = $savedPath
        }
        Assert-ThrowsPrefix `
            -Name 'repeat run' `
            -Prefix (
                'CI_WORKFLOW_CONTROL_FAIL ' +
                'code=CIWC_PHASE_INVALID'
            ) `
            -Body {
                & $baseState.Control `
                    -Run `
                    -JobKind cross-build `
                    -BaseRoot $baseState.Base `
                    -TargetGoos windows `
                    -TargetGoarch amd64 6>$null
            }

        $finalizeOutput = Join-Path $fixture.Root 'finalize-output.txt'
        & $baseState.Control `
            -Finalize `
            -JobKind cross-build `
            -BaseRoot $baseState.Base `
            -TargetGoos windows `
            -TargetGoarch amd64 `
            -ManifestSha256 $prepare.Output['manifest_sha256'] `
            -GitHubOutputPath $finalizeOutput 6>$null
        $finalize = Read-KeyValueFile -Path $finalizeOutput
        Assert-Equal -Name 'finalize output key count' `
            -Expected 4 `
            -Actual $finalize.Count
        Assert-Matches -Name 'artifact set hash' `
            -Value $finalize['artifact_set_sha256'] `
            -Pattern '^[0-9a-f]{64}$'
        Assert-Equal -Name 'artifact file count' `
            -Expected ([string](
                (Get-CrossBuildArtifactRelativePaths `
                    -Goos windows `
                    -Goarch amd64).Count
            )) `
            -Actual $finalize['artifact_file_count']
        Assert-True -Name 'artifact bytes positive' -Condition (
            [int64]$finalize['artifact_bytes'] -gt 0
        )
        Assert-Equal -Name 'finalize records successful Run' `
            -Expected 'true' `
            -Actual $finalize['run_succeeded']
        Assert-SupplyChainArtifacts `
            -ArtifactRoot $prepare.Output['artifact'] `
            -Revision $fixture.Revision `
            -ExpectedRunSucceeded $true
        Assert-ThrowsPrefix `
            -Name 'repeat finalize' `
            -Prefix (
                'CI_WORKFLOW_CONTROL_FAIL ' +
                'code=CIWC_PHASE_INVALID'
            ) `
            -Body {
                & $baseState.Control `
                    -Finalize `
                    -JobKind cross-build `
                    -BaseRoot $baseState.Base `
                    -TargetGoos windows `
                    -TargetGoarch amd64 `
                    -ManifestSha256 (
                        $prepare.Output['manifest_sha256']
                    ) `
                    -GitHubOutputPath (
                        Join-Path $fixture.Root 'repeat-finalize.txt'
                    ) 6>$null
            }

        $script:Lifecycle = [pscustomobject]@{
            Fixture = $fixture
            BaseState = $baseState
            Prepare = $prepare
            Finalize = $finalize
        }
    }

    Invoke-Case 'seal rejects pin mismatch and tamper before succeeding once' {
        Assert-True -Name 'lifecycle prerequisite exists' -Condition (
            $null -ne $script:Lifecycle
        )
        $state = $script:Lifecycle
        Assert-ThrowsPrefix `
            -Name 'wrong artifact set pin' `
            -Prefix (
                'CI_WORKFLOW_CONTROL_FAIL ' +
                'code=CIWC_PHASE_INVALID'
            ) `
            -Body {
                & $state.BaseState.Control `
                    -Seal `
                    -JobKind cross-build `
                    -BaseRoot $state.BaseState.Base `
                    -TargetGoos windows `
                    -TargetGoarch amd64 `
                    -ManifestSha256 (
                        $state.Prepare.Output['manifest_sha256']
                    ) `
                    -ExpectedArtifactSetSha256 ('0' * 64) 6>$null
            }

        $provenancePath = Join-Path $state.Prepare.Output['artifact'] (
            'supply-chain/provenance.unsigned.v1.json'.Replace(
                [char]47,
                [IO.Path]::DirectorySeparatorChar
            )
        )
        [byte[]]$provenanceBytes = [IO.File]::ReadAllBytes($provenancePath)
        [byte[]]$tamperedProvenance = New-Object byte[] (
            $provenanceBytes.Length + 1
        )
        [Array]::Copy(
            $provenanceBytes,
            $tamperedProvenance,
            $provenanceBytes.Length
        )
        $tamperedProvenance[$tamperedProvenance.Length - 1] = 0x20
        [IO.File]::WriteAllBytes($provenancePath, $tamperedProvenance)
        $tamperedSha256 = Get-LowerSha256 -Path $provenancePath
        Assert-ThrowsPrefix `
            -Name 'supply-chain metadata tamper' `
            -Prefix 'CI_RELEASE_JOB_FAIL code=CIJ_' `
            -Body {
                & $state.BaseState.Control `
                    -Seal `
                    -JobKind cross-build `
                    -BaseRoot $state.BaseState.Base `
                    -TargetGoos windows `
                    -TargetGoarch amd64 `
                    -ManifestSha256 (
                        $state.Prepare.Output['manifest_sha256']
                    ) `
                    -ExpectedArtifactSetSha256 (
                        $state.Finalize['artifact_set_sha256']
                    ) 6>$null
            }
        Assert-Equal -Name 'Seal never regenerates tampered metadata' `
            -Expected $tamperedSha256 `
            -Actual (Get-LowerSha256 -Path $provenancePath)
        [IO.File]::WriteAllBytes($provenancePath, $provenanceBytes)

        Write-Utf8NoBom `
            -Path (
                Join-Path $state.Prepare.Output['artifact'] 'unexpected.txt'
            ) `
            -Text "tamper`n"
        Assert-ThrowsPrefix `
            -Name 'unexpected artifact tamper' `
            -Prefix 'CI_RELEASE_JOB_FAIL code=CIJ_ARTIFACT_LAYOUT_INVALID' `
            -Body {
                & $state.BaseState.Control `
                    -Seal `
                    -JobKind cross-build `
                    -BaseRoot $state.BaseState.Base `
                    -TargetGoos windows `
                    -TargetGoarch amd64 `
                    -ManifestSha256 (
                        $state.Prepare.Output['manifest_sha256']
                    ) `
                    -ExpectedArtifactSetSha256 (
                        $state.Finalize['artifact_set_sha256']
                    ) 6>$null
            }
        Remove-Item `
            -LiteralPath (
                Join-Path $state.Prepare.Output['artifact'] 'unexpected.txt'
            ) `
            -Force
        & $state.BaseState.Control `
            -Seal `
            -JobKind cross-build `
            -BaseRoot $state.BaseState.Base `
            -TargetGoos windows `
            -TargetGoarch amd64 `
            -ManifestSha256 (
                $state.Prepare.Output['manifest_sha256']
            ) `
            -ExpectedArtifactSetSha256 (
                $state.Finalize['artifact_set_sha256']
            ) 6>$null
        Assert-ThrowsPrefix `
            -Name 'repeat seal' `
            -Prefix (
                'CI_WORKFLOW_CONTROL_FAIL ' +
                'code=CIWC_PHASE_INVALID'
            ) `
            -Body {
                & $state.BaseState.Control `
                    -Seal `
                    -JobKind cross-build `
                    -BaseRoot $state.BaseState.Base `
                    -TargetGoos windows `
                    -TargetGoarch amd64 `
                    -ManifestSha256 (
                        $state.Prepare.Output['manifest_sha256']
                    ) `
                    -ExpectedArtifactSetSha256 (
                        $state.Finalize['artifact_set_sha256']
                    ) 6>$null
            }
    }

    Invoke-Case 'sealed build context rejects a valid-looking splice' {
        $fixture = New-ControlFixture `
            -Container $suiteRoot `
            -Name 'build context splice fixture'
        $baseState = New-ControlBase `
            -Fixture $fixture `
            -Name 'build context splice base'
        [void](Invoke-ControlPrepare `
            -Fixture $fixture `
            -BaseState $baseState `
            -JobKind permanent)
        $contextPath = Join-Path (
            Join-Path $baseState.Base 'control'
        ) 'build-context.v1'
        [byte[]]$originalContextBytes = [IO.File]::ReadAllBytes($contextPath)
        $context = Read-KeyValueFile -Path $contextPath
        $splicedRevision = if (
            $fixture.Revision -cne ('a' * 40)
        ) {
            'a' * 40
        } else {
            'b' * 40
        }
        Write-Utf8NoBom -Path $contextPath -Text (
            "schema=1`n" +
            "revision=$splicedRevision`n" +
            "created_utc=$($context['created_utc'])`n"
        )
        Assert-ThrowsPrefix `
            -Name 'valid-looking build context splice' `
            -Prefix (
                'CI_WORKFLOW_CONTROL_FAIL ' +
                'code=CIWC_JOB_CONTRACT_MISMATCH'
            ) `
            -Body {
                & $baseState.Control `
                    -Initialize `
                    -JobKind permanent `
                    -BaseRoot $baseState.Base 6>$null
            }
        [IO.File]::WriteAllBytes($contextPath, $originalContextBytes)
        Write-Utf8NoBom -Path $contextPath -Text (
            "schema=1`n" +
            "revision=$($fixture.Revision)`n" +
            "created_utc=2026-07-24T01:02:03.000Z`n"
        )
        Assert-ThrowsPrefix `
            -Name 'noncanonical build context' `
            -Prefix (
                'CI_WORKFLOW_CONTROL_FAIL ' +
                'code=CIWC_BUILD_CONTEXT_INVALID'
            ) `
            -Body {
                & $baseState.Control `
                    -Initialize `
                    -JobKind permanent `
                    -BaseRoot $baseState.Base 6>$null
            }
    }

    Invoke-Case 'Finalize verifies the base contract before metadata generation' {
        $fixture = New-ControlFixture `
            -Container $suiteRoot `
            -Name 'pre-verify fixture'
        $baseState = New-ControlBase `
            -Fixture $fixture `
            -Name 'pre-verify base'
        $prepare = Invoke-ControlPrepare `
            -Fixture $fixture `
            -BaseState $baseState `
            -JobKind cross-build `
            -TargetGoos linux `
            -TargetGoarch amd64
        & $baseState.Control `
            -Initialize `
            -JobKind cross-build `
            -BaseRoot $baseState.Base `
            -TargetGoos linux `
            -TargetGoarch amd64 6>$null

        $tools = Join-Path $fixture.Root 'tools'
        [void][IO.Directory]::CreateDirectory($tools)
        Write-Utf8NoBom `
            -Path (Join-Path $tools 'go.cmd') `
            -Text @'
@echo off
if "%~1"=="mod" exit /b 0
if "%~1"=="build" goto build
exit /b 3
:build
shift
:loop
if "%~1"=="" exit /b 4
if "%~1"=="-o" goto output
shift
goto loop
:output
shift
>"%~1" echo fake-binary
exit /b 0
'@
        $savedPath = $env:PATH
        try {
            $env:PATH = $tools + [IO.Path]::PathSeparator + $savedPath
            & $baseState.Control `
                -Run `
                -JobKind cross-build `
                -BaseRoot $baseState.Base `
                -TargetGoos linux `
                -TargetGoarch amd64 6>$null
        } finally {
            $env:PATH = $savedPath
        }

        $supplyChainDirectory = Join-Path (
            $prepare.Output['artifact']
        ) 'supply-chain'
        Assert-True -Name 'metadata absent before Finalize' -Condition (
            -not (Test-Path -LiteralPath $supplyChainDirectory)
        )
        Write-Utf8NoBom `
            -Path (
                Join-Path $prepare.Output['artifact'] 'unexpected-before.txt'
            ) `
            -Text "reject before generation`n"
        Assert-ThrowsPrefix `
            -Name 'invalid base contract' `
            -Prefix 'CI_RELEASE_JOB_FAIL code=CIJ_ARTIFACT_LAYOUT_INVALID' `
            -Body {
                & $baseState.Control `
                    -Finalize `
                    -JobKind cross-build `
                    -BaseRoot $baseState.Base `
                    -TargetGoos linux `
                    -TargetGoarch amd64 `
                    -ManifestSha256 $prepare.Output['manifest_sha256'] `
                    -GitHubOutputPath (
                        Join-Path $fixture.Root 'pre-verify-finalize.txt'
                    ) 6>$null
            }
        Assert-True -Name 'failed pre-Verify never generates metadata' `
            -Condition (
                -not (Test-Path -LiteralPath $supplyChainDirectory)
            )
    }

    Invoke-Case 'Finalize strictly validates and pins generated expected artifact state' {
        $fixture = New-ControlFixture `
            -Container $suiteRoot `
            -Name 'expected artifact state fixture'
        $baseState = New-ControlBase `
            -Fixture $fixture `
            -Name 'expected artifact state base'
        $prepare = Invoke-ControlPrepare `
            -Fixture $fixture `
            -BaseState $baseState `
            -JobKind cross-build `
            -TargetGoos linux `
            -TargetGoarch amd64
        & $baseState.Control `
            -Initialize `
            -JobKind cross-build `
            -BaseRoot $baseState.Base `
            -TargetGoos linux `
            -TargetGoarch amd64 6>$null

        $tools = Join-Path $fixture.Root 'expected artifact state tools'
        [void][IO.Directory]::CreateDirectory($tools)
        Write-Utf8NoBom `
            -Path (Join-Path $tools 'go.cmd') `
            -Text @'
@echo off
if "%~1"=="mod" exit /b 0
if "%~1"=="build" goto build
exit /b 3
:build
shift
:loop
if "%~1"=="" exit /b 4
if "%~1"=="-o" goto output
shift
goto loop
:output
shift
>"%~1" echo fake-binary
exit /b 0
'@
        $savedPath = $env:PATH
        try {
            $env:PATH = $tools + [IO.Path]::PathSeparator + $savedPath
            & $baseState.Control `
                -Run `
                -JobKind cross-build `
                -BaseRoot $baseState.Base `
                -TargetGoos linux `
                -TargetGoarch amd64 6>$null
        } finally {
            $env:PATH = $savedPath
        }

        $controllerPath = Join-Path (
            Join-Path $baseState.Base 'control'
        ) 'release-job-controller.ps1'
        $controlPath = Join-Path (
            Join-Path $baseState.Base 'control'
        ) 'workflow-control.ps1'
        $controllerSource = [IO.File]::ReadAllText(
            $controllerPath,
            $script:Utf8NoBom
        )
        $controlSource = [IO.File]::ReadAllText(
            $controlPath,
            $script:Utf8NoBom
        )
        $originalControllerPin = Get-ControlPin `
            -Source $controlSource `
            -Name 'ControllerSha256'
        Assert-Equal -Name 'fixture controller starts at pinned bytes' `
            -Expected $originalControllerPin `
            -Actual (Get-LowerSha256 -Path $controllerPath)
        Assert-Equal -Name 'controller pin occurrence count' `
            -Expected 1 `
            -Actual (
                [Regex]::Matches(
                    $controlSource,
                    [Regex]::Escape($originalControllerPin)
                ).Count
            )

        $mutations = @(
            [pscustomobject]@{
                Name = 'missing expected artifact set hash'
                Pattern = (
                    '(?m)^        ExpectedArtifactSetSha256 = ' +
                    '\$expectedArtifactSetSha256\r?\n'
                )
                Replacement = ''
                Prefix = (
                    'CI_WORKFLOW_CONTROL_FAIL ' +
                    'code=CIWC_SUPPLY_CHAIN_RESULT_INVALID'
                )
            },
            [pscustomobject]@{
                Name = 'missing expected artifact file count'
                Pattern = (
                    '(?m)^        ExpectedArtifactFileCount = ' +
                    '\$expectedArtifactFileCount\r?\n'
                )
                Replacement = ''
                Prefix = (
                    'CI_WORKFLOW_CONTROL_FAIL ' +
                    'code=CIWC_SUPPLY_CHAIN_RESULT_INVALID'
                )
            },
            [pscustomobject]@{
                Name = 'missing expected artifact bytes'
                Pattern = (
                    '(?m)^        ExpectedArtifactBytes = ' +
                    '\$expectedArtifactBytes\r?\n'
                )
                Replacement = ''
                Prefix = (
                    'CI_WORKFLOW_CONTROL_FAIL ' +
                    'code=CIWC_SUPPLY_CHAIN_RESULT_INVALID'
                )
            },
            [pscustomobject]@{
                Name = 'noncanonical expected artifact set hash'
                Pattern = (
                    '(?m)^(        ExpectedArtifactSetSha256 = )' +
                    '\$expectedArtifactSetSha256$'
                )
                Replacement = ('$1' + ("'A'" + ' * 64'))
                Prefix = (
                    'CI_WORKFLOW_CONTROL_FAIL ' +
                    'code=CIWC_SUPPLY_CHAIN_RESULT_INVALID'
                )
            },
            [pscustomobject]@{
                Name = 'wrong expected artifact file count type'
                Pattern = (
                    '(?m)^(        ExpectedArtifactFileCount = )' +
                    '\$expectedArtifactFileCount$'
                )
                Replacement = ('$1' + "'5'")
                Prefix = (
                    'CI_WORKFLOW_CONTROL_FAIL ' +
                    'code=CIWC_SUPPLY_CHAIN_RESULT_INVALID'
                )
            },
            [pscustomobject]@{
                Name = 'wrong expected artifact bytes type'
                Pattern = (
                    '(?m)^(        ExpectedArtifactBytes = )' +
                    '\$expectedArtifactBytes$'
                )
                Replacement = ('$1' + "'5'")
                Prefix = (
                    'CI_WORKFLOW_CONTROL_FAIL ' +
                    'code=CIWC_SUPPLY_CHAIN_RESULT_INVALID'
                )
            },
            [pscustomobject]@{
                Name = 'valid-looking expected artifact set splice'
                Pattern = (
                    '(?m)^(        ExpectedArtifactSetSha256 = )' +
                    '\$expectedArtifactSetSha256$'
                )
                Replacement = ('$1' + ("'0'" + ' * 64'))
                Prefix = (
                    'CI_RELEASE_JOB_FAIL ' +
                    'code=CIJ_ARTIFACT_SET_PIN_MISMATCH'
                )
            }
        )

        $variantNumber = 0
        foreach ($mutation in $mutations) {
            $variantNumber++
            $variantBase = Join-Path $fixture.Root (
                'expected-artifact-state-variant-' + $variantNumber
            )
            Copy-Item `
                -LiteralPath $baseState.Base `
                -Destination $variantBase `
                -Recurse `
                -Force
            $variantController = Join-Path (
                Join-Path $variantBase 'control'
            ) 'release-job-controller.ps1'
            $variantControl = Join-Path (
                Join-Path $variantBase 'control'
            ) 'workflow-control.ps1'

            $matchCount = [Regex]::Matches(
                $controllerSource,
                $mutation.Pattern
            ).Count
            Assert-Equal `
                -Name "$($mutation.Name) mutation match count" `
                -Expected 1 `
                -Actual $matchCount
            $mutatedController = [Regex]::Replace(
                $controllerSource,
                $mutation.Pattern,
                $mutation.Replacement
            )
            Write-Utf8NoBom `
                -Path $variantController `
                -Text $mutatedController
            $mutatedControllerSha256 = Get-LowerSha256 `
                -Path $variantController
            $mutatedControl = $controlSource.Replace(
                $originalControllerPin,
                $mutatedControllerSha256
            )
            Write-Utf8NoBom -Path $variantControl -Text $mutatedControl
            Assert-Equal `
                -Name "$($mutation.Name) controller pin" `
                -Expected $mutatedControllerSha256 `
                -Actual (
                    Get-ControlPin `
                        -Source $mutatedControl `
                        -Name 'ControllerSha256'
                )

            $variantOutput = Join-Path $fixture.Root (
                'expected-artifact-state-output-' + $variantNumber + '.txt'
            )
            Assert-ThrowsPrefix `
                -Name $mutation.Name `
                -Prefix $mutation.Prefix `
                -Body {
                    & $variantControl `
                        -Finalize `
                        -JobKind cross-build `
                        -BaseRoot $variantBase `
                        -TargetGoos linux `
                        -TargetGoarch amd64 `
                        -ManifestSha256 (
                            $prepare.Output['manifest_sha256']
                        ) `
                        -GitHubOutputPath $variantOutput 6>$null
                }
            Assert-True `
                -Name "$($mutation.Name) generation completed" `
                -Condition (
                    Test-Path `
                        -LiteralPath (
                            Join-Path $variantBase (
                                'trusted/work/artifact/supply-chain/' +
                                'checksums.sha256'
                            )
                        ) `
                        -PathType Leaf
                )
            Assert-True `
                -Name "$($mutation.Name) never finalizes" `
                -Condition (
                    -not (
                        Test-Path `
                            -LiteralPath (
                                Join-Path $variantBase (
                                    'control/phase-finalized.v1'
                                )
                            )
                    )
                )
        }
    }

    Invoke-Case 'contract splice damaged phase and incomplete Run fail closed' {
        $spliceFixture = New-ControlFixture `
            -Container $suiteRoot `
            -Name 'job kind splice fixture'
        $spliceBase = New-ControlBase `
            -Fixture $spliceFixture `
            -Name 'job kind splice base'
        [void](Invoke-ControlPrepare `
            -Fixture $spliceFixture `
            -BaseState $spliceBase `
            -JobKind linux-quality)
        Assert-ThrowsPrefix `
            -Name 'job kind splice' `
            -Prefix (
                'CI_WORKFLOW_CONTROL_FAIL ' +
                'code=CIWC_JOB_CONTRACT_MISMATCH'
            ) `
            -Body {
                & $spliceBase.Control `
                    -Initialize `
                    -JobKind permanent `
                    -BaseRoot $spliceBase.Base 6>$null
            }

        $damagedFixture = New-ControlFixture `
            -Container $suiteRoot `
            -Name 'damaged phase fixture'
        $damagedBase = New-ControlBase `
            -Fixture $damagedFixture `
            -Name 'damaged phase base'
        [void](Invoke-ControlPrepare `
            -Fixture $damagedFixture `
            -BaseState $damagedBase `
            -JobKind permanent)
        Write-Utf8NoBom `
            -Path (
                Join-Path $damagedBase.Base (
                    'control/phase-prepared.v1'
                )
            ) `
            -Text "damaged`n"
        Assert-ThrowsPrefix `
            -Name 'damaged prepared phase' `
            -Prefix (
                'CI_WORKFLOW_CONTROL_FAIL ' +
                'code=CIWC_PHASE_INVALID'
            ) `
            -Body {
                & $damagedBase.Control `
                    -Initialize `
                    -JobKind permanent `
                    -BaseRoot $damagedBase.Base 6>$null
            }

        $incompleteFixture = New-ControlFixture `
            -Container $suiteRoot `
            -Name 'incomplete run fixture'
        $incompleteBase = New-ControlBase `
            -Fixture $incompleteFixture `
            -Name 'incomplete run base'
        $incompletePrepare = Invoke-ControlPrepare `
            -Fixture $incompleteFixture `
            -BaseState $incompleteBase `
            -JobKind linux-quality
        & $incompleteBase.Control `
            -Initialize `
            -JobKind linux-quality `
            -BaseRoot $incompleteBase.Base 6>$null
        $contractHash = Get-LowerSha256 -Path (
            Join-Path $incompleteBase.Base 'control/job-contract.v1'
        )
        Write-Utf8NoBom `
            -Path (
                Join-Path $incompleteBase.Base (
                    'control/phase-run-started.v1'
                )
            ) `
            -Text (
                "phase=run-started`n" +
                "contract_sha256=$contractHash`n" +
                "manifest_sha256=`n" +
                "artifact_set_sha256=`n"
            )
        Assert-ThrowsPrefix `
            -Name 'Run without terminal receipt' `
            -Prefix (
                'CI_WORKFLOW_CONTROL_FAIL ' +
                'code=CIWC_PHASE_INVALID'
            ) `
            -Body {
                & $incompleteBase.Control `
                    -Finalize `
                    -JobKind linux-quality `
                    -BaseRoot $incompleteBase.Base `
                    -ManifestSha256 (
                        $incompletePrepare.Output['manifest_sha256']
                    ) `
                    -GitHubOutputPath (
                        Join-Path $incompleteFixture.Root (
                            'incomplete-finalize.txt'
                        )
                    ) 6>$null
            }
    }

    Invoke-Case 'failed evidence Run seals unproved output but cannot claim proof' {
        $fixture = New-ControlFixture `
            -Container $suiteRoot `
            -Name 'failed evidence run fixture'
        $baseState = New-ControlBase `
            -Fixture $fixture `
            -Name 'failed evidence run base'
        $prepare = Invoke-ControlPrepare `
            -Fixture $fixture `
            -BaseState $baseState `
            -JobKind linux-quality
        & $baseState.Control `
            -Initialize `
            -JobKind linux-quality `
            -BaseRoot $baseState.Base 6>$null

        $tools = Join-Path $fixture.Root 'tools'
        [void][IO.Directory]::CreateDirectory($tools)
        Write-Utf8NoBom `
            -Path (Join-Path $tools 'go.cmd') `
            -Text @'
@echo off
if "%~1"=="mod" exit /b 0
exit /b 9
'@
        $savedPath = $env:PATH
        try {
            $env:PATH = $tools + [IO.Path]::PathSeparator + $savedPath
            Assert-ThrowsPrefix `
                -Name 'evidence Run failure' `
                -Prefix 'CI_WORKFLOW_JOB_FAIL code=' `
                -Body {
                    & $baseState.Control `
                        -Run `
                        -JobKind linux-quality `
                        -BaseRoot $baseState.Base 6>$null
                }
        } finally {
            $env:PATH = $savedPath
        }

        $finalizePath = Join-Path $fixture.Root 'failed-finalize.txt'
        & $baseState.Control `
            -Finalize `
            -JobKind linux-quality `
            -BaseRoot $baseState.Base `
            -ManifestSha256 $prepare.Output['manifest_sha256'] `
            -GitHubOutputPath $finalizePath 6>$null
        $finalize = Read-KeyValueFile -Path $finalizePath
        Assert-Equal -Name 'failed Run cannot claim proof' `
            -Expected 'false' `
            -Actual $finalize['run_succeeded']
        Assert-Equal -Name 'failed evidence artifact file count' `
            -Expected '8' `
            -Actual $finalize['artifact_file_count']
        Assert-SupplyChainArtifacts `
            -ArtifactRoot $prepare.Output['artifact'] `
            -Revision $fixture.Revision `
            -ExpectedRunSucceeded $false
        & $baseState.Control `
            -Seal `
            -JobKind linux-quality `
            -BaseRoot $baseState.Base `
            -ManifestSha256 $prepare.Output['manifest_sha256'] `
            -ExpectedArtifactSetSha256 (
                $finalize['artifact_set_sha256']
            ) 6>$null
    }

    Invoke-Case 'failed permanent and cross Runs cannot be finalized' {
        $fixture = New-ControlFixture `
            -Container $suiteRoot `
            -Name 'failed cross run fixture'
        $baseState = New-ControlBase `
            -Fixture $fixture `
            -Name 'failed cross run base'
        $prepare = Invoke-ControlPrepare `
            -Fixture $fixture `
            -BaseState $baseState `
            -JobKind cross-build `
            -TargetGoos linux `
            -TargetGoarch amd64
        & $baseState.Control `
            -Initialize `
            -JobKind cross-build `
            -BaseRoot $baseState.Base `
            -TargetGoos linux `
            -TargetGoarch amd64 6>$null

        $tools = Join-Path $fixture.Root 'tools'
        [void][IO.Directory]::CreateDirectory($tools)
        Write-Utf8NoBom `
            -Path (Join-Path $tools 'go.cmd') `
            -Text "@echo off`r`nexit /b 9`r`n"
        $savedPath = $env:PATH
        try {
            $env:PATH = $tools + [IO.Path]::PathSeparator + $savedPath
            Assert-ThrowsPrefix `
                -Name 'cross Run failure' `
                -Prefix 'CI_' `
                -Body {
                    & $baseState.Control `
                        -Run `
                        -JobKind cross-build `
                        -BaseRoot $baseState.Base `
                        -TargetGoos linux `
                        -TargetGoarch amd64 6>$null
                }
        } finally {
            $env:PATH = $savedPath
        }
        Assert-ThrowsPrefix `
            -Name 'failed cross Finalize' `
            -Prefix (
                'CI_WORKFLOW_CONTROL_FAIL ' +
                'code=CIWC_RUN_NOT_SUCCESSFUL'
            ) `
            -Body {
                & $baseState.Control `
                    -Finalize `
                    -JobKind cross-build `
                    -BaseRoot $baseState.Base `
                    -TargetGoos linux `
                    -TargetGoarch amd64 `
                    -ManifestSha256 (
                        $prepare.Output['manifest_sha256']
                    ) `
                    -GitHubOutputPath (
                        Join-Path $fixture.Root 'cross-finalize.txt'
                    ) 6>$null
            }

        $permanentFixture = New-ControlFixture `
            -Container $suiteRoot `
            -Name 'failed permanent run fixture'
        $permanentBase = New-ControlBase `
            -Fixture $permanentFixture `
            -Name 'failed permanent run base'
        $permanentPrepare = Invoke-ControlPrepare `
            -Fixture $permanentFixture `
            -BaseState $permanentBase `
            -JobKind permanent
        & $permanentBase.Control `
            -Initialize `
            -JobKind permanent `
            -BaseRoot $permanentBase.Base 6>$null
        $permanentTools = Join-Path $permanentFixture.Root 'tools'
        [void][IO.Directory]::CreateDirectory($permanentTools)
        Write-Utf8NoBom `
            -Path (Join-Path $permanentTools 'go.cmd') `
            -Text "@echo off`r`nexit /b 9`r`n"
        try {
            $env:PATH =
                $permanentTools + [IO.Path]::PathSeparator + $savedPath
            Assert-ThrowsPrefix `
                -Name 'permanent Run failure' `
                -Prefix 'CI_' `
                -Body {
                    & $permanentBase.Control `
                        -Run `
                        -JobKind permanent `
                        -BaseRoot $permanentBase.Base 6>$null
                }
        } finally {
            $env:PATH = $savedPath
        }
        Assert-ThrowsPrefix `
            -Name 'failed permanent Finalize' `
            -Prefix (
                'CI_WORKFLOW_CONTROL_FAIL ' +
                'code=CIWC_RUN_NOT_SUCCESSFUL'
            ) `
            -Body {
                & $permanentBase.Control `
                    -Finalize `
                    -JobKind permanent `
                    -BaseRoot $permanentBase.Base `
                    -ManifestSha256 (
                        $permanentPrepare.Output['manifest_sha256']
                    ) `
                    -GitHubOutputPath (
                        Join-Path $permanentFixture.Root (
                            'permanent-finalize.txt'
                        )
                    ) 6>$null
            }
    }

    Invoke-Case 'wrong dependency pin fails before any Git tool execution' {
        $fixture = New-ControlFixture `
            -Container $suiteRoot `
            -Name 'bad dependency pin fixture'
        $source = [IO.File]::ReadAllText($script:Control, $script:Utf8NoBom)
        $runnerHash = Get-LowerSha256 -Path $script:CommandRunner
        $occurrences = [Regex]::Matches(
            $source,
            [Regex]::Escape($runnerHash)
        ).Count
        Assert-Equal -Name 'runner pin replacement count' `
            -Expected 1 `
            -Actual $occurrences
        $badControlSource = $source.Replace($runnerHash, ('0' * 64))
        $badControl = Join-Path $fixture.Root 'bad-workflow-control.ps1'
        Write-Utf8NoBom -Path $badControl -Text $badControlSource
        $baseState = New-ControlBase `
            -Fixture $fixture `
            -Name 'bad dependency pin base' `
            -ControlSource $badControl

        $tools = Join-Path $fixture.Root 'marker tools'
        [void][IO.Directory]::CreateDirectory($tools)
        $marker = Join-Path $fixture.Root 'git-tool-was-invoked.txt'
        $fakeGit = Join-Path $tools 'git.cmd'
        Write-Utf8NoBom -Path $fakeGit -Text (
            "@echo off`r`n" +
            ">`"$marker`" echo invoked`r`n" +
            "exit /b 97`r`n"
        )
        $savedPath = $env:PATH
        try {
            $env:PATH = $tools
            Assert-ThrowsPrefix `
                -Name 'wrong dependency pin' `
                -Prefix (
                    'CI_WORKFLOW_CONTROL_FAIL ' +
                    'code=CIWC_COMMAND_RUNNER_AUTH_FAILED'
                ) `
                -Body {
                    & $baseState.Control `
                        -Prepare `
                        -JobKind permanent `
                        -BaseRoot $baseState.Base `
                        -RepositoryRoot $fixture.Repository `
                        -Revision $fixture.Revision `
                        -GitHubOutputPath $baseState.Output `
                        -GitHubEnvironmentPath (
                            $baseState.Environment
                        ) 6>$null
                }
        } finally {
            $env:PATH = $savedPath
        }
        Assert-True -Name 'Git tool marker remains absent' -Condition (
            -not (Test-Path -LiteralPath $marker)
        )
        Assert-True -Name 'release work remains absent' -Condition (
            -not (
                Test-Path `
                    -LiteralPath (
                        Join-Path $baseState.Base 'trusted/work'
                    )
            )
        )
    }
} finally {
    $resolvedSuite = [IO.Path]::GetFullPath($suiteRoot)
    $resolvedParent = [IO.Path]::GetFullPath($testTempRoot).
        TrimEnd([char]92, [char]47) +
        [IO.Path]::DirectorySeparatorChar
    $comparison = if (
        [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
    ) {
        [StringComparison]::OrdinalIgnoreCase
    } else {
        [StringComparison]::Ordinal
    }
    if (-not $resolvedSuite.StartsWith($resolvedParent, $comparison)) {
        throw 'CI_WORKFLOW_CONTROL_SELFTEST_CLEANUP_BOUNDARY'
    }
    if (Test-Path -LiteralPath $resolvedSuite -PathType Container) {
        Remove-Item -LiteralPath $resolvedSuite -Recurse -Force
    }
}

if ($script:Failures.Count -gt 0) {
    $script:Failures | ForEach-Object {
        Write-Error $_ -ErrorAction Continue
    }
    throw (
        'CI_WORKFLOW_CONTROL_SELFTEST_FAIL ' +
        "cases=$($script:Cases) assertions=$($script:Assertions) " +
        "failures=$($script:Failures.Count)"
    )
}

Write-Host (
    'CI_WORKFLOW_CONTROL_SELFTEST_PASS ' +
    "cases=$($script:Cases) assertions=$($script:Assertions)"
)
