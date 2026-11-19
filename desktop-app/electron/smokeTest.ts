// Headless smoke (--smoke-test): load the window, ping the sidecar over
// JSONL, report, and exit non-zero on any failure. No other Electron state
// is touched, so main.ts only hands over the window and the sidecar.
import type { BrowserWindow } from "electron";
import type { Sidecar } from "./sidecar";

export function runWindowSmoke(window: BrowserWindow | null, agent: Sidecar): void {
  // Never dereference the mutable global `win` from a delayed
  // callback: closing the window during a retry used to throw
  // "Cannot read properties of null (reading 'webContents')".
  const smokeWin = window;
  if (!smokeWin) {
    console.error("[smoke] FAIL: no window to test");
    process.exit(1);
  }
  // Electron emits "closed" while process.exit tears the window down,
  // so a passing run must not be turned into a failure by its own
  // shutdown.
  let finished = false;
  const fail = (msg: string): never => {
    finished = true;
    console.error(`[smoke] FAIL: ${msg}`);
    process.exit(1);
  };
  const timer = setTimeout(() => fail("timed out waiting for load + ping"), 25000);
  const gone = (): boolean => smokeWin.isDestroyed();
  const execute = (code: string): Promise<unknown> => {
    if (gone()) return Promise.reject(new Error("window closed"));
    return smokeWin.webContents.executeJavaScript(code);
  };
  smokeWin.once("closed", () => {
    clearTimeout(timer);
    if (!finished) fail("window closed before the smoke test finished");
  });
  smokeWin.webContents.on("did-fail-load", (_e, code, desc) => fail(`renderer load failed ${code}: ${desc}`));
  smokeWin.webContents.on("console-message", (_e, _level, message) => {
    if (/preload bridge missing|Uncaught/.test(message)) fail(`renderer console: ${message}`);
  });
  smokeWin.webContents.on("did-finish-load", () => {
    // The frame can be momentarily undisposed-ready right after load
    // (worse headless); poll briefly instead of failing on one shot.
    const probeBridge = (tries: number): void => {
      if (gone()) return;
      execute("typeof window.paladin")
        .then((t) => {
          if (gone()) return;
          if (t !== "object") {
            if (tries > 0) {
              setTimeout(() => probeBridge(tries - 1), 250);
            } else {
              fail(`window.paladin is ${String(t)} (preload bridge missing)`);
            }
          }
        })
        .catch((e) => {
          if (gone()) return;
          if (tries > 0) {
            setTimeout(() => probeBridge(tries - 1), 250);
          } else {
            fail(`bridge probe failed: ${e.message}`);
          }
        });
    };
    probeBridge(12);
    agent
      .invoke("ping")
      .then(async (r) => {
        if ((r as { ok?: unknown } | null)?.ok !== true) {
          fail(`unexpected ping result: ${JSON.stringify(r)}`);
        }
        const bridged = await execute('window.paladin.invoke("ping")');
        if ((bridged as { ok?: unknown } | null)?.ok !== true) fail("preload bridge ping failed");
        clearTimeout(timer);
        finished = true;
        console.log("[smoke] PASS: renderer loaded, preload bridge and agent-host ping ok");
        process.exit(0);
      })
      .catch((e) => fail(`ping failed: ${e.message}`));
  });
}
