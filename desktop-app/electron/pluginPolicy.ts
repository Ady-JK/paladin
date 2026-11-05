// pluginPolicy.ts holds the pure allow-list rules for the plugin bridge.
// Keeping them out of main.ts lets the allow/deny matrix run in plain
// Node under Playwright.

// READ_ONLY_TOOLS are safe for any view that may invoke tools at all.
const READ_ONLY_TOOLS = new Set(["read", "grep", "glob", "list"]);

// toolAllowed decides whether a view may call a tool. A view can always
// call its own plugin's tools and the read-only built-ins; everything
// else needs the destructive permission. Main also refuses a tool the
// sidecar no longer has.
export function toolAllowed(permissions: readonly string[], plugin: string, tool: string): boolean {
  const canInvoke = permissions.includes("invokeTool") || permissions.includes("invokeToolDestructive");
  if (!canInvoke) return false;
  if (tool.startsWith(`extension__${plugin}__`)) return true;
  if (READ_ONLY_TOOLS.has(tool)) return true;
  return permissions.includes("invokeToolDestructive");
}

// storeKeyValid matches the keys a plugin may use in its own store.
export function storeKeyValid(key: string): boolean {
  return /^[a-zA-Z0-9_.-]{1,64}$/.test(key);
}
