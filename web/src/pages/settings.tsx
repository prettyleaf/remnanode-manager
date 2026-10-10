import { useMemo, useState, type ReactNode } from "react"
import { useSearchParams } from "react-router"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { CheckCircle2Icon, Loader2Icon, PlusIcon, RotateCcwIcon, SendIcon, Trash2Icon, XCircleIcon } from "lucide-react"

import { PageHeader } from "@/components/app-shell"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import { api, put, post } from "@/lib/api"
import { useI18n, type TKey } from "@/lib/i18n"
import { useAuthState, useSettings } from "@/lib/queries"
import type { PanelInfo, Settings, SettingsResponse, SubscriptionTemplate } from "@/lib/types"

const placeholders: Record<string, string[]> = {
  node_down: ["node", "node_address", "message", "hosts_count", "down_minutes"],
  node_up: ["node", "node_address", "downtime"],
  hosts_disabled: ["hosts", "count", "down_minutes", "dry_run"],
  hosts_enabled: ["hosts", "count", "dry_run"],
  mihomo_updated: ["template", "action", "host"],
  panel_unreachable: ["error"],
  panel_restored: ["version"],
  guard_triggered: ["down_nodes", "total_nodes", "percent"],
  error: ["error"],
}

function Field({ label, hint, htmlFor, children }: { label: string; hint?: string; htmlFor?: string; children: ReactNode }) {
  return (
    <div className="grid content-start gap-2">
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  )
}

function SwitchRow({ label, hint, checked, onChange, id }: { label: string; hint?: string; checked: boolean; onChange: (v: boolean) => void; id: string }) {
  return (
    <div className="flex items-start justify-between gap-4">
      <div className="grid gap-1">
        <Label htmlFor={id}>{label}</Label>
        {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
      </div>
      <Switch id={id} checked={checked} onCheckedChange={onChange} />
    </div>
  )
}

function NumberInput({ value, onChange, min, max, id }: { value: number; onChange: (v: number) => void; min: number; max: number; id: string }) {
  return (
    <Input
      id={id}
      type="number"
      inputMode="numeric"
      min={min}
      max={max}
      value={Number.isFinite(value) ? value : ""}
      onChange={(e) => onChange(e.target.value === "" ? NaN : Number(e.target.value))}
      className="max-w-40"
    />
  )
}

type Draft = Settings & { headerRows: [string, string][]; chatIdsText: string }

function toDraft(s: Settings): Draft {
  return {
    ...structuredClone(s),
    panel: { ...s.panel, token: "" },
    telegram: { ...structuredClone(s.telegram), botToken: "" },
    headerRows: Object.entries(s.panel.headers ?? {}),
    chatIdsText: (s.telegram.chatIds ?? []).join(", "),
  }
}

function fromDraft(d: Draft): Settings {
  const { headerRows, chatIdsText, ...rest } = d
  return {
    ...rest,
    panel: { ...rest.panel, headers: Object.fromEntries(headerRows.filter(([k]) => k.trim() !== "")) },
    telegram: {
      ...rest.telegram,
      chatIds: chatIdsText
        .split(/[\s,;]+/)
        .map((v) => v.trim())
        .filter(Boolean),
    },
  }
}

export function SettingsPage() {
  const { t } = useI18n()
  const { data, dataUpdatedAt } = useSettings()
  const [params, setParams] = useSearchParams()
  const tab = params.get("tab") ?? "panel"
  const setTab = (v: string) => setParams(v === "panel" ? {} : { tab: v }, { replace: true })

  return (
    <>
      <PageHeader title={t("settings.title")} />
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList className="mb-4 h-auto flex-wrap">
          <TabsTrigger value="panel">{t("settings.tab.panel")}</TabsTrigger>
          <TabsTrigger value="checker">{t("settings.tab.checker")}</TabsTrigger>
          <TabsTrigger value="mihomo">{t("settings.tab.mihomo")}</TabsTrigger>
          <TabsTrigger value="telegram">{t("settings.tab.telegram")}</TabsTrigger>
          <TabsTrigger value="account">{t("settings.tab.account")}</TabsTrigger>
        </TabsList>
        {!data ? (
          <Skeleton className="h-96" />
        ) : (
          // Remount on fresh data so the draft starts from the saved state.
          <SettingsForm key={dataUpdatedAt} data={data} tab={tab} />
        )}
        <TabsContent value="account">
          <AccountCard />
        </TabsContent>
      </Tabs>
    </>
  )
}

function SettingsForm({ data, tab }: { data: SettingsResponse; tab: string }) {
  const { t } = useI18n()
  const qc = useQueryClient()
  const [draft, setDraft] = useState<Draft>(() => toDraft(data.settings))
  const baseline = useMemo(() => JSON.stringify(toDraft(data.settings)), [data])
  const dirty = JSON.stringify(draft) !== baseline

  const update = (fn: (d: Draft) => void) =>
    setDraft((prev) => {
      const next = structuredClone(prev)
      fn(next)
      return next
    })

  const save = useMutation({
    mutationFn: () => put<SettingsResponse>("/settings", fromDraft(draft)),
    onSuccess: (res) => {
      qc.setQueryData(["settings"], res)
      qc.invalidateQueries({ queryKey: ["status"] })
      toast.success(t("common.saved"))
    },
    onError: (e: Error) => toast.error(e.message),
  })

  if (tab === "account") return null

  return (
    <div className="pb-20">
      <TabsContent value="panel">
        <PanelCard draft={draft} update={update} tokenSaved={data.secrets.panelToken} />
      </TabsContent>
      <TabsContent value="checker">
        <CheckerCard draft={draft} update={update} />
      </TabsContent>
      <TabsContent value="mihomo">
        <MihomoCard draft={draft} update={update} types={data.templateTypes} />
      </TabsContent>
      <TabsContent value="telegram">
        <TelegramCard
          draft={draft}
          update={update}
          tokenSaved={data.secrets.botToken}
          eventTypes={data.eventTypes}
          defaults={data.defaultTemplates}
        />
      </TabsContent>

      {dirty && (
        <div className="fixed inset-x-0 bottom-0 z-30 border-t bg-background/95 backdrop-blur md:left-60">
          <div className="mx-auto flex max-w-6xl items-center justify-between gap-3 px-4 py-3 md:px-6">
            <span className="text-sm text-muted-foreground">{t("common.unsaved")}</span>
            <div className="flex gap-2">
              <Button variant="ghost" onClick={() => setDraft(toDraft(data.settings))}>
                {t("common.discard")}
              </Button>
              <Button onClick={() => save.mutate()} disabled={save.isPending}>
                {save.isPending && <Loader2Icon className="animate-spin" />}
                {save.isPending ? t("common.saving") : t("common.save")}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

type CardProps = { draft: Draft; update: (fn: (d: Draft) => void) => void }

function PanelCard({ draft, update, tokenSaved }: CardProps & { tokenSaved: boolean }) {
  const { t } = useI18n()
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null)
  const test = useMutation({
    mutationFn: () => post<PanelInfo>("/settings/test-panel", { panel: fromDraft(draft).panel }),
    onSuccess: (info) =>
      setResult({
        ok: true,
        text:
          t("panel.testOk", { version: info.version, nodes: info.nodes, hosts: info.hosts, templates: info.templates }) +
          (info.templatesError ? ` (${info.templatesError})` : ""),
      }),
    onError: (e: Error) => setResult({ ok: false, text: e.message }),
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("panel.title")}</CardTitle>
        <CardDescription>{t("panel.desc")}</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-5">
        <Field label={t("panel.url")} htmlFor="panel-url">
          <Input
            id="panel-url"
            placeholder="https://panel.example.com"
            value={draft.panel.url}
            onChange={(e) => update((d) => void (d.panel.url = e.target.value))}
          />
        </Field>
        <Field label={t("panel.token")} hint={t("panel.tokenHint")} htmlFor="panel-token">
          <Input
            id="panel-token"
            type="password"
            autoComplete="off"
            placeholder={tokenSaved ? t("panel.tokenSaved") : "eyJhbGciOi…"}
            value={draft.panel.token ?? ""}
            onChange={(e) => update((d) => void (d.panel.token = e.target.value))}
          />
        </Field>
        <div className="grid gap-2">
          <Label>{t("panel.headers")}</Label>
          {draft.headerRows.map(([k, v], i) => (
            <div key={i} className="flex gap-2">
              <Input
                placeholder={t("panel.headerName")}
                value={k}
                onChange={(e) => update((d) => void (d.headerRows[i][0] = e.target.value))}
                className="max-w-56"
              />
              <Input
                placeholder={t("panel.headerValue")}
                value={v}
                onChange={(e) => update((d) => void (d.headerRows[i][1] = e.target.value))}
              />
              <Button variant="ghost" size="icon" onClick={() => update((d) => void d.headerRows.splice(i, 1))}>
                <Trash2Icon />
              </Button>
            </div>
          ))}
          <div>
            <Button variant="outline" size="sm" onClick={() => update((d) => void d.headerRows.push(["", ""]))}>
              <PlusIcon /> {t("common.add")}
            </Button>
          </div>
          <p className="text-xs text-muted-foreground">{t("panel.headersHint")}</p>
        </div>
        <SwitchRow
          id="panel-insecure"
          label={t("panel.insecure")}
          hint={t("panel.insecureHint")}
          checked={draft.panel.insecureTls}
          onChange={(v) => update((d) => void (d.panel.insecureTls = v))}
        />
        <Separator />
        <div className="flex flex-col items-start gap-3">
          <Button variant="outline" onClick={() => test.mutate()} disabled={test.isPending || !draft.panel.url}>
            {test.isPending && <Loader2Icon className="animate-spin" />}
            {t("common.test")}
          </Button>
          {result && (
            <Alert variant={result.ok ? "default" : "destructive"}>
              {result.ok ? <CheckCircle2Icon className="text-emerald-500" /> : <XCircleIcon />}
              <AlertDescription className="break-all">{result.text}</AlertDescription>
            </Alert>
          )}
        </div>
      </CardContent>
    </Card>
  )
}

function CheckerCard({ draft, update }: CardProps) {
  const { t } = useI18n()
  const c = draft.checker
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("checker.title")}</CardTitle>
        <CardDescription>{t("checker.desc")}</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-5">
        <SwitchRow
          id="checker-enabled"
          label={t("checker.enabled")}
          hint={t("checker.enabledHint")}
          checked={c.enabled}
          onChange={(v) => update((d) => void (d.checker.enabled = v))}
        />
        <Separator />
        <div className="grid gap-5 sm:grid-cols-2">
          <Field label={t("checker.down")} hint={t("checker.downHint")} htmlFor="checker-down">
            <NumberInput id="checker-down" min={1} max={1440} value={c.downMinutes} onChange={(v) => update((d) => void (d.checker.downMinutes = v))} />
          </Field>
          <Field label={t("checker.recovery")} hint={t("checker.recoveryHint")} htmlFor="checker-recovery">
            <NumberInput
              id="checker-recovery"
              min={0}
              max={1440}
              value={c.recoveryMinutes}
              onChange={(v) => update((d) => void (d.checker.recoveryMinutes = v))}
            />
          </Field>
          <Field label={t("checker.interval")} htmlFor="checker-interval">
            <NumberInput
              id="checker-interval"
              min={10}
              max={3600}
              value={c.intervalSeconds}
              onChange={(v) => update((d) => void (d.checker.intervalSeconds = v))}
            />
          </Field>
          <Field label={t("checker.policy")}>
            <Select value={c.policy} onValueChange={(v) => update((d) => void (d.checker.policy = v as "all" | "any"))}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">{t("checker.policy.all")}</SelectItem>
                <SelectItem value="any">{t("checker.policy.any")}</SelectItem>
              </SelectContent>
            </Select>
          </Field>
        </div>
        <Separator />
        <SwitchRow
          id="checker-autoenable"
          label={t("checker.autoEnable")}
          hint={t("checker.autoEnableHint")}
          checked={c.autoEnable}
          onChange={(v) => update((d) => void (d.checker.autoEnable = v))}
        />
        <SwitchRow
          id="checker-disabled-down"
          label={t("checker.disabledAsDown")}
          hint={t("checker.disabledAsDownHint")}
          checked={c.treatDisabledNodeAsDown}
          onChange={(v) => update((d) => void (d.checker.treatDisabledNodeAsDown = v))}
        />
        <SwitchRow
          id="checker-dryrun"
          label={t("checker.dryRun")}
          hint={t("checker.dryRunHint")}
          checked={c.dryRun}
          onChange={(v) => update((d) => void (d.checker.dryRun = v))}
        />
        <Separator />
        <div className="grid gap-5 sm:grid-cols-2">
          <Field label={t("checker.guard")} hint={t("checker.guardHint")} htmlFor="checker-guard">
            <NumberInput
              id="checker-guard"
              min={0}
              max={100}
              value={c.massDownGuardPercent}
              onChange={(v) => update((d) => void (d.checker.massDownGuardPercent = v))}
            />
          </Field>
          <Field label={t("checker.retention")} htmlFor="checker-retention">
            <NumberInput
              id="checker-retention"
              min={1}
              max={3650}
              value={draft.general.eventRetentionDays}
              onChange={(v) => update((d) => void (d.general.eventRetentionDays = v))}
            />
          </Field>
        </div>
      </CardContent>
    </Card>
  )
}

function MihomoCard({ draft, update, types }: CardProps & { types: string[] }) {
  const { t } = useI18n()
  const m = draft.mihomo
  const templates = useQuery({
    queryKey: ["templates"],
    queryFn: () => api<{ templates: SubscriptionTemplate[] }>("/templates"),
    enabled: m.enabled,
    retry: false,
  })
  const toggle = (list: string[], v: string, on: boolean) => (on ? [...list, v] : list.filter((x) => x !== v))

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("mihomo.title")}</CardTitle>
        <CardDescription className="leading-relaxed">{t("mihomo.desc")}</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-5">
        <SwitchRow id="mihomo-enabled" label={t("mihomo.enabled")} checked={m.enabled} onChange={(v) => update((d) => void (d.mihomo.enabled = v))} />
        {m.enabled && (
          <>
            <Separator />
            <div className="grid gap-2">
              <Label>{t("mihomo.types")}</Label>
              <div className="flex flex-wrap gap-4">
                {types.map((type) => (
                  <label key={type} className="flex items-center gap-2 text-sm">
                    <Checkbox
                      checked={m.templateTypes.includes(type)}
                      onCheckedChange={(v) => update((d) => void (d.mihomo.templateTypes = toggle(d.mihomo.templateTypes, type, v === true)))}
                    />
                    {type}
                  </label>
                ))}
              </div>
            </div>
            <div className="grid gap-2">
              <Label>{t("mihomo.templates")}</Label>
              {templates.isLoading && <Skeleton className="h-16" />}
              {templates.error && (
                <p className="text-sm text-destructive">{t("mihomo.templatesError", { error: (templates.error as Error).message })}</p>
              )}
              <div className="grid gap-2 sm:grid-cols-2">
                {templates.data?.templates
                  .filter((tpl) => m.templateTypes.includes(tpl.templateType))
                  .map((tpl) => (
                    <label key={tpl.uuid} className="flex items-center gap-2 rounded-md border px-3 py-2 text-sm">
                      <Checkbox
                        checked={m.templateUuids.includes(tpl.uuid)}
                        onCheckedChange={(v) =>
                          update((d) => void (d.mihomo.templateUuids = toggle(d.mihomo.templateUuids, tpl.uuid, v === true)))
                        }
                      />
                      <span className="flex-1 truncate">{tpl.name}</span>
                      <Badge variant="secondary">{tpl.templateType}</Badge>
                    </label>
                  ))}
              </div>
              <p className="text-xs text-muted-foreground">{t("mihomo.templatesHint")}</p>
            </div>
            <Separator />
            <SwitchRow
              id="mihomo-rules"
              label={t("mihomo.cleanRules")}
              hint={t("mihomo.cleanRulesHint")}
              checked={m.cleanRules}
              onChange={(v) => update((d) => void (d.mihomo.cleanRules = v))}
            />
            <Field label={t("mihomo.fallback")} hint={t("mihomo.fallbackHint")} htmlFor="mihomo-fallback">
              <Input
                id="mihomo-fallback"
                className="max-w-56 font-mono"
                value={m.emptyGroupFallback}
                onChange={(e) => update((d) => void (d.mihomo.emptyGroupFallback = e.target.value))}
              />
            </Field>
          </>
        )}
      </CardContent>
    </Card>
  )
}

function TelegramCard({
  draft,
  update,
  tokenSaved,
  eventTypes,
  defaults,
}: CardProps & { tokenSaved: boolean; eventTypes: string[]; defaults: Record<string, string> }) {
  const { t } = useI18n()
  const tg = draft.telegram
  const test = useMutation({
    mutationFn: () => post("/settings/test-telegram", { telegram: fromDraft(draft).telegram }),
    onSuccess: () => toast.success(t("tg.testSent")),
    onError: (e: Error) => toast.error(e.message),
  })

  return (
    <div className="grid gap-4">
      <Card>
        <CardHeader>
          <CardTitle>{t("tg.title")}</CardTitle>
          <CardDescription>{t("tg.desc")}</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-5">
          <SwitchRow id="tg-enabled" label={t("tg.enabled")} checked={tg.enabled} onChange={(v) => update((d) => void (d.telegram.enabled = v))} />
          <Separator />
          <Field label={t("tg.token")} htmlFor="tg-token">
            <Input
              id="tg-token"
              type="password"
              autoComplete="off"
              placeholder={tokenSaved ? t("tg.tokenSaved") : "123456:ABC-DEF…"}
              value={tg.botToken ?? ""}
              onChange={(e) => update((d) => void (d.telegram.botToken = e.target.value))}
            />
          </Field>
          <div className="grid gap-5 sm:grid-cols-2">
            <Field label={t("tg.chats")} hint={t("tg.chatsHint")} htmlFor="tg-chats">
              <Input
                id="tg-chats"
                placeholder="-1001234567890"
                value={draft.chatIdsText}
                onChange={(e) => update((d) => void (d.chatIdsText = e.target.value))}
              />
            </Field>
            <Field label={t("tg.thread")} hint={t("tg.threadHint")} htmlFor="tg-thread">
              <Input id="tg-thread" inputMode="numeric" value={tg.threadId} onChange={(e) => update((d) => void (d.telegram.threadId = e.target.value))} />
            </Field>
            <Field label={t("tg.apiUrl")} hint={t("tg.apiUrlHint")} htmlFor="tg-api">
              <Input id="tg-api" value={tg.apiUrl} onChange={(e) => update((d) => void (d.telegram.apiUrl = e.target.value))} />
            </Field>
            <Field label={t("tg.proxy")} hint={t("tg.proxyHint")} htmlFor="tg-proxy">
              <Input
                id="tg-proxy"
                placeholder="socks5://127.0.0.1:1080"
                value={tg.proxy}
                onChange={(e) => update((d) => void (d.telegram.proxy = e.target.value))}
              />
            </Field>
          </div>
          <SwitchRow id="tg-silent" label={t("tg.silent")} checked={tg.silent} onChange={(v) => update((d) => void (d.telegram.silent = v))} />
          <Separator />
          <div>
            <Button variant="outline" onClick={() => test.mutate()} disabled={test.isPending}>
              {test.isPending ? <Loader2Icon className="animate-spin" /> : <SendIcon />}
              {t("tg.sendTest")}
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t("tg.events")}</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-4">
          {eventTypes.map((type) => {
            const ev = tg.events[type] ?? { enabled: false, template: defaults[type] ?? "" }
            return (
              <div key={type} className="rounded-lg border p-4">
                <div className="flex items-center justify-between gap-3">
                  <Label htmlFor={`tg-ev-${type}`} className="text-sm font-medium">
                    {t(`tg.event.${type}` as TKey)}
                  </Label>
                  <Switch
                    id={`tg-ev-${type}`}
                    checked={ev.enabled}
                    onCheckedChange={(v) => update((d) => void (d.telegram.events[type] = { ...ev, enabled: v }))}
                  />
                </div>
                {ev.enabled && (
                  <div className="mt-3 grid gap-2">
                    <Textarea
                      rows={4}
                      className="font-mono text-xs"
                      value={ev.template}
                      onChange={(e) => update((d) => void (d.telegram.events[type] = { ...ev, template: e.target.value }))}
                    />
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span className="text-xs text-muted-foreground">{t("tg.placeholders")}:</span>
                      {(placeholders[type] ?? []).map((p) => (
                        <Badge key={p} variant="secondary" className="font-mono text-[10px]">{`{${p}}`}</Badge>
                      ))}
                      <div className="flex-1" />
                      {ev.template !== defaults[type] && (
                        <Button
                          variant="ghost"
                          size="xs"
                          onClick={() => update((d) => void (d.telegram.events[type] = { ...ev, template: defaults[type] }))}
                        >
                          <RotateCcwIcon /> {t("tg.resetTemplate")}
                        </Button>
                      )}
                    </div>
                  </div>
                )}
              </div>
            )
          })}
        </CardContent>
      </Card>
    </div>
  )
}

function AccountCard() {
  const { t } = useI18n()
  const { data: auth } = useAuthState()
  const qc = useQueryClient()
  const [username, setUsername] = useState(auth?.username ?? "")
  const [current, setCurrent] = useState("")
  const [next, setNext] = useState("")
  const [confirm, setConfirm] = useState("")
  const [error, setError] = useState("")

  const save = useMutation({
    mutationFn: () => put<{ username: string }>("/me", { username, currentPassword: current, newPassword: next }),
    onSuccess: () => {
      toast.success(t("account.updated"))
      setCurrent("")
      setNext("")
      setConfirm("")
      qc.invalidateQueries({ queryKey: ["auth"] })
    },
    onError: (e: Error) => setError(e.message),
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("account.title")}</CardTitle>
        <CardDescription>{t("account.desc")}</CardDescription>
      </CardHeader>
      <CardContent>
        <form
          className="grid max-w-md gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            setError("")
            if (next && next.length < 8) return setError(t("auth.tooShort"))
            if (next !== confirm) return setError(t("auth.mismatch"))
            save.mutate()
          }}
        >
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <Field label={t("account.username")} htmlFor="acc-username">
            <Input id="acc-username" autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} />
          </Field>
          <Field label={t("account.current")} htmlFor="acc-current">
            <Input id="acc-current" type="password" autoComplete="current-password" required value={current} onChange={(e) => setCurrent(e.target.value)} />
          </Field>
          <Field label={t("account.new")} hint={t("account.newHint")} htmlFor="acc-new">
            <Input id="acc-new" type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
          </Field>
          <Field label={t("account.confirm")} htmlFor="acc-confirm">
            <Input id="acc-confirm" type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
          </Field>
          <div>
            <Button type="submit" disabled={save.isPending}>
              {save.isPending && <Loader2Icon className="animate-spin" />}
              {t("common.save")}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}
