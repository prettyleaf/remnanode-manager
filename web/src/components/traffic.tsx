import { cn } from "@/lib/utils"
import { formatBytes } from "@/lib/format"
import { useI18n } from "@/lib/i18n"
import type { NodeTraffic } from "@/lib/types"

/**
 * Node traffic against its panel limit. `threshold` marks the share at which
 * hosts are disabled; leave it out while traffic-based disabling is off.
 */
export function TrafficBar({ traffic, threshold, className }: { traffic: NodeTraffic; threshold?: number; className?: string }) {
  const { t, lang } = useI18n()
  const { usedBytes, limitBytes } = traffic

  if (limitBytes <= 0) {
    return (
      <div className={cn("text-xs text-muted-foreground tabular-nums", className)}>
        {formatBytes(usedBytes, lang)} · {t("traffic.noLimit")}
      </div>
    )
  }

  const percent = (usedBytes / limitBytes) * 100
  const marker = threshold != null && Number.isFinite(threshold) && threshold < 100 ? threshold : undefined
  const limit = marker ?? 100
  const tone =
    traffic.overLimit || percent >= limit ? "bg-red-500" : percent >= limit - 10 ? "bg-amber-500" : "bg-emerald-500"
  const notes = [traffic.overLimit && t("traffic.over"), traffic.resetDay && t("traffic.resetDay", { day: traffic.resetDay })].filter(Boolean)

  return (
    <div className={cn("grid gap-1", className)}>
      <div className="flex items-baseline justify-between gap-2 text-xs tabular-nums">
        <span className="truncate text-muted-foreground">
          {formatBytes(usedBytes, lang)} / {formatBytes(limitBytes, lang)}
        </span>
        <span className={cn("font-medium", traffic.overLimit && "text-red-600 dark:text-red-400")}>{Math.floor(percent)}%</span>
      </div>
      <div
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.min(100, Math.round(percent))}
        className="relative h-1.5 overflow-hidden rounded-full bg-muted"
      >
        <div className={cn("h-full rounded-full transition-[width]", tone)} style={{ width: `${Math.min(percent, 100)}%` }} />
        {marker != null && <div className="absolute inset-y-0 w-0.5 bg-foreground/50" style={{ left: `${marker}%` }} />}
      </div>
      {notes.length > 0 && (
        <div className={cn("text-[11px]", traffic.overLimit ? "text-red-600 dark:text-red-400" : "text-muted-foreground")}>
          {notes.join(" · ")}
        </div>
      )}
    </div>
  )
}
