import { test, expect } from "bun:test"
import { Store } from "./store"

test("Store stores, updates and notifies subscribers", () => {
  const s = new Store<number>(0)
  let seen: number[] = []
  const unsub = s.subscribe((v) => seen.push(v))
  s.set(1)
  s.update((x) => x + 2)
  expect(s.get()).toBe(3)
  expect(seen).toEqual([0, 1, 3])
  unsub()
  s.set(9)
  expect(seen).toEqual([0, 1, 3])
})
