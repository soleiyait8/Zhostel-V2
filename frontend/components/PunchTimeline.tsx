"use client";
// The dashboard's signature view: today's punches laid out across 24 hours,
// so a late rush or a dead device reads at a glance.

export function PunchTimeline({ hours, nowHour, total }: { hours: number[]; nowHour: number; total: number }) {
  const peak = Math.max(1, ...hours);
  return (
    <figure className="rounded-lg border border-rule bg-surface px-4 pb-3 pt-4 sm:px-6">
      <figcaption className="mb-4 flex flex-wrap items-baseline justify-between gap-2">
        <span className="text-lg font-semibold">Punches today</span>
        <span className="text-sm text-muted">
          <strong className="text-2xl font-bold text-ink">{total}</strong> received
        </span>
      </figcaption>
      <div className="relative">
        <div className="grid h-36 grid-cols-24 items-end gap-[3px]" style={{ gridTemplateColumns: "repeat(24, minmax(0, 1fr))" }}>
          {hours.map((n, h) => {
            const future = h > nowHour;
            return (
              <div key={h} className="group relative flex h-full items-end" title={`${String(h).padStart(2, "0")}:00 — ${n} punch${n === 1 ? "" : "es"}`}>
                <div
                  className={`w-full rounded-t-sm ${n === 0 ? "bg-rule/60" : future ? "bg-accent/30" : "bg-accent"}`}
                  style={{ height: n === 0 ? "2px" : `${Math.max(6, (n / peak) * 100)}%` }}
                />
                {n > 0 && (
                  <span className="pointer-events-none absolute -top-5 left-1/2 -translate-x-1/2 text-[11px] font-semibold text-ink opacity-0 group-hover:opacity-100">
                    {n}
                  </span>
                )}
              </div>
            );
          })}
        </div>
        <div
          className="pointer-events-none absolute inset-y-0 w-px bg-attention"
          style={{ left: `calc(${((nowHour + 0.5) / 24) * 100}%)` }}
          aria-hidden
        />
      </div>
      <div className="mt-2 grid text-[11px] text-muted" style={{ gridTemplateColumns: "repeat(24, minmax(0, 1fr))" }}>
        {hours.map((_, h) => (
          <span key={h} className="text-center">{h % 3 === 0 ? String(h).padStart(2, "0") : ""}</span>
        ))}
      </div>
    </figure>
  );
}
