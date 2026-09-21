import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { applyTheme, resolveInitialTheme } from '@/lib/storage/themeCookie'
import { initSentry } from '@/lib/sentry'

// Applied before the first render (not inside a component effect) so the
// page never flashes the wrong theme for a frame — the same reason a
// server-rendered app would inline this in <head> instead.
applyTheme(resolveInitialTheme())

// Also before the first render — a crash inside App's own initial render
// (or App's imports) should still be reportable, not just crashes inside
// <ErrorBoundary>'s children. A no-op if VITE_SENTRY_DSN isn't set — see
// src/lib/sentry.ts.
initSentry()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
