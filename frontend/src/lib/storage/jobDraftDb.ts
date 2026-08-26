// Raw IndexedDB — no idb/Dexie wrapper — specifically so the actual
// mechanism is visible: an async, versioned database opened once, object
// stores created only inside onupgradeneeded, and every read/write
// wrapped in its own transaction. This is the browser storage mechanism
// that actually fits a multi-field draft object well: localStorage/
// sessionStorage only store strings (you'd JSON.stringify/parse by hand
// either way), but IndexedDB is the one built for structured records and
// the only one of the four with real async, non-blocking reads/writes —
// the others are all synchronous and can jank the main thread on a large
// value.
//
// Used by NewJobPage to autosave an in-progress job posting: refresh the
// tab, or navigate away and back, and the draft is still there — until
// you actually submit, at which point clearDraft() removes it.

const DB_NAME = "skill-bridge";
const DB_VERSION = 1;
const STORE_NAME = "jobDrafts";
const DRAFT_KEY = "current";

export type JobDraft = {
  title: string;
  description: string;
  requiredSkillIds: string[];
};

function openDb(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, DB_VERSION);

    // Fires only when DB_VERSION increases (or the DB doesn't exist yet)
    // — the one place object stores are allowed to be created/altered.
    request.onupgradeneeded = () => {
      const db = request.result;
      if (!db.objectStoreNames.contains(STORE_NAME)) {
        db.createObjectStore(STORE_NAME);
      }
    };

    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

export async function saveDraft(draft: JobDraft): Promise<void> {
  const db = await openDb();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, "readwrite");
    tx.objectStore(STORE_NAME).put(draft, DRAFT_KEY);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
}

export async function loadDraft(): Promise<JobDraft | null> {
  const db = await openDb();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, "readonly");
    const request = tx.objectStore(STORE_NAME).get(DRAFT_KEY);
    request.onsuccess = () => resolve((request.result as JobDraft | undefined) ?? null);
    request.onerror = () => reject(request.error);
  });
}

export async function clearDraft(): Promise<void> {
  const db = await openDb();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, "readwrite");
    tx.objectStore(STORE_NAME).delete(DRAFT_KEY);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
}
