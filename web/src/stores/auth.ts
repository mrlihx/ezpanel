import { create } from 'zustand';
import type { User } from '../api/auth';
export const useAuth = create<{
  user: User | null;
  ready: boolean;
  setUser: (user: User | null) => void;
}>((set) => ({
  user: null,
  ready: false,
  setUser: (user) => set({ user, ready: true }),
}));
