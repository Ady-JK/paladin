// electron-builder afterPack hook (see "afterPack" in electron-builder.json).
//
// Chromium's sandbox on Linux needs the SUID helper to be root-owned with mode
// 4755. Ubuntu 24.04+ denies the unprivileged user-namespace sandbox via
// AppArmor, so Electron falls back to the setuid helper and aborts with
// "The SUID sandbox helper binary was found, but is not configured correctly"
// unless the bit is set. electron-builder never sets it, so fix it in the
// unpacked dir before the deb is assembled; dpkg installs the mode recorded
// here and chowns every path to root.
import fs from "node:fs";
import path from "node:path";

/**
 * @param {import("electron-builder").AfterPackContext} context
 * @returns {void}
 */
export function afterPack(context) {
  if (context.electronPlatformName !== "linux") return;
  const helper = path.join(context.appOutDir, "chrome-sandbox");
  if (!fs.existsSync(helper)) return;
  fs.chmodSync(helper, 0o4755);
}

export default afterPack;
