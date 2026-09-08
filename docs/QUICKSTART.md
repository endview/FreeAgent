# Developer Preview quickstart

This walkthrough uses the bundled deterministic echo model. It needs no API
key, network access, or paid provider.

Run the commands from the extracted package root. The examples below use the
Windows AMD64 executable; substitute the executable for the selected target.

```powershell
$FreeAgent = (Resolve-Path .\bin\freeagent-windows-amd64.exe).Path
$Runtime = Join-Path $PWD 'data\quickstart'
New-Item -ItemType Directory -Force $Runtime | Out-Null

& $FreeAgent init `
  --db (Join-Path $Runtime 'current.sqlite') `
  --artifact-root (Join-Path $Runtime 'artifacts') `
  --seed .\config\current-v1.bootstrap.seed.json

& $FreeAgent conversation-create `
  --db (Join-Path $Runtime 'current.sqlite') `
  --conversation preview-conversation

$Deadline = (Get-Date).ToUniversalTime().AddMinutes(10).ToString('o')
$Turn1 = & $FreeAgent chat `
  --db (Join-Path $Runtime 'current.sqlite') `
  --artifact-root (Join-Path $Runtime 'artifacts') `
  --conversation preview-conversation `
  --conversation-revision 0 `
  --message 'remember: the package works offline' `
  --request-id preview-turn-1 `
  --deadline $Deadline | ConvertFrom-Json

$Turn2 = & $FreeAgent chat `
  --db (Join-Path $Runtime 'current.sqlite') `
  --artifact-root (Join-Path $Runtime 'artifacts') `
  --conversation preview-conversation `
  --conversation-revision $Turn1.conversation_revision `
  --conversation-head-run $Turn1.run_id `
  --message 'what did I ask you to remember?' `
  --request-id preview-turn-2 `
  --deadline $Deadline | ConvertFrom-Json

& $FreeAgent conversation-get `
  --db (Join-Path $Runtime 'current.sqlite') `
  --conversation preview-conversation
```

An exact retry uses the same request ID, message, deadline, conversation
revision, and head. It must return the original run rather than creating a new
provider attempt. Changing one of those values is a different request or a
conflict, not an exact retry.

Stop all processes using the store before backup:

```powershell
& $FreeAgent backup `
  --db (Join-Path $Runtime 'current.sqlite') `
  --artifact-root (Join-Path $Runtime 'artifacts') `
  --out (Join-Path $Runtime 'backup')

& $FreeAgent backup-verify --bundle (Join-Path $Runtime 'backup')

& $FreeAgent restore `
  --bundle (Join-Path $Runtime 'backup') `
  --db (Join-Path $Runtime 'restored.sqlite') `
  --artifact-root (Join-Path $Runtime 'restored-artifacts')
```

`serve` is local-only and must use a literal loopback address:

```powershell
& $FreeAgent serve `
  --db (Join-Path $Runtime 'restored.sqlite') `
  --artifact-root (Join-Path $Runtime 'restored-artifacts') `
  --listen 127.0.0.1:8080
```

Control is disabled unless `--enable-control` is supplied. Its bootstrap
handoff is sensitive runtime material and must be created in an owner-only
directory. See `KNOWN_LIMITATIONS.md` before enabling Control.
