"use client";
import { ReactNode } from "react";
import type { DeviceStatus } from "@/lib/api";

export function PageHeader({ title, sub, actions }: { title: string; sub?: ReactNode; actions?: ReactNode }) {
  return (
    <header className="mb-6 flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">{title}</h1>
        {sub && <p className="mt-1 max-w-prose text-sm text-muted">{sub}</p>}
      </div>
      {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
    </header>
  );
}

export function Field({ label, error, hint, children }: { label: string; error?: string; hint?: string; children: ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1 block text-sm font-semibold">{label}</span>
      {children}
      {hint && !error && <span className="mt-1 block text-xs text-muted">{hint}</span>}
      {error && <span className="mt-1 block text-xs font-medium text-fault">{error}</span>}
    </label>
  );
}

export function Notice({ tone = "fault", children }: { tone?: "fault" | "attention"; children: ReactNode }) {
  const cls = tone === "fault" ? "border-fault/30 bg-fault-wash text-fault" : "border-attention/30 bg-attention-wash text-ink";
  return <div role={tone === "fault" ? "alert" : "status"} className={`rounded-md border px-3 py-2 text-sm ${cls}`}>{children}</div>;
}

const statusLabel: Record<DeviceStatus, string> = {
  online: "Online",
  offline: "Offline",
  never_connected: "Not connected yet",
  disabled: "Disabled",
};
const statusColor: Record<DeviceStatus, string> = {
  online: "bg-ok",
  offline: "bg-fault",
  never_connected: "bg-attention",
  disabled: "bg-muted",
};

export function DeviceStatusBadge({ status }: { status: DeviceStatus }) {
  return (
    <span className="inline-flex items-center gap-2 text-sm font-medium">
      <span className={`h-2 w-2 rounded-full ${statusColor[status]}`} aria-hidden />
      {statusLabel[status]}
    </span>
  );
}

export function Empty({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="px-4 py-10 text-center">
      <p className="font-semibold">{title}</p>
      {children && <div className="mx-auto mt-1 max-w-md text-sm text-muted">{children}</div>}
    </div>
  );
}

export function Section({ title, aside, children }: { title: string; aside?: ReactNode; children: ReactNode }) {
  return (
    <section className="rounded-lg border border-rule bg-surface">
      <div className="flex items-center justify-between gap-3 border-b border-rule px-4 py-3">
        <h2 className="text-base font-semibold">{title}</h2>
        {aside}
      </div>
      {children}
    </section>
  );
}

export function Loading() {
  return <p className="px-4 py-8 text-sm text-muted">Loading…</p>;
}
