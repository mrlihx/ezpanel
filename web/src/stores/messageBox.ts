import { create } from 'zustand';
export const useMessageBox = create<{
  open: boolean;
  tab: string;
  taskId: string | null;
  show: (taskId?: string) => void;
  close: () => void;
  selectTab: (tab: string) => void;
}>((set) => ({
  open: false,
  tab: 'tasks',
  taskId: null,
  show: (taskId) =>
    set({ open: true, tab: taskId ? 'logs' : 'tasks', taskId: taskId ?? null }),
  close: () => set({ open: false }),
  selectTab: (tab) => set({ tab, taskId: null }),
}));
