"use client";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { createContext, ReactNode, useContext, useEffect, useState } from "react";
import { api, Me, Role } from "@/lib/api";

const MeCtx = createContext<Me | null>(null);
export const useMe = () => useContext(MeCtx);
const rank: Record<Role, number> = { viewer: 1, hr: 2, admin: 3 };
export const can = (me: Me | null, min: Role) => !!me && rank[me.role] >= rank[min];

const nav: { href: string; label: string; min: Role }[] = [
  { href: "/", label: "Today", min: "viewer" },
  { href: "/employees", label: "Employees", min: "viewer" },
  { href: "/devices", label: "Devices", min: "viewer" },
  { href: "/punches", label: "Punch history", min: "viewer" },
  { href: "/audit", label: "Audit trail", min: "hr" },
  { href: "/users", label: "Users", min: "admin" },
];

export function Shell({ children }: { children: ReactNode }) {
  const [me, setMe] = useState<Me | null>(null);
  const [open, setOpen] = useState(false);
  const path = usePathname();
  const router = useRouter();

  useEffect(() => {
    api.get<Me>("/api/auth/me").then(setMe).catch(() => {});
  }, []);
  useEffect(() => setOpen(false), [path]);

  async function signOut() {
    await api.post("/api/auth/logout").catch(() => {});
    router.replace("/login");
  }

  if (!me) return <div className="p-8 text-sm text-muted">Checking your session…</div>;

  const links = nav.filter((n) => can(me, n.min));
  const active = (href: string) => (href === "/" ? path === "/" : path.startsWith(href));

  return (
    <MeCtx.Provider value={me}>
      <div className="min-h-screen lg:grid lg:grid-cols-[15rem_1fr]">
        <aside className="bg-rail text-rail-ink lg:sticky lg:top-0 lg:h-screen">
          <div className="flex items-center justify-between px-5 py-4 lg:py-6">
            <Link href="/" className="leading-tight">
              <span className="block text-lg font-bold text-white">Timekeeping</span>
              <span className="block text-xs text-rail-ink/70">MB10-VL attendance</span>
            </Link>
            <button className="rounded px-2 py-1 text-sm lg:hidden" onClick={() => setOpen(!open)} aria-expanded={open}>
              {open ? "Close" : "Menu"}
            </button>
          </div>
          <nav className={`${open ? "block" : "hidden"} px-3 pb-4 lg:block`}>
            {links.map((n) => (
              <Link
                key={n.href}
                href={n.href}
                aria-current={active(n.href) ? "page" : undefined}
                className={`mb-0.5 block rounded-md px-3 py-2 text-sm font-medium ${
                  active(n.href) ? "bg-white/10 text-white" : "text-rail-ink/80 hover:bg-white/5 hover:text-white"
                }`}
              >
                {n.label}
              </Link>
            ))}
            <div className="mt-6 border-t border-white/10 px-3 pt-4 text-sm lg:absolute lg:bottom-4 lg:left-3 lg:right-3 lg:mt-0">
              <p className="font-semibold text-white">{me.name}</p>
              <p className="text-xs capitalize text-rail-ink/70">{me.role === "hr" ? "HR" : me.role}</p>
              <button onClick={signOut} className="mt-2 text-xs underline underline-offset-2 hover:text-white">
                Sign out
              </button>
            </div>
          </nav>
        </aside>
        <main className="mx-auto w-full max-w-6xl px-4 py-6 sm:px-8 lg:py-10">{children}</main>
      </div>
    </MeCtx.Provider>
  );
}
