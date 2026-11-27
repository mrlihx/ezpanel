import type { PanelSDK } from '../../../sdk/ts/panel-sdk';

export const frameReady = 'ezpanel-plugin-frame-ready';
export type FrameContext = Pick<PanelSDK, 'dark' | 'route' | 'context'>;
export type FrameAPI = {
  path: string;
  method?: string;
  headers?: [string, string][];
  body?: string | ArrayBuffer | Blob | { form: [string, FormDataEntryValue][] };
};
export type FrameCall =
  | { op: 'api'; input: FrameAPI }
  | {
      op: 'navigate' | 'pickFile' | 'pickDirectory' | 'pickResource' | 'task';
      input: string;
    };
export type FrameRequest =
  | (FrameCall & { requestId: number })
  | { op: 'cancel'; requestId: number }
  | { op: 'size'; height: number }
  | { op: 'loaded' | 'failed' };
export type FrameReply =
  | { context: FrameContext }
  | { requestId: number; value: unknown }
  | {
      requestId: number;
      error: { message: string; code: number; status?: number };
    };
