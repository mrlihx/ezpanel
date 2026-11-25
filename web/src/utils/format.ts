import dayjs from 'dayjs';
import utc from 'dayjs/plugin/utc';
import timezone from 'dayjs/plugin/timezone';
import { useAuth } from '../stores/auth';
dayjs.extend(utc);
dayjs.extend(timezone);
export const formatTime = (value: string) =>
  dayjs(value)
    .tz(useAuth.getState().user?.timezone ?? 'Asia/Shanghai')
    .format('YYYY-MM-DD HH:mm:ss');
export const validPassword = (value: string) =>
  Array.from(value).length >= 8 &&
  new TextEncoder().encode(value).length <= 72 &&
  /\p{L}/u.test(value) &&
  /\p{Nd}/u.test(value);
export const safeRedirect = (value: string | null) =>
  value?.startsWith('/') &&
  !value.startsWith('//') &&
  !value.includes('\\') &&
  !Array.from(value).some((char) => char.charCodeAt(0) <= 32) &&
  value.split('?')[0] !== '/login'
    ? value
    : '/';

export const formatSize = (value: number | null) => {
  if (value === null) return '--';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let size = value;
  let index = 0;
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index++;
  }
  return `${size.toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
};
