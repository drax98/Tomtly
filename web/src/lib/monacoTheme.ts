import type * as Monaco from "monaco-editor";

// Hex versions of the page palette (Monaco can't read CSS variables or oklch), so the editor sits in the page instead of on it.
const base = (dark: boolean, c: Record<string, string>): Monaco.editor.IStandaloneThemeData => ({
  base: dark ? "vs-dark" : "vs",
  inherit: true,
  rules: [
    { token: "comment", foreground: c.comment.slice(1), fontStyle: "italic" },
    { token: "keyword", foreground: c.keyword.slice(1) },
    { token: "string", foreground: c.string.slice(1) },
    { token: "number", foreground: c.number.slice(1) },
    { token: "type", foreground: c.type.slice(1) },
    { token: "type.identifier", foreground: c.type.slice(1) },
  ],
  colors: {
    "editor.background": c.bg,
    "editor.foreground": c.fg,
    "editor.lineHighlightBackground": c.line,
    "editor.selectionBackground": c.sel,
    "editorLineNumber.foreground": c.gutter,
    "editorLineNumber.activeForeground": c.fg,
    "editorCursor.foreground": c.type,
    "editorWidget.background": c.bg,
    "editorWidget.border": c.border,
    "editorSuggestWidget.background": c.bg,
    "editorSuggestWidget.border": c.border,
    "editorIndentGuide.background1": c.border,
    "scrollbarSlider.background": `${c.gutter}55`,
  },
});

export const LIGHT = base(false, { bg: "#fdfcf8", fg: "#261d16", line: "#f6f3ec", sel: "#ecd8b6", comment: "#6b6157", keyword: "#92361f", string: "#0e5f36", number: "#0d5c91", type: "#794722", gutter: "#8c857c", border: "#dfdad2" });
export const DARK = base(true, { bg: "#1c1611", fg: "#ebe7e0", line: "#241e19", sel: "#4d391e", comment: "#a39e94", keyword: "#f79e75", string: "#7ad59c", number: "#82c7f0", type: "#e6bd77", gutter: "#777068", border: "#342c26" });
