import { useState, type FormEvent } from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { ActivityIcon, Loader2Icon } from "lucide-react"

import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { LanguageSwitcher, ThemeSwitcher } from "@/components/preferences"
import { post } from "@/lib/api"
import { useI18n } from "@/lib/i18n"

export function LoginPage({ setup }: { setup: boolean }) {
  const { t } = useI18n()
  const qc = useQueryClient()
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")
  const [error, setError] = useState("")

  const submit = useMutation({
    mutationFn: () => post(setup ? "/auth/setup" : "/auth/login", { username, password }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["auth"] }),
    onError: (e: Error) => setError(e.message),
  })

  const onSubmit = (e: FormEvent) => {
    e.preventDefault()
    setError("")
    if (setup) {
      if (password.length < 8) return setError(t("auth.tooShort"))
      if (password !== confirm) return setError(t("auth.mismatch"))
    }
    submit.mutate()
  }

  return (
    <div className="flex min-h-svh flex-col items-center justify-center bg-muted/40 p-4">
      <div className="mb-6 flex items-center gap-2.5">
        <div className="flex size-9 items-center justify-center rounded-lg bg-primary text-primary-foreground">
          <ActivityIcon className="size-5" />
        </div>
        <span className="text-lg font-semibold">{t("app.name")}</span>
      </div>
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>{setup ? t("auth.setup") : t("auth.login")}</CardTitle>
          <CardDescription>{setup ? t("auth.setupDesc") : t("auth.loginDesc")}</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={onSubmit} className="flex flex-col gap-4">
            {error && (
              <Alert variant="destructive">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
            <div className="grid gap-2">
              <Label htmlFor="username">{t("auth.username")}</Label>
              <Input
                id="username"
                autoComplete="username"
                autoFocus
                required
                value={username}
                onChange={(e) => setUsername(e.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="password">{t("auth.password")}</Label>
              <Input
                id="password"
                type="password"
                autoComplete={setup ? "new-password" : "current-password"}
                required
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </div>
            {setup && (
              <div className="grid gap-2">
                <Label htmlFor="confirm">{t("auth.passwordConfirm")}</Label>
                <Input
                  id="confirm"
                  type="password"
                  autoComplete="new-password"
                  required
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                />
              </div>
            )}
            <Button type="submit" className="w-full" disabled={submit.isPending}>
              {submit.isPending && <Loader2Icon className="animate-spin" />}
              {setup ? t("auth.create") : t("auth.signIn")}
            </Button>
          </form>
        </CardContent>
      </Card>
      <div className="mt-4 flex items-center gap-2">
        <LanguageSwitcher />
        <ThemeSwitcher />
      </div>
    </div>
  )
}
