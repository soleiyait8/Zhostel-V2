"use client";
import { FormEvent, useState } from "react";
import { api, ApiError, Department, Employee } from "@/lib/api";
import { Field, Notice } from "@/components/ui";

type Values = Pick<Employee, "employee_number" | "first_name" | "last_name" | "department_id" | "position" | "employment_status" | "hire_date">;

const blank: Values = { employee_number: "", first_name: "", last_name: "", department_id: null, position: "", employment_status: "active", hire_date: null };

export function EmployeeForm({ initial, departments, onSaved, onCancel, id }: {
  initial?: Values; departments: Department[]; onSaved: (id: string) => void; onCancel?: () => void; id?: string;
}) {
  const [v, setV] = useState<Values>(initial ?? blank);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const set = (k: keyof Values) => (e: { target: { value: string } }) => setV({ ...v, [k]: e.target.value || (k === "department_id" || k === "hire_date" ? null : "") });

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true); setError(""); setFields({});
    try {
      if (id) { await api.put(`/api/employees/${id}`, v); onSaved(id); }
      else { const r = await api.post<{ id: string }>("/api/employees", v); onSaved(r.id); }
    } catch (err) {
      if (err instanceof ApiError) { setFields(err.fields ?? {}); setError(err.fields ? "" : err.message); }
      else setError("The server could not be reached.");
    } finally { setBusy(false); }
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      {error && <Notice>{error}</Notice>}
      <div className="grid gap-4 sm:grid-cols-3">
        <Field label="Employee number" error={fields.employee_number}>
          <input className="field" required value={v.employee_number} onChange={set("employee_number")} />
        </Field>
        <Field label="First name" error={fields.first_name}>
          <input className="field" required value={v.first_name} onChange={set("first_name")} />
        </Field>
        <Field label="Last name" error={fields.last_name}>
          <input className="field" required value={v.last_name} onChange={set("last_name")} />
        </Field>
        <Field label="Department" error={fields.department_id}>
          <select className="field" value={v.department_id ?? ""} onChange={set("department_id")}>
            <option value="">No department</option>
            {departments.filter((d) => d.active || d.id === v.department_id).map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
          </select>
        </Field>
        <Field label="Position" error={fields.position}>
          <input className="field" value={v.position} onChange={set("position")} />
        </Field>
        <Field label="Hire date" error={fields.hire_date}>
          <input className="field" type="date" value={v.hire_date ?? ""} onChange={set("hire_date")} />
        </Field>
        {id && (
          <Field label="Employment status" error={fields.employment_status}>
            <select className="field" value={v.employment_status} onChange={set("employment_status")}>
              <option value="active">Active</option>
              <option value="on_leave">On leave</option>
              <option value="inactive">Inactive</option>
              <option value="separated">Separated</option>
            </select>
          </Field>
        )}
      </div>
      <div className="flex gap-2">
        <button className="btn-primary" disabled={busy}>{busy ? "Saving…" : id ? "Save changes" : "Add employee"}</button>
        {onCancel && <button type="button" className="btn-quiet" onClick={onCancel}>Cancel</button>}
      </div>
    </form>
  );
}
