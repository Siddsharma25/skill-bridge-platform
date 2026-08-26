import { useEffect, useState } from "react";

import { createNotificationsWsClient } from "@/lib/ws-client";
import { gqlRequest } from "@/lib/graphql-client";
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

const HISTORY_QUERY = /* GraphQL */ `
  query NotificationHistory {
    notificationHistory {
      id
      type
      message
      createdAt
    }
  }
`;

// Subscribes to onNotification for as long as there's an accessToken —
// reconnects on login, tears down on logout (see /lib/ws-client.ts;
// auth is verified at graphql-ws's connection_init, not per-message) —
// and, on mount, backfills from notificationHistory (a Redis Stream on
// the backend, not the same pub/sub channel the subscription rides) so a
// page refresh doesn't lose whatever arrived before this component was
// listening. Live pushes are prepended in front of that backfilled list
// and deduped by id, in case a notification arrives in the gap between
// the history fetch resolving and the subscription actually connecting.
export function useNotifications() {
  const accessToken = useAuthStore((s) => s.accessToken);
  const [notifications, setNotifications] = useState<Notification[]>([]);

  useEffect(() => {
    // NotificationsBell only mounts this hook while accessToken is truthy
    // (see Layout.tsx), so there's no "reset to empty on logout" case to
    // handle here — the component simply unmounts.
    if (!accessToken) return;

    let cancelled = false;
    gqlRequest<{ notificationHistory: Notification[] }>(HISTORY_QUERY, undefined, accessToken)
      .then((data) => {
        if (!cancelled) setNotifications(data.notificationHistory);
      })
      .catch((err) => {
        console.error("failed to load notification history", err);
      });

    // The client is recreated whenever accessToken changes (it's this
    // effect's dependency), so closing over it directly is safe — no need
    // for a ref to read a "current" value.
    const client = createNotificationsWsClient(() => accessToken);
    const unsubscribe = client.subscribe<{ onNotification: Notification }>(
      { query: SUBSCRIPTION },
      {
        next: (result) => {
          const notif = result.data?.onNotification;
          if (!notif) return;
          setNotifications((prev) =>
            [notif, ...prev.filter((n) => n.id !== notif.id)].slice(0, 20),
          );
        },
        error: (err) => {
          console.error("notification subscription error", err);
        },
        complete: () => {},
      },
    );

    return () => {
      cancelled = true;
      unsubscribe();
      void client.dispose();
    };
  }, [accessToken]);

  return notifications;
}
