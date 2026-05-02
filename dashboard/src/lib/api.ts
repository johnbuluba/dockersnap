// Minimal fetch wrapper. Adds the Authorization header when a token is
// configured and surfaces non-2xx responses as thrown Errors so React Query
// classifies them as failures.
import { getToken } from "./token";

export type FetchOptions = RequestInit & { signal?: AbortSignal };

export async function api<T>(path: string, init: FetchOptions = {}): Promise<T> {
  const token = getToken();
  const headers = new Headers(init.headers);
  if (token) headers.set("Authorization", `Bearer ${token}`);
  if (!headers.has("Accept")) headers.set("Accept", "application/json");

  const res = await fetch(path, { ...init, headers });
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new Error(`${res.status} ${res.statusText}${body ? `: ${body}` : ""}`);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}
