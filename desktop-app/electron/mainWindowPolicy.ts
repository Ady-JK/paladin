// The app shell owns the privileged preload. Keep that preload on the
// original application page, even when a link or a dropped URL navigates.
export function appNavigationAllowed(initialURL: string, requestedURL: string): boolean {
  try {
    const initial = new URL(initialURL);
    const requested = new URL(requestedURL);
    if (initial.protocol === "file:") {
      return (
        requested.protocol === "file:" && requested.host === initial.host && requested.pathname === initial.pathname
      );
    }
    return (initial.protocol === "http:" || initial.protocol === "https:") && requested.origin === initial.origin;
  } catch {
    return false;
  }
}

// Compare Electron object identity, not URLs: an iframe or guest can have
// the same origin while still being a different IPC caller.
export function isMainWindowFrame(
  sender: unknown,
  senderFrame: unknown,
  mainContents: unknown,
  mainFrame: unknown,
): boolean {
  return mainContents != null && mainFrame != null && sender === mainContents && senderFrame === mainFrame;
}

// Frames may navigate only to the local visual page or the validated
// plugin protocol. The protocol host checks every path before serving it.
export function appFrameNavigationAllowed(appURL: string, requestedURL: string): boolean {
  if (requestedURL === "about:blank") return true;
  try {
    const requested = new URL(requestedURL);
    if (requested.protocol === "plugin:" && requested.hostname && !requested.username && !requested.password) {
      return true;
    }
    return requestedURL === new URL("visual-frame.html", appURL).href;
  } catch {
    return false;
  }
}
