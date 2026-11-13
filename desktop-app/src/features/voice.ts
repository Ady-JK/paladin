// voice.ts is push-to-talk dictation into the composer.
//
// Hold the microphone button (or Space/Enter while it has focus): audio is
// captured at 16 kHz, streamed to the Moonshine helper in Electron main, and
// the live transcript is shown above the composer. Releasing the button
// inserts the finished text at the caret. Escape cancels, and the finished
// lines are never sent automatically.

import { bridge, hasBridge, type VoiceEvent, type VoiceModel } from "../core/bridge";
import { $, inputEl } from "../core/dom";
import { toast } from "../ui/toast";

const micBtn = $<HTMLButtonElement>("#voiceBtn");
const menuBtn = $<HTMLButtonElement>("#voiceMenuBtn");
const menuEl = $("#voiceMenu");
const menuListEl = $("#voiceMenuList");
const folderBtn = $<HTMLButtonElement>("#voiceModelsFolder");
const statusEl = $("#voiceStatus");
const statusLabelEl = $("#voiceStatusLabel");
const statusTextEl = $("#voiceStatusText");
const statusTimeEl = $("#voiceStatusTime");
const cancelBtn = $<HTMLButtonElement>("#voiceCancelBtn");

const STORE_KEY = "paladin.voice.model";
const SAMPLE_RATE = 16000;
const CHUNK_SAMPLES = SAMPLE_RATE / 10; // 100 ms
const STOP_TIMEOUT_MS = 20_000;

type Phase = "idle" | "starting" | "listening" | "stopping";

let models: VoiceModel[] = [];
let selected = "";
let phase: Phase = "idle";
let pendingAction: "stop" | "cancel" | null = null;
let discard = false;
let startedAt = 0;
let timer = 0;
let stopTimer = 0;
let selection = { start: 0, end: 0 };
let stream: MediaStream | null = null;
let context: AudioContext | null = null;
let source: MediaStreamAudioSourceNode | null = null;
let processor: ScriptProcessorNode | null = null;
let carry = new Float32Array(0);
const lines = new Map<number, { text: string; complete: boolean }>();

function voiceApi(): NonNullable<ReturnType<typeof bridge>["voice"]> | null {
  if (!hasBridge()) return null;
  return bridge().voice ?? null;
}

function selectedModel(): VoiceModel | undefined {
  return models.find((model) => model.id === selected);
}

function paintButton(): void {
  const model = selectedModel();
  micBtn.disabled = !voiceApi() || models.length === 0 || phase === "stopping";
  menuBtn.disabled = !voiceApi() || models.length === 0;
  micBtn.title = model ? `Hold to dictate · ${model.label}` : "Hold to dictate";
  micBtn.setAttribute("aria-pressed", phase === "listening" ? "true" : "false");
  micBtn.classList.toggle("is-recording", phase === "listening" || phase === "starting");
}

function renderMenu(): void {
  menuListEl.replaceChildren();
  for (const model of models) {
    const item = document.createElement("button");
    item.type = "button";
    item.className = "voice-menu-item";
    item.classList.toggle("is-selected", model.id === selected);
    item.dataset.model = model.id;
    const check = document.createElement("span");
    check.className = "voice-menu-check";
    check.textContent = model.id === selected ? "✓" : "";
    const text = document.createElement("span");
    text.className = "voice-menu-text";
    const label = document.createElement("span");
    label.className = "voice-menu-label";
    label.textContent = model.label;
    text.append(label);
    if (model.description) {
      const detail = document.createElement("span");
      detail.className = "voice-menu-detail";
      detail.textContent = model.description;
      text.append(detail);
    }
    item.append(check, text);
    item.addEventListener("click", () => {
      selected = model.id;
      localStorage.setItem(STORE_KEY, selected);
      paintButton();
      renderMenu();
      closeMenu();
    });
    menuListEl.append(item);
  }
}

async function refreshModels(): Promise<void> {
  const api = voiceApi();
  if (!api) return;
  try {
    const overview = await api.overview();
    if (!overview.available) return;
    models = overview.models;
    if (!models.some((model) => model.id === selected)) {
      selected = models[0]?.id ?? "";
      if (selected) localStorage.setItem(STORE_KEY, selected);
    }
    paintButton();
  } catch {
    // Keep the list we already have.
  }
}

function openMenu(): void {
  void refreshModels().then(() => {
    renderMenu();
    menuEl.hidden = false;
    menuBtn.setAttribute("aria-expanded", "true");
  });
}

function closeMenu(): void {
  menuEl.hidden = true;
  menuBtn.setAttribute("aria-expanded", "false");
}

function setStatus(label: string, detail = ""): void {
  statusLabelEl.textContent = label;
  statusTextEl.textContent = detail;
  statusTextEl.hidden = !detail;
}

function showStatus(): void {
  statusEl.hidden = false;
}

function hideStatus(): void {
  statusEl.hidden = true;
}

function paintClock(): void {
  if (!startedAt) return;
  const seconds = Math.max(0, Math.floor((Date.now() - startedAt) / 1000));
  statusTimeEl.textContent = `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

function paintTranscript(): void {
  const ordered = [...lines.entries()].sort((a, b) => a[0] - b[0]);
  const text = ordered
    .map(([, line]) => line.text.trim())
    .filter(Boolean)
    .join(" ");
  if (phase === "stopping") setStatus("Transcribing…", text);
  else setStatus("Listening…", text);
  statusTimeEl.hidden = phase !== "listening" && phase !== "starting";
}

function startClock(): void {
  startedAt = Date.now();
  paintClock();
  window.clearInterval(timer);
  timer = window.setInterval(paintClock, 250);
}

function stopClock(): void {
  window.clearInterval(timer);
  timer = 0;
  startedAt = 0;
  statusTimeEl.textContent = "0:00";
}

function flushAudio(): void {
  if (carry.length === 0) return;
  voiceApi()?.audio(carry);
  carry = new Float32Array(0);
}

function startCapture(): void {
  if (!stream || !context) return;
  source = context.createMediaStreamSource(stream);
  // ScriptProcessor is deprecated but needs no extra module URL, and its
  // 64 ms callback is well inside the budget for copying a buffer.
  processor = context.createScriptProcessor(1024, 1, 1);
  processor.onaudioprocess = (event) => {
    if (phase !== "listening") return;
    const input = event.inputBuffer.getChannelData(0);
    const merged = new Float32Array(carry.length + input.length);
    merged.set(carry);
    merged.set(input, carry.length);
    let offset = 0;
    while (merged.length - offset >= CHUNK_SAMPLES) {
      voiceApi()?.audio(merged.slice(offset, offset + CHUNK_SAMPLES));
      offset += CHUNK_SAMPLES;
    }
    carry = merged.slice(offset);
  };
  source.connect(processor);
  processor.connect(context.destination);
}

function stopCapture(): void {
  flushAudio();
  processor?.disconnect();
  processor = null;
  source?.disconnect();
  source = null;
  stream?.getTracks().forEach((track) => track.stop());
  stream = null;
  const current = context;
  context = null;
  if (current) void current.close();
}

function insertText(text: string): void {
  const cleaned = text.trim();
  if (!cleaned) return;
  inputEl.focus();
  const value = inputEl.value;
  const start = Math.min(selection.start, value.length);
  const end = Math.min(Math.max(selection.end, start), value.length);
  const before = value.slice(0, start);
  const after = value.slice(end);
  const lead = before && !/\s$/.test(before) ? " " : "";
  const trail = after && !/^\s/.test(after) ? " " : "";
  const inserted = `${lead}${cleaned}${trail}`;
  inputEl.setSelectionRange(start, end);
  if (!document.execCommand("insertText", false, inserted)) {
    inputEl.setRangeText(inserted, start, end, "end");
    inputEl.dispatchEvent(new Event("input", { bubbles: true }));
  }
  inputEl.scrollTop = inputEl.scrollHeight;
}

function finishedText(): string {
  return [...lines.entries()]
    .sort((a, b) => a[0] - b[0])
    .map(([, line]) => line.text.trim())
    .filter(Boolean)
    .join(" ");
}

function reset(): void {
  phase = "idle";
  pendingAction = null;
  discard = false;
  lines.clear();
  stopCapture();
  stopClock();
  window.clearTimeout(stopTimer);
  stopTimer = 0;
  paintButton();
  hideStatus();
}

function finishStop(): void {
  phase = "stopping";
  stopCapture();
  stopClock();
  setStatus("Transcribing…", finishedText());
  showStatus();
  paintButton();
  void voiceApi()?.stop();
  window.clearTimeout(stopTimer);
  stopTimer = window.setTimeout(() => {
    if (phase !== "stopping") return;
    toast("Dictation timed out", "error");
    reset();
  }, STOP_TIMEOUT_MS);
}

function stop(): void {
  if (phase === "starting") {
    pendingAction = "stop";
    return;
  }
  if (phase !== "listening") return;
  finishStop();
}

function cancel(): void {
  if (phase === "idle") return;
  discard = true;
  if (phase === "starting") {
    pendingAction = "cancel";
    return;
  }
  const api = voiceApi();
  stopCapture();
  void api?.cancel();
  reset();
}

async function start(): Promise<void> {
  const api = voiceApi();
  if (!api || phase !== "idle" || !selected) return;
  discard = false;
  lines.clear();
  selection = {
    start: inputEl.selectionStart ?? inputEl.value.length,
    end: inputEl.selectionEnd ?? inputEl.value.length,
  };
  phase = "starting";
  pendingAction = null;
  paintButton();
  setStatus("Starting…");
  showStatus();
  try {
    await api.start(selected);
    if (pendingAction === "cancel") {
      void api.cancel();
      reset();
      return;
    }
    stream = await navigator.mediaDevices.getUserMedia({
      audio: {
        channelCount: 1,
        echoCancellation: false,
        noiseSuppression: false,
        autoGainControl: true,
      },
    });
    if (pendingAction === "cancel") {
      stopCapture();
      void api.cancel();
      reset();
      return;
    }
    context = new AudioContext({ sampleRate: SAMPLE_RATE });
    await context.resume();
    startCapture();
    phase = "listening";
    startClock();
    paintButton();
    paintTranscript();
    if (pendingAction === "stop") {
      finishStop();
      return;
    }
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    if (!discard) toast(message, "error");
    reset();
  }
}

function handleEvent(event: VoiceEvent): void {
  switch (event.event) {
    case "line": {
      if (discard || event.index === undefined) return;
      if (phase !== "listening" && phase !== "stopping") return;
      lines.set(event.index, { text: event.text ?? "", complete: !!event.complete });
      paintTranscript();
      return;
    }
    case "stopped": {
      if (phase !== "stopping") return;
      const text = finishedText();
      reset();
      insertText(text);
      return;
    }
    case "cancelled": {
      // A cancel from the take we just left can land after a new start.
      if (phase === "starting" && pendingAction !== "cancel") return;
      reset();
      return;
    }
    case "error": {
      if (discard || phase === "idle") return;
      toast(event.message || "Dictation failed", "error");
      reset();
      return;
    }
    default:
      return;
  }
}

async function initVoice(): Promise<void> {
  const api = voiceApi();
  if (!api || typeof navigator.mediaDevices?.getUserMedia !== "function") {
    micBtn.disabled = true;
    menuBtn.disabled = true;
    micBtn.title = "Dictation needs a microphone and the desktop app";
    return;
  }
  let overview: Awaited<ReturnType<typeof api.overview>>;
  try {
    overview = await api.overview();
  } catch (error) {
    micBtn.disabled = true;
    menuBtn.disabled = true;
    micBtn.title = `Dictation is unavailable: ${String(error)}`;
    return;
  }
  models = overview.models;
  if (!overview.available || models.length === 0) {
    micBtn.disabled = true;
    menuBtn.disabled = true;
    micBtn.title = overview.available
      ? "No dictation model is installed (see `make voice`)"
      : "The dictation helper is missing (run `make voice`)";
    return;
  }
  const saved = localStorage.getItem(STORE_KEY);
  selected = saved && models.some((model) => model.id === saved) ? saved : models[0].id;
  localStorage.setItem(STORE_KEY, selected);
  api.onEvent(handleEvent);
  paintButton();

  micBtn.addEventListener("pointerdown", (event) => {
    event.preventDefault();
    if (phase !== "idle") return;
    try {
      micBtn.setPointerCapture(event.pointerId);
    } catch {
      // Synthetic events (e2e) have no active pointer to capture.
    }
    void start();
  });
  window.addEventListener("pointerup", () => {
    if (phase === "starting" || phase === "listening") stop();
  });
  window.addEventListener("pointercancel", () => cancel());
  window.addEventListener("blur", () => {
    if (phase !== "idle") cancel();
  });
  micBtn.addEventListener("keydown", (event) => {
    if ((event.key === " " || event.key === "Enter") && !event.repeat) {
      event.preventDefault();
      void start();
    }
  });
  micBtn.addEventListener("keyup", (event) => {
    if (event.key === " " || event.key === "Enter") {
      event.preventDefault();
      stop();
    }
  });
  cancelBtn.addEventListener("click", () => cancel());
  menuBtn.addEventListener("click", (event) => {
    event.stopPropagation();
    if (menuEl.hidden) openMenu();
    else closeMenu();
  });
  folderBtn.addEventListener("click", () => {
    closeMenu();
    void api.openFolder().catch((error: unknown) => toast(String(error), "error"));
  });
  document.addEventListener("click", (event) => {
    if (menuEl.hidden) return;
    const target = event.target as Node;
    if (!menuEl.contains(target) && !menuBtn.contains(target)) closeMenu();
  });
  document.addEventListener(
    "keydown",
    (event) => {
      if (event.key !== "Escape" || phase === "idle") return;
      event.preventDefault();
      event.stopPropagation();
      cancel();
    },
    true,
  );
}

export function initVoiceFeature(): void {
  void initVoice();
}
