// Plain document.cookie usage — no library. Cookies are the one browser
// storage mechanism that travels with every HTTP request to the same
// origin (this app has no server that reads it, but a real app with
// server-rendering would render the correct theme on the very first
// response instead of flashing the wrong one) and the only one with a
// built-in expiry the *browser* enforces, not your own cleanup code.

const COOKIE_NAME = "theme";
const ONE_YEAR_SECONDS = 60 * 60 * 24 * 365;

export type Theme = "light" | "dark";

export function readThemeCookie(): Theme | null {
  const match = document.cookie.match(/(?:^|; )theme=(light|dark)(?:;|$)/);
  return match ? (match[1] as Theme) : null;
}

export function writeThemeCookie(theme: Theme): void {
  // max-age (seconds), not expires (a date string) — simpler to get right.
  // SameSite=Lax + no Secure flag: fine for this app (http://localhost in
  // dev, and no cross-site request this cookie needs to ride along with).
  document.cookie = `${COOKIE_NAME}=${theme}; max-age=${ONE_YEAR_SECONDS}; path=/; SameSite=Lax`;
}

export function applyTheme(theme: Theme): void {
  document.documentElement.classList.toggle("dark", theme === "dark");
}

// Resolves the theme to show on first load: an explicit cookie choice
// wins, otherwise fall back to the OS-level preference (matches this
// app's existing `@media (prefers-color-scheme: dark)` CSS, so a user who
// never touches the toggle still gets a theme-appropriate page).
export function resolveInitialTheme(): Theme {
  const cookie = readThemeCookie();
  if (cookie) return cookie;
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}
