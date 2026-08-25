const API_URL = import.meta.env.VITE_API_URL ?? "http://localhost:8080/query";

export class GraphQLError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "GraphQLError";
  }
}

export async function gqlRequest<TData, TVariables extends Record<string, unknown> = Record<string, unknown>>(
  query: string,
  variables?: TVariables,
  token?: string | null,
): Promise<TData> {
  const res = await fetch(API_URL, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: JSON.stringify({ query, variables }),
  });

  const json = (await res.json()) as { data?: TData; errors?: { message: string }[] };

  if (json.errors?.length) {
    throw new GraphQLError(json.errors[0].message);
  }

  if (!json.data) {
    throw new GraphQLError(`Request failed with status ${res.status}`);
  }

  return json.data;
}
