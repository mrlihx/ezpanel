import { useState } from 'react';
import { App, Button, Flex, Input, Segmented, Space } from 'antd';
import { Minus, Plus } from 'lucide-react';

type EnvRow = { id: number; line: string; key?: string; value?: string };
const assignment =
  /^(\s*(?:export\s+)?)([a-zA-Z_][a-zA-Z0-9_]*)(\s*[=:]\s*)(.*)$/;
const envName = /^[a-zA-Z_][a-zA-Z0-9_]*$/;
const closingQuote = /^(?:[^'\\]|\\.)*'/;

function envRows(text: string) {
  let quoted = false;
  return (text ? text.split(/\r?\n/) : []).map((line, id) => {
    // 多行单引号值保留原样，避免把值中的 KEY=value 识别成变量。
    const match = quoted ? null : line.match(assignment);
    const value = match?.[4];
    if (quoted) quoted = !closingQuote.test(line);
    else if (value?.startsWith("'") && !closingQuote.test(value.slice(1))) {
      quoted = true;
      return { id, line };
    }
    return {
      id,
      line,
      key: match?.[2],
      value: match?.[4],
    };
  });
}

export default function ComposeEnvEditor({
  value,
  onChange,
  readOnly = false,
  masked = false,
}: {
  value: string;
  onChange: (value: string) => void;
  readOnly?: boolean;
  masked?: boolean;
}) {
  const { message } = App.useApp();
  const [rows, setRows] = useState<EnvRow[]>();
  const [nextId, setNextId] = useState(0);
  const table = !!rows && !masked;
  const setTable = (enabled: boolean) => {
    if (enabled) {
      const parsed = envRows(value);
      setNextId(parsed.length);
      setRows(parsed);
    } else {
      setRows(undefined);
    }
  };
  const edit = (nextRows: EnvRow[]) => {
    setRows(nextRows);
    onChange(
      nextRows
        .map((row) => row.line)
        .join(value.includes('\r\n') ? '\r\n' : '\n'),
    );
  };
  const editRow = (row: EnvRow, part: 'key' | 'value', value: string) => {
    const key = part === 'key' ? value : row.key;
    const field = part === 'value' ? value : row.value;
    const match = row.line.match(assignment);
    const line = `${match?.[1] ?? ''}${key ?? ''}${match?.[3] ?? '='}${field ?? ''}`;
    edit(
      (rows ?? []).map((entry) =>
        entry.id === row.id ? { ...row, key, value: field, line } : entry,
      ),
    );
  };
  return (
    <Space direction="vertical" size={12} style={{ width: '100%' }}>
      {!masked && (
        <Segmented
          aria-label="环境变量编辑模式"
          value={table ? 'table' : 'text'}
          options={[
            { label: '文本', value: 'text' },
            { label: '键值对', value: 'table' },
          ]}
          onChange={(mode) => setTable(mode === 'table')}
        />
      )}
      {table ? (
        <Flex vertical gap={8} className="compose-env-rows">
          {rows?.map((row) =>
            row.key === undefined ? (
              <Input
                key={row.id}
                aria-label="环境变量原始行"
                value={row.line}
                readOnly
                placeholder="空行"
              />
            ) : (
              <Flex key={row.id} gap={8}>
                <Input
                  aria-label="变量名称"
                  className="compose-env-key"
                  readOnly={readOnly}
                  status={envName.test(row.key) ? undefined : 'error'}
                  value={row.key}
                  placeholder="变量名称"
                  onChange={(event) => editRow(row, 'key', event.target.value)}
                />
                <Input
                  aria-label={`${row.key || '环境变量'}的值`}
                  readOnly={readOnly}
                  value={row.value}
                  placeholder="变量值"
                  onChange={(event) =>
                    editRow(row, 'value', event.target.value)
                  }
                />
                {!readOnly && (
                  <Button
                    aria-label={`删除 ${row.key || '变量'}`}
                    icon={<Minus size={15} />}
                    onClick={() =>
                      edit(rows.filter((item) => item.id !== row.id))
                    }
                  />
                )}
              </Flex>
            ),
          )}
          {!readOnly && (
            <Button
              icon={<Plus size={15} />}
              onClick={() => {
                if (
                  rows?.some(
                    (row) => row.key !== undefined && !envName.test(row.key),
                  )
                ) {
                  void message.warning('请输入有效的环境变量名称');
                  return;
                }
                edit([
                  ...(rows ?? []),
                  { id: nextId, line: '=', key: '', value: '' },
                ]);
                setNextId(nextId + 1);
              }}
            >
              添加变量
            </Button>
          )}
        </Flex>
      ) : (
        <Input.TextArea
          aria-label=".env 内容"
          autoComplete="off"
          spellCheck={false}
          className="compose-env-text"
          value={value}
          onChange={(event) => onChange(event.target.value)}
          readOnly={readOnly}
          autoSize={{ minRows: 6, maxRows: 14 }}
          placeholder="KEY=value"
        />
      )}
    </Space>
  );
}
