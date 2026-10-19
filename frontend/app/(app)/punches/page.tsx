"use client";
import Link from "next/link";
import { Suspense, useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { api, Department, Device, Punch } from "@/lib/api";
import { dateTime, reasonText, todayISO } from "@/lib/format";
import { Empty, Loading, Notice, PageHeader } from "@/components/ui";

function Punches() {
  const sp = useSearchParams();
  const router = useRouter();
  const [items, setItems] = useState<Punch[] | null>(null);
  const [total, setTotal] = useState(0);
  const [devices, setDevices] = useState<Device[]>([]);
  const [deps, setDeps] = useState<Department[]>([]);
  const [err, setErr] = useState("");
  const f = {
    from: sp.get("from") ?? todayISO(),
    to: sp.get("to") ?? sp.get("from") ?? todayISO(),
    status: sp.get("status") ?? "",
    device_id: sp.get("device_id") ?? "",
    department_id: sp.get("department_id") ?? "",
  };
  const set = (k: keyof typeof f, v: string) => {
    const p = new URLSearchParams({ ...f, [k]: v });
    [...p.keys()].forEach((key) => !p.get(key) && p.delete(key));
    router.replace(`/punches?${p}`);
  };

  useEffect(() => {
    api.get<{ items: Device[] }>("/api/devices").then((r) => setDevices(r.items)).catch(() => {});
    api.get<{ items: Department[] }>("/api/departments").then((r) => setDeps(r.items)).catch(() => {});
  }, []);
  const key = sp.toString();
  useEffect(() => {
    setItems(null);
    const p = new URLSearchParams(key);
    if (!p.get("from")) p.set("from", f.from);
    if (!p.get("to")) p.set("to", f.to);
    p.set("limit", "500");
    api.get<{ items: Punch[]; total: number }>(`/api/punches?${p}`)
      .then((r) => { setItems(r.items); setTotal(r.total); setErr(""); })
      .catch((e) => { setErr(e.message); setItems([]); });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  return (
    <div className="space-y-6">
      <PageHeader title="Punch history" sub="Every record received from the devices, exactly as sent. Held records are kept for review and never discarded." />
      <div className="flex flex-wrap items-end gap-3">
        <label className="text-sm"><span className="mb-1 block font-semibold">From</span><input type="date" className="field" value={f.from} onChange={(e) => set("from", e.target.value)} /></label>
        <label className="text-sm"><span className="mb-1 block font-semibold">To</span><input type="date" className="field" value={f.to} onChange={(e) => set("to", e.target.value)} /></label>
        <label className="text-sm"><span className="mb-1 block font-semibold">Records</span>
          <select className="field" value={f.status} onChange={(e) => set("status", e.target.value)}>
            <option value="">All</option><option value="accepted">Accepted</option><option value="unmatched">Unmapped user IDs</option><option value="rejected">Held for review</option>
          </select></label>
        <label className="text-sm"><span className="mb-1 block font-semibold">Device</span>
          <select className="field" value={f.device_id} onChange={(e) => set("device_id", e.target.value)}>
            <option value="">All devices</option>{devices.map((d) => <option key={d.id} value={d.id}>{d.device_name}</option>)}
          </select></label>
        <label className="text-sm"><span className="mb-1 block font-semibold">Department</span>
          <select className="field" value={f.department_id} onChange={(e) => set("department_id", e.target.value)}>
            <option value="">All departments</option>{deps.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
          </select></label>
      </div>
      {err && <Notice>{err}</Notice>}
      <div className="overflow-x-auto rounded-lg border border-rule bg-surface">
        {!items ? <Loading /> : items.length === 0 ? <Empty title="No records for these filters" /> : (
          <table className="table">
            <thead><tr><th>Punch time</th><th>Employee</th><th>User ID</th><th>Device</th><th>Codes</th><th>Received</th></tr></thead>
            <tbody>{items.map((p) => (
              <tr key={p.id} className={p.parse_status === "rejected" ? "bg-fault-wash/40" : ""}>
                <td className="whitespace-nowrap">{p.punch_at ? dateTime(p.punch_at) : <span className="text-muted">—</span>}
                  {p.device_timestamp && <span className="block text-xs text-muted">device: {p.device_timestamp}</span>}</td>
                <td>{p.employee_id ? <Link href={`/employees/${p.employee_id}`} className="text-accent hover:underline">{p.employee_number} {p.employee_name}</Link>
                  : p.parse_status === "rejected" ? <span className="text-fault">{reasonText(p.rejection_reason)}<code className="mt-1 block whitespace-pre-wrap text-xs text-muted">{p.original_payload.replace(/\t/g, " ⇥ ")}</code></span>
                  : <span className="text-attention">Unmapped ID</span>}</td>
                <td>{p.device_user_id ?? "—"}</td>
                <td>{p.device_name}</td>
                <td className="text-xs">status {p.event_type ?? "—"} · verify {p.verification_method ?? "—"}</td>
                <td className="whitespace-nowrap text-xs text-muted">{dateTime(p.received_at)}</td>
              </tr>
            ))}</tbody>
          </table>
        )}
      </div>
      {items && total > items.length && <p className="text-sm text-muted">Showing the latest {items.length} of {total}. Narrow the dates to see the rest.</p>}
      <p className="text-xs text-muted">Status and verify codes are shown as sent by the device. Their meaning (in/out, fingerprint/face) depends on the terminal’s configuration and is confirmed during device acceptance testing.</p>
    </div>
  );
}

export default function PunchesPage() {
  return <Suspense fallback={<Loading />}><Punches /></Suspense>;
}
