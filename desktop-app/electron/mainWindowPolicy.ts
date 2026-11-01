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

// Frames inside the app window are visuals, each in the app's own
// visual-frame.html. They may never navigate anywhere else: a meta
// refresh or a link inside one would otherwise reach the network, which
// their CSP cannot stop.
export function appFrameNavigationAllowed(appURL: string, requestedURL: string): boolean {
  if (requestedURL === "about:blank") return true;
  try {
    return requestedURL === new URL("visual-frame.html", appURL).href;
  } catch {
    return false;
  }
}
