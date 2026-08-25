// Small env-reading helpers mirroring backend/cmd/<service>/main.go's
// envOr pattern on the Go side, rather than pulling in @nestjs/config for
// a handful of variables — this service only ever reads a handful of
// env vars, so a config module is more ceremony than value here.

/** envOr returns process.env[key], or fallback if it's unset/empty. */
export function envOr(key: string, fallback: string): string {
  const value = process.env[key];
  return value === undefined || value === '' ? fallback : value;
}

/** env returns process.env[key], or undefined if it's unset/empty. */
export function env(key: string): string | undefined {
  const value = process.env[key];
  return value === undefined || value === '' ? undefined : value;
}
