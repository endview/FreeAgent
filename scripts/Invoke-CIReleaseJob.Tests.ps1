[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:Controller = Join-Path $PSScriptRoot 'Invoke-CIReleaseJob.ps1'
$script:Bootstrap = Join-Path $PSScriptRoot 'New-CIReleaseWorkspace.ps1'
$script:Generator = Join-Path $PSScriptRoot 'New-PublicStaging.ps1'
$script:Verifier = Join-Path $PSScriptRoot 'Test-PublicStaging.ps1'
$script:Git = (@(
    Get-Command git -CommandType Application -ErrorAction Stop
))[0].Source
$script:Utf8NoBom = New-Object Text.UTF8Encoding($false)
$script:Utf8Strict = New-Object Text.UTF8Encoding($false, $true)
$script:Cases = 0
$script:Assertions = 0
$script:Failures = New-Object 'Collections.Generic.List[string]'

function Assert-True {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][bool]$Condition
    )
    $script:Assertions++
    if (-not $Condition) {
        throw "ASSERT_FAIL $Name"
    }
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

function Assert-ThrowsCode {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][scriptblock]$Body,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $observed = $null
    try {
        & $Body
    } catch {
        $observed = $_.Exception.Message
    }
    Assert-True -Name "$Name throws" -Condition (-not [string]::IsNullOrWhiteSpace($observed))
    Assert-True -Name "$Name exact code" -Condition (
        $observed.StartsWith(
            "CI_RELEASE_JOB_FAIL code=$Code",
            [StringComparison]::Ordinal
        )
    )
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

function Get-LowerSha256 {
    param([Parameter(Mandatory = $true)][string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).
        Hash.ToLowerInvariant()
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

function New-CIJFixture {
    param(
        [Parameter(Mandatory = $true)][string]$Container,
        [Parameter(Mandatory = $true)][string]$Name
    )
    $root = Join-Path $Container $Name
    $repository = Join-Path $root 'repository with space'
    $trusted = Join-Path $root 'trusted temp'
    [void][IO.Directory]::CreateDirectory($repository)
    [void][IO.Directory]::CreateDirectory($trusted)
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
    [void][IO.Directory]::CreateDirectory((Join-Path $repository 'scripts'))
    [IO.File]::Copy(
        $script:Generator,
        (Join-Path $repository 'scripts/New-PublicStaging.ps1'),
        $false
    )
    [IO.File]::Copy(
        $script:Verifier,
        (Join-Path $repository 'scripts/Test-PublicStaging.ps1'),
        $false
    )
    Write-Utf8NoBom -Path (Join-Path $repository 'README.md') -Text "# fixture`n"
    $revision = Commit-All -Repository $repository -Message 'fixture'
    return [pscustomobject]@{
        Root = $root
        Repository = $repository
        Trusted = $trusted
        Work = Join-Path $trusted 'release work'
        ExecutionTemp = Join-Path $trusted 'execution temp'
        Revision = $revision
    }
}

function Invoke-CIJPrepare {
    param(
        [Parameter(Mandatory = $true)]$Fixture,
        [string]$BootstrapPath = $script:Bootstrap,
        [string]$ExpectedBootstrapSha256 = '',
        [string]$WorkRoot = '',
        [string]$ExecutionTempRoot = ''
    )
    if ([string]::IsNullOrWhiteSpace($ExpectedBootstrapSha256)) {
        $ExpectedBootstrapSha256 = Get-LowerSha256 -Path $BootstrapPath
    }
    if ([string]::IsNullOrWhiteSpace($WorkRoot)) {
        $WorkRoot = $Fixture.Work
    }
    if ([string]::IsNullOrWhiteSpace($ExecutionTempRoot)) {
        $ExecutionTempRoot = $Fixture.ExecutionTemp
    }
    $parameters = [ordered]@{
        Prepare = $true
        RepositoryRoot = $Fixture.Repository
        Revision = $Fixture.Revision
        WorkRoot = $WorkRoot
        ExecutionTempRoot = $ExecutionTempRoot
        BootstrapPath = $BootstrapPath
        ExpectedBootstrapSha256 = $ExpectedBootstrapSha256
        ExpectedGeneratorSha256 = Get-LowerSha256 -Path $script:Generator
        ExpectedVerifierSha256 = Get-LowerSha256 -Path $script:Verifier
        TrustedTemp = $Fixture.Trusted
    }
    $objects = @(& $script:Controller @parameters 6>$null)
    $states = @($objects | Where-Object {
        $null -ne $_.PSObject.Properties['ManifestSha256'] -and
        $null -ne $_.PSObject.Properties['InitialArtifactSetSha256']
    })
    if ($states.Count -ne 1) {
        throw "ASSERT_FAIL prepare returned states=$($states.Count)"
    }
    return $states[0]
}

function Invoke-CIJVerify {
    param(
        [Parameter(Mandatory = $true)]$State,
        [Parameter(Mandatory = $true)][string[]]$Required,
        [string[]]$Optional = @(),
        [string]$ExpectedArtifactSetSha256 = '',
        [string]$SourceRoot = '',
        [string]$StageRoot = '',
        [string]$ArtifactRoot = '',
        [string]$ManifestPath = '',
        [string]$TrustedTemp = ''
    )
    if ([string]::IsNullOrWhiteSpace($SourceRoot)) { $SourceRoot = $State.SourceRoot }
    if ([string]::IsNullOrWhiteSpace($StageRoot)) { $StageRoot = $State.StageRoot }
    if ([string]::IsNullOrWhiteSpace($ArtifactRoot)) { $ArtifactRoot = $State.ArtifactRoot }
    if ([string]::IsNullOrWhiteSpace($ManifestPath)) { $ManifestPath = $State.ManifestPath }
    if ([string]::IsNullOrWhiteSpace($TrustedTemp)) { $TrustedTemp = $State.ExecutionTempRoot }
    $parameters = [ordered]@{
        Verify = $true
        SourceRoot = $SourceRoot
        StageRoot = $StageRoot
        ArtifactRoot = $ArtifactRoot
        ManifestPath = $ManifestPath
        ManifestSha256 = $State.ManifestSha256
        ExpectedVerifierSha256 = Get-LowerSha256 -Path $script:Verifier
        TrustedTemp = $TrustedTemp
        RequiredArtifactRelativePath = $Required
        OptionalArtifactRelativePath = $Optional
    }
    if (-not [string]::IsNullOrWhiteSpace($ExpectedArtifactSetSha256)) {
        $parameters.ExpectedArtifactSetSha256 = $ExpectedArtifactSetSha256
    }
    $objects = @(& $script:Controller @parameters 6>$null)
    $states = @($objects | Where-Object {
        $null -ne $_.PSObject.Properties['ArtifactSetSha256']
    })
    if ($states.Count -ne 1) {
        throw "ASSERT_FAIL verify returned states=$($states.Count)"
    }
    return $states[0]
}

function Get-CIJStringSha256Base64 {
    param([Parameter(Mandatory = $true)][string]$Value)
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        return 'h1:' + [Convert]::ToBase64String(
            $sha.ComputeHash($script:Utf8NoBom.GetBytes($Value))
        )
    } finally {
        $sha.Dispose()
    }
}

function Get-CIJStringSha512Base64 {
    param([Parameter(Mandatory = $true)][string]$Value)
    $sha = [Security.Cryptography.SHA512]::Create()
    try {
        return [Convert]::ToBase64String(
            $sha.ComputeHash($script:Utf8NoBom.GetBytes($Value))
        )
    } finally {
        $sha.Dispose()
    }
}

function Write-CIJJsonFixture {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)]$Value
    )
    $text = ($Value | ConvertTo-Json -Depth 32).
        Replace("`r`n", "`n").
        Replace("`r", "`n") + "`n"
    Write-Utf8NoBom -Path $Path -Text $text
}

function Read-CIJStrictJson {
    param([Parameter(Mandatory = $true)][string]$Path)
    [byte[]]$bytes = [IO.File]::ReadAllBytes($Path)
    $text = $script:Utf8Strict.GetString($bytes)
    return $text | ConvertFrom-Json
}

function Assert-CIJCanonicalText {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Path,
        [switch]$Json
    )
    [byte[]]$bytes = [IO.File]::ReadAllBytes($Path)
    Assert-True -Name "$Name is nonempty" -Condition ($bytes.Length -gt 0)
    Assert-True -Name "$Name has no UTF-8 BOM" -Condition (
        $bytes.Length -lt 3 -or
        -not (
            $bytes[0] -eq 0xEF -and
            $bytes[1] -eq 0xBB -and
            $bytes[2] -eq 0xBF
        )
    )
    $text = $script:Utf8Strict.GetString($bytes)
    Assert-True -Name "$Name contains LF" -Condition ($text.Contains("`n"))
    Assert-True -Name "$Name contains no CR" -Condition (-not $text.Contains("`r"))
    Assert-True -Name "$Name ends with one LF" -Condition (
        $text.EndsWith("`n", [StringComparison]::Ordinal) -and
        -not $text.EndsWith("`n`n", [StringComparison]::Ordinal)
    )
    Assert-True -Name "$Name contains no NUL" -Condition (
        $text.IndexOf([char]0) -lt 0
    )
    if ($Json) {
        $parsed = $text | ConvertFrom-Json
        Assert-True -Name "$Name parses as JSON" -Condition ($null -ne $parsed)
    }
}

function New-CIJSupplyChainFixture {
    param(
        [Parameter(Mandatory = $true)][string]$Container,
        [Parameter(Mandatory = $true)][string]$Name,
        [bool]$RunSucceeded = $true,
        [string]$JobKind = 'cross-build',
        [string]$TargetGoos = 'linux',
        [string]$TargetGoarch = 'amd64'
    )
    $root = Join-Path $Container $Name
    $source = Join-Path $root 'source with space'
    $artifact = Join-Path $root 'artifact with space'
    $moduleCache = Join-Path $root 'module cache'
    $trusted = Join-Path $root 'trusted temp'
    foreach ($directory in @($source, $artifact, $moduleCache, $trusted)) {
        [void][IO.Directory]::CreateDirectory($directory)
    }

    $revision = '0123456789abcdef0123456789abcdef01234567'
    $createdUtc = '2026-07-24T01:02:03Z'
    $modulePath = 'example.com/dependency'
    $moduleVersion = 'v1.2.3'
    $moduleKey = "$modulePath@$moduleVersion"
    $moduleSum = Get-CIJStringSha256Base64 -Value 'fixture module tree'
    $goModSum = Get-CIJStringSha256Base64 -Value 'fixture dependency go.mod'
    $licenseText = "MIT License fixture.`n"
    $specialLicenseText = "Special fixture permission text.`n"
    $moduleDirectory = Join-Path $moduleCache $moduleKey
    [void][IO.Directory]::CreateDirectory($moduleDirectory)
    $licensePath = Join-Path $moduleDirectory 'LICENSE'
    $specialLicensePath = Join-Path $moduleDirectory 'SPECIAL-LICENSE.txt'
    Write-Utf8NoBom -Path $licensePath -Text $licenseText
    Write-Utf8NoBom -Path $specialLicensePath -Text $specialLicenseText

    $assetRelativePath = 'assets/banner.txt'
    $assetPath = Join-Path $source (
        $assetRelativePath.Replace([char]47, [IO.Path]::DirectorySeparatorChar)
    )
    $assetText = "asset one`n"
    Write-Utf8NoBom -Path $assetPath -Text $assetText

    $reactName = 'react'
    $reactVersion = '19.2.8'
    $reactIdentity = "$reactName@$reactVersion"
    $reactResolved =
        'https://registry.npmjs.org/react/-/react-19.2.8.tgz'
    $reactIntegrity = 'sha512-' +
        (Get-CIJStringSha512Base64 -Value 'fixture react tarball')
    $viteName = 'vite'
    $viteVersion = '7.3.6'
    $viteIdentity = "$viteName@$viteVersion"
    $viteResolved =
        'https://registry.npmjs.org/vite/-/vite-7.3.6.tgz'
    $viteIntegrity = 'sha512-' +
        (Get-CIJStringSha512Base64 -Value 'fixture vite tarball')
    $reactLicenseRelative = 'third_party/npm/react/19.2.8/LICENSE'
    $viteLicenseRelative = 'third_party/npm/vite/7.3.6/LICENSE'
    $reactLicensePath = Join-Path $source (
        $reactLicenseRelative.Replace([char]47, [IO.Path]::DirectorySeparatorChar)
    )
    $viteLicensePath = Join-Path $source (
        $viteLicenseRelative.Replace([char]47, [IO.Path]::DirectorySeparatorChar)
    )
    $reactLicenseText = "React MIT fixture license.`n"
    $viteLicenseText = "Vite MIT fixture license.`n"
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
    $chunkRelativePath = 'internal/controlweb/dist/assets/vendor.js'
    $chunkPath = Join-Path $source (
        $chunkRelativePath.Replace([char]47, [IO.Path]::DirectorySeparatorChar)
    )
    $chunkText = "/* fixture react runtime */`n"
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
    $packageLockPath = Join-Path $source 'internal/controlweb/package-lock.json'
    Write-CIJJsonFixture -Path $packageLockPath -Value $packageLock
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
                name = $reactName
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
                name = $viteName
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
                path = $chunkRelativePath
                sha256 = Get-LowerSha256 -Path $chunkPath
                packages = @($reactIdentity)
            }
        )
    }
    $frontendManifestPath = Join-Path (
        Join-Path $source 'testdata/release'
    ) 'frontend-dependency-licenses.v1.json'
    Write-CIJJsonFixture -Path $frontendManifestPath -Value $frontendManifest

    $dependencyManifest = [ordered]@{
        schema_version = 1
        kind = 'freeagent-go-dependency-licenses'
        main_module = 'github.com/endview/freeagent'
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
        license_refs = @(
            [ordered]@{
                id = 'LicenseRef-example-special'
                name = 'Example special fixture license'
                source_url = 'https://example.invalid/dependency/SPECIAL-LICENSE.txt'
                text_sha256 = Get-LowerSha256 -Path $specialLicensePath
                applies_to = $moduleKey
                required_file = 'SPECIAL-LICENSE.txt'
            }
        )
        modules = @(
            [ordered]@{
                path = $modulePath
                version = $moduleVersion
                module_sum = $moduleSum
                go_mod_sum = $goModSum
                declared_license_expression =
                    'LicenseRef-example-special AND MIT'
                source_url = 'https://example.invalid/dependency'
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
                        size = $script:Utf8NoBom.GetByteCount($licenseText)
                        sha256 = Get-LowerSha256 -Path $licensePath
                    },
                    [ordered]@{
                        path = 'SPECIAL-LICENSE.txt'
                        role = 'license'
                        size = $script:Utf8NoBom.GetByteCount($specialLicenseText)
                        sha256 = Get-LowerSha256 -Path $specialLicensePath
                    }
                )
            }
        )
    }
    $dependencyManifestPath = Join-Path (
        Join-Path $source 'testdata/release'
    ) 'dependency-licenses.v1.json'
    Write-CIJJsonFixture `
        -Path $dependencyManifestPath `
        -Value $dependencyManifest

    $assetManifest = [ordered]@{
        schema_version = 1
        kind = 'freeagent-distributed-assets'
        assets = @(
            [ordered]@{
                path = $assetRelativePath
                size = $script:Utf8NoBom.GetByteCount($assetText)
                sha256 = Get-LowerSha256 -Path $assetPath
                origin = 'project-owned'
                source_url = 'project://github.com/endview/freeagent'
                spdx_expression = 'AGPL-3.0-only'
                notice_id = ''
                required_files = @()
            },
            [ordered]@{
                path = $chunkRelativePath
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
    $assetManifestPath = Join-Path (
        Join-Path $source 'testdata/release'
    ) 'distributed-assets.v1.json'
    Write-CIJJsonFixture -Path $assetManifestPath -Value $assetManifest

    $goModPath = Join-Path $source 'go.mod'
    $goSumPath = Join-Path $source 'go.sum'
    Write-Utf8NoBom -Path $goModPath -Text (
        "module github.com/endview/freeagent`n`n" +
        "go 1.26.5`n`n" +
        "require $modulePath $moduleVersion`n"
    )
    Write-Utf8NoBom -Path $goSumPath -Text (
        "$modulePath $moduleVersion $moduleSum`n" +
        "$modulePath $moduleVersion/go.mod $goModSum`n"
    )

    $manifestRelative = 'public-tree-manifest.v1.json'
    $evidenceRelative = 'evidence/result.json'
    $optionalRelative = 'logs/build.log'
    Write-Utf8NoBom `
        -Path (Join-Path $artifact $manifestRelative) `
        -Text (
            '{"kind":"fixture-public-tree","revision":"' +
            $revision +
            '"}' +
            "`n"
        )
    Write-Utf8NoBom `
        -Path (Join-Path $artifact (
            $evidenceRelative.Replace([char]47, [IO.Path]::DirectorySeparatorChar)
        )) `
        -Text "{`"status`":`"passed`"}`n"
    Write-Utf8NoBom `
        -Path (Join-Path $artifact (
            $optionalRelative.Replace([char]47, [IO.Path]::DirectorySeparatorChar)
        )) `
        -Text "build ok`n"

    return [pscustomobject]@{
        Root = $root
        SourceRoot = $source
        ArtifactRoot = $artifact
        ModuleCacheRoot = $moduleCache
        TrustedTemp = $trusted
        Revision = $revision
        CreatedUtc = $createdUtc
        JobKind = $JobKind
        RunSucceeded = $RunSucceeded
        TargetGoos = $TargetGoos
        TargetGoarch = $TargetGoarch
        Required = @($manifestRelative, $evidenceRelative)
        Optional = @($optionalRelative)
        DependencyManifestPath = $dependencyManifestPath
        AssetManifestPath = $assetManifestPath
        FrontendManifestPath = $frontendManifestPath
        PackageLockPath = $packageLockPath
        AssetPath = $assetPath
        ChunkPath = $chunkPath
        ReactLicensePath = $reactLicensePath
        ViteLicensePath = $viteLicensePath
        ReactIdentity = $reactIdentity
        ViteIdentity = $viteIdentity
        GoModPath = $goModPath
        GoSumPath = $goSumPath
        ModuleDirectory = $moduleDirectory
        LicensePath = $licensePath
        SpecialLicensePath = $specialLicensePath
        ModulePath = $modulePath
        ModuleVersion = $moduleVersion
        ModuleSum = $moduleSum
        GoModSum = $goModSum
        SupplyChainRoot = Join-Path $artifact 'supply-chain'
    }
}

function Invoke-CIJGenerateSupplyChain {
    param(
        [Parameter(Mandatory = $true)]$Fixture,
        [string[]]$Required = $Fixture.Required,
        [string[]]$Optional = $Fixture.Optional,
        [string]$MetadataGeneratorSha256 = '',
        [string]$TargetGoos = $Fixture.TargetGoos,
        [string]$TargetGoarch = $Fixture.TargetGoarch
    )
    if ([string]::IsNullOrWhiteSpace($MetadataGeneratorSha256)) {
        $MetadataGeneratorSha256 = Get-LowerSha256 -Path $script:Controller
    }
    $parameters = [ordered]@{
        GenerateSupplyChain = $true
        SourceRoot = $Fixture.SourceRoot
        ArtifactRoot = $Fixture.ArtifactRoot
        ModuleCacheRoot = $Fixture.ModuleCacheRoot
        TrustedTemp = $Fixture.TrustedTemp
        Revision = $Fixture.Revision
        CreatedUtc = $Fixture.CreatedUtc
        JobKind = $Fixture.JobKind
        RunSucceeded = $Fixture.RunSucceeded
        MetadataGeneratorSha256 = $MetadataGeneratorSha256
        RequiredArtifactRelativePath = $Required
        OptionalArtifactRelativePath = $Optional
    }
    if (-not [string]::IsNullOrWhiteSpace($TargetGoos)) {
        $parameters.TargetGoos = $TargetGoos
    }
    if (-not [string]::IsNullOrWhiteSpace($TargetGoarch)) {
        $parameters.TargetGoarch = $TargetGoarch
    }
    return @(& $script:Controller @parameters 6>$null)
}

function New-CIJSupplyChainVerifyFixture {
    param(
        [Parameter(Mandatory = $true)][string]$Container,
        [Parameter(Mandatory = $true)][string]$Name
    )
    $releaseFixture = New-CIJFixture `
        -Container $Container `
        -Name "$Name-release"
    $verifyState = Invoke-CIJPrepare -Fixture $releaseFixture
    $metadataFixture = New-CIJSupplyChainFixture `
        -Container $Container `
        -Name "$Name-metadata"

    foreach ($relative in @('evidence/result.json', 'logs/build.log')) {
        $portable = $relative.Replace(
            [char]47,
            [IO.Path]::DirectorySeparatorChar
        )
        $sourcePath = Join-Path $metadataFixture.ArtifactRoot $portable
        $destinationPath = Join-Path $verifyState.ArtifactRoot $portable
        [void][IO.Directory]::CreateDirectory(
            [IO.Path]::GetDirectoryName($destinationPath)
        )
        [IO.File]::Copy($sourcePath, $destinationPath, $false)
    }

    $generateFixture = [pscustomobject]@{
        Root = $releaseFixture.Root
        SourceRoot = $metadataFixture.SourceRoot
        ArtifactRoot = $verifyState.ArtifactRoot
        ModuleCacheRoot = $metadataFixture.ModuleCacheRoot
        TrustedTemp = $verifyState.ExecutionTempRoot
        Revision = $verifyState.Revision
        CreatedUtc = $metadataFixture.CreatedUtc
        JobKind = $metadataFixture.JobKind
        RunSucceeded = $metadataFixture.RunSucceeded
        TargetGoos = $metadataFixture.TargetGoos
        TargetGoarch = $metadataFixture.TargetGoarch
        Required = @(
            'public-tree-manifest.v1.json',
            'evidence/result.json'
        )
        Optional = @('logs/build.log')
        SupplyChainRoot = Join-Path $verifyState.ArtifactRoot 'supply-chain'
    }
    return [pscustomobject]@{
        GenerateFixture = $generateFixture
        VerifyState = $verifyState
        ExtendedRequired = @(
            @($generateFixture.Required) +
            @(
                'supply-chain/sbom.spdx.json',
                'supply-chain/provenance.unsigned.v1.json',
                'supply-chain/checksums.sha256'
            )
        )
    }
}

function Get-CIJSupplyChainOutputHash {
    param([Parameter(Mandatory = $true)]$Fixture)
    $hashes = [ordered]@{}
    foreach ($relative in @(
        'supply-chain/sbom.spdx.json',
        'supply-chain/provenance.unsigned.v1.json',
        'supply-chain/checksums.sha256'
    )) {
        $path = Join-Path $Fixture.ArtifactRoot (
            $relative.Replace([char]47, [IO.Path]::DirectorySeparatorChar)
        )
        $hashes[$relative] = Get-LowerSha256 -Path $path
    }
    return $hashes
}

function Assert-CIJSupplyChainFailureIsAtomic {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)]$Fixture,
        [Parameter(Mandatory = $true)][scriptblock]$Body,
        [string]$Code = 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    )
    Assert-ThrowsCode -Name $Name -Body $Body -Code $Code
    Assert-True -Name "$Name publishes no supply-chain directory" -Condition (
        -not (Test-Path -LiteralPath $Fixture.SupplyChainRoot)
    )
    Assert-Equal -Name "$Name leaves trusted temp empty" -Expected 0 -Actual (
        @(Get-ChildItem -LiteralPath $Fixture.TrustedTemp -Force).Count
    )
}

foreach ($requiredPath in @(
    $script:Controller,
    $script:Bootstrap,
    $script:Generator,
    $script:Verifier
)) {
    if (-not (Test-Path -LiteralPath $requiredPath -PathType Leaf)) {
        throw "CI_RELEASE_JOB_SELFTEST_DEPENDENCY_MISSING path=$requiredPath"
    }
}

$testTempRoot = if (-not [string]::IsNullOrWhiteSpace(
    $env:FREEAGENT_CI_JOB_TEST_TEMP_ROOT
)) {
    if (-not [IO.Path]::IsPathRooted($env:FREEAGENT_CI_JOB_TEST_TEMP_ROOT) -or
        -not (Test-Path -LiteralPath $env:FREEAGENT_CI_JOB_TEST_TEMP_ROOT -PathType Container)) {
        throw 'FREEAGENT_CI_JOB_TEST_TEMP_ROOT must be an existing absolute directory.'
    }
    [IO.Path]::GetFullPath($env:FREEAGENT_CI_JOB_TEST_TEMP_ROOT)
} else {
    [IO.Path]::GetTempPath()
}
$suiteRoot = Join-Path $testTempRoot (
    'freeagent-ci-release-job-tests-' + [Guid]::NewGuid().ToString('N')
)
[void][IO.Directory]::CreateDirectory($suiteRoot)

try {
    Invoke-Case 'controller parses and portable artifact paths fail closed' {
        $tokens = $null
        $parseErrors = $null
        $ast = [Management.Automation.Language.Parser]::ParseFile(
            $script:Controller,
            [ref]$tokens,
            [ref]$parseErrors
        )
        Assert-Equal -Name 'controller parse errors' -Expected 0 -Actual @($parseErrors).Count
        $bytes = [IO.File]::ReadAllBytes($script:Controller)
        [void]$script:Utf8Strict.GetString($bytes)
        Assert-Equal -Name 'controller has ASCII-only source' -Expected 0 -Actual (
            @($bytes | Where-Object { $_ -gt 127 }).Count
        )
        foreach ($functionName in @(
            'Fail-CIReleaseJob',
            'Assert-CIJPortableRelativePath'
        )) {
            $functionAst = @($ast.FindAll({
                param($node)
                $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
                    $node.Name -ceq $functionName
            }, $true))
            Assert-Equal -Name "$functionName occurs once" -Expected 1 -Actual $functionAst.Count
            . ([scriptblock]::Create($functionAst[0].Extent.Text))
        }
        $script:Utf8NoBom = New-Object Text.UTF8Encoding($false)
        foreach ($invalidPath in @(
            '../escape',
            ('C:' + '/drive'),
            'back\slash',
            'CON.txt',
            ('COM' + [char]0x00B9),
            '.git/config',
            'evidence/name:stream',
            'evidence/*.json',
            ('format' + [char]0x200B + '.txt'),
            ('e' + [char]0x0301 + '.txt'),
            ('a' * 256)
        )) {
            Assert-ThrowsCode `
                -Name "portable path rejects $invalidPath" `
                -Code 'CIJ_ARTIFACT_ALLOWLIST_INVALID' `
                -Body { Assert-CIJPortableRelativePath -RelativePath $invalidPath }
        }
        Assert-CIJPortableRelativePath -RelativePath 'evidence/valid file.stdout.jsonl'
        $script:Assertions++
    }

    Invoke-Case 'prepare preflight and authenticated syntax reject unsafe inputs' {
        $fixture = New-CIJFixture -Container $suiteRoot -Name 'preflight'
        $outside = Join-Path $fixture.Root 'outside work'
        Assert-ThrowsCode `
            -Name 'work outside trusted' `
            -Code 'CIJ_WORK_ROOT_INVALID' `
            -Body { [void](Invoke-CIJPrepare -Fixture $fixture -WorkRoot $outside) }
        Assert-True -Name 'unsafe preflight did not create work' -Condition (
            -not (Test-Path -LiteralPath $fixture.Work)
        )
        $overlapExecution = Join-Path $fixture.Work 'execution'
        Assert-ThrowsCode `
            -Name 'execution nested in work' `
            -Code 'CIJ_ROOTS_OVERLAP' `
            -Body {
                [void](Invoke-CIJPrepare `
                    -Fixture $fixture `
                    -ExecutionTempRoot $overlapExecution)
            }
        $invalidBootstrap = Join-Path $fixture.Root 'invalid-bootstrap.ps1'
        Write-Utf8NoBom -Path $invalidBootstrap -Text "param(`n"
        Assert-ThrowsCode `
            -Name 'authenticated syntax error' `
            -Code 'CIJ_BOOTSTRAP_AUTH_FAILED' `
            -Body {
                [void](Invoke-CIJPrepare `
                    -Fixture $fixture `
                    -BootstrapPath $invalidBootstrap)
            }
    }

    Invoke-Case 'real prepare and verify produce a stable initial artifact seal' {
        $script:HappyFixture = New-CIJFixture -Container $suiteRoot -Name 'happy'
        $script:HappyState = Invoke-CIJPrepare -Fixture $script:HappyFixture
        Assert-Equal -Name 'prepare revision' `
            -Expected $script:HappyFixture.Revision `
            -Actual $script:HappyState.Revision
        Assert-True -Name 'manifest pin format' -Condition (
            $script:HappyState.ManifestSha256 -cmatch '^[0-9a-f]{64}$'
        )
        Assert-True -Name 'initial artifact seal format' -Condition (
            $script:HappyState.InitialArtifactSetSha256 -cmatch '^[0-9a-f]{64}$'
        )
        $artifactEntries = @(Get-ChildItem -LiteralPath $script:HappyState.ArtifactRoot -Force)
        Assert-Equal -Name 'artifact initially owns one file' -Expected 1 -Actual $artifactEntries.Count
        Assert-Equal -Name 'artifact initial file' `
            -Expected 'public-tree-manifest.v1.json' `
            -Actual $artifactEntries[0].Name
        [string[]]$cacheNames = @(Get-ChildItem `
            -LiteralPath $script:HappyState.CacheRoot `
            -Force | ForEach-Object { $_.Name })
        [Array]::Sort($cacheNames, [StringComparer]::Ordinal)
        Assert-Equal -Name 'cache exact layout' `
            -Expected "build`ngo-tmp`ngopath`nmodule" `
            -Actual ($cacheNames -join "`n")
        foreach ($cacheDirectory in Get-ChildItem -LiteralPath $script:HappyState.CacheRoot -Force) {
            Assert-Equal -Name "cache empty $($cacheDirectory.Name)" -Expected 0 -Actual (
                @(Get-ChildItem -LiteralPath $cacheDirectory.FullName -Force).Count
            )
        }
        $verified = Invoke-CIJVerify `
            -State $script:HappyState `
            -Required @('public-tree-manifest.v1.json')
        Assert-Equal -Name 'initial seal recomputes identically' `
            -Expected $script:HappyState.InitialArtifactSetSha256 `
            -Actual $verified.ArtifactSetSha256
        $sealed = Invoke-CIJVerify `
            -State $script:HappyState `
            -Required @('public-tree-manifest.v1.json') `
            -ExpectedArtifactSetSha256 $verified.ArtifactSetSha256
        Assert-Equal -Name 'expected seal passes' `
            -Expected $verified.ArtifactSetSha256 `
            -Actual $sealed.ArtifactSetSha256
    }

    Invoke-Case 'verify binds sibling roots and the fixed manifest location' {
        Assert-ThrowsCode `
            -Name 'source and stage cannot alias' `
            -Code 'CIJ_ROOTS_OVERLAP' `
            -Body {
                [void](Invoke-CIJVerify `
                    -State $script:HappyState `
                    -Required @('public-tree-manifest.v1.json') `
                    -SourceRoot $script:HappyState.StageRoot)
            }
        $externalManifest = Join-Path $script:HappyFixture.Root 'manifest.json'
        [IO.File]::Copy($script:HappyState.ManifestPath, $externalManifest, $false)
        Assert-ThrowsCode `
            -Name 'manifest cannot be external' `
            -Code 'CIJ_MANIFEST_INVALID' `
            -Body {
                [void](Invoke-CIJVerify `
                    -State $script:HappyState `
                    -Required @('public-tree-manifest.v1.json') `
                    -ManifestPath $externalManifest)
            }
        Assert-ThrowsCode `
            -Name 'trusted temp cannot alias stage' `
            -Code 'CIJ_ROOTS_OVERLAP' `
            -Body {
                [void](Invoke-CIJVerify `
                    -State $script:HappyState `
                    -Required @('public-tree-manifest.v1.json') `
                    -TrustedTemp $script:HappyState.StageRoot)
            }
    }

    Invoke-Case 'artifact exact closure and content seal detect same-size replacement' {
        $evidenceDirectory = Join-Path $script:HappyState.ArtifactRoot 'evidence'
        [void][IO.Directory]::CreateDirectory($evidenceDirectory)
        $evidencePath = Join-Path $evidenceDirectory 'go-test.stdout.jsonl'
        Write-Utf8NoBom -Path $evidencePath -Text 'alpha'
        $required = @(
            'public-tree-manifest.v1.json',
            'evidence/go-test.stdout.jsonl'
        )
        $first = Invoke-CIJVerify -State $script:HappyState -Required $required
        Write-Utf8NoBom -Path $evidencePath -Text 'bravo'
        Assert-ThrowsCode `
            -Name 'same-size artifact replacement' `
            -Code 'CIJ_ARTIFACT_SET_PIN_MISMATCH' `
            -Body {
                [void](Invoke-CIJVerify `
                    -State $script:HappyState `
                    -Required $required `
                    -ExpectedArtifactSetSha256 $first.ArtifactSetSha256)
            }
        $second = Invoke-CIJVerify -State $script:HappyState -Required $required
        Assert-True -Name 'replacement changes seal' -Condition (
            $first.ArtifactSetSha256 -cne $second.ArtifactSetSha256
        )
        $unexpected = Join-Path $script:HappyState.ArtifactRoot 'unexpected.txt'
        Write-Utf8NoBom -Path $unexpected -Text 'unexpected'
        Assert-ThrowsCode `
            -Name 'unexpected file' `
            -Code 'CIJ_ARTIFACT_LAYOUT_INVALID' `
            -Body {
                [void](Invoke-CIJVerify -State $script:HappyState -Required $required)
            }
        [IO.File]::Delete($unexpected)
        $emptyDirectory = Join-Path $script:HappyState.ArtifactRoot 'empty'
        [void][IO.Directory]::CreateDirectory($emptyDirectory)
        Assert-ThrowsCode `
            -Name 'extra empty directory' `
            -Code 'CIJ_ARTIFACT_LAYOUT_INVALID' `
            -Body {
                [void](Invoke-CIJVerify -State $script:HappyState -Required $required)
            }
        [IO.Directory]::Delete($emptyDirectory)
    }

    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        Invoke-Case 'artifact ADS on files and directories fail closed' {
            $fixture = New-CIJFixture -Container $suiteRoot -Name 'ads'
            $state = Invoke-CIJPrepare -Fixture $fixture
            $evidenceDirectory = Join-Path $state.ArtifactRoot 'evidence'
            [void][IO.Directory]::CreateDirectory($evidenceDirectory)
            $file = Join-Path $evidenceDirectory 'out.log'
            Write-Utf8NoBom -Path $file -Text 'output'
            $required = @('public-tree-manifest.v1.json', 'evidence/out.log')
            [void](Invoke-CIJVerify -State $state -Required $required)

            Set-Content `
                -LiteralPath ($file + ':hidden') `
                -Value 'hidden' `
                -NoNewline `
                -Confirm:$false
            Assert-ThrowsCode `
                -Name 'file ADS' `
                -Code 'CIJ_ARTIFACT_LAYOUT_INVALID' `
                -Body { [void](Invoke-CIJVerify -State $state -Required $required) }
            [IO.File]::Delete($file)
            Write-Utf8NoBom -Path $file -Text 'output'

            Set-Content `
                -LiteralPath ($evidenceDirectory + ':hidden') `
                -Value 'hidden' `
                -NoNewline `
                -Confirm:$false
            Assert-ThrowsCode `
                -Name 'directory ADS' `
                -Code 'CIJ_ARTIFACT_LAYOUT_INVALID' `
                -Body { [void](Invoke-CIJVerify -State $state -Required $required) }
            $temporaryFile = Join-Path $fixture.Root 'out.log'
            [IO.File]::Move($file, $temporaryFile)
            [IO.Directory]::Delete($evidenceDirectory)
            [void][IO.Directory]::CreateDirectory($evidenceDirectory)
            [IO.File]::Move($temporaryFile, $file)

            Set-Content `
                -LiteralPath ($state.ArtifactRoot + ':hidden') `
                -Value 'hidden' `
                -NoNewline `
                -Confirm:$false
            Assert-ThrowsCode `
                -Name 'artifact root ADS' `
                -Code 'CIJ_ARTIFACT_LAYOUT_INVALID' `
                -Body { [void](Invoke-CIJVerify -State $state -Required $required) }
        }
    }

    Invoke-Case 'fixed commit staging tool pin cannot be hidden by dirty checkout bytes' {
        $fixture = New-CIJFixture -Container $suiteRoot -Name 'commit-pin'
        $repositoryGenerator = Join-Path $fixture.Repository 'scripts/New-PublicStaging.ps1'
        $changed = [IO.File]::ReadAllText($repositoryGenerator, $script:Utf8Strict) +
            "`n# committed replacement`n"
        Write-Utf8NoBom -Path $repositoryGenerator -Text $changed
        $fixture.Revision = Commit-All `
            -Repository $fixture.Repository `
            -Message 'changed generator'
        [IO.File]::Copy($script:Generator, $repositoryGenerator, $true)
        Assert-ThrowsCode `
            -Name 'Git tree generator pin mismatch' `
            -Code 'CIJ_BOOTSTRAP_FAILED' `
            -Body { [void](Invoke-CIJPrepare -Fixture $fixture) }
        Assert-True -Name 'mismatched generator never created stage' -Condition (
            -not (Test-Path -LiteralPath (Join-Path $fixture.Work 'stage'))
        )
        Assert-True -Name 'mismatched generator never created execution temp' -Condition (
            -not (Test-Path -LiteralPath $fixture.ExecutionTemp)
        )
    }

    Invoke-Case 'authenticated bootstrap child receives a scrubbed environment' {
        $fixture = New-CIJFixture -Container $suiteRoot -Name 'child-env'
        $probe = Join-Path $fixture.Root 'environment-probe.ps1'
        Write-Utf8NoBom -Path $probe -Text @'
param(
    [string]$RepositoryRoot,
    [string]$Revision,
    [string]$WorkRoot,
    [string]$ExpectedGeneratorSha256,
    [string]$ExpectedVerifierSha256
)
$names = @(
    'GITHUB_TOKEN',
    'ACTIONS_RUNTIME_TOKEN',
    'RUNNER_NAME',
    'CI',
    'FREEAGENT_API_KEY_SENTINEL',
    'FREEAGENT_SAFE_SENTINEL'
)
$lines = @(
    "CWD=$((Get-Location).Path)",
    "TEMP=$env:TEMP",
    "TMP=$env:TMP",
    "TMPDIR=$env:TMPDIR"
)
foreach ($name in $names) {
    $lines += "$name=$([Environment]::GetEnvironmentVariable($name, 'Process'))"
}
[IO.File]::WriteAllLines(
    (Join-Path (Get-Location).Path 'child-environment.txt'),
    $lines,
    (New-Object Text.UTF8Encoding($false))
)
throw 'EXPECTED_PROBE_STOP'
'@
        $names = @(
            'GITHUB_TOKEN',
            'ACTIONS_RUNTIME_TOKEN',
            'RUNNER_NAME',
            'CI',
            'FREEAGENT_API_KEY_SENTINEL',
            'FREEAGENT_SAFE_SENTINEL'
        )
        $saved = [ordered]@{}
        try {
            foreach ($name in $names) {
                $saved[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
            }
            foreach ($name in $names) {
                $value = if ($name -ceq 'FREEAGENT_SAFE_SENTINEL') {
                    'keep'
                } else {
                    'remove'
                }
                [Environment]::SetEnvironmentVariable(
                    $name,
                    $value,
                    'Process'
                )
            }
            Assert-ThrowsCode `
                -Name 'environment probe stops bootstrap' `
                -Code 'CIJ_BOOTSTRAP_FAILED' `
                -Body {
                    [void](Invoke-CIJPrepare `
                        -Fixture $fixture `
                        -BootstrapPath $probe)
                }
        } finally {
            foreach ($name in $names) {
                [Environment]::SetEnvironmentVariable($name, $saved[$name], 'Process')
            }
        }
        $observation = Join-Path $fixture.Trusted 'child-environment.txt'
        Assert-True -Name 'child observation exists' -Condition (
            Test-Path -LiteralPath $observation -PathType Leaf
        )
        $values = [ordered]@{}
        foreach ($line in [IO.File]::ReadAllLines($observation, $script:Utf8Strict)) {
            $index = $line.IndexOf('=')
            $values[$line.Substring(0, $index)] = $line.Substring($index + 1)
        }
        foreach ($name in @(
            'GITHUB_TOKEN',
            'ACTIONS_RUNTIME_TOKEN',
            'RUNNER_NAME',
            'CI',
            'FREEAGENT_API_KEY_SENTINEL'
        )) {
            Assert-Equal -Name "$name is absent" -Expected '' -Actual $values[$name]
        }
        Assert-Equal -Name 'safe sentinel survives' -Expected 'keep' `
            -Actual $values['FREEAGENT_SAFE_SENTINEL']
        foreach ($name in @('CWD', 'TEMP', 'TMP', 'TMPDIR')) {
            Assert-Equal -Name "$name uses trusted temp" `
                -Expected $fixture.Trusted `
                -Actual $values[$name]
        }
    }

    Invoke-Case 'manifest and sealed stage tampering are terminal' {
        $manifestFixture = New-CIJFixture -Container $suiteRoot -Name 'manifest-tamper'
        $manifestState = Invoke-CIJPrepare -Fixture $manifestFixture
        [IO.File]::AppendAllText(
            $manifestState.ManifestPath,
            'x',
            $script:Utf8NoBom
        )
        Assert-ThrowsCode `
            -Name 'manifest pin tamper' `
            -Code 'CIJ_MANIFEST_PIN_MISMATCH' `
            -Body {
                [void](Invoke-CIJVerify `
                    -State $manifestState `
                    -Required @('public-tree-manifest.v1.json'))
            }

        $stageFixture = New-CIJFixture -Container $suiteRoot -Name 'stage-tamper'
        $stageState = Invoke-CIJPrepare -Fixture $stageFixture
        $readme = Join-Path $stageState.StageRoot 'README.md'
        Write-Utf8NoBom -Path $readme -Text "# changed`n"
        Assert-ThrowsCode `
            -Name 'stage bytes tamper' `
            -Code 'CIJ_TREE_VERIFY_FAILED' `
            -Body {
                [void](Invoke-CIJVerify `
                    -State $stageState `
                    -Required @('public-tree-manifest.v1.json'))
            }
    }

    Invoke-Case 'supply-chain generation is deterministic canonical and atomic' {
        $script:SupplyFixture = New-CIJSupplyChainFixture `
            -Container $suiteRoot `
            -Name 'supply-positive-a'
        $script:SupplyTwin = New-CIJSupplyChainFixture `
            -Container $suiteRoot `
            -Name 'supply-positive-b'
        [void](Invoke-CIJGenerateSupplyChain -Fixture $script:SupplyFixture)
        [void](Invoke-CIJGenerateSupplyChain -Fixture $script:SupplyTwin)

        $expectedNames = @(
            'checksums.sha256',
            'provenance.unsigned.v1.json',
            'sbom.spdx.json'
        )
        foreach ($fixture in @($script:SupplyFixture, $script:SupplyTwin)) {
            Assert-True -Name 'supply-chain directory exists' -Condition (
                Test-Path -LiteralPath $fixture.SupplyChainRoot -PathType Container
            )
            [string[]]$actualNames = @(
                Get-ChildItem -LiteralPath $fixture.SupplyChainRoot -Force |
                    ForEach-Object { $_.Name }
            )
            [Array]::Sort($actualNames, [StringComparer]::Ordinal)
            Assert-Equal -Name 'supply-chain exact file names' `
                -Expected ($expectedNames -join "`n") `
                -Actual ($actualNames -join "`n")
            Assert-Equal -Name 'supply-chain contains no directory' -Expected 0 -Actual (
                @(Get-ChildItem `
                    -LiteralPath $fixture.SupplyChainRoot `
                    -Force |
                    Where-Object { $_ -is [IO.DirectoryInfo] }).Count
            )
            Assert-CIJCanonicalText `
                -Name 'SPDX SBOM' `
                -Path (Join-Path $fixture.SupplyChainRoot 'sbom.spdx.json') `
                -Json
            Assert-CIJCanonicalText `
                -Name 'unsigned provenance' `
                -Path (Join-Path `
                    $fixture.SupplyChainRoot `
                    'provenance.unsigned.v1.json') `
                -Json
            Assert-CIJCanonicalText `
                -Name 'SHA-256 checksum list' `
                -Path (Join-Path $fixture.SupplyChainRoot 'checksums.sha256')
            Assert-Equal -Name 'successful generation leaves trusted temp empty' `
                -Expected 0 `
                -Actual @(
                    Get-ChildItem -LiteralPath $fixture.TrustedTemp -Force
                ).Count
        }
        $firstHashes = Get-CIJSupplyChainOutputHash -Fixture $script:SupplyFixture
        $twinHashes = Get-CIJSupplyChainOutputHash -Fixture $script:SupplyTwin
        foreach ($relative in $firstHashes.Keys) {
            Assert-Equal -Name "deterministic bytes $relative" `
                -Expected $firstHashes[$relative] `
                -Actual $twinHashes[$relative]
        }
    }

    Invoke-Case 'supply-chain predicted set exactly matches extended verification' {
        $integration = New-CIJSupplyChainVerifyFixture `
            -Container $suiteRoot `
            -Name 'supply-predicted-set'
        $generatedObjects = @(
            Invoke-CIJGenerateSupplyChain `
                -Fixture $integration.GenerateFixture
        )
        $generatedStates = @($generatedObjects | Where-Object {
            $null -ne $_.PSObject.Properties['ExpectedArtifactSetSha256'] -and
            $null -ne $_.PSObject.Properties['ExpectedArtifactFileCount'] -and
            $null -ne $_.PSObject.Properties['ExpectedArtifactBytes']
        })
        Assert-Equal -Name 'generation returns one predicted set state' `
            -Expected 1 `
            -Actual $generatedStates.Count
        $predicted = $generatedStates[0]
        Assert-True -Name 'predicted set pin is lowercase SHA-256' -Condition (
            [string]$predicted.ExpectedArtifactSetSha256 -cmatch '^[0-9a-f]{64}$'
        )
        Assert-True -Name 'predicted file count is positive integer' -Condition (
            $predicted.ExpectedArtifactFileCount -is [int] -and
            [int]$predicted.ExpectedArtifactFileCount -gt 0
        )
        Assert-True -Name 'predicted bytes are positive int64' -Condition (
            $predicted.ExpectedArtifactBytes -is [int64] -and
            [int64]$predicted.ExpectedArtifactBytes -gt 0
        )

        $verified = Invoke-CIJVerify `
            -State $integration.VerifyState `
            -Required $integration.ExtendedRequired `
            -Optional $integration.GenerateFixture.Optional `
            -ExpectedArtifactSetSha256 (
                [string]$predicted.ExpectedArtifactSetSha256
            )
        Assert-Equal -Name 'predicted set pin equals extended Verify set pin' `
            -Expected ([string]$predicted.ExpectedArtifactSetSha256) `
            -Actual ([string]$verified.ArtifactSetSha256)
        Assert-Equal -Name 'predicted file count equals extended Verify count' `
            -Expected ([int]$predicted.ExpectedArtifactFileCount) `
            -Actual ([int]$verified.ArtifactFileCount)
        Assert-Equal -Name 'predicted bytes equal extended Verify bytes' `
            -Expected ([int64]$predicted.ExpectedArtifactBytes) `
            -Actual ([int64]$verified.ArtifactBytes)
    }

    Invoke-Case 'predicted set pin rejects replaced payload and metadata bytes' {
        foreach ($replacement in @(
            [pscustomobject]@{
                Name = 'payload'
                RelativePath = 'evidence/result.json'
            },
            [pscustomobject]@{
                Name = 'checksums'
                RelativePath = 'supply-chain/checksums.sha256'
            },
            [pscustomobject]@{
                Name = 'provenance'
                RelativePath = 'supply-chain/provenance.unsigned.v1.json'
            }
        )) {
            $integration = New-CIJSupplyChainVerifyFixture `
                -Container $suiteRoot `
                -Name "supply-pin-$($replacement.Name)"
            $generatedObjects = @(
                Invoke-CIJGenerateSupplyChain `
                    -Fixture $integration.GenerateFixture
            )
            $generatedStates = @($generatedObjects | Where-Object {
                $null -ne $_.PSObject.Properties['ExpectedArtifactSetSha256']
            })
            Assert-Equal -Name "$($replacement.Name) generation returns one state" `
                -Expected 1 `
                -Actual $generatedStates.Count
            $predicted = $generatedStates[0]
            $targetPath = Join-Path `
                $integration.GenerateFixture.ArtifactRoot `
                ($replacement.RelativePath.Replace(
                    [char]47,
                    [IO.Path]::DirectorySeparatorChar
                ))
            [byte[]]$originalBytes = [IO.File]::ReadAllBytes($targetPath)
            Assert-True -Name "$($replacement.Name) replacement target is nonempty" `
                -Condition ($originalBytes.LongLength -gt 0)
            [byte[]]$replacementBytes = $originalBytes.Clone()
            $replacementBytes[0] = if ($replacementBytes[0] -eq 0x7a) {
                [byte]0x79
            } else {
                [byte]0x7a
            }
            [IO.File]::WriteAllBytes($targetPath, $replacementBytes)
            Assert-Equal -Name "$($replacement.Name) replacement preserves bytes" `
                -Expected ([int64]$originalBytes.LongLength) `
                -Actual ([int64](Get-Item -LiteralPath $targetPath).Length)

            Assert-ThrowsCode `
                -Name "$($replacement.Name) replacement violates predicted pin" `
                -Code 'CIJ_ARTIFACT_SET_PIN_MISMATCH' `
                -Body {
                    [void](Invoke-CIJVerify `
                        -State $integration.VerifyState `
                        -Required $integration.ExtendedRequired `
                        -Optional $integration.GenerateFixture.Optional `
                        -ExpectedArtifactSetSha256 (
                            [string]$predicted.ExpectedArtifactSetSha256
                        ))
                }
        }
    }

    Invoke-Case 'SPDX 2.3 describes exact modules LicenseRefs and no fake Go checksum' {
        $sbomPath = Join-Path $script:SupplyFixture.SupplyChainRoot 'sbom.spdx.json'
        $sbom = Read-CIJStrictJson -Path $sbomPath
        Assert-Equal -Name 'SPDX version' -Expected 'SPDX-2.3' `
            -Actual ([string]$sbom.spdxVersion)
        Assert-Equal -Name 'SPDX data license' -Expected 'CC0-1.0' `
            -Actual ([string]$sbom.dataLicense)
        Assert-Equal -Name 'SPDX document id' -Expected 'SPDXRef-DOCUMENT' `
            -Actual ([string]$sbom.SPDXID)
        Assert-Equal -Name 'SPDX creation time' `
            -Expected $script:SupplyFixture.CreatedUtc `
            -Actual ([string]$sbom.creationInfo.created)
        Assert-True -Name 'SPDX has a tool creator' -Condition (
            @($sbom.creationInfo.creators | Where-Object {
                ([string]$_).StartsWith('Tool: ', [StringComparison]::Ordinal)
            }).Count -ge 1
        )
        Assert-True -Name 'SPDX namespace is HTTPS' -Condition (
            ([string]$sbom.documentNamespace).StartsWith(
                'https://',
                [StringComparison]::Ordinal
            )
        )
        $sbomHash = Get-LowerSha256 -Path $sbomPath
        Assert-True -Name 'SPDX namespace has no self digest' -Condition (
            ([string]$sbom.documentNamespace).IndexOf(
                $sbomHash,
                [StringComparison]::Ordinal
            ) -lt 0
        )

        $packages = @($sbom.packages)
        Assert-Equal -Name 'SPDX exact fixture package closure' `
            -Expected 5 `
            -Actual $packages.Count
        $mainPackages = @($packages | Where-Object {
            [string]$_.SPDXID -ceq 'SPDXRef-Package-FreeAgent'
        })
        Assert-Equal -Name 'SPDX has one AGPL main package' `
            -Expected 1 `
            -Actual $mainPackages.Count
        $mainPackage = $mainPackages[0]
        Assert-Equal -Name 'main package files are not analyzed' `
            -Expected $false `
            -Actual ([bool]$mainPackage.filesAnalyzed)
        $dependencyPackages = @($packages | Where-Object {
            [string]$_.name -ceq $script:SupplyFixture.ModulePath -and
            [string]$_.versionInfo -ceq $script:SupplyFixture.ModuleVersion
        })
        Assert-Equal -Name 'SPDX has exact dependency package' `
            -Expected 1 `
            -Actual $dependencyPackages.Count
        $dependencyPackage = $dependencyPackages[0]
        Assert-Equal -Name 'dependency declared license expression' `
            -Expected 'LicenseRef-example-special AND MIT' `
            -Actual ([string]$dependencyPackage.licenseDeclared)
        Assert-Equal -Name 'dependency concluded license is conservative' `
            -Expected 'NOASSERTION' `
            -Actual ([string]$dependencyPackage.licenseConcluded)
        Assert-Equal -Name 'dependency files are not analyzed' `
            -Expected $false `
            -Actual ([bool]$dependencyPackage.filesAnalyzed)
        $dependencyChecksumProperty =
            $dependencyPackage.PSObject.Properties['checksums']
        $dependencyChecksums = if ($null -eq $dependencyChecksumProperty) {
            @()
        } else {
            @($dependencyChecksumProperty.Value)
        }
        Assert-Equal -Name 'Go h1 is not exposed as SPDX SHA256' `
            -Expected 0 `
            -Actual @(
                $dependencyChecksums | Where-Object {
                    [string]$_.algorithm -ceq 'SHA256'
                }
            ).Count

        $licenseRefs = @($sbom.hasExtractedLicensingInfos | Where-Object {
            [string]$_.licenseId -ceq 'LicenseRef-example-special'
        })
        Assert-Equal -Name 'SPDX emits exact LicenseRef' `
            -Expected 1 `
            -Actual $licenseRefs.Count
        Assert-True -Name 'LicenseRef carries reviewed text' -Condition (
            ([string]$licenseRefs[0].extractedText).IndexOf(
                'Special fixture permission text.',
                [StringComparison]::Ordinal
            ) -ge 0
        )
        Assert-Equal -Name 'SPDX package ids are unique' `
            -Expected $packages.Count `
            -Actual @($packages | ForEach-Object {
                [string]$_.SPDXID
            } | Select-Object -Unique).Count
        foreach ($package in $packages) {
            Assert-True -Name "canonical package id $($package.name)" -Condition (
                [string]$package.SPDXID -cmatch '^SPDXRef-[A-Za-z0-9.-]+$'
            )
        }
        Assert-Equal -Name 'document describes main package once' `
            -Expected 1 `
            -Actual @($sbom.relationships | Where-Object {
                [string]$_.spdxElementId -ceq 'SPDXRef-DOCUMENT' -and
                [string]$_.relationshipType -ceq 'DESCRIBES' -and
                [string]$_.relatedSpdxElement -ceq [string]$mainPackage.SPDXID
            }).Count
        Assert-Equal -Name 'main package depends on dependency once' `
            -Expected 1 `
            -Actual @($sbom.relationships | Where-Object {
                [string]$_.spdxElementId -ceq [string]$mainPackage.SPDXID -and
                [string]$_.relationshipType -ceq 'DEPENDS_ON' -and
                [string]$_.relatedSpdxElement -ceq [string]$dependencyPackage.SPDXID
            }).Count

        $reactPackages = @($packages | Where-Object {
            [string]$_.name -ceq 'react' -and
            [string]$_.versionInfo -ceq '19.2.8'
        })
        $vitePackages = @($packages | Where-Object {
            [string]$_.name -ceq 'vite' -and
            [string]$_.versionInfo -ceq '7.3.6'
        })
        Assert-Equal -Name 'SPDX has exact npm runtime package' `
            -Expected 1 `
            -Actual $reactPackages.Count
        Assert-Equal -Name 'SPDX has exact npm build package' `
            -Expected 1 `
            -Actual $vitePackages.Count
        $reactPackage = $reactPackages[0]
        $vitePackage = $vitePackages[0]
        foreach ($npmPackage in @($reactPackage, $vitePackage)) {
            Assert-Equal -Name "npm license $($npmPackage.name)" `
                -Expected 'MIT' `
                -Actual ([string]$npmPackage.licenseDeclared)
            Assert-Equal -Name "npm files not analyzed $($npmPackage.name)" `
                -Expected $false `
                -Actual ([bool]$npmPackage.filesAnalyzed)
        }
        Assert-Equal -Name 'runtime npm package is a main dependency once' `
            -Expected 1 `
            -Actual @($sbom.relationships | Where-Object {
                [string]$_.spdxElementId -ceq [string]$mainPackage.SPDXID -and
                [string]$_.relationshipType -ceq 'DEPENDS_ON' -and
                [string]$_.relatedSpdxElement -ceq [string]$reactPackage.SPDXID
            }).Count
        Assert-Equal -Name 'build npm package is not a runtime dependency' `
            -Expected 0 `
            -Actual @($sbom.relationships | Where-Object {
                [string]$_.spdxElementId -ceq [string]$mainPackage.SPDXID -and
                [string]$_.relationshipType -ceq 'DEPENDS_ON' -and
                [string]$_.relatedSpdxElement -ceq [string]$vitePackage.SPDXID
            }).Count
        Assert-Equal -Name 'build npm package has exact build edge' `
            -Expected 1 `
            -Actual @($sbom.relationships | Where-Object {
                [string]$_.spdxElementId -ceq [string]$vitePackage.SPDXID -and
                [string]$_.relationshipType -ceq 'BUILD_DEPENDENCY_OF' -and
                [string]$_.relatedSpdxElement -ceq [string]$mainPackage.SPDXID
            }).Count
        $chunkFiles = @($sbom.files | Where-Object {
            [string]$_.fileName -ceq
                './internal/controlweb/dist/assets/vendor.js'
        })
        Assert-Equal -Name 'SPDX has exact third-party runtime chunk' `
            -Expected 1 `
            -Actual $chunkFiles.Count
        Assert-Equal -Name 'runtime chunk uses package license' `
            -Expected 'MIT' `
            -Actual ([string]$chunkFiles[0].licenseConcluded)
        Assert-Equal -Name 'runtime chunk is generated from runtime package' `
            -Expected 1 `
            -Actual @($sbom.relationships | Where-Object {
                [string]$_.spdxElementId -ceq [string]$chunkFiles[0].SPDXID -and
                [string]$_.relationshipType -ceq 'GENERATED_FROM' -and
                [string]$_.relatedSpdxElement -ceq [string]$reactPackage.SPDXID
            }).Count
        $distributedPackage = @($packages | Where-Object {
            [string]$_.SPDXID -ceq 'SPDXRef-Package-DistributedAssets'
        })[0]
        Assert-Equal -Name 'distributed asset license set is exact' `
            -Expected "AGPL-3.0-only`nMIT" `
            -Actual (@($distributedPackage.licenseInfoFromFiles) -join "`n")
        Assert-Equal -Name 'mixed distributed license conclusion is conservative' `
            -Expected 'NOASSERTION' `
            -Actual ([string]$distributedPackage.licenseConcluded)

        $decodedModuleSum = [BitConverter]::ToString(
            [Convert]::FromBase64String(
                $script:SupplyFixture.ModuleSum.Substring(3)
            )
        ).Replace('-', '').ToLowerInvariant()
        $sbomText = $script:Utf8Strict.GetString([IO.File]::ReadAllBytes($sbomPath))
        Assert-True -Name 'decoded Go h1 is not labeled package SHA256' -Condition (
            -not (
                $sbomText.IndexOf(
                    '"algorithm":"SHA256"',
                    [StringComparison]::Ordinal
                ) -ge 0 -and
                $sbomText.IndexOf(
                    $decodedModuleSum,
                    [StringComparison]::Ordinal
                ) -ge 0
            )
        )
        Assert-True -Name 'SPDX does not name downstream metadata' -Condition (
            $sbomText.IndexOf(
                'provenance.unsigned.v1.json',
                [StringComparison]::Ordinal
            ) -lt 0 -and
            $sbomText.IndexOf(
                'checksums.sha256',
                [StringComparison]::Ordinal
            ) -lt 0
        )
    }

    Invoke-Case 'unsigned provenance binds payload and declares informational trust' {
        $provenancePath = Join-Path `
            $script:SupplyFixture.SupplyChainRoot `
            'provenance.unsigned.v1.json'
        $provenance = Read-CIJStrictJson -Path $provenancePath
        Assert-Equal -Name 'in-toto statement type' `
            -Expected 'https://in-toto.io/Statement/v1' `
            -Actual ([string]$provenance._type)
        Assert-Equal -Name 'SLSA predicate type' `
            -Expected 'https://slsa.dev/provenance/v1' `
            -Actual ([string]$provenance.predicateType)
        foreach ($forbiddenTopLevel in @(
            'payload',
            'payloadType',
            'signatures',
            'envelope'
        )) {
            Assert-True -Name "unsigned statement omits $forbiddenTopLevel" -Condition (
                $null -eq $provenance.PSObject.Properties[$forbiddenTopLevel]
            )
        }

        [string[]]$expectedSubjects = @(
            @($script:SupplyFixture.Required) +
            @($script:SupplyFixture.Optional) +
            @('supply-chain/sbom.spdx.json')
        )
        [Array]::Sort($expectedSubjects, [StringComparer]::Ordinal)
        [string[]]$actualSubjects = @($provenance.subject | ForEach-Object {
            [string]$_.name
        })
        Assert-Equal -Name 'provenance subject exact ordered closure' `
            -Expected ($expectedSubjects -join "`n") `
            -Actual ($actualSubjects -join "`n")
        foreach ($subject in @($provenance.subject)) {
            $subjectPath = Join-Path $script:SupplyFixture.ArtifactRoot (
                ([string]$subject.name).
                    Replace([char]47, [IO.Path]::DirectorySeparatorChar)
            )
            Assert-Equal -Name "subject digest $($subject.name)" `
                -Expected (Get-LowerSha256 -Path $subjectPath) `
                -Actual ([string]$subject.digest.sha256)
        }
        Assert-True -Name 'provenance excludes itself' -Condition (
            $actualSubjects -cnotcontains
                'supply-chain/provenance.unsigned.v1.json'
        )
        Assert-True -Name 'provenance excludes checksum list' -Condition (
            $actualSubjects -cnotcontains 'supply-chain/checksums.sha256'
        )

        $external = $provenance.predicate.buildDefinition.externalParameters
        Assert-Equal -Name 'external revision' `
            -Expected $script:SupplyFixture.Revision `
            -Actual ([string]$external.revision)
        Assert-Equal -Name 'external job kind' `
            -Expected $script:SupplyFixture.JobKind `
            -Actual ([string]$external.jobKind)
        Assert-Equal -Name 'external target GOOS' `
            -Expected $script:SupplyFixture.TargetGoos `
            -Actual ([string]$external.targetGoos)
        Assert-Equal -Name 'external target GOARCH' `
            -Expected $script:SupplyFixture.TargetGoarch `
            -Actual ([string]$external.targetGoarch)
        $internal = $provenance.predicate.buildDefinition.internalParameters
        Assert-Equal -Name 'internal created UTC' `
            -Expected $script:SupplyFixture.CreatedUtc `
            -Actual ([string]$internal.createdUtc)
        Assert-Equal -Name 'internal metadata generator pin' `
            -Expected (Get-LowerSha256 -Path $script:Controller) `
            -Actual ([string]$internal.metadataGeneratorSha256)
        $resolvedDependencies =
            @($provenance.predicate.buildDefinition.resolvedDependencies)
        foreach ($binding in @(
            [pscustomobject]@{
                Uri = 'file:testdata/release/frontend-dependency-licenses.v1.json'
                Path = $script:SupplyFixture.FrontendManifestPath
            },
            [pscustomobject]@{
                Uri = 'file:internal/controlweb/package-lock.json'
                Path = $script:SupplyFixture.PackageLockPath
            }
        )) {
            $resolved = @($resolvedDependencies | Where-Object {
                [string]$_.uri -ceq $binding.Uri
            })
            Assert-Equal -Name "resolved dependency $($binding.Uri)" `
                -Expected 1 `
                -Actual $resolved.Count
            Assert-Equal -Name "resolved digest $($binding.Uri)" `
                -Expected (Get-LowerSha256 -Path $binding.Path) `
                -Actual ([string]$resolved[0].digest.sha256)
        }

        $extensionName =
            'https://github.com/endview/freeagent/provenance/metadata/v1'
        $extensionProperty =
            $provenance.predicate.PSObject.Properties[$extensionName]
        Assert-True -Name 'FreeAgent unsigned extension exists' -Condition (
            $null -ne $extensionProperty
        )
        $extension = $extensionProperty.Value
        Assert-Equal -Name 'provenance authentication is absent' `
            -Expected 'none' `
            -Actual ([string]$extension.authentication)
        Assert-Equal -Name 'provenance envelope is absent' `
            -Expected 'absent' `
            -Actual ([string]$extension.envelope)
        Assert-Equal -Name 'provenance security claim is informational' `
            -Expected 'informational-only' `
            -Actual ([string]$extension.securityClaim)
        Assert-True -Name 'run succeeded is a JSON boolean' -Condition (
            $extension.runSucceeded -is [bool]
        )
        Assert-Equal -Name 'run succeeded value' `
            -Expected $script:SupplyFixture.RunSucceeded `
            -Actual ([bool]$extension.runSucceeded)
        Assert-Equal -Name 'unsigned extension has exact fields' `
            -Expected 4 `
            -Actual @($extension.PSObject.Properties).Count

        $provenanceText = $script:Utf8Strict.GetString(
            [IO.File]::ReadAllBytes($provenancePath)
        )
        Assert-True -Name 'provenance does not claim a final artifact seal' -Condition (
            $provenanceText.IndexOf(
                'artifactSetSha256',
                [StringComparison]::Ordinal
            ) -lt 0
        )
        Assert-True -Name 'provenance has no downstream checksum edge' -Condition (
            $provenanceText.IndexOf(
                'checksums.sha256',
                [StringComparison]::Ordinal
            ) -lt 0
        )
    }

    Invoke-Case 'checksum list covers exact payload graph and excludes itself' {
        $checksumPath = Join-Path `
            $script:SupplyFixture.SupplyChainRoot `
            'checksums.sha256'
        $checksumText = $script:Utf8Strict.GetString(
            [IO.File]::ReadAllBytes($checksumPath)
        )
        [string[]]$lines = $checksumText.Substring(
            0,
            $checksumText.Length - 1
        ).Split([char]10)
        [string[]]$expectedPaths = @(
            Get-ChildItem `
                -LiteralPath $script:SupplyFixture.ArtifactRoot `
                -File `
                -Recurse `
                -Force |
                ForEach-Object {
                    $relative = $_.FullName.Substring(
                        $script:SupplyFixture.ArtifactRoot.Length + 1
                    ).Replace([char]92, [char]47)
                    if ($relative -cne 'supply-chain/checksums.sha256') {
                        $relative
                    }
                }
        )
        [Array]::Sort($expectedPaths, [StringComparer]::Ordinal)
        Assert-Equal -Name 'checksum exact line count' `
            -Expected $expectedPaths.Count `
            -Actual $lines.Count
        for ($index = 0; $index -lt $expectedPaths.Count; $index++) {
            $relative = $expectedPaths[$index]
            $path = Join-Path $script:SupplyFixture.ArtifactRoot (
                $relative.Replace([char]47, [IO.Path]::DirectorySeparatorChar)
            )
            $expectedLine = (Get-LowerSha256 -Path $path) + '  ' + $relative
            Assert-Equal -Name "checksum line $index" `
                -Expected $expectedLine `
                -Actual $lines[$index]
        }
        Assert-Equal -Name 'checksum paths are unique' `
            -Expected $lines.Count `
            -Actual @($lines | ForEach-Object {
                $_.Substring(66)
            } | Select-Object -Unique).Count
        Assert-True -Name 'checksum covers manifest payload' -Condition (
            @($lines | Where-Object {
                $_.EndsWith(
                    '  public-tree-manifest.v1.json',
                    [StringComparison]::Ordinal
                )
            }).Count -eq 1
        )
        Assert-True -Name 'checksum covers optional payload' -Condition (
            @($lines | Where-Object {
                $_.EndsWith(
                    '  logs/build.log',
                    [StringComparison]::Ordinal
                )
            }).Count -eq 1
        )
        Assert-True -Name 'checksum covers SBOM' -Condition (
            @($lines | Where-Object {
                $_.EndsWith(
                    '  supply-chain/sbom.spdx.json',
                    [StringComparison]::Ordinal
                )
            }).Count -eq 1
        )
        Assert-True -Name 'checksum covers provenance' -Condition (
            @($lines | Where-Object {
                $_.EndsWith(
                    '  supply-chain/provenance.unsigned.v1.json',
                    [StringComparison]::Ordinal
                )
            }).Count -eq 1
        )
        Assert-True -Name 'checksum excludes itself' -Condition (
            @($lines | Where-Object {
                $_.EndsWith(
                    '  supply-chain/checksums.sha256',
                    [StringComparison]::Ordinal
                )
            }).Count -eq 0
        )
    }

    Invoke-Case 'supply-chain generation refuses duplicate and partial publication' {
        $before = Get-CIJSupplyChainOutputHash -Fixture $script:SupplyFixture
        Assert-ThrowsCode `
            -Name 'duplicate generation' `
            -Code 'CIJ_SUPPLY_CHAIN_OUTPUT_EXISTS' `
            -Body {
                [void](Invoke-CIJGenerateSupplyChain `
                    -Fixture $script:SupplyFixture)
            }
        $after = Get-CIJSupplyChainOutputHash -Fixture $script:SupplyFixture
        foreach ($relative in $before.Keys) {
            Assert-Equal -Name "duplicate preserves $relative" `
                -Expected $before[$relative] `
                -Actual $after[$relative]
        }
        Assert-Equal -Name 'duplicate leaves trusted temp empty' `
            -Expected 0 `
            -Actual @(
                Get-ChildItem `
                    -LiteralPath $script:SupplyFixture.TrustedTemp `
                    -Force
            ).Count

        $partial = New-CIJSupplyChainFixture `
            -Container $suiteRoot `
            -Name 'supply-partial-output'
        [void][IO.Directory]::CreateDirectory($partial.SupplyChainRoot)
        $marker = Join-Path $partial.SupplyChainRoot 'sbom.spdx.json'
        Write-Utf8NoBom -Path $marker -Text "partial-marker`n"
        $markerHash = Get-LowerSha256 -Path $marker
        Assert-ThrowsCode `
            -Name 'partial output directory' `
            -Code 'CIJ_SUPPLY_CHAIN_OUTPUT_EXISTS' `
            -Body {
                [void](Invoke-CIJGenerateSupplyChain -Fixture $partial)
            }
        Assert-Equal -Name 'partial marker is not overwritten' `
            -Expected $markerHash `
            -Actual (Get-LowerSha256 -Path $marker)
        Assert-Equal -Name 'partial directory gains no files' `
            -Expected 1 `
            -Actual @(
                Get-ChildItem -LiteralPath $partial.SupplyChainRoot -Force
            ).Count
        Assert-Equal -Name 'partial attempt leaves trusted temp empty' `
            -Expected 0 `
            -Actual @(
                Get-ChildItem -LiteralPath $partial.TrustedTemp -Force
            ).Count
    }

    Invoke-Case 'supply-chain input closure rejects duplicates partials and rogue payload' {
        $duplicateManifest = New-CIJSupplyChainFixture `
            -Container $suiteRoot `
            -Name 'supply-duplicate-manifest'
        $manifestObject = Read-CIJStrictJson `
            -Path $duplicateManifest.DependencyManifestPath
        $manifestObject.modules = @(
            $manifestObject.modules[0],
            $manifestObject.modules[0]
        )
        Write-CIJJsonFixture `
            -Path $duplicateManifest.DependencyManifestPath `
            -Value $manifestObject
        Assert-CIJSupplyChainFailureIsAtomic `
            -Name 'duplicate dependency row' `
            -Fixture $duplicateManifest `
            -Body {
                [void](Invoke-CIJGenerateSupplyChain `
                    -Fixture $duplicateManifest)
            }

        $duplicateAllowlist = New-CIJSupplyChainFixture `
            -Container $suiteRoot `
            -Name 'supply-duplicate-allowlist'
        $repeatedRequired = @(
            $duplicateAllowlist.Required[0],
            $duplicateAllowlist.Required[0],
            $duplicateAllowlist.Required[1]
        )
        Assert-CIJSupplyChainFailureIsAtomic `
            -Name 'duplicate payload allowlist' `
            -Fixture $duplicateAllowlist `
            -Body {
                [void](Invoke-CIJGenerateSupplyChain `
                    -Fixture $duplicateAllowlist `
                    -Required $repeatedRequired)
            }

        $partialInput = New-CIJSupplyChainFixture `
            -Container $suiteRoot `
            -Name 'supply-partial-input'
        [IO.File]::Delete($partialInput.GoSumPath)
        Assert-CIJSupplyChainFailureIsAtomic `
            -Name 'missing go.sum' `
            -Fixture $partialInput `
            -Body {
                [void](Invoke-CIJGenerateSupplyChain -Fixture $partialInput)
            }

        $missingLicense = New-CIJSupplyChainFixture `
            -Container $suiteRoot `
            -Name 'supply-missing-license'
        [IO.File]::Delete($missingLicense.SpecialLicensePath)
        Assert-CIJSupplyChainFailureIsAtomic `
            -Name 'missing reviewed license file' `
            -Fixture $missingLicense `
            -Body {
                [void](Invoke-CIJGenerateSupplyChain `
                    -Fixture $missingLicense)
            }

        $roguePayload = New-CIJSupplyChainFixture `
            -Container $suiteRoot `
            -Name 'supply-rogue-payload'
        Write-Utf8NoBom `
            -Path (Join-Path $roguePayload.ArtifactRoot 'rogue.txt') `
            -Text "rogue`n"
        Assert-CIJSupplyChainFailureIsAtomic `
            -Name 'payload outside exact allowlist' `
            -Fixture $roguePayload `
            -Body {
                [void](Invoke-CIJGenerateSupplyChain `
                    -Fixture $roguePayload)
            }
    }

    Invoke-Case 'supply-chain rejects tampered Go sums licenses and assets atomically' {
        $goSumTamper = New-CIJSupplyChainFixture `
            -Container $suiteRoot `
            -Name 'supply-go-sum-tamper'
        $replacementSum = Get-CIJStringSha256Base64 -Value 'other module tree'
        $goSumText = [IO.File]::ReadAllText(
            $goSumTamper.GoSumPath,
            $script:Utf8Strict
        ).Replace(
            $goSumTamper.ModuleSum,
            $replacementSum
        )
        Write-Utf8NoBom -Path $goSumTamper.GoSumPath -Text $goSumText
        Assert-CIJSupplyChainFailureIsAtomic `
            -Name 'go.sum differs from reviewed manifest' `
            -Fixture $goSumTamper `
            -Body {
                [void](Invoke-CIJGenerateSupplyChain -Fixture $goSumTamper)
            }

        $licenseTamper = New-CIJSupplyChainFixture `
            -Container $suiteRoot `
            -Name 'supply-license-tamper'
        Write-Utf8NoBom `
            -Path $licenseTamper.LicensePath `
            -Text "MIT License fixturE.`n"
        Assert-CIJSupplyChainFailureIsAtomic `
            -Name 'same-size reviewed license tamper' `
            -Fixture $licenseTamper `
            -Body {
                [void](Invoke-CIJGenerateSupplyChain -Fixture $licenseTamper)
            }

        $assetTamper = New-CIJSupplyChainFixture `
            -Container $suiteRoot `
            -Name 'supply-asset-tamper'
        Write-Utf8NoBom -Path $assetTamper.AssetPath -Text "asset two`n"
        Assert-CIJSupplyChainFailureIsAtomic `
            -Name 'same-size distributed asset tamper' `
            -Fixture $assetTamper `
            -Body {
                [void](Invoke-CIJGenerateSupplyChain -Fixture $assetTamper)
            }

        $invalidUtf8 = New-CIJSupplyChainFixture `
            -Container $suiteRoot `
            -Name 'supply-invalid-license-utf8'
        [byte[]]$invalidUtf8Bytes = @(0xC3, 0x28)
        [IO.File]::WriteAllBytes(
            $invalidUtf8.SpecialLicensePath,
            $invalidUtf8Bytes
        )
        $invalidUtf8Hash = Get-LowerSha256 `
            -Path $invalidUtf8.SpecialLicensePath
        $invalidUtf8Manifest = Read-CIJStrictJson `
            -Path $invalidUtf8.DependencyManifestPath
        $invalidUtf8Manifest.license_refs[0].text_sha256 =
            $invalidUtf8Hash
        $invalidUtf8RequiredFile = @(
            $invalidUtf8Manifest.modules[0].required_files |
                Where-Object {
                    [string]$_.path -ceq 'SPECIAL-LICENSE.txt'
                }
        )
        Assert-Equal -Name 'invalid UTF-8 fixture required file exists' `
            -Expected 1 `
            -Actual $invalidUtf8RequiredFile.Count
        $invalidUtf8RequiredFile[0].size = $invalidUtf8Bytes.Length
        $invalidUtf8RequiredFile[0].sha256 = $invalidUtf8Hash
        Write-CIJJsonFixture `
            -Path $invalidUtf8.DependencyManifestPath `
            -Value $invalidUtf8Manifest
        Assert-CIJSupplyChainFailureIsAtomic `
            -Name 'reviewed license invalid UTF-8' `
            -Fixture $invalidUtf8 `
            -Body {
                [void](Invoke-CIJGenerateSupplyChain -Fixture $invalidUtf8)
            }
    }

    Invoke-Case 'supply-chain rejects npm lock manifest legal and chunk tampering' {
        $classificationTamper = New-CIJSupplyChainFixture `
            -Container $suiteRoot `
            -Name 'supply-npm-classification-tamper'
        $classificationManifest = Read-CIJStrictJson `
            -Path $classificationTamper.FrontendManifestPath
        $classificationManifest.packages[0].dependency_kind = 'build'
        Write-CIJJsonFixture `
            -Path $classificationTamper.FrontendManifestPath `
            -Value $classificationManifest
        Assert-CIJSupplyChainFailureIsAtomic `
            -Name 'npm runtime reclassified as build' `
            -Fixture $classificationTamper `
            -Body {
                [void](Invoke-CIJGenerateSupplyChain `
                    -Fixture $classificationTamper)
            }

        $lockCollision = New-CIJSupplyChainFixture `
            -Container $suiteRoot `
            -Name 'supply-npm-lock-empty-key-collision'
        $lockText = [IO.File]::ReadAllText(
            $lockCollision.PackageLockPath,
            $script:Utf8Strict
        )
        $packagesPrefix = New-Object Text.RegularExpressions.Regex(
            '("packages"\s*:\s*\{)\s*(""\s*:)',
            [Text.RegularExpressions.RegexOptions]::CultureInvariant
        )
        $collidingLockText = $packagesPrefix.Replace(
            $lockText,
            '$1 "__freeagent_package_lock_root__": {}, $2',
            1
        )
        Assert-True -Name 'npm empty-key collision fixture changed lock' `
            -Condition ($collidingLockText -cne $lockText)
        Write-Utf8NoBom `
            -Path $lockCollision.PackageLockPath `
            -Text $collidingLockText
        $collisionManifest = Read-CIJStrictJson `
            -Path $lockCollision.FrontendManifestPath
        $collisionManifest.package_lock.sha256 =
            Get-LowerSha256 -Path $lockCollision.PackageLockPath
        Write-CIJJsonFixture `
            -Path $lockCollision.FrontendManifestPath `
            -Value $collisionManifest
        Assert-CIJSupplyChainFailureIsAtomic `
            -Name 'npm lock empty-root projection collision' `
            -Fixture $lockCollision `
            -Body {
                [void](Invoke-CIJGenerateSupplyChain -Fixture $lockCollision)
            }

        $extraEmptyKey = New-CIJSupplyChainFixture `
            -Container $suiteRoot `
            -Name 'supply-npm-lock-extra-empty-key'
        $extraEmptyText = [IO.File]::ReadAllText(
            $extraEmptyKey.PackageLockPath,
            $script:Utf8Strict
        )
        $reactPrefix = New-Object Text.RegularExpressions.Regex(
            '("node_modules/react"\s*:\s*\{)',
            [Text.RegularExpressions.RegexOptions]::CultureInvariant
        )
        $extraEmptyText = $reactPrefix.Replace(
            $extraEmptyText,
            '$1 "": null,',
            1
        )
        Write-Utf8NoBom `
            -Path $extraEmptyKey.PackageLockPath `
            -Text $extraEmptyText
        $extraEmptyManifest = Read-CIJStrictJson `
            -Path $extraEmptyKey.FrontendManifestPath
        $extraEmptyManifest.package_lock.sha256 =
            Get-LowerSha256 -Path $extraEmptyKey.PackageLockPath
        Write-CIJJsonFixture `
            -Path $extraEmptyKey.FrontendManifestPath `
            -Value $extraEmptyManifest
        Assert-CIJSupplyChainFailureIsAtomic `
            -Name 'npm lock has only one schema-defined empty property' `
            -Fixture $extraEmptyKey `
            -Body {
                [void](Invoke-CIJGenerateSupplyChain -Fixture $extraEmptyKey)
            }

        $npmLegalTamper = New-CIJSupplyChainFixture `
            -Container $suiteRoot `
            -Name 'supply-npm-legal-tamper'
        Write-Utf8NoBom `
            -Path $npmLegalTamper.ReactLicensePath `
            -Text "React MIT fixture licensE.`n"
        Assert-CIJSupplyChainFailureIsAtomic `
            -Name 'same-size npm legal text tamper' `
            -Fixture $npmLegalTamper `
            -Body {
                [void](Invoke-CIJGenerateSupplyChain -Fixture $npmLegalTamper)
            }

        $chunkTamper = New-CIJSupplyChainFixture `
            -Container $suiteRoot `
            -Name 'supply-npm-chunk-tamper'
        Write-Utf8NoBom `
            -Path $chunkTamper.ChunkPath `
            -Text "/* fixture react runtimE */`n"
        Assert-CIJSupplyChainFailureIsAtomic `
            -Name 'same-size npm runtime chunk tamper' `
            -Fixture $chunkTamper `
            -Body {
                [void](Invoke-CIJGenerateSupplyChain -Fixture $chunkTamper)
            }
    }

    Invoke-Case 'supply-chain validates generator pin and serializes false outcome' {
        $badPin = New-CIJSupplyChainFixture `
            -Container $suiteRoot `
            -Name 'supply-bad-generator-pin'
        Assert-CIJSupplyChainFailureIsAtomic `
            -Name 'metadata generator malformed hash' `
            -Fixture $badPin `
            -Code 'CIJ_HASH_INVALID' `
            -Body {
                [void](Invoke-CIJGenerateSupplyChain `
                    -Fixture $badPin `
                    -MetadataGeneratorSha256 ('0' * 63))
            }

        $failedRun = New-CIJSupplyChainFixture `
            -Container $suiteRoot `
            -Name 'supply-failed-run' `
            -RunSucceeded $false `
            -JobKind 'windows' `
            -TargetGoos '' `
            -TargetGoarch ''
        [void](Invoke-CIJGenerateSupplyChain -Fixture $failedRun)
        $failedProvenance = Read-CIJStrictJson -Path (
            Join-Path `
                $failedRun.SupplyChainRoot `
                'provenance.unsigned.v1.json'
        )
        $extensionName =
            'https://github.com/endview/freeagent/provenance/metadata/v1'
        $extension =
            $failedProvenance.predicate.PSObject.Properties[$extensionName].Value
        Assert-True -Name 'failed run result remains JSON boolean' -Condition (
            $extension.runSucceeded -is [bool]
        )
        Assert-Equal -Name 'failed run result is false' `
            -Expected $false `
            -Actual ([bool]$extension.runSucceeded)
    }
} finally {
    $resolvedSuite = [IO.Path]::GetFullPath($suiteRoot)
    $resolvedParent = [IO.Path]::GetFullPath($testTempRoot).
        TrimEnd([char]92, [char]47) + [IO.Path]::DirectorySeparatorChar
    $comparison = if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        [StringComparison]::OrdinalIgnoreCase
    } else {
        [StringComparison]::Ordinal
    }
    if (-not $resolvedSuite.StartsWith($resolvedParent, $comparison)) {
        throw 'CI_RELEASE_JOB_SELFTEST_CLEANUP_BOUNDARY'
    }
    if (Test-Path -LiteralPath $resolvedSuite -PathType Container) {
        Remove-Item -LiteralPath $resolvedSuite -Recurse -Force
    }
}

if ($script:Failures.Count -gt 0) {
    $script:Failures | ForEach-Object { Write-Error $_ -ErrorAction Continue }
    throw (
        'CI_RELEASE_JOB_SELFTEST_FAIL ' +
        "cases=$($script:Cases) assertions=$($script:Assertions) failures=$($script:Failures.Count)"
    )
}

Write-Host (
    'CI_RELEASE_JOB_SELFTEST_PASS ' +
    "cases=$($script:Cases) assertions=$($script:Assertions)"
)
