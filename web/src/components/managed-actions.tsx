import { useMutation, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { EyeOffIcon, PowerIcon } from "lucide-react"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { post } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import type { ManagedHost } from "@/lib/types"

export function ManagedActions({ host }: { host: ManagedHost }) {
  const { t } = useI18n()
  const qc = useQueryClient()
  const done = (msg: string) => {
    toast.success(msg)
    qc.invalidateQueries({ queryKey: ["status"] })
    qc.invalidateQueries({ queryKey: ["events"] })
  }
  const enable = useMutation({
    mutationFn: () => post(`/hosts/${host.uuid}/enable`),
    onSuccess: () => done(t("managed.enabled")),
    onError: (e: Error) => toast.error(e.message),
  })
  const forget = useMutation({
    mutationFn: () => post(`/hosts/${host.uuid}/forget`),
    onSuccess: () => done(t("managed.forgotten")),
    onError: (e: Error) => toast.error(e.message),
  })

  return (
    <div className="flex justify-end gap-1.5">
      {host.state === "disabled" && (
        <AlertDialog>
          <AlertDialogTrigger asChild>
            <Button size="sm" variant="outline" disabled={enable.isPending} title={t("managed.enable")}>
              <PowerIcon />
              <span className="hidden xl:inline">{t("managed.enable")}</span>
            </Button>
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{t("managed.enableTitle")}</AlertDialogTitle>
              <AlertDialogDescription>{t("managed.enableDesc", { host: host.remark })}</AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
              <AlertDialogAction onClick={() => enable.mutate()}>{t("managed.enable")}</AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      )}
      <AlertDialog>
        <AlertDialogTrigger asChild>
          <Button size="sm" variant="ghost" disabled={forget.isPending} title={t("managed.forget")}>
            <EyeOffIcon />
            <span className="hidden xl:inline">{t("managed.forget")}</span>
          </Button>
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("managed.forgetTitle")}</AlertDialogTitle>
            <AlertDialogDescription>{t("managed.forgetDesc", { host: host.remark })}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction onClick={() => forget.mutate()}>{t("managed.forget")}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
