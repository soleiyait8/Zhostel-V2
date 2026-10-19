"use client";
import Link from "next/link";
import { useEffect, useState } from "react";
import { api, Dashboard } from "@/lib/api";
import { ago, errorTypeText } from "@/lib/format";
import { PunchTimeline } from "@/components/PunchTimeline";
import { DeviceStatusBadge, Empty, Loading, Notice, Section } from "@/components/ui";

function Figure({ value, label, href, tone }: { value: number | null; label: string; href?: string; tone?: "attention" }) {
  const body = (
    <>
      <span className={`block text-3xl font-bold ${tone === "attention" && value ? "text-attention" : ""}`}>
        {value ?? "—"}
      </span>
      <span className="mt-1 block text-sm text-muted">{label}</span>
    </>
  );
  const cls = "block px-4 py-4 sm:px-6";
  return href ? <Link href={href} className={`${cls} hover:bg-paper`}>{body}</Link> : <div className={cls}>{body}</div>;
}

export default function TodayPage() {
  const [d, setD] = useState<Dashboard | null>(null);
  const [err, setErr] = useState("");

  useEffect(() => {
    const load = () => api.get<Dashboard>("/api/dashboard").then((x) => { setD(x); setErr(""); }).catch((e) => setErr(e.message));
    load();
    const t = setInterval(load, 30_000);
    return () => clearInterval(t);
  }, []);

  if (err && !d) return <Notice>{err}</Notice>;
  if (!d) return <Loading />;

  const nowHour = Number(new Intl.DateTimeFormat("en-GB", { hour: "numeric", hourCycle: "h23", timeZone: d.timezone }).format(new Date()));
  const total = d.punches_by_hour.reduce((a, b) => a + b, 0);
  const longDate = new Intl.DateTimeFormat("en-PH", { weekday: "long", month: "long", day: "numeric", timeZone: d.timezone }).format(new Date());

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-2xl font-bold tracking-tight">{longDate}</h1>
        <p className="mt-1 text-sm text-muted">Times shown in {d.timezone}. Refreshes every 30 seconds.</p>
      </header>

      {err && <Notice tone="attention">Latest refresh failed: {err}. Showing the last figures received.</Notice>}

      <PunchTimeline hours={d.punches_by_hour} nowHour={nowHour} total={total} />

      <div className="grid grid-cols-2 divide-rule rounded-lg border border-rule bg-surface sm:grid-cols-4 sm:divide-x">
        <Figure value={d.active_employees} label="Active employees" href="/employees" />
        <Figure value={d.present_today} label="Have punched in" href={`/punches?status=accepted`} />
        <Figure value={d.not_yet_punched} label="No punch yet today" />
        <Figure value={d.unmatched_punches_today} label="Punches from unmapped IDs" href="/punches?status=unmatched" tone="attention" />
      </div>

      {!d.attendance_engine_ready && (
        <p className="text-sm text-muted">
          Late arrivals, missing punches and overtime appear here once shifts are assigned and attendance calculation is enabled.
        </p>
      )}

      <div className="grid gap-6 lg:grid-cols-2">
        <Section title="Devices" aside={<Link href="/devices" className="text-sm font-medium text-accent hover:underline">Manage</Link>}>
          {d.devices.length === 0 ? (
            <Empty title="No devices registered">
              <Link href="/devices" className="text-accent underline">Register your MB10-VL</Link> using the serial number on its system info screen.
            </Empty>
          ) : (
            <ul className="divide-y divide-rule">
              {d.devices.map((dev) => (
                <li key={dev.id}>
                  <Link href={`/devices/${dev.id}`} className="flex items-center justify-between gap-4 px-4 py-3 hover:bg-paper">
                    <span>
                      <span className="block font-semibold">{dev.device_name}</span>
                      <span className="block text-xs text-muted">
                        {dev.location || dev.serial_number} · last contact {ago(dev.last_seen_at)}
                      </span>
                    </span>
                    <span className="text-right">
                      <DeviceStatusBadge status={dev.status} />
                      <span className="block text-xs text-muted">{dev.punches_today} today</span>
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </Section>

        <Section title="Needs attention" aside={d.rejected_records_today > 0 ? <span className="text-sm text-attention">{d.rejected_records_today} held today</span> : undefined}>
          {d.sync_errors.length === 0 ? (
            <Empty title="Nothing to fix">Device connections and uploads are clean.</Empty>
          ) : (
            <ul className="divide-y divide-rule">
              {d.sync_errors.map((e) => (
                <li key={e.id} className="px-4 py-3">
                  <p className="font-semibold">{errorTypeText(e.error_type)}</p>
                  <p className="text-sm text-muted">
                    {e.device} · {e.occurrences > 1 ? `${e.occurrences} times, ` : ""}last {ago(e.last_seen_at)}
                  </p>
                </li>
              ))}
            </ul>
          )}
        </Section>
      </div>
    </div>
  );
}
