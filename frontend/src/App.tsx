import * as Sentry from "@sentry/react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { Layout } from "@/components/Layout";
import { ProtectedRoute } from "@/components/ProtectedRoute";
import { RegisterPage } from "@/features/auth/RegisterPage";
import { LoginPage } from "@/features/auth/LoginPage";
import { ProfilePage } from "@/features/auth/ProfilePage";
import { SkillsPage } from "@/features/skills/SkillsPage";
import { JobsPage } from "@/features/jobs/JobsPage";
import { JobDetailPage } from "@/features/jobs/JobDetailPage";
import { NewJobPage } from "@/features/jobs/NewJobPage";
import { ReduxExamplePage } from "@/features/redux-example/ReduxExamplePage";

const queryClient = new QueryClient();

// A render error anywhere below this point currently just shows a blank
// page in production, with nothing to investigate — see
// src/lib/sentry.ts/docs/DECISIONS.md's Sentry section. Sentry.ErrorBoundary
// is a no-op wrapper (renders children unchanged, reports nothing) when
// Sentry was never initialized (no VITE_SENTRY_DSN), so this is safe to
// leave in place unconditionally rather than gating it separately.
function ErrorFallback({ resetError }: { resetError: () => void }) {
  return (
    <div className="flex min-h-svh flex-col items-center justify-center gap-4 p-6 text-center">
      <h1 className="text-xl font-semibold">Something went wrong</h1>
      <p className="text-muted-foreground max-w-sm text-sm">
        This has been reported. Try reloading the page — if it keeps happening, let us know.
      </p>
      <Button onClick={resetError}>Reload</Button>
    </div>
  );
}

function App() {
  return (
    <Sentry.ErrorBoundary fallback={ErrorFallback} onReset={() => window.location.reload()}>
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <Routes>
            <Route element={<Layout />}>
              <Route path="/" element={<Navigate to="/jobs" replace />} />
              <Route path="/register" element={<RegisterPage />} />
              <Route path="/login" element={<LoginPage />} />
              <Route path="/skills" element={<SkillsPage />} />
              <Route path="/jobs" element={<JobsPage />} />
              <Route path="/jobs/new" element={<NewJobPage />} />
              <Route path="/jobs/:id" element={<JobDetailPage />} />
              <Route path="/redux-example" element={<ReduxExamplePage />} />
              <Route element={<ProtectedRoute />}>
                <Route path="/profile" element={<ProfilePage />} />
              </Route>
            </Route>
          </Routes>
        </BrowserRouter>
      </QueryClientProvider>
    </Sentry.ErrorBoundary>
  );
}

export default App;
