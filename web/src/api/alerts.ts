import { request } from './client';

const alerts = '/api/settings/alerts';

export type AlertChannelKind = 'email' | 'webhook';

export const alertChannelNames: Record<AlertChannelKind, string> = {
  email: '邮箱',
  webhook: 'Webhook',
};

export type AlertChannelInput = {
  kind: AlertChannelKind;
  name: string;
  enabled: boolean;
  isDefault: boolean;
  smtpHost?: string;
  smtpPort?: number;
  smtpMode?: 'tls' | 'starttls' | 'plain';
  from?: string;
  username?: string;
  recipients?: string[];
  password?: string;
  clearPassword?: boolean;
  url?: string;
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH';
  format?: 'json' | 'form';
  verifyTLS?: boolean;
  params?: string;
  headers?: string;
};

export type AlertChannel = Omit<
  AlertChannelInput,
  'password' | 'clearPassword' | 'url' | 'params' | 'headers'
> & {
  id: string;
  hasPassword?: boolean;
  hasURL?: boolean;
  hasParams?: boolean;
  hasHeaders?: boolean;
};

export type AlertRuleKind = 'service' | 'project' | 'certificate';

export type AlertRuleInput = {
  title: string;
  enabled: boolean;
  kind: AlertRuleKind;
  target: string;
  channelIds: string[];
  intervalSeconds: number;
  maxDaily: number;
  days: number;
};

export type AlertRule = AlertRuleInput & {
  id: string;
  targetName: string;
  state: 'pending' | 'normal' | 'triggered' | 'error';
  lastCheckedAt?: string;
};

export type AlertLog = {
  id: string;
  ruleId: string;
  title: string;
  channelName: string;
  kind: AlertChannelKind;
  success: boolean;
  error?: string;
  createdAt: string;
};

export type AlertLogStatus = 'success' | 'failed';

export function alertChannels() {
  return request<{ list: AlertChannel[] }>(`${alerts}/channels`, {
    silentError: true,
  });
}

export function saveAlertChannel(
  id: string | undefined,
  data: AlertChannelInput,
) {
  return request<AlertChannel>(`${alerts}/channels${id ? `/${id}` : ''}`, {
    method: id ? 'PUT' : 'POST',
    data,
  });
}

export function deleteAlertChannel(id: string) {
  return request(`${alerts}/channels/${id}`, { method: 'DELETE' });
}

export function testAlertChannel(id: string) {
  return request<{ success: boolean; error?: string }>(
    `${alerts}/channels/${id}/test`,
    { method: 'POST' },
  );
}

export function alertRules() {
  return request<{ list: AlertRule[] }>(`${alerts}/rules`, {
    silentError: true,
  });
}

export function saveAlertRule(id: string | undefined, data: AlertRuleInput) {
  return request<AlertRule>(`${alerts}/rules${id ? `/${id}` : ''}`, {
    method: id ? 'PUT' : 'POST',
    data,
  });
}

export function setAlertRuleEnabled(id: string, enabled: boolean) {
  return request(`${alerts}/rules/${id}`, {
    method: 'PATCH',
    data: { enabled },
  });
}

export function deleteAlertRule(id: string) {
  return request(`${alerts}/rules/${id}`, { method: 'DELETE' });
}

export function alertTargets(kind: AlertRuleKind) {
  return request<{ list: { id: string; name: string }[] }>(
    `${alerts}/targets`,
    {
      params: { kind },
      silentError: true,
    },
  );
}

export function alertLogs(params: {
  status?: AlertLogStatus;
  keyword?: string;
  ruleId?: string;
  page: number;
  page_size: number;
}) {
  return request<{ list: AlertLog[]; total: number }>(`${alerts}/logs`, {
    params,
    silentError: true,
  });
}
