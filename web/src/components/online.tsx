import { cn } from "@/lib/utils"
import { formatDuration, parseTime } from "@/lib/format"
import { useI18n } from "@/lib/i18n"
import type { NodeView } from "@/lib/types"

/** Users online against the node's usual level, with a drop if there is one. */
export function NodeOnline({ node, now, className }: { node: NodeView; now: number; className?: string }) {
  const { t, lang } = useI18n()
  const { online, block } = node
  if (online == null) return <span className={cn("text-xs text-muted-foreground", className)}>—</span>

  const drop = parseTime(block.dropSince)
  const blocked = parseTime(block.blockedSince)
  return (
    <div className={cn("grid gap-0.5 text-xs tabular-nums", className)}>
      <div className="flex items-baseline gap-1.5">
        <span className={cn("text-sm font-medium", drop && "text-amber-600 dark:text-amber-400")}>{online}</span>
        {block.usual > 0 && <span className="text-muted-foreground">{t("online.usual", { count: block.usual })}</span>}
      </div>
      {drop && (
        <span className={blocked ? "text-amber-600 dark:text-amber-400" : "text-muted-foreground"}>
          {t(blocked ? "block.blockedFor" : "block.dropping", { time: formatDuration(now - drop.getTime(), lang) })}
        </span>
      )}
    </div>
  )
}
