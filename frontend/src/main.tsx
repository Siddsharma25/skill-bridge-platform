import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { applyTheme, resolveInitialTheme } from '@/lib/storage/themeCookie'

// Applied before the first render (not inside a component effect) so the
// page never flashes the wrong theme for a frame — the same reason a
// server-rendered app would inline this in <head> instead.
applyTheme(resolveInitialTheme())

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
