// The Redux Toolkit equivalent of ../../lib/auth-store.ts — same shape
// (accessToken, userId), same two operations (setAuth, clearAuth), same
// localStorage persistence. Compare this file + store.ts + hooks.ts
// against that one file to see the actual boilerplate difference; see
// ReduxExamplePage.tsx for a live side-by-side of both stores running at
// once. This slice is NOT used by the real app — auth-store.ts still is —
// this exists purely as a comparison example (see frontend/README.md).
import { createSlice, type PayloadAction } from "@reduxjs/toolkit";

const STORAGE_KEY = "skill-bridge-auth-redux-example";

type AuthState = {
  accessToken: string | null;
  userId: string | null;
};

// Redux Toolkit has no built-in persistence (unlike zustand/middleware's
// persist) — reading/writing localStorage by hand is the plain-Redux way,
// which is exactly the point of this comparison: Zustand's persist()
// wraps this same logic in one line, RTK doesn't ship an equivalent.
function loadPersistedState(): AuthState {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    return raw ? (JSON.parse(raw) as AuthState) : { accessToken: null, userId: null };
  } catch {
    return { accessToken: null, userId: null };
  }
}

const authSlice = createSlice({
  name: "auth",
  initialState: loadPersistedState(),
  reducers: {
    setAuth: (state, action: PayloadAction<{ accessToken: string; userId: string }>) => {
      // Redux Toolkit uses Immer under the hood, so this looks like a
      // direct mutation but isn't one — it's producing a new immutable
      // state behind the scenes. Zustand's set({ ... }) is a plain object
      // merge with no such translation layer.
      state.accessToken = action.payload.accessToken;
      state.userId = action.payload.userId;
    },
    clearAuth: (state) => {
      state.accessToken = null;
      state.userId = null;
    },
  },
});

export const { setAuth, clearAuth } = authSlice.actions;
export default authSlice.reducer;
export { STORAGE_KEY };
