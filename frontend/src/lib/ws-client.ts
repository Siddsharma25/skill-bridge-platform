import { createClient } from "graphql-ws";

const API_URL = import.meta.env.VITE_API_URL ?? "http://localhost:8080/query";

function wsUrl(): string {
  const url = new URL(API_URL);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  return url.toString();
}

export function createNotificationsWsClient(getToken: () => string | null) {
  return createClient({
    url: wsUrl(),
    connectionParams: () => {
      const token = getToken();
      return token ? { Authorization: `Bearer ${token}` } : {};
    },
  });
}
