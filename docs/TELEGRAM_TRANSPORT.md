# Telegram transport contract — local implementation

Access date: 2026-10-28. This documents a thin adapter and its local HTTP
fixtures. No real Bot credential, private chat or group has been tested.
The adapter is now owner-wired only when the saved channel setting is enabled; a disabled setting performs no network call.

## Official contract used

- [Bot API: getUpdates](https://core.telegram.org/bots/api#getupdates):
  positive offset acknowledges earlier updates; offset -1 selects from the
  queue tail and forgets previous updates. This permits an explicit startup
  discard barrier, not replay of offline requests. Receive at most 100 updates,
  and request message/callback_query types explicitly. Previously created
  unwanted update types may still arrive and must be ignored.
- [CallbackQuery](https://core.telegram.org/bots/api#callbackquery) and
  [InlineKeyboardButton](https://core.telegram.org/bots/api#inlinekeyboardbutton):
  the clicker is the platform event's from user, never a person claimed in
  callback_data. Data is 1–64 bytes. Data may not correspond to a current button,
  so the core must verify the exact pending action. Inaccessible/inline
  messages do not supply an ordinary chat-message approval target.
- [sendMessage](https://core.telegram.org/bots/api#sendmessage):
  text is bounded at 4096 characters. The adapter uses conservative UTF-16
  chunks, plain text, disabled link previews and no paid broadcast flag.
- [ResponseParameters](https://core.telegram.org/bots/api#responseparameters):
  retry_after is retained as a bounded delay. Transport uncertainty is retained;
  the adapter never retries a send or repeats any model/tool work.
- [Official FAQ: message visibility and limits](https://core.telegram.org/bots/faq):
  Privacy Mode and administrator state affect ordinary group-message delivery;
  bots do not receive other bots' messages. The documented free limits include
  roughly one message/second per chat, 20/minute in groups and 30/second for
  broadcast. The shared sender applies bounded pacing.

These are platform documentation claims, not observed live account behavior.
The configured app's actual visibility and limits remain NOT_RUN.

## Local adapter boundaries

The client accepts the official HTTPS origin or an explicit loopback fixture.
Token-bearing request URLs and upstream descriptions are not included in
errors, redirects are not followed, and bodies/batches/buttons are bounded.
Replies and callbacks return delivery uncertainty separately from execution.

Events retain stable chat/person/message IDs and message time. Anonymous
sender_chat is not granted the incidental from user's identity. Private text
triggers a response; groups require a valid command, Bot mention or reply to the
Bot. Mention ranges use Telegram's UTF-16 offsets. Edited messages and unsupported
chat types are not converted into new execution requests.

The owner integration uses the existing KeyStore, TaskService, Store and
control journal. It provides in-memory pairing codes, two-layer authority,
nullable background message provenance, atomic receipt/Run association, public
background projection, exact callback approvals, frozen recipients and a startup
offline discard barrier. Delivery remains uncertainty-aware and is never retried
by replaying model or tool work. The shared sender applies bounded pacing and
durable pre-send claims; no real account acceptance has been performed. See
[Channel setup](CHANNELS.md).
