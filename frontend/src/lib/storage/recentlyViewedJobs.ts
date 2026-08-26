// sessionStorage, not localStorage: this list should reset when the tab
// closes, not persist forever — "what did I look at in this browsing
// session" is a genuinely different question from "remember me across
// visits" (which is what localStorage's auth token is for). Open the app
// in two separate tabs and each gets its own independent recent-jobs
// list, since sessionStorage is scoped per tab, not per origin like
// localStorage.

const KEY = "recentlyViewedJobs";
const MAX_ENTRIES = 5;

export type RecentJob = { id: string; title: string };

export function getRecentlyViewedJobs(): RecentJob[] {
  try {
    const raw = sessionStorage.getItem(KEY);
    return raw ? (JSON.parse(raw) as RecentJob[]) : [];
  } catch {
    // Malformed JSON (shouldn't happen since this module is the only
    // writer) or sessionStorage unavailable (private-browsing edge cases
    // in some browsers) — either way, an empty list is a safe fallback,
    // not a crash.
    return [];
  }
}

export function recordJobView(job: RecentJob): void {
  const existing = getRecentlyViewedJobs().filter((j) => j.id !== job.id);
  const next = [job, ...existing].slice(0, MAX_ENTRIES);
  try {
    sessionStorage.setItem(KEY, JSON.stringify(next));
  } catch {
    // Storage full or disabled — recording "recently viewed" is a nicety,
    // never worth failing the page over.
  }
}
