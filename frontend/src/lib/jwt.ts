// Decodes the `sub` claim from a JWT's payload without verifying the
// signature — verification already happened server-side (api-gateway
// checks every token against auth-service's JWKS); this is purely so the
// client can recover the user ID that `login` doesn't return on the wire
// (see schema.resolvers.go's Login resolver).
export function decodeJwtSubject(token: string): string {
  const payload = token.split(".")[1];
  const json = atob(payload.replace(/-/g, "+").replace(/_/g, "/"));
  const claims = JSON.parse(json) as { sub?: string };
  return claims.sub ?? "";
}
