import { useEffect, useRef, useState } from "react";
import { Bell } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useNotifications } from "@/features/notifications/useNotifications";
import { cn } from "@/lib/utils";

export function NotificationsBell() {
  const notifications = useNotifications();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function onClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener("mousedown", onClick);
    return () => document.removeEventListener("mousedown", onClick);
  }, []);

  // WCAG 2.1.2 (No Keyboard Trap) / standard menu-widget convention:
  // Escape closes the dropdown and returns focus to the button that
  // opened it, so a keyboard user is never left with an open panel and
  // no obvious way out other than clicking elsewhere.
  useEffect(() => {
    if (!open) return;
    function onKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") {
        setOpen(false);
        ref.current?.querySelector("button")?.focus();
      }
    }
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [open]);

  return (
    <div className="relative" ref={ref}>
      <Button
        variant="ghost"
        size="icon"
        onClick={() => setOpen((o) => !o)}
        className={cn("relative", open && "bg-accent")}
        aria-haspopup="true"
        aria-expanded={open}
        aria-label={`Notifications${notifications.length > 0 ? ` (${notifications.length} unread)` : ""}`}
      >
        <Bell className="size-4" />
        {notifications.length > 0 && (
          <span
            aria-hidden="true"
            className="bg-primary text-primary-foreground absolute -top-1 -right-1 flex size-4 animate-pulse items-center justify-center rounded-full text-[10px] shadow-sm"
          >
            {notifications.length}
          </span>
        )}
      </Button>
      {/* Visually hidden live region: announces new notifications as they
          arrive to a screen reader, independent of whether the dropdown
          below is open — the same information sighted users get from the
          badge count updating, made available non-visually too (WCAG
          4.1.3, Status Messages). "polite" so it queues behind whatever
          the user is already doing rather than interrupting speech. */}
      <span role="status" aria-live="polite" className="sr-only">
        {notifications.length > 0
          ? `${notifications.length} notification${notifications.length === 1 ? "" : "s"}`
          : ""}
      </span>
      {open && (
        <div
          role="menu"
          aria-label="Notifications"
          className="bg-popover animate-in-up absolute right-0 z-10 mt-2 w-80 rounded-lg border p-2 shadow-lg"
        >
          {notifications.length === 0 ? (
            <p className="text-muted-foreground p-4 text-center text-sm">No notifications yet.</p>
          ) : (
            <ul className="grid gap-1">
              {notifications.map((n) => (
                <li
                  key={n.id}
                  role="menuitem"
                  className="hover:bg-accent/60 rounded-md p-2 text-sm transition-colors"
                >
                  <p>{n.message}</p>
                  <p className="text-muted-foreground text-xs">
                    {new Date(n.createdAt).toLocaleString()}
                  </p>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}
