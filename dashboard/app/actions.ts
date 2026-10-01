"use server";

import { cookies } from "next/headers";
import { revalidatePath } from "next/cache";
import { redirect } from "next/navigation";

import { api, ApiError, login, SESSION_COOKIE } from "@/lib/api";

export type FormState = { error?: string; ok?: string; email?: string };

function message(e: unknown): string {
  return e instanceof ApiError ? e.message : "Something went wrong. Try again.";
}

function str(fd: FormData, key: string): string {
  return String(fd.get(key) ?? "").trim();
}

export async function signIn(_: FormState, fd: FormData): Promise<FormState> {
  try {
    const { token, expires_at } = await login(str(fd, "email"), String(fd.get("password") ?? ""));
    (await cookies()).set(SESSION_COOKIE, token, {
      httpOnly: true,
      sameSite: "lax",
      secure: process.env.NODE_ENV === "production" && process.env.INSECURE_COOKIES !== "1",
      path: "/",
      expires: new Date(expires_at),
    });
  } catch (e) {
    // React resets the form after an action; keep the email so only the password is retyped.
    return { error: message(e), email: str(fd, "email") };
  }
  redirect("/");
}

export async function signOut() {
  try {
    await api("/api/v1/auth/logout", { method: "POST" });
  } catch {}
  (await cookies()).delete(SESSION_COOKIE);
  redirect("/login");
}

export async function updateOrg(_: FormState, fd: FormData): Promise<FormState> {
  const body: Record<string, unknown> = {};
  if (fd.has("name")) body.name = str(fd, "name");
  if (fd.has("report_interval_minutes")) body.report_interval_minutes = Number(fd.get("report_interval_minutes"));
  if (fd.has("allow_ai_present")) body.allow_ai = fd.get("allow_ai") === "on";
  if (fd.has("slack_webhook_url")) body.slack_webhook_url = str(fd, "slack_webhook_url");
  try {
    await api("/api/v1/org", { method: "PATCH", body: JSON.stringify(body) });
  } catch (e) {
    return { error: message(e) };
  }
  revalidatePath("/", "layout");
  return { ok: "Saved." };
}

export async function rotateCode(_: FormState): Promise<FormState> {
  try {
    await api("/api/v1/org/rotate-code", { method: "POST" });
  } catch (e) {
    return { error: message(e) };
  }
  revalidatePath("/settings");
  return { ok: "New code created. The old code no longer works; enrolled laptops are not affected." };
}

export async function testSlack(_: FormState): Promise<FormState> {
  try {
    await api("/api/v1/org/test-slack", { method: "POST" });
  } catch (e) {
    return { error: message(e) };
  }
  return { ok: "Test message sent to Slack." };
}

function ruleBody(fd: FormData) {
  return {
    name: str(fd, "name"),
    metric: str(fd, "metric"),
    op: str(fd, "op"),
    threshold: Number(fd.get("threshold")),
  };
}

export async function createRule(_: FormState, fd: FormData): Promise<FormState> {
  const body = ruleBody(fd);
  if (!Number.isFinite(body.threshold)) return { error: "Threshold must be a number." };
  try {
    await api("/api/v1/alert-rules", { method: "POST", body: JSON.stringify(body) });
  } catch (e) {
    return { error: message(e) };
  }
  revalidatePath("/alerts");
  return { ok: "Rule added." };
}

export async function updateRule(_: FormState, fd: FormData): Promise<FormState> {
  const id = str(fd, "id");
  const body: Record<string, unknown> = {};
  if (fd.has("enabled")) body.enabled = fd.get("enabled") === "true";
  if (fd.has("threshold")) {
    body.threshold = Number(fd.get("threshold"));
    if (!Number.isFinite(body.threshold)) return { error: "Threshold must be a number." };
  }
  try {
    await api(`/api/v1/alert-rules/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify(body) });
  } catch (e) {
    return { error: message(e) };
  }
  revalidatePath("/alerts");
  return { ok: "Saved." };
}

export async function deleteRule(_: FormState, fd: FormData): Promise<FormState> {
  try {
    await api(`/api/v1/alert-rules/${encodeURIComponent(str(fd, "id"))}`, { method: "DELETE" });
  } catch (e) {
    return { error: message(e) };
  }
  revalidatePath("/alerts");
  return {};
}

export async function removeDevice(_: FormState, fd: FormData): Promise<FormState> {
  try {
    await api(`/api/v1/devices/${encodeURIComponent(str(fd, "id"))}`, { method: "DELETE" });
  } catch (e) {
    return { error: message(e) };
  }
  revalidatePath("/");
  redirect("/");
}
