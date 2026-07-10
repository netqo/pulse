// Wires monaco to the bundled npm package and a locally built web worker instead
// of the default CDN loader. This keeps the app fully self-hosted (no external
// network requests) and compatible with a strict Content-Security-Policy.
//
// We import the editor API directly and register only the SQL language, rather
// than the package root (which pulls in every built-in language). That keeps the
// bundle to the editor core plus SQL, and needs only the base editor worker.
import { loader } from '@monaco-editor/react';
import * as monaco from 'monaco-editor/esm/vs/editor/editor.api';
import 'monaco-editor/esm/vs/basic-languages/sql/sql.contribution';
import editorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker';

declare global {
  interface Window {
    MonacoEnvironment?: monaco.Environment;
  }
}

window.MonacoEnvironment = {
  getWorker: () => new editorWorker(),
};

// A dark theme whose chrome matches the app's dusty-mauve palette, inheriting
// vs-dark's syntax token colors. Hex values mirror the --color-dusty-mauve-*
// tokens in index.css (monaco themes cannot read CSS variables).
monaco.editor.defineTheme('pulse-dark', {
  base: 'vs-dark',
  inherit: true,
  rules: [],
  colors: {
    'editor.background': '#1d1618', // dusty-mauve-900
    'editor.foreground': '#f4f1f2', // dusty-mauve-50
    'editorGutter.background': '#1d1618',
    'editorLineNumber.foreground': '#735960', // dusty-mauve-600
    'editorLineNumber.activeForeground': '#bca9ae', // dusty-mauve-300
    'editor.lineHighlightBackground': '#3a2c3066', // dusty-mauve-800, translucent
    'editor.selectionBackground': '#56434899', // dusty-mauve-700, translucent
    'editorCursor.foreground': '#bca9ae', // dusty-mauve-300
    'editorWidget.background': '#1d1618',
    'editorWidget.border': '#564348', // dusty-mauve-700
  },
});

loader.config({ monaco });
