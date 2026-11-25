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
export const login = (data: { username: string; password: string }) =>
  request<User>('/api/auth/login', { method: 'POST', data, silentError: true });
export const logout = () =>
  request<null>('/api/auth/logout', { method: 'POST' });
export const changePassword = (data: {
  oldPassword: string;
  newPassword: string;
}) =>
  request<null>('/api/auth/password', {
    method: 'POST',
    data,
    silentError: true,
  });

export const branding = () =>
  request<{ alias: string }>('/api/auth/branding', { silentError: true });
