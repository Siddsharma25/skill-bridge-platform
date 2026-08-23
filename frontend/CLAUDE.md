# frontend/

React 19 + Vite 8 + TypeScript SPA. Requires **Node 20.19+ or 22.12+** (Vite 8 hard-requires it) — this repo pins Node 22 via `.nvmrc`; run `nvm use` before installing.

## Commands

```
npm install
npm run dev
npm run build   # tsc -b && vite build
npm run lint
```

## Conventions

- Path alias `@/*` → `src/*` (see `tsconfig.app.json`, `components.json`, `vite-tsconfig-paths` in `vite.config.ts`).
- shadcn/ui components live in `src/components/ui/` — generate new ones with `npx shadcn@latest add <component>`, don't hand-write them.
- Talks only to `api-gateway`'s GraphQL endpoint (`VITE_API_URL`) — never call backend services directly.
- TanStack Query for server state, Zustand for client-only UI state — don't duplicate server data into a Zustand store.

## Don't

- Don't downgrade below Node 20.19 to "fix" a build error — the actual fix is using the pinned Node 22 via `.nvmrc`/nvm.
