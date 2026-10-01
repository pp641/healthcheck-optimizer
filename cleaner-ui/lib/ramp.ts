"use client";

import { useEffect, useState } from "react";

// Sequential blue ramp (one hue) for "how much space". In light mode more is
// darker; in dark mode the anchor flips so small folders recede into the
// background and big ones stand out.
const LIGHT = ["#cde2fb", "#9ec5f4", "#6da7ec", "#3987e5", "#256abf", "#184f95", "#0d366b"];
const DARK = ["#16273d", "#184f95", "#1c5cab", "#256abf", "#2a78d6", "#5598e7", "#9ec5f4"];

export function useDark(): boolean {
  const [dark, setDark] = useState(false);
  useEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const update = () => {
      const forced = document.documentElement.dataset.theme;
      setDark(forced ? forced === "dark" : mq.matches);
    };
    update();
    mq.addEventListener("change", update);
    return () => mq.removeEventListener("change", update);
  }, []);
  return dark;
}

export function ramp(dark: boolean): string[] {
  return dark ? DARK : LIGHT;
}

/** Maps a size share (0..1) onto the ramp. sqrt spreads small folders apart. */
export function colorFor(share: number, dark: boolean): string {
  const r = ramp(dark);
  const i = Math.min(r.length - 1, Math.max(0, Math.round(Math.sqrt(Math.max(0, Math.min(1, share))) * (r.length - 1))));
  return r[i];
}

/** Ink that stays readable on a ramp color. */
export function inkOn(hex: string): string {
  const n = parseInt(hex.slice(1), 16);
  const [r, g, b] = [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((c) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  });
  const lum = 0.2126 * r + 0.7152 * g + 0.0722 * b;
  return lum > 0.3 ? "#0b0b0b" : "#ffffff";
}
