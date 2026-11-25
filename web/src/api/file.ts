import { request } from './client';
import { waitForTask } from './task';
export type FileEntry = {
  remark: string;
  remarkReadonly: boolean;
  name: string;
  path: string;
  isDir: boolean;
  isLink: boolean;
  linkTarget?: string;
  linkIsDir?: boolean;
  protected: boolean;
  size: number | null;
  mode: string;
  owner: string;
  group: string;
  modTime: string;
  match?: { line: number; text: string };
};
export type Batch = {
  results: { target: string; code: number; msg: string }[];
  successCount: number;
  failedCount: number;
};
export type TrashEntry = {
  id: string;
  name: string;
  originalPath: string;
  isDir: boolean;
  size: number;
  deletedAt: string;
};
export type TrashConfig = {
  enabled: boolean;
  count: number;
  totalSize: number;
  maxSize: number;
};
export const files = <T>(
  path: string,
  options: Parameters<typeof request>[1] = {},
) => request<T>(`/api/files${path}`, options);
export const fileTask = (path: string, data?: unknown, method = 'POST') =>
  files<{ taskId: string }>(path, { method, data });

export const resumeUploadThreshold = 32 * 1024 ** 2;

export async function uploadInChunks(
  dir: string,
  path: string,
  file: File,
  overwrite: boolean,
  progress: (bytes: number) => void,
) {
  const samples = await Promise.all(
    [0, Math.floor(file.size / 2), file.size - 64].map((offset) =>
      file.slice(offset, offset + 64).arrayBuffer(),
    ),
  );
  const fingerprint = [
    file.lastModified,
    ...samples.map((sample) =>
      Array.from(new Uint8Array(sample), (byte) =>
        byte.toString(16).padStart(2, '0'),
      ).join(''),
    ),
  ].join(':');
  const session = await files<{
    id: string;
    offset: number;
    chunkSize: number;
  }>('/uploads', {
    method: 'POST',
    data: { dir, path, size: file.size, fingerprint, overwrite },
    silentError: true,
  });
  const endpoint = `/uploads/${encodeURIComponent(session.id)}`;
  let offset = session.offset;
  progress(offset);
  while (offset < file.size) {
    const chunk = file.slice(offset, offset + session.chunkSize);
    const start = offset;
    const result = await files<{ offset: number }>(endpoint, {
      method: 'PATCH',
      params: { offset: start },
      data: chunk,
      headers: { 'Content-Type': 'application/octet-stream' },
      silentError: true,
      onUploadProgress: ({ loaded }) => progress(start + loaded),
    });
    offset = result.offset;
    progress(offset);
  }
  await files(`${endpoint}/finish`, { method: 'POST', silentError: true });
}

export const canPreviewFile = (entry: FileEntry) =>
  !entry.isDir &&
  !entry.linkIsDir &&
  /\.(png|jpe?g|gif|webp|bmp|ico|avif|pdf|mp3|wav|flac|m4a|ogg|oga|weba|mp4|webm|ogv|mov)$/i.test(
    entry.name,
  );

export const filePreviewUrl = (path: string) =>
  `/api/files/preview?path=${encodeURIComponent(path)}`;

export async function downloadFile(target: string) {
  const url = `/api/files/download?path=${encodeURIComponent(target)}`;
  const check = await fetch(url, {
    method: 'HEAD',
    credentials: 'same-origin',
  });
  if (!check.ok) {
    await files('/download', { method: 'HEAD', params: { path: target } });
    return;
  }
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.target = '_blank';
  anchor.rel = 'noopener';
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
}

export type FileProperties = {
  entry: FileEntry;
  type: string;
  accessTime?: string;
  changeTime?: string;
  uid?: number;
  gid?: number;
  links?: number;
  inode?: string;
  inodeMode?: number;
  device?: string;
  attributes?: string;
  md5?: string;
  sha1?: string;
  linkTarget?: string;
};
export const fileProperties = (
  path: string,
  details = false,
  signal?: AbortSignal,
) =>
  files<FileProperties>('/properties', { params: { path, details }, signal });
export async function directorySize(path: string, signal: AbortSignal) {
  const { taskId } = await files<{ taskId: string }>('/size', {
    method: 'POST',
    params: { path },
    signal,
    silentError: true,
  });
  const task = await waitForTask(taskId, signal);
  const result = task.result;
  if (
    task.status === 'success' &&
    result &&
    typeof result === 'object' &&
    'size' in result &&
    typeof result.size === 'number'
  )
    return { size: result.size };
  throw new Error(task.errorMsg || '目录大小计算失败');
}
export const saveFileRemark = (path: string, remark: string) =>
  files<{ remark: string }>('/remark', {
    method: 'PUT',
    data: { path, remark },
  });

export type TransferPlan = {
  paths: string[];
  dest: string;
  names?: Record<string, string>;
  overwrites?: Record<string, boolean>;
};
export type TransferConflict = {
  source: string;
  entry: FileEntry;
  canOverwrite: boolean;
  suggestedName: string;
};
export const previewTransfer = (plan: TransferPlan) =>
  files<{ conflicts: TransferConflict[] }>('/transfer/preview', {
    method: 'POST',
    data: plan,
  });
