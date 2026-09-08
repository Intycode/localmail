/**
 * lib/onboarding — first-run wizard trigger.
 *
 * The wizard opens once, on the very first launch of the app, when no
 * account has been connected yet. Pure logic lives in
 * `shouldShowOnboarding` so the trigger is unit-testable without a window
 * or the Tauri runtime.
 */

import { onboardingDone } from "./settings"

export interface OnboardingTrigger {
  /** Tauri runtime only: in the plain browser the wizard would hijack dev. */
  isTauri: boolean
  /** accounts have been hydrated from the engine at least once */
  loaded: boolean
  /** number of connected accounts */
  accountCount: number
}

/**
 * Should the first-run wizard open now? True only on the very first launch
 * (flag never set) with zero accounts. Once any account exists — or the
 * user skipped the wizard — `onboardingDone()` is true and this never
 * fires again.
 */
export function shouldShowOnboarding(t: OnboardingTrigger): boolean {
  return t.isTauri && t.loaded && t.accountCount === 0 && !onboardingDone()
}
