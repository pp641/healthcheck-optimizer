import "server-only";

import { cookies } from "next/headers";
import { redirect } from "next/navigation";

// The dashboard talks to the Go API from the server only; the session token
// lives in an httpOnly cookie and never reaches browser JavaScript.
export const API_URL = process.env.API_URL ?? "http://localhost:8080";
export const SESSION_COOKIE = "tf_session";

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

async function request(path: string, init: RequestInit = {}, token?: string): Promise<Response> {
  const headers = new Headers(init.headers);
  if (init.body) headers.set("Content-Type", "application/json");
  if (token) headers.set("Authorization", `Bearer ${token}`);
  return fetch(`${API_URL}${path}`, { ...init, headers, cache: "no-store" });
}

async function errorFrom(res: Response): Promise<ApiError> {
  let msg = `Request failed (HTTP ${res.status})`;
  try {
    const body = await res.json();
    if (body?.error) msg = body.error;
  } catch {}
  return new ApiError(res.status, msg);
}

/** Calls the API as the signed-in user; sends them to /login if the session is gone. */
export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const token = (await cookies()).get(SESSION_COOKIE)?.value;
  if (!token) redirect("/login");
  const res = await request(path, init, token);
  if (res.status === 401) redirect("/login");
  if (!res.ok) throw await errorFrom(res);
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export async function login(email: string, password: string): Promise<{ token: string; expires_at: string }> {
  const res = await request("/api/v1/auth/login", {
    method: "POST",
    body: JSON.stringify({ email, password }),
  });
  if (!res.ok) throw await errorFrom(res);
  return res.json();
}
