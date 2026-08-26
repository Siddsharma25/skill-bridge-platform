import { describe, it, expect } from "vitest";
import { decodeJwtSubject, decodeJwtRole } from "@/lib/jwt";
import { makeToken } from "@/test/makeToken";

describe("decodeJwtSubject", () => {
  it("returns the sub claim from a real-shaped token", () => {
    const token = makeToken({ sub: "user-123", role: "user" });
    expect(decodeJwtSubject(token)).toBe("user-123");
  });

  it("returns an empty string when sub is missing", () => {
    const token = makeToken({ role: "user" });
    expect(decodeJwtSubject(token)).toBe("");
  });
});

describe("decodeJwtRole", () => {
  it("returns the role claim from a real-shaped token", () => {
    const token = makeToken({ sub: "user-123", role: "admin" });
    expect(decodeJwtRole(token)).toBe("admin");
  });

  it("defaults to 'user' when role is missing (a token issued before RBAC, or a malformed one)", () => {
    const token = makeToken({ sub: "user-123" });
    expect(decodeJwtRole(token)).toBe("user");
  });
});
