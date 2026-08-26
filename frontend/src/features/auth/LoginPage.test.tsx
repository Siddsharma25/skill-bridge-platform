import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { LoginPage } from "@/features/auth/LoginPage";
import { useAuthStore } from "@/lib/auth-store";
import { makeToken } from "@/test/makeToken";
import * as authApi from "@/features/auth/api";

// Only `login` is mocked — everything else in this module (types, other
// exports) stays real, so the test exercises the actual component code
// path (react-hook-form + Zod validation, TanStack Query's useMutation,
// the real decodeJwtSubject/decodeJwtRole calls in the onSuccess
// handler) and only stubs the one thing that would otherwise be a real
// network call.
vi.mock("@/features/auth/api", async () => {
  const actual = await vi.importActual<typeof import("@/features/auth/api")>("@/features/auth/api");
  return { ...actual, login: vi.fn() };
});

function renderLoginPage() {
  const queryClient = new QueryClient();
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={["/login"]}>
        <LoginPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  // Reset the real Zustand store (not a mock — see auth-store.ts, it's
  // just a plain external store) so one test's login doesn't leak into
  // the next.
  useAuthStore.setState({ accessToken: null, userId: null, role: null });
  vi.mocked(authApi.login).mockReset();
});

describe("LoginPage", () => {
  it("shows a validation error for an invalid email instead of submitting", async () => {
    const user = userEvent.setup();
    renderLoginPage();

    await user.type(screen.getByLabelText(/email/i), "not-an-email");
    await user.type(screen.getByLabelText(/password/i), "password123");
    await user.click(screen.getByRole("button", { name: /log in/i }));

    expect(await screen.findByText(/enter a valid email/i)).toBeInTheDocument();
    expect(authApi.login).not.toHaveBeenCalled();
  });

  it("on successful login, decodes the token and stores accessToken/userId/role", async () => {
    const token = makeToken({ sub: "user-42", role: "admin" });
    vi.mocked(authApi.login).mockResolvedValue({ accessToken: token, userId: "" });

    const user = userEvent.setup();
    renderLoginPage();

    await user.type(screen.getByLabelText(/email/i), "person@example.com");
    await user.type(screen.getByLabelText(/password/i), "password123");
    await user.click(screen.getByRole("button", { name: /log in/i }));

    // login's own AuthPayload.userId always comes back "" by design (see
    // schema.resolvers.go's Login resolver) — this asserts the client-side
    // workaround (decoding sub/role from the token itself) actually ran,
    // not just that *some* value got stored.
    await waitFor(() => {
      expect(useAuthStore.getState().accessToken).toBe(token);
    });
    expect(useAuthStore.getState().userId).toBe("user-42");
    expect(useAuthStore.getState().role).toBe("admin");
  });

  it("shows the mutation's error message when login fails", async () => {
    const { GraphQLError } = await import("@/lib/graphql-client");
    vi.mocked(authApi.login).mockRejectedValue(new GraphQLError("invalid email or password"));

    const user = userEvent.setup();
    renderLoginPage();

    await user.type(screen.getByLabelText(/email/i), "person@example.com");
    await user.type(screen.getByLabelText(/password/i), "wrong-password");
    await user.click(screen.getByRole("button", { name: /log in/i }));

    expect(await screen.findByText(/invalid email or password/i)).toBeInTheDocument();
    expect(useAuthStore.getState().accessToken).toBeNull();
  });
});
