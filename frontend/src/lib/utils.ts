import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

const CHIP_PALETTE = [
  "bg-fuchsia-100 text-fuchsia-700 ring-1 ring-fuchsia-300/60 dark:bg-fuchsia-500/15 dark:text-fuchsia-300 dark:ring-fuchsia-400/30",
  "bg-violet-100 text-violet-700 ring-1 ring-violet-300/60 dark:bg-violet-500/15 dark:text-violet-300 dark:ring-violet-400/30",
  "bg-sky-100 text-sky-700 ring-1 ring-sky-300/60 dark:bg-sky-500/15 dark:text-sky-300 dark:ring-sky-400/30",
  "bg-emerald-100 text-emerald-700 ring-1 ring-emerald-300/60 dark:bg-emerald-500/15 dark:text-emerald-300 dark:ring-emerald-400/30",
  "bg-amber-100 text-amber-700 ring-1 ring-amber-300/60 dark:bg-amber-500/15 dark:text-amber-300 dark:ring-amber-400/30",
  "bg-rose-100 text-rose-700 ring-1 ring-rose-300/60 dark:bg-rose-500/15 dark:text-rose-300 dark:ring-rose-400/30",
  "bg-cyan-100 text-cyan-700 ring-1 ring-cyan-300/60 dark:bg-cyan-500/15 dark:text-cyan-300 dark:ring-cyan-400/30",
  "bg-orange-100 text-orange-700 ring-1 ring-orange-300/60 dark:bg-orange-500/15 dark:text-orange-300 dark:ring-orange-400/30",
]

export const gradientButton =
  "border-0 bg-gradient-to-r from-fuchsia-500 via-violet-500 to-sky-500 text-white shadow-md shadow-violet-500/30 hover:opacity-90"

export function chipColor(seed: string) {
  let hash = 0
  for (let i = 0; i < seed.length; i++) hash = (hash * 31 + seed.charCodeAt(i)) >>> 0
  return CHIP_PALETTE[hash % CHIP_PALETTE.length]
}

const BLOB_PALETTE = ["#f472b6", "#a78bfa", "#38bdf8", "#34d399", "#fbbf24", "#fb7185", "#22d3ee", "#fb923c"]

export function blobColor(seed: number) {
  return BLOB_PALETTE[seed % BLOB_PALETTE.length]
}
