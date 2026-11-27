import axios, { type AxiosRequestConfig } from 'axios';
import { useAuth } from '../stores/auth';

type Options = AxiosRequestConfig & { silentError?: boolean };
type Envelope<T> = { code: number; msg: string; data: T };
export class APIError extends Error {
  constructor(
    public code: number,
    message: string,
    public status?: number,
  ) {
    super(message);
  }
}
let showError: (message: string) => void = () => {};
export function configureErrors(handler: typeof showError) {
  showError = handler;
}
const client = axios.create({
  baseURL: '/',
  headers: { 'X-Panel-Request': '1' },
});
export async function request<T>(
  url: string,
  options: Options = {},
): Promise<T> {
  try {
    const result = await client.request<Envelope<T>>({ url, ...options });
    if (result.data.code !== 0)
      throw new APIError(result.data.code, result.data.msg);
    return result.data.data;
  } catch (error) {
    if (axios.isCancel(error)) throw error;
    const body = axios.isAxiosError<Envelope<unknown>>(error)
      ? error.response?.data
      : undefined;
    const failure =
      error instanceof APIError
        ? error
        : new APIError(
            body?.code ?? 5000,
            body?.msg ?? '请求失败，请检查连接后重试',
            axios.isAxiosError(error) ? error.response?.status : undefined,
          );
    const sessionEnded =
      failure.status === 404 &&
      (url === '/api/auth/me' || url === '/api/auth/activity') &&
      Boolean(useAuth.getState().user);
    if ((failure.code === 1002 && url !== '/api/auth/login') || sessionEnded) {
      useAuth.getState().setUser(null);
      if (location.pathname !== '/login')
        location.assign(
          `/login?redirect=${encodeURIComponent(location.pathname + location.search + location.hash)}`,
        );
    }
    if (!options.silentError && failure.code !== 1002)
      showError(failure.message);
    throw failure;
  }
}
