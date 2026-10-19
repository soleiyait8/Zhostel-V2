"use client";
import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { dateTime } from "@/lib/format";
import { Empty, Loading, Notice, PageHeader } from "@/components/ui";

type Entry = { id: number; actor_name: string | null; action: string; entity_type: string; entity_id: string | null; before: unknown; after: unknown; source_ip: string | null; created_at: string };

export default function AuditPage() {
  const [items, setItems] = useState<Entry[] | null>(null);
  const [type, setType] = useState("");
  const [err, setErr] = useState("");
  useEffect(() => {
    setItems(null);
    api.get<{ items: Entry[] }>(`/api/audit-logs?limit=200${type ? `&entity_type=${type}` : ""}`).then((r) => setItems(r.items)).catch((e) => setErr(e.message));
  }, [type]);

  return (
    <div className="space-y-6">
      <PageHeader title="Audit trail" sub="Every change made in this system, who made it and from where. Entries cannot be edited or deleted." />
      <select className="field max-w-xs" value={type} onChange={(e) => setType(e.target.value)} aria-label="Record type">
        <option value="">All record types</option>
        {["employee", "employee_device_mapping", "device", "department", "user", "sync_error"].map((t) => <option key={t} value={t}>{t.replace(/_/g, " ")}</option>)}
      </select>
      {err && <Notice>{err}</Notice>}
      <div className="overflow-x-auto rounded-lg border border-rule bg-surface">
        {!items ? <Loading /> : items.length === 0 ? <Empty title="No entries" /> : (
          <table className="table">
            <thead><tr><th>When</th><th>Who</th><th>What</th><th>Details</th></tr></thead>
            <tbody>{items.map((e) => (
              <tr key={e.id}>
                <td className="whitespace-nowrap">{dateTime(e.created_at)}</td>
                <td>{e.actor_name ?? "System"}<span className="block text-xs text-muted">{e.source_ip}</span></td>
                <td>{e.action.replace(/_/g, " ")} {e.entity_type.replace(/_/g, " ")}</td>
                <td>{(e.before != null || e.after != null) && (
                  <details className="text-xs"><summary className="cursor-pointer text-muted">Show change</summary>
                    <div className="mt-1 grid gap-2 sm:grid-cols-2">
                      {e.before != null && <pre className="max-h-48 overflow-auto rounded bg-paper p-2">{JSON.stringify(e.before, null, 1)}</pre>}
                      {e.after != null && <pre className="max-h-48 overflow-auto rounded bg-paper p-2">{JSON.stringify(e.after, null, 1)}</pre>}
                    </div></details>
                )}</td>
              </tr>
            ))}</tbody>
          </table>
        )}
      </div>
    </div>
  );
}
