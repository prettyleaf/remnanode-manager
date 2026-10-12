import { useState } from "react"
import { Loader2Icon, SearchIcon } from "lucide-react"

import { PageHeader } from "@/components/app-shell"
import { StatusDot } from "@/components/status"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { formatDateTime, formatDuration } from "@/lib/format"
import { useI18n, type TKey } from "@/lib/i18n"
import { useEvents } from "@/lib/queries"
import type { AppEvent } from "@/lib/types"

const eventTypes = [
  "node_down",
  "node_up",
  "host_disabled",
  "host_enabled",
  "host_overridden",
  "host_released",
  "dry_run_disable",
  "dry_run_enable",
  "mihomo_removed",
  "mihomo_restored",
  "panel_unreachable",
  "panel_restored",
  "guard_triggered",
  "error",
  "telegram_failed",
  "login",
  "login_failed",
  "settings_updated",
]

function useEventText() {
  const { t, has, lang } = useI18n()
  return (e: AppEvent): { title: string; details: string } => {
    const key = `ev.${e.type}`
    if (!has(key)) return { title: e.message, details: "" }
    const meta = (e.meta ?? {}) as Record<string, unknown>
    const vars: Record<string, string | number> = {}
    for (const [k, v] of Object.entries(meta)) {
      if (typeof v === "string" || typeof v === "number") vars[k] = v
    }
    if (typeof meta.downtimeSeconds === "number") vars.downtime = formatDuration(meta.downtimeSeconds * 1000, lang)
    if (e.type === "telegram_failed" && has(`tg.event.${meta.event}`)) vars.event = t(`tg.event.${meta.event}` as TKey)

    let details = ""
    switch (e.type) {
      case "host_disabled":
      case "dry_run_disable":
        details = Array.isArray(meta.nodes) ? (meta.nodes as string[]).join(", ") : ""
        break
      case "node_down":
        details = String(meta.message ?? "")
        break
      case "mihomo_removed":
      case "mihomo_restored":
        details = Array.isArray(meta.groups) ? (meta.groups as string[]).join(", ") : ""
        if (Array.isArray(meta.rules) && meta.rules.length)
          details += (details ? " · " : "") + `${t("hosts.rules")}: ${meta.rules.length}`
        break
      case "error":
      case "panel_unreachable":
      case "telegram_failed":
        details = String(meta.error ?? e.message)
        break
    }
    return { title: t(key as TKey, vars), details }
  }
}

const levelColor = { info: "blue", warn: "amber", error: "red" } as const

export function EventsPage() {
  const { t, lang } = useI18n()
  const [type, setType] = useState("all")
  const [level, setLevel] = useState("all")
  const [search, setSearch] = useState("")
  const [query, setQuery] = useState("")
  const text = useEventText()

  const { data, fetchNextPage, hasNextPage, isFetchingNextPage, isLoading } = useEvents({
    type: type === "all" ? "" : type,
    level: level === "all" ? "" : level,
    search: query,
  })
  const events = data?.pages.flatMap((p) => p.events) ?? []

  return (
    <>
      <PageHeader title={t("events.title")} description={t("events.desc")} />
      <div className="mb-4 flex flex-col gap-2 sm:flex-row">
        <form
          className="relative flex-1"
          onSubmit={(e) => {
            e.preventDefault()
            setQuery(search.trim())
          }}
        >
          <SearchIcon className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            className="pl-8"
            placeholder={t("common.search")}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            onBlur={() => setQuery(search.trim())}
          />
        </form>
        <Select value={type} onValueChange={setType}>
          <SelectTrigger className="sm:w-64">
            <SelectValue placeholder={t("events.type")} />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t("common.all")}</SelectItem>
            {eventTypes.map((et) => (
              <SelectItem key={et} value={et}>
                {t(`evtype.${et}` as TKey)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select value={level} onValueChange={setLevel}>
          <SelectTrigger className="sm:w-40">
            <SelectValue placeholder={t("events.level")} />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t("common.all")}</SelectItem>
            {(["info", "warn", "error"] as const).map((l) => (
              <SelectItem key={l} value={l}>
                {t(`events.level.${l}`)}
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
                <TableHead className="w-40 pl-4">{t("events.time")}</TableHead>
                <TableHead>{t("events.event")}</TableHead>
                <TableHead className="hidden pr-4 md:table-cell">{t("events.details")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {isLoading && (
                <TableRow>
                  <TableCell colSpan={3} className="py-10 text-center">
                    <Loader2Icon className="mx-auto size-5 animate-spin text-muted-foreground" />
                  </TableCell>
                </TableRow>
              )}
              {!isLoading && events.length === 0 && (
                <TableRow>
                  <TableCell colSpan={3} className="py-10 text-center text-muted-foreground">
                    {t("events.empty")}
                  </TableCell>
                </TableRow>
              )}
              {events.map((e) => {
                const { title, details } = text(e)
                return (
                  <TableRow key={e.id}>
                    <TableCell className="pl-4 align-top text-xs whitespace-nowrap text-muted-foreground tabular-nums">
                      {formatDateTime(new Date(e.time), lang)}
                    </TableCell>
                    <TableCell className="align-top whitespace-normal">
                      <div className="flex items-start gap-2">
                        <span className="mt-1.5">
                          <StatusDot color={levelColor[e.level] ?? "gray"} />
                        </span>
                        <div className="min-w-0">
                          <div className="text-sm">{title}</div>
                          {details && <div className="mt-0.5 text-xs break-all text-muted-foreground md:hidden">{details}</div>}
                        </div>
                      </div>
                    </TableCell>
                    <TableCell className="hidden max-w-md pr-4 align-top text-xs break-all whitespace-normal text-muted-foreground md:table-cell">
                      {details}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
      {hasNextPage && (
        <div className="mt-4 flex justify-center">
          <Button variant="outline" onClick={() => fetchNextPage()} disabled={isFetchingNextPage}>
            {isFetchingNextPage && <Loader2Icon className="animate-spin" />}
            {t("common.loadMore")}
          </Button>
        </div>
      )}
    </>
  )
}
