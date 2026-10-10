import { useState, type ReactNode } from "react"
import { toast } from "sonner"
import {
  ActivityIcon,
  ArrowUpCircleIcon,
  BugIcon,
  CalendarIcon,
  CopyIcon,
  CpuIcon,
  GitBranchIcon,
  HashIcon,
  ServerIcon,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Separator } from "@/components/ui/separator"
import { displayVersion, formatDuration, formatFullDateTime, formatRelative, parseTime, useNow } from "@/lib/format"
import { useI18n } from "@/lib/i18n"
import { useAbout } from "@/lib/queries"
import type { AboutResponse } from "@/lib/types"
import { cn } from "@/lib/utils"

const short = (sha: string, n = 7) => sha.slice(0, n)

// Lucide dropped brand icons; this is the GitHub mark from Primer Octicons (MIT).
function GithubIcon() {
  return (
    <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
      <path d="M8 0c4.42 0 8 3.58 8 8a8.013 8.013 0 0 1-5.45 7.59c-.4.08-.55-.17-.55-.38 0-.27.01-1.13.01-2.2 0-.75-.25-1.23-.54-1.48 1.78-.2 3.65-.88 3.65-3.95 0-.88-.31-1.59-.82-2.15.08-.2.36-1.02-.08-2.12 0 0-.67-.22-2.2.82-.64-.18-1.32-.27-2-.27-.68 0-1.36.09-2 .27-1.53-1.03-2.2-.82-2.2-.82-.44 1.1-.16 1.92-.08 2.12-.51.56-.82 1.28-.82 2.15 0 3.06 1.86 3.75 3.64 3.95-.23.2-.44.55-.51 1.07-.46.21-1.61.55-2.33-.66-.15-.24-.6-.83-1.23-.82-.67.01-.27.38.01.53.34.19.73.9.82 1.13.16.45.68 1.31 2.69.94 0 .67.01 1.3.01 1.49 0 .21-.15.45-.55.38A7.995 7.995 0 0 1 0 8c0-4.42 3.58-8 8-8Z" />
    </svg>
  )
}

/** Version chip for the header; turns amber and pulses when an update is out. */
export function VersionChip({ className }: { className?: string }) {
  const { t } = useI18n()
  const { data } = useAbout()
  const [open, setOpen] = useState(false)
  if (!data) return null
  const update = data.update.available

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        title={update ? t("build.updateChip") : t("build.chip")}
        className={cn(
          "inline-flex h-7 items-center gap-1.5 rounded-full border px-2.5 font-mono text-xs whitespace-nowrap text-muted-foreground transition-colors outline-none hover:bg-muted hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 [&_svg]:size-3.5",
          update &&
            "animate-[pulse-ring_2s_ease-out_infinite] border-amber-500/55 motion-reduce:animate-none bg-amber-500/10 text-amber-600 hover:bg-amber-500/15 hover:text-amber-700 dark:text-amber-400 dark:hover:text-amber-300",
          className,
        )}
      >
        {update ? <span className="size-1.5 rounded-full bg-current shadow-[0_0_0_3px] shadow-amber-500/25" /> : <GitBranchIcon />}
        {displayVersion(data.build.version)}
      </button>
      <BuildInfoDialog data={data} open={open} onOpenChange={setOpen} />
    </>
  )
}

function Tile({ icon, title, children }: { icon: ReactNode; title: string; children: ReactNode }) {
  return (
    <div className="grid min-w-0 gap-1.5 rounded-lg border bg-background p-3">
      <div className="flex items-center gap-1.5 text-[11.5px] font-semibold text-muted-foreground [&_svg]:size-3.5">
        {icon}
        {title}
      </div>
      <div className="font-mono text-[13px] break-all">{children}</div>
    </div>
  )
}

const Missing = () => <span className="font-sans text-muted-foreground">—</span>

// The clipboard API needs a secure context; plain-HTTP setups fall back to a selection.
async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    // fall through
  }
  const area = document.createElement("textarea")
  area.value = text
  area.style.cssText = "position:fixed;top:0;left:0;opacity:0"
  document.body.appendChild(area)
  area.select()
  let copied = false
  try {
    copied = document.execCommand("copy")
  } catch {
    copied = false
  }
  area.remove()
  return copied
}

function BuildInfoDialog({ data, open, onOpenChange }: { data: AboutResponse; open: boolean; onOpenChange: (o: boolean) => void }) {
  const { t, lang } = useI18n()
  const { build, update, panelVersion } = data
  const repo = `https://github.com/${build.repository}`
  const built = parseTime(build.buildTime)
  const started = parseTime(build.startedAt)
  const checked = parseTime(update.checkedAt)
  const isRelease = update.channel === "release"
  const ref = build.branch || (isRelease ? "main" : "")
  const now = useNow(30_000)

  const text = [
    `Remnanode Manager ${displayVersion(build.version)} (${[ref, short(build.commit, 12)].filter(Boolean).join(", ")})`,
    `${t("build.built")}: ${build.buildTime || "—"}`,
    `${t("build.runtime")}: ${build.goVersion} ${build.platform}`,
    `${t("build.panel")}: ${panelVersion || "—"}`,
  ].join("\n")

  const latest = isRelease ? displayVersion(update.latest ?? "") : short(update.latest ?? "")
  const current = isRelease ? displayVersion(build.version) : short(build.commit)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2.5">
            <span className="flex size-7 items-center justify-center rounded-md bg-primary text-primary-foreground">
              <ActivityIcon className="size-4" />
            </span>
            {t("build.title")}
          </DialogTitle>
        </DialogHeader>

        <div className="grid gap-3.5 rounded-xl border bg-muted/45 p-3.5">
          <div className="flex flex-wrap items-center gap-2">
            <Badge className="h-[26px] gap-1.5 px-2.5 font-mono text-[12.5px]">
              <ActivityIcon className="size-3.5" />
              {displayVersion(build.version)}
            </Badge>
            {ref && (
              <Badge variant="outline" className="h-[26px] gap-1.5 px-2.5 font-mono text-[12.5px]">
                <GitBranchIcon className="size-3.5" />
                {ref.toUpperCase()}
              </Badge>
            )}
            <Button
              variant="ghost"
              size="icon-sm"
              className="ml-auto"
              title={t("build.copy")}
              aria-label={t("build.copy")}
              onClick={async () => {
                const ok = await copyText(text)
                if (ok) toast.success(t("build.copied"))
                else toast.warning(t("build.copyFailed"))
              }}
            >
              <CopyIcon />
            </Button>
          </div>
          <Separator />
          <div className="grid gap-2.5 sm:grid-cols-2">
            <Tile icon={<CalendarIcon />} title={t("build.built")}>
              {built ? formatFullDateTime(built, lang) : <Missing />}
            </Tile>
            <Tile icon={<HashIcon />} title={t("build.commit")}>
              {build.commit ? (
                <a href={`${repo}/commit/${build.commit}`} target="_blank" rel="noopener noreferrer" title={build.commit} className="hover:underline">
                  {short(build.commit, 12)}
                </a>
              ) : (
                <Missing />
              )}
            </Tile>
          </div>
        </div>

        <div className="grid gap-2.5 sm:grid-cols-2">
          <Tile icon={<CpuIcon />} title={t("build.runtime")}>
            <div>{build.goVersion}</div>
            <div className="text-muted-foreground">
              {build.platform}
              {started && ` · ${t("build.started")} ${formatDuration(now - started.getTime(), lang)}`}
            </div>
          </Tile>
          <Tile icon={<ServerIcon />} title={t("build.panel")}>
            {panelVersion ? `Remnawave ${displayVersion(panelVersion)}` : <span className="font-sans text-muted-foreground">{t("build.notConnected")}</span>}
          </Tile>
        </div>

        {update.available ? (
          <Alert className="border-amber-500/40 bg-amber-500/10 text-amber-800 dark:text-amber-300 [&>svg]:text-amber-600 dark:[&>svg]:text-amber-400">
            <ArrowUpCircleIcon />
            <AlertTitle>{t("build.updateAvailable")}</AlertTitle>
            <AlertDescription className="text-amber-800/90 dark:text-amber-300/90">
              <div className="font-mono">
                {current} → {latest}
              </div>
              {update.url && (
                <Button asChild variant="outline" size="sm" className="mt-2">
                  <a href={update.url} target="_blank" rel="noopener noreferrer" className="no-underline">
                    <ArrowUpCircleIcon />
                    {isRelease ? t("build.openRelease") : t("build.compare")}
                  </a>
                </Button>
              )}
            </AlertDescription>
          </Alert>
        ) : (
          <p className="text-xs text-muted-foreground">
            {!update.enabled
              ? t("build.checkDisabled")
              : update.error
                ? t("build.checkFailed", { error: update.error })
                : checked
                  ? t("build.upToDate", { time: formatRelative(checked, now, lang) })
                  : null}
          </p>
        )}

        <div className="flex flex-wrap gap-2">
          <Button asChild variant="outline" size="sm">
            <a href={`${repo}/issues`} target="_blank" rel="noopener noreferrer">
              <BugIcon /> {t("build.issues")}
            </a>
          </Button>
          <Button asChild variant="outline" size="sm">
            <a
              href={isRelease ? `${repo}/releases/tag/${build.version}` : `${repo}/tree/${ref || "main"}`}
              target="_blank"
              rel="noopener noreferrer"
            >
              <GithubIcon /> {t("build.github")}
            </a>
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
