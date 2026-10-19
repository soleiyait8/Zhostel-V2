"use client";
import Link from "next/link";
import { use, useCallback, useEffect, useState } from "react";
import { api, ApiError, Department, Device, Employee, Mapping, Punch } from "@/lib/api";
import { dateTime, todayISO } from "@/lib/format";
import { can, useMe } from "@/components/Shell";
import { EmployeeForm } from "@/components/EmployeeForm";
import { Empty, Field, Loading, Notice, PageHeader, Section } from "@/components/ui";

export default function EmployeePage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const me = useMe();
  const editor = can(me, "hr");
  const [emp, setEmp] = useState<Employee | null>(null);
  const [deps, setDeps] = useState<Department[]>([]);
  const [maps, setMaps] = useState<Mapping[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [punches, setPunches] = useState<Punch[] | null>(null);
  const [editing, setEditing] = useState(false);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [newMap, setNewMap] = useState({ device_id: "", device_user_id: "" });
  const [mapErr, setMapErr] = useState<Record<string, string>>({});

  const load = useCallback(() => {
    api.get<Employee>(`/api/employees/${id}`).then(setEmp).catch((e) => setErr(e.message));
    api.get<{ items: Mapping[] }>(`/api/employees/${id}/mappings`).then((r) => setMaps(r.items)).catch(() => {});
    const from = new Date(Date.now() - 30 * 86400_000).toISOString().slice(0, 10);
    api.get<{ items: Punch[] }>(`/api/punches?employee_id=${id}&from=${from}&to=${todayISO()}&limit=200`)
      .then((r) => setPunches(r.items)).catch(() => setPunches([]));
  }, [id]);

  useEffect(() => {
    load();
    api.get<{ items: Department[] }>("/api/departments").then((r) => setDeps(r.items)).catch(() => {});
    api.get<{ items: Device[] }>("/api/devices").then((r) => {
      setDevices(r.items);
      if (r.items[0]) setNewMap((m) => ({ ...m, device_id: m.device_id || r.items[0].id }));
    }).catch(() => {});
  }, [load]);

  async function addMapping() {
    setMapErr({});
    try {
      await api.post(`/api/employees/${id}/mappings`, newMap);
      setNewMap({ ...newMap, device_user_id: "" });
      setMsg("Device user ID mapped. New punches from that ID now count for this employee.");
      load();
    } catch (e) {
      if (e instanceof ApiError) setMapErr(e.fields ?? { device_user_id: e.message });
    }
  }
  async function endMapping(mid: string) {
    if (!confirm("End this mapping? Punches already recorded stay with this employee.")) return;
    await api.post(`/api/mappings/${mid}/end`);
    load();
  }
  async function deactivate() {
    if (!confirm(`Deactivate ${emp?.first_name} ${emp?.last_name}? Their history is kept and their device IDs are released.`)) return;
    const r = await api.post<{ mappings_closed: number }>(`/api/employees/${id}/deactivate`);
    setMsg(`Employee deactivated. ${r.mappings_closed} device mapping${r.mappings_closed === 1 ? "" : "s"} closed.`);
    load();
  }

  if (err) return <Notice>{err}</Notice>;
  if (!emp) return <Loading />;
  const current = emp.employment_status === "active" || emp.employment_status === "on_leave";

  return (
    <div className="space-y-6">
      <p className="text-sm"><Link href="/employees" className="text-accent hover:underline">Employees</Link></p>
      <PageHeader
        title={`${emp.first_name} ${emp.last_name}`}
        sub={<>{emp.employee_number} · {emp.position || "No position"} · {emp.department_name ?? "No department"}{!current && <> · <strong className="text-fault">{emp.employment_status}</strong></>}</>}
        actions={editor && <>
          {!editing && <button className="btn-quiet" onClick={() => setEditing(true)}>Edit details</button>}
          {current && <button className="btn-danger" onClick={deactivate}>Deactivate</button>}
        </>}
      />
      {msg && <Notice tone="attention">{msg}</Notice>}

      {editing && (
        <Section title="Edit details">
          <div className="p-4">
            <EmployeeForm id={id} initial={emp} departments={deps} onCancel={() => setEditing(false)}
              onSaved={() => { setEditing(false); setMsg("Changes saved."); load(); }} />
          </div>
        </Section>
      )}

      <Section title="Device user IDs">
        <p className="p-4 text-sm text-muted">
          Enter the user ID enrolled for this person on the terminal. One ID can belong to only one employee at a time; ending a mapping keeps past punches with this employee.
        </p>
        {maps.length > 0 && (
          <div className="overflow-x-auto">
            <table className="table">
              <thead><tr><th>Device</th><th>User ID</th><th>From</th><th>Until</th><th /></tr></thead>
              <tbody>{maps.map((m) => (
                <tr key={m.id}>
                  <td>{m.device_name}</td><td className="font-semibold">{m.device_user_id}</td>
                  <td>{dateTime(m.valid_from)}</td><td>{m.valid_until ? dateTime(m.valid_until) : "Current"}</td>
                  <td className="text-right">{editor && !m.valid_until && <button className="text-sm text-fault underline" onClick={() => endMapping(m.id)}>End mapping</button>}</td>
                </tr>
              ))}</tbody>
            </table>
          </div>
        )}
        {editor && current && (
          devices.length === 0 ? <p className="border-t border-rule p-4 text-sm">Register a device first.</p> : (
            <div className="flex flex-wrap items-end gap-3 border-t border-rule p-4">
              <Field label="Device" error={mapErr.device_id}>
                <select className="field" value={newMap.device_id} onChange={(e) => setNewMap({ ...newMap, device_id: e.target.value })}>
                  {devices.map((d) => <option key={d.id} value={d.id}>{d.device_name}</option>)}
                </select>
              </Field>
              <Field label="User ID on device" error={mapErr.device_user_id}>
                <input className="field w-40" value={newMap.device_user_id} onChange={(e) => setNewMap({ ...newMap, device_user_id: e.target.value })} />
              </Field>
              <button className="btn-primary" onClick={addMapping} disabled={!newMap.device_user_id}>Map ID</button>
            </div>
          )
        )}
      </Section>

      <Section title="Punches, last 30 days">
        {!punches ? <Loading /> : punches.length === 0 ? <Empty title="No punches recorded">Punches appear once this employee has a mapped device user ID and uses the terminal.</Empty> : (
          <div className="overflow-x-auto">
            <table className="table">
              <thead><tr><th>Time</th><th>Device</th><th>Status code</th><th>Verify code</th></tr></thead>
              <tbody>{punches.map((p) => (
                <tr key={p.id}><td>{dateTime(p.punch_at)}</td><td>{p.device_name}</td><td>{p.event_type ?? "—"}</td><td>{p.verification_method ?? "—"}</td></tr>
              ))}</tbody>
            </table>
          </div>
        )}
      </Section>
    </div>
  );
}
