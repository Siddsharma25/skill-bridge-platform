// Runs once before every test file (see vite.config.ts's test.setupFiles).
import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";
// Extends Vitest's `expect` with jest-dom's DOM-specific matchers
// (toBeInTheDocument, toHaveTextContent, etc.) — without this import,
// those matchers don't exist and every test would need to fall back to
// verbose manual assertions on raw DOM properties.
import "@testing-library/jest-dom/vitest";

// React Testing Library's own auto-cleanup normally hooks itself into a
// *global* afterEach (Jest always provides one) — this project's config
// deliberately doesn't enable Vitest's `globals: true` (every test file
// imports describe/it/expect explicitly instead, matching this
// codebase's no-ambient-magic style elsewhere), so that auto-detection
// never fires and DOM from one test leaks into the next. Registering it
// explicitly here, once, is the direct fix — every test file still
// stays fully explicit about its own imports.
afterEach(() => {
  cleanup();
});
