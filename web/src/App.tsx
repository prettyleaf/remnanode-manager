import { useEffect } from "react"
import { BrowserRouter, Navigate, Route, Routes } from "react-router"
import { useQueryClient } from "@tanstack/react-query"
import { Loader2Icon } from "lucide-react"

import { AppShell } from "@/components/app-shell"
import { UNAUTHORIZED_EVENT } from "@/lib/api"
import { useAuthState } from "@/lib/queries"
import { DashboardPage } from "@/pages/dashboard"
import { EventsPage } from "@/pages/events"
import { HostsPage } from "@/pages/hosts"
import { LoginPage } from "@/pages/login"
import { NodesPage } from "@/pages/nodes"
import { SettingsPage } from "@/pages/settings"

export default function App() {
  const qc = useQueryClient()
  const { data, isLoading, error } = useAuthState()

  useEffect(() => {
    const onUnauthorized = () => qc.invalidateQueries({ queryKey: ["auth"] })
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
  }, [qc])

  if (isLoading) {
    return (
      <div className="flex min-h-svh items-center justify-center">
        <Loader2Icon className="size-6 animate-spin text-muted-foreground" />
      </div>
    )
  }
  if (error || !data) {
    return <div className="flex min-h-svh items-center justify-center p-4 text-sm text-destructive">{(error as Error)?.message}</div>
  }
  if (!data.authenticated) {
    return <LoginPage setup={data.setupRequired} />
  }

  return (
    <BrowserRouter>
      <Routes>
        <Route element={<AppShell />}>
          <Route index element={<DashboardPage />} />
          <Route path="hosts" element={<HostsPage />} />
          <Route path="nodes" element={<NodesPage />} />
          <Route path="events" element={<EventsPage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}
