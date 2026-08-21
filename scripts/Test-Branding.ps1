[CmdletBinding()]
param(
    [string]$RepositoryRoot = (Split-Path -Parent $PSScriptRoot)
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$root = (Resolve-Path -LiteralPath $RepositoryRoot).Path
$excludedRoots = @('.git', '.tools', 'bin', 'data', 'logs', 'storage')
$excludedPrefixes = @('.cache/go-build')
$retiredTokens = @(
    ([char[]]@(120, 120, 98) -join ''),
    ([char[]]@(99, 104, 117, 110, 107, 98, 117, 114, 115, 116) -join '')
)
$violations = New-Object System.Collections.Generic.List[string]

function Get-RelativePath {
    param([string]$Path)

    $relative = $Path.Substring($root.Length)
    $relative = $relative.TrimStart([char[]]@([char]92, [char]47))
    return $relative.Replace([char]92, [char]47)
}

function Test-Excluded {
    param([string]$RelativePath)

    foreach ($directory in $excludedRoots) {
        if ($RelativePath -eq $directory -or $RelativePath.StartsWith($directory + '/', [StringComparison]::OrdinalIgnoreCase)) {
            return $true
        }
    }
    foreach ($prefix in $excludedPrefixes) {
        if ($RelativePath -eq $prefix -or $RelativePath.StartsWith($prefix + '/', [StringComparison]::OrdinalIgnoreCase)) {
            return $true
        }
    }
    return $false
}

$files = Get-ChildItem -LiteralPath $root -Recurse -File -Force |
    Where-Object {
        $relative = Get-RelativePath -Path $_.FullName
        -not (Test-Excluded -RelativePath $relative)
    }

foreach ($file in $files) {
    $relative = Get-RelativePath -Path $file.FullName
    if ($file.Extension -ieq '.php') {
        $violations.Add("$relative is a legacy PHP source file.")
        continue
    }

    $bytes = [IO.File]::ReadAllBytes($file.FullName)
    $ascii = [Text.Encoding]::ASCII.GetString($bytes)
    foreach ($token in $retiredTokens) {
        $pattern = [Regex]::Escape($token)
        $matches = [Regex]::Matches($ascii, $pattern, [Text.RegularExpressions.RegexOptions]::IgnoreCase)
        if ($matches.Count -gt 0) {
            $violations.Add("$relative contains a retired project identity ($($matches.Count) match(es)).")
        }
    }
}

if ($violations.Count -gt 0) {
    $violations | ForEach-Object { Write-Error $_ -ErrorAction Continue }
    throw "Brand gate failed with $($violations.Count) violation(s)."
}

Write-Host 'Brand gate passed: no retired project identity or legacy PHP source was found.'
