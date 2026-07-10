import Editor, { type OnMount } from '@monaco-editor/react';
import { useEffect, useRef } from 'react';

interface SqlEditorProps {
  value: string;
  onChange: (value: string) => void;
  onRun: () => void;
  disabled?: boolean;
}

/**
 * SqlEditor is the monaco-backed SQL input. Ctrl/Cmd+Enter runs the query,
 * matching the Run button (both are gated by `disabled`). The run handler and
 * disabled flag are held in refs so the keybinding, registered once on mount,
 * always sees the latest values rather than a stale closure.
 */
export function SqlEditor({ value, onChange, onRun, disabled = false }: SqlEditorProps) {
  const onRunRef = useRef(onRun);
  const disabledRef = useRef(disabled);
  useEffect(() => {
    onRunRef.current = onRun;
  }, [onRun]);
  useEffect(() => {
    disabledRef.current = disabled;
  }, [disabled]);

  const handleMount: OnMount = (editor, monaco) => {
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter, () => {
      if (!disabledRef.current) {
        onRunRef.current();
      }
    });
  };

  return (
    <Editor
      height="100%"
      defaultLanguage="sql"
      theme="pulse-dark"
      value={value}
      onChange={(next) => onChange(next ?? '')}
      onMount={handleMount}
      options={{
        ariaLabel: 'SQL query editor',
        minimap: { enabled: false },
        fontSize: 14,
        scrollBeyondLastLine: false,
        automaticLayout: true,
        tabSize: 2,
        renderLineHighlight: 'line',
      }}
    />
  );
}
