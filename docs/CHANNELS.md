# Optional channels

Updated for v8.6. Platform clients run in the optional `freeagent-channel`
process; Core owns admission, deduplication, permissions, context and execution.
Default installations open no platform connection. Lark/QQ retain mock coverage;
new live acceptance is limited to the scenarios recorded in the release report.

## Setup

1. Start the GUI/web/chat Owner. The CLI attaches to the same Owner. Disabled
   connections do no network work; optional transport errors leave local UI usable.
2. Save a channel credential under Settings → Connections → Channels, then
   select it and explicitly enable the connection. Model Provider management
   has a separate namespace and never lists platform protocols. Channel keys
   cannot be model keys. Lark/QQ also require their existing official App ID;
   QQ uses a numeric loopback listener behind your own HTTPS reverse proxy.
3. Telegram: send `/start` privately or `/start@YourBot` in the target group.
   Settings → Telegram shows an eight-character application code and the numeric
   Bot/chat/person identities. Verify them, then click Approve. The code only
   identifies an application; it cannot redeem access or substitute identities.
4. Approval grants chat only. Grant space and person tool permissions separately
   if needed; the effective mode is the lower of both. Increases affect new Runs;
   reductions restrict old Runs immediately. Group admission defaults to specified
   people; explicitly allowing everyone permits all group members to consume
   model quota but grants no tools. Private/group admission does not cross scopes.
5. Applications last one hour, with 32 pending per Owner. Duplicates retain their
   original expiry and applicant notifications are paced at 60 seconds. Owner
   restart/Bot change invalidates applications. Rotating the same Bot's token
   preserves approvals; a different numeric Bot inherits none. Nicknames never
   authorize. Lark/QQ retain their ten-minute one-use `/pair CODE` flow.

CLI (Owner running; --confirm explicitly authorizes the displayed numeric scope):

```powershell
freeagent channel telegram status --data-dir <data>
freeagent channel telegram credential add --data-dir <data> --name <account> --base-url https://api.telegram.org --key-stdin
freeagent channel telegram configure --connection telegram --data-dir <data> --key <channel-credential-id> --app-id <numeric-bot-id> --enabled true
freeagent channel telegram access --data-dir <data>
freeagent channel telegram access --data-dir <data> --request-code ABCD2345 --confirm
freeagent channel telegram access --data-dir <data> --chat-id 123456 --kind private --members 123456 --confirm
freeagent channel telegram access --data-dir <data> --chat-id 123456 --access-enabled=false --confirm
freeagent channel lark configure --connection lark --data-dir <data> --key <saved-id> --app-id <app> --enabled true
freeagent channel lark pair-code --data-dir <data>
freeagent channel qq configure --connection qq --data-dir <data> --key <saved-id> --app-id <app> --listen 127.0.0.1:8098 --enabled true
```

AI proposals use `channels.set_policy` and `telegram.set_access` with existing
administrator confirmation. A model cannot approve its own change. Admission
policy is stored beside the existing external-space binding; no second pairing
store can bypass it. Stop access retains history. Deleting the external space
also revokes access and moves conversations to Local; reauthorization does not
republish that old history.

Desktop/interactive Owners launch the bundled channel process only after a
connection is enabled; stopping the Owner stops that process. On servers, use
the separate `deploy/freeagent-channel.service` after configuring the account.
The generated bootstrap is private, generation-bound and not a database
connection. Progress files contain platform cursors; durable input adoption and
reply receipts remain in Core. Never run the same Bot in two polling processes.

v8.0 uses a fresh PG database. Old histories are archived without import; approve
the numeric Bot/chat/person identities again before exposing the new Bot.

## Running inputs (v9)

Running-task steering uses plain Telegram messages; `/inputs` shows the caller's
inputs. In groups, another admitted member's explicit request uses the existing
20-entry FIFO queue with its own identity/permissions, rather than steering the
first member's Run. A full queue rejects new work and records a diagnostic.
Unadopted inputs do not enter model history. GUI/CLI and channels use the same
TaskService and adoption sequence. Private Telegram steering remains unchanged.
See [continuous execution and context](CONTEXT_AND_CONTINUOUS_EXECUTION.md).
TG multi-account/group changes in this version are simulated, not new live
platform acceptance.

## Commands and routing

`/help`, `/mode` (read only), `/status`, `/stop`, `/continue`, `/abandon`,
`/compact`; organization: `/multi on|off`, `/sessions`, `/new title`, `/switch ID`,
`/default ID`. None may chat and view public state. Organization requires operation permission. Admitted None users may manually
compact their current public conversation. Approval/continue/abandon retain the
original requester; stop permits that requester and currently operational
members. GUI/CLI can continue the same saved Run without changing its requester.

Untriggered group text is discarded before full inbound persistence by default.
Authenticated management can opt a group into 24-hour bounded text-only
background; it is a per-topic ephemeral reference, not transcript, summary or
Memory. Its body is omitted from persistent model Attempt requests. Turning
retention off clears temporary background. Older stored background is excluded
from context and can be explicitly cleared in group settings. Backups follow
their existing retention; already published replies remain ordinary records.
The separate opt-in “reply all” treats admitted member messages as normal
requests and does not enable background retention.

Forum topics use verified is_forum/is_topic_message facts; an ordinary reply
chain's thread ID does not create a topic. General sends omit thread ID.
Conversations, commands, approvals and deliveries retain the originating topic.
Bot/group authorization remains group-scoped. Group-readable resources may be
disclosed to every member; topic context separation is not a privacy boundary.
Migration retains the group space/history; only never-dispatched output with
verifiable references may change target. Old cards/references pause; dispatched
or UNKNOWN operations remain associated with their original target. Member
status updates stop forbidden sends and recheck ordinary-member speaking rights.
Display names, forwards, quotations and summaries are never identity or approval.

Moving a local conversation into an external space requires the existing local
publication confirmation. Visible local text is mirrored once into the public
scope in bounded batches; private tool bodies and synthetic command prompts
remain excluded. Original records are retained.

Callbacks verify the exact confirmation, original requester, target, bot/card
message, current grants, expiry and pending state. A per-Bot numeric confirmer
roster, managed only through authenticated settings, permits private confirmation
of a configured model or one already-selected extension option. It grants no
chat/tool permission. Other settings still use management. Confirmation performs
no extra model request and takes effect next Run. Reconnecting invalidates old buttons; GUI/CLI can handle the
existing pending operation. There are no chat confirmation codes.

## Platform matrix

| Platform | Local implementation | Limits | Real acceptance |
|---|---|---|---|
| Telegram | Bot polling, private/group text, mentions/replies/commands, buttons; bounded media adapters | Only received updates; Privacy Mode applies; edited-input execution unsupported; attachment rules in ATTACHMENTS.md | v7.11 private text PASS; v7.12 new admission, groups, second account, media and callbacks NOT_RUN |
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

## Runtime clarity, compaction and delivery in v7.12

Admission is checked before history/media/model work, callbacks, command launch
and outgoing results. A policy revision invalidates old callbacks and frozen
requests even after reauthorization. Revocation cancels active requests and
requests command termination; prior effects remain in the evidence. `/status`
does not publish undelivered administrator results. Full is the service OS
user's authority, not root. `/mode` distinguishes next-request limits from the
latest frozen Run, including its original requester and effective backend.

New installations enable automatic compaction with a 4096-byte cap. Existing
missing/zero settings stay off. Settings → Context compression controls the same
`task_summary_bytes` value as the confirmed `runtime.set_limits` operation:
0 disables automatic compaction; 1–16384 enables it. Compression occurs before
an over-limit model request, uses the Run's finite budget, retains originals,
and shares no private tool history with channels. `/compact` works for admitted
None users, without enabling tools or changing this automatic setting.

`file_export` immediately creates an authenticated download card. Telegram sends
the selected file when the Run ends or reaches a limit; no repeated export or
poll is required. The receipt determines sent/failed/UNKNOWN. Identical exports
reuse the snapshot; changed content selects the latest unsent version of that
source. Dispatched/UNKNOWN versions are frozen. Multiple versions require an
explicit choice. File replies need no hashes in the prose or file body.

An ordinary settled limit retains status `limited`, reason and consumption,
releases conversation occupancy and delivers existing files. Only an explicit
whole “继续”, `/continue` or Continue button creates one linked next Run. Saved
tools are not requeued. Pending/UNKNOWN calls and background commands prevent
settlement; collaboration children cannot escape the parent's budget. A manual
increase of the original Run's limit can still resume that Run without resetting
its consumption. An interrupted send never retries model or tool execution.

## Telegram expression (v8.3.1–v8.5)

Telegram text is rendered from Markdown into native text/entities, with
UTF-16/grapheme-safe fragments and frozen per-fragment receipts. Code stays
literal. A definite formatting rejection may fall back to plain text; a
timeout/UNKNOWN never authorizes a resend. `plain_text: true` disables native
formatting. Private running requests refresh short-lived typing; approvals,
stops and expired Core leases end refresh. `disable_typing: true` disables it.

`reply_stickers` explicitly enables model sticker expression.
`autonomous_reactions` explicitly permits a reaction on the current incoming
message; both default false. None can use this scoped communication tool
without gaining command, file or arbitrary network access. A different known
message target always needs model confirmation. Direct user selections in the
conversation panel use the same verified Core resources.

The sticker panel reads a named pack or `https://t.me/addstickers/NAME`,
pages 24 at a time, previews/selects stickers, edits the space's saved packs,
and sets/removes a reaction on a verified message. Saving pack names is a
confirmed `telegram.set_sticker_sets` change (at most 32 sets), shared only
within that space. Received/explicit lookup references expire in 24 hours;
saved-collection references also require the current authorization generation.
Removal/re-addition does not revive revoked references. Bot file IDs and
token-bearing URLs are not model-selected targets.

CLI `stickers list|sets|targets|preview|send|reaction --session CHAT_ID` exposes
the same operations. `--saved`, `--set`, `--offset`, `--resource` and
`--message-ref` select the exact scope. Persistent editing uses generic
`control plan --kind telegram.set_sticker_sets` followed by confirmation.

Static WebP is bounded and converted to PNG. TGS/WebM retain their original
sendable resource while vision sees only an available thumbnail. Without
vision the model gets accurate metadata, not an invented image/animation
description. Pack sharing sends its addstickers link, not every sticker.
There is no installed-pack discovery or background indexing.

Identified reactions replace the prior person's fact. Anonymous updates store
absolute counts without inventing a person. Neither starts Runs or changes
relationships; platform permissions determine which events are received.
Custom/paid reactions may be recorded as received counts but are unsupported
outgoing choices.

See [reply delivery](REPLY_DELIVERY.md) for ordering, cancellation, UNKNOWN and
optional one-time follow-ups, and [experience summary](EXPERIENCE_SUMMARY.md)
for the optional `/analyze` source/draft flow. These work independently of
AffectWeave; character expression policy remains that extension's job.

## Preparation and protocol revision 4

Core and `freeagent-channel` must be updated together. The authenticated connection registers its v4 action and format declaration before accepting input or claiming delivery. Unsupported actions, topics and buttons do not acquire permission through the declaration. Lark declares text, files and its existing buttons; QQ declares text only. Telegram formatting, topics, stickers and reactions are owned by the platform adapter and its host-side integration. The model/tool loop uses the shared TaskService.

Text preparation is pure local conversion. Its result is checked against the original source hash, target/connection and finite bounds before Core freezes the whole group. A lost preparation receipt may be recomputed for that same input. Sending uses only frozen parts; its uncertain result is UNKNOWN and is not automatically resent. Cancellation during preparation prevents a late result from reviving the reply. Generated output, preparation and confirmed delivery are separate states.

A confirmed Telegram rate rejection with `retry_after` delays new delivery to that Bot/chat, including other topics in the same group, without delaying unrelated chats. The outbox stores the deadline across restarts. If it extends past the original authorization, that delivery stops instead of sending early.

For Telegram ingress, only a definitive receipt advances an update submitted to the Owner. A malformed route that was rejected before execution gets a durable, redacted refusal record. Network errors or unconfirmed receipts keep the cursor in place and back off. The connection's reception diagnostics separate an online transport from the age and retry count of the current waiting update; a quiet Bot without a waiting update is not stalled.

The default Core dependency check is `python scripts/check-core-boundaries.py`. No configured channel means no platform connection or optional adapter process. `/analyze` is a Core source-analysis command: use an installed, enabled method explicitly authorized to this space; the transport owns no extra summary workflow.
