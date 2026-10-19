import "@fontsource/public-sans/400.css";
import "@fontsource/public-sans/500.css";
import "@fontsource/public-sans/600.css";
import "@fontsource/public-sans/700.css";
import "./globals.css";
import type { Metadata, Viewport } from "next";

export const metadata: Metadata = { title: "Timekeeping", description: "Attendance and device management" };
export const viewport: Viewport = { width: "device-width", initialScale: 1 };

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body className="font-sans text-base">{children}</body>
    </html>
  );
}
