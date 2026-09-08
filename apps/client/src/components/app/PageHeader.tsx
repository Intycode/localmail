import * as React from "react"

interface PageHeaderProps {
  title: string
  description?: string
  actions?: React.ReactNode
}

/** Header for page-mode routes (Accounts, Settings): title + description + actions. */
export function PageHeader({ title, description, actions }: PageHeaderProps) {
  return (
    <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
      <div className="min-w-0">
        <h2 className="text-xl font-semibold tracking-tight">{title}</h2>
        {description && <p className="mt-1.5 text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
  )
}

/** Centered max-width container for page-mode route content. */
export function PageBody({ children }: { children: React.ReactNode }) {
  return <div className="mx-auto w-full max-w-2xl px-5 py-6">{children}</div>
}
