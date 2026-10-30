// popover.ts keeps the composer popovers mutually exclusive: each one
// registers its close function, and opening one closes the rest.

const closers = new Map<string, () => void>();

export function registerPopover(id: string, close: () => void): void {
  closers.set(id, close);
}

// claimPopover closes every registered popover except id.
export function claimPopover(id: string): void {
  for (const [other, close] of closers) {
    if (other !== id) close();
  }
}
