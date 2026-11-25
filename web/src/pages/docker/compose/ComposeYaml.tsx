import { lazy, Suspense } from 'react';
import { Alert, Skeleton } from 'antd';
import { parseDocument } from 'yaml';

const CodeEditor = lazy(() => import('../../../components/CodeEditor'));

export function composeYamlError(yaml: string) {
  return parseDocument(yaml, { uniqueKeys: true }).errors[0]?.message;
}

export default function ComposeYaml({
  value,
  onChange,
  id,
  dark = false,
}: {
  value: string;
  onChange: (value: string) => void;
  id: string;
  dark?: boolean;
}) {
  const error = composeYamlError(value);
  return (
    <>
      <div className="compose-code-editor">
        <Suspense fallback={<Skeleton active />}>
          <CodeEditor
            path={`/${id}/compose.yaml`}
            value={value}
            editorTheme={dark ? 'vs-dark' : undefined}
            onChange={onChange}
            options={{
              tabSize: 2,
              insertSpaces: true,
              ariaLabel: 'Compose YAML',
            }}
          />
        </Suspense>
      </div>
      {error && <Alert type="error" message={error} showIcon />}
    </>
  );
}
