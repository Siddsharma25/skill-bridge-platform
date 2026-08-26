import { create } from "zustand";
import { persist } from "zustand/middleware";

type AuthState = {
  accessToken: string | null;
  userId: string | null;
  // RBAC (see docs/DECISIONS.md) — "user" or "admin", decoded from the
  // JWT's role claim at login/register time (see lib/jwt.ts). Purely a
  // UI convenience for deciding what to show (e.g. SkillsPage's
  // admin-only form) — the actual authorization decision is always
  // re-checked server-side from the verified token, never trusted from
  // this client-side copy alone.
  role: string | null;
  setAuth: (accessToken: string, userId: string, role: string) => void;
  clearAuth: () => void;
};

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      accessToken: null,
      userId: null,
      role: null,
      setAuth: (accessToken, userId, role) => set({ accessToken, userId, role }),
      clearAuth: () => set({ accessToken: null, userId: null, role: null }),
    }),
    { name: "skill-bridge-auth" },
  ),
);
