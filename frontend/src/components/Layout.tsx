import { Link, Outlet, useLocation, useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { NotificationsBell } from "@/components/NotificationsBell";
import { ThemeToggle } from "@/components/ThemeToggle";
import { PaintSplashBackground } from "@/components/PaintSplashBackground";
import { useAuthStore } from "@/lib/auth-store";
import { cn } from "@/lib/utils";

function NavLink({ to, children }: { to: string; children: React.ReactNode }) {
  const location = useLocation();
  const active = location.pathname.startsWith(to);
  return (
    <Link
      to={to}
      aria-current={active ? "page" : undefined}
      className={cn(
        "relative px-1 py-1 transition-colors",
        active ? "text-foreground font-medium" : "text-muted-foreground hover:text-foreground",
      )}
    >
      {children}
      <span
        className={cn(
          "absolute -bottom-[17px] left-0 h-0.5 w-full origin-left scale-x-0 rounded-full bg-gradient-to-r from-fuchsia-500 via-violet-500 to-sky-500 transition-transform duration-200",
          active && "scale-x-100",
        )}
      />
    </Link>
  );
}

export function Layout() {
  const navigate = useNavigate();
  const location = useLocation();
  const queryClient = useQueryClient();
  const { accessToken, clearAuth } = useAuthStore();

  function handleLogout() {
    clearAuth();
    queryClient.clear();
    navigate("/login", { replace: true });
  }

  // Hides the redundant nav CTA for whichever auth page you're already on
  // (e.g. showing a "Log in" nav button while the Login page's own "Log
  // in" submit button is right there in the middle of the screen reads as
  // a duplicate control, not two different actions).
  const onLogin = location.pathname === "/login";
  const onRegister = location.pathname === "/register";

  return (
    <div className="min-h-svh">
      {/* WCAG 2.4.1 (Bypass Blocks): lets a keyboard/screen-reader user
          jump straight past the header nav to the page's actual content
          instead of tabbing through every nav link on every page load.
          Visually hidden until it receives focus (Tab from a fresh page
          load lands here first), via the sr-only/focus:not-sr-only pair
          Tailwind ships for exactly this pattern. */}
      <a
        href="#main-content"
        className="bg-background text-foreground focus:ring-ring sr-only rounded-md px-4 py-2 focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-50 focus:ring-2"
      >
        Skip to main content
      </a>
      <PaintSplashBackground />
      <header className="bg-background/70 sticky top-0 z-20 flex items-center gap-6 border-b px-6 py-3 backdrop-blur-md">
        <Link to="/jobs" className="flex items-center gap-2 font-semibold tracking-tight">
          <span className="flex size-8 items-center justify-center rounded-xl bg-gradient-to-br from-fuchsia-500 via-violet-500 to-sky-500 text-sm text-white shadow-md shadow-violet-500/30">
            SB
          </span>
          <span className="bg-gradient-to-r from-fuchsia-600 via-violet-600 to-sky-600 bg-clip-text text-transparent">
            Skill Bridge
          </span>
        </Link>
        <nav aria-label="Primary" className="flex gap-5 text-sm">
          <NavLink to="/jobs">Jobs</NavLink>
          <NavLink to="/skills">Skills</NavLink>
          {accessToken && <NavLink to="/profile">Profile</NavLink>}
          <NavLink to="/redux-example">Redux vs Zustand</NavLink>
        </nav>
        <div className="ml-auto flex items-center gap-2">
          <ThemeToggle />
          {accessToken ? (
            <>
              <NotificationsBell />
              <Button variant="outline" size="sm" onClick={handleLogout}>
                Log out
              </Button>
            </>
          ) : (
            <>
              {!onLogin && (
                <Button variant="ghost" size="sm" asChild>
                  <Link to="/login">Log in</Link>
                </Button>
              )}
              {!onRegister && (
                <Button
                  size="sm"
                  asChild
                  className="border-0 bg-gradient-to-r from-fuchsia-500 via-violet-500 to-sky-500 text-white shadow-md shadow-violet-500/30 hover:opacity-90"
                >
                  <Link to="/register">Register</Link>
                </Button>
              )}
            </>
          )}
        </div>
      </header>
      {/* tabIndex={-1}: focusable via the skip link's #main-content jump
          (so focus visibly moves there, not just the scroll position),
          but not part of the regular Tab order otherwise. */}
      <main id="main-content" tabIndex={-1}>
        <Outlet />
      </main>
    </div>
  );
}
