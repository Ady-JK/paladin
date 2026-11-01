import { expect, test } from "@playwright/test";
import { hostCommand, validateConnection } from "../electron/remote";

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
