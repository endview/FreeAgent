import {
  BOOTSTRAP_RESPONSE_SCHEMA,
  RESUME_RESPONSE_SCHEMA,
  credential,
  decodeControlError,
  decodeSessionExchange,
  exactLoopbackOrigin,
  type BootstrapHandoff,
  type SessionExchange
} from "./contracts.ts";

export const RESUME_STORAGE_KEY = "freeagent.control.resume.v1";
export const RESUME_STORAGE_SCHEMA = "freeagent.control-resume-storage/v1";

export type StoredResume = {
  schema_version: "freeagent.control-resume-storage/v1";
  origin: string;
  resume_credential: string;
};

export class ControlSessionError extends Error {
  readonly status: number;
  readonly code: string;
  readonly correlationID: string;

  constructor(message: string, status = 0, code = "TRANSPORT_ERROR", correlationID = "") {
    super(message);
    this.name = "ControlSessionError";
    this.status = status;
    this.code = code;
    this.correlationID = correlationID;
  }
}

export const canonicalBootstrapBody = (capability: string) =>
  `{"capability":${JSON.stringify(capability)},"schema_version":"control-bootstrap-exchange/v1"}`;

export const canonicalResumeBody = (resumeCredential: string) =>
  `{"resume_credential":${JSON.stringify(resumeCredential)},"schema_version":"control-session-resume/v1"}`;

const exactJSONResponse = async (response: Response, label: string) => {
  if (response.headers.get("Content-Type") !== "application/json") {
    throw new ControlSessionError(`${label} returned an invalid content type`, response.status);
  }
  return response.text();
};

const responseError = async (response: Response) => {
  let text = "";
  try {
    text = await response.text();
  } catch {
    return new ControlSessionError("the control service returned an unreadable error", response.status);
  }
  const decoded = decodeControlError(text);
  if (decoded === null) {
    return new ControlSessionError("the control service returned an invalid error", response.status);
  }
  return new ControlSessionError(
    decoded.message,
    response.status,
    decoded.code,
    decoded.correlation_id
  );
};

export const exchangeHandoff = async (
  handoff: BootstrapHandoff,
  fetcher: typeof fetch = fetch
): Promise<SessionExchange> => {
  if (handoff.origin !== window.location.origin) {
    throw new ControlSessionError(
      "handoff origin does not match this control page",
      0,
      "HANDOFF_ORIGIN_MISMATCH"
    );
  }
  const response = await fetcher(`${handoff.origin}/control/bootstrap`, {
    method: "POST",
    credentials: "same-origin",
    redirect: "error",
    headers: { "Content-Type": "application/json" },
    body: canonicalBootstrapBody(handoff.capability)
  });
  if (!response.ok) throw await responseError(response);
  const text = await exactJSONResponse(response, "bootstrap exchange");
  return decodeSessionExchange(text, BOOTSTRAP_RESPONSE_SCHEMA);
};

export const resumeSession = async (
  origin: string,
  resumeCredential: string,
  fetcher: typeof fetch = fetch
): Promise<SessionExchange> => {
  if (
    !exactLoopbackOrigin(origin) ||
    origin !== window.location.origin ||
    !credential(resumeCredential)
  ) {
    throw new ControlSessionError("stored session metadata is invalid", 0, "STORED_SESSION_INVALID");
  }
  const response = await fetcher(`${origin}/control/session/resume`, {
    method: "POST",
    credentials: "include",
    redirect: "error",
    headers: { "Content-Type": "application/json" },
    body: canonicalResumeBody(resumeCredential)
  });
  if (!response.ok) throw await responseError(response);
  const text = await exactJSONResponse(response, "session resume");
  return decodeSessionExchange(text, RESUME_RESPONSE_SCHEMA);
};

export const decodeStoredResume = (text: string): StoredResume | null => {
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    return null;
  }
  if (
    typeof value !== "object" ||
    value === null ||
    Array.isArray(value) ||
    Object.keys(value).sort().join("\n") !==
      ["origin", "resume_credential", "schema_version"].sort().join("\n")
  ) return null;
  const record = value as Record<string, unknown>;
  if (
    record.schema_version !== RESUME_STORAGE_SCHEMA ||
    !exactLoopbackOrigin(record.origin) ||
    !credential(record.resume_credential)
  ) return null;
  return record as StoredResume;
};

export const readStoredResume = (): StoredResume | null => {
  try {
    const value = sessionStorage.getItem(RESUME_STORAGE_KEY);
    if (value === null) return null;
    const decoded = decodeStoredResume(value);
    if (decoded === null || decoded.origin !== window.location.origin) {
      sessionStorage.removeItem(RESUME_STORAGE_KEY);
      return null;
    }
    return decoded;
  } catch {
    return null;
  }
};

export const storeResume = (origin: string, value: string) => {
  if (origin !== window.location.origin || !exactLoopbackOrigin(origin) || !credential(value)) {
    throw new Error("resume session metadata is invalid");
  }
  const encoded = JSON.stringify({
    schema_version: RESUME_STORAGE_SCHEMA,
    origin,
    resume_credential: value
  } satisfies StoredResume);
  try {
    sessionStorage.setItem(RESUME_STORAGE_KEY, encoded);
    return true;
  } catch {
    return false;
  }
};

export const clearStoredResume = () => {
  try {
    sessionStorage.removeItem(RESUME_STORAGE_KEY);
  } catch {
    // A disabled storage area is already equivalent to no resumable session.
  }
};

export const shouldDiscardResume = (error: unknown) =>
  error instanceof ControlSessionError &&
  (error.code === "STORED_SESSION_INVALID" ||
    error.code === "UNAUTHENTICATED" ||
    error.code === "SESSION_EXPIRED" ||
    error.status === 401);
