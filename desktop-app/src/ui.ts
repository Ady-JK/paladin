export function toast(msg: string, kind: "error" | "info" = "info"): void {
  const box = document.querySelector("#toast")!;
  const el = document.createElement("div");
  el.className = `toast ${kind}`;
  el.textContent = msg;
  box.append(el);
  setTimeout(() => el.remove(), 4000);
}

export function setStatus(msg: string): void {
  document.querySelector("#statusLine")!.textContent = msg;
}
