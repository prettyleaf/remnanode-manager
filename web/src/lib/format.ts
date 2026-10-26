import { useEffect, useState } from "react"
import type { Lang } from "./i18n"

/** Go encodes a zero time.Time as year 1; treat it as "no value". */
export function parseTime(v: string | null | undefined): Date | null {
  if (!v) return null
  const d = new Date(v)
  if (Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return null
  return d
}

export function formatDuration(ms: number, lang: Lang): string {
  const total = Math.max(0, Math.round(ms / 1000))
  const units =
    lang === "ru" ? { d: "д", h: "ч", m: "мин", s: "сек" } : { d: "d", h: "h", m: "min", s: "s" }
  if (total < 60) return `${total} ${units.s}`
  const days = Math.floor(total / 86400)
  const hours = Math.floor((total % 86400) / 3600)
  const mins = Math.floor((total % 3600) / 60)
  const parts: string[] = []
  if (days) parts.push(`${days} ${units.d}`)
  if (hours) parts.push(`${hours} ${units.h}`)
  if (mins && days === 0) parts.push(`${mins} ${units.m}`)
  return parts.join(" ") || `0 ${units.m}`
}

/** Binary units, the way the panel counts node traffic. */
export function formatBytes(bytes: number, lang: Lang): string {
  const units = lang === "ru" ? ["Б", "КБ", "МБ", "ГБ", "ТБ", "ПБ"] : ["B", "KB", "MB", "GB", "TB", "PB"]
  let v = Math.max(0, bytes)
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  const num = new Intl.NumberFormat(lang, { maximumFractionDigits: v < 100 ? 1 : 0 }).format(v)
  return `${num} ${units[i]}`
}

export function formatRelative(date: Date, now: number, lang: Lang): string {
  const diff = (date.getTime() - now) / 1000
  const rtf = new Intl.RelativeTimeFormat(lang, { numeric: "auto" })
  const abs = Math.abs(diff)
  if (abs < 45) return rtf.format(Math.round(diff), "second")
  if (abs < 3600) return rtf.format(Math.round(diff / 60), "minute")
  if (abs < 86400) return rtf.format(Math.round(diff / 3600), "hour")
  return rtf.format(Math.round(diff / 86400), "day")
}

export function formatDateTime(date: Date, lang: Lang): string {
  return new Intl.DateTimeFormat(lang, {
    day: "2-digit",
    month: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(date)
}

export function formatFullDateTime(date: Date, lang: Lang): string {
  return new Intl.DateTimeFormat(lang, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date)
}

/** Release versions get a "v" prefix, dev builds are shown as is. */
export function displayVersion(v: string): string {
  return /^\d+\.\d+\.\d+$/.test(v) ? `v${v}` : v
}

/** Re-renders the component every `interval` ms and returns the current time. */
export function useNow(interval = 1000): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), interval)
    return () => window.clearInterval(id)
  }, [interval])
  return now
}

export function countryFlag(code: string): string {
  if (!/^[A-Za-z]{2}$/.test(code) || code.toUpperCase() === "XX") return "🏳️"
  return String.fromCodePoint(...[...code.toUpperCase()].map((c) => 0x1f1a5 + c.charCodeAt(0)))
}
