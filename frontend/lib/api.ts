// Thin client for the application API. Same-origin only; the session lives in
// an HttpOnly cookie and every write carries the CSRF header the API requires.

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public fields?: Record<string, string>,
  ) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown, raw = false): Promise<T> {
  const headers: Record<string, string> = {};
  if (method !== "GET") headers["X-ADMS-CSRF"] = "1";
  let payload: BodyInit | undefined;
  if (typeof body === "string") {
    payload = body;
    headers["Content-Type"] = "text/csv";
  } else if (body !== undefined) {
    payload = JSON.stringify(body);
    headers["Content-Type"] = "application/json";
  }
  const res = await fetch(path, { method, headers, body: payload, credentials: "same-origin", cache: "no-store" });
  if (res.status === 401 && !path.endsWith("/auth/login") && typeof window !== "undefined") {
    window.location.href = "/login?next=" + encodeURIComponent(window.location.pathname);
  }
  if (res.status === 204) return undefined as T;
  if (raw) return (await res.text()) as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok && !(res.status === 422 && "rows" in data)) {
    const e = data.error ?? {};
    throw new ApiError(res.status, e.code ?? "error", e.message ?? `Request failed (${res.status})`, e.fields);
  }
  return data as T;
}

export const api = {
  get: <T>(p: string) => request<T>("GET", p),
  post: <T>(p: string, b?: unknown) => request<T>("POST", p, b ?? {}),
  put: <T>(p: string, b: unknown) => request<T>("PUT", p, b),
  csv: <T>(p: string, text: string) => request<T>("POST", p, text),
};

export type Role = "admin" | "hr" | "viewer";
export type Me = { id: string; name: string; email: string; role: Role };

export type Department = { id: string; name: string; description: string; active: boolean; employee_count: number };

export type Employee = {
  id: string;
  employee_number: string;
  first_name: string;
  last_name: string;
  department_id: string | null;
  department_name: string | null;
  position: string;
  employment_status: "active" | "on_leave" | "inactive" | "separated";
  hire_date: string | null;
  device_user_ids: string[];
};

export type DeviceStatus = "online" | "offline" | "never_connected" | "disabled";

export type Device = {
  id: string;
  device_name: string;
  serial_number: string;
  model: string;
  firmware_version: string | null;
  push_version: string | null;
  location: string;
  timezone: string;
  allowed_cidrs: string[];
  enabled: boolean;
  status: DeviceStatus;
  last_seen_at: string | null;
  last_ip: string | null;
  last_transaction_at: string | null;
  punches_today: number;
  open_errors: number;
};

export type Mapping = {
  id: string;
  device_id: string;
  device_name: string;
  device_user_id: string;
  valid_from: string;
  valid_until: string | null;
};

export type Punch = {
  id: string;
  device_name: string | null;
  device_user_id: string | null;
  device_timestamp: string | null;
  punch_at: string | null;
  received_at: string;
  event_type: string | null;
  verification_method: string | null;
  parse_status: "accepted" | "rejected";
  rejection_reason: string | null;
  original_payload: string;
  employee_id: string | null;
  employee_number: string | null;
  employee_name: string | null;
};

export type SyncError = {
  id: number;
  device_id: string | null;
  device_name: string | null;
  serial_number: string | null;
  error_type: string;
  message: string;
  occurrences: number;
  first_seen_at: string;
  last_seen_at: string;
};

export type Dashboard = {
  date: string;
  timezone: string;
  active_employees: number;
  present_today: number;
  not_yet_punched: number;
  unmatched_punches_today: number;
  rejected_records_today: number;
  punches_by_hour: number[];
  devices: Device[];
  sync_errors: { id: number; device: string; error_type: string; message: string; occurrences: number; last_seen_at: string }[];
  late_arrivals: number | null;
  missing_punches: number | null;
  overtime_minutes: number | null;
  attendance_engine_ready: boolean;
};
