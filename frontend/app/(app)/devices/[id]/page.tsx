"use client";
import Link from "next/link";
import { use, useCallback, useEffect, useState } from "react";
import { api, ApiError, Device, Employee, SyncError } from "@/lib/api";
import { ago, dateTime, errorTypeText } from "@/lib/format";
import { can, useMe } from "@/components/Shell";
import { DeviceStatusBadge, Empty, Field, Loading, Notice, PageHeader, Section } from "@/components/ui";

type Status = { device: Device; outcomes_24h: Record<string, number>; last_transaction: { device_user_id: string; punch_at: string; received_at: string } | null };
type DeviceUser = { device_user_id: string; name_on_device: string | null; last_punch_at: string | null; employee_id: string | null; employee_number: string | null; employee_name: string | null };
type Comm = { id: number; method: string; path: string; query: string; body_size: number; body: string; response_status: number; response_body: string; remote_ip: string | null; outcome: string; error: string; created_at: string };
type Reach = { adms_service: { reachable: boolean; error?: string; detail?: { profile_verified?: boolean; profile?: string } }; device_last_seen_at: string | null; device_last_ip: string | null; setup: Setup };
type Setup = { server_address: string; server_port: number; https: boolean; endpoint: string };

const outcomeText: Record<string, string> = {
  handshake: "Handshakes", heartbeat: "Heartbeats", attlog_stored: "Punch uploads", operlog_stored: "User list uploads",
  table_ignored: "Other uploads (not stored)", db_error: "Server errors (device will retry)", device_disabled: "Refused: disabled",
  source_ip_not_allowed: "Refused: network not allowed", unknown_endpoint: "Unsupported requests", command_sent: "Commands sent",
};

export default function DevicePage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const me = useMe();
  const [s, setS] = useState<Status | null>(null);
  const [users, setUsers] = useState<DeviceUser[]>([]);
  const [errors, setErrors] = useState<SyncError[]>([]);
  const [comms, setComms] = useState<Comm[] | null>(null);
  const [setup, setSetup] = useState<Setup | null>(null);
  const [reach, setReach] = useState<Reach | null>(null);
  const [employees, setEmployees] = useState<Employee[]>([]);
  const [mapping, setMapping] = useState<{ uid: string; emp: string } | null>(null);
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [edit, setEdit] = useState<null | { device_name: string; location: string; timezone: string; allowed: string; enabled: boolean }>(null);

  const load = useCallback(() => {
    api.get<Status>(`/api/devices/${id}/status`).then(setS).catch((e) => setErr(e.message));
    api.get<{ items: DeviceUser[] }>(`/api/devices/${id}/users`).then((r) => setUsers(r.items)).catch(() => {});
    api.get<{ items: SyncError[] }>(`/api/sync-errors?device_id=${id}`).then((r) => setErrors(r.items)).catch(() => {});
    if (can(me, "hr")) api.get<{ items: Comm[] }>(`/api/devices/${id}/communications?limit=30`).then((r) => setComms(r.items)).catch(() => setComms([]));
  }, [id, me]);

  useEffect(() => {
    load();
    api.get<Setup>("/api/devices/setup-info").then(setSetup).catch(() => {});
    api.get<{ items: Employee[] }>("/api/employees?status=active&limit=500").then((r) => setEmployees(r.items)).catch(() => {});
  }, [load]);

  async function checkReach() {
    setReach(null);
    setReach(await api.get<Reach>(`/api/devices/${id}/reachability`));
  }
  async function reprocess(acceptFuture: boolean) {
    if (acceptFuture && !confirm("Accept punches dated in the future? Only do this after confirming the device clock is now correct.")) return;
    const r = await api.post<{ examined: number; accepted: number; still_rejected: number }>(`/api/devices/${id}/reprocess`, { accept_future: acceptFuture });
    setMsg(`Reviewed ${r.examined} held record${r.examined === 1 ? "" : "s"}: ${r.accepted} accepted, ${r.still_rejected} still held.`);
    load();
  }
  async function resolve(eid: number) {
    await api.post(`/api/sync-errors/${eid}/resolve`);
    load();
  }
  async function saveMapping() {
    if (!mapping?.emp) return;
    try {
      await api.post(`/api/employees/${mapping.emp}/mappings`, { device_id: id, device_user_id: mapping.uid });
      setMapping(null);
      setMsg(`User ID ${mapping.uid} mapped.`);
      load();
    } catch (e) {
      setMsg(e instanceof ApiError ? e.message : "Mapping failed.");
    }
  }
  async function saveSettings() {
    if (!edit || !s) return;
    try {
      await api.put(`/api/devices/${id}`, { device_name: edit.device_name, location: edit.location, timezone: edit.timezone,
        allowed_cidrs: edit.allowed.split(/[\s,]+/).filter(Boolean), enabled: edit.enabled });
      setEdit(null);
      setMsg("Device settings saved.");
      load();
    } catch (e) {
      setMsg(e instanceof ApiError ? (e.fields ? Object.values(e.fields).join(" ") : e.message) : "Save failed.");
    }
  }

  if (err) return <Notice>{err}</Notice>;
  if (!s) return <Loading />;
  const d = s.device;
  const unmapped = users.filter((u) => !u.employee_id);

  return (
    <div className="space-y-6">
      <p className="text-sm"><Link href="/devices" className="text-accent hover:underline">Devices</Link></p>
      <PageHeader
        title={d.device_name}
        sub={<>{d.model} · serial {d.serial_number}{d.location && <> · {d.location}</>}</>}
        actions={<>
          <button className="btn-quiet" onClick={checkReach}>Check server path</button>
          {can(me, "admin") && !edit && <button className="btn-quiet" onClick={() => setEdit({ device_name: d.device_name, location: d.location, timezone: d.timezone, allowed: d.allowed_cidrs.join(", "), enabled: d.enabled })}>Edit settings</button>}
        </>}
      />
      {msg && <Notice tone="attention">{msg}</Notice>}

      <div className="grid gap-px overflow-hidden rounded-lg border border-rule bg-rule sm:grid-cols-4">
        {[
          ["Status", <DeviceStatusBadge key="s" status={d.status} />],
          ["Last contact", <span key="c">{ago(d.last_seen_at)}{d.last_ip && <span className="block text-xs text-muted">from {d.last_ip}</span>}</span>],
          ["Last punch received", <span key="p">{s.last_transaction ? <>{dateTime(s.last_transaction.received_at)}<span className="block text-xs text-muted">user ID {s.last_transaction.device_user_id}</span></> : "None yet"}</span>],
          ["Firmware", <span key="f">{d.firmware_version ?? "Not reported"}{d.push_version && <span className="block text-xs text-muted">push {d.push_version}</span>}</span>],
        ].map(([label, value]) => (
          <div key={label as string} className="bg-surface px-4 py-3">
            <span className="block text-xs text-muted">{label}</span>
            <span className="mt-1 block font-semibold">{value}</span>
          </div>
        ))}
      </div>

      {reach && (
        <Notice tone={reach.adms_service.reachable ? "attention" : "fault"}>
          {reach.adms_service.reachable
            ? <>The device service is running{reach.adms_service.detail?.profile_verified === false && <> with an <strong>unverified</strong> protocol profile</>}. Last contact from this device: {ago(reach.device_last_seen_at)}. To test the device side, use the terminal’s own network test or watch for a handshake in the traffic log below after saving its server settings.</>
            : <>The device service did not respond ({reach.adms_service.error ?? "no reply"}). Punches are not being received. Check that the adms container is running.</>}
        </Notice>
      )}

      {edit && (
        <Section title="Device settings">
          <div className="grid gap-4 p-4 sm:grid-cols-2">
            <Field label="Name"><input className="field" value={edit.device_name} onChange={(e) => setEdit({ ...edit, device_name: e.target.value })} /></Field>
            <Field label="Location"><input className="field" value={edit.location} onChange={(e) => setEdit({ ...edit, location: e.target.value })} /></Field>
            <Field label="Device clock timezone"><input className="field" value={edit.timezone} onChange={(e) => setEdit({ ...edit, timezone: e.target.value })} /></Field>
            <Field label="Allowed source networks" hint="Empty allows any network."><input className="field" value={edit.allowed} onChange={(e) => setEdit({ ...edit, allowed: e.target.value })} /></Field>
            <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={edit.enabled} onChange={(e) => setEdit({ ...edit, enabled: e.target.checked })} /> Accept data from this device</label>
            <div className="flex gap-2 sm:col-span-2"><button className="btn-primary" onClick={saveSettings}>Save settings</button><button className="btn-quiet" onClick={() => setEdit(null)}>Cancel</button></div>
          </div>
        </Section>
      )}

      <div className="grid gap-6 lg:grid-cols-2">
        <Section title="Open problems" aside={can(me, "admin") && <span className="flex gap-3 text-sm">
          <button className="text-accent underline" onClick={() => reprocess(false)}>Re-check held records</button>
        </span>}>
          {errors.length === 0 ? <Empty title="No open problems" /> : (
            <ul className="divide-y divide-rule">{errors.map((e) => (
              <li key={e.id} className="flex items-start justify-between gap-3 px-4 py-3">
                <span>
                  <span className="block font-semibold">{errorTypeText(e.error_type)}</span>
                  <span className="block text-sm text-muted">{e.message} {e.occurrences > 1 && `(${e.occurrences}×, last ${ago(e.last_seen_at)})`}</span>
                  {e.error_type === "rejected_records" && <Link className="text-sm text-accent underline" href={`/punches?status=rejected&device_id=${id}&from=${e.first_seen_at.slice(0, 10)}`}>See held records</Link>}
                </span>
                {can(me, "hr") && <button className="shrink-0 text-sm text-muted underline" onClick={() => resolve(e.id)}>Mark resolved</button>}
              </li>
            ))}</ul>
          )}
          {can(me, "admin") && errors.some((e) => e.error_type === "rejected_records") && (
            <p className="border-t border-rule px-4 py-3 text-xs text-muted">
              Punches dated in the future stay held even after a re-check.{" "}
              <button className="underline" onClick={() => reprocess(true)}>Accept them after fixing the device clock</button>.
            </p>
          )}
        </Section>

        <Section title="Traffic, last 24 hours">
          {Object.keys(s.outcomes_24h).length === 0 ? <Empty title="No traffic recorded">Heartbeats are not logged by default; uploads and refusals are.</Empty> : (
            <table className="table"><tbody>{Object.entries(s.outcomes_24h).map(([k, n]) => (
              <tr key={k}><td>{outcomeText[k] ?? k}</td><td className="text-right font-semibold">{n}</td></tr>
            ))}</tbody></table>
          )}
        </Section>
      </div>

      <Section title="User IDs on this device" aside={unmapped.length > 0 && <span className="text-sm font-semibold text-attention">{unmapped.length} not mapped</span>}>
        {users.length === 0 ? <Empty title="No user IDs seen yet">IDs appear after the device uploads punches or its user list.</Empty> : (
          <div className="overflow-x-auto">
            <table className="table">
              <thead><tr><th>User ID</th><th>Name on device</th><th>Last punch</th><th>Employee</th></tr></thead>
              <tbody>{users.map((u) => (
                <tr key={u.device_user_id}>
                  <td className="font-semibold">{u.device_user_id}</td>
                  <td>{u.name_on_device ?? <span className="text-muted">—</span>}</td>
                  <td>{dateTime(u.last_punch_at)}</td>
                  <td>
                    {u.employee_id ? <Link href={`/employees/${u.employee_id}`} className="text-accent hover:underline">{u.employee_number} {u.employee_name}</Link>
                      : mapping?.uid === u.device_user_id ? (
                        <span className="flex flex-wrap gap-2">
                          <select className="field max-w-xs py-1" value={mapping.emp} onChange={(e) => setMapping({ ...mapping, emp: e.target.value })} aria-label="Employee">
                            <option value="">Choose employee…</option>
                            {employees.map((e) => <option key={e.id} value={e.id}>{e.employee_number} {e.last_name}, {e.first_name}</option>)}
                          </select>
                          <button className="btn-primary py-1" onClick={saveMapping} disabled={!mapping.emp}>Map</button>
                          <button className="btn-quiet py-1" onClick={() => setMapping(null)}>Cancel</button>
                        </span>
                      ) : can(me, "hr") ? <button className="text-sm font-medium text-attention underline" onClick={() => setMapping({ uid: u.device_user_id, emp: "" })}>Map to employee</button>
                        : <span className="text-attention">Not mapped</span>}
                  </td>
                </tr>
              ))}</tbody>
            </table>
          </div>
        )}
      </Section>

      <Section title="Connecting the terminal">
        <div className="space-y-3 p-4 text-sm">
          <p>On the MB10-VL, open <strong>Menu › COMM. › Cloud Server Setting</strong> (the wording varies by firmware) and enter:</p>
          <dl className="grid max-w-md grid-cols-[10rem_1fr] gap-y-1">
            <dt className="text-muted">Server mode</dt><dd className="font-semibold">ADMS</dd>
            <dt className="text-muted">Server address</dt><dd className="font-semibold">{setup?.server_address ?? "…"}</dd>
            <dt className="text-muted">Server port</dt><dd className="font-semibold">{setup?.server_port ?? "…"}</dd>
            <dt className="text-muted">HTTPS</dt><dd className="font-semibold">{setup?.https ? "On" : "Off"}</dd>
            <dt className="text-muted">Proxy</dt><dd className="font-semibold">Off</dd>
          </dl>
          <p className="text-muted">Then restart the terminal. Within a minute this page should show a handshake in the traffic log. If the firmware has no HTTPS option, read the HTTPS section of the device guide before connecting over the internet.</p>
        </div>
      </Section>

      {comms && (
        <Section title="Recent requests from the device">
          {comms.length === 0 ? <Empty title="Nothing logged yet" /> : (
            <div className="overflow-x-auto">
              <table className="table">
                <thead><tr><th>Time</th><th>Request</th><th>Result</th><th>Reply</th></tr></thead>
                <tbody>{comms.map((c) => (
                  <tr key={c.id}>
                    <td className="whitespace-nowrap">{dateTime(c.created_at)}</td>
                    <td><span className="font-medium">{c.method} {c.path}</span><span className="block max-w-xs truncate text-xs text-muted" title={c.query}>{c.query}</span>
                      {c.body_size > 0 && <details className="text-xs"><summary className="cursor-pointer text-muted">{c.body_size} bytes</summary><pre className="mt-1 max-h-40 max-w-md overflow-auto whitespace-pre-wrap rounded bg-paper p-2">{c.body}</pre></details>}</td>
                    <td><span className={c.response_status >= 400 ? "text-fault" : ""}>{c.response_status} {outcomeText[c.outcome] ?? c.outcome}</span>{c.error && <span className="block text-xs text-fault">{c.error}</span>}</td>
                    <td><code className="text-xs">{c.response_body.slice(0, 60)}</code></td>
                  </tr>
                ))}</tbody>
              </table>
            </div>
          )}
        </Section>
      )}
    </div>
  );
}
