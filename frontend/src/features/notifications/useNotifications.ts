import { useEffect, useState } from "react";

import { createNotificationsWsClient } from "@/lib/ws-client";
import { useAuthStore } from "@/lib/auth-store";

export type Notification = {
  id: string;
  type: string;
  message: string;
  createdAt: string;
};

const SUBSCRIPTION = /* GraphQL */ `
  subscription OnNotification {
    onNotification {
      id
      type
      message
      createdAt
    }
  }
`;

// Subscribes to onNotification for as long as there's an accessToken —
// reconnects on login, tears down on logout (see /lib/ws-client.ts;
// auth is verified at graphql-ws's connection_init, not per-message).
export function useNotifications() {
  const accessToken = useAuthStore((s) => s.accessToken);
  const [notifications, setNotifications] = useState<Notification[]>([]);

  useEffect(() => {
    if (!accessToken) return;

    // The client is recreated whenever accessToken changes (it's this
    // effect's dependency), so closing over it directly is safe — no need
    // for a ref to read a "current" value.
    const client = createNotificationsWsClient(() => accessToken);
    const unsubscribe = client.subscribe<{ onNotification: Notification }>(
      { query: SUBSCRIPTION },
      {
        next: (result) => {
          if (result.data?.onNotification) {
            setNotifications((prev) => [result.data!.onNotification, ...prev].slice(0, 20));
          }
        },
        error: (err) => {
          console.error("notification subscription error", err);
        },
        complete: () => {},
      },
    );

    return () => {
      unsubscribe();
      void client.dispose();
    };
  }, [accessToken]);

  return notifications;
}
