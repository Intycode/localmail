import * as React from "react"

interface PageHeaderProps {
  title: string
  description?: string
  actions?: React.ReactNode
}

/** Header for page-mode routes (Accounts, Settings): title + description + actions. */
export function PageHeader({ title, description, actions }: PageHeaderProps) {
  return (
    <div className="mb-7 flex flex-wrap items-end justify-between gap-3">
      <div className="min-w-0">
        <h1 className="text-[22px] font-semibold tracking-tight">{title}</h1>
        {description && <p className="mt-1.5 text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
  )
}

/** Centered max-width container for page-mode route content. */
export function PageBody({ children }: { children: React.ReactNode }) {
  return <div className="mx-auto w-full max-w-4xl px-6 pt-10 pb-12">{children}</div>
}
