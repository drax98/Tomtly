import { useEffect, useRef } from "react";
import * as monaco from "monaco-editor";
import editorWorker from "monaco-editor/editor/editor.worker?worker";
import tsWorker from "monaco-editor/language/typescript/ts.worker?worker";
import { api } from "@/api";
import { DARK, LIGHT } from "@/lib/monacoTheme";

// Bundled rather than loaded from a CDN: the panel has to work on a home network with no internet.
self.MonacoEnvironment = {
  getWorker: (_id, label) => (label === "typescript" || label === "javascript" ? new tsWorker() : new editorWorker()),
};

const ts = monaco.typescript;

ts.typescriptDefaults.setCompilerOptions({
  target: ts.ScriptTarget.ES2017,
  lib: ["lib.es2017.d.ts"], // the file name: this option skips the shorthand tsc accepts
  module: ts.ModuleKind.ESNext,
  moduleResolution: ts.ModuleResolutionKind.NodeJs,
  strict: true,
  noEmit: true,
});

/** Teaches the editor the `home` API, and the real devices and plugins as they are right now. */
async function loadSDK() {
  try {
    const libs = Object.entries(await api.sdk()).map(([path, content]) => ({ content, filePath: `file:///node_modules/@tomtly/${path}` }));
    ts.typescriptDefaults.setExtraLibs(libs);
    const [devices, rooms] = await Promise.all([api.devices(), api.rooms()]);
    // Names aren't unique, as with a window sensor in every room, so the room tells them apart.
    const roomOf = (id: string) => Object.entries(rooms).find(([, ids]) => ids.includes(id))?.[0];
    deviceNames = devices.map((d) => ({ id: d.id, label: roomOf(d.id) ? `${d.name} · ${roomOf(d.id)}` : d.name }));
  } catch {
    // The editor still works, without completion; the next open tries again.
  }
}

const OFFERS_NAMES = /(\.(get|set|action)|home\.(device|plugin))\(["']$/;

let deviceNames: { id: string; label: string }[] = [];

// Device ids are unreadable, so inside home.device("...") the list shows names too, and you can type a name to find one.
monaco.languages.registerCompletionItemProvider("typescript", {
  triggerCharacters: ['"', "'"],
  provideCompletionItems(model, position) {
    const before = model.getValueInRange(new monaco.Range(position.lineNumber, 1, position.lineNumber, position.column));
    const typed = /home\.device\(["']([^"']*)$/.exec(before)?.[1];
    if (typed === undefined) return { suggestions: [] };
    const range = new monaco.Range(position.lineNumber, position.column - typed.length, position.lineNumber, position.column);
    return {
      suggestions: deviceNames.map(({ id, label }) => ({
        label: { label, description: id },
        kind: monaco.languages.CompletionItemKind.Value,
        insertText: id,
        filterText: `${label} ${id}`,
        sortText: `0${label}`,
        range,
      })),
    };
  },
});

monaco.editor.defineTheme("home-light", LIGHT);
monaco.editor.defineTheme("home-dark", DARK);

const theme = () => (window.matchMedia("(prefers-color-scheme: dark)").matches ? "home-dark" : "home-light");

interface Props {
  value: string;
  onChange: (value: string) => void;
  onSave: () => void;
}

export default function MonacoEditor({ value, onChange, onSave }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const onSaveRef = useRef(onSave);
  onSaveRef.current = onSave;

  useEffect(() => {
    void loadSDK();
    // A file path rather than Monaco's default in-memory one, which module lookup can't start from.
    const model = monaco.editor.createModel(value, "typescript", monaco.Uri.parse("file:///automations/script.ts"));
    const instance = monaco.editor.create(host.current!, {
      model,
      theme: theme(),
      automaticLayout: true,
      minimap: { enabled: false },
      fontSize: 14,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
      padding: { top: 12, bottom: 12 },
      tabSize: 2,
      scrollBeyondLastLine: false,
      // Off inside strings by default, which is exactly where device ids, codes and action names go.
      quickSuggestions: { other: true, comments: false, strings: true },
    });
    instance.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => onSaveRef.current());
    const changes = instance.onDidChangeModelContent((event) => {
      onChangeRef.current(instance.getValue());
      // TypeScript only offers these once a letter is typed, but the whole point is not knowing the first letter.
      if (!event.changes.some((c) => /^["']/.test(c.text))) return;
      // The cursor moves after this event, so look once it has.
      setTimeout(() => {
        const position = instance.getPosition();
        if (!position || model.isDisposed()) return;
        const before = model.getLineContent(position.lineNumber).slice(0, position.column - 1);
        if (OFFERS_NAMES.test(before)) instance.trigger("tomtly", "editor.action.triggerSuggest", {});
      });
    });

    const dark = window.matchMedia("(prefers-color-scheme: dark)");
    const followTheme = () => monaco.editor.setTheme(theme());
    dark.addEventListener("change", followTheme);
    return () => {
      dark.removeEventListener("change", followTheme);
      changes.dispose();
      instance.dispose();
      model.dispose();
    };
    // The editor owns the text once created. Feeding every change back in would overwrite what's being typed with
    // React's slightly older copy; to load a different text, the parent remounts this component with a new key.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return <div ref={host} aria-label="Script source" className="min-h-72 flex-1 overflow-hidden rounded-xl border" />;
}
