import Editor, { loader, type OnMount } from '@monaco-editor/react';
import * as monaco from 'monaco-editor';
import EditorWorker from 'monaco-editor/editor/editor.worker.js?worker';
import JSONWorker from 'monaco-editor/language/json/json.worker.js?worker';
import CSSWorker from 'monaco-editor/language/css/css.worker.js?worker';
import HTMLWorker from 'monaco-editor/language/html/html.worker.js?worker';
import TSWorker from 'monaco-editor/language/typescript/ts.worker.js?worker';
import { useTheme } from '../stores/theme';
self.MonacoEnvironment = {
  getWorker: (_id, label) => {
    if (label === 'json') return new JSONWorker();
    if (['css', 'scss', 'less'].includes(label)) return new CSSWorker();
    if (['html', 'handlebars', 'razor'].includes(label))
      return new HTMLWorker();
    if (['typescript', 'javascript'].includes(label)) return new TSWorker();
    return new EditorWorker();
  },
};
loader.config({ monaco });
export default function CodeEditor({
  path,
  value,
  onChange,
  readOnly = false,
  onMount,
  options,
  editorTheme,
  keepCurrentModel = false,
}: {
  path: string;
  value: string;
  onChange?: (value: string) => void;
  readOnly?: boolean;
  onMount?: OnMount;
  options?: monaco.editor.IStandaloneEditorConstructionOptions;
  editorTheme?: string;
  keepCurrentModel?: boolean;
}) {
  const { mode, systemDark } = useTheme();
  const dark = mode === 'dark' || (mode === 'system' && systemDark);
  return (
    <Editor
      path={path}
      value={value}
      onChange={(value) => onChange?.(value ?? '')}
      theme={editorTheme ?? (dark ? 'vs-dark' : 'vs')}
      onMount={onMount}
      keepCurrentModel={keepCurrentModel}
      options={{
        readOnly,
        minimap: { enabled: false },
        automaticLayout: true,
        scrollBeyondLastLine: false,
        ...options,
      }}
    />
  );
}
