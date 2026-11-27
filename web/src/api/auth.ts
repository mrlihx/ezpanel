import { request } from './client';
export type User = {
  username: string;
  timezone: string;
  alias: string;
  idleMinutes: number;
  siteRoot: string;
  backupRoot: string;
};
export const me = () => request<User>('/api/auth/me', { silentError: true });
export const login = (data: {
  username: string;
  password: string;
  totpCode?: string;
}) =>
  request<User>('/api/auth/login', { method: 'POST', data, silentError: true });
export const logout = () =>
  request<null>('/api/auth/logout', { method: 'POST' });

export const branding = () =>
  request<{ alias: string; ssoTitle?: string; totpEnabled?: boolean }>(
    '/api/auth/branding',
    {
      silentError: true,
    },
  );

export type TOTPSetup = {
  secret: string;
  otpauthUrl: string;
  issuer: string;
  account: string;
  existing?: boolean;
};
export const totpStatus = () =>
  request<{ enabled: boolean; hasSecret?: boolean }>('/api/auth/totp');
export const setupTOTP = (reset = false) =>
  request<TOTPSetup>('/api/auth/totp/setup', {
    method: 'POST',
    data: { reset },
  });
export const enableTOTP = (data: { secret: string; code: string }) =>
  request<{ enabled: boolean }>('/api/auth/totp/enable', {
    method: 'POST',
    data,
  });
export const disableTOTP = () =>
  request<{ enabled: boolean }>('/api/auth/totp/disable', {
    method: 'POST',
  });

export type SSOConfig = {
  enabled: boolean;
  pluginId: string;
  issuer: string;
  clientId: string;
  subject: string;
  secretSet: boolean;
  callback: string;
};
export type SSOInput = Omit<SSOConfig, 'secretSet' | 'callback'> & {
  clientSecret?: string;
};
export const ssoConfig = () => request<SSOConfig>('/api/auth/sso/config');
export const saveSSOConfig = (data: SSOInput) =>
  request<SSOConfig>('/api/auth/sso/config', { method: 'PUT', data });
