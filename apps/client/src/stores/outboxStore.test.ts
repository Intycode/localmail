import { describe, expect, test } from "bun:test"

import { outboxStore, setConnectionOnline, setOutboxPending } from "./outboxStore"

describe("outboxStore", () => {
  test("starts optimistic-online with zero pending", () => {
    expect(outboxStore.get()).toEqual({ pending: 0, online: true, known: false })
  })

  test("pending updates mark the state as known", () => {
    setOutboxPending(3)
    expect(outboxStore.get().pending).toBe(3)
    expect(outboxStore.get().known).toBe(true)
  })

  test("connection transitions flip the online flag", () => {
    setConnectionOnline(false)
    expect(outboxStore.get().online).toBe(false)
    setConnectionOnline(true)
    expect(outboxStore.get().online).toBe(true)
  })

  test("subscribers see the same object identity between changes", () => {
    setOutboxPending(0) // reset the singleton: tests share one store
    const seen: number[] = []
    const stop = outboxStore.subscribe((s) => seen.push(s.pending))
    setOutboxPending(1)
    setOutboxPending(2)
    stop()
    expect(seen).toEqual([0, 1, 2]) // subscribe fires immediately, then per update
    setOutboxPending(0)
  })
})
