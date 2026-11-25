import { request } from './client';
import { openEventStream } from './eventStream';
export type Task = {
  taskId: string;
  module: string;
  action: string;
  target: string;
  resourceId: string;
  status: 'running' | 'success' | 'failed' | 'interrupted';
  exitCode: number | null;
  startedAt: string;
  finishedAt: string | null;
  durationMs: number | null;
  errorCode: number;
  errorMsg: string;
  result: unknown;
  batch?: {
    successCount: number;
    failedCount: number;
    results: { target: string; code: number; msg: string }[];
  };
  metadata?: string[];
  logs: string[];
  nextOffset: number;
  logTruncated: boolean;
};
export type TaskSummary = Pick<
  Task,
  | 'taskId'
  | 'module'
  | 'action'
  | 'target'
  | 'resourceId'
  | 'status'
  | 'startedAt'
  | 'finishedAt'
  | 'durationMs'
  | 'exitCode'
  | 'errorMsg'
>;
export const listTasks = (
  params: {
    module: string;
    status?: 'running' | 'finished';
    page?: number;
    page_size?: number;
  },
  signal?: AbortSignal,
) =>
  request<{ list: TaskSummary[]; total: number }>('/api/tasks', {
    params,
    signal,
    silentError: true,
  });
export const cancelTask = (id: string) =>
  request(`/api/tasks/${encodeURIComponent(id)}/cancel`, { method: 'POST' });

function describeTask(task: Task): Task {
  const result = task.result;
  if (result && typeof result === 'object') {
    if (
      'successCount' in result &&
      typeof result.successCount === 'number' &&
      'failedCount' in result &&
      typeof result.failedCount === 'number' &&
      'results' in result &&
      Array.isArray(result.results)
    ) {
      const rows: unknown[] = result.results;
      const items = rows.flatMap((item) =>
        item &&
        typeof item === 'object' &&
        'target' in item &&
        typeof item.target === 'string' &&
        'code' in item &&
        typeof item.code === 'number' &&
        'msg' in item &&
        typeof item.msg === 'string'
          ? [{ target: item.target, code: item.code, msg: item.msg }]
          : [],
      );
      task.batch = {
        successCount: result.successCount,
        failedCount: result.failedCount,
        results: items,
      };
    } else {
      task.metadata = Object.entries(result).map(
        ([key, value]) =>
          `${key}：${typeof value === 'object' ? JSON.stringify(value) : String(value)}`,
      );
    }
  } else if (result != null) {
    task.metadata = [String(result)];
  }
  return task;
}

export function subscribeTask(
  id: string,
  onUpdate: (task: Task, finished: boolean) => void,
  onError: (message: string, reconnecting: boolean) => void,
  logs = true,
) {
  const close = openEventStream(
    `/api/tasks/${encodeURIComponent(id)}/stream?logs=${logs}`,
    {
      task: (data) => onUpdate(describeTask(data as Task), false),
      done: (data) => {
        close();
        onUpdate(describeTask(data as Task), true);
      },
    },
    onError,
  );
  return close;
}

export function waitForTask(id: string, signal?: AbortSignal): Promise<Task> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(signal.reason);
      return;
    }
    const abort = () => {
      cleanup();
      reject(signal?.reason);
    };
    const close = subscribeTask(
      id,
      (task, finished) => {
        if (!finished) return;
        cleanup();
        resolve(task);
      },
      (message, reconnecting) => {
        if (reconnecting) return;
        cleanup();
        reject(new Error(message));
      },
      false,
    );
    function cleanup() {
      close();
      signal?.removeEventListener('abort', abort);
    }
    signal?.addEventListener('abort', abort, { once: true });
  });
}

export function subscribeTaskChanges(
  module: 'software' | 'cron',
  onChange: () => void,
) {
  return openEventStream(
    `/api/tasks/changes?module=${module}`,
    { changed: onChange },
    () => {},
  );
}
