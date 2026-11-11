import * as React from "react"
import { TriangleAlert } from "lucide-react"

import { t } from "@/lib/i18n"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"

/** ErrorBoundary — a render error in a page shows a reload prompt instead of
 *  a blank window. Pages are full Astro navigations, so the error state
 *  clears on the next page load. */
export class ErrorBoundary extends React.Component<
  { children: React.ReactNode },
  { failed: boolean }
> {
  state = { failed: false }

  static getDerivedStateFromError(): { failed: boolean } {
    return { failed: true }
  }

  componentDidCatch(error: unknown): void {
    console.error("render error:", error)
  }

  render(): React.ReactNode {
    if (!this.state.failed) return this.props.children
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center p-8">
        <Empty>
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <TriangleAlert />
            </EmptyMedia>
            <EmptyTitle>{t("error.title")}</EmptyTitle>
            <EmptyDescription>{t("error.desc")}</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button onClick={() => window.location.reload()}>{t("error.reload")}</Button>
          </EmptyContent>
        </Empty>
      </div>
    )
  }
}
