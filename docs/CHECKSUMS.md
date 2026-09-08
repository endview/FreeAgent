# Verify release checksums

The distribution directory contains `SHA256SUMS`, with one lowercase SHA-256
record for every archive. Verify it before extraction.

PowerShell example:

```powershell
$Archive = 'freeagent-v0.1.0-dev.1-windows-amd64.zip'
$Expected = (Get-Content .\SHA256SUMS | Where-Object {
  $_ -match ('^[0-9a-f]{64}  ' + [regex]::Escape($Archive) + '$')
}) -replace ('  ' + [regex]::Escape($Archive) + '$'), ''
if ($Expected.Count -ne 1) { throw 'archive checksum record is missing or ambiguous' }
$Actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $Archive).Hash.ToLowerInvariant()
if ($Actual -cne $Expected) { throw 'archive checksum mismatch' }
```

Linux or macOS example:

```sh
sha256sum -c SHA256SUMS
```

After extraction, `supply-chain/checksums.sha256` covers every sealed artifact
payload file except the checksum file itself. Paths are relative to the package
root. `ARTIFACT_SET_SHA256` records the release controller's sealed artifact-set
digest, and the provenance subject set binds the same payload plus the SBOM.

Checksums provide integrity detection, not publisher authentication. This
Developer Preview does not include signatures, certificates, or notarization.
