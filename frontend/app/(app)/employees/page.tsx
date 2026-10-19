"use client";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { api, Department, Employee } from "@/lib/api";
import { can, useMe } from "@/components/Shell";
import { EmployeeForm } from "@/components/EmployeeForm";
import { Empty, Loading, Notice, PageHeader, Section } from "@/components/ui";

type ImportRow = { line: number; employee_number: string; action: string; error?: string };
const statusText: Record<string, string> = { active: "Active", on_leave: "On leave", inactive: "Inactive", separated: "Separated" };

export default function EmployeesPage() {
  const me = useMe();
  const router = useRouter();
  const [items, setItems] = useState<Employee[] | null>(null);
  const [total, setTotal] = useState(0);
  const [deps, setDeps] = useState<Department[]>([]);
  const [q, setQ] = useState("");
  const [dep, setDep] = useState("");
  const [status, setStatus] = useState("active");
  const [adding, setAdding] = useState(false);
  const [err, setErr] = useState("");
  const [csv, setCsv] = useState<{ text: string; rows: ImportRow[]; ok: boolean; saved: boolean } | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const load = useCallback(() => {
    const p = new URLSearchParams({ limit: "200" });
    if (q) p.set("q", q);
    if (dep) p.set("department_id", dep);
    if (status) p.set("status", status);
    api.get<{ items: Employee[]; total: number }>(`/api/employees?${p}`)
      .then((r) => { setItems(r.items); setTotal(r.total); })
      .catch((e) => setErr(e.message));
  }, [q, dep, status]);

  useEffect(() => { const t = setTimeout(load, 200); return () => clearTimeout(t); }, [load]);
  useEffect(() => { api.get<{ items: Department[] }>("/api/departments").then((r) => setDeps(r.items)).catch(() => {}); }, []);

  async function pickFile(f: File | undefined) {
    if (!f) return;
    setErr("");
    try {
      const text = await f.text();
      const r = await api.csv<{ saved: boolean; rows: ImportRow[] }>("/api/employees/import?dry_run=true", text);
      setCsv({ text, rows: r.rows, ok: r.rows.every((x) => x.action !== "error"), saved: false });
    } catch (e) {
      setErr((e as Error).message);
    }
    if (fileRef.current) fileRef.current.value = "";
  }
  async function confirmImport() {
    if (!csv) return;
    const r = await api.csv<{ saved: boolean; rows: ImportRow[] }>("/api/employees/import", csv.text);
    setCsv({ ...csv, rows: r.rows, saved: r.saved });
    load();
  }

  const editor = can(me, "hr");
  return (
    <div className="space-y-6">
      <PageHeader
        title="Employees"
        sub="Deactivating an employee keeps all of their attendance history and frees their device user ID for reuse."
        actions={<>
          <a className="btn-quiet" href="/api/employees/export.csv">Export CSV</a>
          {editor && <>
            <button className="btn-quiet" onClick={() => fileRef.current?.click()}>Import CSV</button>
            <input ref={fileRef} type="file" accept=".csv,text/csv" className="hidden" onChange={(e) => pickFile(e.target.files?.[0])} />
            <button className="btn-primary" onClick={() => setAdding(true)}>Add employee</button>
          </>}
        </>}
      />
      {err && <Notice>{err}</Notice>}

      {csv && (
        <Section title={csv.saved ? "Import saved" : "Check this import before saving"} aside={<button className="text-sm text-muted underline" onClick={() => setCsv(null)}>Close</button>}>
          <div className="space-y-3 p-4">
            {!csv.ok && <Notice>Fix the rows marked below and choose the file again. Nothing has been saved.</Notice>}
            {csv.ok && !csv.saved && <p className="text-sm">{csv.rows.length} rows are ready. Existing employee numbers will be updated.</p>}
            <div className="max-h-64 overflow-auto">
              <table className="table">
                <thead><tr><th>Line</th><th>Employee number</th><th>Result</th></tr></thead>
                <tbody>{csv.rows.map((r) => (
                  <tr key={r.line}><td>{r.line}</td><td>{r.employee_number}</td>
                    <td className={r.action === "error" ? "text-fault" : ""}>{r.action === "error" ? r.error : r.action === "created" ? "New" : "Update"}</td></tr>
                ))}</tbody>
              </table>
            </div>
            {csv.ok && !csv.saved && <button className="btn-primary" onClick={confirmImport}>Save {csv.rows.length} employees</button>}
            <p className="text-xs text-muted">Columns: employee_number, first_name, last_name, department, position, employment_status, hire_date (YYYY-MM-DD). Departments must already exist.</p>
          </div>
        </Section>
      )}

      {adding && (
        <Section title="New employee">
          <div className="p-4">
            <EmployeeForm departments={deps} onCancel={() => setAdding(false)} onSaved={(id) => router.push(`/employees/${id}`)} />
          </div>
        </Section>
      )}

      <div className="flex flex-wrap gap-3">
        <input className="field max-w-xs" placeholder="Search by name or number" value={q} onChange={(e) => setQ(e.target.value)} aria-label="Search employees" />
        <select className="field max-w-[12rem]" value={dep} onChange={(e) => setDep(e.target.value)} aria-label="Department">
          <option value="">All departments</option>
          {deps.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
        </select>
        <select className="field max-w-[10rem]" value={status} onChange={(e) => setStatus(e.target.value)} aria-label="Status">
          <option value="">Any status</option>
          {Object.entries(statusText).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </select>
      </div>

      <div className="overflow-x-auto rounded-lg border border-rule bg-surface">
        {!items ? <Loading /> : items.length === 0 ? (
          <Empty title="No employees match">{editor ? "Add one, or import a CSV exported from your HR system." : "Try a different search."}</Empty>
        ) : (
          <table className="table">
            <thead><tr><th>Number</th><th>Name</th><th>Department</th><th>Position</th><th>Device IDs</th><th>Status</th></tr></thead>
            <tbody>
              {items.map((e) => (
                <tr key={e.id} className="hover:bg-paper">
                  <td><Link className="font-medium text-accent hover:underline" href={`/employees/${e.id}`}>{e.employee_number}</Link></td>
                  <td>{e.last_name}, {e.first_name}</td>
                  <td>{e.department_name ?? <span className="text-muted">—</span>}</td>
                  <td>{e.position || <span className="text-muted">—</span>}</td>
                  <td>{e.device_user_ids.length ? e.device_user_ids.join(", ") : <span className="text-attention">Not mapped</span>}</td>
                  <td>{statusText[e.employment_status]}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      {items && total > items.length && <p className="text-sm text-muted">Showing {items.length} of {total}. Narrow the search to see others.</p>}
    </div>
  );
}
