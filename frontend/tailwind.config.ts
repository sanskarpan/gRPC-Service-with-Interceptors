import type { Config } from "tailwindcss";

const config: Config = {
  content: [
    "./pages/**/*.{js,ts,jsx,tsx,mdx}",
    "./components/**/*.{js,ts,jsx,tsx,mdx}",
    "./app/**/*.{js,ts,jsx,tsx,mdx}",
  ],
  theme: {
    extend: {
      fontFamily: {
        sans: ["var(--font-sans)", "ui-sans-serif", "system-ui", "sans-serif"],
        mono: ["var(--font-mono)", "ui-monospace", "monospace"],
      },
      colors: {
        ink: {
          900: "#0a0b0e",
          850: "#0e1014",
          800: "#121419",
          760: "#171a20",
          700: "#1d212a",
        },
        line: {
          DEFAULT: "#242832",
          2: "#2f3542",
        },
        txt: {
          DEFAULT: "#e8eaf0",
          2: "#98a0ac",
          3: "#616772",
        },
        signal: {
          DEFAULT: "#c7f04a",
          soft: "rgba(199,240,74,0.12)",
          line: "rgba(199,240,74,0.32)",
        },
        cyan: { signal: "#63d3e8" },
        amber: { signal: "#f0c65a" },
        rose: { signal: "#f2748d" },
        emerald: { signal: "#79e2a8" },
      },
      boxShadow: {
        panel: "0 24px 60px -20px rgba(0,0,0,0.6)",
        signal: "0 0 0 1px rgba(199,240,74,0.35), 0 8px 30px -8px rgba(199,240,74,0.35)",
      },
      keyframes: {
        "fade-up": {
          "0%": { opacity: "0", transform: "translateY(6px)" },
          "100%": { opacity: "1", transform: "translateY(0)" },
        },
      },
      animation: {
        "fade-up": "fade-up 0.4s cubic-bezier(0.2,0.7,0.2,1) both",
      },
    },
  },
  plugins: [],
};
export default config;
