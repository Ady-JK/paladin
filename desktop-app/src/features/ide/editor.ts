import { basicSetup, EditorView } from "codemirror";
import { Compartment, EditorState } from "@codemirror/state";
import { keymap, type ViewUpdate } from "@codemirror/view";
import { indentWithTab } from "@codemirror/commands";
import { oneDark } from "@codemirror/theme-one-dark";
import { javascript } from "@codemirror/lang-javascript";
import { python } from "@codemirror/lang-python";
import { go } from "@codemirror/lang-go";
import { markdown } from "@codemirror/lang-markdown";
import { json } from "@codemirror/lang-json";
import { html } from "@codemirror/lang-html";
import { css } from "@codemirror/lang-css";

export const theme = new Compartment();

export function language(path: string) {
  const lower = path.toLowerCase();
  if (/\.(tsx|jsx|ts|js|mjs|cjs)$/.test(lower))
    return javascript({ typescript: /\.tsx?$/.test(lower), jsx: /\.[jt]sx$/.test(lower) });
  if (/\.pyi?$/.test(lower)) return python();
  if (lower.endsWith(".go")) return go();
  if (/\.(md|mdx)$/.test(lower)) return markdown();
  if (lower.endsWith(".json")) return json();
  if (/\.(html|htm)$/.test(lower)) return html();
  if (lower.endsWith(".css")) return css();
  return null;
}

export function createIDEEditorState(
  path: string,
  content: string,
  onUpdate: (update: ViewUpdate) => void,
): EditorState {
  return EditorState.create({
    doc: content,
    extensions: [
      basicSetup,
      keymap.of([indentWithTab]),
      language(path) ?? [],
      theme.of(document.documentElement.classList.contains("dark") ? oneDark : []),
      EditorView.contentAttributes.of({ "aria-label": "File editor", spellcheck: "false" }),
      EditorView.updateListener.of(onUpdate),
    ],
  });
}
