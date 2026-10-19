"use client";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { FormEvent, useEffect, useState } from "react";
import { api, ApiError, Device } from "@/lib/api";
import { ago } from "@/lib/format";
import { can, useMe } from "@/components/Shell";
import { DeviceStatusBadge, Empty, Field, Loading, Notice, PageHeader, Section } from "@/components/ui";

export default function DevicesPage() {
  const me = useMe();
  const router = useRouter();
  const [items, setItems] = useState<Device[] | null>(null);
  const [err, setErr] = useState("");
  const [adding, setAdding] = useState(false);
  const [f, setF] = useState({ device_name: "", serial_number: "", location: "", timezone: "Asia/Manila", allowed: "" });
  const [fe, setFe] = useState<Record<string, string>>({});

  useEffect(() => { api.get<{ items: Device[] }>("/api/devices").then((r) => setItems(r.items)).catch((e) => setErr(e.message)); }, []);

  async function register(e: FormEvent) {
    e.preventDefault();
    setFe({});
    try {
      const r = await api.post<{ id: string }>("/api/devices", {
        device_name: f.device_name, serial_number: f.serial_number, location: f.location, timezone: f.timezone,
        allowed_cidrs: f.allowed.split(/[\s,]+/).filter(Boolean),
      });
      router.push(`/devices/${r.id}`);
    } catch (err) {
      if (err instanceof ApiError) setFe(err.fields ?? { serial_number: err.message });
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Devices"
        sub="A terminal must be registered here before its punches are accepted. Unregistered terminals keep their records and resend them after registration."
        actions={can(me, "admin") && !adding && <button className="btn-primary" onClick={() => setAdding(true)}>Register device</button>}
      />
      {err && <Notice>{err}</Notice>}

      {adding && (
        <Section title="Register a device">
          <form onSubmit={register} className="grid gap-4 p-4 sm:grid-cols-2">
            <Field label="Name" error={fe.device_name} hint="How HR will recognise it, e.g. Main lobby">
              <input className="field" required value={f.device_name} onChange={(e) => setF({ ...f, device_name: e.target.value })} />
            </Field>
            <Field label="Serial number" error={fe.serial_number} hint="From the device: Menu › System Info › Device Info. Must match exactly.">
              <input className="field" required value={f.serial_number} onChange={(e) => setF({ ...f, serial_number: e.target.value.trim() })} />
            </Field>
            <Field label="Location" error={fe.location}>
              <input className="field" value={f.location} onChange={(e) => setF({ ...f, location: e.target.value })} />
            </Field>
            <Field label="Device clock timezone" error={fe.timezone} hint="The timezone the terminal's clock is set to.">
              <input className="field" value={f.timezone} onChange={(e) => setF({ ...f, timezone: e.target.value })} />
            </Field>
            <div className="sm:col-span-2">
              <Field label="Allowed source networks (optional)" error={fe.allowed_cidrs}
                hint="Public IPs or ranges the device connects from, separated by commas. Leave empty if the site has a changing IP address.">
                <input className="field" value={f.allowed} onChange={(e) => setF({ ...f, allowed: e.target.value })} placeholder="203.0.113.10, 198.51.100.0/24" />
              </Field>
            </div>
            <div className="flex gap-2 sm:col-span-2">
              <button className="btn-primary">Register device</button>
              <button type="button" className="btn-quiet" onClick={() => setAdding(false)}>Cancel</button>
            </div>
          </form>
        </Section>
      )}

      <div className="overflow-x-auto rounded-lg border border-rule bg-surface">
        {!items ? <Loading /> : items.length === 0 ? (
          <Empty title="No devices yet">Register the MB10-VL by its serial number, then point it at this server.</Empty>
        ) : (
          <table className="table">
            <thead><tr><th>Device</th><th>Serial</th><th>Status</th><th>Last contact</th><th>Last punch</th><th>Today</th><th>Problems</th></tr></thead>
            <tbody>{items.map((d) => (
              <tr key={d.id} className="hover:bg-paper">
                <td><Link href={`/devices/${d.id}`} className="font-medium text-accent hover:underline">{d.device_name}</Link>
                  <span className="block text-xs text-muted">{d.location}</span></td>
                <td>{d.serial_number}</td>
                <td><DeviceStatusBadge status={d.status} /></td>
                <td>{ago(d.last_seen_at)}</td>
                <td>{ago(d.last_transaction_at)}</td>
                <td>{d.punches_today}</td>
                <td>{d.open_errors > 0 ? <span className="font-semibold text-attention">{d.open_errors} open</span> : <span className="text-muted">None</span>}</td>
              </tr>
            ))}</tbody>
          </table>
        )}
      </div>
    </div>
  );
}
