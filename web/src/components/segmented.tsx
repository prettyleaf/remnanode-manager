import type { ComponentProps, ReactNode } from "react"

import { cn } from "@/lib/utils"

/** A group of toggle buttons (theme, language), styled like the shadcn tabs list. */
export function Segmented({ label, className, children }: { label: string; className?: string; children: ReactNode }) {
  return (
    <div role="group" aria-label={label} className={cn("inline-flex h-8 shrink-0 items-center rounded-lg bg-muted p-[3px]", className)}>
      {children}
    </div>
  )
}

export function SegmentedItem({ active, className, ...props }: ComponentProps<"button"> & { active: boolean }) {
  return (
    <button
      type="button"
      aria-pressed={active}
      className={cn(
        "inline-flex h-full items-center justify-center gap-1 rounded-md border border-transparent px-2 text-xs font-medium text-muted-foreground transition-[color,background-color,box-shadow] outline-none hover:text-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 [&_svg]:size-3.5",
        "aria-pressed:bg-background aria-pressed:text-foreground aria-pressed:shadow-sm dark:aria-pressed:border-input dark:aria-pressed:bg-input/30",
        className,
      )}
      {...props}
    />
  )
}
