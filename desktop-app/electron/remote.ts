// An SSH session carries the existing bidirectional JSONL host protocol.
// SSH verifies the host key and provides encryption; the renderer never
// receives access to the local SSH process or credentials.
export type CoreConnection = { mode: "local" } | { mode: "ssh"; target: string; binary: string };

const TARGET = /^(?:[a-zA-Z0-9_.-]+@)?[a-zA-Z0-9_.-]+$/;

export function validateConnection(value: unknown): CoreConnection {
  if (!value || typeof value !== "object") throw new Error("invalid core connection");
  const input = value as Record<string, unknown>;
  if (input.mode === "local") return { mode: "local" };
  if (input.mode !== "ssh" || typeof input.target !== "string" || !TARGET.test(input.target)) {
    throw new Error("SSH target must be a host or user@host");
  }
  if (typeof input.binary !== "string" || !input.binary.startsWith("/") || /[\r\n\0]/.test(input.binary)) {
    throw new Error("remote agent-host path must be absolute");
  }
  return { mode: "ssh", target: input.target, binary: input.binary };
}

function shellQuote(value: string): string {
  return "'" + value.replace(/'/g, "'\\''") + "'";
}

export function hostCommand(connection: CoreConnection, localBinary: string): { command: string; args: string[] } {
  if (connection.mode === "local") return { command: localBinary, args: [] };
  return {
    command: "ssh",
    args: [
      "-T",
      "-o",
      "BatchMode=yes",
      "-o",
      "StrictHostKeyChecking=yes",
      "-o",
      "ConnectTimeout=10",
      "--",
      connection.target,
      `exec ${shellQuote(connection.binary)}`,
    ],
  };
}
