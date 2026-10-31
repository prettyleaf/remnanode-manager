import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"
import { useI18n, type TKey } from "@/lib/i18n"
import type { HostState, NodeStatus } from "@/lib/types"

const dotColors: Record<string, string> = {
  green: "bg-emerald-500",
  red: "bg-red-500",
  amber: "bg-amber-500",
  gray: "bg-muted-foreground/40",
  blue: "bg-sky-500",
}

export function StatusDot({ color, pulse }: { color: keyof typeof dotColors; pulse?: boolean }) {
  return (
    <span className="relative inline-flex size-2 shrink-0">
      {pulse && (
        <span className={cn("absolute inline-flex size-full animate-ping rounded-full opacity-60", dotColors[color])} />
      )}
      <span className={cn("relative inline-flex size-2 rounded-full", dotColors[color])} />
    </span>
  )
}

export function NodeStatusBadge({ status }: { status: NodeStatus }) {
  const { t } = useI18n()
  const map: Record<string, { color: keyof typeof dotColors; label: TKey }> = {
    up: { color: "green", label: "node.up" },
    down: { color: "red", label: "node.down" },
    disabled: { color: "gray", label: "node.disabled" },
  }
  const v = map[status] ?? { color: "gray", label: "node.unknown" as TKey }
  return (
    <Badge variant="outline" className="gap-1.5 font-normal">
      <StatusDot color={v.color} pulse={status === "down"} />
      {t(v.label)}
    </Badge>
  )
}

const hostStateStyle: Record<HostState, string> = {
  ok: "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
  at_risk: "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400",
  managed: "border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-400",
  dry_run: "border-violet-500/30 bg-violet-500/10 text-violet-700 dark:text-violet-400",
  overridden: "border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-400",
  disabled: "text-muted-foreground",
  excluded: "text-muted-foreground",
  unbound: "text-muted-foreground",
}

export function HostStateBadge({ state }: { state: HostState }) {
  const { t } = useI18n()
  return (
    <Badge variant="outline" className={cn("font-normal", hostStateStyle[state])}>
      {t(`state.${state}` as TKey)}
    </Badge>
  )
}

export function BlockBadge() {
  const { t } = useI18n()
  return (
    <Badge variant="outline" className="gap-1.5 border-amber-500/30 bg-amber-500/10 font-normal text-amber-700 dark:text-amber-400">
      <StatusDot color="amber" pulse />
      {t("block.badge")}
    </Badge>
  )
}
