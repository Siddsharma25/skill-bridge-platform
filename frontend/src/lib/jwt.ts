// Decodes claims from a JWT's payload without verifying the signature —
// verification already happened server-side (api-gateway checks every
// token against auth-service's JWKS); this is purely so the client can
// read claims register/login already handed it, or that login's
// AuthPayload doesn't return on the wire (see schema.resolvers.go's
// Login resolver comment re: userId).
type JwtClaims = { sub?: string; role?: string };

function decodeJwtClaims(token: string): JwtClaims {
  const payload = token.split(".")[1];
  const json = atob(payload.replace(/-/g, "+").replace(/_/g, "/"));
  return JSON.parse(json) as JwtClaims;
}

export function decodeJwtSubject(token: string): string {
  return decodeJwtClaims(token).sub ?? "";
}

// The RBAC role claim (see docs/DECISIONS.md's RBAC notes) — "user" or
// "admin". Both register and login only return an accessToken (no role
// field on AuthPayload), so the client always reads this from the token
// itself rather than the gateway adding a fourth way to learn it.
export function decodeJwtRole(token: string): string {
  return decodeJwtClaims(token).role ?? "user";
}
