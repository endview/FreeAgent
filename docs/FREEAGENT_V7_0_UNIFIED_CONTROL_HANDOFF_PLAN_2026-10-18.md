# FreeAgent v7.0 Unified Control And Handoff Plan

Date: 2026-10-18

Status: implementation baseline after architecture review

This document is the v7.0 implementation contract. It defines what is
allowed, what must be proved on native Windows, and where the release stops
when a security or recovery claim cannot be demonstrated. It is a plan, not a
claim that the implementation already exists.

## 1. Version Positioning

FreeAgent v6.x provides the lightweight execution foundation:

- one local Harness core;
- persistent conversations, Runs, Attempts and Messages;
- bounded file, command, HTTP, MCP, memory and knowledge tools;
- exact approval and UNKNOWN semantics;
- execution profiles and permission modes;
- model selection, collaboration and Provider adapters;
- local CLI, Chat UI and desktop shell;
- Responses and explicit server-side web search support where a Provider
  advertises it.

v7.0 makes that foundation operable from every local product surface. It
centralizes configuration control, gives GUI and CLI an authenticated attach
path to one running engine, and makes Windows command execution usable under a
verifiable OS-enforced sandbox.

v7.0 remains a lightweight Harness release. It does not introduce a general
workflow platform, a distributed worker system, a second persistence model or
a module lifecycle platform.

## 2. Product Contract

The product contract is:

One Harness core, multiple shells, every persistent operation available through
an explicit AI conversation, with the same records, approvals and recovery
semantics in GUI and CLI.

The user can ask the conversation to:

- enable command_run under a selected sandbox profile;
- allow a named executable in the current workspace;
- allow a conversation to read a selected root without allowing writes there;
- select a Provider, model, protocol or Responses capability;
- disable network access for future Runs;
- continue a CLI conversation in the desktop app;
- inspect an active CLI Run from the GUI and request cancellation;
- create, inspect or withdraw a pending configuration proposal.

The result must come from the Harness control service and persisted records.
Model prose cannot claim that a setting changed. The product surfaces read
the applied result from the core after the operation has been committed.

## 3. Locked Decisions

These decisions are product requirements.

### 3.1 Confirmation

Every model-proposed persistent control-plane mutation requires explicit user
confirmation.
This includes a reversible title or model change and an authority-expanding or
destructive change. Natural language may authorize the model to prepare a
proposal, but it cannot apply the proposal.

The same control-plane proposal can be confirmed through:

- a Chat UI confirmation card, by clicking Apply or Reject;
- a CLI confirmation prompt.

Confirmation typed into the conversation was removed in the October 19
review: chat text always goes to the model as ordinary user text. Both
surfaces call the same core operation. They do not implement their
own risk logic. A model tool can create, inspect or withdraw its own control
proposal, but cannot confirm or reject it. Ordinary file, command, HTTP and
MCP approvals continue through the existing ToolRunner approval path.

### 3.2 Attach

GUI and CLI use attach mode when an engine already owns the current data
directory. An attached client is a full user surface: it can submit new Runs,
observe events, confirm user-visible proposals, request cancellation, and
perform allowed settings operations through the owner control API.

The second caller never opens a competing Store writer. A one-shot command that
cannot attach reports the owner and protocol error. It does not silently open
a read-only view while claiming that a mutation succeeded.

### 3.3 Windows command execution

v7.0 includes a Windows sandbox backend. command_run must work under a bounded
profile without requiring Full access. The backend exposes its actual
enforcement state and fails closed when it cannot enforce the selected policy.

The v7.0 bounded backend is an AppContainer worker with explicit capability
and filesystem grants. Full access is a separate explicit host mode. Full
access is never a fallback for sandbox setup failure.

Full access with host explicit execution is a trusted same-user mode, the
same trust level as running the command in the user's own terminal. It is not
a sandbox and is not a control-plane security boundary. Its rules are:

- it is never a default and is not copied to new conversations;
- it is granted per conversation and stays in that conversation's permission
  setting until the user changes it;
- the confirmation surface states plainly that host commands run as the
  current Windows user and can read stored Keys, change FreeAgent settings
  and data, and read or change any file that user can access;
- once granted, command_run under Full access does not ask per command, so
  the model can actually perform the requested host work instead of refusing;
- the model capability description reports Full access as active only after
  the grant is recorded in the current Run snapshot.

## 4. Non-Negotiable Boundaries

- Keep one composable lightweight Harness core.
- GUI, CLI and local callers use the same TaskService, Store, ToolRunner,
  model adapters, approval path and runtime policy.
- Every persistent product setting has a typed control operation and an AI
  conversation path.
- Every model-proposed persistent control-plane change requires user
  confirmation.
- A user-operated direct change confirms itself when its own effect is the
  requested ordinary change. Authority-expanding or destructive direct changes
  require an explicit confirmation step.
- Configuration changes never rewrite the frozen snapshot of an active Run.
- Existing UNKNOWN results are never implicitly replayed.
- Reuse Run, Attempt, Message, Conversation, evidence and configuration
  metadata as the persistence boundary.
- Do not create independent Task, Step, Checkpoint or Evidence subsystems.
- Do not restore old platform compatibility or add a migration layer.
- Use a new current-only data directory when a format revision is required.
- Do not add automatic RAG, hidden memory learning, implicit personality
  learning or background knowledge governance.
- Do not add multi-tenancy, organization permissions, billing, account pools,
  plugin marketplace or distributed workers.
- Do not add automatic Provider failover after an uncertain call.
- Do not treat model prose, summaries or settings claims as runtime evidence.
- The Harness never injects a secret into model context, tool arguments,
  command arguments or environment. Host explicit mode is the trusted
  same-user mode of section 3.3 and does not isolate files or local services
  that the user's processes can read or change.

## 5. Baseline Facts And Release Gates

The implementation starts with a recorded v6.2 baseline. The following facts
are current inputs to the plan:

1. The restricted-token command worker currently rejects network modes other
   than host_inherited. This is not a complete filesystem or network sandbox.
2. Non-Windows sandbox behavior is unsupported and must report NOT_SUPPORTED.
3. apply_patch is not currently a built-in tool in the internal catalog. It
   is a v7.0 product limitation and moves to v7.2.
4. Responses and search have fixture coverage and still need a separate live
   Provider acceptance report.
5. VERSION is v6.2.0-dev.1 while some release documents still say v5.5.
   The release documentation gate must remove this drift.
6. The store has one active Run index per conversation. The relevant statuses
   are running, waiting_confirmation, completed, stopped, unknown, limited,
   failed and abandoned.
7. Attempts already support model, tool and workflow kinds with request and
   response JSON. A model control proposal is stored as a tool Attempt; a
   direct control action is recorded in a bounded control journal attached to
   configuration metadata. No standalone operation table is required.
8. OpenStore takes owner.lock. OpenStoreReadOnly does not take that lock and
   cannot be used as a mutation path.
9. The desktop currently starts CLI subprocesses for config show and workspace
   switching. Those calls must go through the owner control API after attach.
10. The Chat UI is loopback-only, checks host and Origin, and accepts a
    capability header. The current listener limit is eight and must be scoped
    or raised for attach.
11. Key profile updates clear LastCheck when the base URL or protocol changes,
    but the stored Key may remain. v7.0 must add persistent origin binding and
    hidden re-entry.
12. Skill trust is currently workspace-level and extension processes can be
    launched from workspace-managed content. v7.0 must bind trust to content
    hashes and protect those paths from sandbox writes.
13. command_run, MCP stdio, Extension workers and the desktop engine currently
    have start paths that can attach the Job after process start. v7.0 must
    close that race for host and AppContainer launch.

The v7.0-0 gate records each fact with a commit, test result or explicit
blocker. Later packages do not silently absorb a failed prerequisite.

## 6. Target Architecture

### 6.1 Core control service

Add one internal control boundary above existing configuration APIs:

- ReadControlState
- ListControlOperations
- PlanControlChange
- ConfirmControlChange
- ApplyControlChange
- RejectControlChange
- RevertControlChange

This is a core service, not a GUI service or a CLI service. Existing GUI HTTP
endpoints, CLI commands and AI control tools become adapters over this
boundary. They do not keep separate mutation or risk logic.

The service owns:

- typed operation validation;
- current resource revisions;
- deterministic risk classification;
- canonical operation digest creation;
- confirmation binding;
- per-resource application ordering;
- partial-result reporting;
- change journal and revert metadata;
- redaction before any result reaches the model.

### 6.2 Operation record

An operation contains:

- operation_id;
- operation kind and typed arguments;
- authorization source derived by the caller boundary;
- affected resource IDs and resource revisions;
- old and proposed redacted values;
- risk class;
- user-facing preview;
- canonical digest stored by the core;
- creation and expiry time;
- status view derived from the existing Attempt and Run records: pending,
  applied, rejected, stale, partially_applied or failed;
- revert class and revert metadata reference;
- per-step results and recovery instruction.

The operation is represented by an existing tool Attempt or configuration
metadata record wherever possible. For a model proposal, pending means a
pending tool Attempt on a Run with waiting_confirmation. A bounded journal
file in the data directory may hold user-direct control records and small
per-step metadata.
The journal is append-only, size-bounded, checksummed and rotated. It is not
a new Task, Step, Checkpoint or Evidence subsystem.

### 6.2.1 Control plane and tool plane

The control plane contains persistent FreeAgent product settings and
authority changes: Provider and model bindings, permission and sandbox
profiles, roots, network policy, MCP/Skill/Extension lifecycle, memory
selection and other catalogued configuration. Model-proposed changes in this
plane use the control proposal and user confirmation contract.

The tool plane contains file, apply_patch, command, HTTP, MCP and other
execution calls made by a Run. These calls continue through ToolRunner's
existing effect, permission profile, approval, cancellation and evidence
paths. A model-generated apply_patch is therefore a normal tool call; it does
not receive a control confirmation code. Full access and Workspace behavior
remain those defined by the selected permission profile.

The control service does not replace tool approval. It provides the typed
control operation that changes the policy used by later tool calls. A model
control proposal is always recorded as a tool Attempt and enters the existing
Run approval state described in Section 8.3; no second proposal lifecycle is
introduced.

### 6.2.2 Control decision and host execution gate

The owner keeps one small in-memory gate with two mutually exclusive holders:

- control_decision: a short critical section that covers verifying and
  committing a Confirm, Apply or Revert whose effect expands authority,
  deletes data or is irreversible;
- host_execution: a host explicit command is running under the current user's
  identity, from launch until its process tree has exited or been killed.

The gate is owner-wide because a same-user host command started in one
conversation can affect a proposal in any conversation. It covers the
decision, not the waiting period:

- a pending proposal holds nothing, so a proposal waiting for the user never
  blocks host commands in any conversation;
- gated decisions return host_execution_busy while any host command runs;
  the proposal stays pending and the card or prompt shows the reason, so the
  user can wait or stop the command;
- ordinary changes, changes that only reduce authority, rejection and
  cancellation never use the gate, so the user can always revoke Full
  access, turn off network or stop a command immediately;
- a host explicit command that arrives during a control decision waits for
  that short commit and then launches;
- creating a proposal and ordinary direct changes do not use the gate; a host
  command that changed a target before the decision is caught by the
  revision check and section 11.2.1.

Checking and acquiring the gate is one owner-serialized operation. The gate is
never persisted. Pending proposals survive an owner restart as
waiting_confirmation Attempts, but they do not hold the gate. A restarted
owner starts with an empty gate because the previous generation's host
process trees ended with its Job objects. It runs the section 11.2.1 check
before the first decision, and a detected change makes Confirm return
external_change_review_required until the user reviews it.

AppContainer commands are not part of this gate because their profile denies
the data directory and attach credentials. The gate is defense in depth
against honest mistakes and accidental interleaving, but it is not a security
boundary for same-user processes that can directly edit files, inject into
GUI processes, or read owner state from disk or process memory. The security
boundary exists only at the AppContainer profile.

MCP stdio and Extension workers that run as host processes are long-lived, so
they do not hold host_execution; otherwise a running server would block every
gated decision. They are covered by workspace-level trust in section 11.4 and
by the change check before each new Run in section 11.2.1.

The gate does not change the ordinary Full, Workspace or Ask approval rules
for tool calls. A rejected decision changes nothing and leaves the proposal
pending. A host command that
finishes releases host_execution even when its result is UNKNOWN. A host
command never receives interactive_control_capability, and an attach token
read by a same-user host process cannot apply or confirm a persistent control
operation through the attach API.

### 6.3 Authorization source

The executor distinguishes:

- user_direct: an operation initiated by a user-operated GUI or CLI control;
- model_proposed_confirmed: a model-created proposal confirmed by the user
  through a card or CLI prompt;
- system_recovery: a narrowly defined owner-safe recovery action that cannot
  expand authority or dispatch a new external call.

The caller boundary assigns the source. It is never accepted as a model
argument or inferred from attach.secret alone. A model tool can request a
proposal and read its redacted result, but cannot provide the user
confirmation.

Persistent Apply, Revert, Confirm and destructive direct operations require
interactive_control_capability issued by the owner after a GUI gesture or an
interactive CLI confirmation step. It is memory-only, short-lived, bound to
the local user, frontend process, conversation and operation, and never
written to disk, a URL, environment, command argument or model context. It is
defense in depth against well-behaved tools and cannot stop a same-user host
process from injecting into the frontend, driving the GUI or reading the
operation state from process memory. The boundary exists at the AppContainer
profile.

attach.secret alone can observe state, submit a task, request cancellation
and read a redacted plan; it cannot create a pending operation or establish
user_direct or model_proposed_confirmed authorization. The owner rejects
control mutation requests from a host command or a token-only attached client
with user_interaction_required.

Risk classes are fixed in the core:

- ordinary: reversible future-only settings, such as title or model choice;
- authority: permission, sandbox, roots, network, tool enablement and trust;
- destructive: deletion, data clearing, workspace removal and Run abandon;
- irreversible: an external effect or secret deletion for which the Harness
  has no exact reverse operation.

Model-proposed changes in every class require the control confirmation. Direct
ordinary changes use the user action as confirmation; direct authority,
destructive and irreversible changes require the second explicit prompt. The
preview states the class, exact target, affected scope and revert class.

### 6.4 Revisions and partial application

Revisions are tracked per resource family. They are not represented by one
global revision that falsely promises cross-store atomicity. Families include
configuration, runtime policy, key profiles, model bindings, memory metadata,
MCP and extension metadata, and external integration settings.

Before each step, the executor checks the expected resource revision. A step
that observes a changed revision returns stale and stops later steps unless
the operation declares a safe independent step. The result identifies every
completed and skipped step.

A compound operation spanning config.json, keys.json, SQLite, MCP, Skill or
Extension state can return partially_applied. It is never reported as fully
applied when only part succeeded. Each successfully changed reversible value
has a journal reference to an exact typed snapshot or canonical file bytes,
protected by the data-directory ACL and user-bound OS encryption. A
redacted value is used for display only and is never presented as a revert
snapshot. If an exact protected snapshot cannot be captured, the operation is
classified non-revertible before apply. Secret values are never copied into
the journal; secret changes require hidden re-entry to recover.

### 6.5 Revert and irreversible actions

Revert is a new confirmed control operation. It is allowed only when the
resource revision still equals the revision recorded after the original
operation. If a newer change exists, Revert returns stale and changes
nothing.

Revert metadata is stored in the bounded journal or existing configuration
metadata. It contains the resource revision, old value class, an integrity
hash and a protected exact-snapshot reference where needed. It does not
contain Provider Keys or other plaintext credentials.

The operation catalog marks irreversible actions explicitly. Deleting a Key,
deleting memory content, deleting extension data, and abandoning an UNKNOWN
external effect have no revert operation. The UI, CLI and preview state this
before confirmation. Recovery means re-entry, restore from a user-held backup,
or a new Run; the core never invents a reverse action.

### 6.6 Secret and origin binding

AI can prepare a Key profile or request secure Key entry, but never receives a
Key value. A resolved secret is persistently bound to:

- normalized base origin;
- protocol;
- source kind, such as hidden user entry or environment variable;
- profile revision.

Changing the origin or protocol invalidates the resolved secret. A hidden
user-entry source requires hidden re-entry, and an environment-variable source
requires explicit re-bind confirmation. Testing with a stale secret is
rejected before any network request.

A stored secret is protected with the current user's DPAPI. The protected
payload embeds the normalized origin, protocol and profile ID next to the
secret, and the owner compares them with the current profile after
decryption. A mismatch blocks resolution before any network call and returns
origin_mismatch. Editing base_url or protocol in config.json outside the
control service therefore cannot redirect a stored Key. An
environment-variable source stores no secret; its binding record carries the
same fields.

This binding stops redirection by configuration edits. It does not stop a
user-authorized host command from decrypting keys.json directly, because
DPAPI does not separate processes of the same Windows user.

Provider redirects remain blocked by CheckRedirect returning
ErrUseLastResponse in every Provider and HTTP probe client. A regression test
must assert that a redirect cannot move a Key to another origin.

The Harness never places secret values in model context, control previews,
operation arguments, Attempts, events, logs, command arguments, URLs or
injected command environment. A host explicit command runs as the current
user and may read user-accessible files or call local user services; this is
shown as a residual risk and is not claimed as sandbox isolation.

## 7. AI Conversation Control Surface

### 7.1 Bounded read state

The model may request a redacted control state containing:

- current permission and execution profile;
- enabled tools and effective readiness;
- workspace identity and redacted allowed roots;
- command policy and executable readiness;
- network mode and redacted allowed origins;
- model pool, default model and protocol capabilities;
- MCP, Skill and Extension status without secrets or raw arguments;
- memory and knowledge selection summary;
- collaboration and Auto settings;
- current Run, pending approval, UNKNOWN and recovery state;
- attach owner, observer status and engine generation.

Absolute paths, environment values, credential material, raw MCP arguments,
private command-line arguments and unrelated workspace contents are redacted
or omitted according to an explicit sensitivity class. A remote Provider
receives only the bounded representation required for the requested decision.

### 7.2 Operation catalog

The catalog covers all persistent product settings, including:

- model selection, default model and Auto selection;
- Provider base address, protocol, model and capability declaration;
- Key profile creation, update, test, model discovery and removal;
- secure Key re-entry through hidden GUI or CLI input;
- workspace selection;
- file scope, per-root access mode, additional roots and blocked paths;
- permission mode, sandbox mode and Full access confirmation;
- file, command, network and memory tool switches;
- command execution mode, executable allowlist, rules, timeout and output cap;
- network mode, origins and loopback policy;
- MCP server add, edit, enable, disable, restart and trust settings;
- Skill import, enable, disable, trust and removal;
- Extension import, enable, disable, trust, restart and data clearing;
- memory and knowledge selection and lifecycle actions;
- collaboration mode, authorized models, tools, paths and finite budgets;
- Auto routing pool and assessment settings;
- recap, compaction and Run limits;
- conversation title, model, permission selection and pin state;
- session resume, attach, safe branch creation and revert.

The following operations are user-only at apply time. They are still reachable
from an AI conversation through a non-mutating user-control request rendered
by the card or CLI prompt:

- confirm a pending model proposal;
- reject a pending model proposal;
- apply a user-direct authority change after its explicit prompt;
- cancel a Run;
- abandon an UNKNOWN Run;
- delete a Key, memory or extension data;
- grant Full access or host command execution.

The model may create, inspect and withdraw its own pending proposal. It may
ask the user to confirm, reject, cancel or abandon, but its tool call cannot
perform any of those user-only actions. The user-control request carries no
trusted action token and is applied only by the user boundary.

The catalog is generated or checked against persistent config fields, GUI
mutation endpoints and CLI mutation commands. A coverage test fails when a
new persistent setting has no typed control operation and no AI conversation
path.

### 7.3 Control-call exclusivity

When a model response contains a control proposal ToolCall, that response must
contain exactly one ToolCall and no other tool call. The check runs after the
model response is validated and before any Tool Attempt is created or
dispatched. Two control proposals in one response are also rejected.

A mixed response is a certain failure, not UNKNOWN, because nothing was
dispatched. The current invalid-response path calls markUnknown, see
service.go validateResponse handling; v7.0 adds a separate branch for this
check that uses the existing certain-failure path, failModel, with the error
control_call_not_exclusive. It must not reuse ErrModelCapability, which leads
to UNKNOWN.

The branch creates no pending tool Attempts, dispatches no file, command,
HTTP, MCP or control operation, and does not partially execute the response.
The model Attempt and the Run fail deterministically with
control_call_not_exclusive. The rejected ToolCalls stay only in the
diagnostic record and are not added to conversation history, so the next Run
sends a protocol-valid history without unanswered tool calls. The user sees
the error, and the next Run's model context receives a short structured
notice explaining that a control call must be the only call in its response.
No in-Run retry is added. The Run is never marked UNKNOWN for this reason.

This prevents approval of a control change from settling the Run while a
second pending tool call remains unprocessed.

### 7.3.1 Control tool scope

Control proposal tools are exposed only to the top-level foreground Run of a
user conversation. They are not exposed to collaboration children, workflow
steps, scheduled or background Runs, Extensions, MCP servers, Skill scripts or
any child model. Exposure is decided when the Run snapshot is frozen, so a
child Run's tool list never contains a control tool. Read-only state tools
follow the same rule unless a later plan adds a narrower child read scope.

A control ToolCall from a Run that did not expose control tools is handled
like any call to an unexposed tool. It is a certain, not-dispatched failure,
creates no pending Attempt and never becomes UNKNOWN.

As a result, a child Run never enters waiting_confirmation for a control
proposal and never completes with control_change_applied_next_run.
TaskService.Confirm never routes a control decision through
continueComposition for a controlled parent. A child that needs a setting
changed reports that need in its result, and the top-level Run may propose
the change itself.

### 7.4 Result redaction

The model receives an opaque proposal reference, redacted preview, expiry,
status and error code. It never receives the confirmation code, the raw
canonical digest, a secret, or a card action token. The model can tell the
user that a proposal is waiting, but cannot reproduce the value needed to
confirm it.

User-visible cards and CLI prompts are rendered from core operation records.
Model markdown cannot create a trusted button or confirmation card.

### 7.5 Capability honesty

The model system context and advertised tool list are generated from the
effective Run snapshot and current readiness state. They include whether
command_run is enabled, which backend is ready, which roots are allowed, and
whether a control proposal can be requested. A model must not claim that it
can use a tool whose readiness is disabled, unsupported or blocked.

When a user asks to inspect a path outside the allowed roots, the model can
request a control proposal for an exact user-selected root. A broad drive
root is not implicitly granted. If command_run is unavailable, the model
reports the readiness reason and does not suggest that changing chat wording
will enable it. Native acceptance includes an outside-workspace path request,
an allowed-root request and a host explicit request.

## 8. Confirmation Protocol

### 8.1 Proposal

For a model-proposed control-plane mutation:

1. The model requests a typed preview.
2. The core validates the target and reads current resource revisions.
3. The core creates an opaque operation reference and a canonical digest.
4. The core creates a default ten-character uppercase Crockford Base32 code.
   The code is generated from a cryptographically secure random nonce and is
   verified online with a per-owner in-memory HMAC key.
5. The core stores the nonce, expiry, operation binding, surface binding,
   attempt count and code epoch, but never stores the rendered code.
6. The card and CLI prompt display the redacted preview and code.
7. The model tool result contains no code, raw digest or action token.

The code is ten characters by default and may be eight to ten characters only
when the same online rate limit is enforced; authority-expanding changes use
ten. It is short for human entry, not a standalone bearer credential. The
owner key, operation revision, authenticated local-user principal, expiry and
retry limit are all required for acceptance.

The code appears only in the authenticated card or CLI prompt rendering
response. It is never written to a transcript, tool result, model context,
ordinary event payload, log or URL. A page refresh in the same owner
generation re-renders the same code for the same authenticated local-user
principal and conversation. After an owner restart, the old code epoch is
invalid and the first render issues a new code.

The surface binding is the tuple of owner generation, authenticated local-user
principal, conversation ID and operation ID. It excludes the GUI or CLI
surface type as well as a transient page or tab ID. Thus the same local
user can see a proposal in the GUI and type its code in the CLI prompt, while
another user or conversation cannot reuse it.

Only one code epoch is active for an operation at a time. Rendering the
pending proposal on any authenticated surface in the same owner generation
preserves the code until it is used, rejected, expired or the operation
revision changes. An owner restart invalidates the epoch and the first render
issues a new code.

### 8.2 No typed chat confirmation

v7.0 has no chat interceptor. The GUI card is decided by clicking Apply or
Reject; the CLI prompt accepts y or the displayed code and allows three
entries per prompt. Chat text is never treated as a confirmation, so it never
needs to be hidden from the model or the transcript.

While a proposal is pending, sending a new message uses the existing
end-and-send action. Ending the waiting Run, from the GUI, the CLI or
end-and-send, withdraws its pending proposal with error code run_abandoned in
the same TaskService.Abandon call. A card or CLI prompt left open afterwards
cannot apply it, and a proposal is never discarded without a journal entry.

After five failed code verifications or ten minutes, the code epoch is
invalidated but the unchanged proposal stays pending; rendering it again
issues a new code. A new proposal is required only when the target or
resource revision has changed.

### 8.3 Existing Run approval path

Control proposals reuse the existing tool Attempt, Run status
waiting_confirmation, TaskService.Preview and TaskService.Confirm path. They
do not add a second proposal state machine or confirmation table. The control
request is represented as a prepared control ToolCall whose typed arguments
are validated by the control service.

waiting_confirmation means that the Run is suspended before the consequential
operation is dispatched. It still occupies the existing one_active_run slot
for its conversation, so a second Run cannot race the pending Attempt. It is
not an execution lease: no command, Provider call or other external operation
is running merely because the Run has this status.

The resource-busy check uses the execution lease and actual running process,
not the one_active_run index. A pending waiting_confirmation Run therefore
does not block applying a future-only control change to its own conversation.
Workspace, active permission and active sandbox changes still apply to a new
Run because the pending Run retains its frozen snapshot.

The card and CLI prompt call the same TaskService.Confirm
decision. The control short code is only an additional input verifier for
control-plane proposals; ordinary tool approvals keep their existing
Confirmation digest, Preview and Confirm behavior and never use this code.

On approval, Confirm records the control Attempt exactly once. It then uses a
small control-applied branch inside the existing confirmation path. Any
applied persistent control-plane mutation marks the suspended Run completed
with control_change_applied_next_run and does not call ordinary Continue.
The active snapshot remains unchanged, and Apply and start a new Run creates
the successor with the new snapshot. Rejection or expiry records the decision
and may resume the old Run through the existing Continue path because the
configuration snapshot has not changed.

This branch does not add a second proposal state machine. It prevents
Continue from treating an intentionally changed configuration as an
unexpected ErrConfigChanged and never rewrites a completed Run.

### 8.4 Apply and continue

The card and CLI prompt offer:

- Apply;
- Apply and start a new Run;
- Reject.

Confirming first completes the proposing Run's control Attempt and applies the
change. Apply and start a new Run creates a successor Run with a fresh config
snapshot and a deterministic Harness-generated continuation prompt. The
continuation prompt is not model-authored authorization text.

The tool result states that the change takes effect in the next Run. Continuing
an old Run keeps its old snapshot unless the user explicitly chooses the new
Run action.

### 8.5 Stale, expiry and failure

The core rechecks the digest, target, expected resource revisions, code epoch,
surface binding, expiry and operation status immediately before apply. It
returns distinct codes for wrong_code, expired, stale_revision,
changed_target, already_used, rejected, busy_active_run and apply_failed.

The executor never queues an operation for a later safe boundary. If a
resource is busy, the request returns busy_active_run and the proposal remains
confirmable until its expiry. The user may retry after the active Run ends. A
crash during apply is recovered from the journal and reports each step; the
core never re-runs a partially applied operation automatically.

## 9. Active Run And Next-Run Semantics

An active Run keeps its frozen model, permission, tool, workspace and network
snapshot. For resource-busy decisions, active execution means a Run with a
running execution lease or a live command, Provider or other external call.
waiting_confirmation is a suspended approval state, not an execution lease.
A mutation may apply while a Run is active only if it affects future Runs and
does not modify the active snapshot.

The result explicitly states one of:

- applied_to_future_runs;
- rejected with busy_active_run;
- applied_and_new_run_required;
- applied_to_new_run.

The following resources return busy_active_run while an execution lease owns
them:

- workspace selection;
- MCP restart or removal;
- Extension restart, removal or data clearing;
- removing a Key or model referenced by the active Run;
- changing sandbox roots while a command is running;
- changing the active Run's frozen permission or network snapshot.

A waiting_confirmation Run continues to occupy its conversation's
one_active_run index, but it does not by itself produce busy_active_run. This
distinction allows the user to confirm a workspace or authority proposal and
then start a successor Run without making the pending Run mutate its old
snapshot.

After an authority or workspace change, the UI and CLI offer an explicit start
next Run action. A desktop workspace switch uses the owner control API, waits
for the required stop or attach decision, and starts a new engine generation.
It never mutates an old Run's snapshot.

## 10. GUI/CLI Attach And Handoff

### 10.1 Discovery and credentials

The owner writes engine.json in the current data directory with:

- loopback endpoint;
- engine generation;
- protocol version;
- product version;
- data-format version;
- owner process identifier;
- owner kind: interactive or one_shot;
- one-shot lease expiry when applicable;
- expiration and integrity fields.

The file has a user-only ACL and explicitly denies the sandbox identity. The
attach capability is stored separately as attach.secret with a user-only ACL,
is protected with the current user's DPAPI when available, and is never
included in engine.json, a URL, a command argument or a Provider request.
The sandbox cannot read either file.

The owner rotates the capability on every generation. A client presents it in
an authenticated header over loopback. The server checks the data-directory
binding, protocol, product and data-format versions before accepting attach.
This capability is an attach and observation credential, not a persistent
mutation credential. A frontend must complete the interactive control
handshake to receive interactive_control_capability for Apply, Revert,
Confirm or destructive direct operations.

### 10.2 Client capabilities

The attach API is loopback-only, host and Origin checked, and capability
scoped. It supports:

- bounded state and event snapshots;
- submit and continue;
- cancellation request;
- settings read and proposal planning through the control service;
- user confirmation, rejection, Apply and Revert only with
  interactive_control_capability;
- pending approval and recovery inspection;
- owner handoff request.

An attached client is a full user surface, so it can submit a new task. It
cannot write SQLite directly, dispatch a duplicate Run, supply a model
confirmation, or rewrite an active snapshot. The owner treats
attach.secret-only control mutation as user_interaction_required.
These checks separate token-only API clients from interactive surfaces. They
are not a boundary against a same-user host process; see section 6.3.

The current eight-listener limit is either raised to a bounded configured
limit or split between Chat UI streams and attach streams. Exhaustion returns
a clear attach_busy error and never drops an existing owner.

### 10.3 Owner and promotion

Interactive CLI or desktop becomes owner when it acquires owner.lock. An
interactive client with no owner creates the engine. A one-shot CLI command
attaches when an owner exists and otherwise opens the store as a one_shot
owner for the bounded duration of its operation. It publishes engine.json and
serves the same authenticated attach API while it owns the lock. A desktop
startup that observes a one_shot owner waits for attach with a bounded timeout
and displays the owner state; it does not immediately fail or open a competing
writer. A one-shot mutation that cannot obtain ownership or attach exits with
an error.

On normal exit, the owner prompts when Runs are active. It stops accepting new
submissions, and a requested handoff waits for live execution to settle or
requires explicit user cancellation. A dispatched external call that cannot
be proven complete is marked UNKNOWN before release. The owner writes the
final generation record and releases the lock. Attached clients observe the
transition and race to acquire ownership. The winner runs recovery; the
others reattach.

On a crash, owner.lock is released by the operating system. The next owner
performs the same recovery without assuming that a dispatched call completed.
Known completed outcomes remain usable, unresolved external calls remain
UNKNOWN, and no caller automatically replays UNKNOWN.

An attached client cannot seize ownership while the owner has a live
execution lease. It can request cancellation or graceful handoff through the
owner. Promotion begins only after owner.lock is released; the new owner then
publishes a new generation and performs recovery.

### 10.4 Desktop integration

Before an owner exists, the desktop init step may call the engine executable
directly. After initialization, desktop config show, workspace switching,
Run submission, cancellation and settings changes use the authenticated owner
control API. Desktop must not spawn a second copy of the desktop executable as
the CLI engine.

### 10.5 Event cursor

Events have monotonic sequence numbers within an engine generation. A client
resumes from its cursor only within the same generation. After a generation
change it receives a fresh bounded snapshot and starts a new cursor. Event
replay is informational; it never replays a tool or Provider call.

## 11. Windows Command Sandbox

### 11.1 Backend and launcher

Add a Windows-only sandbox package behind a small platform-neutral policy
interface. command_run asks the backend to create a worker for an explicit
ExecutionProfile. It does not infer security from the command string.

The AppContainer launcher must use CreateProcessW with STARTUPINFOEX and an
attribute list containing:

- PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES for the selected AppContainer
  token and capabilities;
- PROC_THREAD_ATTRIBUTE_HANDLE_LIST for the minimal inherited handles.

The launcher creates the process with CREATE_SUSPENDED, assigns it to the Job
Object, applies process limits, closes unneeded handles, and then calls
ResumeThread. This ordering is required for both AppContainer and host
explicit modes. It removes the current start-before-Job race. The same
launcher contract applies to command_run, MCP stdio workers, trusted
Extension workers and the desktop engine child. Existing os/exec paths that
start first and attach a Job later must use this launcher or remain
unavailable under the claimed profile.

CreateAppContainerProfile, DeriveAppContainerSidFromAppContainerName and
DeleteAppContainerProfile are loaded lazily from userenv.dll. A custom launcher
is required because os/exec.SysProcAttr does not expose the security
capabilities attribute needed here.

The receipt records backend, profile identity, capability SIDs, grant IDs,
network mode, probe generation, Job identifier, enforcement state and any
unsupported reason.

AppContainer identity is scoped to an immutable grant set. The setup may
cache a profile under a hash of the canonical data-directory path, product
version and grant-set hash, but it never reuses a profile for a different
root, mode or capability set. A worker with a shared identity still receives
a private TEMP directory, private named-pipe namespace and minimal handle
list. Native tests must prove that concurrent workers cannot read one
another's temporary files, handles or command receipts. If that proof fails,
the launcher uses a per-Run profile. Per-root capability SIDs are verified in
the v7.0-0 spike; if no profile strategy can prove the boundary, the sandbox
gate fails.

### 11.2 Current capability decision

The v7.0 sandbox command network modes are:

- off;
- internet.

For AppContainer commands, internet maps to the internetClient capability.
Per-domain allowlist is rejected as unsupported for sandboxed commands in
v7.0. Loopback is always denied for sandboxed commands. No administrator
loopback exemption, WFP rule or system-wide NetworkIsolationSetAppContainerConfig
edit is part of v7.0.

The built-in HTTP tool keeps its own AllowedOrigins allowlist. That allowlist
does not claim to restrict arbitrary sockets opened by a command.

If a user selects sandbox allowlist, sandbox loopback, or another mode the
backend cannot enforce, the request returns unsupported and does not launch
the command. The host explicit mode is a separate confirmed choice.

Host explicit is an intentional same-user escape hatch. An arbitrary host
command can use its Windows identity to edit files or call local services that
the Harness cannot mediate. The owner blocks host commands from using the
attach API to confirm or Apply control operations, but it cannot prove that a
host command did not edit config files directly. Therefore host explicit
receipts carry host_escape_hatch=true, and host-mode runs do not count as
evidence for v7.0 control-plane enforcement. Persistent configuration
acceptance must run through the control service and the bounded AppContainer
profile.

Preventing a user-authorized host command from bypassing the control plane is
an explicit v7.0 non-goal, not a release blocker. It would require a separate
OS identity or a privileged broker, which needs administrator setup and
credential management and conflicts with the portable lightweight product. An
opt-in isolated host user may be considered after v7.0. Until then, v7.0
relies on the trusted same-user definition in section 3.3 and the detection
rules in section 11.2.1.

### 11.2.1 Host run change detection

Detection does not prevent a host command from changing control state. It
makes such a change visible and stops it from taking effect silently. It is
lightweight and runs only in the owner:

- before and after each host explicit command, the owner records SHA-256
  fingerprints of config.json, keys.json and the Skill and Extension trust
  records;
- the owner reads PRAGMA data_version on its single SQLite connection before
  and after the command; because every owner write uses that one connection,
  any change means another process committed to state.sqlite;
- the same fingerprint check runs at owner startup and before each new Run,
  so manual edits and processes that outlived the command are also caught.

desktop.json holds only window geometry and the last UI port and is written
by the desktop process, so it is not fingerprinted. The workspace .freeagent
tree holds Skills and Extensions, which section 11.4 covers with
workspace-level trust, so it is not fingerprinted either.

The control service stores the committed fingerprints with each control
revision in state.sqlite, and also the committed content of config.json,
which is small. Key and trust contents are not copied. Every owner write to
these files goes through the control service and updates the committed
fingerprint in the same operation, so ordinary owner work never reports a
change. A crash between a file write and its revision commit is reported at
startup as a change for review.

When a fingerprint or data_version differs from the last committed value:

- the change is reported by the owner check with the changed item names
  only, never their contents; command receipts do not carry it;
- a new Run does not start and returns external_change_review_required until
  the user reviews a redacted summary on an interactive GUI or CLI surface;
  a Run already in progress keeps its frozen snapshot;
- accepting the change records it as a user_direct commit with a new
  revision; discarding it restores the committed config.json content;
- a changed Key profile is marked untested and must be re-tested or
  re-entered before use;
- a changed trust record invalidates the affected Skill or Extension trust;
- an external SQLite commit runs PRAGMA quick_check and requires the user to
  acknowledge the report before a new Run starts.

A same-user process that rewrites both the files and the stored fingerprints
is not detected; this is detection, not a boundary. Reads leave no trace, so
detection cannot reveal that a host command read keys.json or another user
file. The confirmation text in section 3.3 is the only mitigation for that
risk.

### 11.3 Filesystem policy

The data directory is denied to sandboxed commands. The following paths are
read-only, so they cannot be written, deleted, renamed or replaced, even when
the workspace is writable:

- workspace .freeagent;
- .git, unless git_write is on; then .git/hooks, .git/config and
  .git/config.worktree, plus .git/modules and .git/worktrees when present;
- current data directory and all Harness state, which are never granted;
- owner.lock, keys.json, config.json, desktop.json and state.sqlite files.

Immediately before every launch, the owner compares the data directory with
the workspace, every command-visible additional root and the executable
directory as absolute cleaned paths, and rejects any pair where one contains
the other, including the data directory inside a workspace or an additional
root and the workspace inside the data directory. The portable package root is
therefore not a valid workspace root when it contains the data directory. A
grant root or protected path that is itself a symlink, junction or mount point
is rejected. Resolving links, short names or case variants for this check was
removed in the October 19 review.

Links inside a grant root are allowed. ACL propagation does not follow a
junction or symlink, so a link that points outside the root reaches a target
whose ACL never gained the container SID, and a pnpm-style link whose target
is inside the root keeps working. A native test proves both cases and checks
that the outside target has no container ACE.

An AppContainer can use an access only when an ACE for its package or
capability SID grants it; the user's own ACEs do not satisfy that check.
Protected entries therefore get a protected DACL for the duration of one
launch. It does not inherit the workspace grant, keeps full control for the
user, SYSTEM and Administrators, and gives the container only what its mode
allows:

- read-only: .freeagent, .git without git_write, and the protected .git
  children with git_write. A missing .freeagent, .git/hooks, .git/config or
  .git/config.worktree is created as an empty placeholder for the launch and
  removed afterwards, because a missing name in a writable directory cannot
  be denied;
- git_write on .git: the container reads and writes inside .git, but .git
  itself carries no DELETE, so it cannot be removed, renamed or replaced;
- a .git file (worktree or submodule pointer) is read-only. Its metadata
  directory is never granted, so git fails closed there. Resolving gitdir
  pointers was removed in the October 19 review.

Write grants carry an inherit-only DELETE for ordinary children and never
FILE_DELETE_CHILD, which would defeat the protected entries. Each protected
entry gets its previous DACL back when the launch ends. Writable additional
roots get the same .git rules as the workspace.

git_write is a commands setting, off by default, changed through the same
commands.set_policy operation from the GUI, the CLI (--git-write) or an AI
proposal, and frozen in each Run snapshot.

A missing .git is not created as a placeholder, because an empty .git changes
how repository tools detect a repository. A sandboxed git init in a non-git
workspace therefore remains possible. FreeAgent does not run git or another
repository-configured tool on the host automatically; git runs only as a
requested command_run.

Residual risk: content written by a sandboxed command is untrusted input for
later host tools, including host explicit commands. Examples are hooks or
core.fsmonitor in a repository the command initialized, .vscode/tasks.json,
package.json scripts, Makefiles and build configuration. The sandbox protects
only paths that FreeAgent loads or executes automatically. A command receipt
sets git_metadata_created when a sandboxed command creates .git in a root
that had none, and the release report states this risk. Because .git is
readable, a token embedded in a remote URL in .git/config is readable by
sandboxed commands.

Native tests must prove that a sandboxed command cannot delete, rename,
recreate, or replace each protected subpath. If the tests cannot prove this,
the sandbox profile is not ready.

Command-visible additional roots are the selected file roots, and only when
the Run's file scope includes them; a workspace-only Run grants commands the
workspace alone. Additional roots are current-format records, not strings.
Each record contains:

- canonical path;
- access mode: read, write or git_write;
- user confirmation revision;
- content or directory identity when required;
- capability grant ID;
- grant status.

The data directory format is Harness-v5. Old AdditionalRoots arrays are not
converted. A new Harness-v5 data directory is created instead.

### 11.4 Skill and Extension trust

Workspace Skill and Extension trust stays a per-workspace user decision
(TrustedWorkspaces). Binding trust to a content or manifest hash was removed
in the October 19 review. Sandboxed commands cannot write .freeagent, so a
sandboxed command cannot plant or change a Skill or Extension; edits made by
the user or by a host command fall under the user's own trust decision.

The residual risk of user-approved git hooks remains documented: a user who
explicitly grants git_write and runs a Git operation on the host may cause Git
to invoke a hook in the workspace. v7.0 does not claim that Git hooks are safe
code; hooks stay read-only to sandboxed commands even with git_write, and the
settings page states this next to the git_write switch.

### 11.5 Executable compatibility

Executables installed in a user profile may be unreadable inside AppContainer.
The launcher grants read on the executable's own directory for that launch;
system install paths are readable already. There is no multi-tool
compatibility probe. A launcher shim whose real program lives elsewhere, such
as a Scoop shim, fails inside the sandbox; the user points the command at the
real executable or uses Full access. The sandbox never falls back to host
execution.

doctor runs one sandbox check: it starts cmd.exe in a throwaway AppContainer
with no grants and no network and reports ready, unsupported or failed. A
failed check fails doctor only when command_run is enabled with the sandboxed
backend.

### 11.6 Environment and limits

The worker starts from a minimal filtered environment. TEMP and TMP point to
a container-private temporary directory. pip, npm, Go and Cargo caches are
container-private, so builds can be slower. Global gitconfig is not granted;
Git identity comes from explicit environment values when needed.

The Job Object contains the process tree and terminates all descendants on
cancellation or when the owner closes it; the command timeout is the
wall-time limit. CPU, memory and process-count limits were removed in the
October 19 review. A command receipt records the final tree cleanup.

### 11.7 Grant reuse

There is no setup step, readiness state machine, grant ledger or cleanup
command. Workspace and command-visible root grants are applied on the first
sandboxed command of an owner session and kept, under one AppContainer
profile per data directory and access mode, until the owner shuts down; the
owner then revokes them and deletes the profiles. Later commands reuse the
grants, so the propagation cost is paid once per session. A native test
measured 3,002 entries at about 0.42 s for the first command, 0.04 s for the
next, and 0.40 s to release; a 30,000-entry tree took about 5 s per grant
before reuse.

Protected paths and the executable-directory grant are applied per launch.
Revocation removes only the explicit ACEs for the container SID, so ACL
changes the user made in between are kept. If the owner is killed, the ACEs
remain; they name only the container SID, grant nothing the user does not
already have, and are revoked by the next clean shutdown that uses the same
roots. Deleting the portable folder after a crash can leave these inert ACEs.

### 11.8 Sandbox acceptance

Native Windows tests must prove:

- command launch uses the intended AppContainer identity;
- the Job is assigned before resume;
- read outside the allowed root is denied;
- write, delete, rename and recreate outside writable roots are denied;
- protected paths remain read-only inside writable roots, and .git stays
  readable;
- a junction inside a root does not expose an outside target, and an
  inside target stays usable;
- kept grants are applied once per session and fully released;
- child processes inherit the restriction;
- direct sockets fail when network is off;
- internet mode works only according to AppContainer capability;
- loopback is denied;
- doctor's sandbox check reports the host truthfully;
- concurrent workers cannot read one another's private TEMP files, handles or
  command receipts;
- process-tree termination leaves no surviving worker;
- host explicit mode is distinguishable in receipts and requires confirmation;
- host explicit receipts state the same-user file and local-service trust
  boundary;
- host-mode receipts set host_escape_hatch=true and are excluded from
  control-plane enforcement evidence;
- old AdditionalRoots format is rejected or starts a fresh Harness-v5
  directory;
- overlapping workspace, additional-root and data-directory paths are rejected
  before every launch.

If the custom capability SID spike cannot prove the required path grants, the
release stops at v7.0-0 and records the missing Windows capability. No
fallback to host execution is allowed.

## 12. apply_patch Contract

apply_patch moved to v7.2 in the October 19 review. v7.0 does not ship it;
this section stays as its contract.

apply_patch is a Harness tool with an exact structured request:

- patch text;
- target workspace-relative paths;
- expected SHA-256 for every file being changed;
- tool Attempt ID and CallID when the patch was proposed from an AI
  conversation.

The core rejects absolute paths, traversal, symlink or junction escapes,
protected paths, unexpected file hashes and patches outside the workspace.
The patch is applied to a temporary file, flushed, re-hashed and atomically
renamed only after all checks pass. The original is retained in the existing
file-operation evidence path when the operation is reversible.

A model-proposed patch remains a normal tool-plane call. It uses the existing
ToolRunner Preview and Confirm path, including the selected Full, Workspace or
Ask behavior; it never uses the control-plane short code. A user-direct patch
uses the normal file-tool approval for an ordinary edit, while protected or
authority-expanding changes require the existing explicit approval.
Patch text is treated as untrusted data and cannot create a trusted UI action.

## 13. Data Directory And Format

v7.0 uses a new current-only directory named Harness-v5 under the selected
FreeAgent data root. It contains the v7 configuration, SQLite state, bounded
control journal, owner discovery file and attach secret. Sandbox grants are
not recorded there; section 11.7 describes their lifetime.

There is no reader for old data formats, no migration layer, and no implicit
copy from an old directory. The first v7 startup creates the directory and
requires the normal initialization flow. A user can export selected v7
conversation records through an explicit user operation. v7.0 does not import
records from another format.

The format version is checked in engine discovery and attach handshake.
Clients with a mismatch receive a clear incompatible_data_format result.
The old path is never opened as a writer by v7.

## 14. Work Packages

### v7.0-0: Baseline And Backend Gate

This package has two hard gates:

- v7.0-0a launcher spike: prove CreateProcessW attribute-list launch,
  AppContainer identity, per-grant or per-Run isolation, Job-before-resume,
  protected-path ACLs, the placeholder and FILE_DELETE_CHILD rules of section
  11.3, data-directory overlap rejection and the supported network modes, and
  measure ACL propagation time when granting a large workspace, such as one
  with node_modules, so grant progress and cancellation can be designed from
  real numbers;
- v7.0-0b backend implementation: build the proven launcher into command,
  MCP, Extension and desktop-engine paths, then run the compatibility and
  cleanup tests.

0a must pass before 0b exposes command_run. A failed 0a decision records the
missing Windows capability and stops v7.0. It cannot be replaced by host
execution.

- freeze and record v6.2 settings and API coverage;
- resolve stale version references in release documents;
- leave apply_patch to v7.2;
- implement the shared AppContainer/host launcher and Job-before-resume
  ordering for command, MCP, Extension and desktop-engine children;
- implement filesystem grants with per-session reuse and protected paths;
- implement the supported off and internet network modes;
- implement the doctor sandbox check and native Windows acceptance tests;
- define the current-only attach discovery file and capability secret;
- record live Provider Responses and search acceptance separately.

Exit criterion: native tests prove the stated sandbox matrix. If they do not,
v7.0 is blocked with evidence and no command execution claim.

### v7.0-A: Control Contract

- define typed operation and redacted state schemas;
- implement read, plan, confirm, apply, reject and revision-checked revert;
- add authorization source derived from caller boundaries;
- add per-resource revisions, deterministic digests and bounded journal;
- define partial results and irreversible operation classes;
- represent model proposals as tool Attempts and reuse waiting_confirmation,
  Preview and Confirm;
- add the control-applied branch that finishes a changed-snapshot Run and
  leaves successor creation explicit;
- test that a waiting_confirmation Run occupies one_active_run but does not
  create an execution lease or self-block a future-only control change;
- implement the owner-serialized control_decision and host_execution gate;
- test mixed control/tool responses, concurrent host/control requests and
  exact protected revert snapshots;
- test that a same-user host child with attach.secret cannot Apply, Revert,
  Confirm or create user_direct authorization;
- bind Key secrets to origin, protocol and source;
- generate or check the operation catalog against persistent config fields;
- test stale proposals, busy resources and crash recovery.

Exit criterion: mutation semantics and safety checks live in the core service.

### v7.0-B: AI Control And Confirmation

- register bounded read and proposal tools;
- return only opaque operation references and redacted previews to the model;
- render core-generated cards and CLI prompts;
- implement owner-keyed short codes, bounded retries and refresh re-rendering
  for control-plane proposals;
- withdraw a pending proposal when its Run is ended;
- test same-user GUI/CLI rendering, owner restart reissue, wrong-code no-op,
  five-attempt invalidation and absence of codes in model results;
- test that copied confirmation codes and attach.secret without
  interactive_control_capability return user_interaction_required;
- test that Full, Workspace and Ask still follow the existing file and command
  approval behavior;
- reject a model response that mixes a control proposal with any other
  ToolCall before creating or dispatching Attempts;
- expose control tools only to top-level foreground Runs and test that
  children, workflows, Extensions and background Runs cannot reach
  waiting_confirmation or continueComposition through a control call;
- reject model self-confirmation and raw model action tokens;
- add hidden Key re-entry after origin or protocol changes;
- add prompt-injection tests for files, MCP, web search, memory and children.

Exit criterion: every catalogued persistent setting can be proposed through an
ordinary AI conversation and requires a user confirmation path.

### v7.0-C: GUI/CLI Parity

- route GUI settings endpoints through the control service;
- route CLI config and explicit mutations through the control service;
- make AI, GUI and CLI results share operation status and error codes;
- add result-parity tests;
- add attach capability negotiation and bounded event streams;
- route desktop config show and workspace switching through the owner API;
- test desktop attach while a one-shot CLI owner is within its lease.

Exit criterion: input surface can differ, but authorization and persisted
results are identical.

### v7.0-D: Attach Handoff And Recovery

- add owner discovery, DPAPI-protected capability and loopback attach;
- add full attached-client submit, confirm, cancel and settings methods;
- add observer snapshots and generation-scoped event cursors;
- add sequential CLI-to-GUI and GUI-to-CLI handoff tests;
- add normal-exit, crash and owner-replacement recovery tests;
- cover UNKNOWN retention and duplicate-dispatch prevention;
- test graceful handoff with a live execution lease, explicit cancellation,
  owner.lock release and next-owner promotion;
- add safe branch creation with explicit copy/reference rules for Messages,
  ProviderItems, memory selection and permission snapshots.

Exit criterion: GUI and CLI share one engine safely and no frontend change
duplicates dispatch or loses known state.

### v7.0-E: Durable Context And Long Tasks

Moved out of v7.0 in the October 19 review. Durable compaction is the v7.1
priority; background command sessions follow in a later release.

- persist bounded compaction summaries with source message hashes;
- add explicit user-visible compact, refresh and clear operations;
- add background command sessions with poll, output and stop controls;
- reuse Attempts and runtime evidence for process outcomes;
- mark process outcomes UNKNOWN after restart when final state is not proven;
- keep background execution owner-bound and free of a distributed scheduler.

### v7.0-F: Tool Pipeline And Provider Gate

Moved to v7.2, together with apply_patch. Generic pre-execute, execute and
post-execute lifecycle hooks were dropped. Live Provider acceptance still
needs a Key from the user.

- standardize timeout, cancellation, truncation and concurrency safety;
- add fixtures for Chat, OpenAI-compatible, Anthropic and Responses;
- run real DeepSeek Responses and search acceptance;
- report live checks separately for each Provider;
- add local usage, latency and error diagnostics.

## 15. Acceptance Matrix

### Conversation control

- every persistent product setting has a typed control operation;
- every such operation is reachable through an AI conversation;
- command policy, sandbox roots and network modes can be configured through a
  confirmed conversation operation;
- model and Provider settings change without exposing credentials;
- Key origin changes invalidate the old resolved secret;
- editing base_url or protocol in config.json outside the control service
  returns origin_mismatch and sends no request with the stored Key;
- MCP, Skill and Extension actions show exact trust and execution details;
- memory, knowledge and collaboration settings use the same confirmation
  contract;
- catalog coverage fails when a new persistent field is uncovered.

### Confirmation safety

- every model-proposed persistent control-plane mutation requires user
  confirmation;
- ordinary user-direct changes use the direct tier;
- authority-expanding and destructive direct changes use explicit confirmation;
- UI card and CLI prompt use the same core operation;
- control codes are short, owner-keyed, expiring, surface-bound and absent
  from model data;
- ordinary file and command approvals use the existing ToolRunner path and
  permission profile;
- wrong codes are bounded no-ops;
- chat text is never treated as a confirmation;
- ending the waiting Run, including end-and-send, withdraws its proposal with
  run_abandoned, and a card or prompt left open cannot apply it;
- after code expiry or five failures, the unchanged proposal can be
  re-rendered with a new code without silently creating a different proposal;
- model tools cannot confirm, reject or apply their own proposals;
- a control proposal is the only ToolCall in its model response;
- control tools appear only in top-level foreground Run tool lists; a control
  call from a child, workflow, Extension or background Run is a certain
  failure and never reaches waiting_confirmation or continueComposition;
- a mixed control/tool response creates no pending Attempt and dispatches
  nothing, and the Run fails with control_call_not_exclusive instead of
  UNKNOWN;
- decisions that expand authority, delete data or are irreversible return
  host_execution_busy while any host command runs, authority reduction,
  rejection and cancellation are never blocked, a pending proposal never
  blocks a host command in any conversation, and the two holders cannot pass
  the gate check concurrently;
- after an owner restart, pending proposals reload without holding the gate,
  and the first decision is preceded by the section 11.2.1 change check;
- attach.secret alone cannot confirm, Apply, Revert or establish user_direct;
- stale, expired, changed-target and rejected proposals have distinct results;
- partial application and irreversible actions are reported accurately;
- reversible changes restore exact protected snapshots; redacted previews
  cannot be used as revert data;
- revision-checked revert refuses stale resources.

### Sandbox

- native Windows tests cover identity, launch ordering, filesystem boundaries,
  protected paths, junctions inside roots, network off, internet capability,
  loopback, process tree, concurrent-worker isolation and kept-grant release;
- with workspace-root write granted, a sandboxed command can create, edit,
  rename and delete ordinary root files, but cannot delete, rename, replace
  with a junction or populate the .freeagent placeholder, .git, .git/config
  or .git/hooks, and no grant contains FILE_DELETE_CHILD;
- .git is readable by sandboxed commands and writable only with git_write;
- a sandboxed git init in a non-git workspace sets git_metadata_created in
  the receipt;
- command readiness is identical in GUI, CLI and AI state;
- unsupported modes fail closed and never select host execution;
- host explicit mode requires confirmation and is recorded in evidence;
- host explicit receipts state the same-user file and local-service trust
  boundary;
- the Full access confirmation states that host commands can read stored
  Keys, change FreeAgent settings and data, and access all user files; the
  grant is per conversation, is never a default and is not copied to a new
  conversation;
- under Full access, command_run executes the requested host command without
  a per-command prompt and the model does not refuse an available capability;
- a host command that edits config.json, keys.json, a trust record or
  state.sqlite makes the next Run return external_change_review_required
  until the user accepts or discards the change;
- ordinary owner operations and desktop window-state writes never trigger
  external change review;
- the same change made while the owner is stopped is reported at startup;
- doctor reports the sandbox check; a failed check fails doctor only when
  command_run uses the sandbox;
- workspace, additional-root and data-directory overlap is rejected as
  absolute cleaned paths, and a grant root or protected path that is itself
  a link is rejected.

### GUI/CLI attach

- CLI-created session opens identically in GUI;
- GUI-created session resumes identically in CLI;
- an attached client can submit a new task;
- owner conflicts become authenticated observation or a clear attach error;
- owner-safe cancellation works across processes;
- desktop attaches to a one-shot CLI owner within its lease instead of opening
  a second writer;
- pending approval survives attach and owner replacement;
- known tool results survive handoff;
- UNKNOWN remains UNKNOWN and is never replayed automatically;
- promotion waits for owner.lock release and cannot seize a live execution
  lease;
- desktop settings operations do not spawn a competing engine;
- duplicate dispatch is impossible across reconnect and generation change.

### Regression and reporting

- deterministic Go suite remains green;
- Chat UI tests and build remain green;
- Windows sandbox and desktop startup tests run on native Windows;
- Provider fixture suite remains green;
- live Provider acceptance is reported separately;
- race, native Windows and packaged acceptance are labeled when unavailable;
- final report distinguishes implemented, tested, NOT_RUN, UNKNOWN and
  blocked items.

## 16. Explicit Non-Goals

- no plugin marketplace;
- no general module lifecycle platform;
- no OpenClaw channel or device network;
- no account pool, billing or multi-tenant gateway;
- no automatic RAG or background indexing;
- no implicit memory or personality learning;
- no automatic Provider failover after uncertain calls;
- no distributed job scheduler;
- no default mouse or keyboard desktop takeover;
- no legacy data migration or old-platform compatibility;
- no independent Task, Step, Checkpoint or Evidence persistence subsystem;
- no AppContainer domain allowlist or loopback exemption in v7.0;
- no system-wide firewall, WFP or network-isolation setup in v7.0;
- no separate OS identity or privileged broker that stops a user-authorized
  host command from bypassing the control plane in v7.0;
- no typed chat confirmation;
- no sandbox readiness state machine, multi-tool compatibility probe, grant
  ledger or sandbox cleanup command;
- no Job CPU, memory or process-count limits;
- no content-hash binding for Skill and Extension trust;
- no gitdir resolution for worktrees and submodules, and no link or
  short-name resolution for the overlap check.

## 17. Definition Of Done

v7.0 is complete only when all applicable native gates pass:

1. The AI conversation can read and propose every persistent product
   configuration.
2. Every model-proposed persistent control-plane mutation requires explicit
   user confirmation through a card or CLI prompt.
3. GUI and CLI call the same core mutation and execution APIs.
4. GUI and CLI can attach to one owner engine and move a session in both
   directions without losing state.
5. An attached client can submit new tasks and cannot create duplicate
   dispatch.
6. Windows command_run runs under the implemented sandbox matrix, or the
   release is explicitly blocked with native evidence.
7. Protected workspace paths, data paths, Skill paths and Extension paths
   cannot be modified by sandboxed commands, using placeholders and
   protected per-launch ACLs rather than missing-name denial, and the release
   report states the residual risk of sandbox-written workspace content.
8. Authority changes are previewed, confirmed and revision checked, while
   ordinary file and command calls retain the existing ToolRunner approval
   semantics.
9. The Harness does not inject secrets into model-visible data, tool
   arguments or command environments, and sandboxed commands cannot read the
   data directory. The release report states the residual same-user risk of
   host explicit mode.
10. Active, stopped, failed and UNKNOWN Runs have the same meaning in every
    frontend.
11. Tests prove no duplicate dispatch across handoff or reconnect.
12. A mixed control/tool model response produces no dispatch and is a certain
    failure rather than UNKNOWN, host execution cannot race control
    confirmation, control tools are limited to top-level foreground Runs, and
    attach.secret cannot authorize persistent mutation.
13. Workspace and additional roots cannot overlap the data directory, and
    reversible changes have exact protected snapshots.
14. The release report distinguishes implemented, tested, NOT_RUN, UNKNOWN
    and blocked items.

Tool-less models are not evidence that AI control works. Deterministic GUI and
CLI controls remain available, while AI-control acceptance requires an adapter
that can call the typed control tools.

## 18. Execution Order

1. Freeze the v6.2 baseline and record current settings and API coverage.
2. Create the Harness-v5 current-only data directory and reject old formats.
3. Implement the custom AppContainer launcher spike and capability grant
   proof.
4. Implement protected filesystem policy, grant reuse and the doctor sandbox
   check.
5. Run native security tests.
6. Define attach discovery, capability protection, generation and handshake.
7. Build the operation catalog and coverage gate.
8. Implement plan, confirmation, apply, partial result, revert and origin
   binding in the core.
9. Add card and CLI prompt confirmation.
10. Route GUI, CLI and desktop settings paths through the control service.
11. Add full attach client, handoff, crash and UNKNOWN tests.
12. After v7.0: durable compaction (v7.1), then apply_patch and the Provider
    pipeline (v7.2), then background command sessions.

The release owner stops at each gate, records actual evidence and investigates
failures before continuing. No package is marked complete because the token
budget is nearly exhausted.

## 19. Revision Notes

This revision resolves the architecture review findings:

- control-plane mutations and tool-plane approvals are explicitly separated;
- control proposals reuse the existing tool Attempt, waiting_confirmation,
  Preview and Confirm path;
- the short control code is owner-keyed, surface-bound, re-renderable within
  a generation and reissued after owner restart;
- tool-plane file and command calls retain Full, Workspace and Ask behavior;
- busy checks use execution leases while waiting_confirmation still protects
  the conversation's one_active_run slot;
- revert storage, revision checks and irreversible actions are defined;
- Key secrets are bound to origin, protocol and source and require re-entry
  after a change;
- AppContainer launching uses a custom CreateProcessW attribute-list launcher,
  Job-before-resume ordering and explicit capability limits;
- v7.0 sandbox networking supports only off and internet, with sandbox
  allowlist and loopback rejected as unsupported;
- protected workspace, .freeagent, .git and data paths are defined, including
  content-bound Skill and Extension trust;
- attach is a full client, has owner promotion semantics, protected
  credentials and generation-scoped cursors;
- desktop config and workspace operations use the owner control API;
- AdditionalRoots becomes a current-only per-root access record in Harness-v5.

The host boundary and high-risk review adds:

- Full access with host execution is a trusted same-user mode with explicit
  disclosure, per-conversation scope and no default;
- a separate OS identity or broker is a non-goal and not a v7.0 blocker;
- host run change detection uses committed fingerprints and SQLite
  data_version, and external changes require user review before use;
- the control gate and interactive_control_capability are described as
  defense in depth, with the security boundary at the AppContainer profile;
- stored Keys embed origin, protocol and profile ID inside the DPAPI payload;
- a mixed control/tool response is a certain failure and never UNKNOWN;
- protected workspace entries use placeholders and explicit deny ACEs, no
  grant contains FILE_DELETE_CHILD, and sandbox-written content is a stated
  residual risk;
- near-miss confirmation text is intercepted, and other messages during a
  pending proposal are an announced reject-and-send;
- the control gate covers only the decision step, is owner-wide, is never
  persisted and is rebuilt empty after restart;
- authority reduction, rejection and cancellation never wait for the gate,
  and long-lived host MCP and Extension workers do not hold it;
- desktop window state and the user's own .freeagent edits do not trigger
  external change review, and read-only .git gets inheritable deny ACEs;
- control tools are limited to top-level foreground Runs.

The October 19 reduction removes work that added weight without a matching
product need. It supersedes the earlier notes above where they disagree:

- typed chat confirmation and the superseded_by_user_message outcome are
  removed; the GUI card is clicked, the CLI prompt shows the code, and ending
  a waiting Run withdraws its proposal;
- the host gate keeps booleans, because only one Run is active and only
  read-only file children run in parallel;
- Job CPU, memory and process-count limits, the sandbox readiness states, the
  multi-tool compatibility probe, the grant ledger and the cleanup command are
  removed; doctor runs one sandbox check;
- Skill and Extension trust stays workspace-level instead of content-bound;
- receipts do not carry external_control_change;
- gitdir pointers are not resolved and still fail closed, and the overlap
  check compares cleaned absolute paths without resolving links;
- links inside a grant root are allowed instead of rejected, so pnpm
  projects can use the sandbox, and root grants are reused for the owner
  session instead of re-propagated for every command;
- protected paths use protected per-launch DACLs instead of inheritable deny
  ACEs, because an AppContainer can use only accesses granted to its package
  SID, and .git is readable so git status works;
- a git_write switch makes .git writable while hooks and config stay
  read-only;
- apply_patch and the tool pipeline move to v7.2, durable compaction is the
  v7.1 priority, and generic lifecycle hooks are dropped.
