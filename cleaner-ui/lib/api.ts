// Talks to the agent's local API on the same origin (127.0.0.1). Every call
// carries the launch token from the URL that `tidyfleet ui` opened.

const KEY = "tidyfleet-token";

export function initToken(): string | null {
  if (typeof window === "undefined") return null;
  const m = window.location.hash.match(/token=([0-9a-f]+)/);
  if (m) {
    try {
      sessionStorage.setItem(KEY, m[1]);
    } catch {}
    history.replaceState(null, "", window.location.pathname + window.location.search);
    return m[1];
  }
  try {
    return sessionStorage.getItem(KEY);
  } catch {
    return null;
  }
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

export async function api<T>(path: string, body?: unknown, method?: string): Promise<T> {
  let token: string | null = null;
  try {
    token = sessionStorage.getItem(KEY);
  } catch {}
  const res = await fetch(path, {
    method: method ?? (body === undefined ? "GET" : "POST"),
    headers: {
      "X-Tidyfleet-Token": token ?? "",
      ...(body === undefined ? {} : { "Content-Type": "application/json" }),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
    cache: "no-store",
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, data?.error ?? `Request failed (HTTP ${res.status})`);
  return data as T;
}

export function errorText(e: unknown): string {
  return e instanceof Error ? e.message : "Something went wrong";
}

/** The token, for opening another view of the app in a new tab. */
export function tokenForNewTab(): string {
  try {
    return sessionStorage.getItem(KEY) ?? "";
  } catch {
    return "";
  }
}
