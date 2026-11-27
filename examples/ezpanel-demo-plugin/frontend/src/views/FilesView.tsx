import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Input,
  Modal,
  Popconfirm,
  Space,
  Table,
  Tag,
  Typography,
} from "antd";
import type { PanelSDK } from "../panel-sdk";
import { formatAPIError, formatBytes, postJSON } from "../api";

interface FileEntry {
  name: string;
  mode: string;
  size: number;
  modifiedAt: string;
  directory: boolean;
  link: boolean;
}

interface FilePage {
  entries: FileEntry[];
  nextOffset: number;
  more: boolean;
}

interface StreamPreview {
  path: string;
  bytes: number;
  sha256: string;
  isUtf8: boolean;
  textPreview: string;
  hexHead: string;
}

function joinPath(dir: string, name: string): string {
  const clean = dir.endsWith("/") ? dir.slice(0, -1) : dir;
  return `${clean}/${name}`;
}

/**
 * 文件与存储视图：
 * 演示受控文件系统 API（FSList / FSStat / FSHash / FSRead / FSWrite / FSMkdir / FSDelete / FSOpen / FSUpload），
 * 以及文件管理右键/批量菜单上下文（sdk.context.filePaths）。
 * 注意：sdk.pickFile/pickDirectory 仅方便选择路径，不会扩大后端 manifest.fsRoots（/tmp）的授权范围。
 */
export default function FilesView({ sdk }: { sdk: PanelSDK }) {
  const [currentDir, setCurrentDir] = useState("/tmp");
  const [page, setPage] = useState<FilePage>({ entries: [], nextOffset: 0, more: false });
  const [offset, setOffset] = useState(0);
  const [error, setError] = useState("");
  const [refreshSeq, setRefreshSeq] = useState(0);

  const [newDirName, setNewDirName] = useState("ezpanel-demo");
  const [targetFile, setTargetFile] = useState("/tmp/ezpanel-demo.txt");
  const [fileText, setFileText] = useState("Hello from ezpanel-demo-plugin!\n");
  const [fileMode, setFileMode] = useState("0600");
  const [binaryPath, setBinaryPath] = useState("/tmp/ezpanel-demo.bin");
  const [preview, setPreview] = useState<StreamPreview>();
  const [statInfo, setStatInfo] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    setError("");
    void sdk
      .api<FilePage>(
        `/fs/list?path=${encodeURIComponent(currentDir)}&offset=${offset}`,
        { signal: controller.signal },
      )
      .then((res) => setPage(res))
      .catch((err) => {
        if (!controller.signal.aborted) setError(formatAPIError(err));
      });
    return () => controller.abort();
  }, [sdk, currentDir, offset, refreshSeq]);

  const inspectFile = async (path: string) => {
    try {
      const [meta, hash] = await Promise.all([
        sdk.api<FileEntry>(`/fs/stat?path=${encodeURIComponent(path)}`),
        sdk.api<{ sha256: string }>(`/fs/hash?path=${encodeURIComponent(path)}`).catch(() => ({
          sha256: "-",
        })),
      ]);
      setStatInfo(`${path} · ${meta.mode} · ${formatBytes(meta.size)} · SHA256=${hash.sha256}`);
      if (!meta.directory) {
        const res = await sdk.api<StreamPreview>(
          `/fs/stream-preview?path=${encodeURIComponent(path)}`,
        );
        setPreview(res);
      }
    } catch (err) {
      sdk.message.error(formatAPIError(err));
    }
  };

  const readTextFile = async (path: string) => {
    try {
      const res = await sdk.api<{ path: string; content: string }>(
        `/fs/read?path=${encodeURIComponent(path)}`,
      );
      setTargetFile(res.path);
      setFileText(res.content);
      sdk.message.success(`已读取 ${res.path}`);
    } catch (err) {
      sdk.message.error(formatAPIError(err));
    }
  };

  const uploadBinarySample = async (file?: File) => {
    try {
      const body = file ?? new Blob(["\x00\x01\x02EzPanel-Binary-Stream\n"], { type: "application/octet-stream" });
      await sdk.api(`/fs/upload?path=${encodeURIComponent(binaryPath)}`, {
        method: "POST",
        body,
      });
      sdk.message.success(`已流式上传到 ${binaryPath}`);
      setRefreshSeq((n) => n + 1);
    } catch (err) {
      sdk.message.error(formatAPIError(err));
    }
  };

  return (
    <Space direction="vertical" size="middle" style={{ width: "100%" }}>
      {error && <Alert type="error" showIcon message={error} />}

      {sdk.context?.filePaths?.length ? (
        <Card size="small" title={`来自文件菜单的多选路径 (${sdk.context.filePaths.length})`}>
          <Space direction="vertical" style={{ width: "100%" }}>
            {sdk.context.filePaths.map((p) => (
              <Space key={p} wrap>
                <Tag color="cyan">{p}</Tag>
                <Button size="small" onClick={() => void inspectFile(p)}>
                  检查 Stat/Hash/流预览
                </Button>
              </Space>
            ))}
          </Space>
        </Card>
      ) : null}

      <Card
        size="small"
        title={`授权根目录浏览器（fsRoots: /tmp）`}
        extra={
          <Space>
            {sdk.pickDirectory && (
              <Button
                size="small"
                onClick={() =>
                  void sdk.pickDirectory?.(currentDir).then((picked) => {
                    if (picked) {
                      setCurrentDir(picked);
                      setOffset(0);
                    }
                  })
                }
              >
                用面板选目录
              </Button>
            )}
            <Button size="small" onClick={() => setRefreshSeq((n) => n + 1)}>
              刷新
            </Button>
          </Space>
        }
      >
        <Space direction="vertical" size="middle" style={{ width: "100%" }}>
          <Space wrap>
            <Input
              style={{ width: 300 }}
              value={currentDir}
              onChange={(e) => {
                setCurrentDir(e.target.value);
                setOffset(0);
              }}
              addonBefore="目录"
            />
            <Input
              style={{ width: 200 }}
              value={newDirName}
              onChange={(e) => setNewDirName(e.target.value)}
              placeholder="新建子目录名"
            />
            <Button
              onClick={() =>
                void postJSON(sdk, "/fs/mkdir", { path: joinPath(currentDir, newDirName) })
                  .then(() => {
                    sdk.message.success("目录已创建");
                    setRefreshSeq((n) => n + 1);
                  })
                  .catch((e) => sdk.message.error(formatAPIError(e)))
              }
            >
              FSMkdir
            </Button>
          </Space>

          {statInfo && <Alert type="info" showIcon message={statInfo} />}

          <Table
            size="small"
            rowKey="name"
            pagination={false}
            scroll={{ x: "max-content" }}
            dataSource={page.entries}
            columns={[
              {
                title: "名称",
                dataIndex: "name",
                render: (name: string, row) =>
                  row.directory ? (
                    <Button
                      type="link"
                      size="small"
                      style={{ padding: 0 }}
                      onClick={() => {
                        setCurrentDir(joinPath(currentDir, name));
                        setOffset(0);
                      }}
                    >
                      📁 {name}
                    </Button>
                  ) : (
                    <span>{name}</span>
                  ),
              },
              { title: "权限", dataIndex: "mode", width: 120 },
              {
                title: "大小",
                dataIndex: "size",
                width: 100,
                render: (v: number, row) => (row.directory ? "-" : formatBytes(v)),
              },
              {
                title: "操作",
                width: 280,
                render: (_, row) => {
                  const full = joinPath(currentDir, row.name);
                  return (
                    <Space size={4}>
                      <Button size="small" onClick={() => void inspectFile(full)}>
                        Stat/Hash
                      </Button>
                      {!row.directory && (
                        <Button size="small" onClick={() => void readTextFile(full)}>
                          FSRead
                        </Button>
                      )}
                      <Popconfirm
                        title={`删除 ${full}？`}
                        onConfirm={() =>
                          void postJSON(sdk, "/fs/delete", { path: full })
                            .then(() => {
                              sdk.message.success("已删除");
                              setRefreshSeq((n) => n + 1);
                            })
                            .catch((e) => sdk.message.error(formatAPIError(e)))
                        }
                      >
                        <Button size="small" danger>
                          删除
                        </Button>
                      </Popconfirm>
                    </Space>
                  );
                },
              },
            ]}
          />

          <Space>
            <Button
              size="small"
              disabled={offset === 0}
              onClick={() => setOffset((v) => Math.max(0, v - 200))}
            >
              上一页
            </Button>
            <Button
              size="small"
              disabled={!page.more}
              onClick={() => setOffset(page.nextOffset)}
            >
              下一页
            </Button>
          </Space>
        </Space>
      </Card>

      <div className="cs-grid">
        <Card size="small" title="文本读写（Host.FSRead / FSWrite，限 UTF-8 / 2MiB）">
          <Space direction="vertical" style={{ width: "100%" }}>
            <Space wrap>
              <Input
                style={{ width: 260 }}
                value={targetFile}
                onChange={(e) => setTargetFile(e.target.value)}
                addonBefore="路径"
              />
              <Input
                style={{ width: 110 }}
                value={fileMode}
                onChange={(e) => setFileMode(e.target.value)}
                addonBefore="mode"
              />
            </Space>
            <Input.TextArea
              rows={4}
              value={fileText}
              onChange={(e) => setFileText(e.target.value)}
            />
            <Space>
              <Button onClick={() => void readTextFile(targetFile)}>读取</Button>
              <Button
                type="primary"
                onClick={() =>
                  void postJSON(sdk, "/fs/write", {
                    path: targetFile,
                    content: fileText,
                    mode: fileMode,
                  })
                    .then(() => {
                      sdk.message.success("写入成功");
                      setRefreshSeq((n) => n + 1);
                    })
                    .catch((e) => sdk.message.error(formatAPIError(e)))
                }
              >
                原子写入
              </Button>
            </Space>
          </Space>
        </Card>

        <Card size="small" title="二进制流传输（Host.FSOpen / FSUpload）">
          <Space direction="vertical" style={{ width: "100%" }}>
            <Input
              value={binaryPath}
              onChange={(e) => setBinaryPath(e.target.value)}
              addonBefore="目标路径"
            />
            <Space wrap>
              <Button type="primary" onClick={() => void uploadBinarySample()}>
                上传二进制测试样本
              </Button>
              <label>
                <input
                  type="file"
                  style={{ display: "none" }}
                  onChange={(e) => {
                    const f = e.target.files?.[0];
                    if (f) void uploadBinarySample(f);
                    e.target.value = "";
                  }}
                />
                <Typography.Text underline style={{ cursor: "pointer" }}>
                  选择本地文件流式上传
                </Typography.Text>
              </label>
              <Button onClick={() => void inspectFile(binaryPath)}>FSOpen 流预览</Button>
            </Space>
            <Typography.Text className="cs-muted">
              选择面板文件不会扩大后端 fsRoots；所有读写均受 Host 路径策略约束。
            </Typography.Text>
          </Space>
        </Card>
      </div>

      <Modal
        open={Boolean(preview)}
        title={preview ? `FSOpen 流式预览 · ${preview.path}` : ""}
        onCancel={() => setPreview(undefined)}
        footer={
          <Button type="primary" onClick={() => setPreview(undefined)}>
            关闭
          </Button>
        }
        width={720}
      >
        {preview && (
          <div data-panel-plugin="ezpanel-demo-plugin">
            <Space direction="vertical" style={{ width: "100%" }}>
              <Typography.Text>
                大小：{formatBytes(preview.bytes)} · UTF-8：{String(preview.isUtf8)} · SHA-256：
                <Typography.Text code>{preview.sha256}</Typography.Text>
              </Typography.Text>
              <pre
                className="cs-pre cs-modal-pre"
                style={{
                  whiteSpace: "pre-wrap",
                  overflowWrap: "anywhere",
                  wordBreak: "break-word",
                  overflow: "auto",
                  maxHeight: "60vh",
                  maxWidth: "100%",
                }}
              >
                {preview.isUtf8 ? preview.textPreview : `HEX 前 64 字节: ${preview.hexHead}`}
              </pre>
            </Space>
          </div>
        )}
      </Modal>
    </Space>
  );
}
