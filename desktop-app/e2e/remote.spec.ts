import { expect, test } from "@playwright/test";
import { hostCommand, hostEnv, validateConnection } from "../electron/remote";

test("a local core gets the Electron binary as its extension Node", () => {
  const env = hostEnv({ mode: "local" }, { PATH: "/usr/bin", ELECTRON_RUN_AS_NODE: "1" }, "/app/electron");
  expect(env.ELECTRON_NODE).toBe("/app/electron");
  // Electron's own run-as-node flag must not leak into the sidecar.
  expect(env.ELECTRON_RUN_AS_NODE).toBeUndefined();
  expect(env.PATH).toBe("/usr/bin");
  // A remote core runs on the other machine, so it gets no local path.
  const remote = hostEnv({ mode: "ssh", target: "host", binary: "/bin/agent-host" }, {}, "/app/electron");
  expect(remote.ELECTRON_NODE).toBe(undefined);
});

test("remote connection uses batch SSH with strict host verification", () => {
  const value = validateConnection({ mode: "ssh", target: "dev@example.internal", binary: "/opt/paladin/agent-host" });
  const command = hostCommand(value, "/local/agent-host");
  expect(command.command).toBe("ssh");
  expect(command.args).toContain("BatchMode=yes");
  expect(command.args).toContain("StrictHostKeyChecking=yes");
  expect(command.args.slice(-2)).toEqual(["dev@example.internal", "exec '/opt/paladin/agent-host'"]);
});

test("remote connection rejects command injection in target and path", () => {
  expect(() => validateConnection({ mode: "ssh", target: "host;touch /tmp/pwn", binary: "/bin/host" })).toThrow();
  expect(() => validateConnection({ mode: "ssh", target: "-oProxyCommand=evil", binary: "/bin/host" })).toThrow();
  expect(() => validateConnection({ mode: "ssh", target: "host", binary: "agent-host" })).toThrow();
  expect(() => validateConnection({ mode: "ssh", target: "host", binary: "/bin/host\ncmd" })).toThrow();
  const command = hostCommand(validateConnection({ mode: "ssh", target: "host", binary: "/path/it's/host" }), "local");
  expect(command.args.at(-1)).toBe("exec '/path/it'\\''s/host'");
});

test("an SSH target never starts with a dash, on either side of the @", () => {
  for (const target of ["-v", "-J", "-user@host", "user@-host", "@host", "user@", "a@b@c"]) {
    expect(() => validateConnection({ mode: "ssh", target, binary: "/bin/host" }), target).toThrow();
  }
  for (const target of ["host", "dev@example.internal", "u_1@h-2.example.com", "10.0.0.5"]) {
    expect(validateConnection({ mode: "ssh", target, binary: "/bin/host" })).toMatchObject({ target });
  }
});

test("the host does not inherit the app's Electron or Node-injection variables", () => {
  const base = {
    PATH: "/usr/bin",
    OPENAI_API_KEY: "kept-for-the-host",
    NODE_OPTIONS: "--require /tmp/inject.js",
    ELECTRON_START_URL: "http://localhost:5173",
    ELECTRON_ENABLE_LOGGING: "1",
    ELECTRON_RUN_AS_NODE: "1",
  };
  for (const connection of [{ mode: "local" } as const, { mode: "ssh", target: "h", binary: "/b" } as const]) {
    const env = hostEnv(connection, base, "/app/electron");
    expect(env.PATH).toBe("/usr/bin");
    expect(env.OPENAI_API_KEY).toBe("kept-for-the-host");
    for (const name of ["NODE_OPTIONS", "ELECTRON_START_URL", "ELECTRON_ENABLE_LOGGING", "ELECTRON_RUN_AS_NODE"]) {
      expect(env[name], name).toBeUndefined();
    }
  }
  expect(hostEnv({ mode: "local" }, base, "/app/electron").ELECTRON_NODE).toBe("/app/electron");
});
