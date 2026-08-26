// Builds a real (unsigned, but structurally real) three-part
// header.payload.signature JWT string — shared by any test that needs to
// exercise the actual base64url-decode path (lib/jwt.ts) or hand a
// component a token shaped like the real thing, rather than a stand-in
// string that happens to satisfy a mock.
export function makeToken(claims: Record<string, unknown>): string {
  const header = { alg: "RS256", typ: "JWT" };
  // btoa (browser-native, no Node "Buffer" type available under this
  // project's browser-only tsconfig) — same primitive lib/jwt.ts's own
  // decode side uses (atob), just the encode direction.
  const b64url = (obj: object) =>
    btoa(JSON.stringify(obj)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  return `${b64url(header)}.${b64url(claims)}.fake-signature`;
}
