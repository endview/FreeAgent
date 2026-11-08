export type Permission = "none" | "ask" | "workspace" | "full";
export interface Conversation {
  id: string;
  title: string;
  workspace: string;
  space_id: string;
  created_at: string;
  updated_at: string;
  pinned?: boolean;
}
export interface Space {
  selection?: Record<string, unknown>;
  id: string;
  title: string;
  source: string;
  permission?: Permission;
  owner_private?: boolean;
  active_conversation_id?: string;
  default_conversation_id?: string;
  multi_conversation_enabled?: boolean;
}
export interface Attachment {
  id: string;
  name: string;
  mime: string;
  sha256?: string;
  bytes: number;
  source?: string;
}
export interface ChatMessage {
  key: string;
  role: string;
  raw: string;
  truncated?: boolean;
  attachments?: Attachment[];
}
export interface ToolReceipt {
  kind: string;
  path?: string;
  status: string;
  exit_code?: number;
  host_escape_hatch?: boolean;
  detail?: string;
}
export interface Activity {
  requested_reasoning?: ReasoningSelection;
  usage?: {
    input_tokens: number;
    output_tokens: number;
    total_tokens: number;
    reasoning_tokens?: number;
  };
  key: string;
  kind: string;
  name?: string;
  target?: string;
  status: string;
  is_error?: boolean;
  text?: string;
  duration_ms?: number;
  receipts?: ToolReceipt[];
  failure?: { category?: string; retryable?: boolean };
}
export interface Confirmation {
  run_id: string;
  attempt_id: string;
  digest: string;
  tool: string;
  preview: string;
  items?: {
    kind: string;
    target: string;
    old?: string;
    new?: string;
    fields?: Record<string, string>;
  }[];
  control_operation_id?: string;
  [key: string]: unknown;
}
export interface Turn {
  analysis?: { skill?: string; digest?: string };
  trigger?: { kind:string;extension_id:string;rule_id:string;opportunity_id:string };
  sources?: {
    title?: string;
    scope?: { kind?: string; owner?: string };
    revision?: number;
    state?: string;
  }[];
  attachments?: Attachment[];
  messages_has_earlier?: boolean;
  activity_has_earlier?: boolean;
  evidence?: Record<string, unknown>;
  key: string;
  state: string;
  reason?: string;
  messages: ChatMessage[];
  activity: Activity[];
  steps: number;
  elapsed_ms: number;
  can_continue: boolean;
  can_end: boolean;
  confirmation?: Confirmation;
  model_id?: string;
  ended?: boolean;
  maintenance?: boolean;
  [key: string]: unknown;
}
export interface ChatView {
  conversation: Conversation;
  model_id: string;
  permission: { mode: Permission; source?: string };
  capabilities: CapabilitySelection;
  turns: Turn[];
  has_earlier?: boolean;
  before?: string;
  background?: ChatMessage[];
  [key: string]: unknown;
}
export interface CapabilitySelection {
  skills?: string[];
  mcp_servers?: string[];
  extensions?: string[];
  extension_variants?: Record<string, string>;
  extension_settings?: Record<string, Record<string, string>>;
}
export interface Stream {
  attempt: string;
  run: string;
  raw: string;
  interrupted: boolean;
  truncated?: boolean;
}
export interface ChatSnapshot {
  view: ChatView;
  streams: Stream[];
  busy: boolean;
  stopping: boolean;
  running_elsewhere?: boolean;
  active_chats?: string[];
  generation?: string;
  snapshot_seq?: number;
  snapshot_stable?: boolean;
  server_version?: string;
}
export interface Model {
  id?: string;
  provider?: string;
  model?: string;
  label?: string;
  key?: string;
  enabled?: boolean;
  provider_enabled?: boolean;
  missing?: boolean;
  capabilities?: Record<string, unknown>;
}
export interface ReasoningSelection {
  mode?: string;
  effort?: string;
}
export interface ReasoningCapabilities {
  control?: string;
  efforts?: string;
  default?: ReasoningSelection;
  auxiliary?: ReasoningSelection;
}
export interface Environment {
  delivery_available?: boolean;
  workspace: string;
  environment: string;
  default_model: string;
  models: Record<string, Model>;
  providers: Record<string, boolean>;
  capabilities?: CapabilityCatalog;
  remote_max_permission?: Permission;
  desktop?: boolean;
  public_entry?: boolean;
  [key: string]: unknown;
}
export interface CapabilityCatalog {
  skills: {
    id: string;
    enabled: boolean;
    name?: string;
    invocation?: string;
  }[];
  mcp_servers: { id: string; enabled: boolean }[];
  extensions: {
    id: string;
    name?: string;
    enabled: boolean;
    turn_settings?: ExtensionTurnSetting[];
  }[];
}
export interface ExtensionTurnSetting {
  key: string;
  label: string;
  options: { value: string; label: string }[];
}
export interface ContextState {
  window: {
    model_id: string;
    tokens: number;
    input_budget_tokens: number;
    output_reserve_tokens: number;
    automatic_percent: number;
    byte_limit: number;
    source: string;
  };
  last_request?: {
    occupancy_percent: number;
    estimated_input_tokens: number;
    bytes: number;
    limiting_resource: string;
    newer_history_available?: boolean;
    parts: { kind: string; bytes: number; estimated_tokens: number }[];
  };
  last_reported_usage?: { input_tokens: number; output_tokens: number };
  status: string;
  revision: string;
  summary?: { text: string; source_count: number; revision: number };
  automatic: boolean;
  byte_limit: number;
  blocked?: string;
  can_propose: boolean;
}
export interface CoreEvent {
  kind: string;
  seq?: number;
  generation?: string;
  message?: string;
  run_id?: string;
  attempt_id?: string;
  conversation_id?: string;
  server_version?: string;
  [key: string]: unknown;
}
export interface PublicSession {
  authenticated?: boolean;
  csrf?: string;
  cookie_check?: boolean;
  [key: string]: unknown;
}

export interface ControlOperation {
  old_values?: Record<string, string>;
  attempt_id?: string;
  operation_id: string;
  kind: string;
  preview: string;
  digest: string;
  risk: string;
  status: string;
  resource_ids: string[];
  proposed_values?: Record<string, string>;
  authorization_source?: string;
  conversation_id?: string;
  created_at?: string;
  revert_class?: string;
}
