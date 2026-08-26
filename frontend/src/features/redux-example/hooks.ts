// The typed useDispatch/useSelector wrapper Redux Toolkit's own docs
// recommend for TypeScript — every component that touches this store
// imports these instead of the untyped react-redux hooks directly. This
// file has no Zustand equivalent: useAuthStore's plain
// `useAuthStore((s) => s.accessToken)` already gets full type inference
// for free, no wrapper needed.
import { useDispatch, useSelector, type TypedUseSelectorHook } from "react-redux";

import type { ReduxExampleDispatch, ReduxExampleState } from "./store";

export const useReduxExampleDispatch = useDispatch.withTypes<ReduxExampleDispatch>();
export const useReduxExampleSelector: TypedUseSelectorHook<ReduxExampleState> = useSelector;
