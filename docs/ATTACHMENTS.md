# Attachments (v7.9)

v7.9 uses **Harness-v8 / config and SQLite schema 10 /
freeagent-harness-backup-v4**. These identifiers were checked against the
repository before selection. It does not migrate v7.8 data. Keep the previous
program and data directory together; never point an older program at this one.

GUI: use the file button or drop files on the composer. The uploaded file card
belongs to that conversation; removing a draft card does not erase submitted
history. Submit with a prompt. Download published files from their cards. Use
“另存为 Markdown” on an assistant reply, including in a None space.

CLI (explicit client-local paths, including when attached to a server Owner):

```sh
freeagent run --session CHAT --attachment '/local/input.md' --request INPUT-1 'Read this attachment'
freeagent attachments list --session CHAT
freeagent attachments upload --session CHAT --file '/local/input.md' --request UPLOAD-1
freeagent run --session CHAT --attachment-id ATTACHMENT_ID 'Read this attachment'
freeagent attachments save-reply --session CHAT --run RUN --request SAVE-1
freeagent attachments download --session CHAT --id ATTACHMENT_ID --out '/local/new-result.md'
```

`chat`/attach also supports `/attach <local-path>`, `/attachments` and
`/save-reply`. A client selects a file; no upload endpoint accepts a server
filesystem path. Download never silently overwrites an existing file.

Inputs support NUL-free UTF-8 text/code and verified PNG/JPEG. Defaults: 8 files,
32 MiB per submission, 8 MiB per input, 16 million pixels, 32 MiB per export,
8 export references per Run, 1 GiB per instance, 24-hour unsubmitted retention.
At most 10,000 stored files are allowed. Unknown file bodies are never executed,
unzipped or OCRed. Text exceeding the request context budget is refused with an
explicit excerpt/smaller-file request, never silently truncated.

The `file_export` tool snapshots only FileBroker-authorized regular files,
requires the existing permission/approval policy, and checks its prepared hash
again before publication. Changing its source invalidates approval. Publication
and transport delivery are recorded separately. HTML/PDF/archives/Office are
allowed result downloads; only validated PNG/JPEG can be previewed inline.

Settings → attachments changes format switches and finite limits through
`attachments.set_policy`; the CLI/AI use the same control operation. Permanent
removal uses `attachments.delete` with exact `conversation_id`, `id`, `sha256`;
its preview reports affected message references. A model proposal always needs
control confirmation. Data-file removal cannot run while that conversation has
an unfinished Run. Backup contains referenced bytes and their hashes; missing
or changed blobs make verification/restore fail.

Images require explicit local vision declarations, configured by each model's
“图片能力” button, `model.set_profile`, or `config model --vision-config`.
No model-name guesses, silent resizing or Provider substitution are used.
The native Anthropic Messages hard bounds also apply: 10 MB encoded per image,
8000 px per side, or 2000 px per side when a request contains over 20 images.
Attachment turns currently require collaboration off; incompatible selections
are rejected with instructions instead of silently changing the execution mode.
Recent images are resent under count/encoded-byte/side/request budgets. Older
images omitted from a request retain their reference and produce a UI notice;
select the original again to re-examine it. Base64 is dispatch-only and is not
stored in SQLite, events or logs. Original references remain after compaction.

Channel transports are previews. Telegram/Lark download only authenticated
event-bound platform file IDs after a trigger, with fixed origins and no
redirects; background group events store metadata only. File deliveries retain
the original target and per-file sent/failed/UNKNOWN receipts; UNKNOWN is never
resent automatically. `/save-reply` exports already public reply text even in
None. QQ C2C/group generic file upload is NOT_SUPPORTED by this implementation:
the official botpy API marks file type 4 as not open; its image API requires a
public URL, and FreeAgent does not publish anonymous attachment URLs.

Protocol fixtures are separate from real visual understanding or real channel
accounts, which require current credentials and authorized test recipients.

References checked 2026-11-03:

- [OpenAI image input guide](https://developers.openai.com/api/docs/guides/images-vision): Chat `image_url` and Responses `input_image`, including Base64 data URLs.
- [Telegram Bot API](https://core.telegram.org/bots/api#getfile): event-bound getFile/download and sendDocument.
- [Lark official Go SDK](https://github.com/larksuite/oapi-sdk-go/blob/v3_main/service/im/v1/resource.go): message resource download and multipart file upload.
- [QQ official Python SDK](https://github.com/tencent-connect/botpy/blob/master/botpy/api.py): C2C/group media API and currently documented file-type restriction.
