import { request } from './client';

export type HardeningCategory =
  'service' | 'environment' | 'account' | 'critical' | 'schedule';
export type HardeningMode = 'monitor' | 'immutable' | 'append';
export type HardeningSnapshot = {
  digest: string;
  permission: string;
  uid: number;
  gid: number;
  entries: number;
};
export type HardeningRuleInput = {
  path: string;
  category: HardeningCategory;
  mode: HardeningMode;
  enabled: boolean;
  recursive: boolean;
  permission: string;
  remark: string;
  exclusions: string;
};
export type HardeningRule = HardeningRuleInput & {
  id: string;
  directory?: boolean;
  status:
    | 'off'
    | 'disabled'
    | 'monitoring'
    | 'protected'
    | 'changed'
    | 'missing'
    | 'unsupported'
    | 'error'
    | 'paused';
  message: string;
  checkedAt?: string | null;
  pausedUntil?: string | null;
  baseline?: HardeningSnapshot | null;
  current?: HardeningSnapshot | null;
};
export type HardeningEvent = {
  id: string;
  time: string;
  ruleId: string;
  path: string;
  action: string;
  message: string;
};
export type HardeningState = {
  enabled: boolean;
  rules: HardeningRule[];
  events: HardeningEvent[];
  checkedAt?: string | null;
  intervalSeconds: number;
  supported: boolean;
  directorySupported?: boolean;
  directoryNotice?: string;
  notice: string;
  recoveryCommand?: string;
};
const base = '/api/security/hardening';
export const hardening = () =>
  request<HardeningState>(base, { silentError: true });
export const setHardeningEnabled = (enabled: boolean) =>
  request<HardeningState>(base, { method: 'PUT', data: { enabled } });
export const scanHardening = () =>
  request<HardeningState>(`${base}/scan`, { method: 'POST' });
export const saveHardeningRule = (data: HardeningRuleInput, id?: string) =>
  request<HardeningState>(`${base}/rules${id ? `/${id}` : ''}`, {
    method: id ? 'PUT' : 'POST',
    data,
  });
export const deleteHardeningRule = (id: string) =>
  request<HardeningState>(`${base}/rules/${id}`, { method: 'DELETE' });
export const hardeningRuleAction = (
  id: string,
  action: 'baseline' | 'pause' | 'resume',
  minutes?: number,
) =>
  request<HardeningState>(`${base}/rules/${id}/action`, {
    method: 'POST',
    data: { action, minutes },
  });
