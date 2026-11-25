import { request } from './client';

export type CertificateAlgorithm =
  'rsa2048' | 'rsa4096' | 'ecdsa256' | 'ecdsa384';

export type Certificate = {
  id: string;
  domains: string[];
  algorithm: CertificateAlgorithm;
  issuer: string;
  notBefore: string;
  notAfter: string;
  source: 'import' | 'letsencrypt';
  validationMethod: '' | 'http-01' | 'dns-01';
  email: string;
  createdAt: string;
};
export type CertificateDetail = Certificate & {
  certificate: string;
  privateKey?: string;
  keyConfigured: boolean;
};
export type CertificateOrder = {
  id: string;
  domains: string[];
  algorithm: CertificateAlgorithm;
  email: string;
  method: 'http-01' | 'dns-01';
  siteId: string;
  status: 'creating' | 'pending' | 'issuing' | 'valid' | 'failed';
  error: string;
  certificateId: string;
  autoDeploy: boolean;
  taskId: string;
  challenges: {
    domain: string;
    type: string;
    recordName: string;
    value: string;
  }[];
  createdAt: string;
};
export type CertificateOrderInput = Pick<
  CertificateOrder,
  'domains' | 'email' | 'method' | 'algorithm'
> & { siteId?: string; renewalId?: string; acceptTerms: true };

export const certificates = (page: number, size: number, keyword: string) =>
  request<{ list: Certificate[]; total: number }>('/api/certificates', {
    params: { page, page_size: size, keyword },
    silentError: true,
  });
export const certificate = (id: string) =>
  request<CertificateDetail>(`/api/certificates/${encodeURIComponent(id)}`, {
    silentError: true,
  });
export const importCertificate = (certificate: string, privateKey: string) =>
  request<Certificate>('/api/certificates', {
    method: 'POST',
    data: { certificate, privateKey },
    silentError: true,
  });
export const deleteCertificate = (id: string) =>
  request(`/api/certificates/${encodeURIComponent(id)}`, {
    method: 'DELETE',
    silentError: true,
  });
import { fetchDownload } from '../utils/download';

export async function downloadCertificate(id: string, siteId?: string) {
  await fetchDownload(
    `/api/certificates/${encodeURIComponent(id)}/download${siteId ? `?siteId=${encodeURIComponent(siteId)}` : ''}`,
    `certificate-${id}.zip`,
    '证书下载失败',
  );
}
export const certificateOrders = (siteId?: string) =>
  request<{ list: CertificateOrder[] }>('/api/certificates/orders', {
    params: { siteId },
    silentError: true,
  });
export const certificateOrder = (id: string) =>
  request<CertificateOrder>(
    `/api/certificates/orders/${encodeURIComponent(id)}`,
    { silentError: true },
  );
export const createCertificateOrder = (data: CertificateOrderInput) =>
  request<{ orderId: string; taskId: string }>('/api/certificates/orders', {
    method: 'POST',
    data,
    silentError: true,
  });
export const completeCertificateOrder = (id: string) =>
  request<{ taskId: string }>(
    `/api/certificates/orders/${encodeURIComponent(id)}/complete`,
    { method: 'POST', silentError: true },
  );

export const retryCertificateOrderDeploy = (id: string) =>
  request<{ orderId: string; taskId: string }>(
    `/api/certificates/orders/${encodeURIComponent(id)}/deploy`,
    { method: 'POST', silentError: true },
  );
