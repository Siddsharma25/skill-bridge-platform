import { configureStore } from "@reduxjs/toolkit";

import authReducer, { STORAGE_KEY } from "./authSlice";

export const reduxExampleStore = configureStore({
  reducer: { auth: authReducer },
});

// Manual persistence: subscribe to every state change and write it to
// localStorage — the plain-Redux way, since RTK has no persist()
// middleware of its own. This is the same handful of lines
// zustand/middleware's persist() does internally and hands you for free.
reduxExampleStore.subscribe(() => {
  const { auth } = reduxExampleStore.getState();
  localStorage.setItem(STORAGE_KEY, JSON.stringify(auth));
});

export type ReduxExampleState = ReturnType<typeof reduxExampleStore.getState>;
export type ReduxExampleDispatch = typeof reduxExampleStore.dispatch;
