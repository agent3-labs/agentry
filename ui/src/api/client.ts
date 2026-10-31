// Talking to `agentry serve`. The token comes from the link the server
// printed, or from signing in: the page keeps it in this browser and sends
// it on every call, and takes it out of the address bar so it isn't shared
// by accident. The page may have no token of its own and still be signed
// in: the server sets a cookie too, and an installed app, on iOS
// especially, may keep only that. So only a 401 means signed out.

import type { PolicyChange, Settings, SigninCode, TriedCall } from "@/api/types";
import { request, storage } from "@/lib/host";

const tokenKey = "agentry-token";

// takeFromAddress reads a parameter and takes it out of the address bar;
// the rest of the address, such as ?view=board, stays.
function takeFromAddress(name: string): string {
  const query = new URLSearchParams(location.search);
  const value = query.get(name) ?? "";
  if (value) {
    query.delete(name);
    const rest = query.toString();
    history.replaceState(null, "", location.pathname + (rest ? "?" + rest : "") + location.hash);
  }
  return value;
}

function keep(value: string) {
  try {
    if (value) storage().setItem(tokenKey, value);
    else storage().removeItem(tokenKey);
  } catch {
    // storage is off: the page keeps the token until it closes, the cookie longer
  }
}

function kept(): string {
  try {
    return storage().getItem(tokenKey) ?? "";
  } catch {
    return "";
  }
}

let token = takeFromAddress("token");
if (token) keep(token);
else token = kept();

// The one-time code a QR code's link (/?signin=<code>) brings, for the
// sign-in page to exchange.
export const signinCodeFromLink = takeFromAddress("signin");

// Signed out is what the server said last: a 401 sets it, signing in clears it.
let signedOut = false;
// Each sign-in starts a new session. A 401 to a call sent in an earlier one,
// such as the theme the page saves as it opens, can arrive after signing in
// and says nothing about the new token, so it doesn't sign the page out.
let session = 0;
const listeners = new Set<() => void>();
function setSignedOut(value: boolean) {
  if (signedOut === value) return;
  signedOut = value;
  listeners.forEach((listener) => listener());
}

export function isSignedOut(): boolean {
  return signedOut;
}

export function watchSignedOut(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export class SignedOutError extends Error {
  constructor() {
    super("Not signed in.");
  }
}

// authorization is the bearer header, when the page has the token; without
// it, the cookie signs the call in, if the browser kept it.
function authorization(): Record<string, string> {
  return token ? { Authorization: "Bearer " + token } : {};
}

// ApiError is a call the server refused, with its reason as the message.
// A reply of JSON {error, ...} keeps the rest in body, for a caller that
// can act on it, such as the profiles a new team needs.
export class ApiError extends Error {
  readonly status: number;
  readonly body?: Record<string, unknown>;
  constructor(message: string, status: number, body?: Record<string, unknown>) {
    super(message);
    this.status = status;
    this.body = body;
  }
}

async function refusal(response: Response): Promise<ApiError> {
  const text = (await response.text()).trim();
  try {
    const body = JSON.parse(text) as Record<string, unknown>;
    if (typeof body.error === "string") return new ApiError(body.error, response.status, body);
  } catch {
    // a plain-text reason
  }
  return new ApiError(text || response.statusText, response.status);
}

// send makes a call signed in, and throws what the server refused: a 401 as
// SignedOutError, anything else as an ApiError with the server's reason.
export async function send(path: string, init: RequestInit & { headers?: Record<string, string> }): Promise<Response> {
  const sentIn = session;
  const response = await request(path, { ...init, headers: { ...init.headers, ...authorization() } });
  if (response.status === 401) {
    if (sentIn === session) setSignedOut(true);
    throw new SignedOutError();
  }
  if (!response.ok) throw await refusal(response);
  return response;
}

export async function api<T>(method: "GET" | "POST" | "PUT" | "DELETE", path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const response = await send(path, {
    method,
    signal,
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  return response.json() as Promise<T>;
}

// signIn swaps a one-time code, or the token pasted in, for the token, and
// keeps it. When the server refuses, it throws the server's reason: a wrong
// or used code, or too many tries.
export async function signIn(given: { code: string } | { token: string }): Promise<void> {
  const response = await request("/api/signin", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(given),
  });
  if (!response.ok) throw new Error((await response.text()).trim() || response.statusText);
  token = ((await response.json()) as { token: string }).token;
  keep(token);
  session++;
  setSignedOut(false);
}

// signOut signs this device out: the server forgets the cookie, the page its token.
export async function signOut(): Promise<void> {
  await api("POST", "/api/signout");
  token = "";
  keep("");
  setSignedOut(true);
}

// newSigninCode is a one-time code that signs in another device.
export function newSigninCode(): Promise<SigninCode> {
  return api<SigninCode>("POST", "/api/signin/codes");
}

// changeSettings changes the settings named, and answers with all of them.
export function changeSettings(change: Partial<Settings>): Promise<Settings> {
  return api<Settings>("PUT", "/api/settings", change);
}

// Changes to the policy, each made against the version the page saw
// (hash). The server refuses one made against an older version.

const profilePath = (name: string, action: string) => `/api/policy/profiles/${encodeURIComponent(name)}/${action}`;

export function addDefaultProfile(name: string, hash: string): Promise<PolicyChange> {
  return api<PolicyChange>("POST", profilePath(name, "add"), { hash });
}

export function updateDefaultProfile(name: string, hash: string): Promise<PolicyChange> {
  return api<PolicyChange>("POST", profilePath(name, "update"), { hash });
}

// takeDefaultProfile replaces a profile you changed with the default.
export function takeDefaultProfile(name: string, hash: string): Promise<PolicyChange> {
  return api<PolicyChange>("POST", profilePath(name, "use-default"), { hash });
}

export function turnOffUseForEveryone(hash: string): Promise<PolicyChange> {
  return api<PolicyChange>("POST", "/api/policy/use-for-everyone/off", { hash });
}

export function revertPolicy(position: number, hash: string): Promise<PolicyChange> {
  return api<PolicyChange>("POST", "/api/policy/revert", { position, hash });
}

// tryPolicyCall judges a call as profile would, without running it or
// changing anything. With no workspace, the server picks a stand-in.
export function tryPolicyCall(call: { profile: string; tool: string; input: string; workspace?: string }): Promise<TriedCall> {
  return api<TriedCall>("POST", "/api/policy/try", call);
}

// Websockets can't send headers, so the terminal's carries the token in its address.
function socketURL(path: string, query = ""): string {
  const scheme = location.protocol === "https:" ? "wss://" : "ws://";
  return `${scheme}${location.host}${path}?token=${encodeURIComponent(token)}${query}`;
}

export function terminalURL(taskId: string): string {
  return socketURL(`/api/tasks/${encodeURIComponent(taskId)}/terminal`);
}

// Your own Claude session beside a task, in its worktree; started on the way.
export function helperTerminalURL(taskId: string, theme: "light" | "dark"): string {
  return socketURL(`/api/tasks/${encodeURIComponent(taskId)}/claude/terminal`, `&theme=${theme}`);
}

// The Claude session that turns what you say into tasks for the coordinator.
// It starts in the page's theme, and restarts in a new one, keeping the
// conversation, when the address's theme changes.
export function deskTerminalURL(theme: "light" | "dark"): string {
  return socketURL("/api/desk/terminal", `&theme=${theme}`);
}
