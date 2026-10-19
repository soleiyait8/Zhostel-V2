import type { Config } from "tailwindcss";

const v = (name: string) => `rgb(var(--${name}) / <alpha-value>)`;

export default {
  content: ["./app/**/*.{ts,tsx}", "./components/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        paper: v("paper"),
        surface: v("surface"),
        ink: v("ink"),
        muted: v("muted"),
        rule: v("rule"),
        rail: v("rail"),
        "rail-ink": v("rail-ink"),
        attention: v("attention"),
        "attention-wash": v("attention-wash"),
        ok: v("ok"),
        fault: v("fault"),
        "fault-wash": v("fault-wash"),
        accent: v("accent"),
      },
      fontFamily: {
        sans: ['"Public Sans"', "ui-sans-serif", "system-ui", "-apple-system", "Segoe UI", "Roboto", "sans-serif"],
      },
      fontSize: {
        // modular scale ~1.2
        xs: ["0.75rem", "1.1rem"],
        sm: ["0.8125rem", "1.25rem"],
        base: ["0.9375rem", "1.5rem"],
        lg: ["1.125rem", "1.6rem"],
        xl: ["1.375rem", "1.8rem"],
        "2xl": ["1.75rem", "2.1rem"],
        "3xl": ["2.5rem", "2.75rem"],
      },
    },
  },
  plugins: [],
} satisfies Config;
