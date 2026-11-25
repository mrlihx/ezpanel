import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
} from 'react';
import {
  App,
  ConfigProvider,
  Button,
  Dropdown,
  Empty,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Switch,
  Tabs,
  Tooltip,
  Typography,
  theme,
} from 'antd';
import {
  CloseOutlined,
  CopyOutlined,
  FullscreenExitOutlined,
  FullscreenOutlined,
  MinusOutlined,
  ReloadOutlined,
  SaveOutlined,
  SearchOutlined,
  SettingOutlined,
  LeftOutlined,
  RightOutlined,
} from '@ant-design/icons';
import type * as Monaco from 'monaco-editor';
import { useBlocker } from 'react-router-dom';
import { files, downloadFile } from '../../api/file';
import { APIError } from '../../api/client';
import { FONT_SIZE, LAYOUT, MONO_FONT, SPACING } from '../../styles/tokens';
import CodeEditor from '../../components/CodeEditor';
import FileTree from './FileTree';
import './editor.css';
import { isMac, modifierKey } from '../../utils/platform';
import { useFloatingWindow } from '../../hooks/useFloatingWindow';

type Document = { path: string; content: string; saved: string };
const modelPath = (path: string) =>
  `panel-file://workspace${path.split('/').map(encodeURIComponent).join('/')}`;

export default function FileEditor(
  props: Parameters<typeof EditorWorkspace>[0],
) {
  return (
    <ConfigProvider
      theme={{
        inherit: false,
        algorithm: theme.darkAlgorithm,
        token: {
          colorPrimary: '#2563EB',
          colorSuccess: '#16A34A',
          colorWarning: '#D97706',
          colorError: '#DC2626',
          colorBgContainer: '#1e1e1e',
          colorBgElevated: '#252526',
          colorText: '#d4d4d4',
          colorTextSecondary: '#6B7280',
          colorBorder: '#454545',
          borderRadius: 6,
          borderRadiusLG: 8,
          fontSize: FONT_SIZE.body,
          fontSizeSM: FONT_SIZE.auxiliary,
          fontSizeLG: FONT_SIZE.subtitle,
          fontFamily:
            '-apple-system, Segoe UI, PingFang SC, Microsoft YaHei, sans-serif',
        },
        components: {
          Modal: {
            contentBg: '#1e1e1e',
            headerBg: '#1e1e1e',
            footerBg: '#1e1e1e',
          },
        },
      }}
    >
      <EditorWorkspace {...props} />
    </ConfigProvider>
  );
}

function EditorWorkspace({
  request,
  minimized,
  onMinimizedChange,
  onClose,
  onSaved,
}: {
  request: { path: string; line?: number };
  minimized: boolean;
  onMinimizedChange: (value: boolean) => void;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { modal, message } = App.useApp();
  const { token } = theme.useToken();
  const [documents, setDocuments] = useState<Document[]>([]);
  const documentsRef = useRef(documents);
  const dirty = documents.some((doc) => doc.content !== doc.saved);
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      dirty && currentLocation.pathname !== nextLocation.pathname,
  );
  useEffect(() => {
    if (blocker.state !== 'blocked') return;
    const confirm = modal.confirm({
      title: '文件尚未保存，确定离开？',
      content: '离开文件页面将丢弃编辑器中未保存的修改。',
      okText: '放弃修改并离开',
      cancelText: '继续编辑',
      okButtonProps: { danger: true },
      onOk: () => blocker.proceed(),
      onCancel: () => blocker.reset(),
    });
    return () => confirm.destroy();
  }, [blocker, modal]);
  const [active, setActive] = useState('');
  const [directory, setDirectory] = useState(
    request.path.slice(0, request.path.lastIndexOf('/')) || '/',
  );
  const floating = useFloatingWindow({
    width: (window.innerWidth * parseFloat(LAYOUT.fileDialogWidth)) / 100,
    height: (window.innerHeight * parseFloat(LAYOUT.fileDialogHeight)) / 100,
    minWidth: LAYOUT.fileEditorMinWidth,
    minHeight: LAYOUT.fileEditorMinHeight,
  });
  const { full, toggleFull } = floating;
  const [treeVisible, setTreeVisible] = useState(true);
  const [fontSize, setFontSize] = useState<number>(FONT_SIZE.body);
  const [tabSize, setTabSize] = useState(4);
  const [wordWrap, setWordWrap] = useState(false);
  const [minimap, setMinimap] = useState(false);
  const [saving, setSaving] = useState(false);
  const [opening, setOpening] = useState(false);
  const [position, setPosition] = useState({ lineNumber: 1, column: 1 });
  const [language, setLanguage] = useState('');
  const [eol, setEol] = useState('LF');
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const [createForm] = Form.useForm<{ name: string }>();
  const editor = useRef<Monaco.editor.IStandaloneCodeEditor>();
  const monaco = useRef<typeof Monaco>();
  const models = useRef(new Set<Monaco.editor.ITextModel>());
  const listeners = useRef<Monaco.IDisposable[]>([]);
  const saveCommand = useRef<(all: boolean) => void>(() => {});
  const openSequence = useRef(0);
  const reads = useRef(new Set<AbortController>());
  const requestedLine = useRef<{ path: string; line: number }>();
  const revealRequestedLine = useCallback(() => {
    const target = requestedLine.current;
    const instance = editor.current;
    const api = monaco.current;
    const model = instance?.getModel();
    if (
      !target ||
      !instance ||
      !api ||
      !model ||
      model.uri.toString() !== api.Uri.parse(modelPath(target.path)).toString()
    )
      return;
    requestedLine.current = undefined;
    const line = Math.min(target.line, model.getLineCount());
    instance.setPosition({ lineNumber: line, column: 1 });
    instance.revealLineInCenter(line);
  }, []);
  useEffect(() => {
    documentsRef.current = documents;
  }, [documents]);
  useEffect(() => {
    if (!documents.some((doc) => doc.content !== doc.saved)) return;
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [documents]);
  const openFile = useCallback(
    async (path: string) => {
      const sequence = ++openSequence.current;
      if (documentsRef.current.some((doc) => doc.path === path)) {
        setActive(path);
        setOpening(false);
        return;
      }
      setOpening(true);
      const controller = new AbortController();
      reads.current.add(controller);
      try {
        const result = await files<{ content: string }>('/content', {
          params: { path },
          signal: controller.signal,
          silentError: true,
        });
        setDocuments((previous) =>
          previous.some((doc) => doc.path === path)
            ? previous
            : [
                ...previous,
                { path, content: result.content, saved: result.content },
              ],
        );
        if (sequence === openSequence.current) setActive(path);
      } catch (error) {
        if (controller.signal.aborted) return;
        if (error instanceof APIError && error.code === 1003)
          modal.confirm({
            title: '此文件不能在文本编辑器中打开',
            content: error.message,
            okText: '下载',
            onOk: () => downloadFile(path),
          });
        else
          void message.error(
            error instanceof Error ? error.message : '读取文件失败',
          );
      } finally {
        reads.current.delete(controller);
        if (!controller.signal.aborted && sequence === openSequence.current)
          setOpening(false);
      }
    },
    [message, modal],
  );
  useEffect(() => {
    let cancelled = false;
    queueMicrotask(() => {
      if (cancelled) return;
      requestedLine.current = request.line
        ? { path: request.path, line: request.line }
        : undefined;
      void openFile(request.path);
      revealRequestedLine();
    });
    return () => {
      cancelled = true;
    };
  }, [request, openFile, revealRequestedLine]);
  useEffect(
    () => () => {
      reads.current.forEach((controller) => controller.abort());
      listeners.current.forEach((listener) => listener.dispose());
      models.current.forEach((model) => model.dispose());
    },
    [],
  );
  useEffect(() => {
    const api = monaco.current;
    if (!api) return;
    const retained = new Set(
      documents.map((doc) => api.Uri.parse(modelPath(doc.path)).toString()),
    );
    models.current.forEach((model) => {
      if (
        model !== editor.current?.getModel() &&
        !retained.has(model.uri.toString())
      ) {
        model.dispose();
        models.current.delete(model);
      }
    });
  }, [documents]);
  async function saveDocuments(chosen: Document[]) {
    setSaving(true);
    const failed: string[] = [];
    let count = 0;
    try {
      for (const doc of chosen) {
        if (doc.content === doc.saved) continue;
        try {
          await files('/content', {
            method: 'PUT',
            data: { path: doc.path, content: doc.content },
            silentError: true,
          });
          setDocuments((current) =>
            current.map((item) =>
              item.path === doc.path ? { ...item, saved: doc.content } : item,
            ),
          );
          count++;
        } catch (error) {
          failed.push(
            `${doc.path}：${error instanceof Error ? error.message : '保存失败'}`,
          );
        }
      }
      if (count) {
        void message.success(`已保存 ${count} 个文件`);
        onSaved();
      }
      if (failed.length)
        modal.error({
          title: '部分文件未保存',
          content: (
            <div>
              {failed.map((line) => (
                <p key={line}>{line}</p>
              ))}
            </div>
          ),
        });
      else if (!count) void message.info('没有需要保存的修改');
    } finally {
      setSaving(false);
    }
  }
  useEffect(() => {
    saveCommand.current = (all) => {
      if (!saving)
        void saveDocuments(
          all ? documents : documents.filter((doc) => doc.path === active),
        );
    };
  });
  function closeDocuments(paths: string[], closeWindow = false) {
    if (saving) return;
    const remaining = documents.filter((doc) => !paths.includes(doc.path));
    const finish = () => {
      if (closeWindow || !remaining.length) {
        onClose();
        return;
      }
      setDocuments(remaining);
      if (paths.includes(active)) setActive(remaining[0].path);
    };
    const dirty = documents.filter(
      (doc) => paths.includes(doc.path) && doc.content !== doc.saved,
    );
    if (dirty.length)
      modal.confirm({
        title: `${dirty.length} 个文件尚未保存`,
        content: (
          <Typography.Paragraph>
            关闭将丢弃这些文件的修改：{dirty.map((doc) => doc.path).join('、')}
          </Typography.Paragraph>
        ),
        okText: '放弃修改并关闭',
        cancelText: '返回编辑',
        okButtonProps: { danger: true },
        onOk: finish,
      });
    else finish();
  }
  function action(id: string) {
    editor.current?.focus();
    void editor.current?.getAction(id)?.run();
  }
  function reload() {
    const doc = documents.find((item) => item.path === active);
    if (!doc) return;
    const read = async () => {
      setOpening(true);
      try {
        const result = await files<{ content: string }>('/content', {
          params: { path: doc.path },
        });
        setDocuments((current) =>
          current.map((item) =>
            item.path === doc.path
              ? { ...item, content: result.content, saved: result.content }
              : item,
          ),
        );
      } finally {
        setOpening(false);
      }
    };
    if (doc.content !== doc.saved)
      modal.confirm({
        title: '重新读取文件？',
        content: '当前未保存的修改将被磁盘中的内容替换。',
        okText: '重新读取',
        onOk: read,
      });
    else void read();
  }
  const current = documents.find((doc) => doc.path === active);
  const style = {
    '--editor-bg': token.colorBgContainer,
    '--editor-raised': token.colorFillAlter,
    '--editor-border': token.colorBorderSecondary,
    '--editor-ink': token.colorText,
    '--editor-muted': token.colorTextSecondary,
    '--editor-selected': token.colorPrimaryBg,
    '--editor-accent': token.colorPrimary,
    '--file-folder': token.colorWarning,
    '--editor-space-xs': `${SPACING.xs}px`,
    '--editor-space-sm': `${SPACING.sm}px`,
    '--editor-space-md': `${SPACING.md}px`,
    '--editor-space-lg': `${SPACING.lg}px`,
    '--editor-tree-width': `${LAYOUT.fileEditorTreeWidth}px`,
    '--editor-tab-width': `${LAYOUT.fileTabWidth}px`,
    '--editor-small-font': `${FONT_SIZE.auxiliary}px`,
    '--editor-mono': MONO_FONT,
    '--editor-radius': `${token.borderRadius}px`,
  } as CSSProperties;
  return (
    <>
      {minimized && (
        <Button
          className="file-editor-restore"
          style={{
            position: 'fixed',
            bottom: SPACING.lg,
            right: SPACING.xl,
            zIndex: token.zIndexPopupBase,
          }}
          icon={<SaveOutlined />}
          onClick={() => onMinimizedChange(false)}
        >
          恢复编辑器 · {documents.length} 个文件
        </Button>
      )}
      <Modal
        className={`file-editor-modal${full ? ' file-editor-full' : ''}`}
        title={
          <div className="file-editor-heading" {...floating.titleProps}>
            <span>在线文本编辑器{opening ? ' · 正在读取…' : ''}</span>
            <Space size="small">
              <Tooltip title="最小化">
                <Button
                  type="text"
                  size="small"
                  aria-label="最小化编辑器"
                  icon={<MinusOutlined />}
                  onClick={() => onMinimizedChange(true)}
                />
              </Tooltip>
              <Tooltip title={full ? '还原窗口' : '最大化'}>
                <Button
                  type="text"
                  size="small"
                  aria-label={full ? '还原编辑器' : '最大化编辑器'}
                  icon={
                    full ? <FullscreenExitOutlined /> : <FullscreenOutlined />
                  }
                  onClick={toggleFull}
                />
              </Tooltip>
              <Button
                type="text"
                size="small"
                aria-label="关闭编辑器"
                disabled={saving}
                icon={<CloseOutlined />}
                onClick={() =>
                  closeDocuments(
                    documents.map((doc) => doc.path),
                    true,
                  )
                }
              />
            </Space>
          </div>
        }
        open={!minimized}
        width={floating.width}
        style={floating.modalStyle}
        modalRender={(node) => <div {...floating.frameProps}>{node}</div>}
        styles={{
          body: {
            flex: 1,
            minHeight: 0,
            display: 'flex',
            flexDirection: 'column',
            padding: 0,
          },
          content: {
            height: '100%',
            display: 'flex',
            flexDirection: 'column',
            padding: 0,
          },
          header: { padding: SPACING.lg, marginBottom: 0 },
          wrapper: { overflow: 'hidden' },
        }}
        footer={null}
        closable={false}
        keyboard={false}
        maskClosable={false}
      >
        <div className="file-editor-workspace" style={style}>
          <div className="file-editor-toolbar">
            <Button
              type="text"
              icon={<SaveOutlined />}
              disabled={!current || saving}
              loading={saving}
              onClick={() => void saveDocuments(current ? [current] : [])}
            >
              保存
            </Button>
            <Button
              type="text"
              icon={<CopyOutlined />}
              disabled={!documents.length || saving}
              onClick={() => void saveDocuments(documents)}
            >
              全部保存
            </Button>
            <Button
              type="text"
              icon={<ReloadOutlined />}
              disabled={!current || saving}
              onClick={reload}
            >
              刷新
            </Button>
            <Button
              type="text"
              icon={<SearchOutlined />}
              disabled={!current}
              onClick={() => action('actions.find')}
            >
              搜索
            </Button>
            <Button
              type="text"
              disabled={!current}
              onClick={() => action('editor.action.startFindReplaceAction')}
            >
              替换
            </Button>
            <Button
              type="text"
              disabled={!current}
              onClick={() => action('editor.action.gotoLine')}
            >
              跳转行
            </Button>
            <Dropdown
              trigger={['click']}
              menu={{
                items: Array.from(
                  {
                    length:
                      (LAYOUT.fileEditorFontMax - LAYOUT.fileEditorFontMin) /
                        LAYOUT.fileEditorFontStep +
                      1,
                  },
                  (_, i) => {
                    const size =
                      LAYOUT.fileEditorFontMin + i * LAYOUT.fileEditorFontStep;
                    return { key: String(size), label: `${size}px` };
                  },
                ),
                selectedKeys: [String(fontSize)],
                onClick: ({ key }) => setFontSize(Number(key)),
              }}
            >
              <Button type="text">字体大小</Button>
            </Dropdown>
            <Button
              type="text"
              icon={<SettingOutlined />}
              onClick={() => setSettingsOpen(true)}
            >
              设置
            </Button>
            <Button
              type="text"
              onClick={() =>
                modal.info({
                  title: '编辑器快捷键',
                  content: (
                    <div>
                      <p>{modifierKey} + S：保存当前文件</p>
                      <p>{modifierKey} + Shift + S：全部保存</p>
                      <p>
                        {modifierKey} + F：查找；
                        {isMac ? '⌘ + Option + F' : 'Ctrl + H'}：替换
                      </p>
                      <p>Ctrl + G：跳转行；{modifierKey} + Z：撤销</p>
                    </div>
                  ),
                })
              }
            >
              快捷键
            </Button>
          </div>
          <div className="file-editor-main">
            {treeVisible && (
              <aside id="file-editor-tree" className="file-editor-sidebar">
                <FileTree
                  directory={directory}
                  activePath={active}
                  onDirectoryChange={setDirectory}
                  onOpen={(path) => void openFile(path)}
                  onCreate={() => {
                    createForm.resetFields();
                    setCreateOpen(true);
                  }}
                />
              </aside>
            )}
            <div className="file-editor-code">
              <button
                type="button"
                className="file-editor-tree-toggle"
                aria-label={treeVisible ? '收起目录' : '展开目录'}
                title={treeVisible ? '收起目录' : '展开目录'}
                aria-expanded={treeVisible}
                aria-controls="file-editor-tree"
                onClick={() => setTreeVisible((value) => !value)}
              >
                {treeVisible ? <LeftOutlined /> : <RightOutlined />}
              </button>
              <Tabs
                type="editable-card"
                hideAdd
                activeKey={active}
                onChange={setActive}
                onEdit={(key, change) => {
                  if (change === 'remove') closeDocuments([String(key)]);
                }}
                items={documents.map((doc, index) => ({
                  key: doc.path,
                  label: (
                    <Dropdown
                      trigger={['contextMenu']}
                      menu={{
                        items: [
                          { key: 'close', label: '关闭当前' },
                          {
                            key: 'others',
                            label: '关闭其它',
                            disabled: documents.length === 1,
                          },
                          {
                            key: 'right',
                            label: '关闭右侧',
                            disabled: index === documents.length - 1,
                          },
                        ],
                        onClick: ({ key }) =>
                          closeDocuments(
                            key === 'close'
                              ? [doc.path]
                              : key === 'others'
                                ? documents
                                    .filter((item) => item.path !== doc.path)
                                    .map((item) => item.path)
                                : documents
                                    .slice(index + 1)
                                    .map((item) => item.path),
                          ),
                      }}
                    >
                      <span title={doc.path}>
                        {doc.content !== doc.saved ? '● ' : ''}
                        {doc.path.split('/').pop()}
                      </span>
                    </Dropdown>
                  ),
                }))}
              />
              <div className="file-editor-monaco">
                {current ? (
                  <CodeEditor
                    path={modelPath(current.path)}
                    value={current.content}
                    keepCurrentModel
                    readOnly={opening}
                    editorTheme="vs-dark"
                    options={{
                      fontSize,
                      tabSize,
                      insertSpaces: true,
                      detectIndentation: false,
                      wordWrap: wordWrap ? 'on' : 'off',
                      minimap: { enabled: minimap },
                      find: { addExtraSpaceOnTop: false },
                    }}
                    onChange={(content) =>
                      setDocuments((all) =>
                        all.map((doc) =>
                          doc.path === current.path ? { ...doc, content } : doc,
                        ),
                      )
                    }
                    onMount={(instance, api) => {
                      editor.current = instance;
                      monaco.current = api;
                      const update = () => {
                        const model = instance.getModel();
                        if (model) {
                          models.current.add(model);
                          setLanguage(model.getLanguageId());
                          setEol(model.getEOL() === '\r\n' ? 'CRLF' : 'LF');
                        }
                        if (requestedLine.current)
                          queueMicrotask(revealRequestedLine);
                        setPosition(
                          instance.getPosition() || {
                            lineNumber: 1,
                            column: 1,
                          },
                        );
                      };
                      update();
                      listeners.current.push(
                        instance.onDidChangeModel(update),
                        instance.onDidChangeCursorPosition((event) =>
                          setPosition(event.position),
                        ),
                        instance.onDidChangeModelLanguage(update),
                        instance.onDidChangeModelContent(update),
                      );
                      instance.addCommand(
                        api.KeyMod.CtrlCmd | api.KeyCode.KeyS,
                        () => saveCommand.current(false),
                      );
                      instance.addCommand(
                        api.KeyMod.CtrlCmd |
                          api.KeyMod.Shift |
                          api.KeyCode.KeyS,
                        () => saveCommand.current(true),
                      );
                    }}
                  />
                ) : (
                  <Empty
                    description={
                      opening ? '正在读取文件' : '从左侧目录选择文件'
                    }
                  />
                )}
              </div>
            </div>
          </div>
          <div className="file-editor-status">
            <Typography.Text ellipsis title={active}>
              文件位置：{active || '—'}
            </Typography.Text>
            <Space wrap size="small">
              <span>{eol}</span>
              <span>
                行 {position.lineNumber}，列 {position.column}
              </span>
              <span>空格：{tabSize}</span>
              <span>编码：UTF-8</span>
              <span>语言：{language || '—'}</span>
              {current?.content !== current?.saved && <span>未保存</span>}
            </Space>
          </div>
        </div>
      </Modal>
      <Modal
        title="编辑器设置"
        open={settingsOpen}
        onCancel={() => setSettingsOpen(false)}
        footer={<Button onClick={() => setSettingsOpen(false)}>完成</Button>}
      >
        <Form
          layout="horizontal"
          labelCol={{ flex: LAYOUT.formLabelWidth }}
          wrapperCol={{ flex: 1 }}
          colon={false}
        >
          <Form.Item label="缩进宽度">
            <Select
              value={tabSize}
              options={[2, 4, 8].map((size) => ({
                value: size,
                label: `${size} 个空格`,
              }))}
              onChange={setTabSize}
            />
          </Form.Item>
          <Form.Item label="自动换行">
            <Switch checked={wordWrap} onChange={setWordWrap} />
          </Form.Item>
          <Form.Item label="代码缩略图">
            <Switch checked={minimap} onChange={setMinimap} />
          </Form.Item>
        </Form>
      </Modal>
      <Modal
        title={`新建文件 · ${directory}`}
        open={createOpen}
        confirmLoading={creating}
        onCancel={() => {
          if (!creating) setCreateOpen(false);
        }}
        onOk={async () => {
          if (creating) return;
          const { name } = await createForm.validateFields();
          const path = `${directory === '/' ? '' : directory}/${name}`;
          setCreating(true);
          try {
            await files('/create', {
              method: 'POST',
              data: { path, type: 'file' },
            });
            setCreateOpen(false);
            onSaved();
            void openFile(path);
          } finally {
            setCreating(false);
          }
        }}
      >
        <Form
          form={createForm}
          layout="horizontal"
          labelCol={{ flex: LAYOUT.formLabelWidth }}
          wrapperCol={{ flex: 1 }}
          colon={false}
        >
          <Form.Item
            name="name"
            label="文件名"
            rules={[
              { required: true, message: '请输入文件名' },
              {
                validator: (_, value: string) =>
                  !value ||
                  ['.', '..'].includes(value) ||
                  /[/\\]/.test(value) ||
                  value.includes(String.fromCharCode(0))
                    ? Promise.reject(new Error('请输入单个文件名'))
                    : Promise.resolve(),
              },
            ]}
          >
            <Input autoFocus />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}
