import { Link } from "react-router"
import {
  AlertTriangleIcon,
  ClockIcon,
  FlaskConicalIcon,
  PauseCircleIcon,
  PlugZapIcon,
  ServerIcon,
  ShieldAlertIcon,
  ShieldOffIcon,
} from "lucide-react"
import type { ReactNode } from "react"

import { PageHeader } from "@/components/app-shell"
import { ManagedActions } from "@/components/managed-actions"
import { HostStateBadge, NodeStatusBadge } from "@/components/status"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { countryFlag, formatDuration, formatRelative, parseTime, useNow } from "@/lib/format"
import { useI18n } from "@/lib/i18n"
import { useStatus } from "@/lib/queries"
import type { HostView, NodeView } from "@/lib/types"

function StatCard({ title, value, hint, icon, tone }: { title: string; value: ReactNode; hint?: ReactNode; icon: ReactNode; tone?: "danger" | "warn" }) {
  return (
    <Card size="sm">
      <CardContent className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="text-xs font-medium text-muted-foreground">{title}</div>
          <div
            className={
              "mt-1.5 text-2xl font-semibold tabular-nums " +
              (tone === "danger" ? "text-red-600 dark:text-red-400" : tone === "warn" ? "text-amber-600 dark:text-amber-400" : "")
            }
          >
            {value}
          </div>
          {hint && <div className="mt-1 truncate text-xs text-muted-foreground">{hint}</div>}
        </div>
        <div className="rounded-md bg-muted p-2 text-muted-foreground">{icon}</div>
      </CardContent>
    </Card>
  )
}

export function DashboardPage() {
  const { t, lang } = useI18n()
  const { data, isLoading } = useStatus()
  const now = useNow()

  if (isLoading || !data) {
    return (
      <div className="grid gap-4">
        <Skeleton className="h-8 w-48" />
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-24" />
          ))}
        </div>
        <Skeleton className="h-64" />
      </div>
    )
  }

  const { snapshot: snap, checker } = data
  const nodes = snap.nodes ?? []
  const hosts = snap.hosts ?? []
  const managed = snap.managed ?? []
  const nodeName = new Map(nodes.map((n) => [n.uuid, n.name]))
  const checkedAt = parseTime(snap.checkedAt)
  const nextAt = parseTime(snap.nextCheckAt)

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        className="mb-0"
        title={t("dash.title")}
        description={t("dash.rules", {
          down: checker.downMinutes,
          recovery: checker.recoveryMinutes,
          policy: t(`checker.policyShort.${checker.policy}`),
        })}
      />

      {!checker.configured && (
        <Alert>
          <PlugZapIcon />
          <AlertTitle>{t("dash.notConfigured")}</AlertTitle>
          <AlertDescription>
            <p>{t("dash.notConfiguredDesc")}</p>
            <Button asChild size="sm" className="mt-2">
              <Link to="/settings">{t("dash.openSettings")}</Link>
            </Button>
          </AlertDescription>
        </Alert>
      )}
      {checker.configured && snap.panelErrorCode && (
        <Alert variant="destructive">
          <AlertTriangleIcon />
          <AlertTitle>{t("dash.panelError")}</AlertTitle>
          <AlertDescription className="break-all">{snap.panelError}</AlertDescription>
        </Alert>
      )}
      {checker.dryRun && (
        <Alert>
          <FlaskConicalIcon />
          <AlertTitle>{t("dash.dryRun")}</AlertTitle>
          <AlertDescription>{t("dash.dryRunDesc")}</AlertDescription>
        </Alert>
      )}
      {!checker.enabled && (
        <Alert>
          <PauseCircleIcon />
          <AlertTitle>{t("dash.automationOff")}</AlertTitle>
          <AlertDescription>{t("dash.automationOffDesc")}</AlertDescription>
        </Alert>
      )}

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          title={t("dash.nodesOnline")}
          value={
            <>
              {snap.stats.nodesUp}
              <span className="text-base font-normal text-muted-foreground"> / {snap.stats.nodesTotal}</span>
            </>
          }
          hint={snap.stats.nodesDown > 0 ? t("dash.nodesDown", { count: snap.stats.nodesDown }) : undefined}
          icon={<ServerIcon className="size-4" />}
          tone={snap.stats.nodesDown > 0 ? "danger" : undefined}
        />
        <StatCard
          title={t("dash.hostsAtRisk")}
          value={snap.stats.hostsAtRisk}
          hint={t("dash.hostsAtRiskDesc")}
          icon={<ShieldAlertIcon className="size-4" />}
          tone={snap.stats.hostsAtRisk > 0 ? "warn" : undefined}
        />
        <StatCard
          title={t("dash.managed")}
          value={snap.stats.hostsManaged}
          hint={t("dash.managedDesc", { total: snap.stats.hostsTotal })}
          icon={<ShieldOffIcon className="size-4" />}
          tone={snap.stats.hostsManaged > 0 ? "danger" : undefined}
        />
        <StatCard
          title={t("dash.lastCheck")}
          value={<span className="text-lg">{checkedAt ? formatRelative(checkedAt, now, lang) : t("dash.never")}</span>}
          hint={nextAt && nextAt.getTime() > now ? t("dash.nextCheck", { time: formatRelative(nextAt, now, lang) }) : undefined}
          icon={<ClockIcon className="size-4" />}
        />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>{t("dash.managedTitle")}</CardTitle>
        </CardHeader>
        <CardContent>
          {managed.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t("dash.managedEmpty")}</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("managed.host")}</TableHead>
                  <TableHead className="hidden md:table-cell">{t("managed.nodes")}</TableHead>
                  <TableHead>{t("managed.since")}</TableHead>
                  <TableHead className="hidden sm:table-cell">{t("managed.state")}</TableHead>
                  <TableHead className="hidden lg:table-cell">{t("managed.templates")}</TableHead>
                  <TableHead className="text-right">{t("managed.actions")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {managed.map((m) => {
                  const since = parseTime(m.disabledAt)
                  const state = m.state === "disabled" ? "managed" : m.state
                  return (
                    <TableRow key={m.uuid}>
                      <TableCell className="font-medium">{m.remark}</TableCell>
                      <TableCell className="hidden text-muted-foreground md:table-cell">
                        {m.nodes.map((id) => nodeName.get(id) ?? id.slice(0, 8)).join(", ")}
                      </TableCell>
                      <TableCell className="text-muted-foreground">{since ? formatRelative(since, now, lang) : "—"}</TableCell>
                      <TableCell className="hidden sm:table-cell">
                        <HostStateBadge state={state} />
                      </TableCell>
                      <TableCell className="hidden lg:table-cell">
                        {m.mihomo && m.mihomo.length > 0 ? (
                          <Badge variant="secondary">{t("managed.templatesCount", { count: m.mihomo.length })}</Badge>
                        ) : (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </TableCell>
                      <TableCell>
                        <ManagedActions host={m} />
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t("dash.nodesTitle")}</CardTitle>
          <CardDescription>
            {snap.stats.nodesUp} / {snap.stats.nodesTotal}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {nodes.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t("dash.nodesEmpty")}</p>
          ) : (
            <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
              {nodes.map((n) => (
                <NodeTile key={n.uuid} node={n} hosts={hosts} now={now} />
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

function NodeTile({ node, hosts, now }: { node: NodeView; hosts: HostView[]; now: number }) {
  const { t, lang } = useI18n()
  const down = parseTime(node.downSince)
  const bound = hosts.filter((h) => h.nodes.includes(node.uuid))
  const atRisk = bound.filter((h) => h.state === "at_risk")
  const disableAt = atRisk
    .map((h) => parseTime(h.disableAt)?.getTime() ?? Infinity)
    .reduce((a, b) => Math.min(a, b), Infinity)

  return (
    <div className={"rounded-lg border p-3 " + (node.status === "down" ? "border-red-500/40 bg-red-500/5" : "")}>
      <div className="flex items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <span className="text-base leading-none">{countryFlag(node.countryCode)}</span>
          <span className="truncate text-sm font-medium">{node.name}</span>
        </div>
        <NodeStatusBadge status={node.status} />
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
        <span className="truncate font-mono">{node.address}</span>
        <span>
          {t("nav.hosts")}: {bound.length}
        </span>
        {node.excluded && <HostStateBadge state="excluded" />}
      </div>
      {node.status === "down" && down && (
        <div className="mt-2 text-xs">
          <span className="text-red-600 dark:text-red-400">{t("node.downFor", { time: formatDuration(now - down.getTime(), lang) })}</span>
          {Number.isFinite(disableAt) && (
            <span className="text-amber-600 dark:text-amber-400">
              {" · "}
              {disableAt > now
                ? t("state.disableIn", { time: formatDuration(disableAt - now, lang) })
                : t("state.disableSoon")}
            </span>
          )}
        </div>
      )}
    </div>
  )
}
