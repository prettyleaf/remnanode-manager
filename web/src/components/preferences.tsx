import { useTheme } from "next-themes"
import { MonitorIcon, MoonIcon, SunIcon } from "lucide-react"

import { Segmented, SegmentedItem } from "@/components/segmented"
import { useI18n, type Lang, type TKey } from "@/lib/i18n"

const themes: { value: string; icon: typeof SunIcon; label: TKey }[] = [
  { value: "system", icon: MonitorIcon, label: "header.system" },
  { value: "light", icon: SunIcon, label: "header.light" },
  { value: "dark", icon: MoonIcon, label: "header.dark" },
]

/** Auto / light / dark. "Auto" follows prefers-color-scheme live. */
export function ThemeSwitcher({ className }: { className?: string }) {
  const { t } = useI18n()
  const { theme = "system", setTheme } = useTheme()
  return (
    <Segmented label={t("header.theme")} className={className}>
      {themes.map(({ value, icon: Icon, label }) => (
        <SegmentedItem key={value} active={theme === value} onClick={() => setTheme(value)} title={t(label)} aria-label={t(label)}>
          <Icon aria-hidden="true" />
        </SegmentedItem>
      ))}
    </Segmented>
  )
}

const langs: { value: Lang; label: string; title: string }[] = [
  { value: "ru", label: "RU", title: "Русский" },
  { value: "en", label: "EN", title: "English" },
]

export function LanguageSwitcher({ className }: { className?: string }) {
  const { t, lang, setLang } = useI18n()
  return (
    <Segmented label={t("header.language")} className={className}>
      {langs.map((l) => (
        <SegmentedItem key={l.value} active={lang === l.value} onClick={() => setLang(l.value)} title={l.title}>
          {l.label}
        </SegmentedItem>
      ))}
    </Segmented>
  )
}
