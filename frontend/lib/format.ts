export const BUSINESS_TZ = "Asia/Manila";

export function time(iso: string | null | undefined, tz = BUSINESS_TZ) {
  if (!iso) return "—";
  return new Intl.DateTimeFormat("en-PH", { hour: "2-digit", minute: "2-digit", hour12: false, timeZone: tz }).format(new Date(iso));
}

export function dateTime(iso: string | null | undefined, tz = BUSINESS_TZ) {
  if (!iso) return "—";
  return new Intl.DateTimeFormat("en-PH", {
    month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false, timeZone: tz,
  }).format(new Date(iso));
}

export function ago(iso: string | null | undefined) {
  if (!iso) return "never";
  const s = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return `${Math.floor(s / 86400)} d ago`;
}

export function todayISO(tz = BUSINESS_TZ) {
  return new Intl.DateTimeFormat("en-CA", { timeZone: tz }).format(new Date());
}

const reasons: Record<string, string> = {
  not_tab_separated: "Line is not in the expected tab-separated format",
  invalid_user_id: "User ID is missing or contains unexpected characters",
  invalid_timestamp: "Date or time could not be read",
  timestamp_out_of_range: "Date is outside 2000–2099",
  timestamp_in_future: "Punch time is in the future — check the device clock",
  field_too_long: "A field is longer than the device normally sends",
  line_too_long: "Line is longer than the device normally sends",
};
export const reasonText = (r: string | null) => (r ? reasons[r] ?? r : "");

const errorTypes: Record<string, string> = {
  unknown_device: "Unregistered device is trying to connect",
  device_disabled: "Disabled device is still connecting",
  source_ip_not_allowed: "Device connected from an unexpected network",
  rejected_records: "Records held for review",
  unknown_endpoint: "Device used an unsupported request",
};
export const errorTypeText = (t: string) => errorTypes[t] ?? t;
