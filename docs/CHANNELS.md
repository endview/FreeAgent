# Optional channels

Updated 2026-11-03. Telegram private-text pairing, real DeepSeek replies and
confirmed delivery passed on Debian 12 with v7.11.0-dev.1. See the
[scoped live record](FREEAGENT_TELEGRAM_PRIVATE_LIVE_ACCEPTANCE_2026-11-03.md).
Telegram groups, second-account permissions, live file delivery and recovery
checks remain NOT_RUN. Lark and QQ still have local/mock acceptance only.

## Setup

1. Start the GUI/web/chat Owner. The CLI attaches to that Owner; no second Bot
   process is created. Disabled connections do no network work.
2. In Key management, save credentials with protocol telegram, lark or qq.
   Origins: `https://api.telegram.org`, `https://open.feishu.cn` (or
   `https://open.larksuite.com`), `https://api.sgroup.qq.com` (or QQ sandbox).
   Windows uses the existing DPAPI store. Channel keys cannot be model keys.
3. In Settings, select the saved profile, set Lark/QQ app ID, and enable the
   connection. Inspect its state/error. Optional connection errors leave local
   UI usable. QQ needs an explicit numeric loopback listener and your own HTTPS
   reverse proxy to `/qq/events`; FreeAgent provides no hosted ingress.
4. Generate a ten-minute one-use pairing code and send `/pair CODE` to the
   intended chat/group. Five failed attempts invalidate the code. Pairing
   creates a None space and its first conversation; it grants no tools.
5. Grant both the space and the actual person a permission locally when needed.
   Effective authority is the lower of both, frozen for each Run. Increases
   affect future Runs; reductions block old authority immediately.

CLI examples (Owner must be running):

```powershell
freeagent channel telegram status --data-dir <data>
freeagent channel telegram configure --data-dir <data> --key <saved-id> --enabled true
freeagent channel telegram pair-code --data-dir <data>
freeagent channel lark configure --data-dir <data> --key <saved-id> --app-id <app> --enabled true
freeagent channel qq configure --data-dir <data> --key <saved-id> --app-id <app> --listen 127.0.0.1:8098 --enabled true
```

status/pair-code/configure accept telegram/lark/qq. AI can propose
`channels.set_policy` with saved key references using existing local
confirmation. Pairing capabilities are not given to models.

## Commands and routing

`/help`, `/mode` (read only), `/status`, `/stop`, `/continue`, `/abandon`,
`/compact`; organization: `/multi on|off`, `/sessions`, `/new title`, `/switch ID`,
`/default ID`. None may chat and view public state. Organization and manual
compaction require operation permission. Approval/continue/abandon retain the
original requester; stop permits that requester and currently operational
members. GUI/CLI can continue the same saved Run without changing its requester.

Received group text is public background and creates no Run until explicitly
woken. The trigger fixes its background cutoff. Display names, forwards,
quotations and summaries cannot supply identity or approval. GUI shows recent
unassociated background separately from execution turns. Replies stay bound to
the original app, tenant/chat and Run when clients change selected conversations.

Moving a local conversation into an external space requires the existing local
publication confirmation. Visible local text is mirrored once into the public
scope in bounded batches; private tool bodies and synthetic command prompts
remain excluded. Original records are retained.

Callbacks verify the exact confirmation, original requester, target, bot/card
message, current grants, expiry and pending state. Product settings are confirmed
locally. Reconnecting invalidates volatile buttons; GUI/CLI can handle the
existing pending operation. There are no chat confirmation codes.

## Platform matrix

| Platform | Local implementation | Limits | Real acceptance |
|---|---|---|---|
| Telegram | Bot polling, private/group text, mentions/replies/commands, buttons; bounded media adapters | Only received updates; Privacy Mode applies; edited-input execution unsupported; attachment rules in ATTACHMENTS.md | Private text PASS; groups, second account, media and callbacks NOT_RUN |
| Lark/Feishu | App websocket, bounded multipart/acks, private/group text, cards | Internal app credentials; app+tenant identity scope; group visibility depends on event permissions | NOT_RUN |
| QQ official | Ed25519 webhook; C2C and GROUP_AT_MESSAGE_CREATE; passive replies | Ordinary group text, guild/channel types, edits and approval buttons unsupported; approvals remain local | NOT_RUN |

QQ enforces a conservative local ceiling of 120 seconds and five passive replies
per received message. This is not proof that an application has that platform
quota. Failed/expired delivery retains the completed Run. A later legal `/status`
reads its saved answer without rerunning model/tools. App permissions, visibility
and passive reply behavior still need real application testing.

Sends are paced; total transport text is bounded at 64 KiB and split. Telegram
groups use a conservative three-second gap; other targets one second. Long
server retry delays fail the send rather than hold work indefinitely. Runtime
responses are final bounded messages; the Telegram adapter also supports explicit
bounded edits. High-frequency model-token edits are not sent.

## Parallel execution and recovery

Two conversations can execute; a third is rejected, not queued. Stop targets
one conversation. Waiting/UNKNOWN reserve their conversation's unfinished Run
but no active model slot. Owner-wide model in-flight capacity is two, including
collaboration children; existing parent cumulative budgets remain in force.
Command quota remains one plus two compatible finite services across the Owner.

Global setting commits, backups and workspace changes require an execution idle
boundary. The proposing Run can commit its confirmed setting only when no other
conversation executes. Permission revocation is immediate. Incompatible sandbox
profiles remain busy; no host fallback is introduced. A short shared file-tool
publication lock prevents two tool instances committing the same old hash.
Commands/external programs have no general transactional file protection.

Messages, deduplication and trigger-to-Run association reuse existing records.
Offline/reconnect backlogs are discarded, not executed. A durable send claim is
saved before dispatch. Unknown sends are not retried after restart and never
replay model/tool work. Only confirmed text enters public delivery mirrors;
partial sends expose only confirmed parts. A dispatching claim after a crash
means UNKNOWN delivery, not success.
GUI/CLI execution evidence displays failed and unknown delivery separately from
the saved Run outcome. No-op compression and failures before Run creation still
produce an explicit channel response without inventing an execution record.

## Official references

Read on 2026-10-29; wire contracts do not establish real account acceptance.

- [Telegram Bot API](https://core.telegram.org/bots/api) and [FAQ](https://core.telegram.org/bots/faq).
- [Official Lark Go websocket implementation](https://github.com/larksuite/oapi-sdk-go/tree/v3_main/ws).
- [Official QQ SDK](https://github.com/tencent-connect/botgo), [webhook](https://github.com/tencent-connect/botgo/tree/master/interaction/webhook) and [signature](https://github.com/tencent-connect/botgo/tree/master/interaction/signature).

Further acceptance needs authorized group targets, two controlled members and
a current Provider for representative group chat/summary, callbacks and media.
The private-text pass does not certify those remaining platform cases.
