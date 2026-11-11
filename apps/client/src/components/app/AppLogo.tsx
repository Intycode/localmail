import * as React from "react"

import { cn } from "@/lib/utils"

/** App mark — inline mirror of src-tauri/icons/source-icon.svg (fixed brand
 *  colors: the logo does not follow the accent setting). */
export function AppLogo({ className }: { className?: string }): React.JSX.Element {
  // Gradient ids must be unique per instance; strip useId's punctuation for url(#…).
  const id = React.useId().replace(/[^\w-]/g, "")
  return (
    <svg viewBox="0 0 256 256" fill="none" aria-hidden className={cn("shrink-0", className)}>
      <defs>
        <linearGradient
          id={`${id}bg`}
          x1="0"
          y1="0"
          x2="256"
          y2="256"
          gradientUnits="userSpaceOnUse"
        >
          <stop offset="0" stopColor="#2a74ee" />
          <stop offset="1" stopColor="#00c0de" />
        </linearGradient>
        <linearGradient
          id={`${id}env`}
          x1="0"
          y1="68"
          x2="0"
          y2="188"
          gradientUnits="userSpaceOnUse"
        >
          <stop offset="0" stopColor="#ffffff" />
          <stop offset="1" stopColor="#eef5ff" />
        </linearGradient>
        <clipPath id={`${id}clip`}>
          <rect x="38" y="68" width="180" height="120" rx="14" />
        </clipPath>
      </defs>
      <rect width="256" height="256" rx="60" fill={`url(#${id}bg)`} />
      <rect x="38" y="68" width="180" height="120" rx="14" fill={`url(#${id}env)`} />
      <path d="M38 68h180l-90 70z" fill="#d6e6ff" clipPath={`url(#${id}clip)`} />
      <path
        d="M46 76l82 62 82-62"
        stroke="#2f74e8"
        strokeWidth="7"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}
