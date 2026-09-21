import * as Sentry from "@sentry/react";

// This is the frontend half of this repo's one Sentry integration — see
// backend/internal/platform/sentry (the Go side) and docs/DECISIONS.md's
// Sentry section for the full reasoning: production (Render + Vercel)
// ships with zero error visibility today, since the local
// Prometheus/Loki/Jaeger stack (docker/docker-compose.observability.yml)
// never runs there. An uncaught render error currently just shows a blank
// page in production with nothing to investigate — this closes that gap.
//
// Same degrade-gracefully convention the backend uses for every optional
// dependency: no VITE_SENTRY_DSN means this is a no-op, not a build/runtime
// failure — there's no live Sentry project by default (see
// docs/DEPLOYMENT.md for creating one).
export function initSentry(): void {
  const dsn = import.meta.env.VITE_SENTRY_DSN;
  if (!dsn) {
    return;
  }

  const tracesSampleRate = Number(import.meta.env.VITE_SENTRY_TRACES_SAMPLE_RATE ?? "0");

  Sentry.init({
    dsn,
    environment: import.meta.env.VITE_SENTRY_ENVIRONMENT ?? import.meta.env.MODE,
    // Opt-in, not on by default — mirrors the backend's
    // SENTRY_TRACES_SAMPLE_RATE default of 0, so this doesn't silently
    // burn Sentry's free-tier performance-unit quota the first time this
    // ships.
    tracesSampleRate: Number.isFinite(tracesSampleRate) ? tracesSampleRate : 0,
    integrations: tracesSampleRate > 0 ? [Sentry.browserTracingIntegration()] : [],
    // Strip any captured request body — the register/login forms submit a
    // plaintext password, and Sentry's browser SDK doesn't know to scrub a
    // GraphQL POST body the way its own header/cookie denylist scrubs
    // known-sensitive keys. Defense in depth alongside the backend's own
    // body-capture opt-out (internal/platform/sentry's doc comment). See
    // docs/SECURITY.md.
    beforeSend(event) {
      if (event.request?.data) {
        delete event.request.data;
      }
      return event;
    },
  });
}
