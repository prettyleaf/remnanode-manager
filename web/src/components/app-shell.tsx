import { useState, type ReactNode } from "react"
import { NavLink, Outlet, useNavigate } from "react-router"
import { useQueryClient, useMutation } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  ActivityIcon,
  GlobeIcon,
  LayoutDashboardIcon,
  LogOutIcon,
  MenuIcon,
  RefreshCwIcon,
  ScrollTextIcon,
  ServerIcon,
  SettingsIcon,
  UserCogIcon,
  UserIcon,
} from "lucide-react"

import { VersionChip } from "@/components/build-info"
import { LanguageSwitcher, ThemeSwitcher } from "@/components/preferences"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "@/components/ui/sheet"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { StatusDot } from "@/components/status"
import { post } from "@/lib/api"
import { useI18n, type TKey } from "@/lib/i18n"
import { useAuthState, useStatus } from "@/lib/queries"
import { cn } from "@/lib/utils"

const nav: { to: string; label: TKey; icon: typeof ActivityIcon }[] = [
  { to: "/", label: "nav.dashboard", icon: LayoutDashboardIcon },
  { to: "/hosts", label: "nav.hosts", icon: GlobeIcon },
  { to: "/nodes", label: "nav.nodes", icon: ServerIcon },
  { to: "/events", label: "nav.events", icon: ScrollTextIcon },
  { to: "/settings", label: "nav.settings", icon: SettingsIcon },
]

function Brand() {
  const { t } = useI18n()
  return (
    <div className="flex items-center gap-2.5 px-2">
      <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary text-primary-foreground">
        <ActivityIcon className="size-4" />
      </div>
      <div className="min-w-0 leading-tight">
        <div className="truncate text-sm font-semibold">{t("app.name")}</div>
        <div className="truncate text-xs text-muted-foreground">{t("app.tagline")}</div>
      </div>
    </div>
  )
}

function NavItems({ onNavigate }: { onNavigate?: () => void }) {
  const { t } = useI18n()
  const { data } = useStatus()
  const managed = data?.snapshot.stats.hostsManaged ?? 0
  const atRisk = data?.snapshot.stats.hostsAtRisk ?? 0
  return (
    <nav className="flex flex-col gap-0.5">
      {nav.map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          end={item.to === "/"}
          onClick={onNavigate}
          className={({ isActive }) =>
            cn(
              "flex items-center gap-2.5 rounded-md px-2.5 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
              isActive && "bg-muted font-medium text-foreground",
            )
          }
        >
          <item.icon className="size-4" />
          <span className="flex-1">{t(item.label)}</span>
          {item.to === "/hosts" && (managed > 0 || atRisk > 0) && (
            <span
              className={cn(
                "rounded-full px-1.5 text-[11px] font-medium tabular-nums",
                managed > 0 ? "bg-red-500/15 text-red-600 dark:text-red-400" : "bg-amber-500/15 text-amber-700 dark:text-amber-400",
              )}
            >
              {managed > 0 ? managed : atRisk}
            </span>
          )}
        </NavLink>
      ))}
    </nav>
  )
}

function UserMenu() {
  const { t } = useI18n()
  const navigate = useNavigate()
  const { data: auth } = useAuthState()
  const qc = useQueryClient()
  const logout = useMutation({
    mutationFn: () => post("/auth/logout"),
    onSettled: () => {
      qc.clear()
      qc.invalidateQueries({ queryKey: ["auth"] })
    },
  })

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" className="h-auto w-full justify-start gap-2.5 px-2 py-1.5">
          <div className="flex size-7 items-center justify-center rounded-full bg-muted">
            <UserIcon className="size-3.5" />
          </div>
          <span className="truncate text-sm">{auth?.username}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" side="top" className="w-56">
        <DropdownMenuItem onSelect={() => navigate("/settings?tab=account")}>
          <UserCogIcon /> {t("header.account")}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => logout.mutate()} variant="destructive">
          <LogOutIcon /> {t("header.logout")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function PanelStatus() {
  const { t } = useI18n()
  const { data } = useStatus()
  if (!data) return null
  const snap = data.snapshot
  let color: "green" | "red" | "gray" = "gray"
  let label = t("header.notConfigured")
  if (data.checker.configured) {
    if (snap.panelOk) {
      color = "green"
      label = t("header.panelOk", { version: snap.panelVersion ? `v${snap.panelVersion}` : "" }).trim()
    } else if (snap.panelErrorCode) {
      color = "red"
      label = t("header.panelDown")
    }
  }
  const pill = (
    <div className="hidden h-7 items-center gap-2 rounded-full border px-2.5 text-xs whitespace-nowrap text-muted-foreground lg:flex">
      <StatusDot color={color} pulse={color === "red"} />
      {label}
    </div>
  )
  if (!snap.panelError) return pill
  return (
    <Tooltip>
      <TooltipTrigger asChild>{pill}</TooltipTrigger>
      <TooltipContent className="max-w-sm break-words">{snap.panelError}</TooltipContent>
    </Tooltip>
  )
}

function CheckNowButton() {
  const { t } = useI18n()
  const qc = useQueryClient()
  const check = useMutation({
    mutationFn: () => post("/check"),
    onSuccess: () => {
      toast.success(t("header.checkQueued"))
      window.setTimeout(() => qc.invalidateQueries({ queryKey: ["status"] }), 1500)
    },
    onError: (e: Error) => toast.error(e.message),
  })
  return (
    <Button variant="outline" size="sm" onClick={() => check.mutate()} disabled={check.isPending}>
      <RefreshCwIcon className={cn(check.isPending && "animate-spin")} />
      <span className="hidden sm:inline">{t("header.checkNow")}</span>
    </Button>
  )
}

function MobileTitle() {
  const { t } = useI18n()
  return <span className="truncate text-sm font-semibold md:hidden">{t("app.name")}</span>
}

function Sidebar({ onNavigate, mobile }: { onNavigate?: () => void; mobile?: boolean }) {
  return (
    <div className="flex h-full flex-col gap-6 p-3">
      <div className="pt-1">
        <Brand />
      </div>
      <div className="flex-1">
        <NavItems onNavigate={onNavigate} />
      </div>
      <div className="flex flex-col gap-2">
        {mobile && (
          <div className="flex flex-wrap items-center gap-2 px-2">
            <VersionChip />
            <LanguageSwitcher />
          </div>
        )}
        <UserMenu />
      </div>
    </div>
  )
}

export function AppShell() {
  const [open, setOpen] = useState(false)
  return (
    <div className="flex min-h-svh bg-background">
      <aside className="sticky top-0 hidden h-svh w-60 shrink-0 border-r bg-sidebar md:block">
        <Sidebar />
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-20 flex h-14 items-center gap-2 border-b bg-background/80 px-4 backdrop-blur md:px-6">
          <Sheet open={open} onOpenChange={setOpen}>
            <SheetTrigger asChild>
              <Button variant="ghost" size="icon" className="md:hidden">
                <MenuIcon />
              </Button>
            </SheetTrigger>
            <SheetContent side="left" className="w-64 p-0">
              <SheetTitle className="sr-only">Menu</SheetTitle>
              <Sidebar mobile onNavigate={() => setOpen(false)} />
            </SheetContent>
          </Sheet>
          <MobileTitle />
          <div className="flex-1" />
          <PanelStatus />
          <VersionChip className="hidden sm:inline-flex" />
          <LanguageSwitcher className="hidden sm:inline-flex" />
          <ThemeSwitcher />
          <CheckNowButton />
        </header>
        <main className="mx-auto w-full max-w-6xl flex-1 px-4 py-6 md:px-6">
          <Outlet />
        </main>
      </div>
    </div>
  )
}

export function PageHeader({
  title,
  description,
  actions,
  className,
}: {
  title: string
  description?: string
  actions?: ReactNode
  className?: string
}) {
  return (
    <div className={cn("mb-6 flex flex-wrap items-end justify-between gap-3", className)}>
      <div className="min-w-0">
        <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
        {description && <p className="mt-1 max-w-2xl text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions}
    </div>
  )
}
