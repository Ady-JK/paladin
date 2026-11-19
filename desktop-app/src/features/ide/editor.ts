import { basicSetup, EditorView } from "codemirror";
import { Compartment, EditorState } from "@codemirror/state";
import { keymap, type ViewUpdate } from "@codemirror/view";
import { indentWithTab } from "@codemirror/commands";
import { oneDark } from "@codemirror/theme-one-dark";
import { language } from "./language";

export const theme = new Compartment();

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
