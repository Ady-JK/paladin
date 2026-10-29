// motion.ts owns the animation level. The preference is System (follow
// the OS reduce-motion setting), Full, Reduced or Off; the effective
// level lands on <html data-motion> and every animation rule keys off it.

export type MotionPref = "system" | "full" | "reduced" | "off";
export type MotionLevel = "full" | "reduced" | "off";

const KEY = "paladin.motion";
const PREFS: MotionPref[] = ["system", "full", "reduced", "off"];

const osReduce = window.matchMedia?.("(prefers-reduced-motion: reduce)");

export function motionPref(): MotionPref {
  try {
    const raw = localStorage.getItem(KEY) as MotionPref | null;
    return raw && PREFS.includes(raw) ? raw : "system";
  } catch {
    return "system";
  }
}

export function motionLevel(): MotionLevel {
  const pref = motionPref();
  if (pref !== "system") {
    return pref;
  }
  return osReduce?.matches ? "reduced" : "full";
}

export function applyMotion(): void {
  document.documentElement.dataset.motion = motionLevel();
}

export function setMotionPref(pref: MotionPref): void {
  try {
    localStorage.setItem(KEY, pref);
  } catch {
  }
  applyMotion();
}

// motionMs scales a JS-timed animation (close fades and the like) to
// the current level: off is instant, reduced is short.
export function motionMs(ms: number): number {
  const level = motionLevel();
  return level === "off" ? 0 : level === "reduced" ? Math.min(ms, 80) : ms;
}

export function initMotion(): void {
  applyMotion();
  osReduce?.addEventListener?.("change", applyMotion);
}
