export type Principal = {
  id: string;
  email: string;
  displayName: string;
  userType: "internal" | "supplier" | "api";
  supplierId?: string;
  permissions: string[];
  dataScope: string;
};

export type Version = {
  name: string;
  version: string;
  commit: string;
  buildTime: string;
};

class APIError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export async function api<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      ...(options?.headers || {}),
    },
    credentials: "same-origin",
  });
  if (response.status === 204) return undefined as T;
  const body = await response.json().catch(() => ({}));
  if (!response.ok)
    throw new APIError(
      response.status,
      body?.error?.message || `요청 실패 (${response.status})`,
    );
  return body as T;
}

export const post = <T>(path: string, body: unknown) =>
  api<T>(path, { method: "POST", body: JSON.stringify(body) });
export const patch = <T>(path: string, body: unknown) =>
  api<T>(path, { method: "PATCH", body: JSON.stringify(body) });
export const put = <T>(path: string, body: unknown) =>
  api<T>(path, { method: "PUT", body: JSON.stringify(body) });
export const del = <T>(path: string) => api<T>(path, { method: "DELETE" });

export function can(user: Principal, permission: string) {
  if (
    permission === "*.read" &&
    user.permissions.some(
      (got) => got === "*" || got === "*.read" || got.endsWith(".read"),
    )
  )
    return true;
  return user.permissions.some(
    (got) =>
      got === "*" ||
      got === permission ||
      (got.endsWith(".*") && permission.startsWith(got.slice(0, -1))) ||
      (got.startsWith("*.") && permission.endsWith(got.slice(1))),
  );
}

export function money(value?: number | null) {
  if (value == null || !Number.isFinite(value)) return "—";
  return new Intl.NumberFormat("ko-KR", {
    style: "currency",
    currency: "KRW",
    maximumFractionDigits: 0,
  }).format(value);
}

export function date(value?: string | null) {
  if (!value) return "—";
  // PostgreSQL may serialize UTC offsets as `+00`; browsers require `+00:00`.
  const parsed = new Date(value.replace(/([+-]\d{2})$/, "$1:00"));
  if (Number.isNaN(parsed.getTime())) return "—";
  return new Intl.DateTimeFormat("ko-KR", {
    year: "numeric",
    month: "short",
    day: "numeric",
  }).format(parsed);
}
