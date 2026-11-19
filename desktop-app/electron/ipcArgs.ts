// Small, pure checks for values that arrive over IPC from a renderer or a
// plugin panel. Nothing here touches Electron, so the rules run in plain
// Node under Playwright.
import path from "node:path";

// field reads a string property of an untrusted object; "" when absent.
export function field(value: unknown, key: string): string {
  if (typeof value !== "object" || value === null) return "";
  const found = (value as Record<string, unknown>)[key];
  return typeof found === "string" ? found : "";
}

export function errText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

// withinBytes reports whether value serializes to at most max bytes.
// Values that cannot be serialized are refused.
export function withinBytes(value: unknown, max: number): boolean {
  if (value === undefined) return true;
  try {
    return Buffer.byteLength(JSON.stringify(value) ?? "") <= max;
  } catch {
    return false;
  }
}

const SECRET_DIRS = new Set([".git", ".ssh", ".aws", ".gnupg", ".kube"]);
const SECRET_FILES = new Set([".npmrc", ".netrc", ".pypirc", ".git-credentials", "credentials", "credentials.json"]);
const SECRET_EXTENSIONS = new Set([".pem", ".key", ".p12", ".pfx", ".p8", ".jks", ".keystore"]);
const ENV_SAFE_SUFFIX = /\.(example|sample|template|dist)$/;

// secretPath flags a workspace-relative path that usually holds credentials
// or version-control internals. A plugin panel with readFile gets the
// workspace, not the secrets in it.
export function secretPath(rel: string): boolean {
  const parts = rel
    .toLowerCase()
    .split(/[\\/]+/)
    .filter(Boolean);
  for (const part of parts) {
    if (SECRET_DIRS.has(part) || SECRET_FILES.has(part)) return true;
    if (part === ".env" || (part.startsWith(".env.") && !ENV_SAFE_SUFFIX.test(part))) return true;
    if (/^id_(rsa|dsa|ecdsa|ed25519)$/.test(part)) return true;
    if (SECRET_EXTENSIONS.has(path.extname(part))) return true;
  }
  return false;
}

const MAX_URL_LENGTH = 2048;

// externalUrl parses a link that may be handed to the OS: https always,
// http and mailto only when asked for, never credentials or long URLs.
export function externalUrl(raw: unknown, allow: { http?: boolean; mailto?: boolean }): URL | null {
  if (typeof raw !== "string" || raw.length > MAX_URL_LENGTH) return null;
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return null;
  }
  if (url.username || url.password) return null;
  if (url.protocol === "https:") return url;
  if (url.protocol === "http:" && allow.http) return url;
  if (url.protocol === "mailto:" && allow.mailto) return url;
  return null;
}

// absolutePath returns value when it is an absolute path without control
// characters, otherwise undefined.
export function absolutePath(value: unknown): string | undefined {
  if (typeof value !== "string" || !value || !path.isAbsolute(value)) return undefined;
  return /[\0\r\n]/.test(value) ? undefined : value;
}
