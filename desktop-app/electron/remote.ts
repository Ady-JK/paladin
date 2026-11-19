// An SSH session carries the existing bidirectional JSONL host protocol.
// SSH verifies the host key and provides encryption; the renderer never
// receives access to the local SSH process or credentials.
export type CoreConnection = { mode: "local" } | { mode: "ssh"; target: string; binary: string };

// Neither the user nor the host may start with "-": ssh would read it as an
// option (the "--" before the target is the second guard).
const TARGET = /^(?:[a-zA-Z0-9_][a-zA-Z0-9_.-]*@)?[a-zA-Z0-9_][a-zA-Z0-9_.-]*$/;

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

// hostEnv is the environment the sidecar runs under. Local cores get
// ELECTRON_NODE: this binary doubles as Node 24 under
// ELECTRON_RUN_AS_NODE, so Node child processes run on a machine without a
// separate Node install. A remote core sets its own.
export function hostEnv(
  connection: CoreConnection,
  base: NodeJS.ProcessEnv = process.env,
  exec: string = process.execPath,
) {
  const env: NodeJS.ProcessEnv = { ...base };
  // Electron's own runtime variables would leak into the sidecar, and
  // NODE_OPTIONS would load code into every Node child it starts (hooks,
  // MCP servers). Provider keys and the rest of the environment pass through.
  for (const name of Object.keys(env)) if (name.startsWith("ELECTRON_")) delete env[name];
  delete env.NODE_OPTIONS;
  if (connection.mode === "local" && exec) env.ELECTRON_NODE = exec;
  return env;
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
