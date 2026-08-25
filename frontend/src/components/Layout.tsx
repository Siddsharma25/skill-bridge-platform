import { Link, Outlet, useNavigate } from "react-router-dom";

import { Button } from "@/components/ui/button";
import { NotificationsBell } from "@/components/NotificationsBell";
import { useAuthStore } from "@/lib/auth-store";

export function Layout() {
  const navigate = useNavigate();
  const { accessToken, clearAuth } = useAuthStore();

  function handleLogout() {
    clearAuth();
    navigate("/login", { replace: true });
  }

  return (
    <div className="min-h-svh">
      <header className="flex items-center gap-4 border-b p-4">
        <Link to="/jobs" className="font-semibold">
          Skill Bridge
        </Link>
        <nav className="flex gap-4 text-sm">
          <Link to="/jobs">Jobs</Link>
          <Link to="/skills">Skills</Link>
          {accessToken && <Link to="/profile">Profile</Link>}
        </nav>
        <div className="ml-auto flex items-center gap-2">
          {accessToken ? (
            <>
              <NotificationsBell />
              <Button variant="outline" size="sm" onClick={handleLogout}>
                Log out
              </Button>
            </>
          ) : (
            <>
              <Button variant="ghost" size="sm" asChild>
                <Link to="/login">Log in</Link>
              </Button>
              <Button size="sm" asChild>
                <Link to="/register">Register</Link>
              </Button>
            </>
          )}
        </div>
      </header>
      <Outlet />
    </div>
  );
}
