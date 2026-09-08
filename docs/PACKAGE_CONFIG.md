# Bundled configuration

`current-v1.bootstrap.seed.json` is the offline quickstart seed. Its referenced
`bootstrap-artifacts` are included below this directory at the exact relative
paths recorded in the seed.

The seed is immutable package input. Copy it before editing. A changed seed or
artifact is a new local configuration and is not covered by the release
checksums or supplied evidence.

Provider credentials are never bundled here. Supply an opt-in provider secret
only through the documented runtime environment variable on the machine that
performs the call.
