import { useMemo, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { EyeOffIcon, FileCode2Icon, Loader2Icon, SearchIcon } from "lucide-react"

import { PageHeader } from "@/components/app-shell"
import { HostStateBadge } from "@/components/status"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { api, post } from "@/lib/api"
import { formatDuration, parseTime, useNow } from "@/lib/format"
import { useI18n, type TKey } from "@/lib/i18n"
import { useStatus } from "@/lib/queries"
import type { HostState, HostView, TemplateRef } from "@/lib/types"

const states: HostState[] = ["ok", "at_risk", "managed", "dry_run", "overridden", "disabled", "excluded", "unbound"]

export function HostsPage() {
  const { t, lang } = useI18n()
  const { data } = useStatus()
  const qc = useQueryClient()
  const now = useNow()
  const [search, setSearch] = useState("")
  const [state, setState] = useState<string>("all")
  const [refsFor, setRefsFor] = useState<HostView | null>(null)

  const nodes = data?.snapshot.nodes ?? []
  const nodeName = new Map(nodes.map((n) => [n.uuid, n]))
  const hosts = useMemo(() => {
    const q = search.trim().toLowerCase()
    return (data?.snapshot.hosts ?? []).filter(
      (h) =>
        (state === "all" || h.state === state) &&
        (!q || h.remark.toLowerCase().includes(q) || h.address.toLowerCase().includes(q) || h.tags.some((tag) => tag.toLowerCase().includes(q))),
    )
  }, [data, search, state])

  const exclude = useMutation({
    mutationFn: ({ uuid, excluded }: { uuid: string; excluded: boolean }) => post(`/hosts/${uuid}/exclude`, { excluded }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["status"] })
      qc.invalidateQueries({ queryKey: ["settings"] })
    },
    onError: (e: Error) => toast.error(e.message),
  })

  return (
    <>
      <PageHeader title={t("hosts.title")} description={t("hosts.desc")} />
      <div className="mb-4 flex flex-col gap-2 sm:flex-row">
        <div className="relative flex-1">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input className="pl-8" placeholder={t("common.search")} value={search} onChange={(e) => setSearch(e.target.value)} />
        </div>
        <Select value={state} onValueChange={setState}>
          <SelectTrigger className="sm:w-56">
            <SelectValue placeholder={t("hosts.filterState")} />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t("common.all")}</SelectItem>
            {states.map((s) => (
              <SelectItem key={s} value={s}>
                {t(`state.${s}` as TKey)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <Card className="py-0">
        <CardContent className="px-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="pl-4">{t("hosts.remark")}</TableHead>
                <TableHead className="hidden lg:table-cell">{t("hosts.address")}</TableHead>
                <TableHead className="hidden md:table-cell">{t("hosts.nodes")}</TableHead>
                <TableHead>{t("hosts.state")}</TableHead>
                <TableHead className="text-center">{t("hosts.exclude")}</TableHead>
                <TableHead className="pr-4" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {hosts.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="py-10 text-center text-muted-foreground">
                    {t("hosts.empty")}
                  </TableCell>
                </TableRow>
              )}
              {hosts.map((h) => {
                const disableAt = parseTime(h.disableAt)
                return (
                  <TableRow key={h.uuid}>
                    <TableCell className="pl-4 whitespace-normal">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <span className="font-medium">{h.remark}</span>
                        {h.isHidden && (
                          <Badge variant="outline" className="gap-1 font-normal text-muted-foreground">
                            <EyeOffIcon className="size-3" />
                            {t("common.hidden")}
                          </Badge>
                        )}
                        {h.tags.map((tag) => (
                          <Badge key={tag} variant="secondary" className="font-mono text-[10px]">
                            {tag}
                          </Badge>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell className="hidden font-mono text-xs text-muted-foreground lg:table-cell">
                      {h.address}:{h.port}
                    </TableCell>
                    <TableCell className="hidden whitespace-normal md:table-cell">
                      <div className="flex flex-wrap gap-1">
                        {h.nodes.length === 0 && <span className="text-muted-foreground">—</span>}
                        {h.nodes.map((id) => {
                          const n = nodeName.get(id)
                          return (
                            <Badge
                              key={id}
                              variant="outline"
                              className={
                                "font-normal " + (n?.status === "down" ? "border-red-500/40 text-red-600 dark:text-red-400" : "")
                              }
                            >
                              {n?.name ?? id.slice(0, 8)}
                            </Badge>
                          )
                        })}
                      </div>
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-col items-start gap-1">
                        <HostStateBadge state={h.state} />
                        {h.state === "at_risk" && disableAt && (
                          <span className="text-xs text-amber-600 dark:text-amber-400">
                            {disableAt.getTime() > now
                              ? t("state.disableIn", { time: formatDuration(disableAt.getTime() - now, lang) })
                              : t("state.disableSoon")}
                          </span>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="text-center">
                      <Switch
                        checked={h.excluded}
                        disabled={exclude.isPending}
                        onCheckedChange={(v) => exclude.mutate({ uuid: h.uuid, excluded: v })}
                      />
                    </TableCell>
                    <TableCell className="pr-4 text-right">
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <Button variant="ghost" size="icon-sm" onClick={() => setRefsFor(h)}>
                            <FileCode2Icon />
                          </Button>
                        </TooltipTrigger>
                        <TooltipContent>{t("hosts.refs")}</TooltipContent>
                      </Tooltip>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
      <ReferencesDialog host={refsFor} onClose={() => setRefsFor(null)} />
    </>
  )
}

function ReferencesDialog({ host, onClose }: { host: HostView | null; onClose: () => void }) {
  const { t } = useI18n()
  const { data, isLoading, error } = useQuery({
    queryKey: ["refs", host?.uuid],
    queryFn: () => api<{ templates: TemplateRef[] }>(`/hosts/${host!.uuid}/references`),
    enabled: !!host,
  })

  return (
    <Dialog open={!!host} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("hosts.refsTitle", { host: host?.remark ?? "" })}</DialogTitle>
          <DialogDescription>{t("hosts.refsDesc")}</DialogDescription>
        </DialogHeader>
        {isLoading && <Loader2Icon className="mx-auto size-5 animate-spin text-muted-foreground" />}
        {error && <p className="text-sm text-destructive">{(error as Error).message}</p>}
        {data && data.templates.length === 0 && <p className="text-sm text-muted-foreground">{t("hosts.refsEmpty")}</p>}
        <div className="flex max-h-96 flex-col gap-3 overflow-y-auto">
          {data?.templates.map((tpl) => (
            <div key={tpl.uuid} className="rounded-lg border p-3 text-sm">
              <div className="mb-2 flex items-center gap-2">
                <span className="font-medium">{tpl.name}</span>
                <Badge variant="secondary">{tpl.type}</Badge>
              </div>
              {tpl.error && <p className="text-destructive">{tpl.error}</p>}
              {!!tpl.groups?.length && (
                <div className="mb-1">
                  <span className="text-muted-foreground">{t("hosts.groups")}: </span>
                  {tpl.groups.join(", ")}
                </div>
              )}
              {!!tpl.rules?.length && (
                <div>
                  <span className="text-muted-foreground">{t("hosts.rules")}:</span>
                  <ul className="mt-1 space-y-0.5 font-mono text-xs">
                    {tpl.rules.map((r) => (
                      <li key={r} className="break-all">
                        {r}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </div>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  )
}
