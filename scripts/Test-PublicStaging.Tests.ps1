[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:Generator = Join-Path $PSScriptRoot 'New-PublicStaging.ps1'
$script:Verifier = Join-Path $PSScriptRoot 'Test-PublicStaging.ps1'
$script:PowerShell = (Get-Process -Id $PID).Path
$script:Utf8NoBom = New-Object System.Text.UTF8Encoding($false)
$script:UnicodeDirectoryName = ([char]0x4E2D).ToString() + ([char]0x6587).ToString()
$script:Cases = 0
$script:Assertions = 0
$script:Failures = New-Object 'System.Collections.Generic.List[string]'

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

function Invoke-ChildScript {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string[]]$Arguments
    )
    $output = New-Object 'System.Collections.Generic.List[string]'
    $savedErrorPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        & $script:PowerShell -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $Path @Arguments 2>&1 |
            ForEach-Object { [void]$output.Add([string]$_) }
        $exitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $savedErrorPreference
    }
    return [pscustomobject]@{
        ExitCode = $exitCode
        Output = ($output -join [Environment]::NewLine)
    }
}

function Assert-FailsWithCode {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)]$Result,
        [Parameter(Mandatory = $true)][string]$Code
    )
    Assert-True -Name "$Name exits nonzero" -Condition ($Result.ExitCode -ne 0)
    Assert-True -Name "$Name reports exact code" `
        -Condition ($Result.Output.IndexOf("PUBLIC_STAGING_FAIL code=$Code", [StringComparison]::Ordinal) -ge 0)
    Assert-True -Name "$Name does not report success" `
        -Condition (
            $Result.Output.IndexOf('PUBLIC_STAGING_PASS ', [StringComparison]::Ordinal) -lt 0 -and
            $Result.Output.IndexOf('PUBLIC_STAGING_VERIFY_PASS ', [StringComparison]::Ordinal) -lt 0
        )
}

function Write-Utf8NoBom {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text
    )
    $parent = [IO.Path]::GetDirectoryName($Path)
    if (-not [string]::IsNullOrEmpty($parent)) {
        [void][IO.Directory]::CreateDirectory($parent)
    }
    [IO.File]::WriteAllText($Path, $Text, $script:Utf8NoBom)
}

function Write-TestBytes {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][byte[]]$Bytes
    )
    $parent = [IO.Path]::GetDirectoryName($Path)
    if (-not [string]::IsNullOrEmpty($parent)) {
        [void][IO.Directory]::CreateDirectory($parent)
    }
    [IO.File]::WriteAllBytes($Path, $Bytes)
}

function Get-Sha256 {
    param([Parameter(Mandatory = $true)][string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function New-Fixture {
    param(
        [Parameter(Mandatory = $true)][string]$Container,
        [Parameter(Mandatory = $true)][string]$Name,
        [switch]$ExistingStage
    )
    $root = Join-Path $Container $Name
    $source = Join-Path $root 'source'
    $stage = Join-Path $root 'stage'
    $artifact = Join-Path $root 'artifact'
    $unicodeDirectory = Join-Path (Join-Path $source 'nested') $script:UnicodeDirectoryName
    [void][IO.Directory]::CreateDirectory($unicodeDirectory)
    [void][IO.Directory]::CreateDirectory($artifact)
    if ($ExistingStage) {
        [void][IO.Directory]::CreateDirectory($stage)
    }
    Write-Utf8NoBom -Path (Join-Path $source 'README.md') -Text "# fixture`n"
    Write-TestBytes -Path (Join-Path $unicodeDirectory 'data.bin') `
        -Bytes ([byte[]](0, 1, 2, 10, 13, 127, 128, 254, 255))
    Write-TestBytes -Path (Join-Path $source 'nested/zero.dat') -Bytes ([byte[]]@())
    return [pscustomobject]@{
        Root = $root
        Source = $source
        Stage = $stage
        Artifact = $artifact
        Manifest = Join-Path $artifact 'public-tree-manifest.v1.json'
        UnicodeDirectory = $unicodeDirectory
    }
}

function Invoke-Generator {
    param([Parameter(Mandatory = $true)]$Fixture)
    return Invoke-ChildScript -Path $script:Generator -Arguments @(
        '-SourceRoot', $Fixture.Source,
        '-StageRoot', $Fixture.Stage,
        '-ArtifactRoot', $Fixture.Artifact
    )
}

function Invoke-Verifier {
    param(
        [Parameter(Mandatory = $true)]$Fixture,
        [Parameter(Mandatory = $true)][string]$ManifestSha256
    )
    return Invoke-ChildScript -Path $script:Verifier -Arguments @(
        '-Root', $Fixture.Stage,
        '-ManifestPath', $Fixture.Manifest,
        '-ManifestSha256', $ManifestSha256
    )
}

function Assert-TreesEqual {
    param(
        [Parameter(Mandatory = $true)][string]$ExpectedRoot,
        [Parameter(Mandatory = $true)][string]$ActualRoot
    )
    $expected = @(Get-ChildItem -LiteralPath $ExpectedRoot -File -Recurse -Force |
        ForEach-Object {
            $relative = $_.FullName.Substring($ExpectedRoot.Length).TrimStart([char]92, [char]47).Replace([char]92, [char]47)
            "$relative`t$($_.Length)`t$((Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash)"
        } | Sort-Object)
    $actual = @(Get-ChildItem -LiteralPath $ActualRoot -File -Recurse -Force |
        ForEach-Object {
            $relative = $_.FullName.Substring($ActualRoot.Length).TrimStart([char]92, [char]47).Replace([char]92, [char]47)
            "$relative`t$($_.Length)`t$((Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash)"
        } | Sort-Object)
    Assert-Equal -Name 'file trees are byte-identical' -Expected ($expected -join "`n") -Actual ($actual -join "`n")
}

if (-not (Test-Path -LiteralPath $script:Generator -PathType Leaf)) {
    throw 'SELFTEST_GENERATOR_MISSING'
}
if (-not (Test-Path -LiteralPath $script:Verifier -PathType Leaf)) {
    throw 'SELFTEST_VERIFIER_MISSING'
}
if (-not [IO.Path]::IsPathRooted($script:PowerShell)) {
    throw 'SELFTEST_POWERSHELL_INVALID'
}

$testTempRoot = if (-not [string]::IsNullOrWhiteSpace($env:FREEAGENT_TEST_TEMP_ROOT)) {
    if (-not [IO.Path]::IsPathRooted($env:FREEAGENT_TEST_TEMP_ROOT) -or
        -not (Test-Path -LiteralPath $env:FREEAGENT_TEST_TEMP_ROOT -PathType Container)) {
        throw 'SELFTEST_TEMP_ROOT_INVALID'
    }
    [IO.Path]::GetFullPath($env:FREEAGENT_TEST_TEMP_ROOT)
} else {
    [IO.Path]::GetTempPath()
}
$suiteRoot = Join-Path $testTempRoot ("freeagent-public-staging-tests-" + [Guid]::NewGuid().ToString('N'))
[void][IO.Directory]::CreateDirectory($suiteRoot)
try {
    Invoke-Case 'missing stage is atomically created and independently verified' {
        $f = New-Fixture -Container $suiteRoot -Name 'success-missing-stage'
        $result = Invoke-Generator -Fixture $f
        Assert-Equal -Name 'generator exit code' -Expected 0 -Actual $result.ExitCode
        Assert-True -Name 'generator reports pass' -Condition ($result.Output -match 'PUBLIC_STAGING_PASS manifest_sha256=[0-9a-f]{64}')
        Assert-True -Name 'fixed manifest exists' -Condition (Test-Path -LiteralPath $f.Manifest -PathType Leaf)
        Assert-TreesEqual -ExpectedRoot $f.Source -ActualRoot $f.Stage
        $unicodeLeaf = (Get-Item -LiteralPath $f.UnicodeDirectory -Force).Name
        Assert-Equal -Name 'runtime Unicode fixture has the intended code points' `
            -Expected $script:UnicodeDirectoryName -Actual $unicodeLeaf
        $manifestDocument = [IO.File]::ReadAllText($f.Manifest, $script:Utf8NoBom) | ConvertFrom-Json
        $expectedBytes = ((Get-ChildItem -LiteralPath $f.Source -File -Recurse -Force | Measure-Object Length -Sum).Sum)
        Assert-Equal -Name 'manifest total_bytes is the real nonzero sum' -Expected ([int64]$expectedBytes) -Actual ([int64]$manifestDocument.total_bytes)
        Assert-True -Name 'pass output reports nonzero bytes' -Condition ($result.Output -match ' bytes=[1-9][0-9]* ')
        $manifestHash = Get-Sha256 -Path $f.Manifest
        $verify = Invoke-Verifier -Fixture $f -ManifestSha256 $manifestHash
        Assert-Equal -Name 'verifier exit code' -Expected 0 -Actual $verify.ExitCode
        Assert-True -Name 'verifier reports pass' -Condition ($verify.Output -match 'PUBLIC_STAGING_VERIFY_PASS ')
        $artifactEntries = @(Get-ChildItem -LiteralPath $f.Artifact -Force)
        Assert-Equal -Name 'artifact contains only final manifest' -Expected 1 -Actual $artifactEntries.Count
        Assert-Equal -Name 'artifact entry has fixed name' -Expected 'public-tree-manifest.v1.json' -Actual $artifactEntries[0].Name
    }

    Invoke-Case 'existing empty stage is accepted' {
        $f = New-Fixture -Container $suiteRoot -Name 'success-empty-stage' -ExistingStage
        $result = Invoke-Generator -Fixture $f
        Assert-Equal -Name 'generator exit code' -Expected 0 -Actual $result.ExitCode
        Assert-TreesEqual -ExpectedRoot $f.Source -ActualRoot $f.Stage
    }

    Invoke-Case 'flat root with zero subdirectories is accepted' {
        $f = New-Fixture -Container $suiteRoot -Name 'success-flat-root'
        [IO.Directory]::Delete((Join-Path $f.Source 'nested'), $true)
        $result = Invoke-Generator -Fixture $f
        Assert-Equal -Name 'generator exit code' -Expected 0 -Actual $result.ExitCode
        $manifestDocument = [IO.File]::ReadAllText($f.Manifest, $script:Utf8NoBom) | ConvertFrom-Json
        Assert-Equal -Name 'flat root directory_count' -Expected 0 -Actual ([int64]$manifestDocument.directory_count)
        $verify = Invoke-Verifier -Fixture $f -ManifestSha256 (Get-Sha256 -Path $f.Manifest)
        Assert-Equal -Name 'flat root verifier exit' -Expected 0 -Actual $verify.ExitCode
    }

    Invoke-Case 'same source and stage root is rejected' {
        $f = New-Fixture -Container $suiteRoot -Name 'same-root'
        $f.Stage = $f.Source
        $result = Invoke-Generator -Fixture $f
        Assert-FailsWithCode -Name 'same roots' -Result $result -Code 'PST_ROOTS_NOT_DISJOINT'
        Assert-True -Name 'no final manifest' -Condition (-not (Test-Path -LiteralPath $f.Manifest))
    }

    Invoke-Case 'nested artifact root is rejected' {
        $f = New-Fixture -Container $suiteRoot -Name 'nested-artifact'
        $nestedArtifact = Join-Path $f.Source 'artifact'
        [void][IO.Directory]::CreateDirectory($nestedArtifact)
        $f.Artifact = $nestedArtifact
        $f.Manifest = Join-Path $nestedArtifact 'public-tree-manifest.v1.json'
        $result = Invoke-Generator -Fixture $f
        Assert-FailsWithCode -Name 'nested artifact' -Result $result -Code 'PST_ROOTS_NOT_DISJOINT'
    }

    Invoke-Case 'drive-relative root is rejected' {
        $f = New-Fixture -Container $suiteRoot -Name 'drive-relative-root'
        $f.Stage = 'D:relative-stage'
        $result = Invoke-Generator -Fixture $f
        Assert-FailsWithCode -Name 'drive-relative root' -Result $result -Code 'PST_ROOT_NOT_ABSOLUTE'
    }

    Invoke-Case 'single-rooted current-drive path is rejected' {
        $f = New-Fixture -Container $suiteRoot -Name 'single-rooted-path'
        $f.Stage = '\relative-stage'
        $result = Invoke-Generator -Fixture $f
        Assert-FailsWithCode -Name 'single-rooted path' -Result $result -Code 'PST_ROOT_NOT_ABSOLUTE'
    }

    Invoke-Case 'forward-slash device namespace is rejected' {
        $f = New-Fixture -Container $suiteRoot -Name 'device-namespace'
        $f.Source = ('//?/' + [char]68 + ':/freeagent')
        $result = Invoke-Generator -Fixture $f
        Assert-FailsWithCode -Name 'device namespace' -Result $result -Code 'PST_ROOT_NOT_ABSOLUTE'
    }

    Invoke-Case 'nonempty stage file is rejected and preserved' {
        $f = New-Fixture -Container $suiteRoot -Name 'nonempty-stage' -ExistingStage
        $foreign = Join-Path $f.Stage 'foreign.txt'
        Write-Utf8NoBom -Path $foreign -Text "foreign`n"
        $result = Invoke-Generator -Fixture $f
        Assert-FailsWithCode -Name 'nonempty stage' -Result $result -Code 'PST_STAGE_NOT_EMPTY'
        Assert-Equal -Name 'foreign stage entry preserved' -Expected "foreign`n" -Actual ([IO.File]::ReadAllText($foreign))
        Assert-True -Name 'no final manifest' -Condition (-not (Test-Path -LiteralPath $f.Manifest))
    }

    Invoke-Case 'nonempty stage empty directory is rejected and preserved' {
        $f = New-Fixture -Container $suiteRoot -Name 'nonempty-stage-directory' -ExistingStage
        $foreign = Join-Path $f.Stage 'foreign-empty'
        [void][IO.Directory]::CreateDirectory($foreign)
        $result = Invoke-Generator -Fixture $f
        Assert-FailsWithCode -Name 'nonempty stage directory' -Result $result -Code 'PST_STAGE_NOT_EMPTY'
        Assert-True -Name 'foreign empty directory preserved' -Condition (Test-Path -LiteralPath $foreign -PathType Container)
    }

    Invoke-Case 'nonempty artifact is rejected without deleting foreign entry' {
        $f = New-Fixture -Container $suiteRoot -Name 'nonempty-artifact'
        $foreign = Join-Path $f.Artifact 'foreign.txt'
        Write-Utf8NoBom -Path $foreign -Text "foreign`n"
        $result = Invoke-Generator -Fixture $f
        Assert-FailsWithCode -Name 'nonempty artifact' -Result $result -Code 'PST_ARTIFACT_NOT_EMPTY'
        Assert-Equal -Name 'foreign artifact entry preserved' -Expected "foreign`n" -Actual ([IO.File]::ReadAllText($foreign))
        Assert-True -Name 'no final manifest' -Condition (-not (Test-Path -LiteralPath $f.Manifest))
    }

    Invoke-Case 'source .git is rejected' {
        $f = New-Fixture -Container $suiteRoot -Name 'git-forbidden'
        [void][IO.Directory]::CreateDirectory((Join-Path $f.Source '.git'))
        Write-Utf8NoBom -Path (Join-Path $f.Source '.git/config') -Text "[core]`n"
        $result = Invoke-Generator -Fixture $f
        Assert-FailsWithCode -Name 'source .git' -Result $result -Code 'PST_SOURCE_GIT_FORBIDDEN'
    }

    Invoke-Case 'source empty directory is rejected' {
        $f = New-Fixture -Container $suiteRoot -Name 'empty-directory'
        [void][IO.Directory]::CreateDirectory((Join-Path $f.Source 'empty'))
        $result = Invoke-Generator -Fixture $f
        Assert-FailsWithCode -Name 'empty directory' -Result $result -Code 'PST_EMPTY_DIRECTORY'
    }

    Invoke-Case 'source decomposed Unicode path is rejected as non-NFC' {
        $f = New-Fixture -Container $suiteRoot -Name 'decomposed-unicode-path'
        $decomposedName = 'e' + [char]0x0301 + '.txt'
        Write-Utf8NoBom -Path (Join-Path $f.Source $decomposedName) -Text "non-nfc`n"
        $result = Invoke-Generator -Fixture $f
        Assert-FailsWithCode -Name 'decomposed Unicode source path' -Result $result -Code 'PST_PATH_INVALID'
    }

    Invoke-Case 'source ADS is rejected on Windows' {
        $f = New-Fixture -Container $suiteRoot -Name 'ads-forbidden'
        $file = Join-Path $f.Source 'README.md'
        Set-Content -LiteralPath $file -Stream 'hidden' -Value 'secret' -NoNewline
        $result = Invoke-Generator -Fixture $f
        Assert-FailsWithCode -Name 'ADS' -Result $result -Code 'PST_ADS_FORBIDDEN'
    }

    Invoke-Case 'source root ADS is rejected and preserved on Windows' {
        $f = New-Fixture -Container $suiteRoot -Name 'source-root-ads-forbidden'
        Set-Content -LiteralPath $f.Source -Stream 'hidden' -Value 'root-secret' -NoNewline
        $result = Invoke-Generator -Fixture $f
        Assert-FailsWithCode -Name 'source root ADS' -Result $result -Code 'PST_ADS_FORBIDDEN'
        Assert-True -Name 'source root ADS is not deleted' `
            -Condition ($null -ne (Get-Content -LiteralPath $f.Source -Stream 'hidden' -Raw))
        Assert-True -Name 'source root ADS failure has no success manifest' `
            -Condition (-not (Test-Path -LiteralPath $f.Manifest))
    }

    Invoke-Case 'source child-directory ADS is rejected and preserved on Windows' {
        $f = New-Fixture -Container $suiteRoot -Name 'source-child-directory-ads-forbidden'
        $directory = Join-Path $f.Source 'nested'
        Set-Content -LiteralPath $directory -Stream 'hidden' -Value 'child-secret' -NoNewline
        $result = Invoke-Generator -Fixture $f
        Assert-FailsWithCode -Name 'source child-directory ADS' -Result $result -Code 'PST_ADS_FORBIDDEN'
        Assert-True -Name 'source child-directory ADS is not deleted' `
            -Condition ($null -ne (Get-Content -LiteralPath $directory -Stream 'hidden' -Raw))
        Assert-True -Name 'source child-directory ADS failure has no success manifest' `
            -Condition (-not (Test-Path -LiteralPath $f.Manifest))
    }

    Invoke-Case 'source sparse file is rejected when supported' {
        $f = New-Fixture -Container $suiteRoot -Name 'sparse-forbidden'
        $file = Join-Path $f.Source 'sparse.bin'
        Write-TestBytes -Path $file -Bytes ([byte[]](1, 2, 3))
        & fsutil.exe sparse setflag $file *> $null
        if ($LASTEXITCODE -eq 0) {
            $result = Invoke-Generator -Fixture $f
            Assert-FailsWithCode -Name 'sparse file' -Result $result -Code 'PST_SPARSE_FORBIDDEN'
        } else {
            Write-Host 'SKIP sparse fixture is not supported by this filesystem'
        }
    }

    Invoke-Case 'source child junction is rejected when junction creation succeeds' {
        $f = New-Fixture -Container $suiteRoot -Name 'reparse-forbidden'
        $target = Join-Path $f.Root 'junction-target'
        [void][IO.Directory]::CreateDirectory($target)
        Write-Utf8NoBom -Path (Join-Path $target 'never-read.txt') -Text "never`n"
        $junction = Join-Path $f.Source 'junction'
        & cmd.exe /d /c "mklink /J `"$junction`" `"$target`"" *> $null
        if ($LASTEXITCODE -eq 0) {
            try {
                $result = Invoke-Generator -Fixture $f
                Assert-FailsWithCode -Name 'junction' -Result $result -Code 'PST_REPARSE_FORBIDDEN'
            } finally {
                if (Test-Path -LiteralPath $junction) {
                    [IO.Directory]::Delete($junction, $false)
                }
            }
        } else {
            Write-Host 'SKIP junction fixture could not be created'
        }
    }

    Invoke-Case 'same source produces deterministic manifest bytes' {
        $f1 = New-Fixture -Container $suiteRoot -Name 'deterministic-one'
        $f2 = New-Fixture -Container $suiteRoot -Name 'deterministic-two'
        [IO.Directory]::Delete($f2.Source, $true)
        [void][IO.Directory]::CreateDirectory($f2.Source)
        foreach ($directory in @(Get-ChildItem -LiteralPath $f1.Source -Directory -Recurse -Force | Sort-Object FullName)) {
            $relative = $directory.FullName.Substring($f1.Source.Length).TrimStart([char]92, [char]47)
            [void][IO.Directory]::CreateDirectory((Join-Path $f2.Source $relative))
        }
        foreach ($file in @(Get-ChildItem -LiteralPath $f1.Source -File -Recurse -Force | Sort-Object FullName)) {
            $relative = $file.FullName.Substring($f1.Source.Length).TrimStart([char]92, [char]47)
            [IO.File]::Copy($file.FullName, (Join-Path $f2.Source $relative), $false)
        }
        $r1 = Invoke-Generator -Fixture $f1
        $r2 = Invoke-Generator -Fixture $f2
        Assert-Equal -Name 'first generator exit' -Expected 0 -Actual $r1.ExitCode
        Assert-Equal -Name 'second generator exit' -Expected 0 -Actual $r2.ExitCode
        Assert-Equal -Name 'manifest hashes deterministic' -Expected (Get-Sha256 -Path $f1.Manifest) -Actual (Get-Sha256 -Path $f2.Manifest)
        Assert-Equal -Name 'manifest bytes deterministic' `
            -Expected ([Convert]::ToBase64String([IO.File]::ReadAllBytes($f1.Manifest))) `
            -Actual ([Convert]::ToBase64String([IO.File]::ReadAllBytes($f2.Manifest)))
    }

    Invoke-Case 'wrong pinned manifest hash is rejected' {
        $f = New-Fixture -Container $suiteRoot -Name 'wrong-pin'
        $result = Invoke-Generator -Fixture $f
        Assert-Equal -Name 'generator exit code' -Expected 0 -Actual $result.ExitCode
        $verify = Invoke-Verifier -Fixture $f -ManifestSha256 ('0' * 64)
        Assert-FailsWithCode -Name 'wrong manifest pin' -Result $verify -Code 'PST_MANIFEST_HASH_MISMATCH'
    }

    Invoke-Case 'ordinary duplicate JSON key is rejected explicitly' {
        $f = New-Fixture -Container $suiteRoot -Name 'duplicate-key'
        $result = Invoke-Generator -Fixture $f
        Assert-Equal -Name 'generator exit code' -Expected 0 -Actual $result.ExitCode
        $text = [IO.File]::ReadAllText($f.Manifest, $script:Utf8NoBom)
        $text = $text.Replace("  `"schema_version`": 1,`n", "  `"schema_version`": 1,`n  `"schema_version`": 1,`n")
        [IO.File]::WriteAllText($f.Manifest, $text, $script:Utf8NoBom)
        $verify = Invoke-Verifier -Fixture $f -ManifestSha256 (Get-Sha256 -Path $f.Manifest)
        Assert-FailsWithCode -Name 'duplicate key' -Result $verify -Code 'PST_MANIFEST_JSON_DUPLICATE_KEY'
    }

    Invoke-Case 'escaped-equivalent duplicate JSON key is rejected explicitly' {
        $f = New-Fixture -Container $suiteRoot -Name 'escaped-duplicate-key'
        $result = Invoke-Generator -Fixture $f
        Assert-Equal -Name 'generator exit code' -Expected 0 -Actual $result.ExitCode
        $text = [IO.File]::ReadAllText($f.Manifest, $script:Utf8NoBom)
        $text = $text.Replace("  `"schema_version`": 1,`n", "  `"schema_version`": 1,`n  `"schema\u005fversion`": 1,`n")
        [IO.File]::WriteAllText($f.Manifest, $text, $script:Utf8NoBom)
        $verify = Invoke-Verifier -Fixture $f -ManifestSha256 (Get-Sha256 -Path $f.Manifest)
        Assert-FailsWithCode -Name 'escaped duplicate key' -Result $verify -Code 'PST_MANIFEST_JSON_DUPLICATE_KEY'
    }

    Invoke-Case 'CRLF manifest is rejected before JSON trust' {
        $f = New-Fixture -Container $suiteRoot -Name 'crlf-manifest'
        $result = Invoke-Generator -Fixture $f
        Assert-Equal -Name 'generator exit code' -Expected 0 -Actual $result.ExitCode
        $text = [IO.File]::ReadAllText($f.Manifest, $script:Utf8NoBom).Replace("`n", "`r`n")
        [IO.File]::WriteAllText($f.Manifest, $text, $script:Utf8NoBom)
        $verify = Invoke-Verifier -Fixture $f -ManifestSha256 (Get-Sha256 -Path $f.Manifest)
        Assert-FailsWithCode -Name 'CRLF manifest' -Result $verify -Code 'PST_MANIFEST_FORMAT'
    }

    Invoke-Case 'zero-byte manifest is rejected with the format error code' {
        $f = New-Fixture -Container $suiteRoot -Name 'empty-manifest'
        $result = Invoke-Generator -Fixture $f
        Assert-Equal -Name 'generator exit code' -Expected 0 -Actual $result.ExitCode
        [IO.File]::WriteAllBytes($f.Manifest, [byte[]]@())
        $verify = Invoke-Verifier -Fixture $f `
            -ManifestSha256 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'
        Assert-FailsWithCode -Name 'zero-byte manifest' -Result $verify -Code 'PST_MANIFEST_FORMAT'
    }

    Invoke-Case 'manifest extra property is rejected by exact schema' {
        $f = New-Fixture -Container $suiteRoot -Name 'extra-property'
        $result = Invoke-Generator -Fixture $f
        Assert-Equal -Name 'generator exit code' -Expected 0 -Actual $result.ExitCode
        $text = [IO.File]::ReadAllText($f.Manifest, $script:Utf8NoBom)
        $text = $text.Replace("  `"kind`": `"freeagent-public-tree-manifest`",`n", "  `"kind`": `"freeagent-public-tree-manifest`",`n  `"extra`": true,`n")
        [IO.File]::WriteAllText($f.Manifest, $text, $script:Utf8NoBom)
        $verify = Invoke-Verifier -Fixture $f -ManifestSha256 (Get-Sha256 -Path $f.Manifest)
        Assert-FailsWithCode -Name 'extra property' -Result $verify -Code 'PST_MANIFEST_SCHEMA'
    }

    Invoke-Case 'same-size staged content tamper is rejected' {
        $f = New-Fixture -Container $suiteRoot -Name 'stage-tamper'
        $result = Invoke-Generator -Fixture $f
        Assert-Equal -Name 'generator exit code' -Expected 0 -Actual $result.ExitCode
        $path = Join-Path $f.Stage 'README.md'
        $bytes = [IO.File]::ReadAllBytes($path)
        $bytes[0] = $bytes[0] -bxor 1
        [IO.File]::WriteAllBytes($path, $bytes)
        $verify = Invoke-Verifier -Fixture $f -ManifestSha256 (Get-Sha256 -Path $f.Manifest)
        Assert-FailsWithCode -Name 'stage tamper' -Result $verify -Code 'PST_MANIFEST_CONTENT_MISMATCH'
    }

    Invoke-Case 'final stage root ADS is rejected and preserved on Windows' {
        $f = New-Fixture -Container $suiteRoot -Name 'final-stage-root-ads'
        $result = Invoke-Generator -Fixture $f
        Assert-Equal -Name 'generator exit code' -Expected 0 -Actual $result.ExitCode
        Set-Content -LiteralPath $f.Stage -Stream 'hidden' -Value 'stage-root-secret' -NoNewline
        $verify = Invoke-Verifier -Fixture $f -ManifestSha256 (Get-Sha256 -Path $f.Manifest)
        Assert-FailsWithCode -Name 'final stage root ADS' -Result $verify -Code 'PST_ADS_FORBIDDEN'
        Assert-True -Name 'final stage root ADS is not deleted' `
            -Condition ($null -ne (Get-Content -LiteralPath $f.Stage -Stream 'hidden' -Raw))
    }

    Invoke-Case 'final stage child-directory ADS is rejected and preserved on Windows' {
        $f = New-Fixture -Container $suiteRoot -Name 'final-stage-child-directory-ads'
        $result = Invoke-Generator -Fixture $f
        Assert-Equal -Name 'generator exit code' -Expected 0 -Actual $result.ExitCode
        $directory = Join-Path $f.Stage 'nested'
        Set-Content -LiteralPath $directory -Stream 'hidden' -Value 'stage-child-secret' -NoNewline
        $verify = Invoke-Verifier -Fixture $f -ManifestSha256 (Get-Sha256 -Path $f.Manifest)
        Assert-FailsWithCode -Name 'final stage child-directory ADS' -Result $verify -Code 'PST_ADS_FORBIDDEN'
        Assert-True -Name 'final stage child-directory ADS is not deleted' `
            -Condition ($null -ne (Get-Content -LiteralPath $directory -Stream 'hidden' -Raw))
    }

    Invoke-Case 'manifest reserved portable path is rejected' {
        $f = New-Fixture -Container $suiteRoot -Name 'reserved-manifest-path'
        $result = Invoke-Generator -Fixture $f
        Assert-Equal -Name 'generator exit code' -Expected 0 -Actual $result.ExitCode
        $text = [IO.File]::ReadAllText($f.Manifest, $script:Utf8NoBom)
        $text = $text.Replace('README.md', 'CON.txt')
        [IO.File]::WriteAllText($f.Manifest, $text, $script:Utf8NoBom)
        $verify = Invoke-Verifier -Fixture $f -ManifestSha256 (Get-Sha256 -Path $f.Manifest)
        Assert-FailsWithCode -Name 'reserved manifest path' -Result $verify -Code 'PST_PATH_INVALID'
    }

    Invoke-Case 'manifest decomposed Unicode path is rejected as non-NFC' {
        $f = New-Fixture -Container $suiteRoot -Name 'decomposed-manifest-path'
        $result = Invoke-Generator -Fixture $f
        Assert-Equal -Name 'generator exit code' -Expected 0 -Actual $result.ExitCode
        $decomposedName = 'e' + [char]0x0301 + '.txt'
        $text = [IO.File]::ReadAllText($f.Manifest, $script:Utf8NoBom).Replace('README.md', $decomposedName)
        [IO.File]::WriteAllText($f.Manifest, $text, $script:Utf8NoBom)
        $verify = Invoke-Verifier -Fixture $f -ManifestSha256 (Get-Sha256 -Path $f.Manifest)
        Assert-FailsWithCode -Name 'decomposed Unicode manifest path' -Result $verify -Code 'PST_PATH_INVALID'
    }
} finally {
    if (Test-Path -LiteralPath $suiteRoot -PathType Container) {
        Remove-Item -LiteralPath $suiteRoot -Recurse -Force
    }
}

if ($script:Failures.Count -gt 0) {
    $script:Failures | ForEach-Object { Write-Error $_ -ErrorAction Continue }
    throw "PUBLIC_STAGING_SELFTEST_FAIL cases=$($script:Cases) assertions=$($script:Assertions) failures=$($script:Failures.Count)"
}

Write-Host "PUBLIC_STAGING_SELFTEST_PASS cases=$($script:Cases) assertions=$($script:Assertions)"
