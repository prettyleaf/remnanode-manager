import { useMutation, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { PageHeader } from "@/components/app-shell"
import { NodeStatusBadge } from "@/components/status"
import { TrafficBar } from "@/components/traffic"
import { Card, CardContent } from "@/components/ui/card"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { post } from "@/lib/api"
import { countryFlag, formatDuration, parseTime, useNow } from "@/lib/format"
import { useI18n } from "@/lib/i18n"
import { useStatus } from "@/lib/queries"

export function NodesPage() {
  const { t, lang } = useI18n()
  const { data } = useStatus()
  const qc = useQueryClient()
  const now = useNow()
  const nodes = data?.snapshot.nodes ?? []
  const threshold = data?.checker.traffic ? data.checker.trafficPercent : undefined

  const exclude = useMutation({
    mutationFn: ({ uuid, excluded }: { uuid: string; excluded: boolean }) => post(`/nodes/${uuid}/exclude`, { excluded }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["status"] })
      qc.invalidateQueries({ queryKey: ["settings"] })
    },
    onError: (e: Error) => toast.error(e.message),
  })

  return (
    <>
      <PageHeader title={t("nodes.title")} description={t("nodes.desc")} />
      <Card className="py-0">
        <CardContent className="px-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="pl-4">{t("nodes.name")}</TableHead>
                <TableHead>{t("nodes.status")}</TableHead>
                <TableHead className="hidden sm:table-cell">{t("nodes.since")}</TableHead>
                <TableHead className="hidden md:table-cell">{t("traffic.column")}</TableHead>
                <TableHead className="hidden lg:table-cell">{t("nodes.message")}</TableHead>
                <TableHead className="hidden md:table-cell text-center">{t("nodes.hosts")}</TableHead>
                <TableHead className="pr-4 text-center">{t("nodes.exclude")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {nodes.length === 0 && (
                <TableRow>
                  <TableCell colSpan={7} className="py-10 text-center text-muted-foreground">
                    {t("nodes.empty")}
                  </TableCell>
                </TableRow>
              )}
              {nodes.map((n) => {
                const down = parseTime(n.downSince)
                const up = parseTime(n.upSince)
                return (
                  <TableRow key={n.uuid}>
                    <TableCell className="pl-4">
                      <div className="flex items-center gap-2">
                        <span>{countryFlag(n.countryCode)}</span>
                        <div className="min-w-0">
                          <div className="font-medium">{n.name}</div>
                          <div className="font-mono text-xs text-muted-foreground">{n.address}</div>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell>
                      <NodeStatusBadge status={n.status} />
                    </TableCell>
                    <TableCell className="hidden text-sm text-muted-foreground sm:table-cell">
                      {n.status === "down" && down && (
                        <span className="text-red-600 dark:text-red-400">
                          {t("node.downFor", { time: formatDuration(now - down.getTime(), lang) })}
                        </span>
                      )}
                      {n.status === "up" && up && t("node.upFor", { time: formatDuration(now - up.getTime(), lang) })}
                    </TableCell>
                    <TableCell className="hidden md:table-cell">
                      <TrafficBar traffic={n.traffic} threshold={threshold} className="w-44" />
                    </TableCell>
                    <TableCell className="hidden max-w-xs truncate text-xs text-muted-foreground lg:table-cell" title={n.lastMessage}>
                      {n.status !== "up" && n.lastMessage ? n.lastMessage : ""}
                    </TableCell>
                    <TableCell className="hidden text-center tabular-nums md:table-cell">{n.hosts.length}</TableCell>
                    <TableCell className="pr-4 text-center">
                      <Switch
                        checked={n.excluded}
                        disabled={exclude.isPending}
                        onCheckedChange={(v) => exclude.mutate({ uuid: n.uuid, excluded: v })}
                      />
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </>
  )
}
