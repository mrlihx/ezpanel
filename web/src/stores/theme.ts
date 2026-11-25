import { create } from 'zustand';
type Mode = 'system' | 'light' | 'dark';
const stored = localStorage.getItem('panel-theme');
const initial: Mode =
  stored === 'light' || stored === 'dark' ? stored : 'system';
const media = matchMedia('(prefers-color-scheme: dark)');
export const useTheme = create<{
  mode: Mode;
  systemDark: boolean;
  setMode: (mode: Mode) => void;
}>((set) => ({
  mode: initial,
  systemDark: media.matches,
  setMode: (mode) => {
    localStorage.setItem('panel-theme', mode);
    set({ mode });
  },
}));
media.addEventListener('change', (event) =>
  useTheme.setState({ systemDark: event.matches }),
);
