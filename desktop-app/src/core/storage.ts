// Saved UI preferences live in localStorage. Storage can be disabled or
// full, and saved data can be stale or hand-edited, so every read falls
// back to "nothing saved" and every write is best-effort: a preference
// that cannot be saved still applies to this window.

export function loadText(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function saveText(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Best-effort by design; see the header.
  }
}

// loadJson returns the parsed value, or undefined when nothing usable is saved.
export function loadJson(key: string): unknown {
  const raw = loadText(key);
  if (raw === null) {
    return undefined;
  }
  try {
    return JSON.parse(raw);
  } catch {
    return undefined;
  }
}

export function saveJson(key: string, value: unknown): void {
  saveText(key, JSON.stringify(value));
}
