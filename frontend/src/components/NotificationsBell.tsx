import { useState } from "react";
import { Bell } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useNotifications } from "@/features/notifications/useNotifications";

export function NotificationsBell() {
  const notifications = useNotifications();
  const [open, setOpen] = useState(false);

  return (
    <div className="relative">
      <Button variant="ghost" size="icon" onClick={() => setOpen((o) => !o)}>
        <Bell className="size-4" />
        {notifications.length > 0 && (
          <span className="bg-primary text-primary-foreground absolute -top-1 -right-1 flex size-4 items-center justify-center rounded-full text-[10px]">
            {notifications.length}
          </span>
        )}
      </Button>
      {open && (
        <div className="bg-popover absolute right-0 z-10 mt-2 w-80 rounded-md border p-2 shadow-md">
          {notifications.length === 0 ? (
            <p className="text-muted-foreground p-2 text-sm">No notifications yet.</p>
          ) : (
            <ul className="grid gap-1">
              {notifications.map((n) => (
                <li key={n.id} className="rounded-sm p-2 text-sm">
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
