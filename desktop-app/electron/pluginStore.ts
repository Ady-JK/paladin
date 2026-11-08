// Small plugin-owned stores are bounded and replaced atomically. A bad
// existing store must remain available for recovery, not be overwritten.
import fs from "node:fs";
import path from "node:path";
import { randomUUID } from "node:crypto";

const MAX_STORE_BYTES = 1 << 20;
const MAX_VALUE_BYTES = 100 << 10;

export function readPluginStore(file: string): Record<string, unknown> {
  let fd: number;
  try {
    if (!fs.lstatSync(file).isFile()) throw new Error("plugin store is not a regular file");
    fd = fs.openSync(file, fs.constants.O_RDONLY | (fs.constants.O_NOFOLLOW ?? 0) | (fs.constants.O_NONBLOCK ?? 0));
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ENOENT") return Object.create(null) as Record<string, unknown>;
    throw error;
  }
  try {
    const info = fs.fstatSync(fd);
    if (!info.isFile()) throw new Error("plugin store is not a regular file");
    if (info.size > MAX_STORE_BYTES) throw new Error("plugin store is larger than 1 MiB");
    const bytes = Buffer.allocUnsafe(MAX_STORE_BYTES + 1);
    let length = 0;
    while (length < bytes.length) {
      const count = fs.readSync(fd, bytes, length, bytes.length - length, null);
      if (!count) break;
      length += count;
    }
    if (length > MAX_STORE_BYTES) throw new Error("plugin store is larger than 1 MiB");
    const value: unknown = JSON.parse(bytes.toString("utf8", 0, length));
    if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("plugin store is not an object");
    return Object.assign(Object.create(null) as Record<string, unknown>, value);
  } finally {
    fs.closeSync(fd);
  }
}

export function writePluginStore(file: string, key: string, value: unknown): void {
  const encoded = JSON.stringify(value ?? null);
  if (encoded === undefined) throw new Error("value is not JSON");
  if (Buffer.byteLength(encoded) > MAX_VALUE_BYTES) throw new Error("value is larger than 100 KiB");
  const store = readPluginStore(file);
  store[key] = value ?? null;
  const data = JSON.stringify(store);
  if (Buffer.byteLength(data) > MAX_STORE_BYTES) throw new Error("plugin store is larger than 1 MiB");
  const temporary = path.join(path.dirname(file), `.store-${randomUUID()}.tmp`);
  let fd: number | undefined;
  let created = false;
  let failure: Error | undefined;
  const recordFailure = (error: unknown): void => {
    failure ??= error instanceof Error ? error : new Error(String(error));
  };
  try {
    fd = fs.openSync(temporary, "wx", 0o600);
    created = true;
    fs.writeFileSync(fd, data);
    fs.fsyncSync(fd);
    fs.closeSync(fd);
    fd = undefined;
    fs.renameSync(temporary, file);
  } catch (error) {
    recordFailure(error);
  }
  if (fd !== undefined) {
    try {
      fs.closeSync(fd);
    } catch (error) {
      recordFailure(error);
    }
  }
  if (created) {
    try {
      fs.unlinkSync(temporary);
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") recordFailure(error);
    }
  }
  if (failure) throw failure;
}
