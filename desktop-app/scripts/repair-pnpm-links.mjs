// A copied node_modules may contain pnpm hoisted links back to the old
// checkout. Retarget only broken links whose package exists locally.
import { existsSync, readdirSync, readlinkSync, symlinkSync, unlinkSync } from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

export function repairPnpmLinks(projectDir) {
  const store = path.resolve(projectDir, "node_modules", ".pnpm");
  const hoisted = path.join(store, "node_modules");
  if (!existsSync(hoisted)) return 0;
  let repaired = 0;
  const visit = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const link = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        visit(link);
      } else if (entry.isSymbolicLink() && !existsSync(link)) {
        const original = path.resolve(dir, readlinkSync(link));
        const marker = `${path.sep}node_modules${path.sep}.pnpm${path.sep}`;
        const offset = original.lastIndexOf(marker);
        if (offset < 0) continue;
        const target = path.resolve(store, original.slice(offset + marker.length));
        if (!target.startsWith(`${store}${path.sep}`) || !existsSync(target)) continue;
        unlinkSync(link);
        symlinkSync(process.platform === "win32" ? target : path.relative(dir, target), link, "junction");
        repaired++;
      }
    }
  };
  visit(hoisted);
  return repaired;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const count = repairPnpmLinks(process.cwd());
  if (count) process.stdout.write(`Repaired ${count} stale pnpm links\n`);
}
