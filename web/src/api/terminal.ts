import { request } from './client';

export type TerminalHost = {
  id: string;
  name: string;
  address: string;
  port: number;
  username: string;
  authType: 'password' | 'privateKey';
  fingerprint?: string;
};

export type TerminalHostInput = Omit<TerminalHost, 'id'> & {
  password?: string;
  privateKey?: string;
  passphrase?: string;
};

const hostsURL = '/api/terminal/hosts';

export const terminalHosts = () =>
  request<TerminalHost[]>(hostsURL, { silentError: true });

export const saveTerminalHost = (data: TerminalHostInput, id?: string) =>
  request<TerminalHost>(id ? hostsURL + '/' + id : hostsURL, {
    method: id ? 'PUT' : 'POST',
    data,
  });

export const deleteTerminalHost = (id: string) =>
  request<null>(hostsURL + '/' + id, { method: 'DELETE' });
