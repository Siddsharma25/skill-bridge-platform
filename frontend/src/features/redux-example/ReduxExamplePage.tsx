import { Provider } from "react-redux";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { useAuthStore } from "@/lib/auth-store";
import { reduxExampleStore } from "./store";
import { useReduxExampleDispatch, useReduxExampleSelector } from "./hooks";
import { setAuth, clearAuth } from "./authSlice";

// Fake values only — this page never touches your real session. It
// exists purely to run the exact same "log in / log out" operation
// through two different state-management libraries side by side, so the
// code (not just a description of it) shows the difference.
function fakeCreds() {
  const n = Math.floor(Math.random() * 1000);
  return { accessToken: `fake-token-${n}`, userId: `fake-user-${n}` };
}

function ZustandPanel() {
  // This is the entire API surface: call the hook, optionally with a
  // selector. No Provider, no store wiring in this component at all —
  // useAuthStore already knows which store it reads from.
  const { accessToken, userId, setAuth: setZustandAuth, clearAuth: clearZustandAuth } = useAuthStore();

  return (
    <Card>
      <CardHeader>
        <CardTitle>Zustand</CardTitle>
        <CardDescription>src/lib/auth-store.ts — used by the real app</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3">
        <div className="bg-secondary/60 grid gap-1 rounded-md p-3 font-mono text-xs">
          <div>accessToken: {accessToken ?? "null"}</div>
          <div>userId: {userId ?? "null"}</div>
        </div>
        <div className="flex gap-2">
          <Button
            size="sm"
            onClick={() => {
              const creds = fakeCreds();
              setZustandAuth(creds.accessToken, creds.userId, "user");
            }}
          >
            Simulate login
          </Button>
          <Button size="sm" variant="outline" onClick={() => clearZustandAuth()}>
            Log out
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function ReduxPanel() {
  // Two extra imports (the typed hooks) and one extra concept (dispatching
  // an action object rather than calling a function directly) compared to
  // ZustandPanel above — everything else about reading/updating state is
  // conceptually the same.
  const { accessToken, userId } = useReduxExampleSelector((state) => state.auth);
  const dispatch = useReduxExampleDispatch();

  return (
    <Card>
      <CardHeader>
        <CardTitle>Redux Toolkit</CardTitle>
        <CardDescription>src/features/redux-example/ — comparison only, not used by the real app</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3">
        <div className="bg-secondary/60 grid gap-1 rounded-md p-3 font-mono text-xs">
          <div>accessToken: {accessToken ?? "null"}</div>
          <div>userId: {userId ?? "null"}</div>
        </div>
        <div className="flex gap-2">
          <Button size="sm" onClick={() => dispatch(setAuth(fakeCreds()))}>
            Simulate login
          </Button>
          <Button size="sm" variant="outline" onClick={() => dispatch(clearAuth())}>
            Log out
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

export function ReduxExamplePage() {
  return (
    <div className="mx-auto grid max-w-3xl gap-6 p-8">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Zustand vs. Redux Toolkit</h1>
        <p className="text-muted-foreground text-sm">
          The same "auth store" (accessToken + userId, persisted to localStorage) implemented twice.
          Click each store's buttons independently — they don't affect each other, or your real
          logged-in session.
        </p>
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <ZustandPanel />
        {/* Provider only needs to wrap whatever actually uses the Redux
            store — scoped to just this page, not app-wide, since nothing
            else in this app reads from reduxExampleStore. */}
        <Provider store={reduxExampleStore}>
          <ReduxPanel />
        </Provider>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>What's actually different</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-2 text-sm">
          <p>
            <strong>File count / boilerplate:</strong> Zustand is one file, 21 lines, including
            persistence (<code>lib/auth-store.ts</code>). The Redux Toolkit equivalent is three
            files (<code>authSlice.ts</code>, <code>store.ts</code>, <code>hooks.ts</code>) — a
            slice, a store, and a typed-hooks wrapper — plus manual localStorage persistence, since
            RTK has no built-in <code>persist()</code>.
          </p>
          <p>
            <strong>Provider:</strong> Redux requires wrapping whatever consumes the store in{" "}
            <code>&lt;Provider store=&#123;...&#125;&gt;</code> — see this page's own JSX above.
            Zustand needs no provider at all; the hook <em>is</em> the store.
          </p>
          <p>
            <strong>Updating state:</strong> Zustand calls a function directly (
            <code>setAuth(token, id)</code>). Redux dispatches a plain action object (
            <code>dispatch(setAuth(&#123;...&#125;))</code>) that a reducer interprets — more
            ceremony, but it's also what makes Redux DevTools able to show you a full history of
            every action ever dispatched, time-travel included. Zustand has a devtools middleware
            too, but action-by-action history isn't its default model the way it is Redux's.
          </p>
          <p>
            <strong>Why this app uses Zustand for its real auth store:</strong> one small piece of
            client state, one component tree, no need for time-travel debugging or a large team
            convention enforcing action-object discipline — Redux's extra structure pays for itself
            at a scale this app isn't at.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
