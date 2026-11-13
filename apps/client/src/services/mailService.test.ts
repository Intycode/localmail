import { describe, expect, test } from "bun:test"

import type { Thread } from "../types"
import { mergeFirstPage, THREAD_PAGE } from "./mailService"

const th = (i: number): Thread =>
  ({ id: `t${i}`, date: new Date(Date.UTC(2026, 0, 1) - i * 60_000).toISOString() }) as Thread
const range = (from: number, to: number) =>
  Array.from({ length: to - from }, (_, k) => th(from + k))

describe("mergeFirstPage", () => {
  test("a short view is replaced as a whole", () => {
    expect(mergeFirstPage([th(0)], range(0, 5))).toEqual([th(0)])
  })

  test("a refresh keeps the older pages loaded below the first one", () => {
    const loaded = range(0, THREAD_PAGE * 2)
    // one new message on top: the fresh first page is shifted by one
    const fresh = [th(-1), ...range(0, THREAD_PAGE - 1)]
    const merged = mergeFirstPage(fresh, loaded)
    expect(merged.length).toBe(THREAD_PAGE * 2 + 1)
    expect(new Set(merged.map((t) => t.id)).size).toBe(merged.length)
    expect(merged[0].id).toBe("t-1")
    expect(merged.at(-1)?.id).toBe(`t${THREAD_PAGE * 2 - 1}`)
  })
})
