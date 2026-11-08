import { $ } from "../../core/dom";

export function wireResize(
  handle: HTMLElement,
  container: HTMLElement,
  property: string,
  fromRight: boolean,
  min: number,
  max: number,
): void {
  const update = (width: number): void => {
    container.style.setProperty(property, `${Math.max(min, Math.min(max, width))}px`);
    window.dispatchEvent(new Event("resize"));
  };
  handle.addEventListener("pointerdown", (event) => {
    if (event.button !== 0) return;
    handle.setPointerCapture(event.pointerId);
  });
  handle.addEventListener("pointermove", (event) => {
    if (!handle.hasPointerCapture(event.pointerId)) return;
    const bounds = container.getBoundingClientRect();
    update(fromRight ? bounds.right - event.clientX : event.clientX - bounds.left);
  });
  handle.addEventListener("keydown", (event) => {
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
    event.preventDefault();
    const current = fromRight
      ? $("#agentMain").getBoundingClientRect().width
      : $(".ide-explorer").getBoundingClientRect().width;
    const delta = event.key === "ArrowRight" ? 16 : -16;
    update(current + (fromRight ? -delta : delta));
  });
}
