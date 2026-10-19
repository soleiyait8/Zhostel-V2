"use client";
import { FormEvent, useEffect, useState } from "react";
import { api, ApiError, Role } from "@/lib/api";
import { ago } from "@/lib/format";
import { useMe } from "@/components/Shell";
import { Field, Loading, Notice, PageHeader, Section } from "@/components/ui";

type U = { id: string; name: string; email: string; role: Role; active: boolean; last_login_at: string | null };
const roleText: Record<Role, string> = { admin: "Administrator", hr: "HR", viewer: "Viewer" };

export default function UsersPage() {
  const me = useMe();
  const [items, setItems] = useState<U[] | null>(null);
  const [f, setF] = useState({ name: "", email: "", role: "hr" as Role, password: "" });
  const [fe, setFe] = useState<Record<string, string>>({});
  const [msg, setMsg] = useState("");
  const load = () => api.get<{ items: U[] }>("/api/users").then((r) => setItems(r.items)).catch((e) => setMsg(e.message));
  useEffect(() => { load(); }, []);

  async function create(e: FormEvent) {
    e.preventDefault();
    setFe({});
    try {
      await api.post("/api/users", f);
      setMsg(`${f.name} can now sign in. Share the password with them through a separate channel.`);
      setF({ name: "", email: "", role: "hr", password: "" });
      load();
    } catch (err) {
      if (err instanceof ApiError) setFe(err.fields ?? { email: err.message });
    }
  }
  async function update(u: U, patch: Partial<U>) {
    try { await api.put(`/api/users/${u.id}`, patch); load(); }
    catch (err) { setMsg((err as Error).message); }
  }

  return (
    <div className="space-y-6">
      <PageHeader title="Users" sub="Administrators manage devices and users. HR edits employees and mappings. Viewers can only read." />
      {msg && <Notice tone="attention">{msg}</Notice>}
      <div className="overflow-x-auto rounded-lg border border-rule bg-surface">
        {!items ? <Loading /> : (
          <table className="table">
            <thead><tr><th>Name</th><th>Role</th><th>Last sign-in</th><th>Access</th></tr></thead>
            <tbody>{items.map((u) => (
              <tr key={u.id} className={u.active ? "" : "text-muted"}>
                <td>{u.name}<span className="block text-xs text-muted">{u.email}</span></td>
                <td>
                  <select className="field py-1" value={u.role} disabled={u.id === me?.id} onChange={(e) => update(u, { role: e.target.value as Role })} aria-label={`Role for ${u.name}`}>
                    {Object.entries(roleText).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                  </select>
                </td>
                <td>{ago(u.last_login_at)}</td>
                <td>{u.id === me?.id ? <span className="text-sm text-muted">You</span> :
                  <button className={u.active ? "btn-danger py-1" : "btn-quiet py-1"} onClick={() => update(u, { active: !u.active })}>{u.active ? "Revoke access" : "Restore access"}</button>}</td>
              </tr>
            ))}</tbody>
          </table>
        )}
      </div>
      <Section title="Add a user">
        <form onSubmit={create} className="grid gap-4 p-4 sm:grid-cols-2">
          <Field label="Name" error={fe.name}><input className="field" required value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} /></Field>
          <Field label="Email" error={fe.email}><input className="field" type="email" required value={f.email} onChange={(e) => setF({ ...f, email: e.target.value })} /></Field>
          <Field label="Role" error={fe.role}>
            <select className="field" value={f.role} onChange={(e) => setF({ ...f, role: e.target.value as Role })}>
              {Object.entries(roleText).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
          </Field>
          <Field label="Temporary password" error={fe.password} hint="At least 12 characters.">
            <input className="field" type="password" autoComplete="new-password" required value={f.password} onChange={(e) => setF({ ...f, password: e.target.value })} />
          </Field>
          <div><button className="btn-primary">Add user</button></div>
        </form>
      </Section>
    </div>
  );
}
