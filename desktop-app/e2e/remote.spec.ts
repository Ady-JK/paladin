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
